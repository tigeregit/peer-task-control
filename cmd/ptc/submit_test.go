package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tigeregit/peer-task-control/internal/catalog"
	"github.com/tigeregit/peer-task-control/internal/config"
	"github.com/tigeregit/peer-task-control/internal/control"
	"github.com/tigeregit/peer-task-control/internal/dispatch"
	"github.com/tigeregit/peer-task-control/internal/workflow"
)

func TestRemoteSubmitFetchResultOutput(t *testing.T) {
	loaded, err := workflow.LoadJob(filepath.Join("..", "..", "testdata", "submit", "workflow.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	if err := tw.WriteHeader(&tar.Header{Name: "result.txt", Mode: 0o644, Size: 6}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("result")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, tc := range []struct {
		name string
		json bool
		fail bool
	}{
		{name: "json success", json: true},
		{name: "json fetch failure", json: true, fail: true},
		{name: "human success"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/v1/status":
					_ = json.NewEncoder(w).Encode(control.Status{Peers: []catalog.Entry{{Name: "worker", PeerID: "peer-worker"}}})
				case r.URL.Path == "/api/v1/exec":
					_ = json.NewEncoder(w).Encode(dispatch.Event{Type: "done", Status: dispatch.StatusSucceeded, RunID: "run-1", Digest: digest})
				case strings.HasSuffix(r.URL.Path, "/content"):
					if tc.fail {
						http.Error(w, "unavailable", http.StatusServiceUnavailable)
					} else {
						_, _ = w.Write(archive.Bytes())
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			oldAPI := apiAddr
			apiAddr = strings.TrimPrefix(srv.URL, "http://")
			defer func() { apiAddr = oldAPI }()
			var stdout, stderr bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			dir := t.TempDir()
			cfg := config.Defaults()
			cfg.DataDir = t.TempDir()
			err := submitRemote(cmd, loaded, cfg, "worker", false, tc.json, dispatch.DefaultInlineMax, dir)
			if (err != nil) != tc.fail {
				t.Fatalf("error=%v; stdout=%s stderr=%s", err, stdout.String(), stderr.String())
			}
			if tc.json {
				var output struct {
					Event dispatch.Event `json:"event"`
					Error string         `json:"error"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
					t.Fatalf("stdout is not one JSON object: %q: %v", stdout.String(), err)
				}
				if output.Event.RunID != "run-1" || (output.Error != "") != tc.fail {
					t.Fatalf("incorrect output: %+v", output)
				}
				if !tc.fail && (!strings.Contains(stderr.String(), "fetched ") || strings.Contains(stdout.String(), "fetched ")) {
					t.Fatalf("fetch progress is not isolated: stdout=%s stderr=%s", stdout.String(), stderr.String())
				}
			} else if !strings.Contains(stdout.String(), "fetched ") {
				t.Fatalf("missing human-readable fetch progress: %s", stdout.String())
			}
			if !tc.fail {
				if b, err := os.ReadFile(filepath.Join(dir, "result.txt")); err != nil || string(b) != "result" {
					t.Fatalf("downloaded result: %q, %v", b, err)
				}
			}
		})
	}
}

func TestLocalSubmitFetchResultNeedsDaemonBeforeRunning(t *testing.T) {
	data := t.TempDir()
	cfgFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgFile, []byte("name: t\ndata_dir: "+filepath.ToSlash(data)+"\ntrusted_cidrs: [127.0.0.0/8]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldCfg, oldAPI, oldData := cfgPath, apiAddr, dataDir
	cfgPath, apiAddr, dataDir = cfgFile, "", ""
	defer func() { cfgPath, apiAddr, dataDir = oldCfg, oldAPI, oldData }()
	cmd := submitCmd()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--workflow", filepath.Join("..", "..", "testdata", "submit", "workflow.yaml"), "--fetch-result", t.TempDir()})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--fetch-result needs a running ptc daemon") {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(data, "runs")); len(entries) != 0 {
		t.Fatalf("job ran before rejecting --fetch-result: %d run records", len(entries))
	}
}
