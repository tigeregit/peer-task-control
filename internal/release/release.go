// Package release downloads and verifies ptc release assets published by
// .github/workflows/release.yml: ptc-<os>-<arch>[.exe], SKILL-<ver>.md and
// SHA256SUMS.
package release

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tigeregit/peer-task-control/internal/version"
)

// Source locates release assets. Base overrides the download root, e.g. a
// LAN mirror or a test server: <Base>/v<ver>/<asset>. Dir reads assets
// from a local directory (offline); it must contain SHA256SUMS.
type Source struct {
	Repo string
	Base string
	Dir  string
	HTTP *http.Client
}

// Default uses PTC_RELEASE_BASE / PTC_RELEASE_DIR when set, else GitHub.
func Default() Source {
	return Source{
		Repo: version.Repo,
		Base: strings.TrimRight(os.Getenv("PTC_RELEASE_BASE"), "/"),
		Dir:  os.Getenv("PTC_RELEASE_DIR"),
	}
}

func (s Source) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 30 * time.Minute}
}

// AssetName is the binary asset for a platform.
func AssetName(goos, goarch string) string {
	n := "ptc-" + goos + "-" + goarch
	if goos == "windows" {
		n += ".exe"
	}
	return n
}

// SkillAsset is the version-bound skill text asset.
func SkillAsset(ver string) string { return "SKILL-" + strings.TrimPrefix(ver, "v") + ".md" }

func tag(ver string) string { return "v" + strings.TrimPrefix(ver, "v") }

func (s Source) url(ver, asset string) string {
	if s.Base != "" {
		return s.Base + "/" + tag(ver) + "/" + asset
	}
	repo := s.Repo
	if repo == "" {
		repo = version.Repo
	}
	return "https://github.com/" + repo + "/releases/download/" + tag(ver) + "/" + asset
}

// Describe is a human label for where assets come from.
func (s Source) Describe(ver string) string {
	if s.Dir != "" {
		return s.Dir
	}
	return s.url(ver, "")
}

func (s Source) fetch(ctx context.Context, ver, asset string) ([]byte, error) {
	if s.Dir != "" {
		return os.ReadFile(filepath.Join(s.Dir, asset))
	}
	return s.get(ctx, s.url(ver, asset))
}

func (s Source) get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// Sums parses SHA256SUMS ("<hex>  <name>").
func Sums(b []byte) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && len(f[0]) == 64 {
			out[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
		}
	}
	return out
}

// FetchSums downloads and parses SHA256SUMS for a version.
func (s Source) FetchSums(ctx context.Context, ver string) (map[string]string, error) {
	b, err := s.fetch(ctx, ver, "SHA256SUMS")
	if err != nil {
		return nil, fmt.Errorf("SHA256SUMS for %s: %w", tag(ver), err)
	}
	sums := Sums(b)
	if len(sums) == 0 {
		return nil, fmt.Errorf("SHA256SUMS for %s is empty", tag(ver))
	}
	return sums, nil
}

// Asset downloads one asset and verifies it against SHA256SUMS. It returns
// the bytes and the verified "sha256:<hex>" digest.
func (s Source) Asset(ctx context.Context, ver, asset string, sums map[string]string) ([]byte, string, error) {
	if sums == nil {
		var err error
		if sums, err = s.FetchSums(ctx, ver); err != nil {
			return nil, "", err
		}
	}
	want, ok := sums[asset]
	if !ok {
		return nil, "", fmt.Errorf("%s is not listed in SHA256SUMS of %s", asset, tag(ver))
	}
	b, err := s.fetch(ctx, ver, asset)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(b)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return nil, "", fmt.Errorf("%s from %s: sha256 %s does not match SHA256SUMS %s", asset, tag(ver), got, want)
	}
	return b, "sha256:" + got, nil
}

// Info is one GitHub release.
type Info struct {
	Tag        string `json:"tag_name"`
	Prerelease bool   `json:"prerelease"`
	Draft      bool   `json:"draft"`
	URL        string `json:"html_url"`
}

// Latest returns the newest published release (pre-releases included when
// pre is true). With Base or Dir set it reads <Base>/latest or Dir/LATEST.
func (s Source) Latest(ctx context.Context, pre bool) (Info, error) {
	if s.Dir != "" || s.Base != "" {
		var b []byte
		var err error
		if s.Dir != "" {
			b, err = os.ReadFile(filepath.Join(s.Dir, "LATEST"))
		} else {
			b, err = s.get(ctx, s.Base+"/LATEST")
		}
		if err != nil {
			return Info{}, fmt.Errorf("latest version: %w", err)
		}
		return Info{Tag: tag(strings.TrimSpace(string(b)))}, nil
	}
	repo := s.Repo
	if repo == "" {
		repo = version.Repo
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+repo+"/releases?per_page=30", nil)
	if err != nil {
		return Info{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := s.client().Do(req)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Info{}, fmt.Errorf("GitHub releases: %s", resp.Status)
	}
	var list []Info
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return Info{}, err
	}
	var best Info
	for _, r := range list {
		if r.Draft || (r.Prerelease && !pre) || !version.Parse(r.Tag).OK {
			continue
		}
		if best.Tag == "" || version.Compare(r.Tag, best.Tag) > 0 {
			best = r
		}
	}
	if best.Tag == "" {
		return Info{}, fmt.Errorf("no release found in %s", repo)
	}
	return best, nil
}
