package run

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/peer-task-control/ptc/internal/cas"
	"github.com/peer-task-control/ptc/internal/resource"
)

// materializeDigest places a content-addressed seed at dest. It tries, in
// order: a tree package on this node, a blob in this node's CAS, then the
// mesh via opt.Fetch (closest replica). Every source is verified against the
// digest: DirDigest for directory units, sha256 for blobs.
func materializeDigest(name, digest, dest string, store *cas.Store, log *bytes.Buffer, opt Options) error {
	d := resource.Normalize(digest)
	if d == "" {
		return fmt.Errorf("invalid digest %q", digest)
	}
	if dir := resource.FindPackageDirAt(opt.packagesDir(), d); dir != "" {
		got, _, err := cas.DirDigest(dir)
		if err == nil && got == d {
			if err := copyTreeSkipManifest(dir, dest); err != nil {
				return err
			}
			fmt.Fprintf(log, "seed %s %s from this node's packages (verified) -> %s\n", name, d, dest)
			return nil
		}
		fmt.Fprintf(log, "seed %s local package %s failed verification (got %s, err %v); trying elsewhere\n", name, dir, got, err)
	}
	if store.Has(d) {
		if sum, err := fileSHA256(store.Path(d)); err == nil && sum == d {
			if err := store.Materialize(d, dest); err != nil {
				return err
			}
			fmt.Fprintf(log, "seed %s %s from this node's CAS (verified) -> %s\n", name, d, dest)
			return nil
		}
		fmt.Fprintf(log, "seed %s CAS blob %s failed verification; trying the mesh\n", name, d)
	}
	if opt.Fetch == nil {
		return fmt.Errorf("seed %s (%s) is not on this node and no mesh fetcher is available (is ptc daemon running?)", name, d)
	}
	ctx := opt.Context
	if ctx == nil {
		ctx = context.Background()
	}
	t0 := time.Now()
	blob, err := opt.Fetch(ctx, d)
	if err != nil {
		return fmt.Errorf("fetch %s from the mesh: %w", d, err)
	}
	defer blob.Body.Close()
	stage := filepath.Join(opt.FetchTmpDir(), fmt.Sprintf("fetch-%s-%d", resource.Hex(d)[:16], time.Now().UnixNano()))
	defer os.RemoveAll(stage)
	if blob.Kind == "tree" {
		files, n, err := cas.ExtractTar(blob.Body, stage, 0)
		if err != nil {
			return fmt.Errorf("unpack %s: %w", d, err)
		}
		got, _, err := cas.DirDigest(stage)
		if err != nil {
			return err
		}
		if got != d {
			return fmt.Errorf("digest mismatch for %s: received tree hashes to %s", d, got)
		}
		_ = os.Remove(filepath.Join(stage, "manifest.json"))
		if err := placeTree(stage, dest); err != nil {
			return err
		}
		fmt.Fprintf(log, "seed %s %s fetched from the mesh: %d files, %d bytes in %s (DirDigest verified) -> %s\n", name, d, files, n, time.Since(t0).Round(time.Millisecond), dest)
		return nil
	}
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(stage, "blob")
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, cerr := io.Copy(io.MultiWriter(f, h), blob.Body)
	if err := f.Close(); cerr == nil {
		cerr = err
	}
	if cerr != nil {
		return fmt.Errorf("fetch %s: %w", d, cerr)
	}
	if got := "sha256:" + hex.EncodeToString(h.Sum(nil)); got != d {
		return fmt.Errorf("digest mismatch for %s: received blob hashes to %s", d, got)
	}
	if _, err := store.PutFile(tmp); err != nil {
		return err
	}
	if err := store.Materialize(d, dest); err != nil {
		return err
	}
	fmt.Fprintf(log, "seed %s %s fetched from the mesh: %d bytes in %s (sha256 verified) -> %s\n", name, d, n, time.Since(t0).Round(time.Millisecond), dest)
	return nil
}

// placeTree moves a verified staging tree to dest, or copies into an
// existing non-empty dest.
func placeTree(stage, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if ents, err := os.ReadDir(dest); err == nil && len(ents) == 0 {
		_ = os.Remove(dest)
	}
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		if err := os.Rename(stage, dest); err == nil {
			return nil
		}
	}
	return cas.CopyTree(stage, dest)
}

func copyTreeSkipManifest(src, dest string) error {
	if err := cas.CopyTree(src, dest); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(src, "manifest.json")); err == nil {
		_ = os.Remove(filepath.Join(dest, "manifest.json"))
	}
	return nil
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + strings.ToLower(hex.EncodeToString(h.Sum(nil))), nil
}
