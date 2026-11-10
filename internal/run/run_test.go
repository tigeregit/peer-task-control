package run

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/peer-task-control/ptc/internal/caps"
	"github.com/peer-task-control/ptc/internal/resource"
	"github.com/peer-task-control/ptc/internal/workflow"
)

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
