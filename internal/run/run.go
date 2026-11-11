package run

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tigeregit/peer-task-control/internal/caps"
	"github.com/tigeregit/peer-task-control/internal/cas"
	"github.com/tigeregit/peer-task-control/internal/config"
	"github.com/tigeregit/peer-task-control/internal/quota"
	"github.com/tigeregit/peer-task-control/internal/resource"
	"github.com/tigeregit/peer-task-control/internal/taskboard"
	"github.com/tigeregit/peer-task-control/internal/workflow"
	"github.com/tigeregit/peer-task-control/internal/worktreeguard"
)

type Options struct {
	skipSeeds       map[string]bool
	Storage         config.Storage
	InspectResource func(context.Context, string) (resource.Info, error)
	EnsureSpace     func(context.Context, int64) error
	DataDir         string
	// SeedsDir is the parent of cas/ and packages/. Empty means DataDir.
	SeedsDir string
	// WorktreesDir is the root of per-run worktrees. Empty means DataDir/worktrees.
	WorktreesDir string
	// RunsDir holds run records. Empty means DataDir/runs.
	RunsDir string
	// Force skips the NVIDIA / exec host check (tests and explicit override).
	Force bool
	// KeepWorktree is rejected: terminal workspaces are always reclaimed.
	KeepWorktree bool
	Stdout       io.Writer
	// Now is optional; tests may override.
	Now func() time.Time
	// Detect is optional; defaults to caps.Detect.
	Detect func() caps.Info
	// Context bounds Prepare fetches and run steps. Defaults to Background.
	Context context.Context
	// Start fixes the run start time. Zero means Now().
	Start time.Time
	// RunID overrides the record id (mesh queue assigns one at enqueue).
	RunID string
	// DeferRecord lets the dispatch queue write the final, coordinated status.
	DeferRecord bool
	// Fetch downloads a seed by digest from the mesh (closest replica).
	// Nil means digest seeds must already be on this node.
	Fetch func(ctx context.Context, digest string) (resource.Blob, error)
	// SeedOverrides replaces how named seeds are retrieved. Mesh dispatch uses
	// it to point seeds at inline bundle content or at a published digest.
	SeedOverrides map[string]SeedSource
	// Dispatch, when set, marks the run as received over the mesh.
	Dispatch *DispatchInfo
}

// SeedSource is where a seed comes from instead of its Prepare declaration.
type SeedSource struct {
	// Dir is a local file or directory already verified against Digest.
	Dir    string
	Digest string
}

// DispatchInfo records who sent a mesh-dispatched job and who ran it.
type DispatchInfo struct {
	JobID     string
	Submitter taskboard.Party
	Executor  taskboard.Party
}

// TaskID creates a globally distinct run record ID, even across nodes.
func TaskID(start time.Time, job string) (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("generate run id: %w", err)
	}
	return fmt.Sprintf("run-%s-%s-%x", start.UTC().Format("20060102T150405Z"), job, nonce), nil
}

type Result struct {
	ID         string
	Status     string
	Worktree   string
	PackageDir string
	Digest     string
	Task       taskboard.Task
	TaskPath   string
}

func (opt Options) seedsDir() string {
	if opt.SeedsDir != "" {
		return opt.SeedsDir
	}
	return opt.DataDir
}

func (opt Options) packagesDir() string { return filepath.Join(opt.seedsDir(), "packages") }
func (opt Options) casDir() string      { return filepath.Join(opt.seedsDir(), "cas") }

func (opt Options) worktreesDir() string {
	if opt.WorktreesDir != "" {
		return opt.WorktreesDir
	}
	return filepath.Join(opt.DataDir, "worktrees")
}

func (opt Options) runsDir() string {
	if opt.RunsDir != "" {
		return opt.RunsDir
	}
	return filepath.Join(opt.DataDir, "runs")
}

func (opt Options) FetchTmpDir() string {
	return filepath.Join(opt.seedsDir(), ".fetch")
}

func Submit(loaded workflow.Loaded, opt Options) (res Result, retErr error) {
	if opt.Stdout == nil {
		opt.Stdout = io.Discard
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Detect == nil {
		opt.Detect = caps.Detect
	}
	if opt.Context == nil {
		opt.Context = context.Background()
	}
	opt.skipSeeds = map[string]bool{}
	paths := config.Config{DataDir: opt.DataDir, Storage: opt.Storage}
	paths.Storage.SeedsDir, paths.Storage.WorktreesDir, paths.Storage.RunsDir = opt.seedsDir(), opt.worktreesDir(), opt.runsDir()
	if err := paths.ValidateStorage(); err != nil {
		return res, err
	}
	start := opt.Start
	if start.IsZero() {
		start = opt.Now()
	}
	if opt.RunID == "" {
		var err error
		opt.RunID, err = TaskID(start, loaded.JobName)
		if err != nil {
			return Result{}, err
		}
	}
	if loaded.SeedOnly {
		reservation, release, err := opt.reserveStorage(loaded, "", "")
		if err != nil {
			return failedSeedAdmission(loaded, start, opt, err)
		}
		defer func() { retErr = errors.Join(retErr, release()) }()
		ctx, cancel := context.WithCancelCause(opt.Context)
		opt.Context = quota.WithReservation(ctx, reservation.ID)
		defer cancel(nil)
		stopMonitor := opt.monitorStorage(cancel)
		defer stopMonitor()
		return submitSeeds(loaded, start, opt)
	}
	id := strings.TrimPrefix(opt.RunID, "run-")
	host, _ := os.Hostname()
	if !opt.Force {
		if err := checkTarget(loaded.Prepare.Target, opt.Detect()); err != nil {
			return Result{}, err
		}
	}

	workRoot := loaded.Prepare.Layout.Root
	if workRoot == "" {
		workRoot = filepath.Join(opt.worktreesDir(), id)
	}
	if opt.KeepWorktree {
		return res, fmt.Errorf("--keep-worktree is incompatible with immediate job workspace reclamation")
	}
	if err := worktreeguard.Child(opt.worktreesDir(), workRoot); err != nil {
		return res, err
	}
	for _, path := range []string{opt.packagesDir(), opt.casDir(), filepath.Join(opt.seedsDir(), "blocks"), opt.FetchTmpDir(), opt.runsDir()} {
		if err := worktreeguard.Separate(workRoot, path); err != nil {
			return res, err
		}
	}
	containsLocks, err := worktreeguard.ContainsLockDir(opt.worktreesDir(), workRoot)
	if err != nil {
		return Result{}, err
	}
	if containsLocks {
		return Result{}, fmt.Errorf("worktree cleanup would remove the worktree lock directory; use a different layout.root")
	}
	guard, err := worktreeguard.TryLock(opt.worktreesDir(), workRoot)
	if err != nil {
		return Result{}, fmt.Errorf("guard worktree %s: %w", workRoot, err)
	}
	defer func() { retErr = errors.Join(retErr, guard.Close()) }()
	pkgDir := filepath.Join(opt.packagesDir(), id)
	runsDir := opt.runsDir()
	store := cas.New(opt.casDir())

	res = Result{ID: opt.RunID, Worktree: workRoot, PackageDir: pkgDir}
	job := taskboard.Job{ID: loaded.JobName, Name: taskboard.Text{ZH: loaded.JobName, EN: loaded.JobName}, StartedAt: taskboard.PtrTime(start)}
	var recSteps []taskboard.Step

	reservation, release, err := opt.reserveStorage(loaded, workRoot, pkgDir)
	var prepStep taskboard.Step
	var seeds []taskboard.WorktreeSeed
	var cancelStorage context.CancelCauseFunc
	if err == nil {
		defer func() { retErr = errors.Join(retErr, release()) }()
		ctx, cancel := context.WithCancelCause(opt.Context)
		cancelStorage = cancel
		opt.Context = quota.WithReservation(ctx, reservation.ID)
		defer cancel(nil)
		stopMonitor := opt.monitorStorage(cancel)
		defer stopMonitor()
		defer func() {
			if err := os.RemoveAll(workRoot); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("reclaim terminal workspace: %w", err))
				res.Status = "failure"
				res.Task.Status = "failure"
				if res.TaskPath != "" {
					_, writeErr := writeTaskIfNeeded(runsDir, res.Task, opt.DeferRecord)
					retErr = errors.Join(retErr, writeErr)
				}
			}
		}()
		prepStep, seeds, err = runPrepare(loaded, workRoot, store, start, opt)
		if err == nil {
			reservation.SeedBytes, reservation.WorkspaceBytes = 0, 0
			for _, seed := range seeds {
				if seed.Digest != "" {
					reservation.Digests = append(reservation.Digests, seed.Digest)
				}
			}
			err = quota.SaveReservation(opt.seedsDir(), reservation)
		}
		if err == nil {
			err = opt.checkStorage()
		}
	} else {
		prepStep = taskboard.Step{ID: "prepare", Status: "failure", Log: err.Error()}
	}
	recSteps = append(recSteps, prepStep)
	if err != nil {
		prepStep.Status = "failure"
		recSteps[len(recSteps)-1] = prepStep
		job.Status = "failure"
		job.Steps = recSteps
		job.EndedAt = taskboard.PtrTime(opt.Now())
		res.Status = "failure"
		res.Task = finishTask(loaded, host, start, opt.Now(), res.Status, "prepare failed: "+err.Error(), "", job, snapshotWorktree(loaded, workRoot, seeds), opt.Dispatch, opt.RunID)
		var recordErr error
		res.TaskPath, recordErr = writeTaskIfNeeded(runsDir, res.Task, opt.DeferRecord)
		return res, errors.Join(err, recordErr)
	}

	runFailed := error(nil)
	for _, st := range loaded.RunSteps {
		if err := context.Cause(opt.Context); err != nil {
			runFailed = err
			break
		}
		s, err := runCommand(loaded, workRoot, st, opt)
		recSteps = append(recSteps, s)
		if err == nil {
			err = opt.checkStorage()
			if err != nil {
				cancelStorage(err)
			}
		}
		if err != nil {
			runFailed = errors.Join(err, context.Cause(opt.Context))
			break
		}
	}

	var pkgStep taskboard.Step
	var digest string
	pkgErr := context.Cause(opt.Context)
	if pkgErr == nil {
		pkgStep, digest, pkgErr = runPackage(loaded, workRoot, pkgDir, store, runFailed == nil, opt)
		pkgErr = errors.Join(pkgErr, context.Cause(opt.Context))
		if pkgErr == nil {
			pkgErr = opt.checkStorage()
		}
	} else {
		pkgStep = taskboard.Step{ID: "package-result", Status: "failure", Log: pkgErr.Error()}
	}
	recSteps = append(recSteps, pkgStep)
	res.Digest = digest

	end := opt.Now()
	job.Steps = recSteps
	job.EndedAt = taskboard.PtrTime(end)
	switch {
	case runFailed != nil:
		job.Status = "failure"
		res.Status = "failure"
	case pkgErr != nil:
		job.Status = "failure"
		res.Status = "failure"
	default:
		job.Status = "success"
		res.Status = "success"
	}
	summary := fmt.Sprintf("job %s %s", loaded.JobName, res.Status)
	if digest != "" {
		summary += "; result seed " + digest
	}
	if runFailed != nil {
		summary = "run failed: " + runFailed.Error()
	} else if pkgErr != nil {
		summary = "package failed: " + pkgErr.Error()
	}
	res.Task = finishTask(loaded, host, start, end, res.Status, summary, digest, job, snapshotWorktree(loaded, workRoot, seeds), opt.Dispatch, opt.RunID)
	var werr error
	res.TaskPath, werr = writeTaskIfNeeded(runsDir, res.Task, opt.DeferRecord)
	if res.Status == "success" {
		return res, werr
	}
	if runFailed != nil {
		return res, runFailed
	}
	if pkgErr != nil {
		return res, pkgErr
	}
	return res, werr
}

func checkTarget(t workflow.Target, info caps.Info) error {
	if t.RequiresNVIDIA() && !info.NVIDIA {
		return fmt.Errorf("this machine has no NVIDIA GPU; CUDA jobs cannot run here (use a node with exec, or pass --force only for non-CUDA checks)")
	}
	if t.DeniesNonExec() && t.RequiresNVIDIA() && !info.NVIDIA {
		return fmt.Errorf("target.deny_non_exec: refusing to run on a non-exec host")
	}
	return nil
}

func runPrepare(loaded workflow.Loaded, workRoot string, store *cas.Store, start time.Time, opt Options) (taskboard.Step, []taskboard.WorktreeSeed, error) {
	st := taskboard.Step{ID: "prepare", Name: taskboard.Text{ZH: "准备工作树", EN: "Prepare worktree"}, StartedAt: taskboard.PtrTime(start)}
	var seeds []taskboard.WorktreeSeed
	var log bytes.Buffer
	fmt.Fprintf(&log, "worktree %s\n", workRoot)
	if err := os.MkdirAll(workRoot, 0o755); err != nil {
		st.Status, st.Log, st.EndedAt = "failure", log.String()+"\n"+err.Error(), taskboard.PtrTime(opt.Now())
		return st, seeds, err
	}
	for _, d := range loaded.Prepare.Layout.Directories {
		p := filepath.Join(workRoot, filepath.FromSlash(d.Path))
		if err := os.MkdirAll(p, 0o755); err != nil {
			st.Status, st.Log, st.EndedAt = "failure", log.String()+"\n"+err.Error(), taskboard.PtrTime(opt.Now())
			return st, seeds, err
		}
		fmt.Fprintf(&log, "dir %s writable=%v\n", d.Path, d.IsWritable())
	}
	for _, seed := range loaded.Prepare.Seeds {
		if opt.skipSeeds[seed.Name] {
			fmt.Fprintf(&log, "optional seed %s skipped: metadata unavailable at admission\n", seed.Name)
			continue
		}
		dest := filepath.Join(workRoot, filepath.FromSlash(seed.Dest))
		ws := taskboard.WorktreeSeed{Name: seed.Name, Dest: seed.Dest, Class: seed.Class}
		if err := materializeSeed(loaded.Dir, seed, dest, store, &log, opt); err != nil {
			seeds = append(seeds, ws)
			if seed.IsRequired() {
				st.Status, st.Log, st.EndedAt = "failure", log.String(), taskboard.PtrTime(opt.Now())
				return st, seeds, fmt.Errorf("seed %s: %w", seed.Name, err)
			}
			fmt.Fprintf(&log, "optional seed %s skipped: %v\n", seed.Name, err)
			continue
		}
		original := seed.Digest
		if override, ok := opt.SeedOverrides[seed.Name]; ok && override.Digest != "" {
			original = override.Digest
		}
		if _, _, retained, ok := resource.FindByDigest(opt.packagesDir(), opt.casDir(), original); ok {
			ws.Digest, ws.Size, ws.Files = retained.Digest, retained.Size, retained.FileCount
		} else if pub, err := resource.PublishTreeContext(opt.Context, opt.packagesDir(), seed.Name, dest, start); err != nil {
			st.Status, st.Log, st.EndedAt = "failure", log.String()+"\n"+err.Error(), taskboard.PtrTime(opt.Now())
			return st, seeds, fmt.Errorf("publish seed %s: %w", seed.Name, err)
		} else {
			ws.Digest = pub.Digest
			ws.Size = pub.Size
			ws.Files = pub.FileCount
			fmt.Fprintf(&log, "seed %s published %s (%d files, %d bytes)\n", seed.Name, pub.Digest, ws.Files, pub.Size)
		}
		seeds = append(seeds, ws)
	}
	st.Status, st.Log, st.EndedAt = "success", log.String(), taskboard.PtrTime(opt.Now())
	fmt.Fprint(opt.Stdout, st.Log)
	return st, seeds, nil
}

func materializeSeed(base string, seed workflow.Seed, dest string, store *cas.Store, log *bytes.Buffer, opt Options) error {
	if ov, ok := opt.SeedOverrides[seed.Name]; ok {
		switch {
		case ov.Dir != "":
			if err := cas.CopyPrivateContext(opt.Context, ov.Dir, dest); err != nil {
				return err
			}
			fmt.Fprintf(log, "seed %s inline %s -> %s\n", seed.Name, ov.Digest, dest)
			return nil
		case ov.Digest != "":
			return materializeDigest(seed.Name, ov.Digest, dest, store, log, opt)
		}
	}
	switch {
	case seed.Local != nil && seed.Local.Src != "":
		src := seed.Local.Src
		if !filepath.IsAbs(src) {
			src = filepath.Join(base, src)
		}
		if _, err := os.Stat(src); err != nil && seed.Digest != "" {
			fmt.Fprintf(log, "seed %s local %s missing; using digest %s\n", seed.Name, src, seed.Digest)
			return materializeDigest(seed.Name, seed.Digest, dest, store, log, opt)
		}
		if err := cas.CopyPrivateContext(opt.Context, src, dest); err != nil {
			return err
		}
		info, err := os.Stat(src)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			sum, err := store.PutFile(src)
			if err == nil {
				fmt.Fprintf(log, "seed %s local %s -> %s (%s)\n", seed.Name, src, dest, sum)
				if seed.Digest != "" && seed.Digest != sum {
					return fmt.Errorf("local file digest %s does not match declared %s", sum, seed.Digest)
				}
				return nil
			}
		}
		fmt.Fprintf(log, "seed %s local %s -> %s\n", seed.Name, src, dest)
		return nil
	case seed.Digest != "":
		return materializeDigest(seed.Name, seed.Digest, dest, store, log, opt)
	case seed.CID != "" || (seed.Source != nil && seed.Source.Dataset != ""):
		return fmt.Errorf("CID/dataset source resolution is not supported; use seeds.local or a sha256 digest published on the mesh")
	default:
		return fmt.Errorf("no retrieval method")
	}
}

func runCommand(loaded workflow.Loaded, workRoot string, step workflow.Step, opt Options) (taskboard.Step, error) {
	start := opt.Now()
	name := step.Name
	if name == "" {
		name = "run"
	}
	st := taskboard.Step{ID: step.ID, Name: taskboard.Text{EN: name, ZH: name}, StartedAt: taskboard.PtrTime(start)}
	if strings.TrimSpace(step.Run) == "" {
		st.Status, st.Log, st.EndedAt = "failure", "step has no run command", taskboard.PtrTime(opt.Now())
		return st, fmt.Errorf("step %q has no run command", name)
	}
	cwd := workRoot
	if wd := loaded.Prepare.Workspace.WorkingDirectory; wd != "" && wd != "." {
		cwd = filepath.Join(workRoot, filepath.FromSlash(wd))
	}
	timeout := 2 * time.Hour
	if loaded.Prepare.Workspace.Timeout != "" {
		if d, err := time.ParseDuration(loaded.Prepare.Workspace.Timeout); err == nil {
			timeout = d
		}
	}
	parent := opt.Context
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd, err := shellCommand(ctx, loaded.Prepare.Workspace.Shell, step.Run)
	if err != nil {
		st.Status, st.Log, st.EndedAt = "failure", err.Error(), taskboard.PtrTime(opt.Now())
		return st, err
	}
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), envPairs(loaded, workRoot)...)
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(&buf, opt.Stdout)
	cmd.Stderr = io.MultiWriter(&buf, opt.Stdout)
	err = runProcess(cmd)
	st.Log = buf.String()
	st.EndedAt = taskboard.PtrTime(opt.Now())
	if err != nil {
		st.Status = "failure"
		return st, fmt.Errorf("%s: %w", name, err)
	}
	st.Status = "success"
	return st, nil
}

func shellCommand(ctx context.Context, shell, script string) (*exec.Cmd, error) {
	if shell == "" {
		if runtime.GOOS == "windows" {
			shell = "powershell"
		} else {
			shell = "bash"
		}
	}
	switch strings.ToLower(shell) {
	case "bash", "sh":
		bin, err := lookupUnixShell(shell)
		if err != nil {
			return nil, err
		}
		return exec.CommandContext(ctx, bin, "-lc", script), nil
	case "pwsh", "powershell":
		bin, err := lookupPowerShell(shell)
		if err != nil {
			return nil, err
		}
		return exec.CommandContext(ctx, bin, "-NoProfile", "-Command", script), nil
	case "cmd":
		return exec.CommandContext(ctx, "cmd", "/C", script), nil
	default:
		return nil, fmt.Errorf("unsupported shell %q", shell)
	}
}

func lookupPowerShell(want string) (string, error) {
	if want == "powershell" {
		if bin, err := exec.LookPath("powershell"); err == nil {
			return bin, nil
		}
		if bin, err := exec.LookPath("pwsh"); err == nil {
			return bin, nil
		}
	} else {
		if bin, err := exec.LookPath("pwsh"); err == nil {
			return bin, nil
		}
		if bin, err := exec.LookPath("powershell"); err == nil {
			return bin, nil
		}
	}
	return "", fmt.Errorf("no usable PowerShell found (looked for pwsh and powershell)")
}

func lookupUnixShell(want string) (string, error) {
	candidates := []string{want}
	if want == "bash" {
		candidates = append(candidates, "sh")
	}
	var found []string
	for _, name := range candidates {
		if bin, err := exec.LookPath(name); err == nil {
			found = append(found, bin)
		}
	}
	if runtime.GOOS == "windows" {
		found = append(found,
			`C:\Program Files\Gitinash.exe`,
			`C:\Program Files (x86)\Gitinash.exe`,
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Git", "bin", "bash.exe"),
		)
	}
	for _, bin := range found {
		if usableUnixShell(bin) {
			return bin, nil
		}
	}
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("workspace.shell is %s, but no usable bash was found; Windows System32 bash is the WSL stub and is skipped. Install Git Bash or set workspace.shell to powershell or cmd", want)
	}
	return "", fmt.Errorf("no usable %s in PATH", want)
}

func usableUnixShell(bin string) bool {
	if bin == "" {
		return false
	}
	if _, err := os.Stat(bin); err != nil {
		return false
	}
	if runtime.GOOS != "windows" {
		return true
	}
	norm := strings.ToLower(filepath.ToSlash(filepath.Clean(bin)))
	// WSL stubs: System32 bash.exe and the Microsoft Store WindowsApps shim.
	for _, bad := range []string{"/windows/system32/", "/windows/syswow64/", "/windows/sysnative/", "/windowsapps/"} {
		if strings.Contains(norm, bad) {
			return false
		}
	}
	return true
}

func envPairs(loaded workflow.Loaded, workRoot string) []string {
	in := filepath.Join(workRoot, "in")
	out := filepath.Join(workRoot, "out")
	tmp := filepath.Join(workRoot, "tmp")
	pairs := []string{
		"PTC_WORKTREE=" + workRoot,
		"PTC_IN=" + in,
		"PTC_OUT=" + out,
		"PTC_TMP=" + tmp,
		"PTC_JOB=" + loaded.JobName,
	}
	if ep := loaded.Prepare.Workspace.Entrypoint; ep != "" {
		pairs = append(pairs, "PTC_ENTRYPOINT="+filepath.Join(workRoot, filepath.FromSlash(ep)))
	}
	for k, v := range loaded.Prepare.Workspace.Env {
		pairs = append(pairs, k+"="+v)
	}
	return pairs
}

func runPackage(loaded workflow.Loaded, workRoot, pkgDir string, store *cas.Store, runOK bool, opt Options) (step taskboard.Step, digest string, retErr error) {
	start := opt.Now()
	st := taskboard.Step{ID: "package-result", Name: taskboard.Text{ZH: "打包结果", EN: "Package result"}, StartedAt: taskboard.PtrTime(start)}
	var log bytes.Buffer
	items := workflow.CollectItems(loaded.Package, loaded.Prepare)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		st.Status, st.Log, st.EndedAt = "failure", err.Error(), taskboard.PtrTime(opt.Now())
		return st, "", err
	}
	committed := false
	defer func() {
		if !committed {
			retErr = errors.Join(retErr, os.RemoveAll(pkgDir))
		}
	}()
	var missing []string
	for _, it := range items {
		src := filepath.Join(workRoot, filepath.FromSlash(it.Path))
		dst := filepath.Join(pkgDir, filepath.FromSlash(it.Path))
		if _, err := os.Stat(src); err != nil {
			msg := fmt.Sprintf("missing %s (%s)", it.Path, err)
			fmt.Fprintln(&log, msg)
			if it.Required && runOK {
				missing = append(missing, it.Path)
			}
			continue
		}
		if err := cas.CopyPrivateContext(opt.Context, src, dst); err != nil {
			st.Status, st.Log, st.EndedAt = "failure", log.String()+"\n"+err.Error(), taskboard.PtrTime(opt.Now())
			return st, "", err
		}
		fmt.Fprintf(&log, "collect %s -> %s\n", it.Path, dst)
	}
	if len(missing) > 0 {
		err := fmt.Errorf("required collect paths missing: %s", strings.Join(missing, ", "))
		st.Status, st.Log, st.EndedAt = "failure", log.String(), taskboard.PtrTime(opt.Now())
		return st, "", err
	}
	seedName := ""
	if loaded.Package.Publish.Seed != nil {
		seedName = loaded.Package.Publish.Seed.Name
	}
	runOKVal := runOK
	if err := opt.Context.Err(); err != nil {
		return st, "", context.Cause(opt.Context)
	}
	pub, err := resource.CommitTree(pkgDir, seedName, start, resource.CommitOpts{Format: loaded.Package.Package.Format, RunOK: &runOKVal, Pin: loaded.Package.Publish.Pin})
	if err != nil {
		st.Status, st.Log, st.EndedAt = "failure", log.String()+"\n"+err.Error(), taskboard.PtrTime(opt.Now())
		return st, "", err
	}
	committed = true
	digest = pub.Digest
	fmt.Fprintf(&log, "result seed %s\n", digest)
	st.Status, st.Log, st.EndedAt = "success", log.String(), taskboard.PtrTime(opt.Now())
	fmt.Fprint(opt.Stdout, st.Log)
	return st, digest, nil
}

func snapshotWorktree(loaded workflow.Loaded, workRoot string, seeds []taskboard.WorktreeSeed) *taskboard.Worktree {
	wt := &taskboard.Worktree{
		Root:             workRoot,
		WorkingDirectory: loaded.Prepare.Workspace.WorkingDirectory,
		Entrypoint:       loaded.Prepare.Workspace.Entrypoint,
		Seeds:            seeds,
	}
	for _, d := range loaded.Prepare.Layout.Directories {
		wt.Directories = append(wt.Directories, taskboard.WorktreeDir{Path: d.Path, Class: d.Class, Writable: d.IsWritable()})
	}
	for _, o := range loaded.Prepare.Outputs {
		wt.Outputs = append(wt.Outputs, taskboard.WorktreeOutput{Name: o.Name, Path: o.Path, Class: o.Class, Summary: o.Summary})
	}
	return wt
}

func finishTask(loaded workflow.Loaded, host string, start, end time.Time, status, summary, digest string, job taskboard.Job, wt *taskboard.Worktree, d *DispatchInfo, runID string) taskboard.Task {
	name := loaded.Prepare.Metadata.Name
	if name == "" {
		name = loaded.Workflow.Name
	}
	if name == "" {
		name = loaded.JobName
	}
	ann := []taskboard.Annotation{{
		Level:   "notice",
		Message: taskboard.Text{ZH: "本机执行（ptc submit 未指定 --target）。", EN: "Ran locally (ptc submit without --target)."},
	}}
	actor, target, source, event := host, host, "local-submit", "workflow_dispatch"
	var submitter *taskboard.Party
	if d != nil {
		sub := d.Submitter.Node
		if sub == "" {
			sub = d.Submitter.PeerID
		}
		exe := d.Executor.Node
		if exe == "" {
			exe = host
		}
		actor, target, source, event = sub, exe, "mesh-dispatch", "mesh_dispatch"
		p := d.Submitter
		submitter = &p
		ann = []taskboard.Annotation{{
			Level:   "notice",
			Message: taskboard.Text{ZH: "由 " + sub + " 经网格下发，在 " + exe + " 执行（作业 " + d.JobID + "）。", EN: "Dispatched over the mesh by " + sub + "; ran on " + exe + " (job " + d.JobID + ")."},
		}}
	}
	if digest != "" {
		ann = append(ann, taskboard.Annotation{Level: "notice", Message: taskboard.Text{ZH: "结果种子 " + digest, EN: "Result seed " + digest}})
	}
	t := taskboard.Task{
		ID:          runID,
		Name:        taskboard.Text{ZH: name, EN: name},
		Status:      status,
		Event:       event,
		Actor:       actor,
		Target:      target,
		StartedAt:   taskboard.PtrTime(start),
		EndedAt:     taskboard.PtrTime(end),
		Summary:     taskboard.Text{ZH: summary, EN: summary},
		Annotations: ann,
		Jobs:        []taskboard.Job{job},
		Source:      source,
		Submitter:   submitter,
		Worktree:    wt,
	}
	if d != nil {
		t.Node, t.PeerID = d.Executor.Node, d.Executor.PeerID
	}
	return t
}

func writeTask(dir string, t taskboard.Task) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	path := filepath.Join(dir, t.ID+".json")
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, b, 0o644)
}

func writeTaskIfNeeded(dir string, t taskboard.Task, deferRecord bool) (string, error) {
	if deferRecord {
		return "", nil
	}
	return writeTask(dir, t)
}
