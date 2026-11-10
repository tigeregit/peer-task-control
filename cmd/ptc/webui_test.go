package main

import (
	"errors"
	"testing"
	"time"

	"github.com/peer-task-control/ptc/internal/catalog"
	"github.com/peer-task-control/ptc/internal/control"
)

func TestMeshWatchFailure(t *testing.T) {
	started := time.Date(2026, 11, 10, 22, 50, 0, 0, time.FixedZone("CST", 8*3600))
	now := started.Add(2 * time.Second)
	got := meshWatchTask(control.Status{}, errors.New("connection refused"), started, now)
	if got.Status != "failure" || got.ID != "mesh-watch" {
		t.Fatalf("status=%s id=%s", got.Status, got.ID)
	}
	if len(got.Jobs) != 1 || got.Jobs[0].Status != "failure" {
		t.Fatalf("jobs=%+v", got.Jobs)
	}
}

func TestMergeBoardPutsLiveFirst(t *testing.T) {
	started := time.Date(2026, 11, 10, 22, 50, 0, 0, time.FixedZone("CST", 8*3600))
	now := started.Add(5 * time.Second)
	st := control.Status{
		Name:    "gateway",
		PeerID:  "12D3KooWgateway",
		Version: "0.1.0-p0",
		Listen:  []string{"/ip4/10.87.0.2/tcp/18765"},
		Peers: []catalog.Entry{
			{
				PeerID: "12D3KooWgateway", Name: "gateway",
				Addrs:    []string{"/ip4/10.87.0.2/tcp/18765"},
				Services: map[string]catalog.ServiceState{"exec": {Reason: "this node has no NVIDIA GPU"}, "files": {Offered: true, Enabled: true}, "relay": {Offered: true, Enabled: true}},
				Caps:     catalog.Caps{OS: "linux", Arch: "amd64"},
			},
			{
				PeerID: "12D3KooWworkera", Name: "worker-a",
				Addrs:    []string{"/ip4/10.87.0.10/tcp/18765"},
				Services: map[string]catalog.ServiceState{"exec": {Offered: true, Enabled: true}, "files": {Offered: true, Enabled: true}, "relay": {Reason: "disabled"}},
				Caps:     catalog.Caps{NVIDIA: true, GPUName: "NVIDIA GeForce RTX 4090", VRAMMiB: 24564, Driver: "560.00", OS: "linux", Arch: "amd64"},
			},
			{
				PeerID: "12D3KooWworkerb", Name: "worker-b",
				Addrs:    []string{"/ip4/10.87.1.10/tcp/18765"},
				Services: map[string]catalog.ServiceState{"exec": {Offered: true, Enabled: true}, "files": {Offered: true, Enabled: true}, "relay": {Reason: "disabled"}},
				Caps:     catalog.Caps{NVIDIA: true, GPUName: "NVIDIA GeForce RTX 4090", VRAMMiB: 24564, Driver: "560.00", OS: "linux", Arch: "amd64"},
			},
		},
	}
	static := []Task{{ID: "mesh-watch", Name: Text{EN: "should be replaced"}}, {ID: "recorded", Name: Text{EN: "recorded"}, Status: "success"}}
	board := mergeBoard(static, nil, st, nil, started, now)
	if len(board.Tasks) != 2 {
		t.Fatalf("len=%d", len(board.Tasks))
	}
	live := board.Tasks[0]
	if live.ID != "mesh-watch" || live.Status != "in_progress" || live.Source != "live" {
		t.Fatalf("live=%+v", live)
	}
	if live.Jobs[1].Status != "success" { // catalog converged
		t.Fatalf("catalog job status=%s", live.Jobs[1].Status)
	}
	if live.Jobs[2].Status != "in_progress" {
		t.Fatalf("roles job status=%s", live.Jobs[2].Status)
	}
	if board.Tasks[1].ID != "recorded" {
		t.Fatalf("second=%s", board.Tasks[1].ID)
	}
}
