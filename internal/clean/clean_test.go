package clean

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/peer-task-control/ptc/internal/config"
	"github.com/peer-task-control/ptc/internal/resource"
)

func testCfg(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	c := config.Defaults()
	c.DataDir = dir
	c.TrustedCIDRs = []string{"127.0.0.0/8"}
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCleanOutputsSkipsPinnedAndKeepsSeeds(t *testing.T) {
	cfg := testCfg(t)
	seedSrc := filepath.Join(t.TempDir(), "scene")
	if err := os.MkdirAll(seedSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seedSrc, "a.txt"), []byte("seed-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	seed, err := resource.PublishTreeTo(cfg.PackagesDir(), "scene", seedSrc, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(cfg.PackagesDir(), "20260101T000000Z-test")
	if err := os.MkdirAll(filepath.Join(pkg, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "out", "r.txt"), []byte("result"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok := true
	if _, err := resource.CommitTree(pkg, "sample-results", time.Now(), resource.CommitOpts{RunOK: &ok, Pin: true}); err != nil {
		t.Fatal(err)
	}

	plans, err := PlanOutputs(cfg, Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || !plans[0].Pinned || plans[0].Allowed {
		t.Fatalf("pinned output should be refused: %+v", plans)
	}
	forced, err := PlanOutputs(cfg, Options{Force: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(forced) != 1 || !forced[0].Allowed {
		t.Fatalf("force should allow: %+v", forced)
	}
	if _, err := Apply(forced); err != nil {
		t.Fatal(err)
	}
	if resource.FindPackageDirAt(cfg.PackagesDir(), seed.Digest) == "" {
		t.Fatal("cleaning outputs deleted a seed package")
	}
}

func TestCleanWorktreesLeavesCAS(t *testing.T) {
	cfg := testCfg(t)
	src := filepath.Join(t.TempDir(), "blob")
	if err := os.WriteFile(src, []byte("hello cas"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resource.PublishTreeTo(cfg.PackagesDir(), "blob", src, time.Now()); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(cfg.WorktreesDir(), "20260101T000000Z-job")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "copy.txt"), []byte("hello cas"), 0o644); err != nil {
		t.Fatal(err)
	}
	plans, err := PlanWorktrees(cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans=%+v", plans)
	}
	if _, err := Apply(plans); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("worktree still present")
	}
	if n := len(resource.InventoryAt(cfg.PackagesDir(), cfg.CASDir())); n == 0 {
		t.Fatal("worktree clean deleted seeds")
	}
}

func TestCleanResourceRefusesActive(t *testing.T) {
	cfg := testCfg(t)
	src := filepath.Join(t.TempDir(), "s")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "f"), []byte("xx"), 0o644); err != nil {
		t.Fatal(err)
	}
	seed, err := resource.PublishTreeTo(cfg.PackagesDir(), "s", src, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	active := map[string][]string{seed.Digest: {"run-1"}}
	plans, err := PlanResources(cfg, Options{IDs: []string{seed.Digest}}, active)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].Allowed {
		t.Fatalf("should refuse active seed: %+v", plans)
	}
}
