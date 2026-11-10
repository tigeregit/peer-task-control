//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/peer-task-control/ptc/internal/resource"
	"github.com/peer-task-control/ptc/internal/taskboard"
)

func main() {
	root := os.Getenv("PTC_REPO")
	if root == "" {
		fmt.Fprintln(os.Stderr, "PTC_REPO is required")
		os.Exit(1)
	}
	srcRoot := filepath.Join(root, "testdata", "cuda-e2e")
	pkgDir := filepath.Join(root, "docker", "testdata", "node-packages", "worker-a")
	created := time.Date(2026, 11, 10, 15, 53, 26, 0, time.UTC)
	type item struct{ Name, Src string }
	items := []item{
		{"third_party_sdk", filepath.Join(srcRoot, "sdk")},
		{"scene-a", filepath.Join(srcRoot, "scene")},
		{"app", filepath.Join(srcRoot, "bin")},
	}
	seeds := make([]taskboard.WorktreeSeed, 0, len(items))
	for _, it := range items {
		fmt.Printf("publishing %s from %s ...\n", it.Name, it.Src)
		s, err := resource.PublishTreeTo(pkgDir, it.Name, it.Src, created)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", it.Name, err)
			os.Exit(1)
		}
		dest := map[string]string{"third_party_sdk": "in/sdk", "scene-a": "in/scene", "app": "bin"}[it.Name]
		class := "permanent"
		if it.Name == "app" {
			class = "temporary"
		}
		seeds = append(seeds, taskboard.WorktreeSeed{
			Name: it.Name, Dest: dest, Class: class,
			Digest: s.Digest, Size: s.Size, Files: len(s.Files),
		})
		fmt.Printf("  %s  %s  %d files  %d bytes\n", it.Name, s.Digest, len(s.Files), s.Size)
	}

	wt := &taskboard.Worktree{
		Root:             `C:\Users\iamsbb2\AppData\Local\Temp\ptc-cuda-e2e\worktrees\20261110T155326Z-test`,
		WorkingDirectory: ".",
		Entrypoint:       "bin/fake_cuda.ps1",
		Seeds:            seeds,
		Directories: []taskboard.WorktreeDir{
			{Path: "in", Class: "permanent", Writable: false},
			{Path: "in/sdk", Class: "permanent", Writable: false},
			{Path: "in/scene", Class: "permanent", Writable: false},
			{Path: "bin", Class: "temporary", Writable: false},
			{Path: "out", Class: "temporary", Writable: true},
			{Path: "out/results", Class: "temporary", Writable: true},
			{Path: "out/logs", Class: "temporary", Writable: true},
			{Path: "tmp", Class: "temporary", Writable: true},
		},
		Outputs: []taskboard.WorktreeOutput{
			{Name: "results", Path: "out/results", Class: "temporary", Summary: true},
			{Name: "logs", Path: "out/logs", Class: "temporary"},
		},
	}
	runPaths := []string{
		filepath.Join(root, "docker", "testdata", "node-runs", "worker-a", "run-20261110T155326Z-test.json"),
		filepath.Join(root, "docker", "testdata", "runs", "run-20261110T155326Z-test.json"),
		filepath.Join(os.TempDir(), "ptc-cuda-e2e", "runs", "run-20261110T155326Z-test.json"),
	}
	for _, path := range runPaths {
		if err := patchRun(path, wt); err != nil {
			fmt.Fprintf(os.Stderr, "patch %s: %v\n", path, err)
			continue
		}
		fmt.Printf("patched %s\n", path)
	}
}

func patchRun(path string, wt *taskboard.Worktree) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var t taskboard.Task
	if err := json.Unmarshal(b, &t); err != nil {
		return err
	}
	t.Worktree = wt
	out, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}
