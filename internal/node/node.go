package node

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"

	"github.com/tigeregit/peer-task-control/internal/api"
	"github.com/tigeregit/peer-task-control/internal/caps"
	"github.com/tigeregit/peer-task-control/internal/catalog"
	"github.com/tigeregit/peer-task-control/internal/config"
	"github.com/tigeregit/peer-task-control/internal/control"
	"github.com/tigeregit/peer-task-control/internal/disk"
	"github.com/tigeregit/peer-task-control/internal/dispatch"
	"github.com/tigeregit/peer-task-control/internal/gater"
	"github.com/tigeregit/peer-task-control/internal/ident"
	"github.com/tigeregit/peer-task-control/internal/identity"
	"github.com/tigeregit/peer-task-control/internal/policy"
	"github.com/tigeregit/peer-task-control/internal/scan"
	"github.com/tigeregit/peer-task-control/internal/version"
)

const (
	ProtocolCatalog protocol.ID = "/ptc/catalog/1.0.0"
	mdnsServiceTag              = "ptc"
	dialTimeout                 = 8 * time.Second
	probeTimeout                = 400 * time.Millisecond
	scanConcurrency             = 64
)

// Node is a running ptc daemon.
type Node struct {
	Cfg     *config.Config
	Log     *slog.Logger
	Host    host.Host
	Catalog *catalog.Book
	Store   *policy.Store
	Caps    caps.Info

	ctx       context.Context
	cancel    context.CancelFunc
	mdns      mdns.Service
	apiLn     net.Listener
	httpSrv   *http.Server
	mu        sync.Mutex
	localTime time.Time
	scanNow   chan struct{}
	selfAddrs map[string]struct{}
	queue     *dispatch.Queue
	follows   *execFollowers
	cancels   map[string]context.CancelFunc
	execKick  chan struct{}
	disk      disk.Usage
	diskAt    time.Time
}

func Start(parent context.Context, cfg *config.Config, log *slog.Logger) (*Node, error) {
	if log == nil {
		log = slog.Default()
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	priv, err := identity.LoadOrCreate(cfg.IdentityPath())
	if err != nil {
		return nil, err
	}

	listen, err := listenMultiaddr(cfg.Listen)
	if err != nil {
		return nil, err
	}

	opts := []libp2p.Option{
		libp2p.Identity(priv),
		libp2p.ListenAddrs(listen),
		libp2p.ConnectionGater(gater.New(cfg, log)),
		libp2p.DefaultTransports,
		libp2p.Ping(true),
	}
	if len(cfg.PSKBytes) > 0 {
		opts = append(opts, libp2p.PrivateNetwork(cfg.PSKBytes))
	}
	if cfg.Services.Relay {
		// LAN relays are not AutoNAT-public; force the hop service to start anyway.
		opts = append(opts, libp2p.EnableRelayService(lanRelayOptions()...), libp2p.ForceReachabilityPublic())
	} else {
		opts = append(opts, libp2p.ForceReachabilityPrivate())
	}

	h, err := libp2p.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("libp2p: %w", err)
	}

	ctx, cancel := context.WithCancel(parent)
	n := &Node{
		Cfg:       cfg,
		Log:       log,
		Host:      h,
		Catalog:   catalog.New(),
		Store:     policy.New(cfg.Policy.Allow, cfg.Policy.Deny, cfg.Services.Exec, cfg.Services.Files, cfg.Services.Relay),
		Caps:      caps.Detect(),
		ctx:       ctx,
		cancel:    cancel,
		scanNow:   make(chan struct{}, 1),
		execKick:  make(chan struct{}, 1),
		selfAddrs: map[string]struct{}{},
	}
	n.bumpLocal()
	n.refreshSelfAddrs()
	n.Catalog.Put(n.LocalEntry())

	h.SetStreamHandler(ProtocolCatalog, n.handleCatalog)
	h.SetStreamHandler(ProtocolRuns, n.handleRuns)
	h.SetStreamHandler(ProtocolResources, n.handleResources)
	if err := n.initExec(); err != nil {
		cancel()
		_ = h.Close()
		return nil, err
	}
	h.Network().Notify((*netNotifee)(n))

	if !cfg.DisableMDNS {
		svc := mdns.NewMdnsService(h, mdnsServiceTag, (*mdnsNotifee)(n))
		if err := svc.Start(); err != nil {
			n.Log.Warn("mDNS start failed; same-segment discovery will rely on port probes", "err", err)
			_ = svc.Close()
		} else {
			n.mdns = svc
		}
	}

	if err := n.startAPI(); err != nil {
		_ = n.Close()
		return nil, err
	}
	if err := control.WriteLock(cfg.LockPath(), control.LockFile{
		PID:    os.Getpid(),
		API:    n.APIAddr(),
		PeerID: h.ID().String(),
		Name:   cfg.Name,
	}); err != nil {
		_ = n.Close()
		return nil, err
	}

	if err := ident.ServeUDP(ctx, cfg.Listen, h.ID().String(), log); err != nil {
		n.Log.Warn("identity UDP probe not started; gateway scan will not work", "err", err)
	}

	n.RequestScan()
	n.startRelayLoop()
	go n.scanLoop()

	n.Log.Info("ptc daemon started",
		"name", cfg.Name,
		"version", version.Version,
		"peer_id", h.ID().String(),
		"listen", addrsString(h.Addrs()),
		"api", n.APIAddr(),
		"nvidia", n.Caps.NVIDIA,
		"relay", cfg.Services.Relay,
	)
	return n, nil
}

func (n *Node) Close() error {
	n.cancel()
	control.RemoveLock(n.Cfg.LockPath())
	if n.httpSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = n.httpSrv.Shutdown(ctx)
		cancel()
	}
	if n.apiLn != nil {
		_ = n.apiLn.Close()
	}
	if n.mdns != nil {
		_ = n.mdns.Close()
	}
	if n.Host != nil {
		return n.Host.Close()
	}
	return nil
}

func (n *Node) APIAddr() string {
	if n.apiLn != nil {
		return n.apiLn.Addr().String()
	}
	return n.Cfg.API
}

func (n *Node) ListenAddrs() []string {
	return addrsString(n.Host.Addrs())
}

func (n *Node) Policy() *policy.Store { return n.Store }

func (n *Node) Peers() []catalog.Entry {
	n.Catalog.Put(n.LocalEntry())
	return n.Catalog.List()
}

func (n *Node) LocalEntry() catalog.Entry {
	execOn, filesOn, relayOn := n.Store.Services()
	execReason := ""
	execOffered := execOn && n.Caps.NVIDIA
	switch {
	case !n.Caps.NVIDIA:
		execReason = "this node has no NVIDIA GPU"
	case !execOn:
		execReason = "service exec is disabled on this host"
	}

	filesReason := ""
	if !filesOn {
		filesReason = "service files is disabled on this host"
	}
	relayReason := ""
	if !relayOn {
		relayReason = "service relay is disabled on this host"
	}

	n.mu.Lock()
	updated := n.localTime
	n.mu.Unlock()

	return catalog.Entry{
		PeerID: n.Host.ID().String(),
		Name:   n.Cfg.Name,
		Addrs:  addrsString(n.Host.Addrs()),
		Services: map[string]catalog.ServiceState{
			"exec":  {Offered: execOffered, Enabled: execOn, Reason: execReason},
			"files": {Offered: filesOn, Enabled: filesOn, Reason: filesReason},
			"relay": {Offered: relayOn, Enabled: relayOn, Reason: relayReason},
		},
		Caps: catalog.Caps{
			NVIDIA:  n.Caps.NVIDIA,
			GPUName: n.Caps.GPUName,
			VRAMMiB: n.Caps.VRAMMiB,
			Driver:  n.Caps.Driver,
			OS:      n.Caps.OS,
			Arch:    n.Caps.Arch,
		},
		UpdatedAt: updated,
		Disk:      n.diskUsage(),
	}
}

func (n *Node) diskUsage() *disk.Usage {
	n.mu.Lock()
	defer n.mu.Unlock()
	if time.Since(n.diskAt) < 5*time.Second && n.diskAt.Unix() != 0 {
		u := n.disk
		return &u
	}
	u := disk.Measure(n.Cfg)
	n.disk = u
	n.diskAt = time.Now()
	return &u
}

func (n *Node) PersistPolicy() error {
	execOn, filesOn, relayOn := n.Store.Services()
	n.Cfg.Services.Exec = execOn
	n.Cfg.Services.Files = filesOn
	n.Cfg.Services.Relay = relayOn
	n.Cfg.Policy.Allow = n.Store.AllowList()
	n.Cfg.Policy.Deny = n.Store.DenyList()
	if err := n.Cfg.Save(); err != nil {
		return err
	}
	n.bumpLocal()
	n.Catalog.Put(n.LocalEntry())
	n.broadcast()
	if relayOn {
		n.RequestScan()
	}
	return nil
}

func (n *Node) bumpLocal() {
	n.mu.Lock()
	n.localTime = time.Now().UTC()
	n.mu.Unlock()
}

func (n *Node) RequestScan() {
	select {
	case n.scanNow <- struct{}{}:
	default:
	}
}

func (n *Node) startAPI() error {
	ln, err := net.Listen("tcp", n.Cfg.API)
	if err != nil {
		return fmt.Errorf("listen api %s: %w", n.Cfg.API, err)
	}
	n.apiLn = ln
	srv := &http.Server{Handler: api.LocalOnly((&api.Server{Node: n}).Handler())}
	n.httpSrv = srv
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			n.Log.Error("api server stopped", "err", err)
		}
	}()
	return nil
}

func (n *Node) handleCatalog(s network.Stream) {
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(dialTimeout))
	dec := json.NewDecoder(s)
	enc := json.NewEncoder(s)
	var msg catalog.Message
	if err := dec.Decode(&msg); err != nil {
		n.Log.Debug("catalog stream decode", "err", err, "peer", s.Conn().RemotePeer())
		return
	}
	added := n.Catalog.Merge(msg.Entries)
	n.learnAddrs(msg.Entries)
	_ = enc.Encode(catalog.Message{Type: "push", Entries: n.Catalog.Snapshot()})
	if added > 0 {
		n.Log.Info("catalog updated", "added_or_changed", added, "from", s.Conn().RemotePeer())
		n.broadcastExcept(s.Conn().RemotePeer())
		go n.reserveWithRelays(n.ctx)
	}
}

func (n *Node) exchange(ctx context.Context, id peer.ID) error {
	if id == n.Host.ID() {
		return nil
	}
	s, err := n.Host.NewStream(ctx, id, ProtocolCatalog)
	if err != nil {
		return err
	}
	defer s.Close()
	_ = s.SetDeadline(timeNowDeadline(ctx, dialTimeout))
	enc := json.NewEncoder(s)
	dec := json.NewDecoder(s)
	if err := enc.Encode(catalog.Message{Type: "push", Entries: n.Catalog.Snapshot()}); err != nil {
		return err
	}
	var reply catalog.Message
	if err := dec.Decode(&reply); err != nil {
		return err
	}
	added := n.Catalog.Merge(reply.Entries)
	n.learnAddrs(reply.Entries)
	if added > 0 {
		n.Log.Info("catalog updated", "added_or_changed", added, "from", id)
		n.broadcastExcept(id)
		go n.reserveWithRelays(n.ctx)
	}
	return nil
}

func (n *Node) broadcast() {
	n.broadcastExcept("")
}

func (n *Node) broadcastExcept(skip peer.ID) {
	for _, id := range n.Host.Network().Peers() {
		if id == n.Host.ID() || id == skip {
			continue
		}
		id := id
		go func() {
			ctx, cancel := context.WithTimeout(n.ctx, dialTimeout)
			defer cancel()
			if err := n.exchange(ctx, id); err != nil {
				n.Log.Debug("catalog push failed", "peer", id, "err", err)
			}
		}()
	}
}

func (n *Node) learnAddrs(entries []catalog.Entry) {
	for _, e := range entries {
		id, err := peer.Decode(e.PeerID)
		if err != nil || id == n.Host.ID() {
			continue
		}
		var addrs []ma.Multiaddr
		for _, s := range e.Addrs {
			a, err := ma.NewMultiaddr(s)
			if err != nil {
				continue
			}
			if ip, ok := addrIP(a); ok && !n.Cfg.InTrustedIP(ip) && !isCircuit(a) {
				continue
			}
			addrs = append(addrs, a)
		}
		if len(addrs) > 0 {
			n.Host.Peerstore().AddAddrs(id, addrs, peerstore.PermanentAddrTTL)
		}
	}
}

// Probe tries to open a libp2p connection to host:port and exchange catalogs.
func (n *Node) Probe(ctx context.Context, hostport string) error {
	if n.isSelfHostPort(hostport) {
		return nil
	}
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip != nil && !n.Cfg.InTrustedIP(ip) {
		return fmt.Errorf("%s is outside trusted CIDRs", host)
	}

	d := net.Dialer{Timeout: probeTimeout}
	c, err := d.DialContext(ctx, "tcp", hostport)
	if err != nil {
		return err
	}
	_ = c.Close()

	ip := net.ParseIP(host)
	var maddr ma.Multiaddr
	if ip != nil && ip.To4() != nil {
		maddr, err = ma.NewMultiaddr(fmt.Sprintf("/ip4/%s/tcp/%s", ip.To4().String(), port))
	} else if ip != nil {
		maddr, err = ma.NewMultiaddr(fmt.Sprintf("/ip6/%s/tcp/%s", ip.String(), port))
	} else {
		maddr, err = ma.NewMultiaddr(fmt.Sprintf("/dns4/%s/tcp/%s", host, port))
	}
	if err != nil {
		return err
	}

	idStr, err := ident.Query(ctx, hostport)
	if err != nil {
		return fmt.Errorf("ident %s: %w", hostport, err)
	}
	id, err := peer.Decode(idStr)
	if err != nil {
		return fmt.Errorf("ident peer id from %s: %w", hostport, err)
	}
	if err := n.Host.Connect(ctx, peer.AddrInfo{ID: id, Addrs: []ma.Multiaddr{maddr}}); err != nil {
		return err
	}
	return n.exchange(ctx, id)
}

func (n *Node) scanLoop() {
	interval := n.Cfg.ScanInterval
	if interval <= 0 {
		interval = config.DefaultScanPeriod
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-n.scanNow:
		case <-timer.C:
		}
		n.runScan()
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(interval)
	}
}

func (n *Node) runScan() {
	n.Cfg.ResolveScanHosts()
	targets := n.scanTargets()
	hostports := make([]string, 0, len(targets)+len(n.Cfg.ScanHosts))
	port := n.Cfg.ListenPort()
	seen := map[string]struct{}{}
	for _, ip := range targets {
		hp := net.JoinHostPort(ip.String(), fmt.Sprintf("%d", port))
		if _, ok := seen[hp]; ok {
			continue
		}
		seen[hp] = struct{}{}
		hostports = append(hostports, hp)
	}
	for _, hp := range n.Cfg.ScanHostPorts(port) {
		if n.isSelfHostPort(hp) {
			continue
		}
		if _, ok := seen[hp]; ok {
			continue
		}
		seen[hp] = struct{}{}
		hostports = append(hostports, hp)
	}
	if len(hostports) == 0 {
		return
	}
	n.Log.Debug("scanning peers", "targets", len(hostports))
	sem := make(chan struct{}, scanConcurrency)
	var wg sync.WaitGroup
	for _, hostport := range hostports {
		hostport := hostport
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(n.ctx, dialTimeout)
			defer cancel()
			if err := n.Probe(ctx, hostport); err != nil {
				n.Log.Debug("probe miss", "addr", hostport, "err", err)
			} else {
				n.Log.Info("probe connected", "addr", hostport)
			}
		}()
	}
	wg.Wait()
}

func (n *Node) scanTargets() []net.IP {
	_, _, relayOn := n.Store.Services()
	var lists [][]net.IP
	if relayOn {
		for _, n4 := range n.Cfg.TrustedNets {
			lists = append(lists, scan.Hosts(n4))
		}
	} else {
		lists = append(lists, n.localTrustedHosts())
	}
	all := scan.UniqueIPs(lists...)
	out := make([]net.IP, 0, len(all))
	for _, ip := range all {
		if n.isSelfIP(ip) {
			continue
		}
		if !n.Cfg.InTrustedIP(ip) {
			continue
		}
		out = append(out, ip)
	}
	return out
}

func (n *Node) localTrustedHosts() []net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var lists [][]net.IP
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP == nil {
			continue
		}
		if !n.Cfg.InTrustedIP(ipn.IP) {
			continue
		}
		// Probe the interface's own subnet, but not a huge supernet from config.
		lists = append(lists, scan.Hosts(narrow(ipn)))
	}
	return scan.UniqueIPs(lists...)
}

func narrow(n *net.IPNet) *net.IPNet {
	if n == nil {
		return n
	}
	ones, bits := n.Mask.Size()
	if bits == 32 && ones < 24 {
		ip := n.IP.To4()
		if ip == nil {
			return n
		}
		return &net.IPNet{IP: ip.Mask(net.CIDRMask(24, 32)), Mask: net.CIDRMask(24, 32)}
	}
	return n
}

func (n *Node) refreshSelfAddrs() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.selfAddrs = map[string]struct{}{}
	for _, a := range n.Host.Addrs() {
		if ip, ok := addrIP(a); ok {
			n.selfAddrs[ip.String()] = struct{}{}
		}
	}
	if host, _, err := net.SplitHostPort(n.Cfg.Listen); err == nil {
		if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
			n.selfAddrs[ip.String()] = struct{}{}
		}
	}
}

func (n *Node) isSelfIP(ip net.IP) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	_, ok := n.selfAddrs[ip.String()]
	return ok
}

func (n *Node) isSelfHostPort(hostport string) bool {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return false
	}
	wantPort := fmt.Sprintf("%d", n.Cfg.ListenPort())
	if port != wantPort {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return n.isSelfIP(ip)
}

func listenMultiaddr(listen string) (ma.Multiaddr, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return nil, fmt.Errorf("listen %q: %w", listen, err)
	}
	if host == "" {
		host = "0.0.0.0"
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("listen host %q is not an IP", host)
	}
	if ip.To4() != nil {
		return ma.NewMultiaddr(fmt.Sprintf("/ip4/%s/tcp/%s", ip.To4().String(), port))
	}
	return ma.NewMultiaddr(fmt.Sprintf("/ip6/%s/tcp/%s", ip.String(), port))
}

func addrsString(addrs []ma.Multiaddr) []string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.String())
	}
	return out
}

func addrIP(addr ma.Multiaddr) (net.IP, bool) {
	ip, err := manet.ToIP(addr)
	if err != nil {
		return nil, false
	}
	return ip, true
}

func isCircuit(addr ma.Multiaddr) bool {
	for _, p := range addr.Protocols() {
		if p.Code == ma.P_CIRCUIT {
			return true
		}
	}
	return false
}

func timeNowDeadline(ctx context.Context, d time.Duration) time.Time {
	deadline := time.Now().Add(d)
	if t, ok := ctx.Deadline(); ok && t.Before(deadline) {
		return t
	}
	return deadline
}

type mdnsNotifee Node

func (m *mdnsNotifee) HandlePeerFound(pi peer.AddrInfo) {
	n := (*Node)(m)
	if pi.ID == n.Host.ID() {
		return
	}
	var trusted []ma.Multiaddr
	for _, a := range pi.Addrs {
		if isCircuit(a) {
			trusted = append(trusted, a)
			continue
		}
		ip, ok := addrIP(a)
		if ok && n.Cfg.InTrustedIP(ip) {
			trusted = append(trusted, a)
		}
	}
	if len(trusted) == 0 {
		return
	}
	pi.Addrs = trusted
	ctx, cancel := context.WithTimeout(n.ctx, dialTimeout)
	defer cancel()
	if err := n.Host.Connect(ctx, pi); err != nil {
		n.Log.Debug("mDNS connect failed", "peer", pi.ID, "err", err)
		return
	}
	if err := n.exchange(ctx, pi.ID); err != nil {
		n.Log.Debug("mDNS catalog exchange failed", "peer", pi.ID, "err", err)
	}
}

type netNotifee Node

func (nn *netNotifee) Listen(network.Network, ma.Multiaddr)       {}
func (nn *netNotifee) ListenClose(network.Network, ma.Multiaddr)  {}
func (nn *netNotifee) Disconnected(network.Network, network.Conn) {}

func (nn *netNotifee) Connected(_ network.Network, c network.Conn) {
	n := (*Node)(nn)
	id := c.RemotePeer()
	go func() {
		ctx, cancel := context.WithTimeout(n.ctx, dialTimeout)
		defer cancel()
		if err := n.exchange(ctx, id); err != nil {
			n.Log.Debug("connected catalog exchange failed", "peer", id, "err", err)
		}
	}()
}
