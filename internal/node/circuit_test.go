package node

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/p2p/net/swarm"
	ma "github.com/multiformats/go-multiaddr"

	"github.com/tigeregit/peer-task-control/internal/cas"
	"github.com/tigeregit/peer-task-control/internal/catalog"
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

func TestCircuitReservationRestoredAfterRelayRestart(t *testing.T) {
	cidrs := []string{"127.0.0.0/8"}
	target := startTestNode(t, "target", "127.0.0.1", false, cidrs)
	reader := startTestNode(t, "reader", "127.0.0.2", false, []string{"127.0.0.2/32", "127.0.0.3/32"})
	gateway := startTestNode(t, "gateway", "127.0.0.3", true, cidrs)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := gateway.Probe(ctx, listenHostPort(target)); err != nil {
		t.Fatal(err)
	}
	if err := reader.Probe(ctx, listenHostPort(gateway)); err != nil {
		t.Fatal(err)
	}
	waitPeers(t, target, 3)
	waitPeers(t, reader, 3)
	target.reserveWithRelays(ctx)
	reader.reserveWithRelays(ctx)
	time.Sleep(500 * time.Millisecond)

	cfg := gateway.Cfg
	if err := gateway.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Start(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("restart gateway: %v", err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	if changed := target.Catalog.Put(restarted.LocalEntry()); changed {
		t.Fatal("restart changed gateway catalog entry; test requires an unchanged catalog")
	}
	if err := restarted.Probe(ctx, listenHostPort(target)); err != nil {
		t.Fatalf("reconnect target: %v", err)
	}
	if err := reader.Probe(ctx, listenHostPort(restarted)); err != nil {
		t.Fatalf("reconnect reader: %v", err)
	}

	id := target.Host.ID()
	_ = reader.Host.Network().ClosePeer(id)
	reader.Host.Peerstore().ClearAddrs(id)
	networkSwarm, ok := reader.Host.Network().(*swarm.Swarm)
	if !ok {
		t.Fatal("expected libp2p swarm")
	}
	deadline := time.Now().Add(4 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		networkSwarm.Backoff().Clear(id)
		attempt, stop := context.WithTimeout(ctx, time.Second)
		stream, err := reader.openStream(attempt, id, ProtocolResources)
		stop()
		if err == nil {
			remote := stream.Conn().RemoteMultiaddr()
			_ = stream.Close()
			if !isCircuit(remote) {
				t.Fatalf("expected a relayed connection, got %s", remote)
			}
			return
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("relay reservation was not restored promptly after reconnect: %v", lastErr)
}

func TestCatalogLoopbackIsNotUsedAsRemoteAddress(t *testing.T) {
	cidrs := []string{"127.0.0.0/8"}
	reader := startTestNode(t, "reader", "127.0.0.1", false, cidrs)
	target := startTestNode(t, "target", "127.0.0.2", false, cidrs)

	reader.learnAddrs([]catalog.Entry{target.LocalEntry()})
	for _, addr := range reader.Host.Peerstore().Addrs(target.Host.ID()) {
		if ip, ok := addrIP(addr); ok && ip.IsLoopback() {
			t.Fatalf("learned remote loopback address %s", addr)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := reader.Probe(ctx, listenHostPort(target)); err != nil {
		t.Fatalf("explicit same-host probe must remain supported: %v", err)
	}
	if reader.Host.Network().Connectedness(target.Host.ID()) != network.Connected {
		t.Fatal("explicit same-host probe did not connect")
	}
}

func bytesOf(n int, v byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = v
	}
	return b
}
