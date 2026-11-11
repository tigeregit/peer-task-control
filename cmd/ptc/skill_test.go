package main

import (
	"os"
	"strings"
	"testing"

	"github.com/tigeregit/peer-task-control/internal/version"
)

// TestSkillInSync keeps the embedded skill text, the repository SKILL.md and
// the binary version bound to each other.
func TestSkillInSync(t *testing.T) {
	b, err := os.ReadFile("../../SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != skillFallback {
		t.Fatal("cmd/ptc/skill.txt differs from SKILL.md; copy SKILL.md to cmd/ptc/skill.txt")
	}
	if !strings.Contains(skillFallback, "**"+version.Version+"**") {
		t.Fatalf("skill text does not name version %s", version.Version)
	}
}
