package resource

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/tigeregit/peer-task-control/internal/cas"
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
	Format  string
	RunOK   *bool
	Pin     bool
	CID     string
	Replica bool
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
	man.Pin = opts.Pin
	man.CID = opts.CID
	man.Replica = opts.Replica
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

// IsPinned reports whether the package at dir is marked pinned in its manifest.
func IsPinned(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return false
	}
	var man packageManifest
	if json.Unmarshal(stripBOM(b), &man) != nil {
		return false
	}
	return man.Pin
}

// SetPin updates the pin flag on an existing package manifest.
func SetPin(dir string, pin bool) error {
	return updateManifest(dir, func(m *packageManifest) { m.Pin = pin })
}

// BlobCIDPath is the sidecar that records the bitswap transport root CID of a CAS blob.
func BlobCIDPath(blobPath string) string { return blobPath + ".cid" }

func readBlobCID(blobPath string) string {
	b, err := os.ReadFile(BlobCIDPath(blobPath))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// SetBlobCID records the bitswap transport root CID next to a CAS blob.
func SetBlobCID(blobPath, c string) error {
	return os.WriteFile(BlobCIDPath(blobPath), []byte(c+"\n"), 0o644)
}

// SetTreeCID records the bitswap transport root CID in a package manifest.
// The manifest is not part of DirDigest, so the digest does not change.
func SetTreeCID(dir, c string) error {
	return updateManifest(dir, func(m *packageManifest) { m.CID = c })
}

// MarkReplica flags a package as a temporary copy fetched from the mesh.
func MarkReplica(dir string, replica bool) error {
	return updateManifest(dir, func(m *packageManifest) { m.Replica = replica })
}

func updateManifest(dir string, fn func(*packageManifest)) error {
	path := filepath.Join(dir, "manifest.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var man packageManifest
	if err := json.Unmarshal(stripBOM(b), &man); err != nil {
		return err
	}
	fn(&man)
	out, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// CommitVerifiedTree writes the manifest for a tree whose DirDigest was
// already computed (and checked) by the caller, without hashing it again.
func CommitVerifiedTree(pkg, name, digest string, ents []cas.Entry, created time.Time, opts CommitOpts) (Seed, error) {
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
	man := packageManifest{Digest: digest, SeedName: name, Unit: "directory", Entries: entries, FileCount: fileCount, Size: size, Format: format, CreatedAt: &created, RunOK: opts.RunOK, Pin: opts.Pin, CID: opts.CID, Replica: opts.Replica}
	mb, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return Seed{}, err
	}
	if err := os.WriteFile(filepath.Join(pkg, "manifest.json"), mb, 0o644); err != nil {
		return Seed{}, err
	}
	s, ok := loadPackage(pkg)
	if !ok {
		return Seed{}, fmt.Errorf("committed %s but could not reload inventory entry", name)
	}
	if s.Name == "" {
		s.Name = name
	}
	return s, nil
}

// FindByDigest returns the package directory and seed for digest under packagesDir, or a CAS path.
func FindByDigest(packagesDir, casDir, digest string) (kind, path string, seed Seed, ok bool) {
	digest = Normalize(digest)
	if dir := FindPackageDirAt(packagesDir, digest); dir != "" {
		s, loaded := loadPackage(dir)
		return "tree", dir, s, loaded
	}
	hex := Hex(digest)
	if hex == "" {
		return "", "", Seed{}, false
	}
	p := filepath.Join(casDir, "sha256", hex[:2], hex)
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return "blob", p, Seed{Digest: digest, CID: readBlobCID(p), Kind: "blob", Size: st.Size()}, true
	}
	// also accept the cas.Store layout even if casDir already is .../cas
	if _, err := os.Stat(p); err != nil {
		alt := filepath.Join(casDir, hex[:2], hex)
		if st, err := os.Stat(alt); err == nil && !st.IsDir() {
			return "blob", alt, Seed{Digest: digest, CID: readBlobCID(alt), Kind: "blob", Size: st.Size()}, true
		}
	}
	return "", "", Seed{}, false
}
