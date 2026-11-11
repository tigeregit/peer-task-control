package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/tigeregit/peer-task-control/internal/cas"
	"github.com/tigeregit/peer-task-control/internal/catalog"
	"github.com/tigeregit/peer-task-control/internal/config"
	"github.com/tigeregit/peer-task-control/internal/dispatch"
	"github.com/tigeregit/peer-task-control/internal/resource"
	"github.com/tigeregit/peer-task-control/internal/run"
	"github.com/tigeregit/peer-task-control/internal/workflow"
)

func submitCmd() *cobra.Command {
	var jobName, target, fetchDir string
	var dryRun, force, keep, detach, jsonOut bool
	var inlineMax int64
	cmd := &cobra.Command{
		Use:   "submit --workflow <file>",
		Short: "Run or dispatch a workflow, or retain resources with a seed-only action",
		Long: `Parses an Actions-style workflow whose first step is Prepare and last step is Package result.
Alternatively, a single ptc/seed@v0 step with with.file pointing at a kind: Seed
YAML retains and advertises resources without creating a workspace, running
commands, or packaging results. Remote seed-only tasks require files, not exec.

Without --target the job runs on this machine. With --target <node|peer-id> the
local daemon sends the job over /ptc/exec/1.0.0 to that node. --target auto
picks an eligible exec node (NVIDIA + exec on, for CUDA jobs). Never target a
gateway or any node without a GPU for require_nvidia jobs.

Content-addressed seeds already on the mesh are not shipped in the bundle; the
target pulls them from the closest holder and verifies the digest. Small local
sources are inlined; larger ones are published on this node first.

Follow is the default: status and logs stream until the job finishes. Pass
--detach to return after the job is queued. --fetch-result <dir> downloads the
result package when follow succeeds.

The WebUI stays read-only; only this CLI submits, cancels, or cleans.`,
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
				if jsonOut {
					return printJSON(map[string]any{"ok": true, "job": loaded.JobName, "prepare": loaded.PreparePath, "package": loaded.PackagePath, "run_steps": len(loaded.RunSteps), "target": target, "seed_only": loaded.SeedOnly})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "ok: job %q\n  prepare: %s\n  package: %s\n  run steps: %d\n  seed only: %v\n", loaded.JobName, loaded.PreparePath, loaded.PackagePath, len(loaded.RunSteps), loaded.SeedOnly)
				if target != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  target: %s (not sent; --dry-run)\n", target)
				}
				return nil
			}
			cfg := loadPaths()
			if target != "" {
				return submitRemote(cmd, loaded, cfg, target, detach, jsonOut, inlineMax, fetchDir)
			}
			if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
				return err
			}
			logs := cmd.OutOrStdout()
			if jsonOut {
				logs = cmd.ErrOrStderr()
			}
			res, err := run.Submit(loaded, run.Options{
				DataDir:      cfg.DataDir,
				SeedsDir:     cfg.SeedsDir(),
				WorktreesDir: cfg.WorktreesDir(),
				RunsDir:      cfg.RunsDir(),
				Force:        force,
				KeepWorktree: keep,
				Stdout:       logs,
				Context:      cmd.Context(),
				Fetch: func(ctx context.Context, digest string) (resource.Blob, error) {
					cli, err := controlClient()
					if err != nil {
						return resource.Blob{}, err
					}
					if err := ctx.Err(); err != nil {
						return resource.Blob{}, err
					}
					return cli.ResourceContent(digest)
				},
			})
			if jsonOut {
				_ = printJSON(map[string]any{"run_id": res.ID, "status": res.Status, "digest": res.Digest, "task": res.TaskPath, "worktree": res.Worktree})
				return err
			}
			if res.TaskPath != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "run recorded at %s\n", res.TaskPath)
				fmt.Fprintf(cmd.OutOrStdout(), "view with: ptc webui --runs %s\n", cfg.RunsDir())
			}
			if res.Digest != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "result seed %s\n", res.Digest)
			}
			return err
		},
	}
	cmd.Flags().String("workflow", "", "path to the workflow YAML")
	cmd.Flags().StringVar(&jobName, "job", "", "job name (required if the workflow has more than one job)")
	cmd.Flags().StringVar(&target, "target", "", "node name, peer id, or auto (exec for normal jobs, files for seed-only; omit for local)")
	cmd.Flags().BoolVar(&detach, "detach", false, "return after the job is queued (default is to follow until done)")
	cmd.Flags().StringVar(&fetchDir, "fetch-result", "", "after a successful follow, download the result package into this directory")
	cmd.Flags().Int64Var(&inlineMax, "inline-max", dispatch.DefaultInlineMax, "largest local seed (bytes) to ship in the bundle; larger seeds are published and pulled by digest; -1 means never inline")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "parse and validate only, do not execute or dispatch")
	cmd.Flags().BoolVar(&force, "force", false, "skip the local NVIDIA/exec check (local submit only)")
	cmd.Flags().BoolVar(&keep, "keep-worktree", false, "do not delete the worktree after Package result (local submit only)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON (agents should prefer this)")
	_ = cmd.MarkFlagRequired("workflow")
	return cmd
}

func loadPaths() *config.Config {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		cfg = config.Defaults()
		cfg.TrustedCIDRs = []string{"127.0.0.0/8"}
		_ = cfg.Normalize()
	}
	applyPathFlags(cfg)
	return cfg
}

func applyPathFlags(cfg *config.Config) {
	if dataDir != "" {
		cfg.DataDir = dataDir
	}
	if seedsDir != "" {
		cfg.Storage.SeedsDir = seedsDir
	}
	if worktreesDir != "" {
		cfg.Storage.WorktreesDir = worktreesDir
	}
	if runsDir != "" {
		cfg.Storage.RunsDir = runsDir
	}
}

func submitRemote(cmd *cobra.Command, loaded workflow.Loaded, cfg *config.Config, target string, detach, jsonOut bool, inlineMax int64, fetchDir string) error {
	cli, err := controlClient()
	if err != nil {
		return fmt.Errorf("mesh dispatch needs a running ptc daemon: %w", err)
	}
	st, err := cli.Status()
	if err != nil {
		return fmt.Errorf("talking to daemon: %w", err)
	}
	e, err := resolveSubmitTarget(target, loaded, st.Peers)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "building bundle for %s (%s)\n", e.Name, shortID(e.PeerID))
	bundle, err := dispatch.Build(loaded, dispatch.BuildOptions{
		InlineMax: inlineMax,
		Publish: func(name, path string) (resource.Seed, error) {
			return resource.PublishTreeTo(cfg.PackagesDir(), name, path, time.Now())
		},
		Log: cmd.ErrOrStderr(),
	})
	if err != nil {
		return err
	}
	follow := !detach
	ctx := context.Background()
	var last digestEvent
	ev, err := cli.Exec(ctx, e.Name, bundle.Data, follow, func(ev dispatch.Event) {
		last.Event = ev
		if jsonOut {
			return
		}
		switch ev.Type {
		case "accepted":
			fmt.Fprintf(cmd.OutOrStdout(), "queued as %s (%s) on %s\n", ev.RunID, ev.JobID, e.Name)
		case "status":
			fmt.Fprintf(cmd.OutOrStdout(), "status %s %s\n", ev.Status, ev.Message)
		case "log":
			fmt.Fprint(cmd.OutOrStdout(), ev.Message)
		case "done":
			fmt.Fprintf(cmd.OutOrStdout(), "done %s", ev.Status)
			if ev.Digest != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "  result seed %s", ev.Digest)
			}
			fmt.Fprintln(cmd.OutOrStdout())
			if ev.Digest != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "fetch with: ptc resources get %s -o result.tar\n", ev.Digest)
			}
		case "error":
			fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", ev.Error)
		}
	})
	if ev.RunID != "" {
		last.Event = ev
	}
	if jsonOut {
		_ = printJSON(map[string]any{"target": e.Name, "peer_id": e.PeerID, "event": ev, "error": errString(err)})
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if follow && fetchDir != "" && ev.Digest != "" {
		if err := fetchResult(cli, ev.Digest, fetchDir, cmd.OutOrStdout()); err != nil {
			return err
		}
	}
	return nil
}

type digestEvent struct{ Event dispatch.Event }

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func resolveSubmitTarget(target string, loaded workflow.Loaded, peers []catalog.Entry) (catalog.Entry, error) {
	t := loaded.Prepare.Target
	if loaded.SeedOnly && target == "auto" {
		var eligible []catalog.Entry
		for _, e := range peers {
			if dispatch.CheckJobPlacement(loaded, e) == nil {
				eligible = append(eligible, e)
			}
		}
		if len(eligible) == 0 {
			return catalog.Entry{}, fmt.Errorf("no eligible files node for seed-only task")
		}
		best := eligible[0]
		for _, e := range eligible[1:] {
			if e.Name < best.Name {
				best = e
			}
		}
		return best, nil
	}
	if target == "auto" {
		e, err := dispatch.PickAuto(t, peers)
		if err != nil {
			return e, err
		}
		return e, nil
	}
	e, err := dispatch.ResolveTarget(target, peers)
	if err != nil {
		return e, err
	}
	if err := dispatch.CheckJobPlacement(loaded, e); err != nil {
		return e, err
	}
	return e, nil
}

func fetchResult(cli interface {
	ResourceContent(string) (resource.Blob, error)
}, digest, dir string, w io.Writer) error {
	blob, err := cli.ResourceContent(digest)
	if err != nil {
		return fmt.Errorf("download result %s: %w", digest, err)
	}
	defer blob.Body.Close()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	files, n, err := cas.ExtractTar(blob.Body, dir, 0)
	if err != nil {
		return fmt.Errorf("unpack result: %w", err)
	}
	fmt.Fprintf(w, "fetched %s into %s (%d files, %d bytes)\n", digest, dir, files, n)
	return nil
}
