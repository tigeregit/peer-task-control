package resource

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/peer-task-control/ptc/internal/cas"
)

// SeedDirName is the packages/ subdirectory used for a published prepare seed.
func SeedDirName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "seed"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), ".-")
	if out == "" {
		out = "seed"
	}
	return "seed-" + out
}

// DirectoryUnitMinFiles is the file-count threshold at which a tree is
// automatically treated as a directory unit: one digest for the whole
// directory, no per-file hashes, and workspace previews show only the
// file count instead of listing each file.
const DirectoryUnitMinFiles = 100

// PublishTree records src as a named tree seed under dataDir/packages/seed-<name>.
// Files are hard-linked when the filesystem allows, so a second copy is not required.
func PublishTree(dataDir, name, src string, created time.Time) (Seed, error) {
	return PublishTreeTo(filepath.Join(dataDir, "packages"), name, src, created)
}

// PublishTreeTo is PublishTree with an explicit packages directory
// (the directory that contains seed-* folders).
func PublishTreeTo(packagesDir, name, src string, created time.Time) (Seed, error) {
	if packagesDir == "" {
		return Seed{}, fmt.Errorf("packages dir is empty")
	}
	src = filepath.Clean(src)
	info, err := os.Stat(src)
	if err != nil {
		return Seed{}, err
	}
	pkg := filepath.Join(packagesDir, SeedDirName(name))
	if err := os.RemoveAll(pkg); err != nil {
		return Seed{}, err
	}
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		return Seed{}, err
	}
	if info.IsDir() {
		if err := cas.CopyTree(src, pkg); err != nil {
			return Seed{}, fmt.Errorf("link seed %s into %s: %w", name, pkg, err)
		}
	} else if err := cas.LinkOrCopy(src, filepath.Join(pkg, info.Name())); err != nil {
		return Seed{}, err
	}
	return CommitTree(pkg, name, created, CommitOpts{Format: "unixfs"})
}

// CommitOpts optional fields written into a directory-unit tree manifest.
type CommitOpts struct {
	Format string
	RunOK  *bool
}

// CommitTree records pkg as a directory-unit tree seed: one digest for the
// whole directory, a path/size listing, and no per-file hashes.
func CommitTree(pkg, name string, created time.Time, opts CommitOpts) (Seed, error) {
	digest, ents, err := cas.DirDigest(pkg)
	if err != nil {
		return Seed{}, err
	}
	var size int64
	fileCount := 0
	entries := make([]manifestEntry, 0, len(ents))
	for _, e := range ents {
		entries = append(entries, manifestEntry{Path: e.Path, Size: e.Size, Dir: e.Dir})
		if !e.Dir {
			size += e.Size
			fileCount++
		}
	}
	if created.IsZero() {
		created = time.Now()
	}
	created = created.UTC()
	format := opts.Format
	if format == "" {
		format = "unixfs"
	}
	man := packageManifest{
		Digest:    digest,
		SeedName:  name,
		Unit:      "directory",
		Entries:   entries,
		FileCount: fileCount,
		Size:      size,
		Format:    format,
		CreatedAt: &created,
	}
	if opts.RunOK != nil {
		man.RunOK = opts.RunOK
	}
	mb, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return Seed{}, err
	}
	if err := os.WriteFile(filepath.Join(pkg, "manifest.json"), mb, 0o644); err != nil {
		return Seed{}, err
	}
	s, ok := loadPackage(pkg)
	if !ok {
		return Seed{}, fmt.Errorf("published %s but could not reload inventory entry", name)
	}
	if s.Name == "" {
		s.Name = name
	}
	return s, nil
}
