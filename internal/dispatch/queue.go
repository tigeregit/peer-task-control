package dispatch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/peer-task-control/ptc/internal/taskboard"
)

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// Job is a persisted queue entry. The bundle lives next to it as <job_id>.tar.
type Job struct {
	JobID     string          `json:"job_id"`
	RunID     string          `json:"run_id,omitempty"`
	JobName   string          `json:"job_name,omitempty"`
	Name      string          `json:"name,omitempty"`
	Status    string          `json:"status"`
	QueuedAt  time.Time       `json:"queued_at"`
	StartedAt *time.Time      `json:"started_at,omitempty"`
	EndedAt   *time.Time      `json:"ended_at,omitempty"`
	Submitter taskboard.Party `json:"submitter"`
	Digest    string          `json:"digest,omitempty"`
	Error     string          `json:"error,omitempty"`
	Timeout   string          `json:"timeout,omitempty"`
}

// Queue is a FIFO of jobs on one exec node, persisted under dir.
type Queue struct {
	dir  string
	mu   sync.Mutex
	jobs map[string]*Job
}

func OpenQueue(dir string) (*Queue, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	q := &Queue{dir: dir, jobs: map[string]*Job{}}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var j Job
		if json.Unmarshal(b, &j) != nil || j.JobID == "" {
			continue
		}
		// A running job left behind by a crash is re-queued so it can be
		// retried or cancelled; the runner treats status=queued as pending.
		if j.Status == StatusRunning {
			j.Status = StatusQueued
			j.StartedAt = nil
			j.Error = "resumed after daemon restart"
			_ = q.write(&j)
		}
		q.jobs[j.JobID] = &j
	}
	return q, nil
}

func (q *Queue) Dir() string { return q.dir }

func (q *Queue) BundlePath(id string) string {
	return filepath.Join(q.dir, id+".tar")
}

func (q *Queue) Enqueue(j *Job, bundle []byte) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if j.JobID == "" {
		return fmt.Errorf("job id is empty")
	}
	if _, exists := q.jobs[j.JobID]; exists {
		return fmt.Errorf("job %s already queued", j.JobID)
	}
	if j.Status == "" {
		j.Status = StatusQueued
	}
	if j.QueuedAt.IsZero() {
		j.QueuedAt = time.Now().UTC()
	}
	if err := os.WriteFile(q.BundlePath(j.JobID), bundle, 0o644); err != nil {
		return err
	}
	if err := q.write(j); err != nil {
		os.Remove(q.BundlePath(j.JobID))
		return err
	}
	cp := *j
	q.jobs[j.JobID] = &cp
	return nil
}

func (q *Queue) Get(id string) (Job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.jobs[id]
	if !ok {
		// also allow lookup by run id
		for _, x := range q.jobs {
			if x.RunID != "" && x.RunID == id {
				return *x, true
			}
		}
		return Job{}, false
	}
	return *j, true
}

func (q *Queue) Pending() []Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []Job
	for _, j := range q.jobs {
		if j.Status == StatusQueued {
			out = append(out, *j)
		}
	}
	sort.Slice(out, func(i, k int) bool {
		if out[i].QueuedAt.Equal(out[k].QueuedAt) {
			return out[i].JobID < out[k].JobID
		}
		return out[i].QueuedAt.Before(out[k].QueuedAt)
	})
	return out
}

func (q *Queue) Running() []Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []Job
	for _, j := range q.jobs {
		if j.Status == StatusRunning {
			out = append(out, *j)
		}
	}
	sort.Slice(out, func(i, k int) bool { return out[i].JobID < out[k].JobID })
	return out
}

func (q *Queue) List() []Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Job, 0, len(q.jobs))
	for _, j := range q.jobs {
		out = append(out, *j)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].QueuedAt.After(out[k].QueuedAt) })
	return out
}

func (q *Queue) MarkRunning(id, runID string, start time.Time) error {
	return q.update(id, func(j *Job) {
		j.Status = StatusRunning
		j.RunID = runID
		t := start.UTC()
		j.StartedAt = &t
		j.Error = ""
	})
}

func (q *Queue) MarkDone(id, status, digest, errMsg string, end time.Time) error {
	return q.update(id, func(j *Job) {
		j.Status = status
		j.Digest = digest
		j.Error = errMsg
		t := end.UTC()
		j.EndedAt = &t
	})
}

// Cancel marks a queued or running job cancelled. The runner observes this
// and stops a running job if it can. Already-finished jobs are refused.
func (q *Queue) Cancel(id string) (Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.lookup(id)
	if !ok {
		return Job{}, fmt.Errorf("job %s not found", id)
	}
	switch j.Status {
	case StatusSucceeded, StatusFailed, StatusCancelled:
		return *j, fmt.Errorf("job %s is already %s", id, j.Status)
	}
	j.Status = StatusCancelled
	t := time.Now().UTC()
	j.EndedAt = &t
	j.Error = "cancelled"
	if err := q.write(j); err != nil {
		return *j, err
	}
	_ = os.Remove(q.BundlePath(j.JobID))
	return *j, nil
}

func (q *Queue) DropBundle(id string) { _ = os.Remove(q.BundlePath(id)) }

func (q *Queue) ReferencedDigests() map[string][]string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := map[string][]string{}
	for _, j := range q.jobs {
		if j.Status != StatusQueued && j.Status != StatusRunning {
			continue
		}
		if j.Digest != "" {
			out[j.Digest] = append(out[j.Digest], j.RunID)
		}
	}
	return out
}

func (q *Queue) update(id string, fn func(*Job)) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, ok := q.lookup(id)
	if !ok {
		return fmt.Errorf("job %s not found", id)
	}
	fn(j)
	return q.write(j)
}

func (q *Queue) lookup(id string) (*Job, bool) {
	if j, ok := q.jobs[id]; ok {
		return j, true
	}
	for _, j := range q.jobs {
		if j.RunID != "" && j.RunID == id {
			return j, true
		}
	}
	return nil, false
}

func (q *Queue) write(j *Job) error {
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(q.dir, j.JobID+".json.tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(q.dir, j.JobID+".json"))
}

func (j Job) Item() QueueItem {
	return QueueItem{
		JobID:     j.JobID,
		RunID:     j.RunID,
		JobName:   j.JobName,
		Status:    j.Status,
		QueuedAt:  ptrOrNil(j.QueuedAt),
		StartedAt: j.StartedAt,
		EndedAt:   j.EndedAt,
		Submitter: j.Submitter,
		Digest:    j.Digest,
		Error:     j.Error,
	}
}

func ptrOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t
	return &u
}
