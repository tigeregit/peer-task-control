package deploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWindowsReplacementScriptLeavesOldTargetOnInvalidBinary(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows replacement script is executed only on Windows")
	}
	shell, err := exec.LookPath("pwsh")
	if err != nil {
		shell, err = exec.LookPath("powershell.exe")
	}
	if err != nil {
		t.Skip("PowerShell is unavailable")
	}

	dir := newTestDirectory(t)
	dir = filepath.Join(dir, "safe ' $() & path")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	target, err := filepath.Abs(filepath.Join(dir, "ptc.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("new verified bytes"), 8192)
	payloadPath := filepath.Join(filepath.Dir(dir), "payload.exe")
	if err := os.WriteFile(payloadPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	script := buildWindowsReplacementScript(target, hex.EncodeToString(sum[:]), "1.2.3", payloadPath)
	stageProbe := "$reportedVersion = (& $stage version 2>$null | Out-String).Trim()"
	postProbe := "$reportedVersion = (& $target version 2>$null | Out-String).Trim()"
	script = strings.Replace(script, stageProbe, "$reportedVersion = $expectedVersion\n$global:LASTEXITCODE = 0", 1)
	script = strings.Replace(script, postProbe, "[Console]::Out.WriteLine('POST_INSTALL_VERSION_PROBE')\n$reportedVersion = 'wrong'\n$global:LASTEXITCODE = 0", 1)
	if !strings.Contains(script, "POST_INSTALL_VERSION_PROBE") {
		t.Fatal("could not instrument post-install version check")
	}
	command := powershellRemoteCommand(script)
	prefix := "powershell.exe -NoLogo -NoProfile -NonInteractive -OutputFormat Text -EncodedCommand \""
	encoded := strings.TrimSuffix(strings.TrimPrefix(command, prefix), `"`)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encoded)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err == nil {
		t.Fatal("simulated post-install version mismatch unexpectedly succeeded")
	}
	if !strings.Contains(output.String(), "POST_INSTALL_VERSION_PROBE") {
		t.Fatalf("script did not reach post-install validation: %s", output.String())
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old binary" {
		t.Fatalf("failed post-install validation did not roll back: %q; output: %s", got, output.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(target) {
		t.Fatalf("failed stage left temporary files: %v", entries)
	}
}
