package install

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/tigeregit/peer-task-control/internal/selfupdate"
)

// Platform is a remote OS/arch.
type Platform struct {
	OS   string // linux, windows, darwin
	Arch string // amd64, arm64
}

func (p Platform) String() string { return p.OS + "/" + p.Arch }

// Layout is where ptc goes on the remote host. Paths are native to the
// remote OS (backslashes on Windows).
type Layout struct {
	Platform Platform
	System   bool // /usr/local/bin + /etc/ptc, or Program Files
	Sudo     bool // system install through `sudo -n`
	Bin      string
	DataDir  string
	Config   string
	Log      string
	Home     string
}

// Options for one install.
type Options struct {
	OS              string // auto, linux, windows
	System          bool   // system-wide location (root/sudo or elevated)
	Service         bool
	ServiceName     string
	Version         string
	Binary          []byte // the binary for the remote platform (resolved by the caller)
	Skill           []byte // version-bound skill text
	Config          []byte // config.yaml to upload; nil = keep existing or generate
	GenConfig       func(Layout) ([]byte, error)
	OverwriteConfig bool
	DryRun          bool
	Log             io.Writer
}

// Step is one planned or performed action.
type Step struct {
	Action string `json:"action"`
	Detail string `json:"detail"`
	Done   bool   `json:"done"`
	Skip   bool   `json:"skipped,omitempty"`
}

// Report summarises an install.
type Report struct {
	Host     string   `json:"host"`
	Platform string   `json:"platform"`
	Layout   Layout   `json:"layout"`
	Version  string   `json:"version"`
	Previous string   `json:"previous_version,omitempty"`
	Service  string   `json:"service"`
	Steps    []Step   `json:"steps"`
	DryRun   bool     `json:"dry_run,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

func (r *Report) step(o Options, action, detail string, done bool) {
	r.Steps = append(r.Steps, Step{Action: action, Detail: detail, Done: done && !o.DryRun, Skip: !done})
	if o.Log != nil {
		mark := "ok  "
		switch {
		case !done:
			mark = "skip"
		case o.DryRun:
			mark = "plan"
		}
		fmt.Fprintf(o.Log, "  %s %-10s %s\n", mark, action, detail)
	}
}

// Detect finds the remote OS and architecture.
func Detect(c *Conn, force string) (Platform, error) {
	if force == "" || force == "auto" || force == "linux" || force == "darwin" {
		out, _, err := c.Run("uname -sm")
		if err == nil {
			f := strings.Fields(out)
			if len(f) == 2 {
				osName := strings.ToLower(f[0])
				if osName == "linux" || osName == "darwin" {
					return Platform{OS: osName, Arch: normArch(f[1])}, nil
				}
			}
		}
		if force == "linux" || force == "darwin" {
			return Platform{}, fmt.Errorf("uname failed on a host forced to %s: %v", force, err)
		}
	}
	out, _, err := c.Run(`powershell -NoProfile -NonInteractive -Command "$env:PROCESSOR_ARCHITECTURE"`)
	if err != nil {
		return Platform{}, fmt.Errorf("cannot detect remote OS (uname and powershell both failed): %w", err)
	}
	return Platform{OS: "windows", Arch: normArch(strings.TrimSpace(out))}, nil
}

func normArch(a string) string {
	switch strings.ToLower(strings.TrimSpace(a)) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	}
	return strings.ToLower(strings.TrimSpace(a))
}

// sq quotes for POSIX sh.
func sq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }

// psq quotes for PowerShell single-quoted strings.
func psq(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func ps(cmd string) string {
	return `powershell -NoProfile -NonInteractive -Command "` + strings.ReplaceAll(cmd, `"`, `\"`) + `"`
}

// sftpPath maps a native Windows path to the form Windows OpenSSH SFTP uses.
func sftpPath(p Platform, native string) string {
	if p.OS != "windows" {
		return native
	}
	s := strings.ReplaceAll(native, `\`, "/")
	if len(s) >= 2 && s[1] == ':' {
		s = "/" + s
	}
	return s
}

func plan(c *Conn, p Platform, o Options) (Layout, error) {
	l := Layout{Platform: p, System: o.System}
	if p.OS == "windows" {
		out, _, err := c.Run(ps(`Write-Output $env:LOCALAPPDATA; Write-Output $env:APPDATA; Write-Output $env:ProgramFiles; Write-Output $env:ProgramData; Write-Output $env:USERPROFILE; Write-Output ([bool](([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)))`))
		if err != nil {
			return l, fmt.Errorf("read Windows profile paths: %w", err)
		}
		v := strings.Split(strings.ReplaceAll(strings.TrimSpace(out), "\r", ""), "\n")
		if len(v) < 6 {
			return l, fmt.Errorf("unexpected Windows profile output %q", out)
		}
		local, appdata, pf, pd, prof, admin := v[0], v[1], v[2], v[3], v[4], strings.EqualFold(v[5], "true")
		l.Home = prof
		if o.System {
			if !admin {
				return l, fmt.Errorf("--system on Windows needs an elevated (administrator) SSH session")
			}
			l.Bin = pf + `\ptc\ptc.exe`
			l.DataDir = pd + `\ptc`
		} else {
			l.Bin = local + `\ptc\bin\ptc.exe`
			l.DataDir = appdata + `\ptc`
		}
		l.Config = l.DataDir + `\config.yaml`
		l.Log = l.DataDir + `\daemon.log`
		return l, nil
	}
	out, _, err := c.Run(`printf '%s\n' "$HOME" "$(id -u)" "${XDG_CONFIG_HOME:-}"`)
	if err != nil {
		return l, fmt.Errorf("read remote home: %w", err)
	}
	v := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for len(v) < 3 {
		v = append(v, "")
	}
	l.Home = v[0]
	root := v[1] == "0"
	if o.System {
		if !root {
			if _, _, err := c.Run("sudo -n true"); err != nil {
				return l, fmt.Errorf("--system needs root or passwordless sudo on the remote host (sudo -n true failed)")
			}
			l.Sudo = true
		}
		l.Bin = "/usr/local/bin/ptc"
		l.DataDir = "/var/lib/ptc"
		l.Config = "/etc/ptc/config.yaml"
		l.Log = "/var/log/ptc.log"
		return l, nil
	}
	cfgHome := v[2]
	if cfgHome == "" {
		cfgHome = l.Home + "/.config"
		if p.OS == "darwin" {
			cfgHome = l.Home + "/Library/Application Support"
		}
	}
	l.Bin = l.Home + "/.local/bin/ptc"
	l.DataDir = cfgHome + "/ptc"
	l.Config = l.DataDir + "/config.yaml"
	l.Log = l.DataDir + "/daemon.log"
	return l, nil
}

func dirOf(p Platform, native string) string {
	if p.OS == "windows" {
		i := strings.LastIndexByte(native, '\\')
		if i < 0 {
			return native
		}
		return native[:i]
	}
	return path.Dir(native)
}

func remoteSHA256(c *Conn, l Layout, native string) string {
	if l.Platform.OS == "windows" {
		out, _, err := c.Run(ps(`if (Test-Path -LiteralPath ` + psq(native) + `) { (Get-FileHash -Algorithm SHA256 -LiteralPath ` + psq(native) + `).Hash }`))
		if err != nil {
			return ""
		}
		return strings.ToLower(strings.TrimSpace(out))
	}
	out, _, err := c.Run(`if [ -f ` + sq(native) + ` ]; then (sha256sum ` + sq(native) + ` 2>/dev/null || shasum -a 256 ` + sq(native) + `) | cut -d' ' -f1; fi`)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func remoteVersion(c *Conn, l Layout) string {
	var cmd string
	if l.Platform.OS == "windows" {
		cmd = ps(`if (Test-Path -LiteralPath ` + psq(l.Bin) + `) { & ` + psq(l.Bin) + ` version }`)
	} else {
		cmd = `[ -x ` + sq(l.Bin) + ` ] && ` + sq(l.Bin) + ` version`
	}
	out, _, err := c.Run(cmd)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func (c *Conn) mkdirAll(l Layout, native string) error {
	if l.Sudo {
		_, se, err := c.Run("sudo -n mkdir -p " + sq(native))
		if err != nil {
			return fmt.Errorf("sudo mkdir %s: %v %s", native, err, se)
		}
		return nil
	}
	return c.sftp.MkdirAll(sftpPath(l.Platform, native))
}

// put writes data to native path via a temp file and rename.
func (c *Conn) put(l Layout, native string, data []byte, mode os.FileMode) error {
	if l.Sudo {
		tmp := fmt.Sprintf("/tmp/ptc-install-%d", time.Now().UnixNano())
		if err := c.writeFile(tmp, data, mode); err != nil {
			return err
		}
		_, se, err := c.Run(fmt.Sprintf("sudo -n install -m %o %s %s && rm -f %s", mode, sq(tmp), sq(native), sq(tmp)))
		if err != nil {
			return fmt.Errorf("sudo install %s: %v %s", native, err, se)
		}
		return nil
	}
	sp := sftpPath(l.Platform, native)
	tmp := sp + ".new"
	if err := c.writeFile(tmp, data, mode); err != nil {
		return err
	}
	if l.Platform.OS == "windows" {
		_ = c.sftp.Remove(sp + ".old")
		if _, err := c.sftp.Stat(sp); err == nil {
			if err := c.sftp.Rename(sp, sp+".old"); err != nil {
				return fmt.Errorf("move %s aside: %w", native, err)
			}
		}
		if err := c.sftp.Rename(tmp, sp); err != nil {
			return fmt.Errorf("rename into %s: %w", native, err)
		}
		_ = c.sftp.Remove(sp + ".old")
		return nil
	}
	if err := c.sftp.PosixRename(tmp, sp); err != nil {
		return fmt.Errorf("rename into %s: %w", native, err)
	}
	return nil
}

func (c *Conn) writeFile(sp string, data []byte, mode os.FileMode) error {
	f, err := c.sftp.OpenFile(sp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY)
	if err != nil {
		return fmt.Errorf("create %s: %w", sp, err)
	}
	if _, err := f.ReadFrom(bytes.NewReader(data)); err != nil {
		f.Close()
		return fmt.Errorf("write %s: %w", sp, err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	_ = c.sftp.Chmod(sp, mode)
	return nil
}

func (c *Conn) exists(l Layout, native string) bool {
	if l.Sudo {
		_, _, err := c.Run("sudo -n test -e " + sq(native))
		return err == nil
	}
	_, err := c.sftp.Stat(sftpPath(l.Platform, native))
	return err == nil
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Run performs (or, with DryRun, plans) an idempotent install.
func Run(c *Conn, o Options) (Report, error) {
	rep := Report{Host: c.Addr, Version: o.Version, DryRun: o.DryRun}
	p, err := Detect(c, o.OS)
	if err != nil {
		return rep, err
	}
	rep.Platform = p.String()
	l, err := plan(c, p, o)
	if err != nil {
		return rep, err
	}
	rep.Layout = l
	if o.Log != nil {
		fmt.Fprintf(o.Log, "%s %s@%s: %s -> %s\n", map[bool]string{true: "plan for", false: "installing on"}[o.DryRun], c.User, c.Addr, p, l.Bin)
	}
	rep.Previous = remoteVersion(c, l)

	// 1. binary
	want := sum(o.Binary)
	have := remoteSHA256(c, l, l.Bin)
	binChanged := have != want
	if binChanged {
		detail := fmt.Sprintf("%s (%d bytes, sha256 %s…)", l.Bin, len(o.Binary), want[:12])
		if rep.Previous != "" {
			detail += " replacing " + rep.Previous
		}
		if !o.DryRun {
			if err := c.mkdirAll(l, dirOf(p, l.Bin)); err != nil {
				return rep, err
			}
			if err := c.put(l, l.Bin, o.Binary, 0o755); err != nil {
				return rep, err
			}
		}
		rep.step(o, "binary", detail, true)
	} else {
		rep.step(o, "binary", l.Bin+" already at sha256 "+want[:12]+"…", false)
	}

	// 2. skill copy next to the binary (version-bound)
	skillPath := dirOf(p, l.Bin) + map[bool]string{true: `\SKILL.md`, false: "/SKILL.md"}[p.OS == "windows"]
	if len(o.Skill) > 0 {
		if remoteSHA256(c, l, skillPath) != sum(o.Skill) {
			if !o.DryRun {
				if err := c.put(l, skillPath, o.Skill, 0o644); err != nil {
					return rep, err
				}
			}
			rep.step(o, "skill", skillPath, true)
		} else {
			rep.step(o, "skill", skillPath+" up to date", false)
		}
	}

	// 3. config
	cfgExists := c.exists(l, l.Config)
	var cfg []byte
	switch {
	case o.Config != nil:
		cfg = o.Config
	case !cfgExists || o.OverwriteConfig:
		if o.GenConfig == nil {
			return rep, fmt.Errorf("%s has no config; pass --config or --trusted-cidr", l.Config)
		}
		if cfg, err = o.GenConfig(l); err != nil {
			return rep, err
		}
	}
	if cfg != nil && remoteSHA256(c, l, l.Config) != sum(cfg) {
		if cfgExists && !o.OverwriteConfig && o.Config == nil {
			rep.step(o, "config", l.Config+" exists; kept (pass --config or --overwrite-config to replace)", false)
		} else {
			if !o.DryRun {
				if err := c.mkdirAll(l, l.DataDir); err != nil {
					return rep, err
				}
				if err := c.mkdirAll(l, dirOf(p, l.Config)); err != nil {
					return rep, err
				}
				if err := c.put(l, l.Config, cfg, 0o600); err != nil {
					return rep, err
				}
			}
			rep.step(o, "config", l.Config, true)
		}
	} else {
		rep.step(o, "config", l.Config+" unchanged", false)
	}

	// 4. service
	svc := selfupdate.ServiceNone
	name := o.ServiceName
	if name == "" {
		name = "ptc"
	}
	if o.Service {
		svc, err = installService(c, l, o, name, binChanged)
		if err != nil {
			return rep, err
		}
	}
	rep.Service = svc

	// 5. install.json (marks the daemon install-managed for ptc update)
	now := time.Now().UTC()
	man := selfupdate.Install{Version: o.Version, Previous: rep.Previous, Binary: l.Bin, Config: l.Config, DataDir: l.DataDir, Service: svc, ServiceName: name, Log: l.Log, InstalledAt: now, UpdatedAt: now, InstalledBy: "ptc install --ssh"}
	mb, _ := json.MarshalIndent(man, "", "  ")
	manPath := l.DataDir + map[bool]string{true: `\install.json`, false: "/install.json"}[p.OS == "windows"]
	if !o.DryRun {
		if err := c.mkdirAll(l, l.DataDir); err != nil {
			return rep, err
		}
		if err := c.put(l, manPath, append(mb, '\n'), 0o644); err != nil {
			return rep, err
		}
	}
	rep.step(o, "manifest", manPath, true)

	// 6. verify
	if !o.DryRun {
		got := remoteVersion(c, l)
		if got != o.Version {
			return rep, fmt.Errorf("remote %s version reports %q, want %q", l.Bin, got, o.Version)
		}
		rep.step(o, "verify", l.Bin+" version = "+got, true)
	}
	return rep, nil
}
