package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultListen     = "0.0.0.0:18765"
	DefaultAPI        = "127.0.0.1:18780"
	DefaultScanPeriod = 30 * time.Second
	DefaultPort       = 18765
)

type Services struct {
	Exec  bool `yaml:"exec"`
	Files bool `yaml:"files"`
	Relay bool `yaml:"relay"`
}

type Policy struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
	// RemoteClean, when true, lets a trusted peer ask this node to delete
	// local seeds, worktrees, or run records. Default is off; ptc clean
	// operates on the local node unless this is enabled on the target.
	RemoteClean bool `yaml:"remote_clean,omitempty"`
}

// Storage holds the three data roots. Empty fields default under data_dir.
// Windows paths (including another drive such as D:\ptc\worktrees) are fine.
type Storage struct {
	// SeedsDir is the parent of cas/ and packages/. Default: data_dir.
	SeedsDir string `yaml:"seeds_dir,omitempty"`
	// WorktreesDir is the root of per-run worktrees. Default: data_dir/worktrees.
	WorktreesDir string `yaml:"worktrees_dir,omitempty"`
	// RunsDir holds run records and the persistent job queue. Default: data_dir/runs.
	RunsDir string `yaml:"runs_dir,omitempty"`
}

// Update is the policy for remote (mesh) upgrades of this node.
type Update struct {
	// AcceptRemote lets trusted mesh peers push a new ptc version to this
	// node (same major version only). Default true; set false to refuse.
	AcceptRemote *bool `yaml:"accept_remote,omitempty"`
	// Repo is the GitHub owner/name used for release downloads.
	Repo string `yaml:"repo,omitempty"`
}

// AcceptsRemoteUpdate reports the update.accept_remote policy (default on).
func (c *Config) AcceptsRemoteUpdate() bool {
	return c == nil || c.Update.AcceptRemote == nil || *c.Update.AcceptRemote
}

// Exec controls how this node runs dispatched jobs.
type Exec struct {
	// Concurrency is how many jobs this exec node runs at once. Default 1.
	Concurrency int `yaml:"concurrency,omitempty"`
}

// Transfer selects how seeds move between nodes.
type Transfer struct {
	// Mode is auto (default: bitswap when the holder advertises a CID,
	// otherwise the whole-seed tar stream), bitswap (never fall back), or tar
	// (P0/P2 behaviour, no block exchange).
	Mode string `yaml:"mode,omitempty"`
}

type Config struct {
	Name         string        `yaml:"name"`
	Listen       string        `yaml:"listen"`
	API          string        `yaml:"api"`
	DataDir      string        `yaml:"data_dir"`
	TrustedCIDRs []string      `yaml:"trusted_cidrs"`
	PSK          string        `yaml:"psk,omitempty"`
	Services     Services      `yaml:"services"`
	Policy       Policy        `yaml:"policy"`
	ScanInterval time.Duration `yaml:"scan_interval"`
	DisableMDNS  bool          `yaml:"disable_mdns,omitempty"`
	// ScanHosts are extra probe targets (hostname, IP, or host:port) the
	// gateway should dial in addition to trusted CIDR scans. Resolved IPs
	// are also admitted by the connection gater, so a Docker Desktop host
	// node can be reached via host.docker.internal without scanning that
	// whole virtual subnet.
	ScanHosts    []string     `yaml:"scan_hosts,omitempty"`
	Storage      Storage      `yaml:"storage,omitempty"`
	Exec         Exec         `yaml:"exec,omitempty"`
	Transfer     Transfer     `yaml:"transfer,omitempty"`
	Update       Update       `yaml:"update,omitempty"`
	Path         string       `yaml:"-"`
	TrustedNets  []*net.IPNet `yaml:"-"`
	PSKBytes     []byte       `yaml:"-"`
	mu           sync.RWMutex
	scanHostNets []*net.IPNet
}

func DefaultDataDir() string {
	if d, err := os.UserConfigDir(); err == nil && d != "" {
		return filepath.Join(d, "ptc")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".ptc")
	}
	return filepath.Join(home, ".ptc")
}

func Defaults() *Config {
	host, _ := os.Hostname()
	return &Config{
		Name:         host,
		Listen:       DefaultListen,
		API:          DefaultAPI,
		DataDir:      DefaultDataDir(),
		Services:     Services{Exec: true, Files: true, Relay: false},
		ScanInterval: DefaultScanPeriod,
	}
}

func Load(path string) (*Config, error) {
	cfg := Defaults()
	if path == "" {
		return nil, fmt.Errorf("config path is empty")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.Path = path
	if err := cfg.Normalize(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Normalize() error {
	if strings.TrimSpace(c.Name) == "" {
		host, _ := os.Hostname()
		c.Name = host
	}
	if c.Listen == "" {
		c.Listen = DefaultListen
	}
	if c.API == "" {
		c.API = DefaultAPI
	}
	if c.DataDir == "" {
		c.DataDir = DefaultDataDir()
	}
	if c.ScanInterval <= 0 {
		c.ScanInterval = DefaultScanPeriod
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return fmt.Errorf("listen %q: %w", c.Listen, err)
	}
	if _, _, err := net.SplitHostPort(c.API); err != nil {
		return fmt.Errorf("api %q: %w", c.API, err)
	}
	if len(c.TrustedCIDRs) == 0 {
		return fmt.Errorf("trusted_cidrs must contain at least one CIDR")
	}
	c.TrustedNets = c.TrustedNets[:0]
	for _, raw := range c.TrustedCIDRs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		_, n, err := net.ParseCIDR(raw)
		if err != nil {
			return fmt.Errorf("trusted_cidrs %q: %w", raw, err)
		}
		c.TrustedNets = append(c.TrustedNets, n)
	}
	if len(c.TrustedNets) == 0 {
		return fmt.Errorf("trusted_cidrs must contain at least one CIDR")
	}
	if s := strings.TrimSpace(c.PSK); s != "" {
		b, err := ParsePSK(s)
		if err != nil {
			return err
		}
		c.PSKBytes = b
	}
	c.ApplyEnv()
	switch strings.ToLower(strings.TrimSpace(c.Transfer.Mode)) {
	case "", "auto":
		c.Transfer.Mode = "auto"
	case "bitswap", "tar":
		c.Transfer.Mode = strings.ToLower(strings.TrimSpace(c.Transfer.Mode))
	default:
		return fmt.Errorf("transfer.mode %q: want auto, bitswap or tar", c.Transfer.Mode)
	}
	if c.Exec.Concurrency <= 0 {
		c.Exec.Concurrency = 1
	}
	c.ResolveScanHosts()
	return nil
}

// ApplyEnv overlays PTC_DATA_DIR, PTC_SEEDS_DIR, PTC_WORKTREES_DIR, and
// PTC_RUNS_DIR. Call this from Normalize; CLI flags should be applied after Load.
func (c *Config) ApplyEnv() {
	if v := strings.TrimSpace(os.Getenv("PTC_DATA_DIR")); v != "" {
		c.DataDir = v
	}
	if v := strings.TrimSpace(os.Getenv("PTC_SEEDS_DIR")); v != "" {
		c.Storage.SeedsDir = v
	}
	if v := strings.TrimSpace(os.Getenv("PTC_WORKTREES_DIR")); v != "" {
		c.Storage.WorktreesDir = v
	}
	if v := strings.TrimSpace(os.Getenv("PTC_RUNS_DIR")); v != "" {
		c.Storage.RunsDir = v
	}
}

// SeedsDir is the parent of cas/ and packages/.
func (c *Config) SeedsDir() string {
	if c != nil && strings.TrimSpace(c.Storage.SeedsDir) != "" {
		return c.Storage.SeedsDir
	}
	if c != nil && c.DataDir != "" {
		return c.DataDir
	}
	return DefaultDataDir()
}

func (c *Config) PackagesDir() string { return filepath.Join(c.SeedsDir(), "packages") }
func (c *Config) CASDir() string      { return filepath.Join(c.SeedsDir(), "cas") }

func (c *Config) WorktreesDir() string {
	if c != nil && strings.TrimSpace(c.Storage.WorktreesDir) != "" {
		return c.Storage.WorktreesDir
	}
	if c != nil && c.DataDir != "" {
		return filepath.Join(c.DataDir, "worktrees")
	}
	return filepath.Join(DefaultDataDir(), "worktrees")
}

func (c *Config) RunsDir() string {
	if c != nil && strings.TrimSpace(c.Storage.RunsDir) != "" {
		return c.Storage.RunsDir
	}
	if c != nil && c.DataDir != "" {
		return filepath.Join(c.DataDir, "runs")
	}
	return filepath.Join(DefaultDataDir(), "runs")
}

// PullTmpDir stages bitswap pulls on the seeds volume so the verified tree
// can be renamed into packages/ without copying.
func (c *Config) PullTmpDir() string { return filepath.Join(c.SeedsDir(), ".pull") }

func (c *Config) FetchTmpDir() string {
	return filepath.Join(c.WorktreesDir(), ".fetch")
}

func (c *Config) QueueDir() string {
	return filepath.Join(c.RunsDir(), "queue")
}

func ParsePSK(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "0x")
	if len(s) != 64 {
		return nil, fmt.Errorf("psk must be 64 hex characters (32 bytes), got %d characters", len(s))
	}
	out := make([]byte, 32)
	for i := 0; i < 32; i++ {
		var v byte
		if _, err := fmt.Sscanf(s[i*2:i*2+2], "%02x", &v); err != nil {
			return nil, fmt.Errorf("psk hex: %w", err)
		}
		out[i] = v
	}
	return out, nil
}

func (c *Config) ListenPort() int {
	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return DefaultPort
	}
	var p int
	if _, err := fmt.Sscanf(port, "%d", &p); err != nil {
		return DefaultPort
	}
	return p
}

func (c *Config) InTrustedIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		// Loopback is accepted only when a loopback CIDR is configured
		// or a scan_hosts entry resolved to a loopback address.
		for _, n := range c.TrustedNets {
			if n.Contains(ip) {
				return true
			}
		}
		return c.inScanHostNet(ip)
	}
	for _, n := range c.TrustedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return c.inScanHostNet(ip)
}

// ResolveScanHosts looks up scan_hosts and caches their IPs as /32 or /128
// nets used by InTrustedIP. Safe to call again when DNS might have changed.
func (c *Config) ResolveScanHosts() {
	var nets []*net.IPNet
	for _, raw := range c.ScanHosts {
		host, _, ok := splitScanHost(raw)
		if !ok {
			continue
		}
		if ip := net.ParseIP(host); ip != nil {
			nets = append(nets, ipNet(ip))
			continue
		}
		ips, err := net.LookupIP(host)
		if err != nil {
			continue
		}
		for _, ip := range ips {
			nets = append(nets, ipNet(ip))
		}
	}
	c.mu.Lock()
	c.scanHostNets = nets
	c.mu.Unlock()
}

// ScanHostPorts returns host:port targets from scan_hosts, filling in
// defaultPort when an entry has no port.
func (c *Config) ScanHostPorts(defaultPort int) []string {
	port := fmt.Sprintf("%d", defaultPort)
	var out []string
	for _, raw := range c.ScanHosts {
		host, p, ok := splitScanHost(raw)
		if !ok {
			continue
		}
		if p == "" {
			p = port
		}
		out = append(out, net.JoinHostPort(host, p))
	}
	return out
}

func (c *Config) inScanHostNet(ip net.IP) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, n := range c.scanHostNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func splitScanHost(raw string) (host, port string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	if h, p, err := net.SplitHostPort(raw); err == nil {
		return h, p, true
	}
	// A bare IPv6 address is not host:port.
	if strings.Count(raw, ":") > 1 && !strings.HasPrefix(raw, "[") {
		if net.ParseIP(raw) == nil {
			return "", "", false
		}
		return raw, "", true
	}
	return raw, "", true
}

func ipNet(ip net.IP) *net.IPNet {
	if v4 := ip.To4(); v4 != nil {
		return &net.IPNet{IP: v4, Mask: net.CIDRMask(32, 32)}
	}
	return &net.IPNet{IP: ip.To16(), Mask: net.CIDRMask(128, 128)}
}

func (c *Config) Save() error {
	if c.Path == "" {
		return fmt.Errorf("config has no path")
	}
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o700); err != nil {
		return err
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(c.Path, b, 0o600)
}

func (c *Config) IdentityPath() string {
	return filepath.Join(c.DataDir, "identity.key")
}

func (c *Config) LockPath() string {
	return filepath.Join(c.DataDir, "daemon.lock")
}
