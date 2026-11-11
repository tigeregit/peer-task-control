package worktreeguard

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardAcrossProcessesAndCrash(t *testing.T) {
	if root := os.Getenv("PTC_TEST_GUARD_ROOT"); root != "" {
		g, err := TryLock(root, filepath.Join(root, "first", "nested"))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("ready")
		_, _ = io.Copy(io.Discard, os.Stdin)
		if err := g.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}
	root := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestGuardAcrossProcessesAndCrash$")
	cmd.Env = append(os.Environ(), "PTC_TEST_GUARD_ROOT="+root)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("child did not acquire guard: %q, %v", line, err)
	}
	for _, path := range []string{root, filepath.Join(root, "first"), filepath.Join(root, "first", "other")} {
		if g, err := TryLock(root, path); !errors.Is(err, ErrBusy) {
			if g != nil {
				_ = g.Close()
			}
			t.Fatalf("overlapping worktree %s was not protected: %v", path, err)
		}
	}
	other, err := TryLock(root, filepath.Join(root, "second"))
	if err != nil {
		t.Fatalf("unrelated worktree was blocked: %v", err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	g, err := TryLock(root, filepath.Join(root, "first"))
	if err != nil {
		t.Fatalf("crashed process did not release its guard: %v", err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGuardRootAndSymlinkAlias(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "job")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	g, err := TryLock(root, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(root, child); !errors.Is(err, ErrBusy) {
		t.Fatalf("root worktree did not protect its child: %v", err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Logf("symlink creation unavailable: %v", err)
		return
	}
	g, err = TryLock(alias, filepath.Join(alias, "job", "nested"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := TryLock(root, child); !errors.Is(err, ErrBusy) {
		t.Fatalf("symlink alias bypassed worktree protection: %v", err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
}
