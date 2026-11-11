package config

import (
	"path/filepath"
	"testing"

	"github.com/tigeregit/peer-task-control/internal/quota"
)

func TestStorageDefaultsAndSmallLimit(t *testing.T) {
	cfg := Defaults()
	cfg.DataDir = t.TempDir()
	cfg.TrustedCIDRs = []string{"127.0.0.0/8"}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	if int64(cfg.Storage.SizeLimit) != 40*quota.GB || int64(cfg.Storage.DeleteSize) != 20*quota.GB || cfg.Storage.WorkspaceLimit != 0 {
		t.Fatalf("unexpected defaults: %+v", cfg.Storage)
	}
	cfg.Storage.SizeLimit, cfg.Storage.DeleteSize = 10*quota.Bytes(quota.GB), 0
	if err := cfg.Normalize(); err != nil || int64(cfg.Storage.DeleteSize) != 5*quota.GB {
		t.Fatalf("small limit did not retain the default ratio: %+v, %v", cfg.Storage, err)
	}
	cfg.Storage.DeleteSize = cfg.Storage.SizeLimit + 1
	if err := cfg.Normalize(); err == nil {
		t.Fatal("accepted deletion larger than the storage limit")
	}
}

func TestStorageRejectsWorkspaceAliasesAndNestedStores(t *testing.T) {
	cfg := Defaults()
	cfg.DataDir = t.TempDir()
	for _, path := range []string{
		cfg.SeedsDir(), cfg.PackagesDir(), filepath.Join(cfg.PackagesDir(), "job"),
		filepath.Join(cfg.SeedsDir(), ".reservations"), cfg.RunsDir(),
	} {
		cfg.Storage.WorktreesDir = path
		if err := cfg.ValidateStorage(); err == nil {
			t.Fatalf("accepted unsafe workspace %s", path)
		}
	}
	cfg.Storage.WorktreesDir = filepath.Join(cfg.DataDir, "workspaces")
	if err := cfg.ValidateStorage(); err != nil {
		t.Fatal(err)
	}
	cfg.Storage.SeedsDir = filepath.Join(cfg.Storage.WorktreesDir, "seeding")
	if err := cfg.ValidateStorage(); err == nil {
		t.Fatal("accepted seeding directories nested in the workspace root")
	}
}
