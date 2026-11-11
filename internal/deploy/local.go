package deploy

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tigeregit/peer-task-control/internal/version"
)

type LocalUpdateResult struct {
	Path       string
	Platform   string
	OldVersion string
	NewVersion string
}

// UpdateLocal replaces this ptc executable or an explicitly selected local
// ptc executable after probing its version and verifying a release checksum.
func UpdateLocal(ctx context.Context, requestedVersion, path string) (LocalUpdateResult, error) {
	platform := runtime.GOOS + "/" + runtime.GOARCH
	if _, err := BinaryName(platform); err != nil {
		return LocalUpdateResult{}, err
	}

	runningPath, runningInfo, err := currentExecutable()
	if err != nil {
		return LocalUpdateResult{}, fmt.Errorf("locate current ptc executable: %w", err)
	}
	target := runningPath
	selfUpdate := path == ""
	if !selfUpdate {
		if !filepath.IsAbs(path) {
			return LocalUpdateResult{}, errors.New("--path must be an absolute path")
		}
		target = filepath.Clean(path)
	}

	targetInfo, err := os.Lstat(target)
	if err != nil {
		return LocalUpdateResult{}, fmt.Errorf("inspect update target: %w", err)
	}
	if targetInfo.Mode()&os.ModeSymlink != 0 || !targetInfo.Mode().IsRegular() {
		return LocalUpdateResult{}, errors.New("update target must be a regular, non-symlink file")
	}
	if targetInfo.Mode().Perm()&0o111 == 0 && runtime.GOOS != "windows" {
		return LocalUpdateResult{}, errors.New("update target is not executable")
	}
	isRunningBinary := os.SameFile(targetInfo, runningInfo)
	if runtime.GOOS == "windows" && isRunningBinary {
		return LocalUpdateResult{}, errors.New("Windows locks the running executable; use --path to select a different, non-running ptc executable")
	}

	oldVersion := version.Version
	if !isRunningBinary {
		oldVersion, err = probeBinaryVersion(ctx, target)
		if err != nil {
			return LocalUpdateResult{}, fmt.Errorf("version probe failed for %s: %w", target, err)
		}
	}

	artifact, err := FetchRelease(ctx, requestedVersion, platform)
	if err != nil {
		return LocalUpdateResult{}, err
	}
	if err := replaceLocalFile(target, targetInfo.Mode().Perm(), artifact); err != nil {
		return LocalUpdateResult{}, fmt.Errorf("replace local ptc binary: %w", err)
	}
	return LocalUpdateResult{
		Path:       target,
		Platform:   platform,
		OldVersion: oldVersion,
		NewVersion: artifact.Version,
	}, nil
}

func currentExecutable() (string, os.FileInfo, error) {
	path, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
		path = resolved
	}
	info, err := os.Stat(path)
	return path, info, err
}

func probeBinaryVersion(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version")
	out := &boundedBuffer{limit: 256}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", err
	}
	result := strings.TrimSpace(out.String())
	if result == "" || strings.ContainsAny(result, "\r\n") {
		return "", errors.New("target did not return one version line")
	}
	if !releaseVersionPattern.MatchString(result) {
		return "", errors.New("target returned an invalid ptc version")
	}
	return result, nil
}

type boundedBuffer struct {
	buffer strings.Builder
	limit  int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > b.limit {
		return 0, errors.New("version output exceeded limit")
	}
	return b.buffer.Write(p)
}

func (b *boundedBuffer) String() string {
	return b.buffer.String()
}

func replaceLocalFile(target string, mode os.FileMode, artifact Artifact) error {
	expected, err := hex.DecodeString(artifact.Digest)
	if err != nil || len(expected) != sha256.Size {
		return errors.New("invalid release artifact checksum")
	}
	sum := sha256.Sum256(artifact.Data)
	if subtle.ConstantTimeCompare(sum[:], expected) != 1 {
		return errors.New("release artifact checksum does not match its data")
	}

	dir := filepath.Dir(target)
	stage, err := os.CreateTemp(dir, ".ptc-update-*")
	if err != nil {
		return fmt.Errorf("create staging file: %w", err)
	}
	stagePath := stage.Name()
	defer os.Remove(stagePath)
	if _, err := stage.Write(artifact.Data); err != nil {
		_ = stage.Close()
		return fmt.Errorf("write staging file: %w", err)
	}
	if err := stage.Sync(); err != nil {
		_ = stage.Close()
		return fmt.Errorf("sync staging file: %w", err)
	}
	if err := stage.Chmod(mode); err != nil {
		_ = stage.Close()
		return fmt.Errorf("set staging permissions: %w", err)
	}
	if err := stage.Close(); err != nil {
		return fmt.Errorf("close staging file: %w", err)
	}
	if err := verifyFileDigest(stagePath, artifact.Digest); err != nil {
		return fmt.Errorf("verify staging file: %w", err)
	}

	backupFile, err := os.CreateTemp(dir, ".ptc-backup-*")
	if err != nil {
		return fmt.Errorf("create rollback path: %w", err)
	}
	backupPath := backupFile.Name()
	if err := backupFile.Close(); err != nil {
		_ = os.Remove(backupPath)
		return fmt.Errorf("close rollback path: %w", err)
	}
	if err := os.Remove(backupPath); err != nil {
		return fmt.Errorf("prepare rollback path: %w", err)
	}
	if err := os.Rename(target, backupPath); err != nil {
		return fmt.Errorf("preserve existing binary: %w", err)
	}
	if err := os.Rename(stagePath, target); err != nil {
		return restoreLocalFile(target, backupPath, fmt.Errorf("install staged binary: %w", err))
	}
	if err := verifyFileDigest(target, artifact.Digest); err != nil {
		return restoreLocalFile(target, backupPath, fmt.Errorf("verify installed binary: %w", err))
	}
	if err := os.Remove(backupPath); err != nil {
		return restoreLocalFile(target, backupPath, fmt.Errorf("remove rollback copy: %w", err))
	}
	return nil
}

func restoreLocalFile(target, backup string, cause error) error {
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Join(cause, fmt.Errorf("remove failed replacement: %w", err))
	}
	if err := os.Rename(backup, target); err != nil {
		return errors.Join(cause, fmt.Errorf("restore previous binary from %s: %w", backup, err))
	}
	return cause
}

func verifyFileDigest(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	actual := hash.Sum(nil)
	want, err := hex.DecodeString(expected)
	if err != nil || len(want) != sha256.Size || subtle.ConstantTimeCompare(actual, want) != 1 {
		return errors.New("SHA256 digest mismatch")
	}
	return nil
}
