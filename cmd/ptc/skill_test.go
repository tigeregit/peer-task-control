package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/tigeregit/peer-task-control/internal/version"
)

// TestSkillPackInSync keeps the embedded pack, the repository SKILL.md
// index, and the binary version bound to each other.
func TestSkillPackInSync(t *testing.T) {
	index, err := skillText("index")
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.ReadFile("../../SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(root) != index {
		t.Fatal("cmd/ptc/skill/SKILL.md differs from SKILL.md; copy the index both ways")
	}
	if !strings.Contains(index, "**"+version.Version+"**") {
		t.Fatalf("skill index does not name version %s", version.Version)
	}

	seen := map[string]bool{}
	for _, tpc := range skillTopics {
		if tpc.Name == "" || tpc.File == "" || tpc.Summary == "" {
			t.Fatalf("incomplete topic %+v", tpc)
		}
		if seen[tpc.Name] {
			t.Fatalf("duplicate topic %s", tpc.Name)
		}
		seen[tpc.Name] = true
		text, err := skillText(tpc.Name)
		if err != nil {
			t.Fatalf("%s: %v", tpc.Name, err)
		}
		if strings.TrimSpace(text) == "" {
			t.Fatalf("topic %s is empty", tpc.Name)
		}
		embedded, err := fs.ReadFile(skillFS, "skill/"+tpc.File)
		if err != nil {
			t.Fatalf("embed %s: %v", tpc.File, err)
		}
		if string(embedded) != text {
			t.Fatalf("skillText(%s) drifted from embed", tpc.Name)
		}
	}

	entries, err := fs.ReadDir(skillFS, "skill")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			t.Fatalf("unexpected embed entry %s", e.Name())
		}
		found := false
		for _, tpc := range skillTopics {
			if tpc.File == e.Name() {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("embedded skill/%s is not listed in skillTopics", e.Name())
		}
	}
}

func TestSkillPrintUnknownTopic(t *testing.T) {
	if _, err := skillText("not-a-topic"); err == nil {
		t.Fatal("expected error for unknown topic")
	}
}

func TestSkillRequiredRulesPresent(t *testing.T) {
	index, err := skillText("index")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"bound-to-version",
		"never be an exec target",
		"never ask the user for neighbor IPs",
		"read-only",
		"Do not invent commands",
		"ptc skill print",
		"ptc skill list",
		"ptc/seed@v0",
		"DirDigest",
	} {
		if !strings.Contains(index, want) {
			t.Errorf("index missing %q", want)
		}
	}

	checks := map[string][]string{
		"submit": {
			"uses: ptc/seed@v0",
			"kind: Seed",
			"Never target a gateway",
			"Follow disconnect",
			"nvidia/cuda:12.9.2-devel-ubuntu22.04",
			"docker/run-host-node.ps1",
		},
		"resources": {
			"DirDigest",
			"is **not** a UnixFS",
			"256-KiB",
			"10,000",
			"has no GC",
			"interchangeable",
		},
		"install": {
			"ptc install --ssh",
			"StrictHostKeyChecking",
			"update --mesh",
			"install.json",
			"do not install OS services",
		},
		"discovery": {
			"scan_interval: 0s",
			"scan_min_interval",
			"retry_after_seconds",
			"Never ask the user for neighbor IPs",
			"host.docker.internal",
		},
		"storage": {
			"size_limit: 40GB",
			"delete_size: 20GB",
			"ptc clean",
			"local only",
			".ptc-locks",
			"never writable hard links",
		},
		"errors": {
			"daemon lock not found",
			"Do not invent neighbor IPs",
			"ptc clean is local-only",
			"Do not patch a running node out of tree",
		},
	}
	for topic, phrases := range checks {
		text, err := skillText(topic)
		if err != nil {
			t.Fatalf("%s: %v", topic, err)
		}
		for _, want := range phrases {
			if !strings.Contains(text, want) {
				t.Errorf("%s missing %q", topic, want)
			}
		}
	}
}

func TestSkillListJSON(t *testing.T) {
	cmd := skillListCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Version string `json:"version"`
		Topics  []struct {
			Name    string `json:"name"`
			Summary string `json:"summary"`
		} `json:"topics"`
	}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Version != version.Version {
		t.Fatalf("version %s", payload.Version)
	}
	if len(payload.Topics) != len(skillTopics) {
		t.Fatalf("got %d topics", len(payload.Topics))
	}
	if payload.Topics[0].Name != "index" {
		t.Fatalf("first topic %s", payload.Topics[0].Name)
	}
}
