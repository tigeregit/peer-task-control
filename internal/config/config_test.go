package config

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestScanConfigurationDefaultsAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	for _, interval := range []string{"", "scan_interval: 0s\n", "scan_interval: 5m\nscan_min_interval: 2m\n"} {
		if err := os.WriteFile(path, []byte("trusted_cidrs: [127.0.0.1/32]\ndata_dir: "+filepath.ToSlash(dir)+"\n"+interval), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		wantPeriod, wantMin := time.Duration(0), time.Minute
		if interval == "scan_interval: 5m\nscan_min_interval: 2m\n" {
			wantPeriod, wantMin = 5*time.Minute, 2*time.Minute
		}
		if cfg.ScanInterval != wantPeriod || cfg.ScanMinInterval != wantMin {
			t.Fatalf("unexpected scan settings: %v / %v", cfg.ScanInterval, cfg.ScanMinInterval)
		}
		if err := cfg.Save(); err != nil {
			t.Fatal(err)
		}
		reloaded, err := Load(path)
		if err != nil || reloaded.ScanInterval != wantPeriod || reloaded.ScanMinInterval != wantMin {
			t.Fatalf("scan settings changed after save: %+v, %v", reloaded, err)
		}
	}
	for _, invalid := range []string{"scan_interval: -1s\n", "scan_min_interval: -1s\n", "scan_interval: invalid\n"} {
		if err := os.WriteFile(path, []byte("trusted_cidrs: [127.0.0.1/32]\n"+invalid), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("accepted invalid scan configuration %q", invalid)
		}
	}
}

func TestRemoteUpdatePolicySavesConcurrently(t *testing.T) {
	c := Defaults()
	c.Path = filepath.Join(t.TempDir(), "config.yaml")
	var wg sync.WaitGroup
	for task := 0; task < 3; task++ {
		wg.Add(1)
		go func(task int) {
			defer wg.Done()
			for i := 0; i < 12; i++ {
				var err error
				switch task {
				case 0:
					err = c.SetRemoteUpdate(i%2 == 0)
				case 1:
					err = c.SavePolicy(Services{Files: true}, []string{"worker"}, nil)
				case 2:
					_ = c.AcceptsRemoteUpdate()
					err = c.Save()
				}
				if err != nil {
					t.Errorf("save policy: %v", err)
					return
				}
			}
		}(task)
	}
	wg.Wait()
	if c.AcceptsRemoteUpdate() {
		t.Fatal("last policy write was lost")
	}
}

func TestRemoteUpdatePolicySaveFailureRollsBack(t *testing.T) {
	c := Defaults()
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.Path = filepath.Join(parent, "config.yaml")
	if err := c.SetRemoteUpdate(false); err == nil || !c.AcceptsRemoteUpdate() {
		t.Fatalf("failed persistence changed the live policy: %v", err)
	}
}

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

func TestSaveWhileResolvingScanHosts(t *testing.T) {
	dir := t.TempDir()
	c := Defaults()
	c.Path = filepath.Join(dir, "config.yaml")
	c.DataDir = dir
	c.TrustedCIDRs = []string{"127.0.0.0/8"}
	c.ScanHosts = []string{"127.0.0.1"}
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}

	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				c.ResolveScanHosts()
			}
		}
	}()
	var saveErr error
	for i := 0; i < 50; i++ {
		if saveErr = c.Save(); saveErr != nil {
			break
		}
	}
	close(stop)
	<-done
	if saveErr != nil {
		t.Fatal(saveErr)
	}
	loaded, err := Load(c.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.InTrustedIP(net.ParseIP("127.0.0.1")) {
		t.Fatal("saved scan_hosts was not restored")
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
