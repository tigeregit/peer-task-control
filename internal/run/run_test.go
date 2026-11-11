package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tigeregit/peer-task-control/internal/caps"
	"github.com/tigeregit/peer-task-control/internal/resource"
	"github.com/tigeregit/peer-task-control/internal/workflow"
)

func TestSubmitSeedsWithoutWorkspaceOrExecution(t *testing.T) {
	loaded, err := workflow.LoadJob(filepath.Join("..", "..", "testdata", "seed", "workflow.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	worktrees := filepath.Join(data, "must-not-exist")
	res, err := Submit(loaded, Options{DataDir: data, WorktreesDir: worktrees, Detect: func() caps.Info {
		t.Fatal("seed-only task checked execution capabilities")
		return caps.Info{}
	}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "success" || res.Worktree != "" || res.PackageDir != "" || res.Digest != "" || res.Task.Worktree != nil {
		t.Fatalf("unexpected execution state: %+v", res)
	}
	if _, err := os.Stat(worktrees); !os.IsNotExist(err) {
		t.Fatalf("workspace root created: %v", err)
	}
	inv := resource.Inventory(data)
	if len(inv) != 2 {
		t.Fatalf("wanted directory and blob replicas, got %+v", inv)
	}
	if len(res.Task.Jobs[0].Steps) != 1 || res.Task.Jobs[0].Steps[0].ID != "seed" {
		t.Fatal("unexpected execution or package step")
	}
	if _, err := os.Stat(res.TaskPath); err != nil {
		t.Fatal(err)
	}
}

func TestSeedOnlyRejectsCorruptResourceAndCancellation(t *testing.T) {
	loaded, err := workflow.LoadJob(filepath.Join("..", "..", "testdata", "seed", "workflow.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}
	loaded.Prepare.Seeds = loaded.Prepare.Seeds[:1]
	loaded.Prepare.Seeds[0].Digest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	dir := t.TempDir()
	res, err := Submit(loaded, Options{DataDir: dir})
	if err == nil || res.Status != "failure" || len(resource.Inventory(dir)) != 0 {
		t.Fatalf("corrupt resource accepted: %+v, %v", res, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err = Submit(loaded, Options{DataDir: t.TempDir(), Context: ctx})
	if !errors.Is(err, context.Canceled) || res.Status != "failure" {
		t.Fatalf("cancellation lost: %v", err)
	}
}
func TestSubmitLocalFixture(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	name := "workflow.yaml"
	if runtime.GOOS == "windows" {
		name = "workflow.windows.yaml"
	}
	wf := filepath.Join(filepath.Dir(file), "..", "..", "testdata", "submit", name)
	loaded, err := workflow.LoadJob(wf, "")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	res, err := Submit(loaded, Options{
		DataDir: dir,
		Force:   true,
		Detect:  func() caps.Info { return caps.Info{OS: runtime.GOOS} },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "success" || res.Digest == "" {
		t.Fatalf("status=%s digest=%s", res.Status, res.Digest)
	}
	if _, err := os.Stat(filepath.Join(res.PackageDir, "out", "results", "summary.md")); err != nil {
		t.Fatal(err)
	}
	if res.TaskPath == "" {
		t.Fatal("missing task record")
	}
	if res.Task.Worktree == nil {
		t.Fatal("missing worktree")
	}
	if len(res.Task.Worktree.Seeds) != 2 {
		t.Fatalf("seeds=%d", len(res.Task.Worktree.Seeds))
	}
	inv := resource.Inventory(dir)
	names := map[string]bool{}
	for _, s := range inv {
		names[s.Name] = true
	}
	for _, seed := range res.Task.Worktree.Seeds {
		if seed.Digest == "" {
			t.Fatalf("seed %s missing digest", seed.Name)
		}
		if !names[seed.Name] {
			t.Fatalf("inventory missing seed %s: %+v", seed.Name, names)
		}
	}
}
