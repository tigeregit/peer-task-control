// Package disk reports real volume capacity and how much this node stores.
package disk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/peer-task-control/ptc/internal/config"
)

// Volume is one filesystem that holds a ptc data root.
type Volume struct {
	Path  string `json:"path"`
	Role  string `json:"role"` // seeds, worktrees, runs (comma-joined if shared)
	Total int64  `json:"total"`
	Free  int64  `json:"free"`
	Used  int64  `json:"used"`
}

// Usage is advertised in the catalog and shown in peers show / the WebUI.
type Usage struct {
	SeedsBytes     int64    `json:"seeds_bytes"`
	WorktreesBytes int64    `json:"worktrees_bytes"`
	OutputsBytes   int64    `json:"outputs_bytes"`
	RecordsBytes   int64    `json:"records_bytes"`
	Volumes        []Volume `json:"volumes,omitempty"`
}

// Measure walks this node's storage roots and the volumes they sit on.
// Numbers are real; missing directories count as zero.
func Measure(cfg *config.Config) Usage {
	if cfg == nil {
		return Usage{}
	}
	u := Usage{}
	pkg := cfg.PackagesDir()
	cas := cfg.CASDir()
	u.SeedsBytes, u.OutputsBytes = splitPackages(pkg)
	u.SeedsBytes += dirSize(cas)
	u.WorktreesBytes = dirSize(cfg.WorktreesDir())
	u.RecordsBytes = dirSize(cfg.RunsDir())

	type root struct {
		path, role string
	}
	roots := []root{
		{cfg.SeedsDir(), "seeds"},
		{cfg.WorktreesDir(), "worktrees"},
		{cfg.RunsDir(), "runs"},
	}
	byDev := map[string]*Volume{}
	var order []string
	for _, r := range roots {
		total, free, key, ok := volumeOf(r.path)
		if !ok {
			continue
		}
		if v, seen := byDev[key]; seen {
			if !strings.Contains(v.Role, r.role) {
				v.Role = v.Role + "," + r.role
			}
			continue
		}
		used := int64(0)
		if total >= free {
			used = total - free
		}
		byDev[key] = &Volume{Path: displayPath(r.path), Role: r.role, Total: total, Free: free, Used: used}
		order = append(order, key)
	}
	for _, k := range order {
		u.Volumes = append(u.Volumes, *byDev[k])
	}
	return u
}

func displayPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// splitPackages counts prepare/published seeds versus result packages
// (manifests that record run_ok).
func splitPackages(pkgRoot string) (seeds, outputs int64) {
	ents, err := os.ReadDir(pkgRoot)
	if err != nil {
		return 0, 0
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(pkgRoot, e.Name())
		n := dirSize(dir)
		if isResultPackage(dir) {
			outputs += n
		} else {
			seeds += n
		}
	}
	return seeds, outputs
}

func isResultPackage(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return false
	}
	var man struct {
		RunOK *bool `json:"run_ok"`
	}
	if json.Unmarshal(b, &man) != nil {
		return false
	}
	return man.RunOK != nil
}

func dirSize(root string) int64 {
	var n int64
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		n += info.Size()
		return nil
	})
	return n
}
