package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tigeregit/peer-task-control/internal/cas"
	"github.com/tigeregit/peer-task-control/internal/config"
	"github.com/tigeregit/peer-task-control/internal/quota"
	"github.com/tigeregit/peer-task-control/internal/resource"
	"github.com/tigeregit/peer-task-control/internal/workflow"
)

func storageWorkflow(t *testing.T) workflow.Loaded {
	t.Helper()
	name := "workflow.yaml"
	if runtime.GOOS == "windows" {
		name = "workflow.windows.yaml"
	}
	loaded, err := workflow.LoadJob(filepath.Join("..", "..", "testdata", "submit", name), "")
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func TestOversizedPrepareFailsWithoutFetchingOrCreatingWorkspace(t *testing.T) {
	loaded := storageWorkflow(t)
	loaded.Prepare.Seeds = loaded.Prepare.Seeds[:1]
	loaded.Prepare.Seeds[0].Local = nil
	loaded.Prepare.Seeds[0].Digest = "sha256:" + strings.Repeat("a", 64)
	calls := 0
	res, err := Submit(loaded, Options{DataDir: t.TempDir(), Force: true,
		Storage: config.Storage{SizeLimit: 65536, DeleteSize: 32768},
		InspectResource: func(context.Context, string) (resource.Info, error) {
			return resource.Info{Seed: resource.Seed{Size: 65537}}, nil
		},
		Fetch: func(context.Context, string) (resource.Blob, error) {
			calls++
			t.Fatal("oversized Prepare started downloading")
			return resource.Blob{}, nil
		},
	})
	if err == nil || res.Status != "failure" || calls != 0 || !strings.Contains(err.Error(), "before download") {
		t.Fatalf("admission failed incorrectly: %+v, calls=%d, err=%v", res, calls, err)
	}
	if _, err := os.Stat(res.Worktree); !os.IsNotExist(err) {
		t.Fatalf("oversized Prepare constructed a workspace: %v", err)
	}
}

func TestTerminalWorkspacesAreAlwaysReclaimed(t *testing.T) {
	for _, failure := range []bool{false, true} {
		loaded := storageWorkflow(t)
		loaded.Package.Cleanup.EvictWorktree = false
		if failure {
			loaded.RunSteps[0].Run = "exit 7"
		}
		res, err := Submit(loaded, Options{DataDir: t.TempDir(), Force: true})
		if (err != nil) != failure {
			t.Fatalf("failure=%v, err=%v", failure, err)
		}
		if _, err := os.Stat(res.Worktree); !os.IsNotExist(err) {
			t.Fatalf("terminal workspace remains: %v", err)
		}
	}
}

func TestPrepareFailureReclaimsWorkspace(t *testing.T) {
	loaded := storageWorkflow(t)
	loaded.Prepare.Seeds[0].Local = nil
	loaded.Prepare.Seeds[0].Digest = "sha256:" + strings.Repeat("a", 64)
	res, err := Submit(loaded, Options{DataDir: t.TempDir(), Force: true,
		InspectResource: func(context.Context, string) (resource.Info, error) {
			return resource.Info{Seed: resource.Seed{Size: 17}}, nil
		},
		Fetch: func(context.Context, string) (resource.Blob, error) {
			return resource.Blob{}, errors.New("injected Prepare fetch failure")
		},
	})
	if err == nil || res.Status != "failure" || !strings.Contains(err.Error(), "injected Prepare fetch failure") {
		t.Fatalf("expected Prepare fetch failure: %+v, %v", res, err)
	}
	if _, err := os.Stat(res.Worktree); !os.IsNotExist(err) {
		t.Fatalf("failed Prepare left its workspace: %v", err)
	}
}

func TestCancellationReclaimsRunningWorkspace(t *testing.T) {
	loaded := storageWorkflow(t)
	loaded.Prepare.Seeds = nil
	if runtime.GOOS == "windows" {
		loaded.RunSteps[0].Run = "Write-Output child-started; Start-Sleep -Seconds 30"
	} else {
		loaded.RunSteps[0].Run = "echo child-started; sleep 30"
	}
	loaded.RunSteps = loaded.RunSteps[:1]
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out := &readyOutput{ready: make(chan struct{})}
	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	dir := t.TempDir()
	go func() {
		res, err := Submit(loaded, Options{DataDir: dir, Force: true, Context: ctx, Stdout: out})
		done <- outcome{res, err}
	}()
	select {
	case <-out.ready:
	case result := <-done:
		t.Fatalf("job ended before cancellation: %+v", result)
	case <-ctx.Done():
		t.Fatal("job did not reach command execution")
	}
	cancel()
	select {
	case result := <-done:
		if result.err == nil || result.res.Status != "failure" {
			t.Fatalf("cancelled job reported success: %+v", result)
		}
		if _, err := os.Stat(result.res.Worktree); !os.IsNotExist(err) {
			t.Fatalf("cancelled job left its workspace: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled job did not finish")
	}
}

func TestWorkspaceQuotaCountsOtherWorkspacesAndReservations(t *testing.T) {
	loaded := storageWorkflow(t)
	dir := t.TempDir()
	other := filepath.Join(dir, "worktrees", "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "data"), make([]byte, 8192), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Submit(loaded, Options{DataDir: dir, Force: true, Storage: config.Storage{WorkspaceLimit: 8192}})
	if err == nil || res.Status != "failure" || !strings.Contains(err.Error(), "workspace quota") {
		t.Fatalf("aggregate limit ignored another workspace: %+v, %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(other, "data")); err != nil {
		t.Fatal("admission cleaned another job's workspace")
	}
	if err := os.RemoveAll(other); err != nil {
		t.Fatal(err)
	}
	guard, err := quota.Start(dir, quota.Reservation{ID: "other-job", WorkspaceBytes: 8192, Workspace: other})
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	res, err = Submit(loaded, Options{DataDir: dir, Force: true, Storage: config.Storage{WorkspaceLimit: 8192}})
	if err == nil || res.Status != "failure" {
		t.Fatalf("concurrent workspace reservation ignored: %+v, %v", res, err)
	}
}

func TestWorkspaceGrowthCancelsAndReclaimsJob(t *testing.T) {
	loaded := storageWorkflow(t)
	loaded.Prepare.Seeds = nil
	if runtime.GOOS == "windows" {
		loaded.RunSteps[0].Run = `[System.IO.File]::WriteAllBytes('large.bin', (New-Object byte[] 16384)); Start-Sleep -Seconds 30`
		loaded.Prepare.Workspace.Shell = "pwsh"
	} else {
		loaded.RunSteps[0].Run = "dd if=/dev/zero of=large.bin bs=16384 count=1; sleep 30"
		loaded.Prepare.Workspace.Shell = "sh"
	}
	loaded.RunSteps = loaded.RunSteps[:1]
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := Submit(loaded, Options{DataDir: t.TempDir(), Force: true, Context: ctx, Storage: config.Storage{WorkspaceLimit: 8192}})
	if err == nil || !strings.Contains(err.Error(), "aggregate workspace quota exceeded") || res.Status != "failure" {
		t.Fatalf("runtime limit did not cancel execution: %+v, %v", res, err)
	}
	if _, err := os.Stat(res.Worktree); !os.IsNotExist(err) {
		t.Fatalf("quota-failed job workspace remains: %v", err)
	}
}

func TestPrivateMaterializationDoesNotMutateSeed(t *testing.T) {
	src := filepath.Join(t.TempDir(), "seed")
	dest := filepath.Join(t.TempDir(), "workspace")
	if err := os.WriteFile(src, []byte("immutable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cas.CopyPrivate(src, dest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(src)
	if err != nil || string(got) != "immutable" {
		t.Fatalf("workspace writes mutated seeded data: %q, %v", got, err)
	}
}
