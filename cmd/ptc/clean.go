package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/tigeregit/peer-task-control/internal/clean"
	"github.com/tigeregit/peer-task-control/internal/control"
)

func cleanCmd() *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Free disk: result packages, run records, seeds, or worktrees",
		Long: `Without --node, operates on this machine's storage paths. With --node,
the local daemon asks that peer (by name or peer ID) to run the same class of
cleanup. The target must enable policy.remote_clean; older binaries that lack
the clean protocol fail clearly.

Worktrees are independent of seeds: cleaning a worktree never deletes CAS or packages.
Active worktrees are protected by process locks, including when a job starts after
the plan is made. Drain old-version tasks before upgrading and cleaning worktrees.
Pinned seeds/packages and resources that are unique on the network (this node holds
the only known copy) are kept unless --force. If replica counts cannot be confirmed,
the resource is treated as unique. Seeds still referenced by a queued or running job
are refused. --dry-run lists paths and bytes; --yes is required to delete
(so non-interactive agents can pass it).`,
	}
	cmd.PersistentFlags().StringVar(&target, "node", "", "node name or peer ID (default: local storage paths)")
	cmd.AddCommand(cleanSub("outputs", "delete task result packages", clean.Outputs, &target))
	cmd.AddCommand(cleanSub("runs", "delete execution records (finished only)", clean.Runs, &target))
	cmd.AddCommand(cleanSub("resource", "delete a seed or result by digest", clean.Resources, &target))
	cmd.AddCommand(cleanSub("worktrees", "delete leftover worktrees (never touches seeds)", clean.Worktrees, &target))
	return cmd
}

func cleanSub(use, short string, kind clean.Kind, target *string) *cobra.Command {
	var match string
	var dry, yes, force, jsonOut bool
	cmd := &cobra.Command{
		Use:   use + " [ID...]",
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !dry && !yes {
				return fmt.Errorf("refusing to delete without --yes (pass --dry-run to list); agents should pass --yes")
			}
			if target != nil && strings.TrimSpace(*target) != "" {
				return runRemoteClean(cmd, control.CleanRequest{
					Node:   strings.TrimSpace(*target),
					Kind:   string(kind),
					IDs:    args,
					Match:  match,
					DryRun: dry,
					Force:  force,
					Yes:    yes,
				}, jsonOut)
			}
			cfg, err := loadPaths()
			if err != nil {
				return err
			}
			opt := clean.Options{IDs: args, Match: match, Force: force}
			if kind == clean.Outputs || kind == clean.Resources {
				opt.Holders = localHolderCounts()
			}
			active := clean.ActiveDigests(cfg, nil)
			var plans []clean.Plan
			switch kind {
			case clean.Outputs:
				plans, err = clean.PlanOutputs(cfg, opt, active)
			case clean.Runs:
				plans, err = clean.PlanRuns(cfg, opt)
			case clean.Resources:
				plans, err = clean.PlanResources(cfg, opt, active)
			case clean.Worktrees:
				plans, err = clean.PlanWorktrees(cfg, opt)
			}
			if err != nil {
				return err
			}
			return printCleanResult(cmd, dry, jsonOut, plans, func() (int64, error) { return clean.Apply(plans) })
		},
	}
	cmd.Flags().StringVar(&match, "match", "", "regular expression over id, name, and job")
	cmd.Flags().BoolVar(&dry, "dry-run", false, "list what would be deleted and how many bytes, do not delete")
	cmd.Flags().BoolVar(&yes, "yes", false, "required to actually delete")
	cmd.Flags().BoolVar(&force, "force", false, "allow deleting pinned items or the only network copy of a resource")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	if kind == clean.Resources {
		cmd.Args = cobra.MinimumNArgs(0)
	}
	return cmd
}

func localHolderCounts() map[string]int {
	cli, err := controlClient()
	if err != nil {
		return nil
	}
	list, err := cli.Resources()
	if err != nil {
		return nil
	}
	return clean.HolderCounts(list.Seeds)
}

func runRemoteClean(cmd *cobra.Command, req control.CleanRequest, jsonOut bool) error {
	cli, err := controlClient()
	if err != nil {
		return err
	}
	reply, cleanErr := cli.Clean(cmd.Context(), req)
	if jsonOut && reply.Status != "" {
		if err := writeJSON(cmd, reply); err != nil {
			return err
		}
	}
	if cleanErr != nil {
		if reply.Status == "" && looksLikeUnsupportedClean(cleanErr) {
			return fmt.Errorf("local daemon does not support remote clean (upgrade ptc): %w", cleanErr)
		}
		return cleanErr
	}
	if jsonOut {
		return nil
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "clean on %s (%s)\n", reply.Node, reply.PeerID)
	return printCleanResult(cmd, req.DryRun, false, reply.Plans, func() (int64, error) { return reply.Freed, nil })
}

func looksLikeUnsupportedClean(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "404") || strings.Contains(msg, "Not Found") || strings.Contains(msg, "not implemented") || strings.Contains(msg, "does not support remote clean")
}

func printCleanResult(cmd *cobra.Command, dry, jsonOut bool, plans []clean.Plan, apply func() (int64, error)) error {
	out := cmd.OutOrStdout()
	if jsonOut {
		payload := map[string]any{"dry_run": dry, "plans": plans, "bytes": clean.Total(plans, true)}
		if !dry {
			freed, err := apply()
			payload["freed"] = freed
			if err != nil {
				payload["error"] = err.Error()
				_ = writeJSON(cmd, payload)
				return err
			}
		}
		return writeJSON(cmd, payload)
	}
	if len(plans) == 0 {
		fmt.Fprintln(out, "(nothing matched)")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ALLOW\tBYTES\tID\tPATH\tNOTE")
	for _, p := range plans {
		allow := "yes"
		if !p.Allowed {
			allow = "no"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", allow, humanSize(p.Bytes), p.ID, p.Path, p.Reason)
	}
	_ = w.Flush()
	fmt.Fprintf(out, "would free %s (%s skipped)\n", humanSize(clean.Total(plans, true)), humanSize(clean.Total(plans, false)-clean.Total(plans, true)))
	if dry {
		return nil
	}
	freed, err := apply()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "freed %s\n", humanSize(freed))
	return nil
}

func writeJSON(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
