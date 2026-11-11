package deploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// PrevPath is where the previous binary is kept.
func PrevPath(exe string) string { return exe + ".prev" }

// Replace installs newBin at exe and keeps the old file at exe.prev. On
// Windows a running .exe cannot be overwritten but can be renamed, so the
// old file is moved aside first; the new one is written next to it and
// renamed into place, so exe is never half-written.
func Replace(exe string, newBin []byte) error {
	info, err := os.Lstat(exe)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("update target must be a regular, non-symlink file")
	}
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, filepath.Base(exe)+".new-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(newBin); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, info.Mode().Perm()); err != nil {
		os.Remove(tmpName)
		return err
	}
	prev := PrevPath(exe)
	if err := os.Remove(prev); err != nil && !os.IsNotExist(err) {
		// A previous binary may still be running (Windows); park it.
		_ = os.Rename(prev, fmt.Sprintf("%s.%d.old", prev, time.Now().UnixNano()))
	}
	hadOld := false
	if _, err := os.Stat(exe); err == nil {
		if err := os.Rename(exe, prev); err != nil {
			os.Remove(tmpName)
			return fmt.Errorf("move current binary aside: %w", err)
		}
		hadOld = true
	}
	if err := os.Rename(tmpName, exe); err != nil {
		if hadOld {
			_ = os.Rename(prev, exe)
		}
		os.Remove(tmpName)
		return fmt.Errorf("install new binary: %w", err)
	}
	return nil
}

// Rollback swaps exe and exe.prev.
func Rollback(exe string) error {
	prev := PrevPath(exe)
	if _, err := os.Stat(prev); err != nil {
		return fmt.Errorf("no previous binary at %s", prev)
	}
	tmp := fmt.Sprintf("%s.rollback-%d", exe, time.Now().UnixNano())
	if err := os.Rename(exe, tmp); err != nil {
		return err
	}
	if err := os.Rename(prev, exe); err != nil {
		_ = os.Rename(tmp, exe)
		return err
	}
	if err := os.Rename(tmp, prev); err != nil {
		return err
	}
	return nil
}

// BinaryVersion runs `<exe> version`.
func BinaryVersion(exe string) (string, error) {
	return probeBinaryVersion(context.Background(), exe)
}

// WriteSkill writes the version-bound skill of exe to SKILL.md next to it.
func WriteSkill(exe string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "skill", "print").Output()
	if err != nil {
		return fmt.Errorf("%s skill print: %w", exe, err)
	}
	return os.WriteFile(filepath.Join(filepath.Dir(exe), "SKILL.md"), out, 0o644)
}

// RestartCommand returns how to restart an installed daemon from outside
// the daemon process (the CLI). Empty means "start it yourself".
func RestartCommand(in Install) [][]string {
	name := in.ServiceName
	if name == "" {
		name = "ptc"
	}
	switch in.Service {
	case ServiceSystemdUser:
		return [][]string{{"systemctl", "--user", "restart", name}}
	case ServiceSystemdSystem:
		if os.Geteuid() == 0 {
			return [][]string{{"systemctl", "restart", name}}
		}
		return [][]string{{"sudo", "-n", "systemctl", "restart", name}}
	case ServiceSchtasks:
		return [][]string{{"schtasks", "/End", "/TN", name}, {"schtasks", "/Run", "/TN", name}}
	}
	return nil
}

// Exe resolves the running executable.
func Exe() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe, nil
}
