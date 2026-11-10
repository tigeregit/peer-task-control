package cas

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirDigestStableAndNoPerFileHashes(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "a.bin"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{"ignored":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	d1, ents, err := DirDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if d1 == "" {
		t.Fatal("empty digest")
	}
	var files, dirs int
	for _, e := range ents {
		if e.Path == "manifest.json" {
			t.Fatal("manifest.json should be skipped")
		}
		if e.Dir {
			dirs++
		} else {
			files++
		}
	}
	if files != 1 || dirs != 2 {
		t.Fatalf("files=%d dirs=%d ents=%+v", files, dirs, ents)
	}

	root2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root2, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root2, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root2, "sub", "a.bin"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	d2, _, err := DirDigest(root2)
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Fatalf("digest changed: %s vs %s", d1, d2)
	}

	if err := os.WriteFile(filepath.Join(root2, "sub", "a.bin"), []byte("hellp"), 0o644); err != nil {
		t.Fatal(err)
	}
	d3, _, err := DirDigest(root2)
	if err != nil {
		t.Fatal(err)
	}
	if d3 == d1 {
		t.Fatal("content change did not change digest")
	}
}
