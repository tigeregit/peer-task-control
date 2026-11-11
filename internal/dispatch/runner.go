package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/tigeregit/peer-task-control/internal/run"
	"github.com/tigeregit/peer-task-control/internal/taskboard"
	"github.com/tigeregit/peer-task-control/internal/workflow"
)

// Host is the executing node, used to stamp records and fetch seeds.
type Host struct {
	Name    string
	PeerID  string
	RunsDir string
}

// PrepareRecord writes a queued (or cancelled) run record so ptc tasks and
// the WebUI can see the job before it starts.
func WriteRecord(runsDir string, t taskboard.Task) (string, error) {
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(runsDir, t.ID+".json")
	return path, writeJSON(path, t)
}

func QueuedTask(loaded workflow.Loaded, job Job, exec Host) (taskboard.Task, error) {
	name := loaded.Prepare.Metadata.Name
	if name == "" {
		name = loaded.Workflow.Name
	}
	if name == "" {
		name = loaded.JobName
	}
	start := job.QueuedAt
	id := job.RunID
	if id == "" {
		return taskboard.Task{}, fmt.Errorf("queued job %s has no run id", job.JobID)
	}
	sub := job.Submitter
	actor := sub.Node
	if actor == "" {
		actor = sub.PeerID
	}
	t := taskboard.Task{
		ID:        id,
		Name:      taskboard.Text{ZH: name, EN: name},
		Status:    job.Status,
		Event:     "mesh_dispatch",
		Actor:     actor,
		Target:    exec.Name,
		StartedAt: taskboard.PtrTime(start),
		Summary:   taskboard.Text{ZH: "排队等待 " + exec.Name, EN: "queued on " + exec.Name},
		Annotations: []taskboard.Annotation{{
			Level:   "notice",
			Message: taskboard.Text{ZH: "由 " + actor + " 下发，在 " + exec.Name + " 排队。", EN: "Dispatched by " + actor + "; queued on " + exec.Name + "."},
		}},
		Jobs:      []taskboard.Job{{ID: loaded.JobName, Name: taskboard.Text{ZH: loaded.JobName, EN: loaded.JobName}, Status: job.Status, StartedAt: taskboard.PtrTime(start)}},
		Source:    "mesh-dispatch",
		Node:      exec.Name,
		PeerID:    exec.PeerID,
		Submitter: &sub,
	}
	if job.Status == StatusCancelled {
		end := time.Now().UTC()
		if job.EndedAt != nil {
			end = *job.EndedAt
		}
		t.EndedAt = taskboard.PtrTime(end)
		t.Summary = taskboard.Text{ZH: "已取消", EN: "cancelled"}
	}
	return t, nil
}

// Execute unpacks a bundle, materializes seeds (inline or by digest), and
// runs the same Prepare → steps → Package result path as a local submit.
func Execute(ctx context.Context, bundle []byte, job Job, exec Host, opt run.Options, log io.Writer) (run.Result, error) {
	if log == nil {
		log = io.Discard
	}
	if ctx == nil {
		ctx = context.Background()
	}
	stage := filepath.Join(opt.FetchTmpDir(), "bundle-"+job.JobID)
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return run.Result{}, err
	}
	defer os.RemoveAll(stage)
	u, err := Unpack(bytes.NewReader(bundle), stage)
	if err != nil {
		return run.Result{}, err
	}
	overrides := map[string]run.SeedSource{}
	for name, path := range u.Inline {
		overrides[name] = run.SeedSource{Dir: path, Digest: u.Digests[name]}
	}
	for name, digest := range u.Digests {
		if _, ok := overrides[name]; ok {
			continue
		}
		overrides[name] = run.SeedSource{Digest: digest}
	}
	// Optional seeds marked skip are left to run.Submit (not required).
	start := time.Now().UTC()
	if job.StartedAt != nil && !job.StartedAt.IsZero() {
		start = job.StartedAt.UTC()
	}
	opt.Context = ctx
	opt.Start = start
	opt.RunID = job.RunID
	opt.Stdout = log
	opt.SeedOverrides = overrides
	opt.Dispatch = &run.DispatchInfo{
		JobID:     job.JobID,
		Submitter: job.Submitter,
		Executor:  taskboard.Party{Node: exec.Name, PeerID: exec.PeerID},
	}
	return run.Submit(u.Loaded, opt)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
