package resource

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type packageManifest struct {
	Digest    string            `json:"digest"`
	SeedName  string            `json:"seed_name,omitempty"`
	Unit      string            `json:"unit,omitempty"`
	Files     map[string]string `json:"files,omitempty"` // legacy per-file digests
	Entries   []manifestEntry   `json:"entries,omitempty"`
	FileCount int               `json:"file_count,omitempty"`
	Size      int64             `json:"size,omitempty"`
	Format    string            `json:"format,omitempty"`
	CreatedAt *time.Time        `json:"created_at,omitempty"`
	RunOK     *bool             `json:"run_ok,omitempty"`
	Pin       bool              `json:"pin,omitempty"`
	CID       string            `json:"cid,omitempty"`
	Replica   bool              `json:"replica,omitempty"`
}

type manifestEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size,omitempty"`
	Dir  bool   `json:"dir,omitempty"`
}

func stripBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}

func countFiles(files []File) int {
	n := 0
	for _, f := range files {
		if !f.Dir {
			n++
		}
	}
	return n
}

// Inventory lists tree seeds under dataDir/packages and blob seeds in dataDir/cas.
func Inventory(dataDir string) []Seed {
	if dataDir == "" {
		return nil
	}
	return InventoryAt(filepath.Join(dataDir, "packages"), filepath.Join(dataDir, "cas"))
}

// InventoryAt lists tree seeds in packagesDir and blob seeds in casDir.
func InventoryAt(packagesDir, casDir string) []Seed {
	by := map[string]Seed{}
	pkgRoot := packagesDir
	ents, _ := os.ReadDir(pkgRoot)
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		s, ok := loadPackage(filepath.Join(pkgRoot, e.Name()))
		if !ok {
			continue
		}
		by[s.Digest] = s
	}
	casRoot := casDir
	_ = filepath.Walk(casRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		name := info.Name()
		if len(name) != 64 {
			return nil
		}
		digest := Normalize(name)
		if digest == "" {
			return nil
		}
		if _, exists := by[digest]; exists {
			return nil
		}
		t := info.ModTime().UTC()
		by[digest] = Seed{Digest: digest, CID: readCID(p), Kind: "blob", Size: info.Size(), Name: "sha256:" + name[:12], CreatedAt: &t}
		return nil
	})
	out := make([]Seed, 0, len(by))
	for _, s := range by {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Digest < out[j].Digest })
	return out
}

func loadPackage(dir string) (Seed, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return Seed{}, false
	}
	var man packageManifest
	if json.Unmarshal(stripBOM(b), &man) != nil || Normalize(man.Digest) == "" {
		return Seed{}, false
	}
	s := Seed{Digest: Normalize(man.Digest), CID: man.CID, Replica: man.Replica, Name: man.SeedName, Kind: "tree", Unit: man.Unit, Size: man.Size, FileCount: man.FileCount, Pin: man.Pin, RunOK: man.RunOK}
	if c := readCID(dir); c != "" {
		s.CID = c
	}
	if s.Name == "" {
		s.Name = filepath.Base(dir)
	}
	complete := man.Size > 0 && man.CreatedAt != nil && !man.CreatedAt.IsZero()
	if complete && len(man.Entries) > 0 {
		for _, e := range man.Entries {
			s.Files = append(s.Files, File{Path: e.Path, Size: e.Size, Dir: e.Dir})
		}
		if s.Unit == "" {
			s.Unit = "directory"
		}
		if s.FileCount == 0 {
			s.FileCount = countFiles(s.Files)
		}
		s.CreatedAt = pickCreated(man.CreatedAt, time.Time{})
		sort.Slice(s.Files, func(i, j int) bool { return s.Files[i].Path < s.Files[j].Path })
		return s, true
	}
	if complete && len(man.Files) > 0 {
		for rel, dig := range man.Files {
			s.Files = append(s.Files, File{Path: rel, Digest: dig})
		}
		if s.FileCount == 0 {
			s.FileCount = len(s.Files)
		}
		s.CreatedAt = pickCreated(man.CreatedAt, time.Time{})
		sort.Slice(s.Files, func(i, j int) bool { return s.Files[i].Path < s.Files[j].Path })
		return s, true
	}
	var earliest time.Time
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == "." || rel == "manifest.json" {
			return nil
		}
		if info.IsDir() {
			s.Files = append(s.Files, File{Path: rel, Dir: true})
			return nil
		}
		if earliest.IsZero() || info.ModTime().Before(earliest) {
			earliest = info.ModTime()
		}
		s.Size += info.Size()
		dig := ""
		if man.Files != nil {
			dig = man.Files[rel]
		}
		s.Files = append(s.Files, File{Path: rel, Digest: dig, Size: info.Size()})
		return nil
	})
	if man.Size > 0 {
		s.Size = man.Size
	}
	if s.FileCount == 0 {
		s.FileCount = countFiles(s.Files)
	}
	if s.Unit == "" && len(man.Files) == 0 {
		s.Unit = "directory"
	}
	s.CreatedAt = pickCreated(man.CreatedAt, earliest)
	sort.Slice(s.Files, func(i, j int) bool { return s.Files[i].Path < s.Files[j].Path })
	return s, true
}

func pickCreated(explicit *time.Time, fallback time.Time) *time.Time {
	if explicit != nil && !explicit.IsZero() {
		t := explicit.UTC()
		return &t
	}
	if fallback.IsZero() {
		return nil
	}
	t := fallback.UTC()
	return &t
}

func earlierTime(a, b *time.Time) *time.Time {
	if a == nil || a.IsZero() {
		return b
	}
	if b == nil || b.IsZero() {
		return a
	}
	if b.Before(*a) {
		return b
	}
	return a
}

// FindPackageDir returns the packages/<id> directory that holds digest.
func FindPackageDir(dataDir, digest string) string {
	if dataDir == "" {
		return ""
	}
	return FindPackageDirAt(filepath.Join(dataDir, "packages"), digest)
}

// FindPackageDirAt is FindPackageDir with an explicit packages directory.
func FindPackageDirAt(packagesDir, digest string) string {
	digest = Normalize(digest)
	if digest == "" {
		return ""
	}
	root := packagesDir
	ents, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
		if err != nil {
			continue
		}
		var man packageManifest
		if json.Unmarshal(stripBOM(b), &man) != nil {
			continue
		}
		if Normalize(man.Digest) == digest {
			return dir
		}
	}
	return ""
}

func Merge(dst []Info, extra ...Info) []Info {
	idx := map[string]int{}
	for i, s := range dst {
		idx[s.Digest] = i
	}
	for _, s := range extra {
		s.Digest = Normalize(s.Digest)
		if s.Digest == "" {
			continue
		}
		if j, ok := idx[s.Digest]; ok {
			dst[j] = mergeInfo(dst[j], s)
			continue
		}
		idx[s.Digest] = len(dst)
		dst = append(dst, s)
	}
	sort.Slice(dst, func(i, j int) bool { return dst[i].Digest < dst[j].Digest })
	return dst
}

func mergeInfo(a, b Info) Info {
	if a.Name == "" {
		a.Name = b.Name
	}
	if a.Kind == "" {
		a.Kind = b.Kind
	}
	if a.Size == 0 {
		a.Size = b.Size
	}
	if a.CID == "" {
		a.CID = b.CID
	}
	if !a.Replica {
		a.Replica = b.Replica
	}
	if len(a.Files) == 0 {
		a.Files = b.Files
	}
	a.CreatedAt = earlierTime(a.CreatedAt, b.CreatedAt)
	seen := map[string]bool{}
	for _, loc := range a.Locations {
		seen[loc.PeerID] = true
	}
	for _, loc := range b.Locations {
		if loc.PeerID == "" || seen[loc.PeerID] {
			continue
		}
		seen[loc.PeerID] = true
		a.Locations = append(a.Locations, loc)
	}
	return a
}

func Lookup(list []Info, digest string) (Info, bool) {
	digest = Normalize(digest)
	for _, s := range list {
		if s.Digest == digest {
			return s, true
		}
	}
	return Info{}, false
}

func Filename(s Seed) string {
	name := s.Name
	if name == "" {
		name = Hex(s.Digest)
	}
	name = strings.ReplaceAll(name, "/", "-")
	if s.Kind == "tree" && !strings.HasSuffix(name, ".tar") {
		name += ".tar"
	}
	return name
}
