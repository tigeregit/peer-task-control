package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func scanCmd() *cobra.Command {
	var target string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Request one local scan, or a scan on a named relay",
		Long: "Queue one discovery scan. Without --node, scan on the local daemon; " +
			"with --node, request a known relay by name or peer ID. " +
			"All manual requests share the target's global cooldown (default 60s). " +
			"Busy or rate-limited requests are rejected, not queued for later. " +
			"Acceptance does not mean discovery has finished.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := controlClient()
			if err != nil {
				return err
			}
			reply, scanErr := cli.Scan(cmd.Context(), target)
			if jsonOut && reply.Status != "" {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				if err := encoder.Encode(reply); err != nil {
					return err
				}
			}
			if scanErr != nil {
				return scanErr
			}
			if !jsonOut {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "Scan queued on %s (%s)\n", reply.Node, reply.PeerID)
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "node", "", "relay name or peer ID (default: local daemon)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON, including structured rejections")
	return cmd
}
