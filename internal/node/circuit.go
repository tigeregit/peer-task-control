package node

import (
	"context"
	"net"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/protocol"
	relayclient "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	relayv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	ma "github.com/multiformats/go-multiaddr"
)

func (n *Node) openStream(ctx context.Context, pid peer.ID, proto protocol.ID) (network.Stream, error) {
	// Circuit v2 hops are Limited (transient). NewStream / Connect will ignore
	// them and hang on unreachable direct addrs unless this option is set.
	ctx = network.WithAllowLimitedConn(ctx, "circuit")
	if err := n.ensureConnected(ctx, pid); err != nil {
		return nil, err
	}
	return n.Host.NewStream(ctx, pid, proto)
}

func (n *Node) ensureConnected(ctx context.Context, pid peer.ID) error {
	ctx = network.WithAllowLimitedConn(ctx, "circuit")
	switch n.Host.Network().Connectedness(pid) {
	case network.Connected, network.Limited:
		return nil
	}
	var first error
	if addrs := n.reachableAddrs(n.Host.Peerstore().Addrs(pid)); len(addrs) > 0 {
		dctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := n.Host.Connect(dctx, peer.AddrInfo{ID: pid, Addrs: addrs})
		cancel()
		if err == nil {
			return nil
		}
		first = err
	}
	circs := n.circuitAddrs(pid)
	if len(circs) == 0 && first == nil {
		first = errNoRelay
	}
	for _, circ := range circs {
		n.Host.Peerstore().AddAddrs(pid, []ma.Multiaddr{circ}, peerstore.TempAddrTTL)
		if err := n.Host.Connect(ctx, peer.AddrInfo{ID: pid, Addrs: []ma.Multiaddr{circ}}); err == nil {
			return nil
		} else {
			n.Log.Info("circuit dial failed", "peer", pid.String(), "addr", circ.String(), "err", err)
			if first == nil {
				first = err
			}
		}
	}
	return first
}

var errNoRelay = errString("no path to peer (direct or relay)")

type errString string

func (e errString) Error() string { return string(e) }

func (n *Node) reachableAddrs(addrs []ma.Multiaddr) []ma.Multiaddr {
	nets := n.localNets()
	var out []ma.Multiaddr
	for _, a := range addrs {
		if a == nil || isCircuit(a) {
			continue
		}
		ip, ok := addrIP(a)
		if !ok || ip.IsLoopback() || n.isSelfIP(ip) {
			continue
		}
		for _, ipn := range nets {
			if ipn.Contains(ip) {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

func (n *Node) localNets() []*net.IPNet {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []*net.IPNet
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP == nil || ipn.IP.IsLoopback() {
			continue
		}
		out = append(out, narrow(ipn))
	}
	return out
}

func (n *Node) circuitAddrs(dest peer.ID) []ma.Multiaddr {

	var out []ma.Multiaddr
	seen := map[string]bool{}
	for _, e := range n.Catalog.Snapshot() {
		st, ok := e.Services["relay"]
		if !ok || !st.Enabled {
			continue
		}
		rid, err := peer.Decode(e.PeerID)
		if err != nil || rid == dest || rid == n.Host.ID() {
			continue
		}
		var bases []ma.Multiaddr
		for _, c := range n.Host.Network().ConnsToPeer(rid) {
			if a := c.RemoteMultiaddr(); a != nil && !isCircuit(a) {
				bases = append(bases, a)
			}
		}
		if len(bases) == 0 {
			var catalogAddrs []ma.Multiaddr
			for _, s := range e.Addrs {
				a, err := ma.NewMultiaddr(s)
				if err != nil || isCircuit(a) {
					continue
				}
				catalogAddrs = append(catalogAddrs, a)
			}
			bases = n.reachableAddrs(catalogAddrs)
		}
		for _, base := range bases {
			circ, err := circuitThrough(base, rid)
			if err != nil || seen[circ.String()] {
				continue
			}
			seen[circ.String()] = true
			out = append(out, circ)
		}
	}
	return out
}

func circuitThrough(relayAddr ma.Multiaddr, relayID peer.ID) (ma.Multiaddr, error) {
	// Drop a trailing /p2p/<id> if the catalog already included it, then append
	// /p2p/<relay>/p2p-circuit.
	if p, err := relayAddr.ValueForProtocol(ma.P_P2P); err == nil && p != "" {
		relayAddr, _ = ma.SplitFunc(relayAddr, func(c ma.Component) bool { return c.Protocol().Code == ma.P_P2P })
		if relayAddr == nil {
			return nil, errString("empty relay addr")
		}
	}
	p2p, err := ma.NewMultiaddr("/p2p/" + relayID.String())
	if err != nil {
		return nil, err
	}
	hop, err := ma.NewMultiaddr("/p2p-circuit")
	if err != nil {
		return nil, err
	}
	return relayAddr.Encapsulate(p2p).Encapsulate(hop), nil
}

func (n *Node) reserveWithRelays(ctx context.Context) {
	if n.Cfg.Services.Relay {
		return
	}
	for _, e := range n.Catalog.Snapshot() {
		st, ok := e.Services["relay"]
		if !ok || !st.Enabled {
			continue
		}
		rid, err := peer.Decode(e.PeerID)
		if err != nil || rid == n.Host.ID() {
			continue
		}
		var addrs []ma.Multiaddr
		for _, s := range e.Addrs {
			a, err := ma.NewMultiaddr(s)
			if err != nil || isCircuit(a) {
				continue
			}
			addrs = append(addrs, a)
		}
		if len(addrs) == 0 {
			continue
		}
		info := peer.AddrInfo{ID: rid, Addrs: addrs}
		cctx, cancel := context.WithTimeout(ctx, dialTimeout)
		_ = n.Host.Connect(cctx, info)
		_, err = relayclient.Reserve(cctx, n.Host, info)
		cancel()
		if err != nil {
			n.Log.Debug("relay reservation failed", "relay", e.Name, "err", err)
			continue
		}
		n.Log.Info("reserved circuit relay", "relay", e.Name, "peer", e.PeerID)
	}
}

func (n *Node) startRelayLoop() {
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		n.reserveWithRelays(n.ctx)
		for {
			select {
			case <-n.ctx.Done():
				return
			case <-t.C:
				n.reserveWithRelays(n.ctx)
			}
		}
	}()
}

// lanRelayOptions relaxes the public-relay defaults (128KiB / 2min per hop).
// Trusted-segment gateways may forward multi-gigabyte worktrees and result packages.
func lanRelayOptions() []relayv2.Option {
	rc := relayv2.DefaultResources()
	rc.Limit = &relayv2.RelayLimit{Duration: 30 * time.Minute, Data: 64 << 30}
	rc.BufferSize = 64 << 10
	return []relayv2.Option{relayv2.WithResources(rc)}
}
