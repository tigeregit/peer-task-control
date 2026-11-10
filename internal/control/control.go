package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/peer-task-control/ptc/internal/catalog"
	"github.com/peer-task-control/ptc/internal/config"
)

type LockFile struct {
	PID    int    `json:"pid"`
	API    string `json:"api"`
	PeerID string `json:"peer_id"`
	Name   string `json:"name"`
}

func WriteLock(path string, l LockFile) error {
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func ReadLock(path string) (LockFile, error) {
	var l LockFile
	b, err := os.ReadFile(path)
	if err != nil {
		return l, err
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return l, err
	}
	return l, nil
}

func RemoveLock(path string) {
	_ = os.Remove(path)
}

type Client struct {
	Base string
	HTTP *http.Client
}

func NewClient(base string) *Client {
	return &Client{
		Base: "http://" + base,
		HTTP: &http.Client{Timeout: 5 * time.Second},
	}
}

func ClientFromDataDir(dataDir, apiFlag string) (*Client, error) {
	if apiFlag != "" {
		return NewClient(apiFlag), nil
	}
	if dataDir == "" {
		dataDir = config.DefaultDataDir()
	}
	l, err := ReadLock(filepath.Join(dataDir, "daemon.lock"))
	if err != nil {
		return nil, fmt.Errorf("daemon lock not found in %s (is ptc daemon running?): %w", dataDir, err)
	}
	if l.API == "" {
		return nil, fmt.Errorf("daemon lock has no api address")
	}
	return NewClient(l.API), nil
}

type Status struct {
	Name    string          `json:"name"`
	PeerID  string          `json:"peer_id"`
	Version string          `json:"version"`
	Listen  []string        `json:"listen"`
	Peers   []catalog.Entry `json:"peers"`
}

type PolicyView struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
	Exec  bool     `json:"exec"`
	Files bool     `json:"files"`
	Relay bool     `json:"relay"`
}

type PolicyChange struct {
	Op      string `json:"op"` // allow_add, allow_rm, deny_add, deny_rm, service
	Spec    string `json:"spec,omitempty"`
	Service string `json:"service,omitempty"`
	On      *bool  `json:"on,omitempty"`
}

func (c *Client) get(path string, out any) error {
	resp, err := c.HTTP.Get(c.Base + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(body))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

func (c *Client) post(path string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Post(c.Base+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(body))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

func (c *Client) Status() (Status, error) {
	var s Status
	err := c.get("/api/v1/status", &s)
	return s, err
}

func (c *Client) Policy() (PolicyView, error) {
	var p PolicyView
	err := c.get("/api/v1/policy", &p)
	return p, err
}

func (c *Client) ApplyPolicy(ch PolicyChange) (PolicyView, error) {
	var p PolicyView
	err := c.post("/api/v1/policy", ch, &p)
	return p, err
}
