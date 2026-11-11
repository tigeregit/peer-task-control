package node

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peerstore"

	"github.com/tigeregit/peer-task-control/internal/cas"
	"github.com/tigeregit/peer-task-control/internal/resource"
)

func makeSeedTree(t *testing.T, files, size int, big int) string {
	t.Helper()
	src := t.TempDir()
	for i := 0; i < files; i++ {
		p := filepath.Join(src, "corpus", fmt.Sprintf("s%02d", i%10), fmt.Sprintf("x%05d.dat", i))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		b := make([]byte, size)
		_, _ = rand.Read(b)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if big > 0 {
		b := make([]byte, big)
		_, _ = rand.Read(b)
		_ = os.MkdirAll(filepath.Join(src, "pkg"), 0o755)
		if err := os.WriteFile(filepath.Join(src, "pkg", "toolkit.bin"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

func seedCID(t *testing.T, n *Node, digest string) string {
	t.Helper()
	for _, s := range resource.InventoryAt(n.Cfg.PackagesDir(), n.Cfg.CASDir()) {
		if s.Digest == digest {
			return s.CID
		}
	}
	return ""
}

// Two holders, one fetcher: the fetcher pulls with bitswap, verifies the
// DirDigest, keeps a temporary replica and advertises the same CID.
func TestBitswapPullFromTwoHolders(t *testing.T) {
	cidrs := []string{"127.0.0.0/8"}
	a := startTestNode(t, "holder-a", "127.0.0.1", false, cidrs)
	b := startTestNode(t, "holder-b", "127.0.0.2", false, cidrs)
	gw := startTestNode(t, "gateway", "127.0.0.3", true, cidrs)
	c := startTestNode(t, "fetcher", "127.0.0.4", false, cidrs)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, n := range []*Node{a, b, c} {
		if err := gw.Probe(ctx, listenHostPort(n)); err != nil {
			t.Fatal(err)
		}
	}
	waitPeers(t, c, 4)

	src := makeSeedTree(t, 1000, 5000, 5<<20)
	var digest string
	for _, h := range []*Node{a, b} {
		s, err := resource.PublishTreeTo(h.Cfg.PackagesDir(), "corpus", src, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		digest = s.Digest
		h.indexOnce(ctx)
	}
	ca, cb := seedCID(t, a, digest), seedCID(t, b, digest)
	if ca == "" || ca != cb {
		t.Fatalf("holders advertise CIDs %q and %q", ca, cb)
	}
	res, err := c.PullSeed(ctx, digest, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("pull: %d bytes, %d blocks, %.1f MiB/s, providers %+v", res.Fetch.Bytes, res.Fetch.Blocks, res.Fetch.MBPerSec, res.Fetch.Providers)
	if len(res.Holders) != 2 {
		t.Fatalf("holders %v", res.Holders)
	}
	got, _, err := cas.DirDigest(res.Path)
	if err != nil || got != digest {
		t.Fatalf("replica digest %s want %s (%v)", got, digest, err)
	}
	inv := resource.InventoryAt(c.Cfg.PackagesDir(), c.Cfg.CASDir())
	if len(inv) != 1 || !inv[0].Replica || inv[0].CID != ca {
		t.Fatalf("fetcher inventory %+v", inv)
	}
	// Stored once: the flatfs side holds only DAG nodes, not the 10 MB of data.
	if u := c.BlocksUsage(); u > 3<<20 {
		t.Fatalf("block store holds %d bytes after pull", u)
	}
	// OpenResource for a seed that is local now must not touch the network.
	blob, err := c.OpenResource(ctx, digest)
	if err != nil {
		t.Fatal(err)
	}
	blob.Body.Close()

	// Remove the replica: the next index pass garbage-collects its blocks.
	if err := os.RemoveAll(res.Path); err != nil {
		t.Fatal(err)
	}
	c.indexOnce(ctx)
	if u := c.BlocksUsage(); u > 1<<20 {
		t.Fatalf("blocks left after GC: %d bytes", u)
	}
}

// A blob reaches the fetcher over a circuit relay only.
func TestBitswapPullOverRelay(t *testing.T) {
	cidrs := []string{"127.0.0.0/8"}
	a := startTestNode(t, "node-a", "127.0.0.1", false, cidrs)
	b := startTestNode(t, "node-b", "127.0.0.2", false, cidrs)
	gw := startTestNode(t, "gateway", "127.0.0.3", true, cidrs)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, n := range []*Node{a, b} {
		if err := gw.Probe(ctx, listenHostPort(n)); err != nil {
			t.Fatal(err)
		}
	}
	waitPeers(t, a, 3)
	waitPeers(t, b, 3)
	a.reserveWithRelays(ctx)
	b.reserveWithRelays(ctx)

	payload := filepath.Join(t.TempDir(), "pkg.bin")
	data := make([]byte, 9<<20+7)
	_, _ = rand.Read(data)
	_ = os.WriteFile(payload, data, 0o644)
	digest, err := cas.New(a.Cfg.CASDir()).PutFile(payload)
	if err != nil {
		t.Fatal(err)
	}
	a.indexOnce(ctx)
	if seedCID(t, a, digest) == "" {
		t.Fatal("blob not indexed")
	}

	aid := a.Host.ID()
	_ = b.Host.Network().ClosePeer(aid)
	b.Host.Peerstore().ClearAddrs(aid)
	circs := b.circuitAddrs(aid)
	if len(circs) == 0 {
		t.Fatal("no circuit addrs")
	}
	b.Host.Peerstore().AddAddrs(aid, circs, peerstore.TempAddrTTL)

	res, err := b.PullSeed(ctx, digest, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	relayed := false
	for _, cn := range b.Host.Network().ConnsToPeer(aid) {
		if isCircuit(cn.RemoteMultiaddr()) {
			relayed = true
		}
	}
	if !relayed {
		t.Fatal("expected the pull to use a relayed connection")
	}
	got, err := fileSum(res.Path)
	if err != nil || got != digest {
		t.Fatalf("blob %s want %s (%v)", got, digest, err)
	}
	t.Logf("relay pull: %d bytes in %s (%.1f MiB/s)", res.Fetch.Bytes, res.Fetch.Duration, res.Fetch.MBPerSec)
}

// files service off: holders refuse blocks.
func TestBitswapRespectsFilesSwitch(t *testing.T) {
	cidrs := []string{"127.0.0.0/8"}
	a := startTestNode(t, "node-a", "127.0.0.1", false, cidrs)
	b := startTestNode(t, "node-b", "127.0.0.2", false, cidrs)
	gw := startTestNode(t, "gateway", "127.0.0.3", true, cidrs)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, n := range []*Node{a, b} {
		if err := gw.Probe(ctx, listenHostPort(n)); err != nil {
			t.Fatal(err)
		}
	}
	waitPeers(t, b, 3)
	payload := filepath.Join(t.TempDir(), "x.bin")
	_ = os.WriteFile(payload, []byte("not for you"), 0o644)
	digest, _ := cas.New(a.Cfg.CASDir()).PutFile(payload)
	a.indexOnce(ctx)
	if err := a.Store.SetService("files", false); err != nil {
		t.Fatal(err)
	}
	pctx, pcancel := context.WithTimeout(ctx, 4*time.Second)
	defer pcancel()
	if _, err := b.PullSeed(pctx, digest, nil); err == nil {
		t.Fatal("pull succeeded although files is off on the only holder")
	}
}
