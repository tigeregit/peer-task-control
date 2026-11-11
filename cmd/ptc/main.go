package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/tigeregit/peer-task-control/internal/config"
	"github.com/tigeregit/peer-task-control/internal/version"
)

var (
	cfgPath      string
	dataDir      string
	apiAddr      string
	seedsDir     string
	worktreesDir string
	runsDir      string
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "ptc",
		Short:         "peer-task-control: decentralized LAN task dispatch",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	defaultCfg := filepath.Join(config.DefaultDataDir(), "config.yaml")
	cmd.PersistentFlags().StringVar(&cfgPath, "config", defaultCfg, "path to config.yaml")
	cmd.PersistentFlags().StringVar(&dataDir, "data-dir", "", "override data directory (PTC_DATA_DIR)")
	cmd.PersistentFlags().StringVar(&seedsDir, "seeds-dir", "", "override CAS + packages directory (PTC_SEEDS_DIR)")
	cmd.PersistentFlags().StringVar(&worktreesDir, "worktrees-dir", "", "override worktree root (PTC_WORKTREES_DIR)")
	cmd.PersistentFlags().StringVar(&runsDir, "runs-dir", "", "override run records directory (PTC_RUNS_DIR)")
	cmd.PersistentFlags().StringVar(&apiAddr, "api", "", "local daemon API host:port (default: read daemon.lock)")
	cmd.AddCommand(daemonCmd(), peersCmd(), policyCmd(), skillCmd(), webuiCmd(), submitCmd(), resourcesCmd(), tasksCmd(), queueCmd(), cancelStandaloneCmd(), cleanCmd(), installCmd(), updateCmd(), versionCmd(), scanCmd())
	return cmd
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the binary version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version.Version)
		},
	}
}

func loadConfigOrDefault() (*config.Config, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		return nil, fmt.Errorf("config not found at %s (copy config.example.yaml and fill trusted_cidrs)", cfgPath)
	}
	applyPathFlags(cfg)
	return cfg, nil
}

func cancelStandaloneCmd() *cobra.Command {
	cmd := cancelCmd()
	cmd.Use = "cancel RUN-ID"
	return cmd
}
