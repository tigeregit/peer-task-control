package config

import (
	"net"
	"path/filepath"
	"testing"
)

func TestNormalizeRequiresCIDR(t *testing.T) {
	c := Defaults()
	c.TrustedCIDRs = nil
	if err := c.Normalize(); err == nil {
		t.Fatal("expected error for empty trusted_cidrs")
	}
}

func TestLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	src := Defaults()
	src.Path = path
	src.DataDir = dir
	src.TrustedCIDRs = []string{"10.0.0.0/8", "127.0.0.0/8"}
	src.Services.Relay = true
	src.PSK = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := src.Normalize(); err != nil {
		t.Fatal(err)
	}
	if err := src.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Services.Relay || got.ListenPort() != 18765 {
		t.Fatalf("unexpected config: %+v", got)
	}
	if len(got.PSKBytes) != 32 || got.PSKBytes[0] != 0x01 {
		t.Fatalf("psk bytes = %x", got.PSKBytes)
	}
	if !got.InTrustedIP(net.ParseIP("10.9.1.2")) || !got.InTrustedIP(net.ParseIP("127.0.0.1")) {
		t.Fatal("trusted IP check failed")
	}
	if got.InTrustedIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public IP should be rejected")
	}
}

func TestParsePSKRejectsShort(t *testing.T) {
	if _, err := ParsePSK("abcd"); err == nil {
		t.Fatal("expected error")
	}
}

func TestScanHostsTrustResolvedIP(t *testing.T) {
	c := Defaults()
	c.TrustedCIDRs = []string{"10.87.0.0/24"}
	c.ScanHosts = []string{"127.0.0.1", "localhost:19999"}
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
	if !c.InTrustedIP(net.ParseIP("10.87.0.2")) {
		t.Fatal("trusted CIDR should still match")
	}
	if !c.InTrustedIP(net.ParseIP("127.0.0.1")) {
		t.Fatal("scan_hosts loopback should be trusted")
	}
	if c.InTrustedIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("unrelated public IP should be rejected")
	}
	ports := c.ScanHostPorts(18765)
	if len(ports) != 2 {
		t.Fatalf("ScanHostPorts = %v", ports)
	}
	if ports[0] != "127.0.0.1:18765" || ports[1] != "localhost:19999" {
		t.Fatalf("ScanHostPorts = %v", ports)
	}
}

func TestStoragePathsAndEnv(t *testing.T) {
	dir := t.TempDir()
	seeds := filepath.Join(dir, "seeds-root")
	wt := filepath.Join(dir, "D-drive", "worktrees")
	runs := filepath.Join(dir, "runs-root")
	t.Setenv("PTC_SEEDS_DIR", seeds)
	t.Setenv("PTC_WORKTREES_DIR", wt)
	t.Setenv("PTC_RUNS_DIR", runs)
	c := Defaults()
	c.TrustedCIDRs = []string{"127.0.0.0/8"}
	c.DataDir = filepath.Join(dir, "data")
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
	if c.SeedsDir() != seeds || c.PackagesDir() != filepath.Join(seeds, "packages") || c.CASDir() != filepath.Join(seeds, "cas") {
		t.Fatalf("seeds %s packages %s cas %s", c.SeedsDir(), c.PackagesDir(), c.CASDir())
	}
	if c.WorktreesDir() != wt {
		t.Fatalf("worktrees %s", c.WorktreesDir())
	}
	if c.RunsDir() != runs {
		t.Fatalf("runs %s", c.RunsDir())
	}
	t.Setenv("PTC_SEEDS_DIR", "")
	t.Setenv("PTC_WORKTREES_DIR", "")
	t.Setenv("PTC_RUNS_DIR", "")
	c2 := Defaults()
	c2.TrustedCIDRs = []string{"127.0.0.0/8"}
	c2.DataDir = filepath.Join(dir, "data")
	if err := c2.Normalize(); err != nil {
		t.Fatal(err)
	}
	if c2.SeedsDir() != c2.DataDir || c2.WorktreesDir() != filepath.Join(c2.DataDir, "worktrees") || c2.RunsDir() != filepath.Join(c2.DataDir, "runs") {
		t.Fatalf("defaults seeds=%s wt=%s runs=%s", c2.SeedsDir(), c2.WorktreesDir(), c2.RunsDir())
	}
}
