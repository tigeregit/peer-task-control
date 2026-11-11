// Package install puts ptc on a remote machine over SSH: it copies the
// right-platform binary with SFTP, writes the config and the version-bound
// skill, records install.json and optionally registers a service.
package install

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sshconfig "github.com/kevinburke/ssh_config"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Target is where to connect. User/Port/Key fall back to ~/.ssh/config and
// then to the current user, 22 and the default identity files.
type Target struct {
	Host          string // alias or hostname; may be user@host[:port]
	User          string
	Port          int
	KeyFiles      []string
	KnownHosts    string // default ~/.ssh/known_hosts
	InsecureNoHKC bool   // --insecure-ignore-host-key
	Timeout       time.Duration
}

// ParseTarget splits user@host[:port].
func ParseTarget(s string) Target {
	t := Target{}
	if i := strings.LastIndexByte(s, '@'); i >= 0 {
		t.User, s = s[:i], s[i+1:]
	}
	if h, p, err := net.SplitHostPort(s); err == nil {
		if n, err := strconv.Atoi(p); err == nil {
			t.Port = n
			s = h
		}
	}
	t.Host = s
	return t
}

// Conn is an SSH session pair (commands + SFTP).
type Conn struct {
	Addr   string
	User   string
	client *ssh.Client
	sftp   *sftp.Client
}

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") || p == "~" {
		return filepath.Join(home(), strings.TrimPrefix(p, "~"))
	}
	return p
}

// Dial connects with ssh-agent keys and key files. Passwords are never
// used or stored. Host keys are checked against known_hosts unless
// InsecureNoHKC is set explicitly.
func Dial(t Target) (*Conn, error) {
	alias := t.Host
	hostname := sshconfig.Get(alias, "HostName")
	if hostname == "" {
		hostname = alias
	}
	user := t.User
	if user == "" {
		user = sshconfig.Get(alias, "User")
	}
	if user == "" {
		user = os.Getenv("USER")
		if user == "" {
			user = os.Getenv("USERNAME")
		}
	}
	port := t.Port
	if port == 0 {
		if p, err := strconv.Atoi(sshconfig.Get(alias, "Port")); err == nil && p > 0 {
			port = p
		} else {
			port = 22
		}
	}
	keys := append([]string(nil), t.KeyFiles...)
	if len(keys) == 0 {
		for _, k := range sshconfig.GetAll(alias, "IdentityFile") {
			keys = append(keys, expand(k))
		}
	}
	var auths []ssh.AuthMethod
	var signers []ssh.Signer
	var keyErrs []string
	for _, k := range keys {
		k = expand(k)
		b, err := os.ReadFile(k)
		if err != nil {
			if len(t.KeyFiles) > 0 {
				keyErrs = append(keyErrs, fmt.Sprintf("%s: %v", k, err))
			}
			continue
		}
		s, err := ssh.ParsePrivateKey(b)
		if err != nil {
			var pp *ssh.PassphraseMissingError
			if errors.As(err, &pp) {
				keyErrs = append(keyErrs, fmt.Sprintf("%s is passphrase-protected; load it into ssh-agent (ssh-add) instead", k))
			} else {
				keyErrs = append(keyErrs, fmt.Sprintf("%s: %v", k, err))
			}
			continue
		}
		signers = append(signers, s)
	}
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if c, err := net.Dial("unix", sock); err == nil {
			if as, err := agent.NewClient(c).Signers(); err == nil {
				signers = append(signers, as...)
			}
		}
	}
	if len(signers) == 0 && len(t.KeyFiles) == 0 {
		for _, k := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
			if b, err := os.ReadFile(filepath.Join(home(), ".ssh", k)); err == nil {
				if s, err := ssh.ParsePrivateKey(b); err == nil {
					signers = append(signers, s)
				}
			}
		}
	}
	if len(signers) == 0 {
		msg := "no usable SSH key: pass --key, add an IdentityFile in ~/.ssh/config, or load a key into ssh-agent"
		if len(keyErrs) > 0 {
			msg += " (" + strings.Join(keyErrs, "; ") + ")"
		}
		return nil, errors.New(msg)
	}
	auths = append(auths, ssh.PublicKeys(signers...))

	var hkc ssh.HostKeyCallback
	if t.InsecureNoHKC {
		hkc = ssh.InsecureIgnoreHostKey()
	} else {
		kh := t.KnownHosts
		if kh == "" {
			if v := sshconfig.Get(alias, "UserKnownHostsFile"); v != "" {
				kh = expand(strings.Fields(v)[0])
			} else {
				kh = filepath.Join(home(), ".ssh", "known_hosts")
			}
		}
		cb, err := knownhosts.New(kh)
		if err != nil {
			return nil, fmt.Errorf("known_hosts %s: %w (add the host with ssh-keyscan, or pass --insecure-ignore-host-key explicitly)", kh, err)
		}
		hkc = func(host string, remote net.Addr, key ssh.PublicKey) error {
			err := cb(host, remote, key)
			var ke *knownhosts.KeyError
			if errors.As(err, &ke) {
				if len(ke.Want) == 0 {
					return fmt.Errorf("host %s is not in %s; verify its fingerprint %s and add it (ssh-keyscan), or pass --insecure-ignore-host-key", host, kh, ssh.FingerprintSHA256(key))
				}
				return fmt.Errorf("HOST KEY MISMATCH for %s (got %s): refusing to connect", host, ssh.FingerprintSHA256(key))
			}
			return err
		}
	}
	timeout := t.Timeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	addr := net.JoinHostPort(hostname, strconv.Itoa(port))
	cfg := &ssh.ClientConfig{User: user, Auth: auths, HostKeyCallback: hkc, Timeout: timeout}
	cl, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh %s@%s: %w", user, addr, err)
	}
	sc, err := sftp.NewClient(cl)
	if err != nil {
		cl.Close()
		return nil, fmt.Errorf("sftp on %s: %w", addr, err)
	}
	return &Conn{Addr: addr, User: user, client: cl, sftp: sc}, nil
}

func (c *Conn) Close() error {
	if c == nil {
		return nil
	}
	_ = c.sftp.Close()
	return c.client.Close()
}

// Run executes a command and returns stdout, stderr.
func (c *Conn) Run(cmd string) (string, string, error) {
	s, err := c.client.NewSession()
	if err != nil {
		return "", "", err
	}
	defer s.Close()
	var out, errb bytes.Buffer
	s.Stdout, s.Stderr = &out, &errb
	err = s.Run(cmd)
	return out.String(), errb.String(), err
}

// SFTP exposes the file client.
func (c *Conn) SFTP() *sftp.Client { return c.sftp }
