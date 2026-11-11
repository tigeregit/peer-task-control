package deploy

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReplaceLocalFileReplacesBinaryAndPreservesPermissions(t *testing.T) {
	dir := newTestDirectory(t)
	target := filepath.Join(dir, "ptc")
	if err := os.WriteFile(target, []byte("old binary"), 0o750); err != nil {
		t.Fatal(err)
	}
	data := []byte("new verified binary")
	sum := sha256.Sum256(data)
	artifact := Artifact{Data: data, Digest: hex.EncodeToString(sum[:])}

	if err := replaceLocalFile(target, 0o750, artifact); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatalf("got %q, want %q", got, data)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o750 {
		t.Fatalf("mode = %o, want 750", got)
	}
}

func TestReplaceLocalFileRejectsDigestMismatchWithoutChangingTarget(t *testing.T) {
	target := filepath.Join(newTestDirectory(t), "ptc")
	if err := os.WriteFile(target, []byte("old binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := replaceLocalFile(target, 0o700, Artifact{
		Data:   []byte("new binary"),
		Digest: "0000000000000000000000000000000000000000000000000000000000000000",
	})
	if err == nil {
		t.Fatal("expected digest mismatch")
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "old binary" {
		t.Fatalf("existing target changed: %q", got)
	}
}

func newTestDirectory(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(".", ".deploy-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("remove test directory: %v", err)
		}
	})
	return dir
}

func TestBinaryNameMatchesPublishedLocalPlatforms(t *testing.T) {
	tests := map[string]string{
		"linux/amd64":   "ptc-linux-amd64",
		"linux/arm64":   "ptc-linux-arm64",
		"darwin/amd64":  "ptc-darwin-amd64",
		"darwin/arm64":  "ptc-darwin-arm64",
		"windows/amd64": "ptc-windows-amd64.exe",
	}
	for platform, expected := range tests {
		got, err := BinaryName(platform)
		if err != nil || got != expected {
			t.Errorf("BinaryName(%q) = %q, %v; want %q", platform, got, err, expected)
		}
	}
	if _, err := BinaryName("windows/arm64"); err == nil {
		t.Fatal("unsupported platform was accepted")
	}
}
