package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigeregit/peer-task-control/internal/version"
	"github.com/tigeregit/peer-task-control/skills"
)

func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestSkillReadAndPrintAlias(t *testing.T) {
	isolateHome(t)
	cmd := skillReadCmd()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if strings.Contains(text, "# bound-to-version") {
		t.Fatal("stdout must not append bound-to-version")
	}
	ver, err := skills.ParseVersion([]byte(text))
	if err != nil || ver != version.Version {
		t.Fatalf("version %s %v", ver, err)
	}
	if !strings.Contains(errBuf.String(), "ptc skill sync") {
		t.Fatalf("expected guidance on stderr, got %q", errBuf.String())
	}

	out.Reset()
	errBuf.Reset()
	if err := cmd.RunE(cmd, []string{"references/storage.md"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "size_limit: 40GB") {
		t.Fatal("did not print storage reference")
	}

	out.Reset()
	printCmd := skillReadCmd()
	printCmd.SetOut(&out)
	printCmd.SetErr(io.Discard)
	if err := printCmd.RunE(printCmd, []string{"submit"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "uses: ptc/seed@v0") {
		t.Fatal("topic alias failed")
	}
	if _, err := skills.Read("not-a-topic", version.Version); err == nil {
		t.Fatal("expected error for unknown path")
	}
}

func TestSkillSyncAndList(t *testing.T) {
	home := isolateHome(t)
	sync := skillSyncCmd()
	var out bytes.Buffer
	sync.SetOut(&out)
	if err := sync.RunE(sync, nil); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".agents", "skills", skills.Name)
	if !strings.Contains(out.String(), dir) {
		t.Fatalf("sync output %q", out.String())
	}
	index, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	ver, err := skills.ParseVersion(index)
	if err != nil || ver != version.Version {
		t.Fatalf("installed version %s %v", ver, err)
	}

	list := skillListCmd()
	out.Reset()
	list.SetOut(&out)
	if err := list.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := list.RunE(list, nil); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Install struct {
			Path    string `json:"path"`
			Present bool   `json:"present"`
			Version string `json:"version"`
			Stale   bool   `json:"stale"`
		} `json:"install"`
		Files []struct {
			Path    string `json:"path"`
			Summary string `json:"summary"`
		} `json:"files"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Name != skills.Name || payload.Version != version.Version {
		t.Fatalf("payload %+v", payload)
	}
	if !payload.Install.Present || payload.Install.Stale || payload.Install.Version != version.Version {
		t.Fatalf("install %+v", payload.Install)
	}
	if len(payload.Files) != len(skills.Files) || payload.Files[0].Path != "SKILL.md" {
		t.Fatalf("files %+v", payload.Files)
	}
}

func TestStaleSkillNotice(t *testing.T) {
	home := isolateHome(t)
	dir := filepath.Join(home, ".agents", "skills", skills.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := "---\nname: peer-task-control\ndescription: stale\nmetadata:\n  version: \"0.0.1\"\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := skillReadCmd()
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	maybeWarnStaleSkill(cmd)
	if !strings.Contains(errBuf.String(), "warning:") || !strings.Contains(errBuf.String(), "0.0.1") {
		t.Fatalf("missing stale warning: %q", errBuf.String())
	}
	if strings.Contains(out.String(), "warning:") {
		t.Fatal("warning leaked to stdout")
	}

	errBuf.Reset()
	t.Setenv("CI", "true")
	maybeWarnStaleSkill(cmd)
	if errBuf.Len() != 0 {
		t.Fatalf("CI should suppress notice: %q", errBuf.String())
	}
	t.Setenv("CI", "")

	list := skillListCmd()
	if err := list.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	errBuf.Reset()
	list.SetErr(&errBuf)
	maybeWarnStaleSkill(list)
	if errBuf.Len() != 0 {
		t.Fatalf("--json should suppress notice: %q", errBuf.String())
	}
}
