package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/peer-task-control/ptc/internal/config"
	"github.com/peer-task-control/ptc/internal/run"
	"github.com/peer-task-control/ptc/internal/workflow"
)

func submitCmd() *cobra.Command {
	var jobName string
	var dryRun, force, keep bool
	cmd := &cobra.Command{
		Use:   "submit --workflow <file>",
		Short: "Run a workflow locally: Prepare, run steps, then Package result",
		Long:  "Parses an Actions-style workflow whose first step is Prepare and last step is Package result. This release executes the job on this machine only; mesh dispatch and remote seed fetch are not implemented yet. Remote seeds need seeds.local or a blob already in the local CAS.",
		RunE: func(cmd *cobra.Command, args []string) error {
			wf, err := cmd.Flags().GetString("workflow")
			if err != nil {
				return err
			}
			if wf == "" {
				return fmt.Errorf("pass --workflow <file>")
			}
			loaded, err := workflow.LoadJob(wf, jobName)
			if err != nil {
				return err
			}
			if dryRun {
				fmt.Fprintf(cmd.OutOrStdout(), "ok: job %q\n  prepare: %s\n  package: %s\n  run steps: %d\n", loaded.JobName, loaded.PreparePath, loaded.PackagePath, len(loaded.RunSteps))
				return nil
			}
			dir := dataDir
			if dir == "" {
				if cfg, err := config.Load(cfgPath); err == nil && cfg.DataDir != "" {
					dir = cfg.DataDir
				} else {
					dir = config.DefaultDataDir()
				}
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			res, err := run.Submit(loaded, run.Options{
				DataDir:      dir,
				Force:        force,
				KeepWorktree: keep,
				Stdout:       cmd.OutOrStdout(),
			})
			if res.TaskPath != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "run recorded at %s\n", res.TaskPath)
				fmt.Fprintf(cmd.OutOrStdout(), "view with: ptc webui --runs %s\n", filepath.Join(dir, "runs"))
			}
			if res.Digest != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "result seed %s\n", res.Digest)
			}
			return err
		},
	}
	cmd.Flags().String("workflow", "", "path to the workflow YAML")
	cmd.Flags().StringVar(&jobName, "job", "", "job name (required if the workflow has more than one job)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "parse and validate only, do not execute")
	cmd.Flags().BoolVar(&force, "force", false, "skip the local NVIDIA/exec check (for non-CUDA checks)")
	cmd.Flags().BoolVar(&keep, "keep-worktree", false, "do not delete the worktree after Package result")
	_ = cmd.MarkFlagRequired("workflow")
	return cmd
}
