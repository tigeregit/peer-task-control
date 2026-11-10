package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/peer-task-control/ptc/internal/control"
)

func policyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "Show or change this host's allow/deny lists and service switches",
		RunE: func(cmd *cobra.Command, args []string) error {
			return showPolicy()
		},
	}
	cmd.AddCommand(policyShowCmd(), policyAllowCmd(), policyDenyCmd(), policyServiceCmd())
	return cmd
}

func policyShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the local policy",
		RunE: func(cmd *cobra.Command, args []string) error {
			return showPolicy()
		},
	}
}

func policyAllowCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "allow", Short: "Edit the allow list"}
	cmd.AddCommand(policyListEdit("allow", "add"), policyListEdit("allow", "rm"))
	return cmd
}

func policyDenyCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "deny", Short: "Edit the deny list"}
	cmd.AddCommand(policyListEdit("deny", "add"), policyListEdit("deny", "rm"))
	return cmd
}

func policyListEdit(list, verb string) *cobra.Command {
	op := list + "_" + verb
	use := verb + " <IP|CIDR|name|peerID>"
	return &cobra.Command{
		Use:  use,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := controlClient()
			if err != nil {
				return err
			}
			p, err := cli.ApplyPolicy(control.PolicyChange{Op: op, Spec: args[0]})
			if err != nil {
				return err
			}
			return printPolicy(p)
		},
	}
}

func policyServiceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "service <exec|files|relay> <on|off>",
		Short: "Enable or disable a local service",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.ToLower(args[0])
			var on bool
			switch strings.ToLower(args[1]) {
			case "on", "true", "1", "enable", "enabled":
				on = true
			case "off", "false", "0", "disable", "disabled":
				on = false
			default:
				return fmt.Errorf("want on or off, got %q", args[1])
			}
			cli, err := controlClient()
			if err != nil {
				return err
			}
			p, err := cli.ApplyPolicy(control.PolicyChange{Op: "service", Service: name, On: &on})
			if err != nil {
				return err
			}
			return printPolicy(p)
		},
	}
}

func showPolicy() error {
	cli, err := controlClient()
	if err != nil {
		return err
	}
	p, err := cli.Policy()
	if err != nil {
		return fmt.Errorf("talking to daemon: %w", err)
	}
	return printPolicy(p)
}

func printPolicy(p control.PolicyView) error {
	fmt.Printf("allow: %s\n", emptyDash(p.Allow))
	fmt.Printf("deny:  %s\n", emptyDash(p.Deny))
	fmt.Printf("exec:  %v\nfiles: %v\nrelay: %v\n", p.Exec, p.Files, p.Relay)
	return nil
}

func emptyDash(list []string) string {
	if len(list) == 0 {
		return "(empty: accept all peers in trusted CIDRs)"
	}
	return strings.Join(list, ", ")
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
