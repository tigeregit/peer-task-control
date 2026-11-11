package blocks

import (
	"context"
	"sync"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	multiaddr "github.com/multiformats/go-multiaddr"
)

// meshHost lets bitswap use circuit-relay (Limited) connections. ptc relays
// are LAN gateways configured with a 30 min / 64 GiB per-circuit budget, so a
// relayed peer is a normal bitswap partner. Stock bitswap ignores Limited
// connections and opens streams without WithAllowLimitedConn.
type meshHost struct {
	host.Host
	net *meshNet
}

func newMeshHost(h host.Host) *meshHost {
	return &meshHost{Host: h, net: &meshNet{Network: h.Network(), wrapped: map[network.Notifiee]network.Notifiee{}}}
}

func allowLimited(ctx context.Context) context.Context {
	return network.WithAllowLimitedConn(ctx, "ptc-bitswap")
}

func (h *meshHost) NewStream(ctx context.Context, p peer.ID, pids ...protocol.ID) (network.Stream, error) {
	return h.Host.NewStream(allowLimited(ctx), p, pids...)
}

func (h *meshHost) Connect(ctx context.Context, ai peer.AddrInfo) error {
	return h.Host.Connect(allowLimited(ctx), ai)
}

func (h *meshHost) Network() network.Network { return h.net }

type meshNet struct {
	network.Network
	mu      sync.Mutex
	wrapped map[network.Notifiee]network.Notifiee
}

func (n *meshNet) Connectedness(p peer.ID) network.Connectedness {
	c := n.Network.Connectedness(p)
	if c == network.Limited {
		return network.Connected
	}
	return c
}

func (n *meshNet) Conns() []network.Conn {
	cs := n.Network.Conns()
	out := make([]network.Conn, len(cs))
	for i, c := range cs {
		out[i] = meshConn{c}
	}
	return out
}

func (n *meshNet) Notify(f network.Notifiee) {
	w := &meshNotifiee{inner: f, net: n}
	n.mu.Lock()
	n.wrapped[f] = w
	n.mu.Unlock()
	n.Network.Notify(w)
}

func (n *meshNet) StopNotify(f network.Notifiee) {
	n.mu.Lock()
	w, ok := n.wrapped[f]
	delete(n.wrapped, f)
	n.mu.Unlock()
	if ok {
		n.Network.StopNotify(w)
	}
}

type meshConn struct{ network.Conn }

func (c meshConn) Stat() network.ConnStats {
	st := c.Conn.Stat()
	st.Limited = false
	return st
}

type meshNotifiee struct {
	inner network.Notifiee
	net   *meshNet
}

func (m *meshNotifiee) Listen(_ network.Network, a multiaddr.Multiaddr) { m.inner.Listen(m.net, a) }
func (m *meshNotifiee) ListenClose(_ network.Network, a multiaddr.Multiaddr) {
	m.inner.ListenClose(m.net, a)
}
func (m *meshNotifiee) Connected(_ network.Network, c network.Conn) {
	m.inner.Connected(m.net, meshConn{c})
}
func (m *meshNotifiee) Disconnected(_ network.Network, c network.Conn) {
	m.inner.Disconnected(m.net, meshConn{c})
}
