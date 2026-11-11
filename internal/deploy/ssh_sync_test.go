package deploy

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestSSHSyncSkillQuotesUnixPath(t *testing.T) {
	var gotArgs []string
	ssh := SSHOptions{
		Host: "deploy@node-1",
		Port: 2222,
		Runner: func(_ context.Context, name string, args []string, stdin io.Reader, _, _ io.Writer) error {
			if name != "ssh" {
				t.Fatalf("unexpected executable %q", name)
			}
			gotArgs = append([]string(nil), args...)
			return nil
		},
	}
	path := "/opt/ptc'; touch /bad; echo '"
	if err := ssh.SyncSkill(context.Background(), path, "linux/amd64"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "StrictHostKeyChecking=yes") {
		t.Fatalf("SSH options missing: %q", joined)
	}
	if !strings.Contains(gotArgs[len(gotArgs)-1], "'\\''") {
		t.Fatalf("remote path was not shell-escaped: %q", gotArgs[len(gotArgs)-1])
	}
	if !strings.Contains(gotArgs[len(gotArgs)-1], "skill sync") {
		t.Fatalf("remote command is not skill sync: %q", gotArgs[len(gotArgs)-1])
	}
}

func TestSSHSyncSkillEncodesWindowsCommand(t *testing.T) {
	var remote string
	ssh := SSHOptions{
		Host: "windows-host",
		Port: 2022,
		Runner: func(_ context.Context, name string, args []string, _ io.Reader, _, _ io.Writer) error {
			if name != "ssh" {
				t.Fatalf("unexpected executable %q", name)
			}
			remote = args[len(args)-1]
			return nil
		},
	}
	path := `C:\Users\me\prod'$(Write-Output hacked)&\ptc.exe`
	if err := ssh.SyncSkill(context.Background(), path, "windows/amd64"); err != nil {
		t.Fatal(err)
	}
	prefix := "powershell.exe -NoLogo -NoProfile -NonInteractive -OutputFormat Text -EncodedCommand \""
	if !strings.HasPrefix(remote, prefix) || len(remote) >= 8191 {
		t.Fatalf("Windows command is not short encoded PowerShell: %q", remote)
	}
	if strings.Contains(remote, path) {
		t.Fatal("remote command contains unencoded user data")
	}
}
