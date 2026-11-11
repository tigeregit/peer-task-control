package gater

import (
	"log/slog"
	"net"

	"github.com/libp2p/go-libp2p/core/control"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"

	"github.com/tigeregit/peer-task-control/internal/config"
)

// CIDR is a libp2p connection gater that only admits addresses in trusted nets.
type CIDR struct {
	Cfg *config.Config
	Log *slog.Logger
}

func New(cfg *config.Config, log *slog.Logger) *CIDR {
	if log == nil {
		log = slog.Default()
	}
	return &CIDR{Cfg: cfg, Log: log}
}

func (g *CIDR) InterceptPeerDial(p peer.ID) (allow bool) { return true }

func (g *CIDR) InterceptAddrDial(_ peer.ID, addr ma.Multiaddr) (allow bool) {
	if isCircuit(addr) {
		return true
	}
	ip, ok := ipFromAddr(addr)
	if !ok {
		// No IP (e.g. only /p2p/...). Let later stages decide.
		return true
	}
	if !g.Cfg.InTrustedIP(ip) {
		g.Log.Debug("reject dial: address outside trusted CIDRs", "ip", ip.String())
		return false
	}
	return true
}

func (g *CIDR) InterceptAccept(addrs network.ConnMultiaddrs) (allow bool) {
	if isCircuit(addrs.RemoteMultiaddr()) {
		return true
	}
	ip, ok := ipFromAddr(addrs.RemoteMultiaddr())
	if !ok {
		g.Log.Debug("reject accept: no remote IP")
		return false
	}
	if !g.Cfg.InTrustedIP(ip) {
		g.Log.Info("reject accept: address outside trusted CIDRs", "ip", ip.String())
		return false
	}
	return true
}

func (g *CIDR) InterceptSecured(_ network.Direction, _ peer.ID, addrs network.ConnMultiaddrs) (allow bool) {
	return g.InterceptAccept(addrs)
}

func (g *CIDR) InterceptUpgraded(_ network.Conn) (allow bool, reason control.DisconnectReason) {
	return true, 0
}

func ipFromAddr(addr ma.Multiaddr) (net.IP, bool) {
	if addr == nil {
		return nil, false
	}
	ip, err := manet.ToIP(addr)
	if err != nil {
		return nil, false
	}
	return ip, true
}

func isCircuit(addr ma.Multiaddr) bool {
	if addr == nil {
		return false
	}
	for _, p := range addr.Protocols() {
		if p.Code == ma.P_CIRCUIT {
			return true
		}
	}
	return false
}
