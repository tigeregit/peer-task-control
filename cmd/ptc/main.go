package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/peer-task-control/ptc/internal/config"
	"github.com/peer-task-control/ptc/internal/version"
)

var (
	cfgPath string
	dataDir string
	apiAddr string
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
	cmd.PersistentFlags().StringVar(&dataDir, "data-dir", "", "override data directory")
	cmd.PersistentFlags().StringVar(&apiAddr, "api", "", "local daemon API host:port (default: read daemon.lock)")
	cmd.AddCommand(daemonCmd(), peersCmd(), policyCmd(), skillCmd(), versionCmd())
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
	if dataDir != "" {
		cfg.DataDir = dataDir
	}
	return cfg, nil
}
