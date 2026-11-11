package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tigeregit/peer-task-control/internal/control"
)

func TestScanCommandForwardsTargetAndReportsRejections(t *testing.T) {
	for _, status := range []string{"accepted", "rate_limited", "busy", "denied"} {
		t.Run(status, func(t *testing.T) {
			reply := control.ScanResponse{Node: "gateway", PeerID: "relay-peer", Status: status}
			if status != "accepted" {
				reply.RetryAfterSeconds, reply.Error = 60, "scan refused"
			}

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req control.ScanRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Node != "gateway" || r.Method != http.MethodPost || r.URL.Path != "/api/v1/scan" {
					t.Errorf("incorrect scan request: %+v, %v", req, err)
				}
				w.WriteHeader(reply.HTTPStatus())
				_ = json.NewEncoder(w).Encode(reply)
			}))
			defer server.Close()
			oldAPI := apiAddr
			apiAddr = strings.TrimPrefix(server.URL, "http://")
			defer func() { apiAddr = oldAPI }()
			command := scanCmd()
			command.SilenceUsage, command.SilenceErrors = true, true
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetArgs([]string{"--node", "gateway", "--json"})
			err := command.Execute()
			if (err != nil) != (status != "accepted") {
				t.Fatalf("incorrect exit status: %v", err)
			}
			var got control.ScanResponse
			if err := json.Unmarshal(output.Bytes(), &got); err != nil || got != reply {
				t.Fatalf("missing structured scan response: %s, %v", output.String(), err)
			}
		})
	}
}

func TestScanCommandReportsUnsupportedDaemon(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	oldAPI := apiAddr
	apiAddr = strings.TrimPrefix(server.URL, "http://")
	defer func() { apiAddr = oldAPI }()
	command := scanCmd()
	command.SilenceUsage, command.SilenceErrors = true, true
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"--json"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "404") || output.Len() != 0 {
		t.Fatalf("unsupported daemon was not reported explicitly: %v, %s", err, output.String())
	}
}
