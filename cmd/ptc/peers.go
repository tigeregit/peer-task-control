package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/peer-task-control/ptc/internal/catalog"
	"github.com/peer-task-control/ptc/internal/config"
	"github.com/peer-task-control/ptc/internal/control"
)

func peersCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "peers",
		Short: "List known nodes and their services",
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := controlClient()
			if err != nil {
				return err
			}
			st, err := cli.Status()
			if err != nil {
				return fmt.Errorf("talking to daemon: %w", err)
			}
			if jsonOut {
				return printJSON(st)
			}
			fmt.Printf("local %s  peer %s  version %s\n\n", st.Name, st.PeerID, st.Version)
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tPEER ID\tEXEC\tFILES\tRELAY\tADDRS")
			for _, e := range st.Peers {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					e.Name,
					shortID(e.PeerID),
					svcCell(e, "exec"),
					svcCell(e, "files"),
					svcCell(e, "relay"),
					strings.Join(e.Addrs, ","),
				)
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	return cmd
}

func controlClient() (*control.Client, error) {
	dir := dataDir
	if dir == "" {
		if cfg, err := config.Load(cfgPath); err == nil {
			dir = cfg.DataDir
		} else {
			dir = config.DefaultDataDir()
		}
	}
	return control.ClientFromDataDir(dir, apiAddr)
}

func svcCell(e catalog.Entry, name string) string {
	st, ok := e.Services[name]
	if !ok {
		return "-"
	}
	if st.Offered {
		return "yes"
	}
	if st.Reason != "" {
		return "no (" + st.Reason + ")"
	}
	if st.Enabled {
		return "no"
	}
	return "no (disabled)"
}

func shortID(id string) string {
	if len(id) > 16 {
		return id[:16] + "…"
	}
	return id
}
