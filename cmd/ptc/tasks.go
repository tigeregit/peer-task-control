package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/peer-task-control/ptc/internal/query"
	"github.com/peer-task-control/ptc/internal/taskboard"
)

func tasksCmd() *cobra.Command {
	var jsonOut bool
	var f query.TaskFilter
	cmd := &cobra.Command{
		Use:     "tasks",
		Aliases: []string{"task"},
		Short:   "List or show mesh-visible task runs",
		Long:    "Uses the same /api/v1/runs interface as the WebUI task board. List flags share the WebUI search, status, node, and sort semantics.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return tasksList(jsonOut, f)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	cmd.Flags().StringVar(&f.Q, "search", "", "substring over id, name, node, status, summary")
	cmd.Flags().StringVar(&f.Status, "status", "", "all, in_progress (includes queued), completed, failed, or an exact status")
	cmd.Flags().StringVar(&f.Node, "node", "", "filter by node name or peer id")
	cmd.Flags().StringVar(&f.Sort, "sort", "started", "started, duration, name, status, node, id")
	cmd.Flags().StringVar(&f.Order, "order", "", "asc or desc (default desc for started/duration)")
	cmd.AddCommand(tasksShowCmd())
	return cmd
}

func tasksShowCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show ID",
		Short: "Show one task run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := controlClient()
			if err != nil {
				return err
			}
			runs, err := cli.Runs()
			if err != nil {
				return fmt.Errorf("talking to daemon: %w", err)
			}
			for _, t := range runs {
				if t.ID == args[0] {
					if jsonOut {
						return printJSON(t)
					}
					fmt.Printf("id      %s\n", t.ID)
					fmt.Printf("name    %s / %s\n", t.Name.ZH, t.Name.EN)
					fmt.Printf("status  %s\n", t.Status)
					fmt.Printf("node    %s\n", t.Node)
					fmt.Printf("summary %s\n", t.Summary.ZH)
					if t.Summary.ZH == "" {
						fmt.Printf("summary %s\n", t.Summary.EN)
					}
					printWorktree(t.Worktree)
					return nil
				}
			}
			return fmt.Errorf("task %s not found", args[0])
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	return cmd
}

func tasksList(jsonOut bool, f query.TaskFilter) error {
	cli, err := controlClient()
	if err != nil {
		return err
	}
	runs, err := cli.Runs()
	if err != nil {
		return fmt.Errorf("talking to daemon: %w", err)
	}
	runs = query.FilterTasks(runs, f)
	if jsonOut {
		return printJSON(runs)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tNODE\tNAME")
	for _, t := range runs {
		name := t.Name.ZH
		if name == "" {
			name = t.Name.EN
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.ID, t.Status, t.Node, name)
	}
	return w.Flush()
}

func printWorktree(wt *taskboard.Worktree) {
	if wt == nil {
		return
	}
	fmt.Printf("worktree\n")
	if wt.Root != "" {
		fmt.Printf("  root        %s\n", wt.Root)
	}
	if wt.WorkingDirectory != "" {
		fmt.Printf("  cwd         %s\n", wt.WorkingDirectory)
	}
	if wt.Entrypoint != "" {
		fmt.Printf("  entrypoint  %s\n", wt.Entrypoint)
	}
	for _, d := range wt.Directories {
		w := "ro"
		if d.Writable {
			w = "rw"
		}
		fmt.Printf("  dir  %-16s  %s  %s\n", d.Path, d.Class, w)
	}
	for _, s := range wt.Seeds {
		fmt.Printf("  seed %-16s  dest=%s  %s  %s", s.Name, s.Dest, s.Class, s.Digest)
		if s.Size > 0 {
			fmt.Printf("  %d bytes", s.Size)
		}
		if s.Files > 0 {
			fmt.Printf("  %d files", s.Files)
		}
		fmt.Printf("\n")
	}
	for _, o := range wt.Outputs {
		fmt.Printf("  out  %-16s  %s  %s\n", o.Name, o.Path, o.Class)
	}
}
