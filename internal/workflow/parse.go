package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Loaded is a workflow plus the Prepare and Package result documents it points to.
type Loaded struct {
	Workflow    Workflow
	Path        string
	Dir         string
	JobName     string
	Job         Job
	Prepare     Prepare
	PreparePath string
	Package     PackageResult
	PackagePath string
	PrepareStep Step
	PackageStep Step
	RunSteps    []Step
}

func LoadWorkflow(path string) (Workflow, error) {
	var wf Workflow
	if err := readYAML(path, &wf); err != nil {
		return wf, err
	}
	if len(wf.Jobs) == 0 {
		return wf, fmt.Errorf("workflow %s has no jobs", path)
	}
	return wf, nil
}

func LoadPrepare(path string) (Prepare, error) {
	var p Prepare
	if err := readYAML(path, &p); err != nil {
		return p, err
	}
	if p.APIVersion == "" {
		p.APIVersion = "ptc/v0"
	}
	if p.Kind == "" {
		p.Kind = "Prepare"
	}
	if p.Kind != "Prepare" {
		return p, fmt.Errorf("%s: kind must be Prepare, got %q", path, p.Kind)
	}
	if p.APIVersion != "ptc/v0" {
		return p, fmt.Errorf("%s: unsupported apiVersion %q", path, p.APIVersion)
	}
	applyPrepareDefaults(&p)
	return p, nil
}

func LoadPackageResult(path string) (PackageResult, error) {
	var p PackageResult
	if err := readYAML(path, &p); err != nil {
		return p, err
	}
	if p.APIVersion == "" {
		p.APIVersion = "ptc/v0"
	}
	if p.Kind == "" {
		p.Kind = "PackageResult"
	}
	if p.Kind != "PackageResult" {
		return p, fmt.Errorf("%s: kind must be PackageResult, got %q", path, p.Kind)
	}
	if p.APIVersion != "ptc/v0" {
		return p, fmt.Errorf("%s: unsupported apiVersion %q", path, p.APIVersion)
	}
	if p.Package.Format == "" {
		p.Package.Format = "unixfs"
	}
	if p.Cleanup.When == "" {
		p.Cleanup.When = "success"
	}
	return p, nil
}

// LoadJob reads a workflow and the YAML files its first and last steps point to.
// If jobName is empty and there is exactly one job, that job is used.
func LoadJob(workflowPath, jobName string) (Loaded, error) {
	var out Loaded
	abs, err := filepath.Abs(workflowPath)
	if err != nil {
		return out, err
	}
	wf, err := LoadWorkflow(abs)
	if err != nil {
		return out, err
	}
	out.Workflow = wf
	out.Path = abs
	out.Dir = filepath.Dir(abs)
	name, job, err := pickJob(wf, jobName)
	if err != nil {
		return out, err
	}
	out.JobName, out.Job = name, job
	if err := ValidateJob(name, job); err != nil {
		return out, err
	}
	out.PrepareStep = job.Steps[0]
	out.PackageStep = job.Steps[len(job.Steps)-1]
	out.RunSteps = job.Steps[1 : len(job.Steps)-1]

	return finishLoad(out, resolve(out.Dir, out.PrepareStep.WithFile()), resolve(out.Dir, out.PackageStep.WithFile()))
}

// LoadJobFiles is LoadJob with the Prepare and Package result files given
// explicitly instead of read from the steps' with.file (used for dispatched
// bundles, which store them under fixed names).
func LoadJobFiles(workflowPath, preparePath, packagePath, jobName string) (Loaded, error) {
	var out Loaded
	abs, err := filepath.Abs(workflowPath)
	if err != nil {
		return out, err
	}
	wf, err := LoadWorkflow(abs)
	if err != nil {
		return out, err
	}
	out.Workflow, out.Path, out.Dir = wf, abs, filepath.Dir(abs)
	name, job, err := pickJob(wf, jobName)
	if err != nil {
		return out, err
	}
	out.JobName, out.Job = name, job
	if err := ValidateJob(name, job); err != nil {
		return out, err
	}
	out.PrepareStep = job.Steps[0]
	out.PackageStep = job.Steps[len(job.Steps)-1]
	out.RunSteps = job.Steps[1 : len(job.Steps)-1]
	return finishLoad(out, preparePath, packagePath)
}

func finishLoad(out Loaded, prepPath, pkgPath string) (Loaded, error) {
	prep, err := LoadPrepare(prepPath)
	if err != nil {
		return out, err
	}
	pkg, err := LoadPackageResult(pkgPath)
	if err != nil {
		return out, err
	}
	out.Prepare, out.PreparePath = prep, prepPath
	out.Package, out.PackagePath = pkg, pkgPath
	if err := ValidatePrepare(prep); err != nil {
		return out, err
	}
	if err := ValidatePackage(pkg, prep); err != nil {
		return out, err
	}
	return out, nil
}

func pickJob(wf Workflow, jobName string) (string, Job, error) {
	if jobName != "" {
		job, ok := wf.Jobs[jobName]
		if !ok {
			return "", Job{}, fmt.Errorf("job %q not found", jobName)
		}
		return jobName, job, nil
	}
	if len(wf.Jobs) != 1 {
		names := make([]string, 0, len(wf.Jobs))
		for n := range wf.Jobs {
			names = append(names, n)
		}
		return "", Job{}, fmt.Errorf("workflow has %d jobs %v; pass --job", len(wf.Jobs), names)
	}
	for n, j := range wf.Jobs {
		return n, j, nil
	}
	return "", Job{}, fmt.Errorf("workflow has no jobs")
}

func applyPrepareDefaults(p *Prepare) {
	if p.Workspace.WorkingDirectory == "" {
		p.Workspace.WorkingDirectory = "."
	}
	// Leave Workspace.Shell empty so the runner can pick bash on Unix
	// and PowerShell on Windows.
	if len(p.Layout.Directories) == 0 {
		f, t := false, true
		p.Layout.Directories = []Directory{
			{Path: "in", Class: "permanent", Writable: &f},
			{Path: "bin", Class: "temporary", Writable: &f},
			{Path: "out", Class: "temporary", Writable: &t},
			{Path: "out/results", Class: "temporary", Writable: &t},
			{Path: "out/logs", Class: "temporary", Writable: &t},
			{Path: "tmp", Class: "temporary", Writable: &t},
		}
	}
}

func resolve(dir, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

func readYAML(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(b, out); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func usesAction(uses, want string) bool {
	uses = strings.TrimSpace(uses)
	if uses == want {
		return true
	}
	action, _, ok := strings.Cut(uses, "@")
	return ok && strings.TrimSpace(action) == want
}

func isPrepareStep(s Step) bool {
	if !usesAction(s.Uses, "ptc/prepare") {
		return false
	}
	if s.WithFile() == "" {
		return false
	}
	name := strings.TrimSpace(s.Name)
	id := strings.TrimSpace(s.ID)
	return strings.EqualFold(name, "Prepare") || strings.EqualFold(id, "prepare")
}

func isPackageStep(s Step) bool {
	if !usesAction(s.Uses, "ptc/package-result") {
		return false
	}
	if s.WithFile() == "" {
		return false
	}
	name := strings.TrimSpace(s.Name)
	id := strings.TrimSpace(s.ID)
	return strings.EqualFold(name, "Package result") || strings.EqualFold(id, "package-result")
}
