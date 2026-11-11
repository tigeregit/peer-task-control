package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/tigeregit/peer-task-control/internal/config"
	"github.com/tigeregit/peer-task-control/internal/control"
	"github.com/tigeregit/peer-task-control/internal/release"
	"github.com/tigeregit/peer-task-control/internal/selfupdate"
	"github.com/tigeregit/peer-task-control/internal/version"
)

func updateCmd() *cobra.Command {
	var (
		ver, binPath, assetsDir string
		nodes                   []string
		check, dryRun, rollback bool
		mesh, noRestart, force  bool
		jsonOut                 bool
		batch                   int
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update this ptc binary (or the mesh) from GitHub Releases, with rollback",
		Long: `Without --mesh: downloads the release asset for this platform (default: the
latest release; pre-releases count when this binary is a pre-release),
verifies it against SHA256SUMS, atomically replaces this executable (the old
one is kept as <exe>.prev), refreshes SKILL.md next to it, updates
install.json and restarts the local daemon. --rollback swaps back.

With --mesh: asks the local daemon to roll a version (default: this binary's)
out to install-managed peers in stages of --batch nodes. Peers pull the binary
from the mesh over bitswap, not from GitHub; each target only accepts it from
trusted peers, for the same major version, when update.accept_remote is on
(ptc policy update off refuses).

PTC_RELEASE_BASE (mirror URL) or PTC_RELEASE_DIR / --assets-dir (a directory
with the assets and SHA256SUMS) replace GitHub for offline meshes.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if mesh {
				return meshUpdate(ver, nodes, batch, assetsDir, dryRun, jsonOut)
			}
			exe, err := selfupdate.Exe()
			if err != nil {
				return err
			}
			dd := updateDataDir()
			if rollback {
				if dryRun {
					fmt.Printf("would swap %s with %s\n", exe, selfupdate.PrevPath(exe))
					return nil
				}
				if err := selfupdate.Rollback(exe); err != nil {
					return err
				}
				now, err := selfupdate.BinaryVersion(exe)
				if err != nil {
					return err
				}
				_ = selfupdate.WriteSkill(exe)
				recordInstall(dd, exe, now)
				fmt.Printf("rolled back %s: %s -> %s (the %s binary is kept as %s)\n", exe, version.Version, now, version.Version, selfupdate.PrevPath(exe))
				if !noRestart {
					restartLocal(dd, now)
				}
				return nil
			}
			src := release.Default()
			if assetsDir != "" {
				src = release.Source{Dir: assetsDir}
			}
			if cfg, err := config.Load(cfgPath); err == nil && cfg.Update.Repo != "" {
				src.Repo = cfg.Update.Repo
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			target := strings.TrimPrefix(ver, "v")
			if target == "" || target == "latest" {
				info, err := src.Latest(ctx, version.Parse(version.Version).Pre != "")
				if err != nil {
					return err
				}
				target = strings.TrimPrefix(info.Tag, "v")
			}
			cmp := version.Compare(target, version.Version)
			if check {
				state := "up to date"
				if cmp > 0 {
					state = "update available"
				} else if cmp < 0 {
					state = "this binary is newer"
				}
				if jsonOut {
					return printJSON(map[string]any{"current": version.Version, "target": target, "state": state, "source": src.Describe(target)})
				}
				fmt.Printf("current %s, release %s (%s): %s\n", version.Version, target, src.Describe(target), state)
				return nil
			}
			if cmp == 0 && !force && binPath == "" {
				fmt.Printf("already at %s\n", target)
				return nil
			}
			if cmp < 0 && !force {
				return fmt.Errorf("%s is older than %s; use --force to downgrade or --rollback to restore the previous binary", target, version.Version)
			}
			var bin []byte
			from := ""
			if binPath != "" {
				bin, err = os.ReadFile(binPath)
				from = binPath
			} else {
				var sums map[string]string
				sums, err = src.FetchSums(ctx, target)
				if err == nil {
					asset := release.AssetName(runtime.GOOS, runtime.GOARCH)
					var d string
					bin, d, err = src.Asset(ctx, target, asset, sums)
					from = fmt.Sprintf("%s from %s (%s, SHA256SUMS verified)", asset, src.Describe(target), d)
				}
			}
			if err != nil {
				return err
			}
			if dryRun {
				fmt.Printf("would replace %s (%s) with %s %s; previous kept at %s\n", exe, version.Version, target, from, selfupdate.PrevPath(exe))
				return nil
			}
			if err := selfupdate.Replace(exe, bin); err != nil {
				return err
			}
			got, err := selfupdate.BinaryVersion(exe)
			if err != nil || got != target {
				_ = selfupdate.Rollback(exe)
				return fmt.Errorf("new binary reports %q (%v), want %s; rolled back", got, err, target)
			}
			if err := selfupdate.WriteSkill(exe); err != nil {
				fmt.Fprintln(os.Stderr, "warning: skill copy not refreshed:", err)
			}
			recordInstall(dd, exe, got)
			fmt.Printf("updated %s: %s -> %s (%s); previous kept at %s\n", exe, version.Version, got, from, selfupdate.PrevPath(exe))
			if !noRestart {
				restartLocal(dd, got)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&ver, "version", "", "target version (default latest; with --mesh default this binary's version)")
	f.BoolVar(&check, "check", false, "only report whether an update is available")
	f.BoolVar(&dryRun, "dry-run", false, "show what would change")
	f.BoolVar(&rollback, "rollback", false, "restore the previous binary (<exe>.prev)")
	f.BoolVar(&force, "force", false, "allow reinstalling the same version or a downgrade")
	f.StringVar(&binPath, "binary", "", "install this local file instead of downloading")
	f.StringVar(&assetsDir, "assets-dir", "", "directory with release assets and SHA256SUMS (offline)")
	f.BoolVar(&noRestart, "no-restart", false, "do not restart the local daemon")
	f.BoolVar(&mesh, "mesh", false, "roll the version out to install-managed peers over bitswap")
	f.StringSliceVar(&nodes, "nodes", nil, "with --mesh: only these node names or peer IDs")
	f.IntVar(&batch, "batch", 1, "with --mesh: nodes per stage")
	f.BoolVar(&jsonOut, "json", false, "print JSON")
	return cmd
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

func recordInstall(dd, exe, now string) {
	in, ok := selfupdate.Load(dd)
	if !ok || in.Binary != exe {
		return
	}
	in.Previous, in.Version, in.UpdatedAt = in.Version, now, time.Now().UTC()
	_ = selfupdate.Save(dd, in)
}

// restartLocal restarts the daemon so it runs the new binary: through its
// API (it re-execs itself) when it is up, else through the installed service.
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
	in, ok := selfupdate.Load(dd)
	if !ok {
		fmt.Println("no running daemon found; start it with: ptc daemon")
		return
	}
	cmds := selfupdate.RestartCommand(in)
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
		return "sha256:" + d[:12] + "…"
	}
	return "sha256:" + d
}
