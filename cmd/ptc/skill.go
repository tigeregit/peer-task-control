package main

import (
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
	cmd.AddCommand(skillSyncCmd())
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
