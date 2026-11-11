package node

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	manet "github.com/multiformats/go-multiaddr/net"

	"github.com/tigeregit/peer-task-control/internal/cas"
	"github.com/tigeregit/peer-task-control/internal/catalog"
	"github.com/tigeregit/peer-task-control/internal/control"
	"github.com/tigeregit/peer-task-control/internal/deploy"
	"github.com/tigeregit/peer-task-control/internal/resource"
	"github.com/tigeregit/peer-task-control/internal/version"
)

// ProtocolUpdate carries mesh update requests: the initiator names a
// version and the per-platform binary digests (already in its CAS);
// the target checks its policy, pulls its binary over bitswap, verifies,
// replaces itself and restarts.
const ProtocolUpdate protocol.ID = "/ptc/update/1.0.0"

const updateTimeout = 20 * time.Minute

// updateRejoinWait bounds how long a stage waits for its nodes to come back.
var updateRejoinWait = 2 * time.Minute

type updateMessage struct {
	Type    string                 `json:"type"` // "update" or "result"
	Version string                 `json:"version,omitempty"`
	Assets  map[string]UpdateAsset `json:"assets,omitempty"` // "os/arch" -> asset
	From    string                 `json:"from,omitempty"`
	// result
	OK          bool     `json:"ok,omitempty"`
	Error       string   `json:"error,omitempty"`
	FromVersion string   `json:"from_version,omitempty"`
	Providers   []string `json:"providers,omitempty"`
	Bytes       int64    `json:"bytes,omitempty"`
	MiBs        float64  `json:"mib_s,omitempty"`
	Note        string   `json:"note,omitempty"`
}

type updateState struct {
	mu      sync.Mutex
	exe     string // executable path resolved at start (before any replace)
	managed bool
	restart chan struct{}
	busy    bool
}

func (n *Node) initUpdate() {
	n.upd.restart = make(chan struct{}, 1)
	if exe, err := deploy.Exe(); err == nil {
		n.upd.exe = exe
	}
	_, n.upd.managed = deploy.Load(n.Cfg.DataDir)
}

func (n *Node) managed() bool {
	n.upd.mu.Lock()
	defer n.upd.mu.Unlock()
	return n.upd.managed
}

// RestartRequested fires when the daemon should re-exec itself (after an
// update replaced its binary). The daemon command owns the actual exec.
func (n *Node) RestartRequested() <-chan struct{} { return n.upd.restart }

// RequestRestart asks the daemon command to restart the process.
func (n *Node) RequestRestart() {
	if n.upd.restart == nil {
		return
	}
	select {
	case n.upd.restart <- struct{}{}:
	default:
	}
}

// RemoteUpdatePolicy is update.accept_remote.
func (n *Node) RemoteUpdatePolicy() bool { return n.Cfg.AcceptsRemoteUpdate() }

// SetRemoteUpdatePolicy changes and persists update.accept_remote.
func (n *Node) SetRemoteUpdatePolicy(on bool) error {
	v := on
	n.Cfg.Update.AcceptRemote = &v
	if err := n.Cfg.Save(); err != nil {
		return err
	}
	n.bumpLocal()
	n.Catalog.Put(n.LocalEntry())
	n.broadcast()
	return nil
}

func platformKey(goos, goarch string) string { return goos + "/" + goarch }

func (n *Node) handleUpdate(s network.Stream) {
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(updateTimeout))
	var req updateMessage
	if err := json.NewDecoder(io.LimitReader(s, 1<<20)).Decode(&req); err != nil {
		return
	}
	res := n.applyRemoteUpdate(s.Conn().RemotePeer(), req)
	res.Type = "result"
	res.FromVersion = version.Version
	_ = json.NewEncoder(s).Encode(res)
	if res.OK && res.Note == "" {
		go func() {
			time.Sleep(500 * time.Millisecond)
			n.RequestRestart()
		}()
	}
}

func (n *Node) applyRemoteUpdate(from peer.ID, req updateMessage) updateMessage {
	fail := func(f string, a ...any) updateMessage {
		msg := fmt.Sprintf(f, a...)
		n.Log.Warn("remote update refused", "from", from.String(), "version", req.Version, "reason", msg)
		return updateMessage{Error: msg}
	}
	name := ""
	if e, ok := n.Catalog.Get(from.String()); ok {
		name = e.Name
	}
	var ip []byte
	for _, c := range n.Host.Network().ConnsToPeer(from) {
		if isCircuit(c.RemoteMultiaddr()) {
			continue
		}
		if v, err := manet.ToIP(c.RemoteMultiaddr()); err == nil {
			ip = v
			break
		}
	}
	if d := n.Store.CheckPeer(from.String(), name, ip); !d.Allow {
		return fail("policy: %s", d.Reason)
	}
	if !n.Cfg.AcceptsRemoteUpdate() {
		return fail("update.accept_remote is off on this host")
	}
	if !version.Parse(req.Version).OK {
		return fail("bad version %q", req.Version)
	}
	if !version.SameMajor(version.Version, req.Version) {
		return fail("major version differs (%s -> %s); update this host locally with ptc update --version", version.Version, req.Version)
	}
	switch c := version.Compare(req.Version, version.Version); {
	case c == 0:
		return updateMessage{OK: true, Note: "already at " + version.Version}
	case c < 0:
		return fail("refusing downgrade %s -> %s (use a local rollback)", version.Version, req.Version)
	}
	inst, ok := deploy.Load(n.Cfg.DataDir)
	if !ok {
		return fail("not install-managed (no %s); install with ptc install --ssh or ptc update", deploy.ManifestPath(n.Cfg.DataDir))
	}
	exe := n.upd.exe
	if inst.Binary != "" {
		exe = inst.Binary
	}
	if exe == "" {
		return fail("cannot resolve own executable")
	}
	asset, ok := req.Assets[platformKey(runtime.GOOS, runtime.GOARCH)]
	if !ok {
		return fail("no binary offered for %s", platformKey(runtime.GOOS, runtime.GOARCH))
	}
	n.upd.mu.Lock()
	if n.upd.busy {
		n.upd.mu.Unlock()
		return fail("an update is already in progress")
	}
	n.upd.busy = true
	n.upd.mu.Unlock()
	defer func() { n.upd.mu.Lock(); n.upd.busy = false; n.upd.mu.Unlock() }()

	ctx, cancel := context.WithTimeout(n.ctx, updateTimeout-time.Minute)
	defer cancel()
	had := cas.New(n.Cfg.CASDir()).Has(asset.Digest)
	var logb bytes.Buffer
	pr, err := n.PullSeed(ctx, asset.Digest, &logb)
	if err != nil {
		return fail("pull %s %s: %v", asset.Name, asset.Digest, err)
	}
	bin, err := os.ReadFile(pr.Path)
	if err != nil {
		return fail("read pulled binary: %v", err)
	}
	if sum := sha256.Sum256(bin); "sha256:"+hex.EncodeToString(sum[:]) != asset.Digest {
		return fail("pulled binary does not match %s", asset.Digest)
	}
	if err := deploy.Replace(exe, bin); err != nil {
		return fail("replace %s: %v", exe, err)
	}
	if got, err := deploy.BinaryVersion(exe); err != nil || got != req.Version {
		_ = deploy.Rollback(exe)
		return fail("new binary reports version %q (%v), want %s; rolled back", got, err, req.Version)
	}
	if err := deploy.WriteSkill(exe); err != nil {
		n.Log.Warn("refresh skill copy failed", "err", err)
	}
	inst.Previous, inst.Version, inst.UpdatedAt = version.Version, req.Version, time.Now().UTC()
	_ = deploy.Save(n.Cfg.DataDir, inst)
	if !had {
		_ = os.Remove(resource.BlobCIDPath(pr.Path))
		_ = os.Remove(pr.Path)
	}
	out := updateMessage{OK: true, Bytes: pr.Fetch.Bytes, MiBs: pr.Fetch.MBPerSec}
	for _, p := range pr.Fetch.Providers {
		out.Providers = append(out.Providers, fmt.Sprintf("%s (%d blocks)", p.Peer, p.Blocks))
	}
	if pr.AlreadyHad {
		out.Providers = []string{"local CAS"}
	}
	n.Log.Info("remote update installed; restarting", "from", firstNonEmpty(name, from.String()), "old", version.Version, "new", req.Version, "bytes", pr.Fetch.Bytes, "mib_s", fmt.Sprintf("%.1f", pr.Fetch.MBPerSec))
	return out
}

// Mesh update types live in control so the API and CLI share them.
type (
	MeshUpdateRequest = control.MeshUpdateRequest
	MeshUpdateNode    = control.MeshUpdateNode
	MeshUpdateReport  = control.MeshUpdateReport
	UpdateAsset       = control.UpdateAsset
)

// MeshUpdate rolls a version out to eligible peers in stages. Binaries are
// staged in this node's CAS so targets pull them with bitswap (from this
// node and any peer that already has them), not from GitHub.
func (n *Node) MeshUpdate(ctx context.Context, req MeshUpdateRequest) (MeshUpdateReport, error) {
	target := strings.TrimPrefix(req.Version, "v")
	if target == "" {
		target = version.Version
	}
	if target == "latest" {
		if req.AssetsDir != "" {
			return MeshUpdateReport{}, fmt.Errorf("latest is not supported with --assets-dir; pass --version")
		}
		ver, err := deploy.ResolveVersion(ctx, "latest")
		if err != nil {
			return MeshUpdateReport{}, err
		}
		target = ver
	}
	if !version.Parse(target).OK {
		return MeshUpdateReport{}, fmt.Errorf("bad version %q", target)
	}
	rep := MeshUpdateReport{Version: target, Assets: map[string]UpdateAsset{}}
	want := map[string]bool{}
	for _, s := range req.Nodes {
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				want[p] = true
			}
		}
	}
	self := n.Host.ID().String()
	var todo []catalog.Entry
	for _, e := range n.Peers() {
		if e.PeerID == self {
			continue
		}
		if len(want) > 0 && !want[e.Name] && !want[e.PeerID] {
			continue
		}
		delete(want, e.Name)
		delete(want, e.PeerID)
		nr := MeshUpdateNode{Name: e.Name, PeerID: e.PeerID, Platform: platformKey(e.Caps.OS, e.Caps.Arch), From: e.Version}
		switch {
		case e.Version == "":
			nr.Status, nr.Detail = "skipped", "older than 0.3.0 (no update protocol); upgrade it with ptc install --ssh or locally"
		case e.Version == target:
			nr.Status, nr.Detail = "current", "already at "+target
		case !version.SameMajor(e.Version, target):
			nr.Status, nr.Detail = "skipped", "major version differs; update it locally"
		case version.Compare(target, e.Version) < 0:
			nr.Status, nr.Detail = "skipped", "node is newer than "+target
		case !e.Managed:
			nr.Status, nr.Detail = "skipped", "not install-managed (no install.json); use ptc install --ssh or ptc update"
		case !e.AcceptUpdate:
			nr.Status, nr.Detail = "skipped", "node refuses remote updates (update.accept_remote: false)"
		default:
			nr.Status, nr.To = "planned", target
			todo = append(todo, e)
		}
		rep.Nodes = append(rep.Nodes, nr)
	}
	for w := range want {
		rep.Nodes = append(rep.Nodes, MeshUpdateNode{Name: w, Status: "skipped", Detail: "not in the catalog"})
	}
	batch := req.Batch
	if batch <= 0 {
		batch = 1
	}
	stageOf := map[string]int{}
	for i, e := range todo {
		stageOf[e.PeerID] = i/batch + 1
	}
	for i := range rep.Nodes {
		rep.Nodes[i].Stage = stageOf[rep.Nodes[i].PeerID]
	}
	if len(todo) == 0 || req.DryRun {
		return rep, nil
	}
	if n.blocks == nil {
		return rep, fmt.Errorf("bitswap is not running on this node")
	}
	var staged []string
	defer func() {
		for _, p := range staged {
			_ = os.Remove(resource.BlobCIDPath(p))
			_ = os.Remove(p)
		}
	}()
	store := cas.New(n.Cfg.CASDir())
	for _, e := range todo {
		key := platformKey(e.Caps.OS, e.Caps.Arch)
		if _, ok := rep.Assets[key]; ok {
			continue
		}
		name, err := deploy.BinaryName(key)
		if err != nil {
			return rep, err
		}
		var bin []byte
		if target == version.Version && key == platformKey(runtime.GOOS, runtime.GOARCH) && n.upd.exe != "" && req.AssetsDir == "" {
			b, err := os.ReadFile(n.upd.exe)
			if err != nil {
				return rep, err
			}
			bin = b
		} else if req.AssetsDir != "" {
			art, err := deploy.FetchReleaseFromDir(req.AssetsDir, target, key)
			if err != nil {
				return rep, err
			}
			bin = art.Data
		} else {
			art, err := deploy.FetchRelease(ctx, target, key)
			if err != nil {
				return rep, err
			}
			bin = art.Data
		}
		tmp := filepath.Join(n.Cfg.FetchTmpDir(), fmt.Sprintf("update-%s-%d", strings.ReplaceAll(key, "/", "-"), time.Now().UnixNano()))
		if err := os.MkdirAll(filepath.Dir(tmp), 0o755); err != nil {
			return rep, err
		}
		if err := os.WriteFile(tmp, bin, 0o644); err != nil {
			return rep, err
		}
		sum := sha256.Sum256(bin)
		digest := "sha256:" + hex.EncodeToString(sum[:])
		pre := store.Has(digest)
		d, err := store.PutFile(tmp)
		_ = os.Remove(tmp)
		if err != nil {
			return rep, err
		}
		if !pre {
			staged = append(staged, store.Path(d))
		}
		rep.Assets[key] = UpdateAsset{Name: name, Digest: d, Size: int64(len(bin))}
	}
	n.bumpLocal()
	n.Catalog.Put(n.LocalEntry())

	idx := map[string]int{}
	for i, r := range rep.Nodes {
		idx[r.PeerID] = i
	}
	failed := false
	for start := 0; start < len(todo); start += batch {
		end := start + batch
		if end > len(todo) {
			end = len(todo)
		}
		if failed {
			for _, e := range todo[start:end] {
				rep.Nodes[idx[e.PeerID]].Status, rep.Nodes[idx[e.PeerID]].Detail = "skipped", "an earlier stage failed"
			}
			continue
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		for _, e := range todo[start:end] {
			wg.Add(1)
			go func(e catalog.Entry) {
				defer wg.Done()
				t0 := time.Now()
				r, err := n.sendUpdate(ctx, e, target, rep.Assets)
				mu.Lock()
				defer mu.Unlock()
				nr := &rep.Nodes[idx[e.PeerID]]
				nr.Took = time.Since(t0).Round(time.Millisecond).String()
				switch {
				case err != nil:
					nr.Status, nr.Detail, failed = "failed", err.Error(), true
				case !r.OK:
					nr.Status, nr.Detail, failed = "failed", r.Error, true
				default:
					nr.Status, nr.Providers, nr.MiBs, nr.Detail = "updated", r.Providers, r.MiBs, r.Note
				}
			}(e)
		}
		wg.Wait()
		for _, e := range todo[start:end] {
			nr := &rep.Nodes[idx[e.PeerID]]
			if nr.Status != "updated" || nr.Detail != "" {
				continue
			}
			if v, ok := n.waitVersion(ctx, e.PeerID, target, updateRejoinWait); ok {
				nr.Detail = "rejoined at " + v
			} else {
				nr.Detail = "installed; not yet seen back at " + target
				failed = true
			}
		}
	}
	sort.SliceStable(rep.Nodes, func(i, j int) bool { return rep.Nodes[i].Name < rep.Nodes[j].Name })
	return rep, nil
}

func (n *Node) sendUpdate(ctx context.Context, e catalog.Entry, ver string, assets map[string]UpdateAsset) (updateMessage, error) {
	pid, err := peer.Decode(e.PeerID)
	if err != nil {
		return updateMessage{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	s, err := n.openStream(cctx, pid, ProtocolUpdate)
	cancel()
	if err != nil {
		return updateMessage{}, err
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(updateTimeout))
	if err := json.NewEncoder(s).Encode(updateMessage{Type: "update", Version: ver, Assets: assets, From: n.Cfg.Name}); err != nil {
		return updateMessage{}, err
	}
	var r updateMessage
	if err := json.NewDecoder(io.LimitReader(s, 1<<20)).Decode(&r); err != nil {
		return updateMessage{}, fmt.Errorf("no reply: %w", err)
	}
	return r, nil
}

func (n *Node) waitVersion(ctx context.Context, peerID, ver string, d time.Duration) (string, bool) {
	deadline := time.Now().Add(d)
	pid, _ := peer.Decode(peerID)
	for time.Now().Before(deadline) {
		if e, ok := n.Catalog.Get(peerID); ok && e.Version == ver {
			return e.Version, true
		}
		if pid != "" {
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_ = n.ensureConnected(cctx, pid)
			cancel()
			cctx, cancel = context.WithTimeout(ctx, dialTimeout)
			_ = n.exchange(cctx, pid)
			cancel()
		}
		select {
		case <-ctx.Done():
			return "", false
		case <-time.After(2 * time.Second):
		}
	}
	return "", false
}
