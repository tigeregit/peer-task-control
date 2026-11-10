package cas

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// WriteTar streams dir as an uncompressed tar with slash-separated relative
// paths. Only directories and regular files are written.
func WriteTar(dir string, w io.Writer) error {
	tw := tar.NewWriter(w)
	if err := AppendTar(tw, dir, ""); err != nil {
		_ = tw.Close()
		return err
	}
	return tw.Close()
}

// AppendTar adds src (a file or a directory tree) to tw under prefix.
// An empty prefix puts a directory's children at the archive root.
func AppendTar(tw *tar.Writer, src, prefix string) error {
	prefix = strings.Trim(filepath.ToSlash(prefix), "/")
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		name := rel
		if prefix != "" {
			if rel == "." {
				name = prefix
			} else {
				name = prefix + "/" + rel
			}
		} else if rel == "." {
			return nil
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = name
		hdr.Uname, hdr.Gname = "", ""
		if info.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		f.Close()
		return err
	})
}

// SafeRel validates a slash-separated archive path and returns it cleaned.
// Absolute paths, drive letters, and any ".." component are rejected.
func SafeRel(name string) (string, error) {
	n := strings.ReplaceAll(name, `\`, "/")
	if n == "" || strings.HasPrefix(n, "/") || (len(n) >= 2 && n[1] == ':') {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	for _, part := range strings.Split(n, "/") {
		if part == ".." {
			return "", fmt.Errorf("unsafe archive path %q", name)
		}
	}
	c := path.Clean(n)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return c, nil
}

// ExtractTar unpacks a tar stream into dir. Only directories and regular
// files are accepted; links, devices, and paths escaping dir are errors.
// maxBytes caps the total file payload (0 = unlimited).
func ExtractTar(r io.Reader, dir string, maxBytes int64) (files int, total int64, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, 0, err
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return files, total, nil
		}
		if err != nil {
			return files, total, err
		}
		rel, err := SafeRel(hdr.Name)
		if err != nil {
			return files, total, err
		}
		target := filepath.Join(dir, filepath.FromSlash(rel))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return files, total, err
			}
		case tar.TypeReg, tar.TypeRegA:
			total += hdr.Size
			if maxBytes > 0 && total > maxBytes {
				return files, total, fmt.Errorf("archive exceeds %d bytes", maxBytes)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return files, total, err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return files, total, err
			}
			_, cerr := io.CopyN(f, tr, hdr.Size)
			if err := f.Close(); cerr == nil {
				cerr = err
			}
			if cerr != nil {
				return files, total, cerr
			}
			files++
		default:
			return files, total, fmt.Errorf("archive entry %q has unsupported type %q", hdr.Name, string(hdr.Typeflag))
		}
	}
}
