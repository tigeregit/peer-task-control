package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"embed"

	"github.com/tigeregit/peer-task-control/internal/version"
)

//go:embed skill/*.md
var skillFS embed.FS

// skillTopic is one document in the version-bound pack.
type skillTopic struct {
	Name    string `json:"name"`
	File    string `json:"-"`
	Summary string `json:"summary"`
}

var skillTopics = []skillTopic{
	{Name: "index", File: "SKILL.md", Summary: "Core loop, target rules, and topic map"},
	{Name: "submit", File: "submit.md", Summary: "Workflows, seed-only tasks, placement, queue/cancel"},
	{Name: "resources", File: "resources.md", Summary: "DirDigest vs board CID, Bitswap, pull/download"},
	{Name: "install", File: "install.md", Summary: "SSH install, local/remote/mesh update"},
	{Name: "discovery", File: "discovery.md", Summary: "Trusted CIDRs, scans, cooldown, relays"},
	{Name: "storage", File: "storage.md", Summary: "Seeds vs worktrees, quotas, ptc clean"},
	{Name: "errors", File: "errors.md", Summary: "Recovery for common failures"},
}

func skillCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "skill", Short: "Version-bound agent skill"}
	cmd.AddCommand(skillPrintCmd(), skillListCmd())
	return cmd
}

func skillPrintCmd() *cobra.Command {
	names := skillTopicNames()
	return &cobra.Command{
		Use:       "print [topic]",
		Short:     "Print the skill text that matches this binary",
		Long:      "Print the index, or one topic from the pack embedded in this binary. Unknown topics fail; use ptc skill list.",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: names,
		RunE: func(cmd *cobra.Command, args []string) error {
			topic := "index"
			if len(args) == 1 {
				topic = args[0]
			}
			text, err := skillText(topic)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), text)
			if len(text) == 0 || text[len(text)-1] != '\n' {
				fmt.Fprintln(cmd.OutOrStdout())
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\n# bound-to-version %s\n", version.Version)
			return nil
		},
	}
}

func skillListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List skill topics embedded in this binary",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if asJSON {
				payload := struct {
					Version string       `json:"version"`
					Topics  []skillTopic `json:"topics"`
				}{Version: version.Version, Topics: skillTopics}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(payload)
			}
			for _, t := range skillTopics {
				fmt.Fprintf(cmd.OutOrStdout(), "%-10s %s\n", t.Name, t.Summary)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print topics as JSON")
	return cmd
}

func skillText(topic string) (string, error) {
	meta, ok := lookupSkillTopic(topic)
	if !ok {
		return "", fmt.Errorf("unknown skill topic %q (try: %s)", topic, strings.Join(skillTopicNames(), ", "))
	}
	b, err := fs.ReadFile(skillFS, "skill/"+meta.File)
	if err != nil {
		return "", fmt.Errorf("skill topic %s: %w", topic, err)
	}
	if len(b) == 0 {
		return "", fmt.Errorf("skill topic %s is empty", topic)
	}
	return string(b), nil
}

func lookupSkillTopic(name string) (skillTopic, bool) {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" || name == "index" || name == "skill" {
		name = "index"
	}
	for _, t := range skillTopics {
		if t.Name == name {
			return t, true
		}
	}
	return skillTopic{}, false
}

func skillTopicNames() []string {
	names := make([]string, 0, len(skillTopics))
	for _, t := range skillTopics {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	return names
}
