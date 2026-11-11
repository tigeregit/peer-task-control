package deploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"unicode/utf16"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestFetchReleaseVerifiesAssetChecksum(t *testing.T) {
	binary := []byte("ptc release")
	sum := sha256.Sum256(binary)
	digest := hex.EncodeToString(sum[:])
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body []byte
		switch req.URL.Path {
		case "/releases/tags/v1.2.3":
			if req.Header.Get("Accept") != "application/vnd.github+json" {
				t.Fatal("metadata must request JSON")
			}
			body, _ = json.Marshal(map[string]any{
				"tag_name": "v1.2.3",
				"assets": []map[string]string{
					{"name": "ptc-linux-amd64", "url": releaseAPI + "/releases/assets/1"},
					{"name": "SHA256SUMS", "url": releaseAPI + "/releases/assets/2"},
				},
			})
		case "/repos/" + repository + "/releases/assets/1":
			if req.Header.Get("Accept") != "application/octet-stream" {
				t.Fatal("asset API must request binary content, not JSON")
			}
			body = binary
		case "/repos/" + repository + "/releases/assets/2":
			if req.Header.Get("Accept") != "application/octet-stream" {
				t.Fatal("checksum API must request asset content, not JSON")
			}
			body = []byte(fmt.Sprintf("%s  ptc-linux-amd64\n", digest))
		default:
			return nil, fmt.Errorf("unexpected request %s", req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})}

	artifact, err := fetchRelease(context.Background(), client, "https://api.test", "1.2.3", "linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Version != "1.2.3" || artifact.Digest != digest || !bytes.Equal(artifact.Data, binary) {
		t.Fatalf("unexpected artifact: %#v", artifact)
	}
}

func TestFetchReleaseRejectsBadChecksum(t *testing.T) {
	binary := []byte("tampered")
	client := releaseTestClient(binary, strings.Repeat("0", 64))
	_, err := fetchRelease(context.Background(), client, "https://api.test", "1.2.3", "linux/amd64")
	if err == nil || !strings.Contains(err.Error(), "SHA256 verification failed") {
		t.Fatalf("expected checksum failure, got %v", err)
	}
}

func TestReleaseAssetURLRejectsOtherRepositories(t *testing.T) {
	for _, raw := range []string{
		"https://api.github.com/repos/other/repo/releases/assets/1",
		releaseAPI + "/releases/assets/1?download=1",
		"https://api.github.com.evil.example/repos/" + repository + "/releases/assets/1",
	} {
		if err := validateAssetURL(raw); err == nil {
			t.Errorf("accepted unexpected asset URL %q", raw)
		}
	}
}

func releaseTestClient(binary []byte, digest string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body []byte
		switch req.URL.Path {
		case "/releases/tags/v1.2.3":
			body = []byte(`{"tag_name":"v1.2.3","assets":[{"name":"ptc-linux-amd64","url":"https://api.github.com/repos/tigeregit/peer-task-control/releases/assets/1"},{"name":"SHA256SUMS","url":"https://api.github.com/repos/tigeregit/peer-task-control/releases/assets/2"}]}`)
		case "/repos/" + repository + "/releases/assets/1":
			body = binary
		case "/repos/" + repository + "/releases/assets/2":
			body = []byte(digest + "  ptc-linux-amd64\n")
		default:
			return nil, errors.New("unexpected request")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})}
}

func TestSSHReplaceQuotesPathAndStreamsVerifiedBytes(t *testing.T) {
	binary := []byte("verified binary")
	sum := sha256.Sum256(binary)
	digest := hex.EncodeToString(sum[:])
	var gotArgs []string
	var gotInput []byte
	ssh := SSHOptions{
		Host:           "deploy@node-1",
		Port:           2222,
		KnownHostsFile: `C:\Users\me\.ssh\known hosts`,
		Runner: func(_ context.Context, name string, args []string, stdin io.Reader, _, _ io.Writer) error {
			if name != "ssh" {
				t.Fatalf("unexpected executable %q", name)
			}
			gotArgs = append([]string(nil), args...)
			var err error
			gotInput, err = io.ReadAll(stdin)
			return err
		},
	}
	path := "/opt/ptc'; touch /bad; echo '"
	err := ssh.Replace(context.Background(), path, Artifact{
		Platform: "linux/amd64",
		Name:     "ptc-linux-amd64",
		Digest:   digest,
		Data:     binary,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotInput, binary) {
		t.Fatal("binary was not streamed to SSH")
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "-o BatchMode=yes") || !strings.Contains(joined, "-p 2222") {
		t.Fatalf("SSH options missing: %q", joined)
	}
	if !strings.Contains(joined, `UserKnownHostsFile="C:/Users/me/.ssh/known hosts"`) ||
		!strings.Contains(joined, "StrictHostKeyChecking=yes") {
		t.Fatalf("custom known-hosts file did not retain strict checking: %q", joined)
	}
	if !strings.Contains(gotArgs[len(gotArgs)-1], "'\\''") {
		t.Fatalf("remote path was not shell-escaped: %q", gotArgs[len(gotArgs)-1])
	}
}

func TestValidateSSHAndRemotePath(t *testing.T) {
	for _, host := range []string{"-oProxyCommand=evil", "host;touch", "user@host\ncommand"} {
		if err := ValidateSSHOptions(SSHOptions{Host: host, Port: 22}); err == nil {
			t.Errorf("accepted unsafe SSH host %q", host)
		}
	}
	for _, path := range []string{"relative/ptc", "/opt/../ptc", "/opt/ptc\n"} {
		if err := ValidateRemotePath(path); err == nil {
			t.Errorf("accepted unsafe remote path %q", path)
		}
	}
	if err := ValidateRemotePath("/opt/ptc"); err != nil {
		t.Fatalf("rejected valid remote path: %v", err)
	}
}

func TestValidateWindowsRemotePath(t *testing.T) {
	valid := `C:\Program Files\ptc\ptc.exe`
	if err := ValidateRemotePathForPlatform(valid, "windows/amd64"); err != nil {
		t.Fatalf("rejected valid Windows path: %v", err)
	}
	for _, path := range []string{
		`relative\ptc.exe`,
		`C:\ptc\..\other.exe`,
		`C:\ptc\bad:name.exe`,
		`\\?\C:\ptc.exe`,
		`C:\folder\`,
		`C:\ptc\ptc`,
	} {
		if err := ValidateRemotePathForPlatform(path, "windows/amd64"); err == nil {
			t.Errorf("accepted invalid Windows path %q", path)
		}
	}
}

func TestWindowsReplacementCommandEncodesPowerShellAndQuotesPath(t *testing.T) {
	path := `C:\Users\me\prod'$(Write-Output hacked)&\ptc.exe`
	digest := strings.Repeat("ab", 32)
	version := "0.2.0-p2"
	script := buildWindowsReplacementScript(path, digest, version, `C:\Temp\payload.exe`)
	command := powershellRemoteCommand(script)
	prefix := "powershell.exe -NoLogo -NoProfile -NonInteractive -OutputFormat Text -EncodedCommand \""
	if !strings.HasPrefix(command, prefix) {
		t.Fatalf("unexpected PowerShell invocation: %q", command)
	}
	encoded := strings.TrimSuffix(strings.TrimPrefix(command, prefix), `"`)
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	units := make([]uint16, len(payload)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(payload[i*2:])
	}
	decoded := string(utf16.Decode(units))
	if !strings.Contains(decoded, powerShellQuote(path)) {
		t.Fatalf("PowerShell path literal was not correctly quoted: %s", decoded)
	}
	if !strings.Contains(decoded, digest) ||
		!strings.Contains(decoded, "$expectedVersion = '0.2.0-p2'") ||
		!strings.Contains(decoded, "$reportedVersion -cne $expectedVersion") {
		t.Fatal("script omitted checksum or release-version validation")
	}
	if !strings.Contains(decoded, "[System.IO.File]::Move($backup, $target)") ||
		!strings.Contains(decoded, "Replacement failed and rollback failed") {
		t.Fatal("script omitted rollback handling")
	}
	if strings.Contains(command, path) || strings.Contains(command, digest) {
		t.Fatal("remote command contains unencoded user data")
	}
}

func TestSSHReplaceUsesEncodedPowerShellForWindows(t *testing.T) {
	data := []byte("verified windows binary")
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	path := `C:\Users\me\ptc.exe`
	var remotes []string
	var uploaded []byte
	var uploadedScript, localPayload string
	remoteDir := `C:\Users\me\AppData\Local\Temp\ptc-deploy-0123456789abcdef0123456789abcdef`
	ssh := SSHOptions{
		Host: "deploy@windows-host",
		Port: 2022,
		Runner: func(_ context.Context, name string, args []string, stdin io.Reader, stdout, _ io.Writer) error {
			if stdin != nil {
				t.Fatal("Windows deployment must not stream binary stdin")
			}
			if !strings.Contains(strings.Join(args, " "), "StrictHostKeyChecking=yes") {
				t.Fatal("host-key checking was not preserved")
			}
			if name == "scp" {
				if !strings.Contains(strings.Join(args, " "), "-P 2022") {
					t.Fatal("scp did not use the requested SSH port")
				}
				if args[len(args)-1] != "deploy@windows-host:"+strings.ReplaceAll(remoteDir, `\`, "/")+"/" {
					t.Fatalf("unexpected upload destination: %q", args)
				}
				localPayload = args[len(args)-3]
				var err error
				uploaded, err = os.ReadFile(localPayload)
				if err != nil {
					return err
				}
				script, err := os.ReadFile(args[len(args)-2])
				uploadedScript = string(script)
				return err
			}
			if name != "ssh" {
				t.Fatalf("unexpected executable %q", name)
			}
			remotes = append(remotes, args[len(args)-1])
			if len(remotes) == 1 {
				_, err := io.WriteString(stdout, remoteDir+"\r\n")
				return err
			}
			return nil
		},
	}
	err := ssh.Replace(context.Background(), path, Artifact{
		Platform: "windows/amd64",
		Name:     "ptc-windows-amd64.exe",
		Version:  "0.2.0-p2",
		Digest:   digest,
		Data:     data,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(uploaded, data) {
		t.Fatal("verified binary was not uploaded to the Windows host")
	}
	if !strings.Contains(uploadedScript, powerShellQuote(path)) ||
		!strings.Contains(uploadedScript, powerShellQuote(remoteDir+`\payload.exe`)) {
		t.Fatal("Windows paths were not escaped in the uploaded script")
	}
	if _, err := os.Stat(localPayload); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("local deployment files were not cleaned up: %v", err)
	}
	if len(remotes) != 3 {
		t.Fatalf("expected prepare, replacement and cleanup commands, got %d", len(remotes))
	}
	prefix := "powershell.exe -NoLogo -NoProfile -NonInteractive -OutputFormat Text -EncodedCommand \""
	for _, remote := range remotes {
		if !strings.HasPrefix(remote, prefix) || len(remote) >= 8191 {
			t.Fatalf("Windows command is not short encoded PowerShell: %q", remote)
		}
	}
}

func TestWindowsUploadFailureCleansDeploymentFiles(t *testing.T) {
	data := []byte("verified windows binary")
	sum := sha256.Sum256(data)
	uploadErr := errors.New("SFTP upload failed")
	var localPayload string
	sshCalls := 0
	ssh := SSHOptions{
		Host: "windows-host",
		Runner: func(_ context.Context, name string, args []string, _ io.Reader, stdout, _ io.Writer) error {
			if name == "scp" {
				localPayload = args[len(args)-3]
				return uploadErr
			}
			sshCalls++
			if sshCalls == 1 {
				_, err := io.WriteString(stdout, `C:\Temp\ptc-deploy-0123456789abcdef0123456789abcdef`)
				return err
			}
			return nil
		},
	}
	err := ssh.Replace(context.Background(), `C:\Tools\ptc.exe`, Artifact{
		Platform: "windows/amd64",
		Name:     "ptc-windows-amd64.exe",
		Version:  "0.2.0-p2",
		Digest:   hex.EncodeToString(sum[:]),
		Data:     data,
	})
	if !errors.Is(err, uploadErr) || sshCalls != 2 {
		t.Fatalf("upload failure did not propagate with remote cleanup: err=%v SSH calls=%d", err, sshCalls)
	}
	if localPayload == "" {
		t.Fatal("upload did not reach scp")
	}
	if _, err := os.Stat(localPayload); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed upload left local deployment files: %v", err)
	}
}

func TestWindowsDeploymentRejectsUnexpectedCleanupDirectory(t *testing.T) {
	data := []byte("verified windows binary")
	sum := sha256.Sum256(data)
	for _, directory := range []string{`C:\`, `C:\Users\me`, `C:\Temp\ptc-deploy-invalid`} {
		t.Run(directory, func(t *testing.T) {
			calls := 0
			ssh := SSHOptions{
				Host: "windows-host",
				Runner: func(_ context.Context, _ string, _ []string, _ io.Reader, stdout, _ io.Writer) error {
					calls++
					if calls > 1 {
						t.Fatal("unexpected deployment directory must never be uploaded into or recursively removed")
					}
					_, err := io.WriteString(stdout, directory)
					return err
				},
			}
			err := ssh.Replace(context.Background(), `C:\Tools\ptc.exe`, Artifact{
				Platform: "windows/amd64",
				Name:     "ptc-windows-amd64.exe",
				Version:  "0.2.0-p2",
				Digest:   hex.EncodeToString(sum[:]),
				Data:     data,
			})
			if err == nil {
				t.Fatal("accepted unexpected deployment directory")
			}
		})
	}
}

func TestDetectPlatformUsesOpenSSHDefaultsAndMapsArm64(t *testing.T) {
	var gotArgs []string
	ssh := SSHOptions{
		Host: "node",
		Port: 22,
		Runner: func(_ context.Context, _ string, args []string, _ io.Reader, stdout, _ io.Writer) error {
			gotArgs = append([]string(nil), args...)
			_, _ = io.WriteString(stdout, "Linux\naarch64\n")
			return nil
		},
	}
	platform, err := ssh.DetectPlatform(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if platform != "linux/arm64" {
		t.Fatalf("got platform %q", platform)
	}
	args := strings.Join(gotArgs, " ")
	if !strings.Contains(args, "StrictHostKeyChecking=yes") || strings.Contains(args, "StrictHostKeyChecking=no") {
		t.Fatalf("SSH strict host-key checking was not enforced: %q", args)
	}
	if !strings.Contains(args, "uname -s; uname -m") {
		t.Fatalf("missing platform probe: %q", args)
	}
}

func TestReplaceRejectsArtifactDigestMismatch(t *testing.T) {
	called := false
	ssh := SSHOptions{
		Host: "node",
		Port: 22,
		Runner: func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error {
			called = true
			return nil
		},
	}
	err := ssh.Replace(context.Background(), "", Artifact{
		Platform: "linux/amd64",
		Name:     "ptc-linux-amd64",
		Digest:   strings.Repeat("0", 64),
		Data:     []byte("not matching"),
	})
	if err == nil || called {
		t.Fatalf("expected local digest rejection before SSH, err=%v called=%v", err, called)
	}
}
