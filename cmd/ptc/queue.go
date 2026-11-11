package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/tigeregit/peer-task-control/internal/dispatch"
	"github.com/tigeregit/peer-task-control/internal/query"
)

func queueCmd() *cobra.Command {
	var jsonOut bool
	var node string
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "List queued and running jobs on a node or across the mesh",
		Long:  "Shows queued, running, and cancelling jobs. A cancelling job keeps its execution slot until its processes stop. Finished jobs are in ptc tasks and the WebUI board. Use ptc cancel <run-id> to cancel a queued or running job (CLI only).",
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := controlClient()
			if err != nil {
				return err
			}
			items, err := cli.Queue()
			if err != nil {
				return fmt.Errorf("talking to daemon: %w", err)
			}
			if node != "" {
				items = filterQueueForNode(items, node)
			}
			if jsonOut {
				return printJSON(map[string]any{"jobs": items})
			}
			if len(items) == 0 {
				fmt.Println("(no queued or running jobs)")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "RUN ID\tJOB\tSTATUS\tSUBMITTER\tQUEUED")
			for _, it := range items {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", it.RunID, it.JobName, it.Status, it.Submitter.Node, fmtTime(it.QueuedAt))
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	cmd.Flags().StringVar(&node, "node", "", "only jobs on this node name or peer id")
	cmd.AddCommand(cancelCmd())
	return cmd
}

func filterQueueForNode(items []dispatch.QueueItem, node string) []dispatch.QueueItem {
	var out []dispatch.QueueItem
	for _, it := range items {
		if query.MatchNode(it.Node, it.PeerID, node) {
			out = append(out, it)
		}
	}
	return out
}

func cancelCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "cancel RUN-ID",
		Short: "Cancel a queued or running job (CLI only; WebUI cannot cancel)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := controlClient()
			if err != nil {
				return err
			}
			status, err := cli.Cancel(args[0])
			if err != nil {
				return err
			}
			if jsonOut {
				return printJSON(map[string]string{"status": status, "id": args[0]})
			}
			fmt.Printf("%s %s\n", status, args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	return cmd
}

func fmtTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Local().Format("15:04:05")
}
