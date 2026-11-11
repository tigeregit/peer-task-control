package deploy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSyncSkillRunsBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := t.TempDir()
	var exe string
	if runtime.GOOS == "windows" {
		exe = filepath.Join(dir, "ptc.bat")
		script := "@echo off\r\n" +
			"if not \"%~1\"==\"skill\" exit /b 1\r\n" +
			"if not \"%~2\"==\"sync\" exit /b 1\r\n" +
			"set \"DEST=%USERPROFILE%\\.agents\\skills\\peer-task-control\"\r\n" +
			"mkdir \"%DEST%\" >nul 2>&1\r\n" +
			"echo 0.3.1> \"%DEST%\\SKILL.md\"\r\n"
		if err := os.WriteFile(exe, []byte(script), 0o644); err != nil {
			t.Fatal(err)
		}
	} else {
		exe = filepath.Join(dir, "ptc")
		script := "#!/bin/sh\n" +
			"if [ \"$1\" != \"skill\" ] || [ \"$2\" != \"sync\" ]; then exit 1; fi\n" +
			"dest=\"$HOME/.agents/skills/peer-task-control\"\n" +
			"mkdir -p \"$dest\"\n" +
			"echo 0.3.1 > \"$dest/SKILL.md\"\n"
		if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := SyncSkill(exe); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(home, ".agents", "skills", "peer-task-control", "SKILL.md"))
	if err != nil || !strings.Contains(string(got), "0.3.1") {
		t.Fatalf("skill not written: %q %v", got, err)
	}
}
