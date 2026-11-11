// Package skills embeds the canonical Agent Skills pack and installs it
// into the user-scope discovery directory (~/.agents/skills/<name>).
package skills

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"embed"

	"gopkg.in/yaml.v3"

	"github.com/tigeregit/peer-task-control/internal/version"
)

// Name is the skill directory and the required frontmatter name.
const Name = "peer-task-control"

//go:embed peer-task-control/SKILL.md peer-task-control/references/*.md
var bundled embed.FS

// File is one document in the bundled pack.
type File struct {
	Path    string `json:"path"`
	Summary string `json:"summary"`
}

// Files lists the bundled documents in display order.
var Files = []File{
	{Path: "SKILL.md", Summary: "Core loop, target rules, and reference map"},
	{Path: "references/submit.md", Summary: "Workflows, seed-only tasks, placement, queue/cancel"},
	{Path: "references/resources.md", Summary: "DirDigest vs board CID, Bitswap, pull/download"},
	{Path: "references/install.md", Summary: "SSH install, local/remote/mesh update"},
	{Path: "references/discovery.md", Summary: "Trusted CIDRs, scans, cooldown, relays"},
	{Path: "references/storage.md", Summary: "Seeds vs worktrees, quotas, ptc clean"},
	{Path: "references/errors.md", Summary: "Recovery for common failures"},
}

// FS returns the canonical pack, rooted at SKILL.md.
func FS() fs.FS {
	sub, err := fs.Sub(bundled, Name)
	if err != nil {
		panic(err)
	}
	return sub
}

// Resolve maps a user-supplied path or short topic name to a bundled file.
func Resolve(name string) (string, error) {
	name = strings.TrimSpace(strings.ReplaceAll(name, `\`, "/"))
	if name == "" || strings.EqualFold(name, "index") || strings.EqualFold(name, "skill") || name == "SKILL.md" {
		return "SKILL.md", nil
	}
	if path.IsAbs(name) || filepath.IsAbs(name) || strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return "", unknownPath(name)
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", unknownPath(name)
	}
	for _, f := range Files {
		base := strings.TrimSuffix(path.Base(f.Path), ".md")
		got := strings.TrimSuffix(path.Base(clean), ".md")
		if f.Path == clean || path.Base(f.Path) == clean || strings.EqualFold(base, got) {
			return f.Path, nil
		}
	}
	return "", unknownPath(name)
}

func unknownPath(name string) error {
	names := make([]string, 0, len(Files))
	for _, f := range Files {
		names = append(names, f.Path)
	}
	return fmt.Errorf("unknown skill path %q (try: %s)", name, strings.Join(names, ", "))
}

// Read returns a bundled file. SKILL.md is stamped with ver in metadata.version.
func Read(name, ver string) ([]byte, error) {
	p, err := Resolve(name)
	if err != nil {
		return nil, err
	}
	data, err := fs.ReadFile(FS(), p)
	if err != nil {
		return nil, fmt.Errorf("skill file %s: %w", p, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("skill file %s is empty", p)
	}
	if p == "SKILL.md" {
		return StampVersion(data, ver)
	}
	return data, nil
}

type skillFrontmatter struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	Metadata    map[string]string `yaml:"metadata"`
}

// StampVersion sets metadata.version in SKILL.md frontmatter.
func StampVersion(data []byte, ver string) ([]byte, error) {
	fm, body, err := splitFrontmatter(data)
	if err != nil {
		return nil, err
	}
	var meta skillFrontmatter
	if err := yaml.Unmarshal(fm, &meta); err != nil {
		return nil, fmt.Errorf("skill frontmatter: %w", err)
	}
	if meta.Name != Name {
		return nil, fmt.Errorf("frontmatter name %q must be %s", meta.Name, Name)
	}
	stamped, err := setMetadataVersion(fm, ver)
	if err != nil {
		return nil, err
	}
	var out []byte
	out = append(out, []byte("---\n")...)
	out = append(out, stamped...)
	if len(stamped) > 0 && stamped[len(stamped)-1] != '\n' {
		out = append(out, '\n')
	}
	out = append(out, []byte("---\n")...)
	out = append(out, body...)
	return out, nil
}

// ParseVersion returns metadata.version from a SKILL.md document.
func ParseVersion(data []byte) (string, error) {
	fm, _, err := splitFrontmatter(data)
	if err != nil {
		return "", err
	}
	var meta skillFrontmatter
	if err := yaml.Unmarshal(fm, &meta); err != nil {
		return "", fmt.Errorf("skill frontmatter: %w", err)
	}
	if meta.Metadata == nil {
		return "", fmt.Errorf("skill frontmatter has no metadata.version")
	}
	ver := strings.TrimSpace(meta.Metadata["version"])
	if ver == "" {
		return "", fmt.Errorf("skill frontmatter has no metadata.version")
	}
	return ver, nil
}

func splitFrontmatter(data []byte) (fm, body []byte, err error) {
	s := string(data)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return nil, nil, fmt.Errorf("missing YAML frontmatter")
	}
	rest := s[4:]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		if strings.HasPrefix(rest, "---\n") {
			return []byte{}, []byte(rest[4:]), nil
		}
		if strings.HasSuffix(rest, "\n---") {
			return []byte(rest[:len(rest)-4]), []byte{}, nil
		}
		return nil, nil, fmt.Errorf("unterminated YAML frontmatter")
	}
	return []byte(rest[:end+1]), []byte(rest[end+5:]), nil
}

func setMetadataVersion(fm []byte, ver string) ([]byte, error) {
	quoted := "\"" + strings.ReplaceAll(ver, "\"", "\\\"") + "\""
	lines := strings.Split(strings.TrimRight(string(fm), "\n"), "\n")
	inMetadata := false
	metadataIndent := -1
	found := false
	metadataLine := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent == 0 && strings.HasPrefix(trimmed, "metadata:") {
			inMetadata = true
			metadataIndent = indent
			metadataLine = i
			continue
		}
		if inMetadata && indent <= metadataIndent {
			inMetadata = false
		}
		if inMetadata && strings.HasPrefix(trimmed, "version:") {
			pad := line[:indent]
			lines[i] = pad + "version: " + quoted
			found = true
			break
		}
	}
	if !found {
		if metadataLine >= 0 {
			insert := metadataLine + 1
			lines = append(lines[:insert], append([]string{"  version: " + quoted}, lines[insert:]...)...)
		} else {
			lines = append(lines, "metadata:", "  version: "+quoted)
		}
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

// UserDir is ~/.agents/skills/peer-task-control (Windows: %USERPROFILE%\.agents\skills\peer-task-control).
func UserDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agents", "skills", Name), nil
}

// Status describes the user-scope install relative to want (usually the binary version).
type Status struct {
	Path    string `json:"path"`
	Present bool   `json:"present"`
	Version string `json:"version,omitempty"`
	Stale   bool   `json:"stale"`
}

// InstallStatus reports whether the user-scope skill is present and current.
func InstallStatus(want string) (Status, error) {
	dir, err := UserDir()
	if err != nil {
		return Status{}, err
	}
	st := Status{Path: dir}
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return st, err
	}
	st.Present = true
	ver, err := ParseVersion(data)
	if err != nil {
		st.Stale = true
		return st, nil
	}
	st.Version = ver
	st.Stale = ver != want
	return st, nil
}

// Sync writes the bundled pack into dir, replacing only that skill directory.
func Sync(dir, ver string) error {
	if dir == "" || filepath.Base(dir) != Name {
		return fmt.Errorf("skill directory must be named %s", Name)
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, Name+".tmp-")
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tmp)
		}
	}()
	if err := writePack(tmp, ver); err != nil {
		return err
	}
	old := ""
	if _, err := os.Lstat(dir); err == nil {
		old, err = os.MkdirTemp(parent, Name+".old-")
		if err != nil {
			return err
		}
		if err := os.Remove(old); err != nil {
			return err
		}
		if err := os.Rename(dir, old); err != nil {
			_ = os.RemoveAll(old)
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		if old != "" {
			_ = os.Rename(old, dir)
		}
		return err
	}
	cleanup = false
	if old != "" {
		_ = os.RemoveAll(old)
	}
	return nil
}

// SyncUser writes the bundled pack into the user-scope Agent Skills directory.
func SyncUser(ver string) (string, error) {
	dir, err := UserDir()
	if err != nil {
		return "", err
	}
	if err := Sync(dir, ver); err != nil {
		return "", err
	}
	return dir, nil
}

func writePack(dir, ver string) error {
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0o755); err != nil {
		return err
	}
	return fs.WalkDir(FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dir, filepath.FromSlash(p)), 0o755)
		}
		data, err := fs.ReadFile(FS(), p)
		if err != nil {
			return err
		}
		if p == "SKILL.md" {
			data, err = StampVersion(data, ver)
			if err != nil {
				return err
			}
		}
		mode := os.FileMode(0o644)
		return os.WriteFile(filepath.Join(dir, filepath.FromSlash(p)), data, mode)
	})
}

// CheckName reports whether the bundled pack matches the version package's skill name.
func CheckName() error {
	if Name != version.SkillName {
		return fmt.Errorf("skills.Name %q != version.SkillName %q", Name, version.SkillName)
	}
	return nil
}
