package skills

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tigeregit/peer-task-control/internal/version"
)

func TestCanonicalPackContract(t *testing.T) {
	if err := CheckName(); err != nil {
		t.Fatal(err)
	}
	index, err := Read("SKILL.md", version.Version)
	if err != nil {
		t.Fatal(err)
	}
	ver, err := ParseVersion(index)
	if err != nil {
		t.Fatal(err)
	}
	if ver != version.Version {
		t.Fatalf("stamped version %s, want %s", ver, version.Version)
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller path")
	}
	canonical := filepath.Join(filepath.Dir(thisFile), Name, "SKILL.md")
	root, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatal(err)
	}
	srcVer, err := ParseVersion(root)
	if err != nil {
		t.Fatal(err)
	}
	if srcVer != version.Version {
		t.Fatalf("canonical metadata.version %s, want %s", srcVer, version.Version)
	}
	embedded, err := fs.ReadFile(FS(), "SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(embedded) != string(root) {
		t.Fatal("embedded SKILL.md drifted from skills/peer-task-control/SKILL.md")
	}

	seen := map[string]bool{}
	for _, f := range Files {
		if f.Path == "" || f.Summary == "" {
			t.Fatalf("incomplete file %+v", f)
		}
		if seen[f.Path] {
			t.Fatalf("duplicate %s", f.Path)
		}
		seen[f.Path] = true
		data, err := Read(f.Path, version.Version)
		if err != nil {
			t.Fatalf("%s: %v", f.Path, err)
		}
		if strings.TrimSpace(string(data)) == "" {
			t.Fatalf("%s is empty", f.Path)
		}
	}

	err = fs.WalkDir(FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".md") {
			t.Errorf("unexpected embed entry %s", p)
			return nil
		}
		if !seen[p] {
			t.Errorf("embedded %s is not listed in Files", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStampVersionOverridesSource(t *testing.T) {
	data, err := fs.ReadFile(FS(), "SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	out, err := StampVersion(data, "9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseVersion(out)
	if err != nil {
		t.Fatal(err)
	}
	if got != "9.9.9" {
		t.Fatalf("got %s", got)
	}
	if !strings.Contains(string(out), "never be an exec target") {
		t.Fatal("body was not preserved")
	}
}

func TestResolveAndUnknownPath(t *testing.T) {
	for _, in := range []string{"", "index", "SKILL.md", "skill"} {
		p, err := Resolve(in)
		if err != nil || p != "SKILL.md" {
			t.Fatalf("%q -> %q %v", in, p, err)
		}
	}
	for _, in := range []string{"submit", "submit.md", "references/submit.md"} {
		p, err := Resolve(in)
		if err != nil || p != "references/submit.md" {
			t.Fatalf("%q -> %q %v", in, p, err)
		}
	}
	if _, err := Resolve("../SKILL.md"); err == nil {
		t.Fatal("expected error for path traversal")
	}
	if _, err := Resolve("not-a-topic"); err == nil {
		t.Fatal("expected error for unknown path")
	}
}

func TestSyncReplacesOnlyThisSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	sibling := filepath.Join(home, ".agents", "skills", "other-skill")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(sibling, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir, err := UserDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "STALE.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := SyncUser("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("dir %s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "STALE.md")); !os.IsNotExist(err) {
		t.Fatal("stale file was not replaced")
	}
	index, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	ver, err := ParseVersion(index)
	if err != nil || ver != "1.2.3" {
		t.Fatalf("installed version %s %v", ver, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "references", "storage.md")); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "keep" {
		t.Fatalf("sibling skill was touched: %s %v", b, err)
	}

	st, err := InstallStatus("1.2.3")
	if err != nil || !st.Present || st.Stale || st.Version != "1.2.3" {
		t.Fatalf("current status %+v %v", st, err)
	}
	st, err = InstallStatus("9.0.0")
	if err != nil || !st.Present || !st.Stale {
		t.Fatalf("stale status %+v %v", st, err)
	}
}

func TestInstallStatusMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	// USERPROFILE and HOME may differ; set both to the same value.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	st, err := InstallStatus(version.Version)
	if err != nil || st.Present || st.Stale {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestRequiredRulesPresent(t *testing.T) {
	index, err := Read("SKILL.md", version.Version)
	if err != nil {
		t.Fatal(err)
	}
	text := string(index)
	for _, want := range []string{
		"metadata.version",
		"~/.agents/skills/peer-task-control/",
		"ptc skill sync",
		"ptc skill read",
		"ptc skill list",
		"never be an exec target",
		"never ask the user for neighbor IPs",
		"read-only",
		"Do not invent commands",
		"ptc/seed@v0",
		"DirDigest",
		"leftover repository-root or next-to-binary",
		"references/submit.md",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("index missing %q", want)
		}
	}

	checks := map[string][]string{
		"references/submit.md": {
			"uses: ptc/seed@v0",
			"kind: Seed",
			"Never target a gateway",
			"Follow disconnect",
			"nvidia/cuda:12.9.2-devel-ubuntu22.04",
			"docker/run-host-node.ps1",
		},
		"references/resources.md": {
			"DirDigest",
			"is **not** a UnixFS",
			"256-KiB",
			"10,000",
			"has no GC",
			"interchangeable",
		},
		"references/install.md": {
			"ptc install --ssh",
			"StrictHostKeyChecking",
			"update --mesh",
			"install.json",
			"do not install OS services",
			"ptc skill sync",
		},
		"references/discovery.md": {
			"scan_interval: 0s",
			"scan_min_interval",
			"retry_after_seconds",
			"Never ask the user for neighbor IPs",
			"host.docker.internal",
		},
		"references/storage.md": {
			"size_limit: 40GB",
			"delete_size: 20GB",
			"ptc clean",
			"local only",
			".ptc-locks",
			"never writable hard links",
		},
		"references/errors.md": {
			"daemon lock not found",
			"Do not invent neighbor IPs",
			"ptc clean is local-only",
			"Do not patch a running node out of tree",
			"ptc skill sync",
		},
	}
	for path, phrases := range checks {
		data, err := Read(path, version.Version)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		text := string(data)
		for _, want := range phrases {
			if !strings.Contains(text, want) {
				t.Errorf("%s missing %q", path, want)
			}
		}
	}
}
