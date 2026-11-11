package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tigeregit/peer-task-control/internal/clean"
	"github.com/tigeregit/peer-task-control/internal/control"
)

func TestCleanCommandForwardsNodeAndReportsResult(t *testing.T) {
	for _, status := range []string{"ok", "denied", "not_found"} {
		t.Run(status, func(t *testing.T) {
			reply := control.CleanResponse{
				Node:   "gpu-1",
				PeerID: "peer-1",
				Status: status,
				DryRun: true,
				Plans:  []clean.Plan{{Kind: clean.Resources, ID: "sha256:abc", Allowed: status == "ok", Bytes: 12}},
				Bytes:  12,
			}
			if status != "ok" {
				reply.Error = "clean refused"
				reply.Plans = nil
				reply.Bytes = 0
			}
			var got control.CleanRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/clean" {
					t.Errorf("incorrect clean route: %s %s", r.Method, r.URL.Path)
				}
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Errorf("decode: %v", err)
				}
				w.WriteHeader(reply.HTTPStatus())
				_ = json.NewEncoder(w).Encode(reply)
			}))
			defer server.Close()
			oldAPI := apiAddr
			apiAddr = strings.TrimPrefix(server.URL, "http://")
			defer func() { apiAddr = oldAPI }()
			command := cleanCmd()
			command.SilenceUsage, command.SilenceErrors = true, true
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetArgs([]string{"--node", "gpu-1", "resource", "sha256:abc", "--dry-run", "--json"})
			err := command.Execute()
			if (err != nil) != (status != "ok") {
				t.Fatalf("incorrect exit status: %v", err)
			}
			if got.Node != "gpu-1" || got.Kind != "resource" || got.DryRun != true || len(got.IDs) != 1 || got.IDs[0] != "sha256:abc" || got.Yes {
				t.Fatalf("request not forwarded: %+v", got)
			}
			var parsed control.CleanResponse
			if err := json.Unmarshal(output.Bytes(), &parsed); err != nil || parsed.Status != status || parsed.Node != "gpu-1" {
				t.Fatalf("missing structured clean response: %s, %v", output.String(), err)
			}
		})
	}
}

func TestCleanCommandReportsUnsupportedDaemon(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	oldAPI := apiAddr
	apiAddr = strings.TrimPrefix(server.URL, "http://")
	defer func() { apiAddr = oldAPI }()
	command := cleanCmd()
	command.SilenceUsage, command.SilenceErrors = true, true
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"--node", "gpu-1", "worktrees", "--dry-run", "--json"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "does not support remote clean") {
		t.Fatalf("unsupported daemon was not reported explicitly: %v, %s", err, output.String())
	}
}

func TestCleanHelpDocumentsNodeAndForce(t *testing.T) {
	command := cleanCmd()
	command.SilenceUsage, command.SilenceErrors = true, true
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"resource", "--help"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{"--node", "node name or peer ID", "--force", "only network copy"} {
		if !strings.Contains(text, want) {
			t.Errorf("help missing %q\n%s", want, text)
		}
	}
}
