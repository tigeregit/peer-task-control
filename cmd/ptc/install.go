package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/tigeregit/peer-task-control/internal/install"
	"github.com/tigeregit/peer-task-control/internal/release"
	"github.com/tigeregit/peer-task-control/internal/selfupdate"
	"github.com/tigeregit/peer-task-control/internal/version"
)

func installCmd() *cobra.Command {
	var (
		target, osFlag, cfgFile, name, binPath, ver, svcName, knownHosts          string
		keys, cidrs                                                               []string
		port                                                                      int
		service, system, dryRun, jsonOut, insecure, overwrite, exec, files, relay bool
	)
	cmd := &cobra.Command{
		Use:   "install --ssh user@host",
		Short: "Install ptc on another machine over SSH (binary, config, skill, optional service)",
		Long: `Copies the binary for the remote platform with SFTP, writes the config and the
version-bound SKILL.md, records install.json (so ptc update can manage it) and,
with --service, registers the daemon (systemd unit on Linux, a nohup process when
there is no systemd, a logon scheduled task on Windows).

The binary comes from this executable when the platform and version match, else
from --binary, else from the GitHub Release asset of the same version, verified
against SHA256SUMS. Authentication uses ssh-agent and key files only; host keys
are checked against known_hosts. Re-running is safe: unchanged files are skipped.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if target == "" {
				return fmt.Errorf("--ssh user@host is required")
			}
			t := install.ParseTarget(target)
			if port != 0 {
				t.Port = port
			}
			t.KeyFiles, t.KnownHosts, t.InsecureNoHKC = keys, knownHosts, insecure
			if insecure {
				fmt.Fprintln(os.Stderr, "warning: --insecure-ignore-host-key: the remote host key is NOT verified")
			}
			if ver == "" {
				ver = version.Version
			}
			ver = strings.TrimPrefix(ver, "v")
			conn, err := install.Dial(t)
			if err != nil {
				return err
			}
			defer conn.Close()
			plat, err := install.Detect(conn, osFlag)
			if err != nil {
				return err
			}
			log := os.Stderr
			bin, skill, src, err := resolveBinary(plat, ver, binPath)
			if err != nil {
				return err
			}
			fmt.Fprintf(log, "binary for %s, version %s: %s\n", plat, ver, src)
			opt := install.Options{
				OS: plat.OS, System: system, Service: service, ServiceName: svcName,
				Version: ver, Binary: bin, Skill: skill, DryRun: dryRun, OverwriteConfig: overwrite, Log: log,
			}
			if cfgFile != "" {
				b, err := os.ReadFile(cfgFile)
				if err != nil {
					return err
				}
				opt.Config = b
			}
			if len(cidrs) > 0 {
				opt.GenConfig = func(l install.Layout) ([]byte, error) {
					return genConfig(l, name, cidrs, exec, files, relay)
				}
			}
			rep, err := install.Run(conn, opt)
			if jsonOut {
				_ = printJSON(rep)
			}
			if err != nil {
				return err
			}
			if !jsonOut {
				state := "installed"
				if dryRun {
					state = "dry run, nothing changed"
				}
				prev := rep.Previous
				if prev == "" {
					prev = "none"
				}
				fmt.Printf("%s: ptc %s on %s (%s) at %s; previous %s; service %s\n", state, ver, target, rep.Platform, rep.Layout.Bin, prev, rep.Service)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&target, "ssh", "", "user@host[:port] or an alias from ~/.ssh/config")
	f.StringArrayVar(&keys, "key", nil, "private key file (repeatable; default: ssh-agent, ~/.ssh/config IdentityFile, ~/.ssh/id_*)")
	f.IntVar(&port, "port", 0, "SSH port (default from ~/.ssh/config or 22)")
	f.StringVar(&knownHosts, "known-hosts", "", "known_hosts file (default ~/.ssh/known_hosts)")
	f.BoolVar(&insecure, "insecure-ignore-host-key", false, "do not verify the remote host key (unsafe; explicit opt-in)")
	f.StringVar(&osFlag, "os", "auto", "remote OS: auto, linux or windows")
	f.StringVar(&cfgFile, "config", "", "config.yaml to upload to the remote host (default: keep the remote one, or generate from --trusted-cidr)")
	f.StringVar(&name, "name", "", "node name for a generated config (default: remote hostname)")
	f.StringArrayVar(&cidrs, "trusted-cidr", nil, "trusted CIDR for a generated config (repeatable)")
	f.BoolVar(&exec, "exec", true, "generated config: services.exec")
	f.BoolVar(&files, "files", true, "generated config: services.files")
	f.BoolVar(&relay, "relay", false, "generated config: services.relay")
	f.BoolVar(&overwrite, "overwrite-config", false, "replace an existing remote config with the generated one")
	f.BoolVar(&service, "service", false, "register and start the daemon as a service")
	f.StringVar(&svcName, "service-name", "ptc", "service / unit / task name")
	f.BoolVar(&system, "system", false, "system-wide install (/usr/local/bin with root or sudo -n; Program Files when elevated)")
	f.StringVar(&binPath, "binary", "", "local binary built for the remote platform (offline installs)")
	f.StringVar(&ver, "version", "", "version to install (default: this binary's version)")
	f.BoolVar(&dryRun, "dry-run", false, "connect and show the plan without changing the remote host")
	f.BoolVar(&jsonOut, "json", false, "print the report as JSON")
	return cmd
}

// resolveBinary picks the remote binary and the matching skill text.
func resolveBinary(p install.Platform, ver, binPath string) ([]byte, []byte, string, error) {
	var skill []byte
	if ver == version.Version {
		skill = []byte(skillFallback)
	}
	if binPath != "" {
		b, err := os.ReadFile(binPath)
		return b, skill, binPath, err
	}
	if p.OS == runtime.GOOS && p.Arch == runtime.GOARCH && ver == version.Version {
		exe, err := selfupdate.Exe()
		if err != nil {
			return nil, nil, "", err
		}
		b, err := os.ReadFile(exe)
		return b, skill, "this executable (" + exe + ")", err
	}
	src := release.Default()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	sums, err := src.FetchSums(ctx, ver)
	if err != nil {
		return nil, nil, "", err
	}
	asset := release.AssetName(p.OS, p.Arch)
	b, digest, err := src.Asset(ctx, ver, asset, sums)
	if err != nil {
		return nil, nil, "", err
	}
	if skill == nil {
		if s, _, err := src.Asset(ctx, ver, release.SkillAsset(ver), sums); err == nil {
			skill = s
		}
	}
	return b, skill, fmt.Sprintf("release %s from %s (%s, SHA256SUMS verified)", asset, src.Describe(ver), digest), nil
}

func genConfig(l install.Layout, name string, cidrs []string, exec, files, relay bool) ([]byte, error) {
	type svc struct {
		Exec  bool `yaml:"exec"`
		Files bool `yaml:"files"`
		Relay bool `yaml:"relay"`
	}
	cfg := struct {
		Name         string   `yaml:"name,omitempty"`
		Listen       string   `yaml:"listen"`
		API          string   `yaml:"api"`
		DataDir      string   `yaml:"data_dir"`
		TrustedCIDRs []string `yaml:"trusted_cidrs"`
		Services     svc      `yaml:"services"`
	}{name, "0.0.0.0:18765", "127.0.0.1:18780", l.DataDir, cidrs, svc{exec, files, relay}}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return append([]byte("# written by ptc install --ssh\n"), b...), nil
}
