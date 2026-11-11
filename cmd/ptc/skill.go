package main

import (
	"fmt"

	"github.com/spf13/cobra"

	_ "embed"
	"github.com/tigeregit/peer-task-control/internal/version"
)

//go:generate cp ../../SKILL.md skill.txt

//go:embed skill.txt
var skillFallback string

func skillCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "skill", Short: "Version-bound agent skill"}
	cmd.AddCommand(&cobra.Command{
		Use:   "print",
		Short: "Print the skill text that matches this binary",
		RunE: func(cmd *cobra.Command, args []string) error {
			text, err := skillText()
			if err != nil {
				return err
			}
			fmt.Print(text)
			if len(text) == 0 || text[len(text)-1] != '\n' {
				fmt.Println()
			}
			fmt.Printf("\n# bound-to-version %s\n", version.Version)
			return nil
		},
	})
	return cmd
}

// skillText returns the skill text embedded at build time. The embedded copy
// is the authority because it is bound to this binary's version; a SKILL.md on
// disk may belong to another version. cmd/ptc/skill.txt is generated from the
// repository SKILL.md by `go generate ./cmd/ptc` and TestSkillInSync fails when
// they drift.
func skillText() (string, error) {
	if skillFallback == "" {
		return "", fmt.Errorf("binary was built without embedded skill text")
	}
	return skillFallback, nil
}
