package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	cid "github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	manet "github.com/multiformats/go-multiaddr/net"

	"github.com/tigeregit/peer-task-control/internal/blocks"
	"github.com/tigeregit/peer-task-control/internal/cas"
	"github.com/tigeregit/peer-task-control/internal/resource"
)

// ErrNoCID means no holder advertises a bitswap root for the seed (for
// example an older ptc), so the caller should use the tar stream.
var ErrNoCID = errors.New("no holder advertises a bitswap CID for this seed")

// dataPlane is the node's bitswap state.
type dataPlane struct {
	store *blocks.Store
	x     *blocks.Exchange
	// mu: pulls hold RLock from fetch to re-index; GC holds Lock.
	mu       sync.RWMutex
	kick     chan struct{}
	vmu      sync.Mutex
	verified map[string]time.Time // digest -> last time its DAG was checked complete
	gcKey    string
	pulls    sync.Map // digest -> *sync.Mutex
}

func (n *Node) startDataPlane() {
	if n.Cfg.Transfer.Mode == "tar" {
		n.Log.Info("bitswap data plane disabled (transfer.mode: tar)")
		return
	}
	st, err := blocks.Open(n.Cfg.SeedsDir())
	if err != nil {
		n.Log.Warn("bitswap data plane not started; seeds use the tar stream", "err", err)
		return
	}
	dp := &dataPlane{store: st, kick: make(chan struct{}, 1), verified: map[string]time.Time{}}
	dp.x = blocks.NewExchange(n.ctx, n.Host, st, n.allowBlocks)
	n.dp = dp
	go n.indexLoop()
}

func (n *Node) closeDataPlane() {
	if n.dp == nil {
		return
	}
	_ = n.dp.x.Close()
	_ = n.dp.store.Close()
}

// allowBlocks applies the files service switch and allow/deny policy to
// every bitswap block request. The connection gater already limits peers to
// trusted CIDRs.
func (n *Node) allowBlocks(p peer.ID) bool {
	name := ""
	if e, ok := n.Catalog.Get(p.String()); ok {
		name = e.Name
	}
	var ip []byte
	for _, c := range n.Host.Network().ConnsToPeer(p) {
		if v, err := manet.ToIP(c.RemoteMultiaddr()); err == nil {
			ip = v
			break
		}
	}
	return n.Store.Check("files", p.String(), name, ip).Allow
}

// KickIndex asks the indexer to pick up new or removed seeds now.
func (n *Node) KickIndex() {
	if n.dp == nil {
		return
	}
	select {
	case n.dp.kick <- struct{}{}:
	default:
	}
}

func (n *Node) indexLoop() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		n.indexOnce(n.ctx)
		select {
		case <-n.ctx.Done():
			return
		case <-t.C:
		case <-n.dp.kick:
		}
	}
}

// indexOnce maps every local seed to a UnixFS root (importing in place
// where needed) and garbage-collects blocks no local seed references.
func (n *Node) indexOnce(ctx context.Context) {
	dp := n.dp
	seeds := resource.InventoryAt(n.Cfg.PackagesDir(), n.Cfg.CASDir())
	var roots []cid.Cid
	changed := false
	for _, s := range seeds {
		if ctx.Err() != nil {
			return
		}
		kind, path, _, ok := resource.FindByDigest(n.Cfg.PackagesDir(), n.Cfg.CASDir(), s.Digest)
		if !ok {
			continue
		}
		if c, err := cid.Decode(s.CID); err == nil {
			if t, ok := dp.lastVerified(s.Digest); ok && time.Since(t) < 10*time.Minute {
				roots = append(roots, c)
				continue
			}
			if dp.store.Complete(ctx, c) {
				dp.markVerified(s.Digest)
				roots = append(roots, c)
				continue
			}
		}
		t0 := time.Now()
		var c cid.Cid
		var err error
		if kind == "tree" {
			c, err = dp.store.ImportTree(ctx, path)
		} else {
			c, err = dp.store.ImportFile(ctx, path)
		}
		if err != nil {
			n.Log.Warn("index seed for bitswap failed", "digest", s.Digest, "err", err)
			continue
		}
		if kind == "tree" {
			err = resource.SetTreeCID(path, c.String())
		} else {
			err = resource.SetBlobCID(path, c.String())
		}
		if err != nil {
			n.Log.Warn("record seed CID failed", "digest", s.Digest, "err", err)
		}
		dp.markVerified(s.Digest)
		roots = append(roots, c)
		changed = true
		n.Log.Info("indexed seed for bitswap", "name", s.Name, "digest", s.Digest, "cid", c.String(), "files", s.FileCount, "bytes", s.Size, "took", time.Since(t0).Round(time.Millisecond))
	}
	keys := make([]string, 0, len(roots))
	for _, r := range roots {
		keys = append(keys, r.String())
	}
	sort.Strings(keys)
	key := strings.Join(keys, ",")
	if key != dp.gcKey {
		dp.mu.Lock()
		st, err := dp.store.GC(ctx, roots)
		dp.mu.Unlock()
		if err != nil {
			n.Log.Warn("block GC failed", "err", err)
		} else {
			dp.gcKey = key
			if st.Blocks+st.Refs > 0 {
				n.Log.Info("block GC", "blocks", st.Blocks, "filestore_refs", st.Refs, "freed_bytes", st.FreedBytes, "roots", st.KeptRoots)
			}
		}
	}
	if changed {
		n.bumpLocal()
	}
}

// PullResult reports one bitswap pull.
type PullResult struct {
	Digest     string              `json:"digest"`
	CID        string              `json:"cid"`
	Name       string              `json:"name,omitempty"`
	Kind       string              `json:"kind"`
	Path       string              `json:"path"`
	Holders    []string            `json:"holders"`
	Fetch      blocks.FetchStats   `json:"fetch"`
	Total      time.Duration       `json:"total_ns"`
	AlreadyHad bool                `json:"already_local,omitempty"`
	Providers  []resource.Location `json:"-"`
}

// PullSeed fetches a seed from any holders with bitswap, verifies its
// digest, stores it as a temporary replica (packages/replica-* or cas/) and
// indexes it in place, so the node can then serve it too.
func (n *Node) PullSeed(ctx context.Context, digest string, log io.Writer) (PullResult, error) {
	if log == nil {
		log = io.Discard
	}
	digest = resource.Normalize(digest)
	res := PullResult{Digest: digest}
	if digest == "" {
		return res, fmt.Errorf("invalid digest")
	}
	if kind, path, s, ok := resource.FindByDigest(n.Cfg.PackagesDir(), n.Cfg.CASDir(), digest); ok {
		res.Kind, res.Path, res.Name, res.CID, res.AlreadyHad = kind, path, s.Name, s.CID, true
		return res, nil
	}
	if n.dp == nil {
		return res, fmt.Errorf("bitswap data plane is not running on this node")
	}
	lk, _ := n.dp.pulls.LoadOrStore(digest, &sync.Mutex{})
	mu := lk.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	if kind, path, s, ok := resource.FindByDigest(n.Cfg.PackagesDir(), n.Cfg.CASDir(), digest); ok {
		res.Kind, res.Path, res.Name, res.CID, res.AlreadyHad = kind, path, s.Name, s.CID, true
		return res, nil
	}
	t0 := time.Now()
	info, err := n.Resource(ctx, digest)
	if err != nil {
		return res, err
	}
	res.Name, res.Kind = info.Name, info.Kind
	if info.CID == "" {
		return res, ErrNoCID
	}
	root, err := cid.Decode(info.CID)
	if err != nil {
		return res, fmt.Errorf("bad CID %q: %w", info.CID, err)
	}
	res.CID = root.String()
	locs := append([]resource.Location(nil), info.Locations...)
	sort.SliceStable(locs, func(i, j int) bool { return locs[i].Hops < locs[j].Hops })
	var holders []peer.ID
	for _, l := range locs {
		if l.Local || l.PeerID == "" || l.PeerID == n.Host.ID().String() {
			continue
		}
		if pid, err := peer.Decode(l.PeerID); err == nil {
			holders = append(holders, pid)
			res.Holders = append(res.Holders, firstNonEmpty(l.Node, l.PeerID))
		}
	}
	if len(holders) == 0 {
		return res, fmt.Errorf("seed %s has no reachable holder", digest)
	}
	// Reach every holder first, through a relay if needed; bitswap itself only
	// dials addresses already in the peerstore.
	var wg sync.WaitGroup
	for _, pid := range holders {
		wg.Add(1)
		go func(pid peer.ID) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if err := n.ensureConnected(cctx, pid); err != nil {
				n.Log.Info("bitswap holder unreachable", "peer", pid.String(), "err", err)
			}
		}(pid)
	}
	wg.Wait()
	n.dp.mu.RLock()
	defer n.dp.mu.RUnlock()
	fmt.Fprintf(log, "bitswap: fetching %s (%s) from %s\n", digest, root, strings.Join(res.Holders, ", "))
	st, err := n.dp.x.Fetch(ctx, root, holders)
	res.Fetch = st
	if err != nil {
		return res, err
	}
	for i := range st.Providers {
		if e, ok := n.Catalog.Get(st.Providers[i].Peer); ok && e.Name != "" {
			res.Fetch.Providers[i].Peer = e.Name
		}
	}
	if err := os.MkdirAll(n.Cfg.PullTmpDir(), 0o755); err != nil {
		return res, err
	}
	stage := filepath.Join(n.Cfg.PullTmpDir(), fmt.Sprintf("%s-%d", resource.Hex(digest)[:16], time.Now().UnixNano()))
	defer os.RemoveAll(stage)
	if err := n.dp.x.Export(ctx, root, stage); err != nil {
		return res, fmt.Errorf("export %s: %w", root, err)
	}
	var local cid.Cid
	if info.Kind == "tree" {
		got, ents, err := cas.DirDigest(stage)
		if err != nil {
			return res, err
		}
		if got != digest {
			return res, fmt.Errorf("digest mismatch for %s: bitswap tree hashes to %s", digest, got)
		}
		pkg := filepath.Join(n.Cfg.PackagesDir(), "replica-"+resource.Hex(digest)[:16])
		_ = os.RemoveAll(pkg)
		if err := os.MkdirAll(filepath.Dir(pkg), 0o755); err != nil {
			return res, err
		}
		if err := os.Rename(stage, pkg); err != nil {
			if err := cas.CopyTree(stage, pkg); err != nil {
				return res, err
			}
		}
		created := time.Now()
		if info.CreatedAt != nil {
			created = *info.CreatedAt
		}
		if _, err := resource.CommitVerifiedTree(pkg, info.Name, digest, ents, created, resource.CommitOpts{Format: "unixfs", RunOK: info.RunOK, CID: root.String(), Replica: true}); err != nil {
			return res, err
		}
		local, err = n.dp.store.ImportTree(ctx, pkg)
		if err != nil {
			return res, fmt.Errorf("index replica: %w", err)
		}
		res.Path = pkg
		if !local.Equals(root) {
			_ = resource.SetTreeCID(pkg, local.String())
		}
	} else {
		sum, err := fileSum(stage)
		if err != nil {
			return res, err
		}
		if sum != digest {
			return res, fmt.Errorf("digest mismatch for %s: bitswap blob hashes to %s", digest, sum)
		}
		store := cas.New(n.Cfg.CASDir())
		dst := store.Path(digest)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return res, err
		}
		if err := os.Rename(stage, dst); err != nil {
			if _, err := store.PutFile(stage); err != nil {
				return res, err
			}
		}
		local, err = n.dp.store.ImportFile(ctx, dst)
		if err != nil {
			return res, fmt.Errorf("index replica: %w", err)
		}
		_ = resource.SetBlobCID(dst, local.String())
		res.Path = dst
	}
	if !local.Equals(root) {
		fmt.Fprintf(log, "bitswap: local re-index gives %s (holder advertised %s); serving the local root\n", local, root)
		res.CID = local.String()
	}
	n.dp.markVerified(digest)
	res.Total = time.Since(t0)
	parts := make([]string, 0, len(res.Fetch.Providers))
	for _, p := range res.Fetch.Providers {
		parts = append(parts, fmt.Sprintf("%s %d blocks / %d bytes", p.Peer, p.Blocks, p.Bytes))
	}
	fmt.Fprintf(log, "bitswap: %s received %d bytes in %d blocks in %s (%.1f MiB/s; providers: %s); verified and stored at %s\n",
		digest, res.Fetch.Bytes, res.Fetch.Blocks, res.Fetch.Duration.Round(time.Millisecond), res.Fetch.MBPerSec, strings.Join(parts, "; "), res.Path)
	n.Log.Info("bitswap pull", "digest", digest, "cid", res.CID, "bytes", res.Fetch.Bytes, "blocks", res.Fetch.Blocks, "took", res.Fetch.Duration.Round(time.Millisecond), "mib_s", fmt.Sprintf("%.1f", res.Fetch.MBPerSec), "providers", strings.Join(parts, "; "))
	n.bumpLocal()
	n.KickIndex()
	return res, nil
}

func fileSum(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.CopyBuffer(h, f, make([]byte, 1<<20)); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// BlocksUsage is the on-disk size of the block store (DAG nodes and copies;
// filestore data is counted under the seeds it belongs to).
func (n *Node) BlocksUsage() int64 {
	if n.dp == nil {
		return 0
	}
	return n.dp.store.Usage()
}

func (dp *dataPlane) lastVerified(d string) (time.Time, bool) {
	dp.vmu.Lock()
	defer dp.vmu.Unlock()
	t, ok := dp.verified[d]
	return t, ok
}

func (dp *dataPlane) markVerified(d string) {
	dp.vmu.Lock()
	dp.verified[d] = time.Now()
	dp.vmu.Unlock()
}

// Pull is the API form of PullSeed.
func (n *Node) Pull(ctx context.Context, digest string) (any, error) {
	return n.PullSeed(ctx, digest, nil)
}
