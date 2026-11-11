package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tigeregit/peer-task-control/internal/catalog"
	"github.com/tigeregit/peer-task-control/internal/release"
	"github.com/tigeregit/peer-task-control/internal/selfupdate"
)

// fakePtc is a shell script standing in for a ptc binary of version v.
func fakePtc(v string) []byte {
	return []byte(fmt.Sprintf("#!/bin/sh\ncase \"$1\" in version) echo %s;; skill) echo '# skill %s';; esac\n", v, v))
}

func makeManaged(t *testing.T, n *Node) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "bin", "ptc")
	_ = os.MkdirAll(filepath.Dir(exe), 0o755)
	if err := os.WriteFile(exe, fakePtc("0.3.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := selfupdate.Save(n.Cfg.DataDir, selfupdate.Install{Version: "0.3.0", Binary: exe, DataDir: n.Cfg.DataDir, Service: selfupdate.ServiceNone}); err != nil {
		t.Fatal(err)
	}
	n.upd.mu.Lock()
	n.upd.managed = true
	n.upd.mu.Unlock()
	n.bumpLocal()
	n.Catalog.Put(n.LocalEntry())
	n.broadcast()
	return exe
}

// The initiator stages a binary from an offline release dir, a managed
// target pulls it over bitswap, replaces itself (keeping .prev), refreshes
// SKILL.md and asks for a restart; a node with accept_remote off refuses;
// an unmanaged node is skipped.
func TestMeshUpdate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell-script stand-in binary")
	}
	updateRejoinWait = time.Second
	cidrs := []string{"127.0.0.0/8"}
	ini := startTestNode(t, "initiator", "127.0.0.1", false, cidrs)
	tgt := startTestNode(t, "target", "127.0.0.2", false, cidrs)
	off := startTestNode(t, "refuser", "127.0.0.3", false, cidrs)
	plain := startTestNode(t, "unmanaged", "127.0.0.4", false, cidrs)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, n := range []*Node{tgt, off, plain} {
		if err := ini.Probe(ctx, listenHostPort(n)); err != nil {
			t.Fatal(err)
		}
	}
	exe := makeManaged(t, tgt)
	makeManaged(t, off)
	if err := off.SetRemoteUpdatePolicy(false); err != nil {
		t.Fatal(err)
	}
	// Wait for the initiator to see the managed flags.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		a, _ := ini.Catalog.Get(tgt.Host.ID().String())
		b, _ := ini.Catalog.Get(off.Host.ID().String())
		if a.Managed && b.Managed && !b.AcceptUpdate {
			break
		}
		_ = ini.exchange(ctx, tgt.Host.ID())
		_ = ini.exchange(ctx, off.Host.ID())
		time.Sleep(200 * time.Millisecond)
	}

	dir := t.TempDir()
	bin := fakePtc("0.3.1")
	asset := release.AssetName(runtime.GOOS, runtime.GOARCH)
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

	// Ask the refuser directly too: its own policy must say no.
	r, err := ini.sendUpdate(ctx, mustEntry(t, ini, off), "0.3.1", nil)
	if err != nil || r.OK || !strings.Contains(r.Error, "accept_remote") {
		t.Fatalf("refuser answered %+v, %v", r, err)
	}

	rep, err := ini.MeshUpdate(ctx, MeshUpdateRequest{Version: "0.3.1", AssetsDir: dir, Nodes: []string{"target"}})
	if err != nil {
		t.Fatal(err)
	}
	var got MeshUpdateNode
	for _, r := range rep.Nodes {
		if r.Name == "target" {
			got = r
		}
	}
	if got.Status != "updated" {
		t.Fatalf("target: %+v", got)
	}
	b, _ := os.ReadFile(exe)
	if string(b) != string(bin) {
		t.Fatal("target binary not replaced")
	}
	if p, _ := os.ReadFile(selfupdate.PrevPath(exe)); string(p) != string(fakePtc("0.3.0")) {
		t.Fatal("previous binary not kept")
	}
	if s, _ := os.ReadFile(filepath.Join(filepath.Dir(exe), "SKILL.md")); !strings.Contains(string(s), "0.3.1") {
		t.Fatalf("skill not refreshed: %q", s)
	}
	if in, _ := selfupdate.Load(tgt.Cfg.DataDir); in.Version != "0.3.1" || in.Previous != "0.3.0" {
		t.Fatalf("install.json: %+v", in)
	}
	select {
	case <-tgt.RestartRequested():
	case <-time.After(5 * time.Second):
		t.Fatal("no restart requested")
	}
	// Staged and pulled binaries do not linger as resources.
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

func mustEntry(t *testing.T, n *Node, other *Node) catalog.Entry {
	t.Helper()
	e, ok := n.Catalog.Get(other.Host.ID().String())
	if !ok {
		t.Fatal("peer not in catalog")
	}
	return e
}
