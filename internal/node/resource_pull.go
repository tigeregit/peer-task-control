package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/tigeregit/peer-task-control/internal/bitswap"
	"github.com/tigeregit/peer-task-control/internal/cas"
	"github.com/tigeregit/peer-task-control/internal/resource"
)

// PullProvider is one holder's share of a bitswap pull.
type PullProvider struct {
	Peer   string `json:"peer"`
	Blocks int    `json:"blocks"`
	Bytes  int64  `json:"bytes"`
}

// PullResult reports one bitswap pull into this node's store.
type PullResult struct {
	Digest     string             `json:"digest"`
	CID        string             `json:"cid"`
	Name       string             `json:"name,omitempty"`
	Kind       string             `json:"kind"`
	Path       string             `json:"path"`
	Holders    []string           `json:"holders"`
	AlreadyHad bool               `json:"already_local,omitempty"`
	Total      time.Duration      `json:"total_ns"`
	Fetch      bitswap.FetchStats `json:"fetch"`
	Providers  []PullProvider     `json:"-"`
}

// PullSeed fetches a seed from mesh holders with bitswap, verifies its
// digest, and stores it as a temporary replica (packages/replica-* or cas/)
// so this node can serve it too. A replica can be removed with `ptc clean resource`.
func (n *Node) PullSeed(ctx context.Context, digest string, log io.Writer) (PullResult, error) {
	if log == nil {
		log = io.Discard
	}
	if ctx == nil {
		ctx = n.ctx
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
	if n.blocks == nil {
		return res, fmt.Errorf("bitswap is not running on this node")
	}
	lk, _ := n.pulls.LoadOrStore(digest, &sync.Mutex{})
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
	locs := append([]resource.Location(nil), info.Locations...)
	sort.SliceStable(locs, func(i, j int) bool {
		a, b := locs[i], locs[j]
		return a.Hops < b.Hops || (a.Hops == b.Hops && a.Connected && !b.Connected)
	})
	var last error
	for _, loc := range locs {
		if loc.Local || loc.PeerID == "" || loc.PeerID == n.Host.ID().String() {
			continue
		}
		holder := firstNonEmpty(loc.Node, loc.PeerID)
		res.Holders = append(res.Holders, holder)
		fmt.Fprintf(log, "bitswap: fetching %s from %s\n", digest, holder)
		blob, root, st, err := n.fetchBlocksDetailed(ctx, loc.PeerID, digest)
		if err != nil {
			last = err
			n.Log.Info("bitswap holder failed", "peer", holder, "digest", digest, "err", err)
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			continue
		}
		res.CID, res.Fetch = root, st
		if e, ok := n.Catalog.Get(loc.PeerID); ok && e.Name != "" {
			holder = e.Name
		}
		res.Providers = []PullProvider{{Peer: holder, Blocks: st.Blocks, Bytes: st.Bytes}}
		res.Fetch.Providers = []bitswap.PeerBytes{{Peer: holder, Blocks: st.Blocks, Bytes: st.Bytes}}
		path, err := n.commitReplica(blob, info, root)
		_ = blob.Body.Close()
		if err != nil {
			return res, err
		}
		res.Path = path
		res.Kind = blob.Kind
		if res.Name == "" {
			res.Name = blob.Name
		}
		res.Total = time.Since(t0)
		fmt.Fprintf(log, "bitswap: %s received %d bytes in %d blocks in %s (%.1f MiB/s from %s); verified and stored at %s\n",
			digest, st.Bytes, st.Blocks, st.Duration.Round(time.Millisecond), st.MBPerSec, holder, res.Path)
		n.Log.Info("bitswap pull", "digest", digest, "cid", res.CID, "bytes", st.Bytes, "blocks", st.Blocks, "took", st.Duration.Round(time.Millisecond), "mib_s", fmt.Sprintf("%.1f", st.MBPerSec), "from", holder)
		n.bumpLocal()
		return res, nil
	}
	if last != nil {
		return res, fmt.Errorf("seed %s: all replicas failed: %w", digest, last)
	}
	return res, fmt.Errorf("seed %s has no reachable holder", digest)
}

func (n *Node) commitReplica(blob resource.Blob, info resource.Info, root string) (string, error) {
	if err := os.MkdirAll(n.Cfg.FetchTmpDir(), 0o755); err != nil {
		return "", err
	}
	stage := filepath.Join(n.Cfg.FetchTmpDir(), fmt.Sprintf("pull-%s-%d", resource.Hex(info.Digest)[:16], time.Now().UnixNano()))
	defer os.RemoveAll(stage)
	if blob.Kind == "tree" {
		if err := os.MkdirAll(stage, 0o755); err != nil {
			return "", err
		}
		if _, _, err := cas.ExtractTar(blob.Body, stage, 0); err != nil {
			return "", fmt.Errorf("unpack %s: %w", info.Digest, err)
		}
		got, ents, err := cas.DirDigest(stage)
		if err != nil {
			return "", err
		}
		if got != info.Digest {
			return "", fmt.Errorf("digest mismatch for %s: bitswap tree hashes to %s", info.Digest, got)
		}
		pkg := filepath.Join(n.Cfg.PackagesDir(), "replica-"+resource.Hex(info.Digest)[:16])
		_ = os.RemoveAll(pkg)
		if err := os.MkdirAll(filepath.Dir(pkg), 0o755); err != nil {
			return "", err
		}
		if err := os.Rename(stage, pkg); err != nil {
			if err := cas.CopyTree(stage, pkg); err != nil {
				return "", err
			}
		}
		created := time.Now()
		if info.CreatedAt != nil {
			created = *info.CreatedAt
		}
		if _, err := resource.CommitVerifiedTree(pkg, info.Name, info.Digest, ents, created, resource.CommitOpts{Format: "unixfs", RunOK: info.RunOK, CID: root, Replica: true}); err != nil {
			return "", err
		}
		return pkg, nil
	}
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return "", err
	}
	tmp := filepath.Join(stage, "blob")
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, cerr := io.Copy(io.MultiWriter(f, h), blob.Body)
	if err := f.Close(); cerr == nil {
		cerr = err
	}
	if cerr != nil {
		return "", cerr
	}
	if got := "sha256:" + hex.EncodeToString(h.Sum(nil)); got != info.Digest {
		return "", fmt.Errorf("digest mismatch for %s: bitswap blob hashes to %s", info.Digest, got)
	}
	store := cas.New(n.Cfg.CASDir())
	if _, err := store.PutFile(tmp); err != nil {
		return "", err
	}
	dst := store.Path(info.Digest)
	if root != "" {
		_ = resource.SetBlobCID(dst, root)
	}
	return dst, nil
}

// Pull is the API form of PullSeed.
func (n *Node) Pull(ctx context.Context, digest string) (any, error) {
	return n.PullSeed(ctx, digest, nil)
}

func (n *Node) recordSeedCID(digest, kind, c string) {
	if c == "" {
		return
	}
	digest = resource.Normalize(digest)
	if kind, path, s, ok := resource.FindByDigest(n.Cfg.PackagesDir(), n.Cfg.CASDir(), digest); ok {
		if s.CID == c {
			return
		}
		var err error
		if kind == "tree" {
			err = resource.SetTreeCID(path, c)
		} else {
			err = resource.SetBlobCID(path, c)
		}
		if err != nil {
			n.Log.Warn("record seed CID failed", "digest", digest, "err", err)
		}
	}
}

// indexLocalCIDs exports local seeds that do not yet have a transport CID,
// so the resource board can show it and peers can compare holders.
func (n *Node) indexLocalCIDs() {
	if n.blocks == nil {
		return
	}
	for _, s := range resource.InventoryAt(n.Cfg.PackagesDir(), n.Cfg.CASDir()) {
		if n.ctx.Err() != nil {
			return
		}
		if s.CID != "" {
			continue
		}
		blob, err := n.openLocal(s.Digest)
		if err != nil {
			continue
		}
		root, err := n.blocks.Export(n.ctx, blob.Digest, blob.Kind, blob.Body)
		_ = blob.Body.Close()
		if err != nil {
			n.Log.Debug("index seed CID failed", "digest", s.Digest, "err", err)
			continue
		}
		n.recordSeedCID(s.Digest, blob.Kind, root.String())
		n.Log.Info("indexed seed for bitswap", "name", s.Name, "digest", s.Digest, "cid", root.String(), "files", s.FileCount, "bytes", s.Size)
	}
	n.bumpLocal()
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// fetchBlocksDetailed is fetchBlocks plus the transport root CID and stats.
func (n *Node) fetchBlocksDetailed(ctx context.Context, id, digest string) (resource.Blob, string, bitswap.FetchStats, error) {
	var st bitswap.FetchStats
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	pid, err := peer.Decode(id)
	if err != nil {
		return resource.Blob{}, "", st, err
	}
	s, err := n.openStream(ctx, pid, ProtocolResourceBlocks)
	if err != nil {
		return resource.Blob{}, "", st, err
	}
	defer s.Close()
	_ = s.SetDeadline(timeNowDeadline(ctx, 30*time.Minute))
	stop := context.AfterFunc(ctx, func() { _ = s.Reset() })
	defer stop()
	if err := writeResMsg(s, resourcesMessage{Type: "bitswap", Digest: digest}); err != nil {
		return resource.Blob{}, "", st, err
	}
	reply, _, err := readResMsg(io.LimitReader(s, 64<<10))
	if err != nil {
		return resource.Blob{}, "", st, err
	}
	if reply.Error != "" {
		return resource.Blob{}, "", st, fmt.Errorf("resource blocks: %s", reply.Error)
	}
	if reply.Type != "bitswap" || reply.Digest != digest {
		return resource.Blob{}, "", st, fmt.Errorf("invalid resource blocks response")
	}
	root, err := cid.Decode(reply.Root)
	if err != nil {
		return resource.Blob{}, "", st, err
	}
	n.blocks.PeerReady(pid)
	if err := os.MkdirAll(n.Cfg.FetchTmpDir(), 0o700); err != nil {
		return resource.Blob{}, "", st, err
	}
	f, err := os.CreateTemp(n.Cfg.FetchTmpDir(), ".resource-")
	if err != nil {
		return resource.Blob{}, "", st, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
			_ = os.Remove(f.Name())
		}
	}()
	m, st, err := n.blocks.FetchWithStats(ctx, root, digest, f)
	if err != nil {
		return resource.Blob{}, "", st, fmt.Errorf("bitswap fetch: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return resource.Blob{}, "", st, err
	}
	if err := verifyResourceTransport(f, n.Cfg.FetchTmpDir(), digest, m.Kind); err != nil {
		return resource.Blob{}, "", st, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return resource.Blob{}, "", st, err
	}
	ct := "application/octet-stream"
	if m.Kind == "tree" {
		ct = "application/x-tar"
	}
	name := reply.Name
	if name == "" {
		name = resource.Filename(resource.Seed{Digest: digest, Kind: m.Kind})
	}
	ok = true
	return resource.Blob{Digest: digest, Name: name, Kind: m.Kind, Size: m.Size, ContentType: ct, Body: &resourceFile{File: f}}, root.String(), st, nil
}
