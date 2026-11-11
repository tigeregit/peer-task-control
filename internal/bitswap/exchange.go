// Package bitswap transfers resource transport blocks using the standard IPFS
// Bitswap protocols. Resource discovery remains on the trusted PTC mesh.
package bitswap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"time"

	boxo "github.com/ipfs/boxo/bitswap"
	bsmsg "github.com/ipfs/boxo/bitswap/message"
	bsnetwork "github.com/ipfs/boxo/bitswap/network"
	"github.com/ipfs/boxo/bitswap/network/bsnet"
	blocks "github.com/ipfs/go-block-format"
	"github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const (
	ChunkSize       = 256 << 10
	maxBlockSize    = 1 << 20
	maxLegacyChunks = 10000
)

// Manifest describes a PTC blob or tar transport, not a UnixFS DAG. Its CID and
// all chunk and index CIDs are raw SHA-256 CIDs, usable by a standard Bitswap client.
type Manifest struct {
	Version int      `json:"version"`
	Digest  string   `json:"digest"`
	Kind    string   `json:"kind"`
	Size    int64    `json:"size"`
	Chunks  []string `json:"chunks"`
	Index   string   `json:"index,omitempty"`
	Height  int      `json:"height,omitempty"`
}

type Exchange struct {
	store  *Store
	swap   *boxo.Bitswap
	events *bsnetwork.ConnectEventManager
}

// circuitHost opts only Bitswap's streams into the existing circuit-v2 policy.
// The underlying host's connection gater still enforces the trusted network.
type circuitHost struct{ host.Host }

func (h circuitHost) NewStream(ctx context.Context, p peer.ID, ps ...protocol.ID) (network.Stream, error) {
	return h.Host.NewStream(network.WithAllowLimitedConn(ctx, "ptc-bitswap"), p, ps...)
}

func (h circuitHost) Connect(ctx context.Context, p peer.AddrInfo) error {
	return h.Host.Connect(network.WithAllowLimitedConn(ctx, "ptc-bitswap"), p)
}

type circuitNetwork struct {
	bsnetwork.BitSwapNetwork
	host      host.Host
	authorize func(peer.ID) bool
}

func (n circuitNetwork) SendMessage(ctx context.Context, p peer.ID, m bsmsg.BitSwapMessage) error {
	// Recheck at send time too: wants accepted before a policy change may be
	// queued, or fulfilled later by NotifyNewBlocks.
	if n.authorize != nil && (len(m.Blocks()) != 0 || len(m.Haves()) != 0) && !n.authorize(p) {
		return fmt.Errorf("files policy denies Bitswap response to %s", p)
	}
	return n.BitSwapNetwork.SendMessage(ctx, p, m)
}

func (n circuitNetwork) IsConnectedToPeer(_ context.Context, p peer.ID) bool {
	c := n.host.Network().Connectedness(p)
	return c == network.Connected || c == network.Limited
}

func New(ctx context.Context, h host.Host, root string, authorize func(peer.ID) bool) (*Exchange, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	e := &Exchange{store: &Store{root: root}, events: bsnetwork.NewConnectEventManager()}
	net := circuitNetwork{
		BitSwapNetwork: bsnet.NewFromIpfsHost(circuitHost{h}, bsnet.WithConnectEventManager(e.events)),
		host:           h, authorize: authorize,
	}
	// No public DHT/provider lookup: discovery and dialing belong to the PTC mesh.
	e.swap = boxo.New(ctx, net, nil, e.store, boxo.WithPeerBlockRequestFilter(func(p peer.ID, _ cid.Cid) bool {
		return authorize == nil || authorize(p)
	}))
	return e, nil
}

func (e *Exchange) Close() error { return e.swap.Close() }

// PeerReady includes limited relay connections ignored by Boxo's default
// connection notifier. Call after the mesh has established a trusted path.
func (e *Exchange) PeerReady(p peer.ID) { e.events.Connected(p) }

func readBlock(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxBlockSize+1))
	if err == nil && len(b) > maxBlockSize {
		err = fmt.Errorf("block exceeds %d bytes", maxBlockSize)
	}
	return b, err
}

func (e *Exchange) put(ctx context.Context, data []byte) (cid.Cid, error) {
	c, err := (cid.Prefix{Version: 1, Codec: cid.Raw, MhType: 0x12, MhLength: 32}).Sum(data)
	if err != nil {
		return cid.Undef, err
	}
	b, err := blocks.NewBlockWithCid(bytes.Clone(data), c)
	if err == nil {
		err = e.store.Put(ctx, b)
	}
	if err == nil {
		err = e.swap.NotifyNewBlocks(ctx, b)
	}
	return c, err
}

func (e *Exchange) GetBlock(ctx context.Context, c cid.Cid) (blocks.Block, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := checkCID(c); err != nil {
		return nil, err
	}
	if b, err := e.store.Get(ctx, c); err == nil {
		return b, nil
	} else if !ipld.IsNotFound(err) {
		return nil, err
	}
	b, err := e.swap.GetBlock(ctx, c)
	if err != nil {
		return nil, err
	}
	if len(b.RawData()) > maxBlockSize {
		return nil, fmt.Errorf("block too large")
	}
	if err := verify(c, b.RawData()); err != nil {
		return nil, err
	}
	// Boxo's client does not persist fetched blocks itself.
	if err := e.store.Put(ctx, b); err != nil {
		return nil, err
	}
	if err := e.swap.NotifyNewBlocks(ctx, b); err != nil {
		return nil, err
	}
	return b, nil
}

func (e *Exchange) Export(ctx context.Context, digest, kind string, r io.Reader) (cid.Cid, error) {
	m := Manifest{Version: 1, Digest: digest, Kind: kind}
	var tree *indexBuilder
	buf := make([]byte, ChunkSize)
	for {
		if err := ctx.Err(); err != nil {
			return cid.Undef, err
		}
		n, err := io.ReadFull(r, buf)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return cid.Undef, err
		}
		if n > 0 {
			if m.Size > math.MaxInt64-int64(n) {
				return cid.Undef, fmt.Errorf("resource size exceeds int64")
			}
			if tree == nil && len(m.Chunks) == maxLegacyChunks {
				tree = &indexBuilder{exchange: e}
				for _, ref := range m.Chunks {
					if err := tree.add(ctx, 0, indexLink{CID: ref, Size: ChunkSize}); err != nil {
						return cid.Undef, err
					}
				}
				m.Chunks = nil
				m.Version = 2
			}
			c, err := e.put(ctx, buf[:n])
			if err != nil {
				return cid.Undef, err
			}
			if tree == nil {
				m.Chunks = append(m.Chunks, c.String())
			} else if err := tree.add(ctx, 0, indexLink{CID: c.String(), Size: int64(n)}); err != nil {
				return cid.Undef, err
			}
			m.Size += int64(n)
		}
		if err != nil {
			break
		}
	}
	if tree != nil {
		root, height, err := tree.finish(ctx)
		if err != nil {
			return cid.Undef, err
		}
		m.Index, m.Height = root.CID, height
	}
	data, err := json.Marshal(m)
	if err != nil {
		return cid.Undef, err
	}
	return e.put(ctx, data)
}

func (e *Exchange) Fetch(ctx context.Context, root cid.Cid, digest string, w io.Writer) (Manifest, error) {
	b, err := e.GetBlock(ctx, root)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(b.RawData(), &m); err != nil {
		return m, err
	}
	if m.Digest != digest || (m.Kind != "blob" && m.Kind != "tree") || m.Size < 0 {
		return m, fmt.Errorf("invalid resource transport manifest")
	}
	switch m.Version {
	case 2:
		if len(m.Chunks) != 0 || m.Index == "" || m.Height < 0 || m.Height > maxIndexHeight ||
			m.Size <= 0 || m.Size > indexCapacity(m.Height) {
			return m, fmt.Errorf("invalid resource transport index")
		}
		return m, e.fetchIndex(ctx, indexLink{CID: m.Index, Size: m.Size}, m.Height, w)
	case 1:
	default:
		return m, fmt.Errorf("unsupported resource transport manifest version %d", m.Version)
	}
	if m.Index != "" || m.Height != 0 || len(m.Chunks) > maxLegacyChunks || m.Size > int64(len(m.Chunks))*ChunkSize ||
		(len(m.Chunks) > 0 && m.Size <= int64(len(m.Chunks)-1)*ChunkSize) {
		return m, fmt.Errorf("invalid resource transport manifest")
	}
	for i, ref := range m.Chunks {
		want := int64(ChunkSize)
		if i == len(m.Chunks)-1 {
			want = m.Size - int64(i)*ChunkSize
		}
		if err := e.writeChunk(ctx, indexLink{CID: ref, Size: want}, w); err != nil {
			return m, err
		}
	}
	return m, nil
}
