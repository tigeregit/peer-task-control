package deploy

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	repository   = "tigeregit/peer-task-control"
	releaseAPI   = "https://api.github.com/repos/" + repository
	maxBinaryLen = 128 << 20
)

var releaseVersionPattern = regexp.MustCompile(`^v?[0-9][A-Za-z0-9.+-]*$`)
var releaseAssetPath = regexp.MustCompile(`^/repos/` + regexp.QuoteMeta(repository) + `/releases/assets/[1-9][0-9]*$`)

type Artifact struct {
	Version  string
	Platform string
	Name     string
	Digest   string
	Data     []byte
}

type release struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"assets"`
}

// FetchRelease downloads a supported release binary and verifies its SHA256
// against the SHA256SUMS asset published with the release.
func FetchRelease(ctx context.Context, requestedVersion, platform string) (Artifact, error) {
	return fetchRelease(ctx, http.DefaultClient, releaseAPI, requestedVersion, platform)
}

// ResolveVersion returns the tag (without a leading v) for requestedVersion,
// which may be "latest".
func ResolveVersion(ctx context.Context, requestedVersion string) (string, error) {
	endpoint := strings.TrimRight(releaseAPI, "/") + "/releases/latest"
	if requestedVersion != "" && requestedVersion != "latest" {
		tag := requestedVersion
		if !strings.HasPrefix(tag, "v") {
			tag = "v" + tag
		}
		endpoint = strings.TrimRight(releaseAPI, "/") + "/releases/tags/" + url.PathEscape(tag)
	}
	var rel release
	if err := getJSON(ctx, http.DefaultClient, endpoint, &rel); err != nil {
		return "", fmt.Errorf("resolve release: %w", err)
	}
	if rel.TagName == "" {
		return "", errors.New("release response has no tag name")
	}
	return strings.TrimPrefix(rel.TagName, "v"), nil
}

// FetchReleaseFromDir loads a verified release asset from a directory that
// contains the platform binary and SHA256SUMS (offline / mesh staging).
func FetchReleaseFromDir(dir, requestedVersion, platform string) (Artifact, error) {
	name, err := BinaryName(platform)
	if err != nil {
		return Artifact{}, err
	}
	sums, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return Artifact{}, fmt.Errorf("read SHA256SUMS: %w", err)
	}
	want, err := checksumFor(string(sums), name)
	if err != nil {
		return Artifact{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return Artifact{}, fmt.Errorf("read %s: %w", name, err)
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(got), []byte(strings.ToLower(want))) != 1 {
		return Artifact{}, fmt.Errorf("SHA256 verification failed for %s", name)
	}
	ver := strings.TrimPrefix(requestedVersion, "v")
	return Artifact{Version: ver, Platform: platform, Name: name, Digest: "sha256:" + got, Data: data}, nil
}

func fetchRelease(ctx context.Context, client *http.Client, apiBase, requestedVersion, platform string) (Artifact, error) {
	name, err := BinaryName(platform)
	if err != nil {
		return Artifact{}, err
	}
	if requestedVersion != "" && requestedVersion != "latest" && !releaseVersionPattern.MatchString(requestedVersion) {
		return Artifact{}, fmt.Errorf("invalid release version %q (use a version such as 0.2.0-p2)", requestedVersion)
	}

	endpoint := strings.TrimRight(apiBase, "/") + "/releases/latest"
	if requestedVersion != "" && requestedVersion != "latest" {
		tag := requestedVersion
		if !strings.HasPrefix(tag, "v") {
			tag = "v" + tag
		}
		endpoint = strings.TrimRight(apiBase, "/") + "/releases/tags/" + url.PathEscape(tag)
	}

	var rel release
	if err := getJSON(ctx, client, endpoint, &rel); err != nil {
		return Artifact{}, fmt.Errorf("resolve release: %w", err)
	}
	if rel.TagName == "" {
		return Artifact{}, errors.New("release response has no tag name")
	}
	if requestedVersion != "" && requestedVersion != "latest" {
		tag := requestedVersion
		if !strings.HasPrefix(tag, "v") {
			tag = "v" + tag
		}
		if rel.TagName != tag {
			return Artifact{}, fmt.Errorf("requested release %q but GitHub returned %q", tag, rel.TagName)
		}
	}

	assetURL := ""
	for _, asset := range rel.Assets {
		if asset.Name == name {
			assetURL = asset.URL
			break
		}
	}
	if assetURL == "" {
		return Artifact{}, fmt.Errorf("release %s does not contain supported asset %s", rel.TagName, name)
	}
	if err := validateAssetURL(assetURL); err != nil {
		return Artifact{}, err
	}

	sumsURL := ""
	for _, asset := range rel.Assets {
		if asset.Name == "SHA256SUMS" {
			sumsURL = asset.URL
			break
		}
	}
	if sumsURL == "" {
		return Artifact{}, fmt.Errorf("release %s does not contain SHA256SUMS", rel.TagName)
	}
	if err := validateAssetURL(sumsURL); err != nil {
		return Artifact{}, err
	}
	sums, err := getBytes(ctx, client, sumsURL, 1<<20, "application/octet-stream")
	if err != nil {
		return Artifact{}, fmt.Errorf("download SHA256SUMS: %w", err)
	}
	expected, err := checksumFor(string(sums), name)
	if err != nil {
		return Artifact{}, fmt.Errorf("release %s: %w", rel.TagName, err)
	}

	data, err := getBytes(ctx, client, assetURL, maxBinaryLen, "application/octet-stream")
	if err != nil {
		return Artifact{}, fmt.Errorf("download %s: %w", name, err)
	}
	if len(data) == 0 {
		return Artifact{}, fmt.Errorf("release asset %s is empty", name)
	}
	actualBytes := sha256.Sum256(data)
	actual := hex.EncodeToString(actualBytes[:])
	if subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
		return Artifact{}, fmt.Errorf("SHA256 verification failed for %s", name)
	}
	return Artifact{
		Version:  strings.TrimPrefix(rel.TagName, "v"),
		Platform: platform,
		Name:     name,
		Digest:   actual,
		Data:     data,
	}, nil
}

func BinaryName(platform string) (string, error) {
	switch platform {
	case "linux/amd64":
		return "ptc-linux-amd64", nil
	case "linux/arm64":
		return "ptc-linux-arm64", nil
	case "darwin/amd64":
		return "ptc-darwin-amd64", nil
	case "darwin/arm64":
		return "ptc-darwin-arm64", nil
	case "windows/amd64":
		return "ptc-windows-amd64.exe", nil
	default:
		return "", fmt.Errorf("unsupported release platform %q", platform)
	}
}

func checksumFor(contents, filename string) (string, error) {
	for _, line := range strings.Split(contents, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		entryName := strings.TrimPrefix(fields[1], "*")
		if entryName != filename {
			continue
		}
		if len(fields[0]) != sha256.Size*2 {
			return "", fmt.Errorf("invalid checksum entry for %s", filename)
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return "", fmt.Errorf("invalid checksum entry for %s", filename)
		}
		return strings.ToLower(fields[0]), nil
	}
	return "", fmt.Errorf("SHA256SUMS has no entry for %s", filename)
}

func validateAssetURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() != "api.github.com" || u.Port() != "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || !releaseAssetPath.MatchString(u.Path) {
		return fmt.Errorf("refusing unexpected GitHub release asset URL")
	}
	return nil
}

func getJSON(ctx context.Context, client *http.Client, endpoint string, out any) error {
	data, err := getBytes(ctx, client, endpoint, 4<<20, "application/vnd.github+json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}
	return nil
}

func getBytes(ctx context.Context, client *http.Client, endpoint string, limit int64, accept string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "ptc-deploy")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request release data: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read release data: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("release data exceeds size limit")
	}
	return data, nil
}
