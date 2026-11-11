package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tigeregit/peer-task-control/internal/deploy"
)

func updateCmd() *cobra.Command {
	var flags sshDeploymentFlags
	cmd := &cobra.Command{
		Use:   "update [--ssh HOST]",
		Short: "Update ptc locally or on one host over SSH",
		Long: `Update the local ptc executable, or one host's binary over SSH.

Without --ssh, ptc updates its running executable on Linux or macOS. On Windows,
the running executable cannot be replaced; use --path to select a different,
non-running ptc executable (Windows amd64 only). A --path target must already
exist and pass the ptc version subcommand probe. Replacement is checksum-verified
and rolls back if it fails.

With --ssh, the target OS and architecture are detected remotely; supported
targets are Linux amd64/arm64 and native Windows amd64. Windows uses a
short encoded PowerShell launcher after an OpenSSH scp upload, checks the staged
ptc version and SHA256,
and restores the previous executable if replacement fails. OpenSSH's normal
known_hosts settings remain in force with StrictHostKeyChecking enabled;
password prompting is disabled. --known-hosts selects an alternate known_hosts
file. --identity, --known-hosts, and --port apply only to SSH. In both modes
this updates only the binary: it does not restart a running daemon or
upgrade/coordinate the mesh.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("ssh") && flags.host == "" {
				return fmt.Errorf("--ssh requires a destination")
			}
			if flags.host != "" {
				return runSSHDeployment(cmd, flags, "update")
			}
			if cmd.Flags().Changed("identity") || cmd.Flags().Changed("known-hosts") || cmd.Flags().Changed("port") {
				return fmt.Errorf("--identity, --known-hosts, and --port require --ssh")
			}
			return runLocalUpdate(cmd, flags)
		},
	}
	addSSHDeploymentFlags(cmd, &flags, "local target without --ssh; remote target with --ssh (absolute path)", false)
	return cmd
}

func runLocalUpdate(cmd *cobra.Command, flags sshDeploymentFlags) error {
	result, err := deploy.UpdateLocal(cmd.Context(), flags.version, flags.remotePath)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Updated local ptc %s -> %s (%s) at %s\n",
		result.OldVersion, result.NewVersion, result.Platform, result.Path)
	return nil
}
