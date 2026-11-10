package resource

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	ma "github.com/multiformats/go-multiaddr"
)

func TestNormalize(t *testing.T) {
	d := "SHA256:" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if Normalize(d) != "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("got %q", Normalize(d))
	}
	if Normalize("nope") != "" {
		t.Fatal("expected empty")
	}
}

func TestInventoryPackage(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "packages", "run-x")
	if err := os.MkdirAll(filepath.Join(pkg, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "out", "a.bin"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	man := packageManifest{
		Digest:   "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		SeedName: "demo-results",
		Files:    map[string]string{"out/a.bin": "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"},
	}
	b, _ := json.Marshal(man)
	if err := os.WriteFile(filepath.Join(pkg, "manifest.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	got := Inventory(dir)
	if len(got) != 1 || got[0].Name != "demo-results" || got[0].Size != 5 || got[0].Kind != "tree" {
		t.Fatalf("%+v", got)
	}
	if got[0].CreatedAt == nil || got[0].CreatedAt.IsZero() {
		t.Fatal("expected created_at from package mtime")
	}
	if FindPackageDir(dir, man.Digest) != pkg {
		t.Fatal("package dir")
	}
}

func TestCircuitHops(t *testing.T) {
	direct, err := ma.NewMultiaddr("/ip4/10.87.0.10/tcp/18765")
	if err != nil {
		t.Fatal(err)
	}
	if CircuitHops(direct) != 0 {
		t.Fatal("direct")
	}
	relay, err := ma.NewMultiaddr("/ip4/10.87.0.2/tcp/18765/p2p/12D3KooWBQtRnmpKoX5ybrd8EeFwW9yaekVJWfN62ZsrqZAGMggJ/p2p-circuit")
	if err != nil {
		t.Fatal(err)
	}
	if CircuitHops(relay) != 1 {
		t.Fatalf("hops=%d", CircuitHops(relay))
	}
}

func TestPublishTree(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "a.bin"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := PublishTree(dir, "scene-a", src, time.Date(2026, 11, 10, 15, 53, 30, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "scene-a" || got.Kind != "tree" || got.Unit != "directory" || got.Size != 5 || got.FileCount != 1 || got.Digest == "" {
		t.Fatalf("%+v", got)
	}
	for _, f := range got.Files {
		if f.Digest != "" {
			t.Fatalf("directory-unit seed should not store per-file digests: %+v", f)
		}
	}
	inv := Inventory(dir)
	if len(inv) != 1 || inv[0].Name != "scene-a" || inv[0].Digest != got.Digest {
		t.Fatalf("%+v", inv)
	}
	pkg := filepath.Join(dir, "packages", SeedDirName("scene-a"))
	if FindPackageDir(dir, got.Digest) != pkg {
		t.Fatal(FindPackageDir(dir, got.Digest))
	}
}
