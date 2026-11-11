package node

import (
	"context"
	"github.com/tigeregit/peer-task-control/internal/cas"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peerstore"
	ma "github.com/multiformats/go-multiaddr"
)

func TestCircuitOpenStream(t *testing.T) {
	cidrs := []string{"127.0.0.0/8"}
	a := startTestNode(t, "node-a", "127.0.0.1", false, cidrs)
	b := startTestNode(t, "node-b", "127.0.0.2", false, cidrs)
	gw := startTestNode(t, "gateway", "127.0.0.3", true, cidrs)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
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

	// Give reservations a moment (startRelayLoop already ran once at start,
	// but the catalog was empty then).
	b.reserveWithRelays(ctx)
	a.reserveWithRelays(ctx)

	aid := a.Host.ID()
	// Drop any direct connection and hide node-a's direct addrs so b must hop.
	_ = b.Host.Network().ClosePeer(aid)
	b.Host.Peerstore().ClearAddrs(aid)

	circs := b.circuitAddrs(aid)
	if len(circs) == 0 {
		t.Fatalf("no circuit addrs from b to a; catalog=%+v connsToGw=%d", b.Peers(), len(b.Host.Network().ConnsToPeer(gw.Host.ID())))
	}
	t.Logf("circuit addrs: %v", circs)
	b.Host.Peerstore().AddAddrs(aid, circs, peerstore.TempAddrTTL)

	sctx, scancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer scancel()
	s, err := b.openStream(sctx, aid, ProtocolResources)
	if err != nil {
		t.Fatalf("openStream via circuit: %v (connectedness=%s conns=%d addrs=%v)",
			err, b.Host.Network().Connectedness(aid), len(b.Host.Network().ConnsToPeer(aid)), b.Host.Peerstore().Addrs(aid))
	}
	_ = s.Close()

	if got := b.Host.Network().Connectedness(aid); got != network.Connected && got != network.Limited {
		t.Fatalf("expected connected after stream, got %s", got)
	}
	hops := -1
	for _, c := range b.Host.Network().ConnsToPeer(aid) {
		t.Logf("conn remote=%s", c.RemoteMultiaddr())
		h := 0
		for _, p := range c.RemoteMultiaddr().Protocols() {
			if p.Code == ma.P_CIRCUIT {
				h = 1
			}
		}
		hops = h
	}
	if hops != 1 {
		t.Fatalf("expected a relayed connection, hops=%d", hops)
	}
}

func TestCircuitOpenStreamWithBlackholedDirect(t *testing.T) {
	cidrs := []string{"127.0.0.0/8", "172.31.255.0/24", "10.87.0.0/16"}
	a := startTestNode(t, "node-a", "127.0.0.1", false, cidrs)
	b := startTestNode(t, "node-b", "127.0.0.2", false, cidrs)
	gw := startTestNode(t, "gateway", "127.0.0.3", true, cidrs)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := gw.Probe(ctx, listenHostPort(a)); err != nil {
		t.Fatalf("probe a: %v", err)
	}
	if err := gw.Probe(ctx, listenHostPort(b)); err != nil {
		t.Fatalf("probe b: %v", err)
	}
	waitPeers(t, a, 3)
	waitPeers(t, b, 3)
	b.reserveWithRelays(ctx)
	a.reserveWithRelays(ctx)

	aid := a.Host.ID()
	_ = b.Host.Network().ClosePeer(aid)
	b.Host.Peerstore().ClearAddrs(aid)
	blackhole, err := ma.NewMultiaddr("/ip4/172.31.255.1/tcp/18765")
	if err != nil {
		t.Fatal(err)
	}
	b.Host.Peerstore().AddAddrs(aid, []ma.Multiaddr{blackhole}, peerstore.PermanentAddrTTL)

	sctx, scancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer scancel()
	start := time.Now()
	s, err := b.openStream(sctx, aid, ProtocolResources)
	t.Logf("elapsed %s err=%v connectedness=%s", time.Since(start), err, b.Host.Network().Connectedness(aid))
	if err != nil {
		t.Fatalf("openStream with blackholed direct addr: %v addrs=%v circs=%v", err, b.Host.Peerstore().Addrs(aid), b.circuitAddrs(aid))
	}
	_ = s.Close()
}

func TestCircuitBlobTransfer(t *testing.T) {
	cidrs := []string{"127.0.0.0/8"}
	a := startTestNode(t, "node-a", "127.0.0.1", false, cidrs)
	b := startTestNode(t, "node-b", "127.0.0.2", false, cidrs)
	gw := startTestNode(t, "gateway", "127.0.0.3", true, cidrs)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := gw.Probe(ctx, listenHostPort(a)); err != nil {
		t.Fatalf("probe a: %v", err)
	}
	if err := gw.Probe(ctx, listenHostPort(b)); err != nil {
		t.Fatalf("probe b: %v", err)
	}
	waitPeers(t, a, 3)
	waitPeers(t, b, 3)
	a.reserveWithRelays(ctx)
	b.reserveWithRelays(ctx)

	payload := filepath.Join(t.TempDir(), "blob.bin")
	const want = 256 << 10
	if err := os.WriteFile(payload, bytesOf(want, 0x5a), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, err := cas.New(filepath.Join(a.Cfg.DataDir, "cas")).PutFile(payload)
	if err != nil {
		t.Fatal(err)
	}

	aid := a.Host.ID()
	_ = b.Host.Network().ClosePeer(aid)
	b.Host.Peerstore().ClearAddrs(aid)
	circs := b.circuitAddrs(aid)
	if len(circs) == 0 {
		t.Fatal("no circuit addrs")
	}
	b.Host.Peerstore().AddAddrs(aid, circs, peerstore.TempAddrTTL)

	sctx, scancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer scancel()
	blob, err := b.fetchBlob(sctx, aid.String(), digest)
	if err != nil {
		t.Fatalf("fetchBlob: %v", err)
	}
	defer blob.Body.Close()
	n, err := io.Copy(io.Discard, blob.Body)
	if err != nil {
		t.Fatalf("read blob: %v", err)
	}
	if n != want {
		t.Fatalf("got %d bytes, want %d (relay default cap is 128KiB)", n, want)
	}
	if route := b.RouteTo(aid.String()); route.Hops != 1 {
		t.Fatalf("Bitswap transfer did not retain the relay path: %+v", route)
	}
	blocks, err := os.ReadDir(filepath.Join(b.Cfg.SeedsDir(), "blocks"))
	if err != nil || len(blocks) == 0 {
		t.Fatalf("relay transfer did not use Bitswap: blocks=%d, err=%v", len(blocks), err)
	}
}

func bytesOf(n int, v byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = v
	}
	return b
}
