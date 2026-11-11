package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tigeregit/peer-task-control/internal/control"
	"github.com/tigeregit/peer-task-control/internal/version"
)

func TestMeshUpdateJSONFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req control.MeshUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Version != version.Version {
			t.Errorf("default mesh version = %q, want %s: %v", req.Version, version.Version, err)
		}
		_ = json.NewEncoder(w).Encode(control.MeshUpdateReport{Version: "0.3.1",
			Nodes: []control.MeshUpdateNode{{Name: "worker", Status: "failed", Detail: "did not rejoin"}}})
	}))
	defer srv.Close()
	oldAPI := apiAddr
	defer func() { apiAddr = oldAPI }()
	var out bytes.Buffer
	cmd := rootCmd()
	cmd.SetOut(&out)
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--api", strings.TrimPrefix(srv.URL, "http://"), "update", "--mesh", "--json"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("JSON mode hid a failed update")
	}
	var report control.MeshUpdateReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || len(report.Nodes) != 1 || report.Nodes[0].Status != "failed" {
		t.Fatalf("invalid JSON result: %s, %v", out.String(), err)
	}
}
