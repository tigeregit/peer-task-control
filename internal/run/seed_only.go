package run

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tigeregit/peer-task-control/internal/cas"
	"github.com/tigeregit/peer-task-control/internal/resource"
	"github.com/tigeregit/peer-task-control/internal/taskboard"
	"github.com/tigeregit/peer-task-control/internal/workflow"
)

func submitSeeds(loaded workflow.Loaded, start time.Time, opt Options) (Result, error) {
	host, _ := os.Hostname()
	step := taskboard.Step{ID: "seed", Name: taskboard.Text{EN: "Seed resources", ZH: "Seed resources"}, StartedAt: taskboard.PtrTime(start)}
	var log bytes.Buffer
	var seedErr error
	for _, seed := range loaded.Prepare.Seeds {
		if err := opt.Context.Err(); err != nil {
			seedErr = err
			break
		}
		if err := retainSeed(loaded, seed, start, opt, &log); err != nil {
			fmt.Fprintf(&log, "seed %s: %v\n", seed.Name, err)
			if seed.IsRequired() {
				seedErr = fmt.Errorf("seed %s: %w", seed.Name, err)
				break
			}
		}
	}
	end := opt.Now()
	status := "success"
	if seedErr != nil {
		status = "failure"
	}
	step.Status, step.Log, step.EndedAt = status, log.String(), taskboard.PtrTime(end)
	fmt.Fprint(opt.Stdout, step.Log)
	job := taskboard.Job{ID: loaded.JobName, Name: taskboard.Text{EN: loaded.JobName, ZH: loaded.JobName}, Status: status,
		StartedAt: taskboard.PtrTime(start), EndedAt: taskboard.PtrTime(end), Steps: []taskboard.Step{step}}
	summary := "seed-only " + status + "; no workspace or commands"
	rec := finishTask(loaded, host, start, end, status, summary, "", job, nil, opt.Dispatch, opt.RunID)
	rec.Annotations = []taskboard.Annotation{{Level: "notice", Message: taskboard.Text{EN: summary, ZH: summary}}}
	res := Result{ID: rec.ID, Status: status, Task: rec}
	var err error
	res.TaskPath, err = writeTaskIfNeeded(opt.runsDir(), rec, opt.DeferRecord)
	return res, errors.Join(seedErr, err)
}

func retainSeed(loaded workflow.Loaded, seed workflow.Seed, start time.Time, opt Options, log *bytes.Buffer) error {
	if err := os.MkdirAll(opt.packagesDir(), 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(opt.packagesDir(), ".seed-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	dest := filepath.Join(stage, "content")
	store := cas.New(opt.casDir())
	if err := materializeSeed(loaded.Dir, seed, dest, store, log, opt); err != nil {
		return err
	}
	info, err := os.Stat(dest)
	if err != nil {
		return err
	}
	want := seed.Digest
	if ov, ok := opt.SeedOverrides[seed.Name]; ok && ov.Digest != "" {
		want = ov.Digest
	}
	var got string
	if info.IsDir() {
		got, _, err = cas.DirDigest(dest)
	} else {
		got, err = fileSHA256(dest)
	}
	if err != nil {
		return err
	}
	if want != "" && (resource.Normalize(want) == "" || resource.Normalize(want) != got) {
		return fmt.Errorf("digest mismatch: got %s, want %s", got, want)
	}
	if !info.IsDir() {
		if loaded.Prepare.Data.PinPermanent {
			return fmt.Errorf("pin_permanent is supported for directory seeds only")
		}
		_, err = store.PutFile(dest)
		return err
	}
	if existing := resource.FindPackageDirAt(opt.packagesDir(), got); existing != "" {
		if verified, _, err := cas.DirDigest(existing); err == nil && verified == got {
			if loaded.Prepare.Data.PinPermanent {
				if err := resource.SetPin(existing, true); err != nil {
					return err
				}
			}
			fmt.Fprintf(log, "seed %s already retained and advertised as %s\n", seed.Name, got)
			return nil
		}
	}
	// Commit outside the staging folder only after all content is verified.
	final, err := os.MkdirTemp(opt.packagesDir(), "replica-")
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(final)
		}
	}()
	if err := cas.CopyTree(dest, final); err != nil {
		return err
	}
	pub, err := resource.CommitTree(final, seed.Name, start, resource.CommitOpts{Pin: loaded.Prepare.Data.PinPermanent})
	if err != nil {
		return err
	}
	committed = true
	fmt.Fprintf(log, "seed %s retained and advertised as %s\n", seed.Name, pub.Digest)
	return nil
}
