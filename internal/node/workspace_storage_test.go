package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tigeregit/peer-task-control/internal/config"
	"github.com/tigeregit/peer-task-control/internal/taskboard"
	"github.com/tigeregit/peer-task-control/internal/worktreeguard"
)

func TestStorageAdmissionRejectsMismatchedCLIRoots(t *testing.T) {
	n := &Node{Cfg: &config.Config{DataDir: t.TempDir()}}
	if err := n.ValidateStorageRoots(n.Cfg.SeedsDir(), n.Cfg.WorktreesDir()); err != nil {
		t.Fatal(err)
	}
	for _, roots := range [][2]string{
		{"", n.Cfg.WorktreesDir()},
		{t.TempDir(), n.Cfg.WorktreesDir()},
		{n.Cfg.SeedsDir(), t.TempDir()},
	} {
		if err := n.ValidateStorageRoots(roots[0], roots[1]); err == nil {
			t.Fatalf("accepted unrelated CLI storage roots: %v", roots)
		}
	}
}

func TestTerminalWorkspaceRecoveryRejectsOutsideAndActivePaths(t *testing.T) {
	n := &Node{Cfg: &config.Config{DataDir: t.TempDir()}}
	old := filepath.Join(n.Cfg.WorktreesDir(), "old")
	active := filepath.Join(n.Cfg.WorktreesDir(), "active")
	outside := t.TempDir()
	for _, path := range []string{old, active, outside, n.Cfg.RunsDir()} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	guard, err := worktreeguard.TryLock(n.Cfg.WorktreesDir(), active)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	for id, path := range map[string]string{"old": old, "active": active, "outside": outside} {
		b, err := json.Marshal(taskboard.Task{ID: id, Status: "success", Worktree: &taskboard.Worktree{Root: path}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(n.Cfg.RunsDir(), id+".json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := n.reclaimTerminalWorkspaces(); err == nil {
		t.Fatal("outside workspace did not produce an explicit recovery error")
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("completed workspace remains: %v", err)
	}
	for _, path := range []string{active, outside} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("protected path removed: %s, %v", path, err)
		}
	}
}
