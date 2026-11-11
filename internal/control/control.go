package control

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tigeregit/peer-task-control/internal/catalog"
	"github.com/tigeregit/peer-task-control/internal/config"
	"github.com/tigeregit/peer-task-control/internal/dispatch"
	"github.com/tigeregit/peer-task-control/internal/resource"
	"github.com/tigeregit/peer-task-control/internal/taskboard"
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

func (c *Client) Runs() ([]taskboard.Task, error) {
	var out struct {
		Tasks []taskboard.Task `json:"tasks"`
	}
	hc := *c.HTTP
	if hc.Timeout < 15*time.Second {
		hc.Timeout = 15 * time.Second
	}
	tmp := &Client{Base: c.Base, HTTP: &hc}
	err := tmp.get("/api/v1/runs", &out)
	return out.Tasks, err
}

func (c *Client) withTimeout(d time.Duration) *Client {
	if c.HTTP != nil && c.HTTP.Timeout >= d {
		return c
	}
	hc := http.Client{Timeout: d}
	if c.HTTP != nil {
		hc = *c.HTTP
		hc.Timeout = d
	}
	return &Client{Base: c.Base, HTTP: &hc}
}

func (c *Client) Resources() (resource.List, error) {
	var out resource.List
	err := c.withTimeout(20*time.Second).get("/api/v1/resources", &out)
	return out, err
}

func (c *Client) Resource(digest string) (resource.Info, error) {
	hex := resource.Hex(digest)
	if hex == "" {
		return resource.Info{}, fmt.Errorf("invalid digest")
	}
	var out resource.Info
	err := c.withTimeout(20*time.Second).get("/api/v1/resources/sha256/"+hex, &out)
	return out, err
}

func (c *Client) ResourceContent(digest string) (resource.Blob, error) {
	hex := resource.Hex(digest)
	if hex == "" {
		return resource.Blob{}, fmt.Errorf("invalid digest")
	}
	hc := http.Client{Timeout: 5 * time.Minute}
	if c.HTTP != nil {
		hc = *c.HTTP
		hc.Timeout = 5 * time.Minute
	}
	resp, err := hc.Get(c.Base + "/api/v1/resources/sha256/" + hex + "/content")
	if err != nil {
		return resource.Blob{}, err
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resource.Blob{}, fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(body))
	}
	name := resp.Header.Get("Content-Disposition")
	filename := ""
	if i := indexFilename(name); i != "" {
		filename = i
	}
	ct := resp.Header.Get("Content-Type")
	var size int64
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		fmt.Sscanf(cl, "%d", &size)
	}
	kind := "blob"
	if ct == "application/x-tar" || (filename != "" && len(filename) >= 4 && filename[len(filename)-4:] == ".tar") {
		kind = "tree"
	}
	return resource.Blob{
		Digest:      resource.Normalize(digest),
		Name:        filename,
		Kind:        kind,
		Size:        size,
		ContentType: ct,
		Body:        resp.Body,
	}, nil
}

func indexFilename(disp string) string {
	const key = "filename="
	i := strings.Index(strings.ToLower(disp), key)
	if i < 0 {
		return ""
	}
	rest := strings.TrimSpace(disp[i+len(key):])
	if strings.HasPrefix(rest, "\"") {
		rest = rest[1:]
		if j := strings.IndexByte(rest, '"'); j >= 0 {
			rest = rest[:j]
		}
		return rest
	}
	if j := strings.IndexByte(rest, ';'); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

func (c *Client) Queue() ([]dispatch.QueueItem, error) {
	var out struct {
		Jobs []dispatch.QueueItem `json:"jobs"`
	}
	err := c.withTimeout(15*time.Second).get("/api/v1/queue", &out)
	return out.Jobs, err
}

func (c *Client) Cancel(id string) error {
	var out map[string]string
	return c.withTimeout(15*time.Second).post("/api/v1/cancel", map[string]string{"id": id}, &out)
}

// Exec posts a bundle to the local daemon, which dispatches it over the mesh.
// When follow is true, ev is called for each NDJSON event; the last done/error
// event is returned.
func (c *Client) Exec(ctx context.Context, target string, bundle []byte, follow bool, ev func(dispatch.Event)) (dispatch.Event, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	q := "/api/v1/exec?target=" + url.QueryEscape(target)
	if follow {
		q += "&follow=1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+q, bytes.NewReader(bundle))
	if err != nil {
		return dispatch.Event{}, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	hc := http.Client{Timeout: 0} // follow may last as long as the job
	if !follow && c.HTTP != nil && c.HTTP.Timeout > 0 {
		hc.Timeout = c.HTTP.Timeout
		if hc.Timeout < 2*time.Minute {
			hc.Timeout = 2 * time.Minute
		}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return dispatch.Event{}, err
	}
	defer resp.Body.Close()
	if !follow {
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 300 {
			return dispatch.Event{}, fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(body))
		}
		var ev dispatch.Event
		if err := json.Unmarshal(body, &ev); err != nil {
			return dispatch.Event{}, err
		}
		return ev, nil
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return dispatch.Event{}, fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(body))
	}
	br := bufio.NewReader(resp.Body)
	var last dispatch.Event
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			var e dispatch.Event
			if json.Unmarshal(line, &e) == nil {
				last = e
				if ev != nil {
					ev(e)
				}
				if e.Type == "done" || e.Type == "error" {
					if e.Type == "error" || (e.Status != "" && e.Status != dispatch.StatusSucceeded) {
						msg := e.Error
						if msg == "" {
							msg = e.Status
						}
						return e, fmt.Errorf("%s", msg)
					}
					return e, nil
				}
			}
		}
		if err == io.EOF {
			if last.Type != "" {
				return last, nil
			}
			return last, fmt.Errorf("follow ended without a done event")
		}
		if err != nil {
			return last, err
		}
	}
}
