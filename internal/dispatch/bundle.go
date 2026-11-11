// Package dispatch implements P2 mesh dispatch: the bundle a submitter sends,
// the placement rules a target enforces, and the target-side job runner.
// The libp2p stream lives in internal/node; this package has no network code.
package dispatch

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/tigeregit/peer-task-control/internal/cas"
	"github.com/tigeregit/peer-task-control/internal/resource"
	"github.com/tigeregit/peer-task-control/internal/workflow"
)

const (
	BundleVersion = 1
	// DefaultInlineMax is the largest local seed shipped inside the bundle.
	// Bigger local seeds are published on the submitter and pulled by digest.
	DefaultInlineMax int64 = 8 << 20
	// MaxBundleBytes caps what a target accepts in one bundle.
	MaxBundleBytes int64 = 256 << 20

	manifestName = "bundle.json"
	workflowName = "files/workflow.yaml"
	prepareName  = "files/prepare.yaml"
	packageName  = "files/package-result.yaml"
)

// Seed modes in a bundle.
const (
	SeedInline = "inline" // content is inside the bundle under Path
	SeedDigest = "digest" // target resolves by digest (own CAS or mesh)
	SeedSkip   = "skip"   // optional seed with no source on the submitter
)

// SeedPlan says how the target gets one Prepare seed.
type SeedPlan struct {
	Name   string `json:"name"`
	Mode   string `json:"mode"`
	Digest string `json:"digest,omitempty"`
	Kind   string `json:"kind,omitempty"` // tree or blob
	Size   int64  `json:"size,omitempty"`
	Files  int    `json:"files,omitempty"`
	Path   string `json:"path,omitempty"` // inline: archive path
}

// Manifest is bundle.json.
type Manifest struct {
	Version   int        `json:"version"`
	Job       string     `json:"job"`
	Name      string     `json:"name,omitempty"`
	Seeds     []SeedPlan `json:"seeds"`
	CreatedAt time.Time  `json:"created_at"`
}

// PublishFunc publishes a local path on the submitter's node and returns
// the resulting seed (digest). It is how large local seeds reach the mesh.
type PublishFunc func(name, path string) (resource.Seed, error)

type BuildOptions struct {
	// InlineMax is the largest local seed (total bytes) put in the bundle.
	// Negative means never inline; zero means DefaultInlineMax.
	InlineMax int64
	Publish   PublishFunc
	Now       func() time.Time
	// Log receives one line per seed decision.
	Log io.Writer
}

// Bundle is an encoded job ready to send.
type Bundle struct {
	Manifest Manifest
	Data     []byte
}

// Build packs the workflow, its Prepare and Package result YAMLs, and small
// local seeds. Seeds with a digest are referenced, not shipped. Large local
// seeds are published through opt.Publish and referenced by digest.
func Build(loaded workflow.Loaded, opt BuildOptions) (*Bundle, error) {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.Log == nil {
		opt.Log = io.Discard
	}
	inlineMax := opt.InlineMax
	if inlineMax == 0 {
		inlineMax = DefaultInlineMax
	}
	man := Manifest{Version: BundleVersion, Job: loaded.JobName, Name: loaded.Workflow.Name, CreatedAt: opt.Now().UTC()}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range []struct{ src, name string }{
		{loaded.Path, workflowName},
		{loaded.PreparePath, prepareName},
		{loaded.PackagePath, packageName},
	} {
		if f.src == "" {
			continue
		}
		b, err := os.ReadFile(f.src)
		if err != nil {
			return nil, err
		}
		if err := writeBytes(tw, f.name, b); err != nil {
			return nil, err
		}
	}
	for i, seed := range loaded.Prepare.Seeds {
		plan, err := planSeed(tw, i, seed, loaded.Dir, inlineMax, opt)
		if err != nil {
			return nil, fmt.Errorf("seed %s: %w", seed.Name, err)
		}
		switch plan.Mode {
		case SeedInline:
			fmt.Fprintf(opt.Log, "seed %s: inline %s (%d files, %d bytes)\n", plan.Name, plan.Digest, plan.Files, plan.Size)
		case SeedDigest:
			fmt.Fprintf(opt.Log, "seed %s: by digest %s (target resolves from its CAS or the closest holder)\n", plan.Name, plan.Digest)
		case SeedSkip:
			fmt.Fprintf(opt.Log, "seed %s: optional and not available here; skipped\n", plan.Name)
		}
		man.Seeds = append(man.Seeds, plan)
	}
	mb, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeBytes(tw, manifestName, mb); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if int64(buf.Len()) > MaxBundleBytes {
		return nil, fmt.Errorf("bundle is %d bytes, over the %d byte limit; lower --inline-max so large seeds go by digest", buf.Len(), MaxBundleBytes)
	}
	return &Bundle{Manifest: man, Data: buf.Bytes()}, nil
}

func planSeed(tw *tar.Writer, i int, seed workflow.Seed, base string, inlineMax int64, opt BuildOptions) (SeedPlan, error) {
	plan := SeedPlan{Name: seed.Name}
	if seed.Local != nil && seed.Local.Src != "" {
		src := seed.Local.Src
		if !filepath.IsAbs(src) {
			src = filepath.Join(base, src)
		}
		info, err := os.Stat(src)
		if err == nil {
			size, files, err := treeSize(src)
			if err != nil {
				return plan, err
			}
			plan.Size, plan.Files = size, files
			if inlineMax >= 0 && size <= inlineMax {
				plan.Mode = SeedInline
				plan.Path = "seeds/" + strconv.Itoa(i)
				if info.IsDir() {
					plan.Kind = "tree"
					plan.Digest, _, err = cas.DirDigest(src)
				} else {
					plan.Kind = "blob"
					plan.Digest, err = fileDigest(src)
					plan.Path += "/" + filepath.Base(src)
				}
				if err != nil {
					return plan, err
				}
				if info.IsDir() {
					err = cas.AppendTar(tw, src, plan.Path)
				} else {
					err = appendFile(tw, src, plan.Path)
				}
				return plan, err
			}
			if opt.Publish == nil {
				return plan, fmt.Errorf("local seed %s is %d bytes (over the inline limit) and no publisher is available", src, size)
			}
			pub, err := opt.Publish(seed.Name, src)
			if err != nil {
				return plan, fmt.Errorf("publish %s on this node: %w", src, err)
			}
			plan.Mode, plan.Digest, plan.Kind = SeedDigest, resource.Normalize(pub.Digest), "tree"
			if pub.Size > 0 {
				plan.Size = pub.Size
			}
			return plan, nil
		}
		if seed.Digest == "" {
			if !seed.IsRequired() {
				plan.Mode = SeedSkip
				return plan, nil
			}
			return plan, fmt.Errorf("local source %s: %w", src, err)
		}
	}
	if seed.Digest != "" {
		d := resource.Normalize(seed.Digest)
		if d == "" {
			return plan, fmt.Errorf("invalid digest %q", seed.Digest)
		}
		plan.Mode, plan.Digest = SeedDigest, d
		return plan, nil
	}
	if !seed.IsRequired() {
		plan.Mode = SeedSkip
		return plan, nil
	}
	if seed.CID != "" || seed.Source != nil {
		return plan, fmt.Errorf("CID/dataset source resolution is not supported; use seeds.local or a sha256 digest")
	}
	return plan, fmt.Errorf("no local source or digest")
}

// Unpacked is a bundle extracted on the target.
type Unpacked struct {
	Manifest Manifest
	Dir      string
	Loaded   workflow.Loaded
	// Overrides map seed names to inline content or digests for run.Submit.
	Inline  map[string]string // name -> extracted path
	Digests map[string]string // name -> digest (inline and by-digest seeds)
}

// Unpack extracts a bundle into dir, loads the job, and verifies every
// inline seed against its declared digest.
func Unpack(r io.Reader, dir string) (*Unpacked, error) {
	if _, _, err := cas.ExtractTar(io.LimitReader(r, MaxBundleBytes+1), dir, MaxBundleBytes); err != nil {
		return nil, fmt.Errorf("unpack bundle: %w", err)
	}
	mb, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return nil, fmt.Errorf("bundle has no %s", manifestName)
	}
	var man Manifest
	if err := json.Unmarshal(mb, &man); err != nil {
		return nil, fmt.Errorf("bundle manifest: %w", err)
	}
	if man.Version != BundleVersion {
		return nil, fmt.Errorf("bundle version %d is not supported (want %d); upgrade ptc on both nodes", man.Version, BundleVersion)
	}
	loaded, err := workflow.LoadJobFiles(filepath.Join(dir, filepath.FromSlash(workflowName)), filepath.Join(dir, filepath.FromSlash(prepareName)), filepath.Join(dir, filepath.FromSlash(packageName)), man.Job)
	if err != nil {
		return nil, err
	}
	u := &Unpacked{Manifest: man, Dir: dir, Loaded: loaded, Inline: map[string]string{}, Digests: map[string]string{}}
	declared := map[string]bool{}
	for _, s := range loaded.Prepare.Seeds {
		declared[s.Name] = true
	}
	planned := map[string]bool{}
	for _, p := range man.Seeds {
		if !declared[p.Name] {
			return nil, fmt.Errorf("bundle seed %q is not declared in prepare.yaml", p.Name)
		}
		if planned[p.Name] {
			return nil, fmt.Errorf("bundle has duplicate seed plan %q", p.Name)
		}
		planned[p.Name] = true
		switch p.Mode {
		case SeedInline:
			rel, err := cas.SafeRel(p.Path)
			if err != nil {
				return nil, err
			}
			path := filepath.Join(dir, filepath.FromSlash(rel))
			if err := VerifyPath(path, p.Kind, p.Digest); err != nil {
				return nil, fmt.Errorf("inline seed %s: %w", p.Name, err)
			}
			u.Inline[p.Name] = path
			u.Digests[p.Name] = p.Digest
		case SeedDigest:
			d := resource.Normalize(p.Digest)
			if d == "" {
				return nil, fmt.Errorf("seed %s has invalid digest %q", p.Name, p.Digest)
			}
			u.Digests[p.Name] = d
		case SeedSkip:
			for i, seed := range u.Loaded.Prepare.Seeds {
				if seed.Name == p.Name {
					if seed.IsRequired() {
						return nil, fmt.Errorf("required seed %s cannot be skipped", p.Name)
					}
					// Never resolve the submitter's local paths on a files-only target.
					u.Loaded.Prepare.Seeds[i].Local = nil
					u.Loaded.Prepare.Seeds[i].Digest = ""
					u.Loaded.Prepare.Seeds[i].CID = ""
					u.Loaded.Prepare.Seeds[i].Source = nil
				}
			}
		default:
			return nil, fmt.Errorf("seed %s has unknown mode %q", p.Name, p.Mode)
		}
	}
	for name := range declared {
		if !planned[name] {
			return nil, fmt.Errorf("bundle has no plan for seed %q", name)
		}
	}
	return u, nil
}

// VerifyPath checks a file (sha256) or a directory (DirDigest) against digest.
func VerifyPath(path, kind, digest string) error {
	want := resource.Normalize(digest)
	if want == "" {
		return fmt.Errorf("invalid digest %q", digest)
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	var got string
	if info.IsDir() {
		if kind == "blob" {
			return fmt.Errorf("expected a file, got a directory")
		}
		got, _, err = cas.DirDigest(path)
	} else {
		if kind == "tree" {
			return fmt.Errorf("expected a directory, got a file")
		}
		got, err = fileDigest(path)
	}
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("digest mismatch: declared %s, content hashes to %s", want, got)
	}
	return nil
}

func writeBytes(tw *tar.Writer, name string, b []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(b)), Typeflag: tar.TypeReg, ModTime: time.Now()}); err != nil {
		return err
	}
	_, err := tw.Write(b)
	return err
}

func appendFile(tw *tar.Writer, src, name string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: info.Size(), Typeflag: tar.TypeReg, ModTime: info.ModTime()}); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

func treeSize(root string) (int64, int, error) {
	var size int64
	files := 0
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			size += info.Size()
			files++
		}
		return nil
	})
	return size, files, err
}

func fileDigest(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
