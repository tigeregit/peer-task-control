package deploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

var safeSSHHost = regexp.MustCompile(`^[A-Za-z0-9_.@:\[\]-]+$`)
var windowsAbsolutePath = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
var deploymentDirectoryName = regexp.MustCompile(`^ptc-deploy-[0-9a-f]{32}$`)

type SSHOptions struct {
	Host           string
	Port           int
	IdentityFile   string
	KnownHostsFile string
	Runner         CommandRunner
	Stdout         io.Writer
	Stderr         io.Writer
}

// CommandRunner is injectable to keep SSH command construction testable.
type CommandRunner func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) error

func ValidateSSHOptions(opts SSHOptions) error {
	if opts.Host == "" || !safeSSHHost.MatchString(opts.Host) || strings.HasPrefix(opts.Host, "-") {
		return errors.New("invalid SSH host; use a host alias, hostname, address, or user@host")
	}
	if strings.ContainsAny(opts.Host, "\r\n\x00") {
		return errors.New("SSH host must not contain control characters")
	}
	if opts.Port < 0 || opts.Port > 65535 {
		return errors.New("SSH port must be between 1 and 65535")
	}
	if opts.IdentityFile != "" && strings.ContainsAny(opts.IdentityFile, "\r\n\x00") {
		return errors.New("SSH identity path must not contain control characters")
	}
	if opts.KnownHostsFile != "" && strings.ContainsAny(opts.KnownHostsFile, "\r\n\x00") {
		return errors.New("known-hosts path must not contain control characters")
	}
	return nil
}

func ValidateRemotePath(path string) error {
	if path == "" {
		return nil
	}
	if len(path) > 4096 {
		return errors.New("remote path exceeds the supported length")
	}
	if !strings.HasPrefix(path, "/") || path == "/" || strings.HasSuffix(path, "/") {
		return errors.New("remote path must be an absolute file path")
	}
	if strings.ContainsAny(path, "\r\n\x00") || strings.Contains(path, "\\") {
		return errors.New("remote path contains an unsupported character")
	}
	for _, part := range strings.Split(path, "/") {
		if part == "." || part == ".." {
			return errors.New("remote path must not contain . or .. components")
		}
	}
	return nil
}

func ValidateRemotePathForPlatform(path, platform string) error {
	if platform != "windows/amd64" {
		return ValidateRemotePath(path)
	}
	if path == "" {
		return nil
	}
	if len(path) > 2048 {
		return errors.New("Windows remote path exceeds the supported length")
	}
	if strings.ContainsAny(path, "\r\n\x00") || !windowsAbsolutePath.MatchString(path) {
		return errors.New("Windows remote path must be an absolute drive path")
	}
	if strings.HasPrefix(path, `\\?\`) || strings.HasPrefix(path, `\\.\`) {
		return errors.New("Windows device paths are not supported")
	}
	if strings.HasSuffix(path, `\`) || strings.HasSuffix(path, "/") {
		return errors.New("remote path must name a file, not a directory")
	}
	if !strings.EqualFold(filepath.Ext(path), ".exe") {
		return errors.New("Windows remote target must have an .exe extension")
	}
	if strings.ContainsAny(path[2:], `<>:"|?*`) {
		return errors.New("Windows remote path contains an invalid filename character")
	}
	parts := strings.FieldsFunc(path[3:], func(r rune) bool { return r == '\\' || r == '/' })
	for _, part := range parts {
		if part == "." || part == ".." {
			return errors.New("Windows remote path must not contain . or .. components")
		}
	}
	if len(parts) == 0 {
		return errors.New("Windows remote path must name a file")
	}
	return nil
}

func (s SSHOptions) DetectPlatform(ctx context.Context) (string, error) {
	if err := ValidateSSHOptions(s); err != nil {
		return "", err
	}
	var windowsOutput, windowsErr bytes.Buffer
	windowsSSH := s
	windowsSSH.Stderr = &windowsErr
	windowsRemote := powershellRemoteCommand(windowsPlatformProbe)
	if err := windowsSSH.run(ctx, []string{windowsRemote}, nil, &windowsOutput); err == nil {
		detected := strings.TrimSpace(windowsOutput.String())
		if strings.HasPrefix(detected, "windows/") {
			if _, err := BinaryName(detected); err != nil {
				return "", err
			}
			return detected, nil
		}
	}

	var output bytes.Buffer
	if err := s.run(ctx, []string{"uname -s; uname -m"}, nil, &output); err != nil {
		return "", fmt.Errorf("detect SSH target platform (Windows and Linux probes failed): %w", err)
	}
	fields := strings.Fields(output.String())
	if len(fields) != 2 || fields[0] != "Linux" {
		return "", fmt.Errorf("unsupported SSH target OS/architecture %q (supported: Linux amd64 and arm64)", strings.TrimSpace(output.String()))
	}
	arch := fields[1]
	switch arch {
	case "x86_64", "amd64":
		arch = "amd64"
	case "aarch64", "arm64":
		arch = "arm64"
	default:
		return "", fmt.Errorf("unsupported SSH target architecture %q (supported: amd64, arm64)", arch)
	}
	return "linux/" + arch, nil
}

// Replace atomically installs a verified artifact on a remote host.
// The old binary is restored if the replacement or its post-install check fails.
func (s SSHOptions) Replace(ctx context.Context, path string, artifact Artifact) error {
	if err := ValidateSSHOptions(s); err != nil {
		return err
	}
	name, err := BinaryName(artifact.Platform)
	if err != nil {
		return err
	}
	if err := ValidateRemotePathForPlatform(path, artifact.Platform); err != nil {
		return err
	}
	if artifact.Name != name || len(artifact.Data) == 0 || len(artifact.Digest) != 64 {
		return errors.New("invalid release artifact")
	}
	digest := strings.ToLower(artifact.Digest)
	if _, err := hex.DecodeString(digest); err != nil {
		return errors.New("invalid release artifact digest")
	}
	actualBytes := sha256.Sum256(artifact.Data)
	actual := hex.EncodeToString(actualBytes[:])
	if subtle.ConstantTimeCompare([]byte(actual), []byte(digest)) != 1 {
		return errors.New("release artifact checksum does not match its data")
	}

	if artifact.Platform == "windows/amd64" {
		if !releaseVersionPattern.MatchString(artifact.Version) {
			return errors.New("invalid release artifact version")
		}
		return s.replaceWindows(ctx, path, digest, artifact)
	}

	remote := "sh -c " + shellQuote(replacementScript) + " ptc " +
		shellQuote(path) + " " + shellQuote(digest)
	if err := s.run(ctx, []string{remote}, bytes.NewReader(artifact.Data), nil); err != nil {
		return fmt.Errorf("replace remote ptc binary: %w", err)
	}
	return nil
}

func (s SSHOptions) run(ctx context.Context, remoteArgs []string, stdin io.Reader, stdout io.Writer) error {
	args := append([]string{"-T"}, s.connectionArgs(false)...)
	args = append(args, s.Host)
	args = append(args, remoteArgs...)
	return s.runCommand(ctx, "ssh", args, stdin, stdout)
}

func (s SSHOptions) connectionArgs(scp bool) []string {
	args := []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes"}
	if s.KnownHostsFile != "" {
		path := strings.ReplaceAll(filepath.ToSlash(s.KnownHostsFile), `\`, `\\`)
		path = strings.ReplaceAll(path, `"`, `\"`)
		args = append(args, "-o", `UserKnownHostsFile="`+path+`"`)
	}
	if s.Port > 0 {
		flag := "-p"
		if scp {
			flag = "-P"
		}
		args = append(args, flag, strconv.Itoa(s.Port))
	}
	if s.IdentityFile != "" {
		args = append(args, "-i", s.IdentityFile)
	}
	return args
}

func (s SSHOptions) runCommand(ctx context.Context, command string, args []string, stdin io.Reader, stdout io.Writer) error {
	runner := s.Runner
	if runner == nil {
		runner = execRunner
	}
	out := stdout
	if out == nil {
		out = s.Stdout
	}
	errOut := s.Stderr
	if errOut == nil {
		errOut = os.Stderr
	}
	return runner(ctx, command, args, stdin, out, errOut)
}

func (s SSHOptions) replaceWindows(ctx context.Context, path, digest string, artifact Artifact) (err error) {
	var output bytes.Buffer
	prepare := `$ErrorActionPreference = 'Stop'
$directory = Join-Path ([System.IO.Path]::GetTempPath()) ('ptc-deploy-' + [Guid]::NewGuid().ToString('N'))
[void][System.IO.Directory]::CreateDirectory($directory)
[Console]::Out.WriteLine($directory)`
	if err := s.run(ctx, []string{powershellRemoteCommand(prepare)}, nil, &output); err != nil {
		return fmt.Errorf("prepare remote Windows deployment: %w", err)
	}
	remoteDir := strings.TrimSpace(output.String())
	lastSeparator := strings.LastIndexAny(remoteDir, `\/`)
	if lastSeparator < 0 || !deploymentDirectoryName.MatchString(remoteDir[lastSeparator+1:]) {
		return fmt.Errorf("unexpected remote deployment directory %q", remoteDir)
	}
	payloadPath := remoteDir + `\payload.exe`
	if err := ValidateRemotePathForPlatform(payloadPath, artifact.Platform); err != nil {
		return fmt.Errorf("invalid remote deployment directory: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		cleanup := `$ErrorActionPreference = 'Stop'; Remove-Item -LiteralPath ` + powerShellQuote(remoteDir) + ` -Recurse -Force`
		if cleanupErr := s.run(cleanupCtx, []string{powershellRemoteCommand(cleanup)}, nil, nil); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove remote deployment directory %s: %w", remoteDir, cleanupErr))
		}
	}()
	localDir, err := os.MkdirTemp("", "ptc-deploy-")
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(localDir); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove local deployment directory: %w", cleanupErr))
		}
	}()
	localPayload := filepath.Join(localDir, "payload.exe")
	localScript := filepath.Join(localDir, "replace.ps1")
	if err := os.WriteFile(localPayload, artifact.Data, 0o600); err != nil {
		return err
	}
	script := buildWindowsReplacementScript(path, digest, artifact.Version, payloadPath)
	if err := os.WriteFile(localScript, []byte(script), 0o600); err != nil {
		return err
	}
	args := append([]string{"-q"}, s.connectionArgs(true)...)
	destination := s.Host + ":" + strings.ReplaceAll(remoteDir, `\`, "/") + "/"
	args = append(args, "--", localPayload, localScript, destination)
	if err := s.runCommand(ctx, "scp", args, nil, nil); err != nil {
		return fmt.Errorf("upload Windows deployment with scp: %w", err)
	}
	// Execute the uploaded script with a short command, avoiding cmd.exe's
	// 8191-character limit and Windows SSH's binary-stdin handling.
	launch := `$ErrorActionPreference = 'Stop'; & ([scriptblock]::Create([System.IO.File]::ReadAllText(` +
		powerShellQuote(remoteDir+`\replace.ps1`) + `)))`
	if err := s.run(ctx, []string{powershellRemoteCommand(launch)}, nil, nil); err != nil {
		return fmt.Errorf("replace remote Windows ptc binary: %w", err)
	}
	return nil
}

func execRunner(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func powerShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func powershellRemoteCommand(script string) string {
	script = "$ProgressPreference = 'SilentlyContinue'\n" + script
	encoded := utf16.Encode([]rune(script))
	payload := make([]byte, len(encoded)*2)
	for i, unit := range encoded {
		binary.LittleEndian.PutUint16(payload[i*2:], unit)
	}
	return "powershell.exe -NoLogo -NoProfile -NonInteractive -OutputFormat Text -EncodedCommand \"" +
		base64.StdEncoding.EncodeToString(payload) + "\""
}

func buildWindowsReplacementScript(path, digest, version, payloadPath string) string {
	return strings.NewReplacer(
		"__TARGET_LITERAL__", powerShellQuote(path),
		"X_EXPECTED_SHA256_X", digest,
		"X_EXPECTED_VERSION_X", powerShellQuote(version),
		"__PAYLOAD_LITERAL__", powerShellQuote(payloadPath),
	).Replace(windowsReplacementScriptTemplate)
}

const windowsPlatformProbe = `$ErrorActionPreference = 'Stop'
$architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
if ($architecture -eq 'X64') { [Console]::Out.WriteLine('windows/amd64') }
elseif ($architecture -eq 'Arm64') { [Console]::Out.WriteLine('windows/arm64') }
else { exit 3 }`

const windowsReplacementScriptTemplate = `$ErrorActionPreference = 'Stop'
$target = __TARGET_LITERAL__
$expectedHash = 'X_EXPECTED_SHA256_X'
$expectedVersion = X_EXPECTED_VERSION_X
if ([string]::IsNullOrWhiteSpace($target)) {
  $base = $env:LOCALAPPDATA
  if ([string]::IsNullOrWhiteSpace($base)) { $base = $env:USERPROFILE }
  if ([string]::IsNullOrWhiteSpace($base)) { throw 'No local user profile directory is available' }
  $target = Join-Path $base 'Programs\ptc\ptc.exe'
}
$target = [System.IO.Path]::GetFullPath($target)
$directory = [System.IO.Path]::GetDirectoryName($target)
if ([string]::IsNullOrWhiteSpace($directory)) { throw 'Target must include a directory' }
if ([System.IO.Directory]::Exists($target)) { throw 'Target is a directory' }
[void][System.IO.Directory]::CreateDirectory($directory)
$stage = Join-Path $directory ('.ptc-new-' + [Guid]::NewGuid().ToString('N') + '.exe')
$backup = Join-Path $directory ('.ptc-backup-' + [Guid]::NewGuid().ToString('N') + '.exe')
$hadBackup = $false
$installed = $false
$stream = $null
$stdinStream = $null
try {
  $stdinStream = [System.IO.File]::OpenRead(__PAYLOAD_LITERAL__)
  $stream = [System.IO.File]::Open($stage, [System.IO.FileMode]::CreateNew, [System.IO.FileAccess]::Write, [System.IO.FileShare]::None)
  $stdinStream.CopyTo($stream)
  $stream.Flush($true)
  $stream.Dispose()
  $stream = $null
  $stdinStream.Dispose()
  $stdinStream = $null
  $sha = [System.Security.Cryptography.SHA256]::Create()
  try {
    $stream = [System.IO.File]::OpenRead($stage)
    try { $actualHash = [BitConverter]::ToString($sha.ComputeHash($stream)).Replace('-', '').ToLowerInvariant() }
    finally { $stream.Dispose(); $stream = $null }
  } finally { $sha.Dispose() }
  if ($actualHash -cne $expectedHash) { throw 'Staged binary SHA256 mismatch' }
  $reportedVersion = (& $stage version 2>$null | Out-String).Trim()
  if ($LASTEXITCODE -ne 0 -or $reportedVersion -cne $expectedVersion) { throw 'Staged binary version does not match the selected release' }
  $targetAcl = $null
  if ([System.IO.File]::Exists($target)) {
    $item = Get-Item -LiteralPath $target -Force
    if (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'Refusing to replace a reparse-point target' }
    $targetAcl = Get-Acl -LiteralPath $target
    Set-Acl -LiteralPath $stage -AclObject $targetAcl
    [System.IO.File]::Move($target, $backup)
    $hadBackup = $true
  }
  [System.IO.File]::Move($stage, $target)
  $installed = $true
  $sha = [System.Security.Cryptography.SHA256]::Create()
  try {
    $stream = [System.IO.File]::OpenRead($target)
    try { $actualHash = [BitConverter]::ToString($sha.ComputeHash($stream)).Replace('-', '').ToLowerInvariant() }
    finally { $stream.Dispose() }
  } finally { $sha.Dispose() }
  if ($actualHash -cne $expectedHash) { throw 'Installed binary SHA256 mismatch' }
  $reportedVersion = (& $target version 2>$null | Out-String).Trim()
  if ($LASTEXITCODE -ne 0 -or $reportedVersion -cne $expectedVersion) { throw 'Installed binary version mismatch' }
  if ($hadBackup) { [System.IO.File]::Delete($backup) }
} catch {
  $failure = $_
  try {
    if ($hadBackup -and [System.IO.File]::Exists($backup)) {
      if ([System.IO.File]::Exists($target)) { [System.IO.File]::Delete($target) }
      [System.IO.File]::Move($backup, $target)
    } elseif ($installed -and [System.IO.File]::Exists($target)) {
      [System.IO.File]::Delete($target)
    }
  } catch {
    throw "Replacement failed and rollback failed; preserve backup at $backup. Original error: $($failure.Exception.Message)"
  }
  throw $failure
} finally {
  if ($null -ne $stream) { $stream.Dispose() }
  if ($null -ne $stdinStream) { $stdinStream.Dispose() }
  if ([System.IO.File]::Exists($stage)) { [System.IO.File]::Delete($stage) }
}`

const replacementScript = `set -eu
target=$1
expected=$2
if [ -z "$target" ]; then target="${HOME}/.local/bin/ptc"; fi
case "$target" in /*) ;; *) echo "target must be absolute" >&2; exit 2 ;; esac
if [ -d "$target" ]; then echo "target is a directory" >&2; exit 2; fi
dir=${target%/*}
if [ -z "$dir" ]; then dir=/; fi
mkdir -p "$dir"
stage="${target}.ptc-new-$$"
backup="${target}.ptc-backup-$$"
if [ -e "$stage" ] || [ -L "$stage" ] || [ -e "$backup" ] || [ -L "$backup" ]; then
  echo "temporary deployment path already exists" >&2
  exit 1
fi
installed=0
rollback() {
  status=$?
  trap - EXIT HUP INT TERM
  if [ "$status" -ne 0 ]; then
    if [ -e "$backup" ] || [ -L "$backup" ]; then
      rm -f "$target"
      if ! mv "$backup" "$target"; then echo "rollback failed; backup remains at $backup" >&2; fi
    elif [ "$installed" -eq 1 ]; then
      rm -f "$target"
    fi
  fi
  rm -f "$stage"
  exit "$status"
}
trap rollback EXIT
trap 'exit 1' HUP INT TERM
cat > "$stage"
chmod 0755 "$stage"
actual=$(sha256sum "$stage")
actual=${actual%% *}
if [ "$actual" != "$expected" ]; then echo "staged binary checksum mismatch" >&2; exit 1; fi
if [ -e "$target" ] || [ -L "$target" ]; then mv "$target" "$backup"; fi
mv "$stage" "$target"
installed=1
actual=$(sha256sum "$target")
actual=${actual%% *}
if [ "$actual" != "$expected" ]; then echo "installed binary checksum mismatch" >&2; exit 1; fi
if [ -e "$backup" ] || [ -L "$backup" ]; then rm -f "$backup"; fi
trap - EXIT HUP INT TERM`
