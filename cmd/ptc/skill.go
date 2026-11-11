package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tigeregit/peer-task-control/internal/version"
	"github.com/tigeregit/peer-task-control/skills"
)

func skillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:              "skill",
		Short:            "Agent skill pack bound to this binary",
		PersistentPreRun: func(cmd *cobra.Command, args []string) { maybeWarnStaleSkill(cmd) },
		SilenceUsage:     true,
	}
	cmd.AddCommand(skillSyncCmd(), skillListCmd(), skillReadCmd())
	return cmd
}

func skillSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Write the bundled skill to ~/.agents/skills/peer-task-control",
		Long:  "Write the skill pack compiled into this binary to the user-scope Agent Skills directory (~/.agents/skills/peer-task-control, or %USERPROFILE%\\.agents\\skills\\peer-task-control on Windows). An existing copy of this skill is replaced; other skills in the same directory are left untouched.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := skills.SyncUser(version.Version)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Wrote skill %s %s to %s\n", skills.Name, version.Version, dir)
			return nil
		},
	}
}

func skillListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the bundled skill, install status, and reference files",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := skills.InstallStatus(version.Version)
			if err != nil {
				return err
			}
			if asJSON {
				payload := struct {
					Name    string        `json:"name"`
					Version string        `json:"version"`
					Install skills.Status `json:"install"`
					Files   []skills.File `json:"files"`
				}{Name: skills.Name, Version: version.Version, Install: st, Files: skills.Files}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(payload)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s  bundled %s\n", skills.Name, version.Version)
			switch {
			case !st.Present:
				fmt.Fprintf(cmd.OutOrStdout(), "  install  (missing)  run ptc skill sync\n")
			case st.Stale:
				installed := st.Version
				if installed == "" {
					installed = "unknown"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  install  %s  %s  stale (binary is %s)\n", st.Path, installed, version.Version)
			default:
				fmt.Fprintf(cmd.OutOrStdout(), "  install  %s  %s  current\n", st.Path, st.Version)
			}
			for _, f := range skills.Files {
				fmt.Fprintf(cmd.OutOrStdout(), "  %-28s %s\n", f.Path, f.Summary)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}

func skillReadCmd() *cobra.Command {
	names := skillPathNames()
	return &cobra.Command{
		Use:       "read [path]",
		Aliases:   []string{"print"},
		Short:     "Print a file from the skill pack embedded in this binary",
		Long:      "Print SKILL.md, or a path such as references/storage.md, from the pack compiled into this binary. This is a fallback when the on-disk skill at ~/.agents/skills/peer-task-control is missing. Prefer that installed copy, and refresh it with ptc skill sync. print is an alias of read. Guidance is written to stderr; stdout is the file contents. Unknown paths fail; use ptc skill list.",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: names,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "SKILL.md"
			if len(args) == 1 {
				name = args[0]
			}
			data, err := skills.Read(name, version.Version)
			if err != nil {
				return err
			}
			text := string(data)
			fmt.Fprint(cmd.OutOrStdout(), text)
			if len(text) == 0 || text[len(text)-1] != '\n' {
				fmt.Fprintln(cmd.OutOrStdout())
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "Prefer the installed skill at ~/.agents/skills/peer-task-control when present; ptc skill sync refreshes it.")
			return nil
		},
	}
}

func skillPathNames() []string {
	names := make([]string, 0, len(skills.Files))
	for _, f := range skills.Files {
		names = append(names, f.Path)
	}
	return names
}

func maybeWarnStaleSkill(cmd *cobra.Command) {
	if suppressSkillNotice(cmd) {
		return
	}
	st, err := skills.InstallStatus(version.Version)
	if err != nil || !st.Present || !st.Stale {
		return
	}
	installed := st.Version
	if installed == "" {
		installed = "unknown"
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "warning: skill at %s is version %s; this binary is %s. Run ptc skill sync or ptc update to refresh it.\n", st.Path, installed, version.Version)
}

func suppressSkillNotice(cmd *cobra.Command) bool {
	if strings.TrimSpace(os.Getenv("CI")) != "" {
		return true
	}
	if cmd != nil && (cmd.Name() == "help" || cmd.Name() == "cobra-cli") {
		return true
	}
	return commandWantsJSON(cmd)
}

func commandWantsJSON(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if f := c.Flags().Lookup("json"); f != nil && f.Changed {
			v, err := c.Flags().GetBool("json")
			if err == nil && v {
				return true
			}
		}
	}
	return false
}
