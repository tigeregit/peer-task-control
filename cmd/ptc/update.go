package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/tigeregit/peer-task-control/internal/config"
	"github.com/tigeregit/peer-task-control/internal/control"
	"github.com/tigeregit/peer-task-control/internal/deploy"
	"github.com/tigeregit/peer-task-control/internal/version"
)

func updateCmd() *cobra.Command {
	var flags sshDeploymentFlags
	var mesh, noRestart, dryRun, jsonOut bool
	var nodes []string
	var batch int
	var assetsDir string
	cmd := &cobra.Command{
		Use:   "update [--ssh HOST | --mesh]",
		Short: "Update ptc locally, on one host over SSH, or across the mesh",
		Long: `Update the local ptc executable, one host's binary over SSH, or install-managed
peers on the mesh.

Without --ssh or --mesh, ptc updates its running executable on Linux or macOS.
On Windows, the running executable cannot be replaced this way; use --path to
select a different, non-running ptc executable (Windows amd64 only). A --path
target must already exist and pass the ptc version subcommand probe.
Replacement is checksum-verified and rolls back if it fails. After a successful
local update, ptc records install.json and restarts a running local daemon
unless --no-restart is set.

With --ssh, the target OS and architecture are detected remotely; supported
targets are Linux amd64/arm64 and native Windows amd64. Windows uses a
short encoded PowerShell launcher after an OpenSSH scp upload, checks the staged
ptc version and SHA256,
and restores the previous executable if replacement fails. OpenSSH's normal
known_hosts settings remain in force with StrictHostKeyChecking enabled;
password prompting is disabled. --known-hosts selects an alternate known_hosts
file. --identity, --known-hosts, and --port apply only to SSH. SSH update
replaces only the binary; it does not restart a remote daemon.

With --mesh: asks the local daemon to roll a version (default: this binary's)
out to install-managed peers in stages of --batch nodes. Peers pull the binary
from the mesh over bitswap, not from GitHub; each target only accepts it from
trusted peers, for the same major version, when update.accept_remote is on
(ptc policy update off refuses). --assets-dir is a directory with release
assets and SHA256SUMS for offline meshes.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if mesh && flags.host != "" {
				return fmt.Errorf("--mesh and --ssh cannot be used together")
			}
			if mesh {
				return meshUpdate(flags.version, nodes, batch, assetsDir, dryRun, jsonOut)
			}
			if cmd.Flags().Changed("ssh") && flags.host == "" {
				return fmt.Errorf("--ssh requires a destination")
			}
			if flags.host != "" {
				return runSSHDeployment(cmd, flags, "update")
			}
			if cmd.Flags().Changed("identity") || cmd.Flags().Changed("known-hosts") || cmd.Flags().Changed("port") {
				return fmt.Errorf("--identity, --known-hosts, and --port require --ssh")
			}
			if dryRun || jsonOut || assetsDir != "" || len(nodes) > 0 || cmd.Flags().Changed("batch") {
				return fmt.Errorf("--dry-run, --json, --assets-dir, --nodes, and --batch require --mesh")
			}
			return runLocalUpdate(cmd, flags, noRestart)
		},
	}
	addSSHDeploymentFlags(cmd, &flags, "local target without --ssh; remote target with --ssh (absolute path)", false)
	cmd.Flags().BoolVar(&mesh, "mesh", false, "roll the version out to install-managed peers over bitswap")
	cmd.Flags().StringSliceVar(&nodes, "nodes", nil, "with --mesh: only these node names or peer IDs")
	cmd.Flags().IntVar(&batch, "batch", 1, "with --mesh: nodes per stage")
	cmd.Flags().StringVar(&assetsDir, "assets-dir", "", "with --mesh: directory with release assets and SHA256SUMS (offline)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "with --mesh: show the plan without changing nodes")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "with --mesh: print JSON")
	cmd.Flags().BoolVar(&noRestart, "no-restart", false, "after a local update, do not restart the local daemon")
	return cmd
}

func runLocalUpdate(cmd *cobra.Command, flags sshDeploymentFlags, noRestart bool) error {
	result, err := deploy.UpdateLocal(cmd.Context(), flags.version, flags.remotePath)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Updated local ptc %s -> %s (%s) at %s\n",
		result.OldVersion, result.NewVersion, result.Platform, result.Path)
	if err := deploy.RecordInstall(updateDataDir(), result.Path, result.NewVersion); err != nil {
		fmt.Fprintf(os.Stderr, "warning: install.json not updated: %v\n", err)
	}
	if !noRestart {
		restartLocal(updateDataDir(), result.NewVersion)
	}
	return nil
}

func updateDataDir() string {
	if dataDir != "" {
		return dataDir
	}
	if cfg, err := config.Load(cfgPath); err == nil {
		return cfg.DataDir
	}
	return config.DefaultDataDir()
}

func restartLocal(dd, want string) {
	cli, err := control.ClientFromDataDir(dd, apiAddr)
	if err == nil {
		if st, err := cli.Status(); err == nil {
			if err := cli.Restart(); err == nil {
				for i := 0; i < 60; i++ {
					time.Sleep(500 * time.Millisecond)
					c2, err := control.ClientFromDataDir(dd, apiAddr)
					if err != nil {
						continue
					}
					if s2, err := c2.Status(); err == nil && s2.Version == want {
						fmt.Printf("daemon %s restarted at %s\n", st.Name, want)
						return
					}
				}
				fmt.Fprintf(os.Stderr, "warning: daemon %s did not come back at %s within 30s; if it runs from another binary, update that one\n", st.Name, want)
				return
			}
		}
	}
	in, ok := deploy.Load(dd)
	if !ok {
		fmt.Println("no running daemon found; start it with: ptc daemon")
		return
	}
	cmds := deploy.RestartCommand(in)
	if len(cmds) == 0 {
		fmt.Println("daemon not running; start it with: ptc daemon")
		return
	}
	for _, c := range cmds {
		out, err := exec.Command(c[0], c[1:]...).CombinedOutput()
		if err != nil && !(len(c) > 1 && c[1] == "/End") {
			fmt.Fprintf(os.Stderr, "warning: %s: %v %s\n", strings.Join(c, " "), err, strings.TrimSpace(string(out)))
			return
		}
	}
	fmt.Printf("restarted service %s (%s)\n", in.ServiceName, in.Service)
}

func meshUpdate(ver string, nodes []string, batch int, assetsDir string, dryRun, jsonOut bool) error {
	cli, err := controlClient()
	if err != nil {
		return fmt.Errorf("--mesh needs a running ptc daemon: %w", err)
	}
	if ver == "" {
		ver = version.Version
	}
	rep, err := cli.MeshUpdate(control.MeshUpdateRequest{Version: ver, Nodes: nodes, Batch: batch, AssetsDir: assetsDir, DryRun: dryRun})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(rep)
	}
	fmt.Printf("mesh update to %s", rep.Version)
	if dryRun {
		fmt.Print(" (dry run)")
	}
	fmt.Println()
	for k, a := range rep.Assets {
		fmt.Printf("  asset %s: %s %s (%d bytes, served over bitswap)\n", k, a.Name, shortDigest(a.Digest), a.Size)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NODE\tPLATFORM\tFROM\tSTAGE\tSTATUS\tTOOK\tMIB/S\tDETAIL")
	failed := false
	for _, n := range rep.Nodes {
		stage, rate := "-", "-"
		if n.Stage > 0 {
			stage = fmt.Sprint(n.Stage)
		}
		if n.MiBs > 0 {
			rate = fmt.Sprintf("%.1f", n.MiBs)
		}
		detail := n.Detail
		if len(n.Providers) > 0 {
			detail = strings.TrimSpace(detail + " from " + strings.Join(n.Providers, ", "))
		}
		if n.Status == "failed" {
			failed = true
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", n.Name, n.Platform, dash(n.From), stage, n.Status, dash(n.Took), rate, detail)
	}
	_ = w.Flush()
	if failed {
		return fmt.Errorf("mesh update incomplete")
	}
	return nil
}

func shortDigest(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		return "sha256:" + d[:12] + "\u2026"
	}
	return "sha256:" + d
}
