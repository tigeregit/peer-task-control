package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tigeregit/peer-task-control/internal/catalog"
	"github.com/tigeregit/peer-task-control/internal/deploy"
	"github.com/tigeregit/peer-task-control/internal/dispatch"
	"github.com/tigeregit/peer-task-control/internal/version"
)

// fakePtc is a shell script standing in for a ptc binary of version v.
func fakePtc(v string) []byte {
	index := "---\nname: peer-task-control\nmetadata:\n  version: \"" + v + "\"\n---\n# skill " + v + "\n"
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"version) echo " + v + ";;\n" +
		"skill)\n" +
		"  if [ \"$2\" = \"sync\" ]; then\n" +
		"    dir=\"${HOME}/.agents/skills/peer-task-control\"\n" +
		"    mkdir -p \"$dir\"\n" +
		"    printf '%s' '" + index + "' > \"$dir/SKILL.md\"\n" +
		"    exit 0\n" +
		"  fi\n" +
		"  echo '# skill " + v + "';;\n" +
		"esac\n"
	return []byte(script)
}

func makeManaged(t *testing.T, n *Node) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "bin", "ptc")
	_ = os.MkdirAll(filepath.Dir(exe), 0o755)
	if err := os.WriteFile(exe, fakePtc("0.3.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := deploy.Save(n.Cfg.DataDir, deploy.Install{Version: "0.3.0", Binary: exe, DataDir: n.Cfg.DataDir, Service: deploy.ServiceNone}); err != nil {
		t.Fatal(err)
	}
	n.upd.mu.Lock()
	n.upd.managed = true
	n.upd.exe = exe
	n.upd.mu.Unlock()
	n.bumpLocal()
	n.Catalog.Put(n.LocalEntry())
	n.broadcast()
	return exe
}

// The initiator stages a binary from an offline release dir, a managed
// target pulls it over bitswap, replaces itself (keeping .prev), refreshes
// the user-scope skill and asks for a restart; a node with accept_remote off refuses;
// an unmanaged node is skipped.
func TestMeshUpdate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell-script stand-in binary")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	oldWait := updateRejoinWait
	updateRejoinWait = time.Second
	defer func() { updateRejoinWait = oldWait }()
	cidrs := []string{"127.0.0.0/8"}
	ini := startTestNode(t, "initiator", "127.0.0.1", false, cidrs)
	tgt := startTestNode(t, "target", "127.0.0.2", false, cidrs)
	off := startTestNode(t, "refuser", "127.0.0.3", false, cidrs)
	plain := startTestNode(t, "unmanaged", "127.0.0.4", false, cidrs)
	later := startTestNode(t, "z-later", "127.0.0.5", false, cidrs)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, n := range []*Node{tgt, off, plain, later} {
		if err := ini.Probe(ctx, listenHostPort(n)); err != nil {
			t.Fatal(err)
		}
	}
	exe := makeManaged(t, tgt)
	makeManaged(t, off)
	laterExe := makeManaged(t, later)
	if err := off.SetRemoteUpdatePolicy(false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		a, _ := ini.Catalog.Get(tgt.Host.ID().String())
		b, _ := ini.Catalog.Get(off.Host.ID().String())
		c, _ := ini.Catalog.Get(later.Host.ID().String())
		if a.Managed && b.Managed && !b.AcceptUpdate && c.Managed {
			break
		}
		_ = ini.exchange(ctx, tgt.Host.ID())
		_ = ini.exchange(ctx, off.Host.ID())
		_ = ini.exchange(ctx, later.Host.ID())
		time.Sleep(200 * time.Millisecond)
	}

	dir := t.TempDir()
	bin := fakePtc("0.3.1")
	asset, err := deploy.BinaryName(runtime.GOOS + "/" + runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	sum := sha256.Sum256(bin)
	_ = os.WriteFile(filepath.Join(dir, asset), bin, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(hex.EncodeToString(sum[:])+"  "+asset+"\n"), 0o644)

	plan, err := ini.MeshUpdate(ctx, MeshUpdateRequest{Version: "0.3.1", AssetsDir: dir, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	st := map[string]MeshUpdateNode{}
	for _, r := range plan.Nodes {
		st[r.Name] = r
	}
	if st["target"].Status != "planned" || st["unmanaged"].Status != "skipped" || st["refuser"].Status != "skipped" {
		t.Fatalf("plan: %+v", plan.Nodes)
	}

	r, err := ini.sendUpdate(ctx, mustEntry(t, ini, off), "0.3.1", nil)
	if err != nil || r.OK || !strings.Contains(r.Error, "accept_remote") {
		t.Fatalf("refuser answered %+v, %v", r, err)
	}

	rep, err := ini.MeshUpdate(ctx, MeshUpdateRequest{Version: "0.3.1", AssetsDir: dir, Nodes: []string{"target", "z-later"}, Batch: 1})
	if err != nil {
		t.Fatal(err)
	}
	var got MeshUpdateNode
	for _, r := range rep.Nodes {
		if r.Name == "target" {
			got = r
		}
		if r.Name == "z-later" && (r.Status != "skipped" || !strings.Contains(r.Detail, "earlier stage failed")) {
			t.Fatalf("later stage was not stopped: %+v", r)
		}
	}
	if got.Status != "failed" || !strings.Contains(got.Detail, "not yet seen back") {
		t.Fatalf("target: %+v", got)
	}
	if b, _ := os.ReadFile(laterExe); string(b) != string(fakePtc("0.3.0")) {
		t.Fatal("later stage replaced a binary after rejoin failure")
	}
	b, _ := os.ReadFile(exe)
	if string(b) != string(bin) {
		t.Fatal("target binary not replaced")
	}
	if p, _ := os.ReadFile(deploy.PrevPath(exe)); string(p) != string(fakePtc("0.3.0")) {
		t.Fatal("previous binary not kept")
	}
	skillPath := filepath.Join(home, ".agents", "skills", "peer-task-control", "SKILL.md")
	if s, err := os.ReadFile(skillPath); err != nil || !strings.Contains(string(s), "0.3.1") {
		t.Fatalf("skill not refreshed: %q %v", s, err)
	}
	if in, _ := deploy.Load(tgt.Cfg.DataDir); in.Version != "0.3.1" || in.Previous != version.Version {
		t.Fatalf("install.json: %+v (previous want %s)", in, version.Version)
	}
	select {
	case <-tgt.RestartRequested():
	case <-time.After(5 * time.Second):
		t.Fatal("no restart requested")
	}
	for _, n := range []*Node{ini, tgt} {
		_ = filepath.Walk(n.Cfg.CASDir(), func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() {
				t.Errorf("%s kept update blob %s", n.Cfg.Name, p)
			}
			return nil
		})
	}
	t.Logf("target updated in %s from %v at %.1f MiB/s", got.Took, got.Providers, got.MiBs)
}

func TestMeshUpdateSelectsOnlyRequestedNodes(t *testing.T) {
	n := startTestNode(t, "initiator", "127.0.0.1", false, []string{"127.0.0.0/8"})
	for _, name := range []string{"a-selected", "z-unselected"} {
		n.Catalog.Put(catalog.Entry{PeerID: name, Name: name, Version: version.Version, Managed: true, AcceptUpdate: true})
	}
	rep, err := n.MeshUpdate(context.Background(), MeshUpdateRequest{Version: "0.3.1", Nodes: []string{"a-selected"}, DryRun: true})
	if err != nil || len(rep.Nodes) != 1 || rep.Nodes[0].Name != "a-selected" {
		t.Fatalf("selection expanded beyond requested node: %+v, %v", rep, err)
	}
}

func TestUpdateAndRestartPreserveQueuedJobs(t *testing.T) {
	n := startTestNode(t, "busy-worker", "127.0.0.1", false, []string{"127.0.0.0/8"})
	makeManaged(t, n)
	n.mu.Lock()
	err := n.queue.Enqueue(&dispatch.Job{JobID: "busy-job", RunID: "busy-run"}, []byte("bundle"))
	n.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := n.RequestRestart(); err == nil {
		t.Fatal("restart accepted with a queued job")
	}
	res := n.applyRemoteUpdate(n.Host.ID(), updateMessage{Type: "update", Version: "0.3.1",
		Assets: map[string]UpdateAsset{platformKey(runtime.GOOS, runtime.GOARCH): {Digest: "sha256:" + strings.Repeat("a", 64)}}})
	if res.OK || !strings.Contains(res.Error, "drain the queue") || n.updating() {
		t.Fatalf("busy update: %+v", res)
	}
	n.mu.Lock()
	_, err = n.queue.Cancel("busy-job")
	n.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	n.upd.mu.Lock()
	n.upd.busy = true
	n.upd.mu.Unlock()
	if err := n.RequestRestart(); err == nil {
		t.Fatal("restart interrupted an in-flight update")
	}
	n.upd.mu.Lock()
	n.upd.busy = false
	n.upd.mu.Unlock()
	if err := n.RequestRestart(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := n.acceptBundle(slowBundle(t), dispatch.Request{}, n.Host.ID().String(), nil); err == nil ||
		!strings.Contains(err.Error(), "restarting") {
		t.Fatalf("new task admitted during restart: %v", err)
	}
}

func mustEntry(t *testing.T, n *Node, other *Node) catalog.Entry {
	t.Helper()
	e, ok := n.Catalog.Get(other.Host.ID().String())
	if !ok {
		t.Fatal("peer not in catalog")
	}
	return e
}
