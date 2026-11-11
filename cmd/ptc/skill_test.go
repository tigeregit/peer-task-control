package main

import (
	"bytes"
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

func TestSkillSync(t *testing.T) {
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
	want, err := skills.Read("SKILL.md", version.Version)
	if err != nil {
		t.Fatal(err)
	}
	if string(index) != string(want) {
		t.Fatal("installed SKILL.md does not match the embedded pack")
	}
	storage, err := os.ReadFile(filepath.Join(dir, "references", "storage.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(storage), "size_limit: 40GB") {
		t.Fatal("did not write storage reference")
	}
}

func TestStaleSkillNotice(t *testing.T) {
	home := isolateHome(t)
	// Product behavior suppresses the notice when CI is set (GitHub Actions sets CI=true).
	t.Setenv("CI", "")
	dir := filepath.Join(home, ".agents", "skills", skills.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := "---\nname: peer-task-control\ndescription: stale\nmetadata:\n  version: \"0.0.1\"\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := skillCmd()
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

	upd := updateCmd()
	if err := upd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	errBuf.Reset()
	upd.SetErr(&errBuf)
	maybeWarnStaleSkill(upd)
	if errBuf.Len() != 0 {
		t.Fatalf("--json should suppress notice: %q", errBuf.String())
	}
}
