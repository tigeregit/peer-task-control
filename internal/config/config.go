package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
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
	Path         string        `yaml:"-"`
	TrustedNets  []*net.IPNet  `yaml:"-"`
	PSKBytes     []byte        `yaml:"-"`
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
	return nil
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
		// Loopback is accepted only when a loopback CIDR is configured.
		for _, n := range c.TrustedNets {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}
	for _, n := range c.TrustedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
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
