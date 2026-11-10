package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/peer-task-control/ptc/internal/node"
)

func daemonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Start the ptc daemon and join the mesh",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfigOrDefault()
			if err != nil {
				return err
			}
			log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			n, err := node.Start(ctx, cfg, log)
			if err != nil {
				return err
			}
			defer n.Close()
			fmt.Fprintf(os.Stderr, "ptc daemon running as %s (%s); API %s\n", cfg.Name, n.Host.ID(), n.APIAddr())
			fmt.Fprintln(os.Stderr, "wait for the catalog, then use: ptc peers")
			<-ctx.Done()
			return nil
		},
	}
}
