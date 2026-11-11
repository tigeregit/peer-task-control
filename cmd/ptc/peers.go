package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/tigeregit/peer-task-control/internal/catalog"
	"github.com/tigeregit/peer-task-control/internal/config"
	"github.com/tigeregit/peer-task-control/internal/control"
	"github.com/tigeregit/peer-task-control/internal/disk"
	"github.com/tigeregit/peer-task-control/internal/query"
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
			fmt.Fprintln(w, "NAME\tPEER ID\tVERSION\tEXEC\tFILES\tRELAY\tADDRS")
			skew := map[string][]string{}
			for _, e := range st.Peers {
				v := versionCell(e)
				skew[v] = append(skew[v], e.Name)
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					e.Name,
					shortID(e.PeerID),
					v,
					svcCell(e, "exec"),
					svcCell(e, "files"),
					svcCell(e, "relay"),
					strings.Join(e.Addrs, ","),
				)
			}
			if err := w.Flush(); err != nil {
				return err
			}
			if len(skew) > 1 {
				fmt.Printf("\nversion skew: %s\n", skewLine(skew, st.Version))
				fmt.Println("  update install-managed nodes with: ptc update --mesh  (older nodes: ptc install --ssh USER@HOST)")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	cmd.AddCommand(peersShowCmd())
	return cmd
}

func peersShowCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show NAME|PEER",
		Short: "Show a node's hardware, historical tasks, and stored seeds",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return peersShow(args[0], jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	return cmd
}

func peersShow(id string, jsonOut bool) error {
	cli, err := controlClient()
	if err != nil {
		return err
	}
	st, err := cli.Status()
	if err != nil {
		return fmt.Errorf("talking to daemon: %w", err)
	}
	entry, ok := findPeer(st, id)
	if !ok {
		return fmt.Errorf("node %s not found", id)
	}
	runs, err := cli.Runs()
	if err != nil {
		return fmt.Errorf("talking to daemon: %w", err)
	}
	runs = query.FilterTasks(runs, query.TaskFilter{Node: preferID(entry), Sort: "started", Order: "desc"})
	list, err := cli.Resources()
	if err != nil {
		return fmt.Errorf("talking to daemon: %w", err)
	}
	seeds := query.FilterResources(list.Seeds, query.ResourceFilter{Node: preferID(entry), Sort: "name"})
	if jsonOut {
		return printJSON(map[string]any{
			"node":      entry,
			"tasks":     runs,
			"resources": seeds,
		})
	}
	fmt.Printf("name    %s\n", entry.Name)
	fmt.Printf("peer    %s\n", entry.PeerID)
	fmt.Printf("os/arch %s/%s\n", dash(entry.Caps.OS), dash(entry.Caps.Arch))
	if entry.Caps.NVIDIA || entry.Caps.GPUName != "" {
		fmt.Printf("gpu     %s\n", dash(entry.Caps.GPUName))
		if entry.Caps.VRAMMiB > 0 {
			fmt.Printf("vram    %d MiB\n", entry.Caps.VRAMMiB)
		}
		if entry.Caps.Driver != "" {
			fmt.Printf("driver  %s\n", entry.Caps.Driver)
		}
	} else {
		fmt.Printf("gpu     none\n")
	}
	fmt.Printf("version %s\n", versionCell(entry))
	fmt.Printf("addrs   %s\n", strings.Join(entry.Addrs, ", "))
	fmt.Printf("exec    %s\n", svcCell(entry, "exec"))
	fmt.Printf("files   %s\n", svcCell(entry, "files"))
	fmt.Printf("relay   %s\n", svcCell(entry, "relay"))
	if entry.Disk != nil {
		printDisk(entry.Disk)
	}
	fmt.Printf("\nTASKS (%d)\n", len(runs))
	if len(runs) == 0 {
		fmt.Println("(none)")
	} else {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tSTATUS\tNAME")
		for _, t := range runs {
			name := t.Name.ZH
			if name == "" {
				name = t.Name.EN
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", t.ID, t.Status, name)
		}
		_ = w.Flush()
	}
	fmt.Printf("\nRESOURCES (%d)\n", len(seeds))
	if len(seeds) == 0 {
		fmt.Println("(none)")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DIGEST\tNAME\tKIND\tSIZE")
	for _, s := range seeds {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.Digest, s.Name, s.Kind, humanSize(s.Size))
	}
	return w.Flush()
}

func findPeer(st control.Status, id string) (catalog.Entry, bool) {
	if query.MatchNode(st.Name, st.PeerID, id) {
		// Prefer a catalog copy that already has caps/addrs when present.
		for _, e := range st.Peers {
			if query.MatchNode(e.Name, e.PeerID, id) {
				return e, true
			}
		}
		return catalog.Entry{Name: st.Name, PeerID: st.PeerID, Addrs: st.Listen}, true
	}
	for _, e := range st.Peers {
		if query.MatchNode(e.Name, e.PeerID, id) {
			return e, true
		}
	}
	return catalog.Entry{}, false
}

func preferID(e catalog.Entry) string {
	if e.Name != "" {
		return e.Name
	}
	return e.PeerID
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
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

func printDisk(u *disk.Usage) {
	if u == nil {
		return
	}
	fmt.Printf("ptc     seeds %s  worktrees %s  outputs %s  records %s  blocks %s\n",
		humanSize(u.SeedsBytes), humanSize(u.WorktreesBytes), humanSize(u.OutputsBytes), humanSize(u.RecordsBytes), humanSize(u.BlocksBytes))
	for _, v := range u.Volumes {
		fmt.Printf("disk    %s (%s)  %s free / %s total  (%s used)\n",
			v.Path, v.Role, humanSize(v.Free), humanSize(v.Total), humanSize(v.Used))
	}
}

// versionCell shows the version plus whether the node takes mesh updates.
func versionCell(e catalog.Entry) string {
	if e.Version == "" {
		return "<0.3.0"
	}
	switch {
	case e.Managed && e.AcceptUpdate:
		return e.Version
	case e.Managed:
		return e.Version + " (no remote update)"
	default:
		return e.Version + " (unmanaged)"
	}
}

func skewLine(skew map[string][]string, local string) string {
	keys := make([]string, 0, len(skew))
	for k := range skew {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s: %s", k, strings.Join(skew[k], ", ")))
	}
	return strings.Join(parts, "; ") + " (this node: " + local + ")"
}
