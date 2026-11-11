package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	manet "github.com/multiformats/go-multiaddr/net"
	multistream "github.com/multiformats/go-multistream"

	"github.com/tigeregit/peer-task-control/internal/cas"
	"github.com/tigeregit/peer-task-control/internal/policy"
	"github.com/tigeregit/peer-task-control/internal/resource"
)

// Only root discovery is PTC-specific; all payload blocks use IPFS Bitswap.
const ProtocolResourceBlocks protocol.ID = "/ptc/resource-blocks/1.0.0"

func (n *Node) resourcePeerDecision(p peer.ID) policy.Decision {
	name := ""
	if e, ok := n.Catalog.Get(p.String()); ok {
		name = e.Name
	}
	conns := n.Host.Network().ConnsToPeer(p)
	if len(conns) == 0 {
		return n.Store.Check("files", p.String(), name, nil)
	}
	for _, c := range conns {
		var ip net.IP
		// A relay's IP is not the requester's IP and must not grant an
		// unrelated peer access through an IP allow-list entry.
		if !isCircuit(c.RemoteMultiaddr()) {
			ip, _ = manet.ToIP(c.RemoteMultiaddr())
		}
		if d := n.Store.Check("files", p.String(), name, ip); !d.Allow {
			return d
		}
	}
	return policy.Decision{Allow: true}
}

func (n *Node) allowResourcePeer(p peer.ID) bool {
	return n.resourcePeerDecision(p).Allow
}

func isLegacyResourcePeer(err error) bool {
	var unsupported multistream.ErrNotSupported[protocol.ID]
	return errors.As(err, &unsupported)
}

func (n *Node) handleResourceBlocks(s network.Stream) {
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(30 * time.Minute))
	ctx, cancel := context.WithTimeout(n.ctx, 30*time.Minute)
	defer cancel()
	msg, _, err := readResMsg(io.LimitReader(s, 64<<10))
	if err != nil || resource.Normalize(msg.Digest) == "" {
		return
	}
	if d := n.resourcePeerDecision(s.Conn().RemotePeer()); !d.Allow {
		_ = writeResMsg(s, resourcesMessage{Error: "policy refused files: " + d.Reason})
		return
	}
	blob, err := n.openLocal(msg.Digest)
	if err != nil {
		_ = writeResMsg(s, resourcesMessage{Error: err.Error()})
		return
	}
	defer blob.Body.Close()
	n.blocks.PeerReady(s.Conn().RemotePeer())
	root, err := n.blocks.Export(ctx, blob.Digest, blob.Kind, blob.Body)
	if err != nil {
		_ = writeResMsg(s, resourcesMessage{Error: err.Error()})
		return
	}
	n.recordSeedCID(blob.Digest, blob.Kind, root.String())
	if d := n.resourcePeerDecision(s.Conn().RemotePeer()); !d.Allow {
		_ = writeResMsg(s, resourcesMessage{Error: "policy refused files: " + d.Reason})
		return
	}
	_ = writeResMsg(s, resourcesMessage{Type: "bitswap", Digest: blob.Digest, Root: root.String(), Name: blob.Name})
}

func (n *Node) fetchBlocks(ctx context.Context, id, digest string) (resource.Blob, error) {
	blob, _, _, err := n.fetchBlocksDetailed(ctx, id, digest)
	return blob, err
}

type resourceFile struct{ *os.File }

func (f *resourceFile) Close() error {
	err := f.File.Close()
	_ = os.Remove(f.Name())
	return err
}

func verifyResourceTransport(r io.Reader, dataDir, digest, kind string) error {
	if kind == "blob" {
		h := sha256.New()
		if _, err := io.Copy(h, r); err != nil {
			return err
		}
		if "sha256:"+hex.EncodeToString(h.Sum(nil)) != digest {
			return fmt.Errorf("resource blob digest mismatch")
		}
		return nil
	}
	stage, err := os.MkdirTemp(dataDir, ".verify-tree-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if _, _, err := cas.ExtractTar(r, stage, 0); err != nil {
		return err
	}
	got, _, err := cas.DirDigest(stage)
	if err != nil {
		return err
	}
	if got != digest {
		var legacy struct {
			Files map[string]string `json:"files"`
			Unit  string            `json:"unit"`
		}
		manifest := filepath.Join(stage, "manifest.json")
		if data, err := os.ReadFile(manifest); err == nil &&
			json.Unmarshal(data, &legacy) == nil && legacy.Files != nil && legacy.Unit != "directory" {
			// Pre-directory-unit packages hash a list of file digests, with
			// the package manifest added only after computing the root.
			if err := os.Remove(manifest); err != nil {
				return err
			}
			if old, _, err := cas.RootDigest(stage); err == nil && old == digest {
				return nil
			}
		}
		return fmt.Errorf("resource tree digest mismatch: expected %s, got %s", digest, got)
	}
	return nil
}
