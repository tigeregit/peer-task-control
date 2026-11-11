package node

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tigeregit/peer-task-control/internal/control"
	"github.com/tigeregit/peer-task-control/internal/dispatch"
	"github.com/tigeregit/peer-task-control/internal/taskboard"
	"github.com/tigeregit/peer-task-control/internal/workflow"
)

func slowBundle(t *testing.T) []byte {
	t.Helper()
	name := "workflow.yaml"
	old := "run: sh bin/app.sh"
	step := "run: |\n          echo job-started\n          sleep 30"
	if runtime.GOOS == "windows" {
		name = "workflow.windows.yaml"
		old = `run: .\bin\app.ps1`
		step = "run: |\n          Write-Output job-started\n          Start-Sleep -Seconds 30"
	}
	loaded, err := workflow.LoadJob(filepath.Join("..", "..", "testdata", "submit", name), "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(loaded.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("fixture does not contain %q", old)
	}
	path := filepath.Join(t.TempDir(), "workflow.yaml")
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), old, step, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded.Path = path
	bundle, err := dispatch.Build(loaded, dispatch.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return bundle.Data
}

func acceptTestJob(t *testing.T, n *Node, bundle []byte, timeout string) dispatch.Job {
	t.Helper()
	job, _, err := n.acceptBundle(bundle, dispatch.Request{Timeout: timeout}, n.Host.ID().String(), net.ParseIP("127.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func waitJobEvent(t *testing.T, ch <-chan dispatch.Event, want func(dispatch.Event) bool) dispatch.Event {
	t.Helper()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-ch:
			if want(event) {
				return event
			}
			if event.Type == "error" {
				t.Fatalf("job error: %+v", event)
			}
		case <-timer.C:
			t.Fatal("timed out waiting for job event")
		}
	}
}

func readTaskRecord(t *testing.T, n *Node, id string) taskboard.Task {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(n.Cfg.RunsDir(), id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var task taskboard.Task
	if err := json.Unmarshal(b, &task); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestCancelQueuedAndRunningJobs(t *testing.T) {
	bundle := slowBundle(t)
	n := startTestNode(t, "cancel-worker", "127.0.0.1", false, []string{"127.0.0.0/8"})
	submitter := startTestNode(t, "cancel-submitter", "127.0.0.2", false, []string{"127.0.0.0/8"})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := submitter.Probe(ctx, listenHostPort(n)); err != nil {
		t.Fatal(err)
	}
	waitPeers(t, submitter, 2)
	cli := control.NewClient(submitter.APIAddr())
	running := acceptTestJob(t, n, bundle, "")
	runningEvents := n.watch(running.JobID)
	defer n.unwatch(running.JobID, runningEvents)
	n.kickExec()
	waitJobEvent(t, runningEvents, func(e dispatch.Event) bool {
		return e.Type == "log" && strings.Contains(e.Message, "job-started")
	})

	queued := acceptTestJob(t, n, bundle, "")
	queuedEvents := n.watch(queued.JobID)
	defer n.unwatch(queued.JobID, queuedEvents)
	status, err := cli.Cancel(queued.RunID)
	if err != nil || status != dispatch.StatusCancelled {
		t.Fatalf("queued cancel status=%s error=%v", status, err)
	}
	queuedDone := waitJobEvent(t, queuedEvents, func(e dispatch.Event) bool { return e.Type == "done" })
	if task := readTaskRecord(t, n, queued.RunID); queuedDone.Status != dispatch.StatusCancelled || task.Status != "cancelled" {
		t.Fatalf("queued cancellation mismatch: event=%+v task=%+v", queuedDone, task)
	}

	status, err = cli.Cancel(running.RunID)
	if err != nil || status != dispatch.StatusCancelling {
		t.Fatalf("running cancel status=%s error=%v", status, err)
	}
	done := waitJobEvent(t, runningEvents, func(e dispatch.Event) bool { return e.Type == "done" })
	task := readTaskRecord(t, n, running.RunID)
	final, ok := n.queue.Get(running.JobID)
	if !ok || done.Status != dispatch.StatusCancelled || final.Status != dispatch.StatusCancelled || task.Status != "cancelled" ||
		task.Worktree == nil || len(task.Jobs) != 1 || task.Jobs[0].Status != "cancelled" {
		t.Fatalf("running cancellation mismatch: event=%+v job=%+v task=%+v", done, final, task)
	}
	if _, err := n.Cancel(context.Background(), running.RunID); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("finished cancellation should be refused explicitly: %v", err)
	}
}

func TestFinishAndCancelHaveOneConsistentWinner(t *testing.T) {
	n := startTestNode(t, "race-worker", "127.0.0.1", false, []string{"127.0.0.0/8"})
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("race-%d", i)
		job := dispatch.Job{JobID: id, RunID: "run-" + id, Status: dispatch.StatusQueued}
		n.mu.Lock()
		if err := n.queue.Enqueue(&job, []byte("bundle")); err != nil {
			n.mu.Unlock()
			t.Fatal(err)
		}
		if err := n.queue.MarkRunning(id, job.RunID, time.Now()); err != nil {
			n.mu.Unlock()
			t.Fatal(err)
		}
		n.cancels[id] = func() {}
		n.mu.Unlock()
		events := n.watch(id)
		start := make(chan struct{})
		cancelDone := make(chan struct{})
		finishDone := make(chan struct{})
		cancelResult := make(chan error, 1)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			if i%3 == 1 {
				<-finishDone
			}
			_, err := n.CancelLocal(id)
			cancelResult <- err
			close(cancelDone)
		}()
		go func() {
			defer wg.Done()
			<-start
			if i%3 == 0 {
				<-cancelDone
			}
			n.finishJob(job, dispatch.StatusSucceeded, "", "", taskboard.Task{ID: job.RunID})
			close(finishDone)
		}()
		close(start)
		wg.Wait()
		cancelErr := <-cancelResult
		done := waitJobEvent(t, events, func(e dispatch.Event) bool { return e.Type == "done" })
		n.unwatch(id, events)
		final, _ := n.queue.Get(id)
		record := readTaskRecord(t, n, job.RunID)
		if cancelErr == nil {
			if final.Status != dispatch.StatusCancelled || done.Status != dispatch.StatusCancelled || record.Status != "cancelled" {
				t.Fatalf("cancellation lost: job=%+v event=%+v record=%+v", final, done, record)
			}
		} else if final.Status != dispatch.StatusSucceeded || done.Status != dispatch.StatusSucceeded || record.Status != "success" {
			t.Fatalf("completion lost: cancel=%v job=%+v event=%+v record=%+v", cancelErr, final, done, record)
		}
		n.mu.Lock()
		delete(n.cancels, id)
		n.mu.Unlock()
	}
}

func TestTimeoutIsNotRecordedAsCancellation(t *testing.T) {
	bundle := slowBundle(t)
	n := startTestNode(t, "timeout-worker", "127.0.0.1", false, []string{"127.0.0.0/8"})
	job := acceptTestJob(t, n, bundle, "1s")
	events := n.watch(job.JobID)
	defer n.unwatch(job.JobID, events)
	n.kickExec()
	done := waitJobEvent(t, events, func(e dispatch.Event) bool { return e.Type == "done" })
	task := readTaskRecord(t, n, job.RunID)
	if done.Status != dispatch.StatusFailed || !strings.Contains(done.Error, "timed out") || task.Status != "failure" {
		t.Fatalf("timeout was misclassified: event=%+v task=%+v", done, task)
	}
}

func TestCancelledRunRecordRecoveredAfterRestart(t *testing.T) {
	n := startTestNode(t, "restart-worker", "127.0.0.1", false, []string{"127.0.0.0/8"})
	job := dispatch.Job{JobID: "restart-job", RunID: "restart-run", Status: dispatch.StatusQueued}
	n.mu.Lock()
	if err := n.queue.Enqueue(&job, []byte("bundle")); err != nil {
		n.mu.Unlock()
		t.Fatal(err)
	}
	if err := n.queue.MarkRunning(job.JobID, job.RunID, time.Now()); err != nil {
		n.mu.Unlock()
		t.Fatal(err)
	}
	if _, err := n.queue.Cancel(job.JobID); err != nil {
		n.mu.Unlock()
		t.Fatal(err)
	}
	if _, err := dispatch.WriteRecord(n.Cfg.RunsDir(), taskboard.Task{ID: job.RunID, Status: "queued"}); err != nil {
		n.mu.Unlock()
		t.Fatal(err)
	}
	n.mu.Unlock()
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Start(context.Background(), n.Cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	final, _ := restarted.queue.Get(job.JobID)
	record := readTaskRecord(t, restarted, job.RunID)
	if final.Status != dispatch.StatusCancelled || record.Status != "cancelled" {
		t.Fatalf("restart lost cancellation: job=%+v record=%+v", final, record)
	}
	if _, err := os.Stat(restarted.queue.BundlePath(job.JobID)); !os.IsNotExist(err) {
		t.Fatalf("cancelled bundle remained after recovery: %v", err)
	}
}
