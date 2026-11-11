package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

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
	// Prefer the SKILL.md shipped next to the source or the executable.
	candidates := []string{}
	if _, file, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates, filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "SKILL.md")))
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, "SKILL.md"), filepath.Join(dir, "..", "SKILL.md"))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, "SKILL.md"))
	}
	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if err == nil && len(b) > 0 {
			return string(b), nil
		}
	}
	if skillFallback != "" {
		return skillFallback, nil
	}
	return "", fmt.Errorf("SKILL.md not found next to the binary")
}
