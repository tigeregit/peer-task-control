package disk

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tigeregit/peer-task-control/internal/config"
)

func TestMeasureRealVolume(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello"), []byte("abcdefghij"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.DataDir = dir
	cfg.TrustedCIDRs = []string{"127.0.0.0/8"}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	u := Measure(cfg)
	if len(u.Volumes) == 0 {
		t.Fatal("expected at least one real volume")
	}
	v := u.Volumes[0]
	if v.Total <= 0 || v.Free < 0 || v.Used < 0 {
		t.Fatalf("implausible volume: %+v", v)
	}
	if v.Total < v.Free {
		t.Fatalf("free > total: %+v", v)
	}
	if runtime.GOOS == "windows" && v.Path == "" {
		t.Fatal("windows volume path empty")
	}
}

func TestSplitPackages(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "packages", "seed-scene")
	out := filepath.Join(dir, "packages", "20260101T000000Z-test")
	if err := os.MkdirAll(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, "a"), []byte("aaaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "b"), []byte("bbbbbbbb"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok := true
	if err := os.WriteFile(filepath.Join(out, "manifest.json"), []byte(`{"digest":"sha256:00","run_ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = ok
	cfg := config.Defaults()
	cfg.DataDir = dir
	cfg.TrustedCIDRs = []string{"127.0.0.0/8"}
	_ = cfg.Normalize()
	u := Measure(cfg)
	if u.SeedsBytes < 4 {
		t.Fatalf("seeds=%d", u.SeedsBytes)
	}
	if u.OutputsBytes < 8 {
		t.Fatalf("outputs=%d", u.OutputsBytes)
	}
}
