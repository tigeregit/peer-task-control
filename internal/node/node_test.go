package node

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/peer-task-control/ptc/internal/catalog"
	"github.com/peer-task-control/ptc/internal/config"
	"github.com/peer-task-control/ptc/internal/control"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func canBind(ip string) bool {
	l, err := net.Listen("tcp", net.JoinHostPort(ip, "0"))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

func startTestNode(t *testing.T, name, listenIP string, relay bool, cidrs []string) *Node {
	t.Helper()
	if !canBind(listenIP) {
		t.Skipf("cannot bind %s", listenIP)
	}
	dir := t.TempDir()
	lp := freePort(t)
	ap := freePort(t)
	cfg := config.Defaults()
	cfg.Name = name
	cfg.Listen = net.JoinHostPort(listenIP, fmt.Sprintf("%d", lp))
	cfg.API = net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", ap))
	cfg.DataDir = dir
	cfg.Path = filepath.Join(dir, "config.yaml")
	cfg.TrustedCIDRs = cidrs
	cfg.Services.Relay = relay
	cfg.Services.Exec = true
	cfg.Services.Files = true
	cfg.DisableMDNS = true
	cfg.ScanInterval = 24 * time.Hour
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	n, err := Start(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Close() })
	return n
}

func listenHostPort(n *Node) string {
	return n.Cfg.Listen
}

func findPeer(list []catalog.Entry, name string) (catalog.Entry, bool) {
	for _, e := range list {
		if e.Name == name {
			return e, true
		}
	}
	return catalog.Entry{}, false
}

func waitPeers(t *testing.T, n *Node, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(n.Peers()) >= want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d peers, have %+v", want, n.Peers())
}

func waitService(t *testing.T, n *Node, name, service string, enabled bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if e, ok := findPeer(n.Peers(), name); ok {
			if st, ok := e.Services[service]; ok && st.Enabled == enabled {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s.%s enabled=%v in %+v", name, service, enabled, n.Peers())
}

func TestGatewayScanPushesCatalog(t *testing.T) {
	cidrs := []string{"127.0.0.1/32", "127.0.0.2/32", "127.0.0.3/32"}
	a := startTestNode(t, "node-a", "127.0.0.1", false, cidrs)
	b := startTestNode(t, "node-b", "127.0.0.2", false, cidrs)
	gw := startTestNode(t, "gateway", "127.0.0.3", true, cidrs)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := gw.Probe(ctx, listenHostPort(a)); err != nil {
		t.Fatalf("gateway probe a: %v", err)
	}
	if err := gw.Probe(ctx, listenHostPort(b)); err != nil {
		t.Fatalf("gateway probe b: %v", err)
	}

	waitPeers(t, a, 3)
	waitPeers(t, b, 3)
	waitPeers(t, gw, 3)

	if _, ok := findPeer(a.Peers(), "node-b"); !ok {
		t.Fatalf("node-a catalog missing node-b: %+v", a.Peers())
	}
	if _, ok := findPeer(b.Peers(), "node-a"); !ok {
		t.Fatalf("node-b catalog missing node-a: %+v", b.Peers())
	}

	local, ok := findPeer(a.Peers(), "node-a")
	if !ok {
		t.Fatal("self missing")
	}
	st := local.Services["exec"]
	if st.Offered {
		t.Fatal("exec should not be offered without an NVIDIA GPU")
	}
	if st.Reason == "" {
		t.Fatal("disabled exec should include a reason")
	}

	cli := control.NewClient(a.APIAddr())
	stt, err := cli.Status()
	if err != nil {
		t.Fatal(err)
	}
	if stt.Name != "node-a" || len(stt.Peers) != 3 {
		t.Fatalf("api status: %+v", stt)
	}

	on := false
	if _, err := cli.ApplyPolicy(control.PolicyChange{Op: "service", Service: "files", On: &on}); err != nil {
		t.Fatal(err)
	}
	waitService(t, gw, "node-a", "files", false)
}

func TestRejectOutsideTrustedCIDR(t *testing.T) {
	a := startTestNode(t, "inside", "127.0.0.1", false, []string{"127.0.0.1/32"})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := a.Probe(ctx, "8.8.8.8:18765"); err == nil {
		t.Fatal("expected probe to 8.8.8.8 to be rejected")
	}
}

func TestPolicyDenyReason(t *testing.T) {
	n := startTestNode(t, "pol", "127.0.0.1", false, []string{"127.0.0.0/8"})
	if err := n.Store.Add("deny", "evil"); err != nil {
		t.Fatal(err)
	}
	d := n.Store.Check("files", "", "evil", net.ParseIP("127.0.0.1"))
	if d.Allow || d.Reason == "" {
		t.Fatalf("expected deny with reason, got %+v", d)
	}
}
