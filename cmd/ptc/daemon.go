package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/tigeregit/peer-task-control/internal/deploy"
	"github.com/tigeregit/peer-task-control/internal/node"
)

func daemonCmd() *cobra.Command {
	var waitPid int
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Start the ptc daemon and join the mesh",
		RunE: func(cmd *cobra.Command, args []string) error {
			if waitPid > 0 {
				waitForExit(waitPid, 30*time.Second)
			}
			cfg, err := loadConfigOrDefault()
			if err != nil {
				return err
			}
			// Resolve the executable path now: after an update replaces it,
			// the restart must exec the new file at the same path.
			exe, _ := deploy.Exe()
			log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			n, err := node.Start(ctx, cfg, log)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "ptc daemon running as %s (%s); API %s\n", cfg.Name, n.Host.ID(), n.APIAddr())
			fmt.Fprintln(os.Stderr, "wait for the catalog, then use: ptc peers")
			select {
			case <-ctx.Done():
				return n.Close()
			case <-n.RestartRequested():
			}
			log.Info("restarting daemon", "exe", exe)
			_ = n.Close()
			stop()
			if exe == "" {
				return fmt.Errorf("restart requested but the executable path is unknown")
			}
			return restartSelf(exe)
		},
	}
	cmd.Flags().IntVar(&waitPid, "wait-pid", 0, "wait for this process to exit first (used by self-restart)")
	_ = cmd.Flags().MarkHidden("wait-pid")
	return cmd
}
