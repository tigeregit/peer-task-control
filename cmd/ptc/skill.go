package main

import (
	"fmt"

	"github.com/spf13/cobra"

	_ "embed"
	"github.com/tigeregit/peer-task-control/internal/version"
)

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

func skillText() (string, error) {
	if skillFallback != "" {
		return skillFallback, nil
	}
	return "", fmt.Errorf("SKILL.md not found next to the binary")
}
