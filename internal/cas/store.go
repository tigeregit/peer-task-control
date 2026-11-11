package cas

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Store is a local sha256 content store. The node handles mesh transfers.
type Store struct {
	Root string
}

func New(root string) *Store {
	return &Store{Root: root}
}

func (s *Store) Path(digest string) string {
	hexPart := strings.TrimPrefix(strings.ToLower(digest), "sha256:")
	if len(hexPart) < 4 {
		return filepath.Join(s.Root, "sha256", hexPart)
	}
	return filepath.Join(s.Root, "sha256", hexPart[:2], hexPart)
}

func (s *Store) Has(digest string) bool {
	_, err := os.Stat(s.Path(digest))
	return err == nil
}

func (s *Store) PutFile(src string) (digest string, err error) {
	f, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	sum := "sha256:" + hex.EncodeToString(h.Sum(nil))
	dst := s.Path(sum)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if _, err := os.Stat(dst); err == nil {
		return sum, nil
	}
	if err := linkOrCopy(src, dst); err != nil {
		return "", err
	}
	return sum, nil
}

func (s *Store) Materialize(digest, dest string) error {
	src := s.Path(digest)
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("cas missing %s (fetch it through the daemon or use seeds.local)", digest)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return linkOrCopy(src, dest)
}

// RootDigest hashes a sorted list of "relpath sha256:...\n" records for a directory tree.
func RootDigest(root string) (string, map[string]string, error) {
	files := map[string]string{}
	var paths []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		sum, err := fileDigest(p)
		if err != nil {
			return err
		}
		files[rel] = sum
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		fmt.Fprintf(h, "%s %s\n", p, files[p])
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), files, nil
}

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
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

func linkOrCopy(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		os.Remove(tmp)
		return closeErr
	}
	return os.Rename(tmp, dst)
}

// LinkOrCopy places src at dest, preferring a hard link.
func LinkOrCopy(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(dest); err == nil {
		_ = os.Remove(dest)
	}
	return linkOrCopy(src, dest)
}

func CopyTree(src, dest string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return LinkOrCopy(src, dest)
	}
	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if fi.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return LinkOrCopy(p, target)
	})
}

type Blob struct {
	Digest string
	Size   int64
	Path   string
}

func (s *Store) List() []Blob {
	var out []Blob
	if s == nil || s.Root == "" {
		return nil
	}
	_ = filepath.Walk(s.Root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		name := info.Name()
		if len(name) != 64 {
			return nil
		}
		out = append(out, Blob{Digest: "sha256:" + strings.ToLower(name), Size: info.Size(), Path: p})
		return nil
	})
	return out
}

func (s *Store) Open(digest string) (*os.File, error) {
	p := s.Path(digest)
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("cas missing %s", digest)
	}
	return f, nil
}

// Entry is a path in a directory-unit tree. Digest is not computed per file.
type Entry struct {
	Path string
	Size int64
	Dir  bool
}

// DirDigest treats a directory as one content-addressed unit.
// It hashes a canonical stream of the tree and does not produce per-file digests.
// Root-level manifest.json is excluded (it is written after the digest).
//
// For each entry in sorted slash-separated relative path order:
//
//	directories: "d\t" + path + "\n"
//	regular files: "f\t" + path + "\t" + decimal size + "\n" + file bytes
func DirDigest(root string) (string, []Entry, error) {
	var ents []Entry
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." || rel == "manifest.json" {
			return nil
		}
		if info.IsDir() {
			ents = append(ents, Entry{Path: rel, Dir: true})
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		ents = append(ents, Entry{Path: rel, Size: info.Size()})
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	sort.Slice(ents, func(i, j int) bool { return ents[i].Path < ents[j].Path })
	h := sha256.New()
	for _, e := range ents {
		if e.Dir {
			fmt.Fprintf(h, "d\t%s\n", e.Path)
			continue
		}
		fmt.Fprintf(h, "f\t%s\t%d\n", e.Path, e.Size)
		f, err := os.Open(filepath.Join(root, filepath.FromSlash(e.Path)))
		if err != nil {
			return "", nil, err
		}
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil {
			return "", nil, copyErr
		}
		if closeErr != nil {
			return "", nil, closeErr
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), ents, nil
}
