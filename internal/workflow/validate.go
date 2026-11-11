package workflow

import (
	"fmt"
	"path"
	"strings"

	"github.com/tigeregit/peer-task-control/internal/resource"
)

func ValidateJob(jobName string, job Job) error {
	for _, s := range job.Steps {
		if usesAction(s.Uses, "ptc/seed") {
			if len(job.Steps) != 1 || !isSeedStep(s) || s.Run != "" || s.If != "" {
				return fmt.Errorf("job %q: ptc/seed@v0 must be the only step, with.file and no run or if", jobName)
			}
			return nil
		}
	}
	if len(job.Steps) < 2 {
		return fmt.Errorf("job %q must start with Prepare and end with Package result", jobName)
	}
	if !isPrepareStep(job.Steps[0]) {
		return fmt.Errorf("job %q: first step must be name: Prepare, uses: ptc/prepare@v0, with.file pointing at a Prepare YAML", jobName)
	}
	last := job.Steps[len(job.Steps)-1]
	if !isPackageStep(last) {
		return fmt.Errorf("job %q: last step must be name: Package result, uses: ptc/package-result@v0, with.file pointing at a Package result YAML", jobName)
	}
	for i, s := range job.Steps[1 : len(job.Steps)-1] {
		if isPrepareStep(s) || isPackageStep(s) {
			return fmt.Errorf("job %q: Prepare/Package result may only appear as the first and last steps (found at step %d)", jobName, i+2)
		}
		if strings.TrimSpace(s.Run) == "" && strings.TrimSpace(s.Uses) == "" {
			return fmt.Errorf("job %q: step %d has neither run nor uses", jobName, i+2)
		}
		if strings.TrimSpace(s.Uses) != "" && !strings.HasPrefix(strings.TrimSpace(s.Uses), "ptc/") {
			return fmt.Errorf("job %q: step %d uses %q is not supported (marketplace actions are not run)", jobName, i+2, s.Uses)
		}
	}
	return nil
}

func ValidateSeeds(seeds []Seed) error {
	if len(seeds) == 0 {
		return fmt.Errorf("seed: seeds is required")
	}
	seen := map[string]bool{}
	for _, s := range seeds {
		if strings.TrimSpace(s.Name) == "" || seen[s.Name] {
			return fmt.Errorf("seed: names must be nonempty and unique: %q", s.Name)
		}
		seen[s.Name] = true
		if s.Dest != "" || s.Entrypoint {
			return fmt.Errorf("seed %s: dest and entrypoint are not supported by seed-only actions", s.Name)
		}
		if s.Digest != "" && resource.Normalize(s.Digest) == "" {
			return fmt.Errorf("seed %s: invalid sha256 digest", s.Name)
		}
		if s.CID != "" || s.Source != nil {
			return fmt.Errorf("seed %s: use local or sha256 digest; CID/dataset resolution is not supported", s.Name)
		}
		if (s.Local == nil || s.Local.Src == "") && s.Digest == "" {
			return fmt.Errorf("seed %s: a local source or sha256 digest is required", s.Name)
		}
	}
	return nil
}

func ValidatePrepare(p Prepare) error {
	if p.Target.RequiresNVIDIA() && p.Target.DeniesNonExec() {
		// ok, defaults
	}
	if len(p.Seeds) == 0 {
		return fmt.Errorf("prepare: seeds is required")
	}
	hasEntry := strings.TrimSpace(p.Workspace.Entrypoint) != ""
	dirs := map[string]Directory{}
	for _, d := range p.Layout.Directories {
		d.Path = path.Clean(filepathToSlash(d.Path))
		dirs[d.Path] = d
	}
	for i, s := range p.Seeds {
		if strings.TrimSpace(s.Name) == "" {
			return fmt.Errorf("prepare: seeds[%d] missing name", i)
		}
		if strings.TrimSpace(s.Dest) == "" {
			return fmt.Errorf("prepare: seed %s missing dest", s.Name)
		}
		if s.Local == nil && s.Digest == "" && s.CID == "" && (s.Source == nil || s.Source.Dataset == "") {
			return fmt.Errorf("prepare: seed %s has no local, digest, cid, or source", s.Name)
		}
		dest := path.Clean(filepathToSlash(s.Dest))
		if err := destInLayout(dest, dirs); err != nil {
			return fmt.Errorf("prepare: seed %s: %w", s.Name, err)
		}
		if s.Class == "permanent" && writableDest(dest, dirs) {
			return fmt.Errorf("prepare: permanent seed %s must not land in a writable out/ or tmp/ path", s.Name)
		}
		if s.Entrypoint {
			hasEntry = true
		}
	}
	if !hasEntry {
		return fmt.Errorf("prepare: set workspace.entrypoint or mark one seed as entrypoint: true")
	}
	return nil
}

func ValidatePackage(pkg PackageResult, prep Prepare) error {
	items := CollectItems(pkg, prep)
	if len(items) == 0 {
		return fmt.Errorf("package result: collect.items is empty")
	}
	dirs := map[string]Directory{}
	for _, d := range prep.Layout.Directories {
		d.Path = path.Clean(filepathToSlash(d.Path))
		dirs[d.Path] = d
	}
	for _, it := range items {
		if strings.TrimSpace(it.Path) == "" {
			return fmt.Errorf("package result: collect item %q missing path", it.Name)
		}
		p := path.Clean(filepathToSlash(it.Path))
		if err := destInLayout(p, dirs); err != nil {
			return fmt.Errorf("package result: %s: %w", it.Name, err)
		}
	}
	switch pkg.Package.Format {
	case "", "unixfs", "tar", "zip":
	default:
		return fmt.Errorf("package result: unsupported package.format %q", pkg.Package.Format)
	}
	return nil
}

// CollectItems merges explicit items with prepare.outputs when requested.
func CollectItems(pkg PackageResult, prep Prepare) []CollectItem {
	seen := map[string]int{}
	var items []CollectItem
	add := func(it CollectItem) {
		key := path.Clean(filepathToSlash(it.Path))
		if i, ok := seen[key]; ok {
			items[i] = it
			return
		}
		seen[key] = len(items)
		items = append(items, it)
	}
	if pkg.Collect.FromPrepareOutputs {
		for _, o := range prep.Outputs {
			add(CollectItem{Name: o.Name, Path: o.Path, Summary: o.Summary})
		}
	}
	for _, it := range pkg.Collect.Items {
		add(it)
	}
	return items
}

func destInLayout(dest string, dirs map[string]Directory) error {
	dest = path.Clean(filepathToSlash(dest))
	if dest == "." || dest == ".." || strings.HasPrefix(dest, "../") || path.IsAbs(dest) {
		return fmt.Errorf("path %q is outside the worktree", dest)
	}
	if _, ok := dirs[dest]; ok {
		return nil
	}
	dir := path.Dir(dest)
	for dir != "." && dir != "/" {
		if _, ok := dirs[dir]; ok {
			return nil
		}
		dir = path.Dir(dir)
	}
	return fmt.Errorf("path %q is not inside layout.directories", dest)
}

func writableDest(dest string, dirs map[string]Directory) bool {
	dest = path.Clean(filepathToSlash(dest))
	if d, ok := dirs[dest]; ok {
		return d.IsWritable()
	}
	dir := path.Dir(dest)
	for dir != "." && dir != "/" {
		if d, ok := dirs[dir]; ok {
			return d.IsWritable()
		}
		dir = path.Dir(dir)
	}
	return false
}

func filepathToSlash(p string) string {
	return strings.ReplaceAll(p, `\`, "/")
}
