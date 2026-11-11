package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tigeregit/peer-task-control/internal/deploy"
)

type sshDeploymentFlags struct {
	host         string
	version      string
	remotePath   string
	identityFile string
	knownHosts   string
	port         int
}

func installCmd() *cobra.Command {
	var flags sshDeploymentFlags
	cmd := &cobra.Command{
		Use:              "install --ssh HOST",
		Short:            "Install a verified ptc release binary on one host over SSH",
		PersistentPreRun: func(cmd *cobra.Command, args []string) { maybeWarnStaleSkill(cmd) },
		Long: `Install a verified ptc release binary on one host over SSH.

The target OS and architecture are detected over SSH. Supported targets are
Linux amd64/arm64 and native Windows amd64. SSH host-key verification uses
your normal OpenSSH configuration and known_hosts with StrictHostKeyChecking
enabled; password prompting is disabled. The release binary is SHA256-verified;
Windows additionally validates the staged ptc version before replacement.
Replacement rolls back on failure. Windows targets require the local OpenSSH
scp client for binary-safe uploads. This installs the binary only; it does not
configure or start a service.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSSHDeployment(cmd, flags, "install")
		},
	}
	addSSHDeploymentFlags(cmd, &flags, "absolute remote path (Linux default: $HOME/.local/bin/ptc; Windows default: per-user LocalAppData)", true)
	return cmd
}

func addSSHDeploymentFlags(cmd *cobra.Command, flags *sshDeploymentFlags, pathHelp string, requireSSH bool) {
	cmd.Flags().StringVar(&flags.host, "ssh", "", "SSH destination (host alias, hostname, address, or user@host)")
	cmd.Flags().StringVar(&flags.version, "version", "latest", "release version to deploy (for example 0.2.0-p2; default: latest stable)")
	cmd.Flags().StringVar(&flags.remotePath, "path", "", pathHelp)
	cmd.Flags().StringVar(&flags.identityFile, "identity", "", "SSH private-key file (prefer ssh-agent for encrypted keys)")
	cmd.Flags().StringVar(&flags.knownHosts, "known-hosts", "", "alternate known_hosts file (host-key checking remains strict)")
	cmd.Flags().IntVarP(&flags.port, "port", "p", 22, "SSH port")
	if requireSSH {
		_ = cmd.MarkFlagRequired("ssh")
	}
}

func runSSHDeployment(cmd *cobra.Command, flags sshDeploymentFlags, action string) error {
	if strings.TrimSpace(flags.host) != flags.host || flags.host == "" {
		return fmt.Errorf("--ssh must be a non-empty SSH destination")
	}
	if flags.port < 1 || flags.port > 65535 {
		return fmt.Errorf("--port must be between 1 and 65535")
	}
	ssh := deploy.SSHOptions{
		Host:           flags.host,
		Port:           flags.port,
		IdentityFile:   flags.identityFile,
		KnownHostsFile: flags.knownHosts,
		Stdout:         cmd.OutOrStdout(),
		Stderr:         cmd.ErrOrStderr(),
	}
	platform, err := ssh.DetectPlatform(cmd.Context())
	if err != nil {
		return err
	}
	if err := deploy.ValidateRemotePathForPlatform(flags.remotePath, platform); err != nil {
		return err
	}
	artifact, err := deploy.FetchRelease(cmd.Context(), flags.version, platform)
	if err != nil {
		return err
	}
	if err := ssh.Replace(cmd.Context(), flags.remotePath, artifact); err != nil {
		return err
	}
	if err := ssh.SyncSkill(cmd.Context(), flags.remotePath, platform); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: skill sync on %s failed: %v\n", flags.host, err)
	}
	target := flags.remotePath
	if target == "" {
		if platform == "windows/amd64" {
			target = "%LOCALAPPDATA%\\Programs\\ptc\\ptc.exe"
		} else {
			target = "$HOME/.local/bin/ptc"
		}
	}
	verb := "Installed"
	if action == "update" {
		verb = "Updated"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s ptc %s (%s) on %s at %s\n", verb, artifact.Version, platform, flags.host, target)
	return nil
}
