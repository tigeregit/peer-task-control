package blocks

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/ipfs/boxo/bitswap"
	bsmsg "github.com/ipfs/boxo/bitswap/message"
	"github.com/ipfs/boxo/bitswap/network/bsnet"
	"github.com/ipfs/boxo/bitswap/server"
	"github.com/ipfs/boxo/blockservice"
	"github.com/ipfs/boxo/ipld/merkledag"
	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// ProtocolPrefix keeps ptc bitswap separate from public IPFS bitswap:
// streams are /ptc/ipfs/bitswap/1.2.0 etc. and only reach trusted mesh peers.
const ProtocolPrefix protocol.ID = "/ptc"

// FetchConcurrency is how many DAG nodes are requested in parallel.
const FetchConcurrency = 64

// Exchange is bitswap on the ptc host, fed with providers from the trusted
// mesh inventory (no DHT).
type Exchange struct {
	Store *Store
	host  *meshHost
	bsw   *bitswap.Bitswap
	dag   ipld.DAGService
	prov  *providers
	tr    *recvTracer
}

// Allow decides whether a peer may download blocks from this node.
type Allow func(peer.ID) bool

// NewExchange starts bitswap on h. allow is checked for every block request.
func NewExchange(ctx context.Context, h host.Host, st *Store, allow Allow) *Exchange {
	mh := newMeshHost(h)
	net := bsnet.NewFromIpfsHost(mh, bsnet.Prefix(ProtocolPrefix))
	prov := &providers{active: map[int]map[peer.ID]struct{}{}}
	tr := &recvTracer{bytes: map[peer.ID]int64{}, blocks: map[peer.ID]int{}}
	bsw := bitswap.New(ctx, net, prov, st.Blockstore(),
		bitswap.WithTracer(tr),
		bitswap.WithPeerBlockRequestFilter(func(p peer.ID, _ cid.Cid) bool { return allow == nil || allow(p) }),
		bitswap.WithServerOption(server.MaxOutstandingBytesPerPeer(32<<20)),
		bitswap.WithServerOption(server.EngineTaskWorkerCount(16)),
	)
	dag := merkledag.NewDAGService(blockservice.New(st.Blockstore(), bsw))
	return &Exchange{Store: st, host: mh, bsw: bsw, dag: dag, prov: prov, tr: tr}
}

func (x *Exchange) Close() error {
	if x == nil || x.bsw == nil {
		return nil
	}
	return x.bsw.Close()
}

// PeerBytes is how much one provider sent during a fetch.
type PeerBytes struct {
	Peer   string `json:"peer"`
	Blocks int    `json:"blocks"`
	Bytes  int64  `json:"bytes"`
}

// FetchStats summarises one Fetch.
type FetchStats struct {
	Root       string        `json:"cid"`
	Duration   time.Duration `json:"duration_ns"`
	Bytes      int64         `json:"bytes_received"`
	Blocks     int           `json:"blocks_received"`
	Providers  []PeerBytes   `json:"providers"`
	MBPerSec   float64       `json:"mb_per_sec"`
	AlreadyHad bool          `json:"already_local,omitempty"`
}

// Fetch pulls every block of the DAG under root into the local store, asking
// holders (and any other connected trusted peer that has the blocks).
func (x *Exchange) Fetch(ctx context.Context, root cid.Cid, holders []peer.ID) (FetchStats, error) {
	st := FetchStats{Root: root.String()}
	if x.Store.Complete(ctx, root) {
		st.AlreadyHad = true
		return st, nil
	}
	x.Store.mu.RLock()
	defer x.Store.mu.RUnlock()
	id := x.prov.add(holders)
	defer x.prov.remove(id)
	var wg sync.WaitGroup
	for _, p := range holders {
		if p == x.host.ID() {
			continue
		}
		wg.Add(1)
		go func(p peer.ID) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			_ = x.host.Connect(cctx, peer.AddrInfo{ID: p})
		}(p)
	}
	wg.Wait()
	before := x.tr.snapshot()
	t0 := time.Now()
	err := merkledag.FetchGraph(ctx, root, x.dag, merkledag.Concurrency(FetchConcurrency))
	st.Duration = time.Since(t0)
	after := x.tr.snapshot()
	for p, b := range after.bytes {
		d := b - before.bytes[p]
		if d <= 0 {
			continue
		}
		st.Bytes += d
		nb := after.blocks[p] - before.blocks[p]
		st.Blocks += nb
		st.Providers = append(st.Providers, PeerBytes{Peer: p.String(), Bytes: d, Blocks: nb})
	}
	sort.Slice(st.Providers, func(i, j int) bool { return st.Providers[i].Bytes > st.Providers[j].Bytes })
	if s := st.Duration.Seconds(); s > 0 {
		st.MBPerSec = float64(st.Bytes) / (1 << 20) / s
	}
	if err != nil {
		return st, fmt.Errorf("bitswap fetch %s: %w", root, err)
	}
	return st, nil
}

// Export writes a fetched DAG to dest.
func (x *Exchange) Export(ctx context.Context, root cid.Cid, dest string) error {
	return x.Store.Export(ctx, root, dest)
}

// providers answers bitswap provider queries from the mesh inventory: the
// holders of every fetch in progress.
type providers struct {
	mu     sync.Mutex
	next   int
	active map[int]map[peer.ID]struct{}
}

func (p *providers) add(ids []peer.ID) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.next++
	set := map[peer.ID]struct{}{}
	for _, id := range ids {
		set[id] = struct{}{}
	}
	p.active[p.next] = set
	return p.next
}

func (p *providers) remove(id int) {
	p.mu.Lock()
	delete(p.active, id)
	p.mu.Unlock()
}

func (p *providers) FindProvidersAsync(ctx context.Context, _ cid.Cid, max int) <-chan peer.AddrInfo {
	p.mu.Lock()
	seen := map[peer.ID]struct{}{}
	var out []peer.AddrInfo
	for _, set := range p.active {
		for id := range set {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, peer.AddrInfo{ID: id})
		}
	}
	p.mu.Unlock()
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	ch := make(chan peer.AddrInfo, len(out))
	for _, ai := range out {
		ch <- ai
	}
	close(ch)
	return ch
}

type recvTracer struct {
	mu     sync.Mutex
	bytes  map[peer.ID]int64
	blocks map[peer.ID]int
}

type recvSnap struct {
	bytes  map[peer.ID]int64
	blocks map[peer.ID]int
}

func (t *recvTracer) MessageReceived(p peer.ID, m bsmsg.BitSwapMessage) {
	bl := m.Blocks()
	if len(bl) == 0 {
		return
	}
	t.mu.Lock()
	for _, b := range bl {
		t.bytes[p] += int64(len(b.RawData()))
		t.blocks[p]++
	}
	t.mu.Unlock()
}

func (t *recvTracer) MessageSent(peer.ID, bsmsg.BitSwapMessage) {}

func (t *recvTracer) snapshot() recvSnap {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := recvSnap{bytes: map[peer.ID]int64{}, blocks: map[peer.ID]int{}}
	for k, v := range t.bytes {
		s.bytes[k] = v
	}
	for k, v := range t.blocks {
		s.blocks[k] = v
	}
	return s
}
