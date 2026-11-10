package dispatch

import (
	"testing"
	"time"
)

func TestQueueFIFOAndCancel(t *testing.T) {
	q, err := OpenQueue(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	j1 := &Job{JobID: "a", JobName: "test"}
	j2 := &Job{JobID: "b", JobName: "test"}
	if err := q.Enqueue(j1, []byte("one")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := q.Enqueue(j2, []byte("two")); err != nil {
		t.Fatal(err)
	}
	pending := q.Pending()
	if len(pending) != 2 || pending[0].JobID != "a" || pending[1].JobID != "b" {
		t.Fatalf("fifo: %+v", pending)
	}
	if _, err := q.Cancel("a"); err != nil {
		t.Fatal(err)
	}
	pending = q.Pending()
	if len(pending) != 1 || pending[0].JobID != "b" {
		t.Fatalf("after cancel: %+v", pending)
	}
	got, ok := q.Get("a")
	if !ok || got.Status != StatusCancelled {
		t.Fatalf("got %+v", got)
	}
	// persist across reopen
	q2, err := OpenQueue(q.Dir())
	if err != nil {
		t.Fatal(err)
	}
	got, ok = q2.Get("a")
	if !ok || got.Status != StatusCancelled {
		t.Fatalf("reopen %+v", got)
	}
	if len(q2.Pending()) != 1 {
		t.Fatalf("pending after reopen: %+v", q2.Pending())
	}
}

func TestQueueResumeRunning(t *testing.T) {
	dir := t.TempDir()
	q, _ := OpenQueue(dir)
	j := &Job{JobID: "r1", Status: StatusQueued}
	if err := q.Enqueue(j, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := q.MarkRunning("r1", "run-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	q2, err := OpenQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := q2.Get("r1")
	if got.Status != StatusQueued {
		t.Fatalf("expected re-queue after crash, got %s", got.Status)
	}
}
