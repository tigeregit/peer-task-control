package dispatch

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigeregit/peer-task-control/internal/cas"
	"github.com/tigeregit/peer-task-control/internal/resource"
	"github.com/tigeregit/peer-task-control/internal/workflow"
)

func loadSample(t *testing.T) workflow.Loaded {
	t.Helper()
	wf := filepath.Join("..", "..", "testdata", "submit", "workflow.yaml")
	loaded, err := workflow.LoadJob(wf, "")
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func TestBundleRoundTrip(t *testing.T) {
	loaded := loadSample(t)
	b, err := Build(loaded, BuildOptions{InlineMax: DefaultInlineMax})
	if err != nil {
		t.Fatal(err)
	}
	if b.Manifest.Job != "test" || len(b.Manifest.Seeds) != 2 {
		t.Fatalf("manifest: %+v", b.Manifest)
	}
	for _, s := range b.Manifest.Seeds {
		if s.Mode != SeedInline || s.Digest == "" {
			t.Fatalf("seed %+v should be inlined with a digest", s)
		}
	}
	dir := t.TempDir()
	u, err := Unpack(bytes.NewReader(b.Data), dir)
	if err != nil {
		t.Fatal(err)
	}
	if u.Loaded.JobName != "test" {
		t.Fatalf("job %s", u.Loaded.JobName)
	}
	if len(u.Inline) != 2 {
		t.Fatalf("inline=%d", len(u.Inline))
	}
	for name, path := range u.Inline {
		if err := VerifyPath(path, "", u.Digests[name]); err != nil {
			t.Fatalf("verify %s: %v", name, err)
		}
	}
}

func TestBundleDigestNotInlined(t *testing.T) {
	dir := t.TempDir()
	tree := filepath.Join(dir, "scene")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, _, err := cas.DirDigest(tree)
	if err != nil {
		t.Fatal(err)
	}
	// A job whose seed is only a digest (no local path).
	wfDir := t.TempDir()
	mustWrite := func(name, body string) {
		if err := os.WriteFile(filepath.Join(wfDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("workflow.yaml", `
name: digest-only
jobs:
  test:
    steps:
      - name: Prepare
        uses: ptc/prepare@v0
        with: {file: prepare.yaml}
      - name: Run
        run: "true"
      - name: Package result
        uses: ptc/package-result@v0
        with: {file: package-result.yaml}
`)
	mustWrite("prepare.yaml", `
apiVersion: ptc/v0
kind: Prepare
metadata: {name: digest-only}
target: {require_nvidia: false, deny_non_exec: false}
workspace:
  entrypoint: bin/app.sh
layout:
  directories:
    - {path: in, class: permanent, writable: false}
    - {path: in/scene, class: permanent, writable: false}
    - {path: out, class: temporary, writable: true}
    - {path: out/results, class: temporary, writable: true}
seeds:
  - name: scene
    dest: in/scene
    digest: `+sum+`
outputs:
  - name: results
    path: out/results
`)
	mustWrite("package-result.yaml", `
apiVersion: ptc/v0
kind: PackageResult
collect:
  from_prepare_outputs: true
publish: {class: temporary, pin: false}
`)
	loaded, err := workflow.LoadJob(filepath.Join(wfDir, "workflow.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(loaded, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Manifest.Seeds) != 1 || b.Manifest.Seeds[0].Mode != SeedDigest || b.Manifest.Seeds[0].Digest != resource.Normalize(sum) {
		t.Fatalf("expected digest seed, got %+v", b.Manifest.Seeds)
	}
	if bytes.Contains(b.Data, []byte("hello")) {
		t.Fatal("digest seed content was shipped inline")
	}
	u, err := Unpack(bytes.NewReader(b.Data), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if u.Digests["scene"] != sum {
		t.Fatalf("digest %s", u.Digests["scene"])
	}
	if _, ok := u.Inline["scene"]; ok {
		t.Fatal("digest seed should not be inline")
	}
}

func TestBundleLargeLocalIsPublished(t *testing.T) {
	loaded := loadSample(t)
	var published string
	b, err := Build(loaded, BuildOptions{
		InlineMax: -1, // never inline
		Publish: func(name, path string) (resource.Seed, error) {
			sum, _, err := cas.DirDigest(path)
			if err != nil {
				// file?
				st, sterr := os.Stat(path)
				if sterr == nil && !st.IsDir() {
					// hash via VerifyPath helper
					f, _ := os.ReadFile(path)
					_ = f
				}
				if err != nil {
					// DirDigest on a file fails; use a dummy for the file seed
					published = name
					return resource.Seed{Digest: "sha256:" + strings.Repeat("ab", 32), Kind: "tree"}, nil
				}
			}
			published = name
			return resource.Seed{Digest: sum, Kind: "tree"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if published == "" {
		t.Fatal("expected Publish to be called")
	}
	for _, s := range b.Manifest.Seeds {
		if s.Mode != SeedDigest {
			t.Fatalf("expected digest mode, got %+v", s)
		}
	}
}

func TestVerifyPathMismatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := VerifyPath(dir, "tree", "sha256:"+strings.Repeat("00", 32))
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("got %v", err)
	}
}

func TestUnpackRejectsGarbage(t *testing.T) {
	_, err := Unpack(bytes.NewReader([]byte("not a tar")), t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSeedBundleRejectsMissingDuplicateOrSkippedPlans(t *testing.T) {
	loaded, err := workflow.LoadJob(filepath.Join("..", "..", "testdata", "seed", "workflow.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := Build(loaded, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing", "duplicate", "skip-required"} {
		t.Run(mode, func(t *testing.T) {
			tr := tar.NewReader(bytes.NewReader(bundle.Data))
			var data bytes.Buffer
			tw := tar.NewWriter(&data)
			for {
				h, err := tr.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(tr)
				if err != nil {
					t.Fatal(err)
				}
				if h.Name == manifestName {
					var man Manifest
					if err := json.Unmarshal(body, &man); err != nil {
						t.Fatal(err)
					}
					switch mode {
					case "missing":
						man.Seeds = man.Seeds[:1]
					case "duplicate":
						man.Seeds = append(man.Seeds, man.Seeds[0])
					case "skip-required":
						man.Seeds[0].Mode = SeedSkip
					}
					body, err = json.Marshal(man)
					if err != nil {
						t.Fatal(err)
					}
				}
				h.Size = int64(len(body))
				if err := tw.WriteHeader(h); err != nil {
					t.Fatal(err)
				}
				if _, err := tw.Write(body); err != nil {
					t.Fatal(err)
				}
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Unpack(bytes.NewReader(data.Bytes()), t.TempDir()); err == nil {
				t.Fatalf("accepted %s plan", mode)
			}
		})
	}
}
