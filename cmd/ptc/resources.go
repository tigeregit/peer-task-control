package main

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/tigeregit/peer-task-control/internal/query"
	"github.com/tigeregit/peer-task-control/internal/resource"
)

func resourcesCmd() *cobra.Command {
	var jsonOut bool
	var f query.ResourceFilter
	cmd := &cobra.Command{
		Use:     "resources",
		Aliases: []string{"resource", "res"},
		Short:   "List mesh seeds, show a seed, or download it",
		Long:    "Uses the same /api/v1/resources interface as the WebUI. The CLI is the headless, complete form of that board: list fleet-wide seeds, show size and device locations with relay hop counts from the current node, and download a seed. List flags share the WebUI search, kind, node, and sort semantics.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return resourcesList(jsonOut, f)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	cmd.Flags().StringVar(&f.Q, "search", "", "substring over digest, name, kind, file paths, and locations")
	cmd.Flags().StringVar(&f.Kind, "kind", "", "all, tree, or blob")
	cmd.Flags().StringVar(&f.Node, "node", "", "only seeds stored on this node name or peer id")
	cmd.Flags().StringVar(&f.Sort, "sort", "name", "name, size, kind, hops, replicas, created, digest")
	cmd.Flags().StringVar(&f.Order, "order", "", "asc or desc (default desc for size/replicas)")
	cmd.AddCommand(resourcesShowCmd(), resourcesGetCmd())
	return cmd
}

func resourcesShowCmd() *cobra.Command {
	var jsonOut, listFiles bool
	cmd := &cobra.Command{
		Use:   "show DIGEST",
		Short: "Show a seed's size, replicas, and hop counts from this node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := controlClient()
			if err != nil {
				return err
			}
			info, err := cli.Resource(args[0])
			if err != nil {
				return fmt.Errorf("talking to daemon: %w", err)
			}
			if jsonOut {
				return printJSON(info)
			}
			fmt.Printf("digest  %s\n", info.Digest)
			if info.Name != "" {
				fmt.Printf("name    %s\n", info.Name)
			}
			fmt.Printf("kind    %s\n", info.Kind)
			fmt.Printf("size    %s (%d bytes)\n", humanSize(info.Size), info.Size)
			fmt.Printf("created %s\n", fmtCreated(info.CreatedAt))
			fmt.Printf("nodes   %d\n", len(info.Locations))
			if info.Unit != "" {
				fmt.Printf("unit    %s\n", info.Unit)
			}
			if info.FileCount > 0 {
				fmt.Printf("files   %d\n", info.FileCount)
			}
			fmt.Printf("hops    %s\n", fmtMinHops(info.Locations))
			fmt.Println()
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NODE\tPEER ID\tHOPS\tVIA\tCONNECTED")
			for _, loc := range info.Locations {
				conn := "no"
				if loc.Connected {
					conn = "yes"
				}
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", loc.Node, shortID(loc.PeerID), loc.Hops, loc.Via, conn)
			}
			if err := w.Flush(); err != nil {
				return err
			}
			printResourceFiles(info, listFiles)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	cmd.Flags().BoolVar(&listFiles, "list-files", false, "list every path in a tree seed (default: count only when there are more than 100 files)")
	return cmd
}

func resourcesGetCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "get DIGEST",
		Short: "Download a seed from the closest replica",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := controlClient()
			if err != nil {
				return err
			}
			blob, err := cli.ResourceContent(args[0])
			if err != nil {
				return fmt.Errorf("talking to daemon: %w", err)
			}
			defer blob.Body.Close()
			if out == "" || out == "-" {
				_, err = io.Copy(os.Stdout, blob.Body)
				return err
			}
			f, err := os.Create(out)
			if err != nil {
				return err
			}
			defer f.Close()
			n, err := io.Copy(f, blob.Body)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "wrote %s (%s)\n", out, humanSize(n))
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "output file (default: stdout)")
	return cmd
}

func resourcesList(jsonOut bool, f query.ResourceFilter) error {
	cli, err := controlClient()
	if err != nil {
		return err
	}
	list, err := cli.Resources()
	if err != nil {
		return fmt.Errorf("talking to daemon: %w", err)
	}
	list.Seeds = query.FilterResources(list.Seeds, f)
	if jsonOut {
		return printJSON(list)
	}
	if list.Viewer != "" {
		fmt.Printf("viewer %s  peer %s  %d seed(s)\n\n", list.Viewer, list.PeerID, len(list.Seeds))
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DIGEST\tNAME\tKIND\tSIZE\tNODES\tCREATED\tHOPS")
	for _, s := range list.Seeds {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n", s.Digest, s.Name, s.Kind, humanSize(s.Size), len(s.Locations), fmtCreated(s.CreatedAt), fmtMinHops(s.Locations))
	}
	return w.Flush()
}

func fmtCreated(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	return t.In(loc).Format("2006-01-02 15:04")
}

func fmtMinHops(locs []resource.Location) string {
	if len(locs) == 0 {
		return "-"
	}
	m := locs[0].Hops
	for _, loc := range locs[1:] {
		if loc.Hops < m {
			m = loc.Hops
		}
	}
	return fmt.Sprintf("%d", m)
}

func locSummary(locs []resource.Location) string {
	if len(locs) == 0 {
		return "-"
	}
	out := ""
	for i, loc := range locs {
		if i > 0 {
			out += ", "
		}
		name := loc.Node
		if name == "" {
			name = shortID(loc.PeerID)
		}
		out += fmt.Sprintf("%s:%d", name, loc.Hops)
	}
	return out
}

func humanSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	val := float64(n)
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	u := -1
	for val >= 1024 && u < len(units)-1 {
		val /= 1024
		u++
	}
	if u < 0 {
		return fmt.Sprintf("%d B", n)
	}
	if val >= 10 {
		return fmt.Sprintf("%.0f %s", val, units[u])
	}
	return fmt.Sprintf("%.1f %s", val, units[u])
}

func printResourceFiles(info resource.Info, listFiles bool) {
	count := info.FileCount
	if count == 0 {
		count = len(info.Files)
	}
	if count == 0 {
		return
	}
	fmt.Println()
	if !listFiles && count > resource.DirectoryUnitMinFiles {
		unit := info.Unit
		if unit == "" {
			unit = "directory"
		}
		fmt.Printf("%d files, %s unit; not listing paths (pass --list-files)\n", count, unit)
		return
	}
	if len(info.Files) == 0 {
		fmt.Printf("%d files (listing not in this advertisement)\n", count)
		return
	}
	fw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(fw, "PATH\tDIGEST\tSIZE")
	for _, f := range info.Files {
		kind := f.Digest
		if f.Dir && kind == "" {
			kind = "dir"
		}
		fmt.Fprintf(fw, "%s\t%s\t%s\n", f.Path, kind, humanSize(f.Size))
	}
	_ = fw.Flush()
}
