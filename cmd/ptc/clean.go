package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/tigeregit/peer-task-control/internal/clean"
)

func cleanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Free disk on this node: result packages, run records, seeds, or worktrees",
		Long: `Operates on the local node only (the daemon's storage paths). Remote --node
deletion is not implemented; enable it later only if the target sets policy.remote_clean.

Worktrees are independent of seeds: cleaning a worktree never deletes CAS or packages.
Pinned seeds/packages are kept unless --force. Seeds still referenced by a queued or
running job are refused. --dry-run lists paths and bytes; --yes is required to delete
(so non-interactive agents can pass it).`,
	}
	cmd.AddCommand(cleanSub("outputs", "delete task result packages", clean.Outputs))
	cmd.AddCommand(cleanSub("runs", "delete execution records (finished only)", clean.Runs))
	cmd.AddCommand(cleanSub("resource", "delete a seed or result by digest", clean.Resources))
	cmd.AddCommand(cleanSub("worktrees", "delete leftover worktrees (never touches seeds)", clean.Worktrees))
	return cmd
}

func cleanSub(use, short string, kind clean.Kind) *cobra.Command {
	var match string
	var dry, yes, force, jsonOut bool
	cmd := &cobra.Command{
		Use:   use + " [ID...]",
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !dry && !yes {
				return fmt.Errorf("refusing to delete without --yes (pass --dry-run to list); agents should pass --yes")
			}
			cfg := loadPaths()
			opt := clean.Options{IDs: args, Match: match, Force: force}
			active := clean.ActiveDigests(cfg, nil)
			var plans []clean.Plan
			var err error
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
			if jsonOut {
				out := map[string]any{"dry_run": dry, "plans": plans, "bytes": clean.Total(plans, true)}
				if !dry {
					freed, err := clean.Apply(plans)
					out["freed"] = freed
					if err != nil {
						out["error"] = err.Error()
						_ = printJSON(out)
						return err
					}
				}
				return printJSON(out)
			}
			if len(plans) == 0 {
				fmt.Println("(nothing matched)")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ALLOW\tBYTES\tID\tPATH\tNOTE")
			for _, p := range plans {
				allow := "yes"
				if !p.Allowed {
					allow = "no"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", allow, humanSize(p.Bytes), p.ID, p.Path, p.Reason)
			}
			_ = w.Flush()
			fmt.Printf("would free %s (%s skipped)\n", humanSize(clean.Total(plans, true)), humanSize(clean.Total(plans, false)-clean.Total(plans, true)))
			if dry {
				return nil
			}
			freed, err := clean.Apply(plans)
			if err != nil {
				return err
			}
			fmt.Printf("freed %s\n", humanSize(freed))
			return nil
		},
	}
	cmd.Flags().StringVar(&match, "match", "", "regular expression over id, name, and job")
	cmd.Flags().BoolVar(&dry, "dry-run", false, "list what would be deleted and how many bytes, do not delete")
	cmd.Flags().BoolVar(&yes, "yes", false, "required to actually delete")
	cmd.Flags().BoolVar(&force, "force", false, "allow deleting pinned items")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	if kind == clean.Resources {
		cmd.Args = cobra.MinimumNArgs(0)
	}
	return cmd
}
