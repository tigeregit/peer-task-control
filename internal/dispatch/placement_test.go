package dispatch

import (
	"strings"
	"testing"

	"github.com/tigeregit/peer-task-control/internal/catalog"
	"github.com/tigeregit/peer-task-control/internal/workflow"
)

func TestCheckPlacementRefuseNoGPU(t *testing.T) {
	req := true
	deny := true
	target := workflow.Target{RequireNVIDIA: &req, DenyNonExec: &deny}
	gw := catalog.Entry{
		Name:   "gateway",
		PeerID: "QmGW",
		Services: map[string]catalog.ServiceState{
			"exec":  {Offered: false, Enabled: false, Reason: "this node has no NVIDIA GPU"},
			"relay": {Offered: true, Enabled: true},
		},
	}
	err := CheckPlacement(target, gw)
	if err == nil || !strings.Contains(err.Error(), "gateway") {
		t.Fatalf("got %v", err)
	}
	worker := catalog.Entry{
		Name:   "worker-b",
		PeerID: "QmWB",
		Services: map[string]catalog.ServiceState{
			"exec": {Offered: false, Enabled: true, Reason: "this node has no NVIDIA GPU"},
		},
	}
	err = CheckPlacement(target, worker)
	if err == nil || !strings.Contains(err.Error(), "NVIDIA") {
		t.Fatalf("got %v", err)
	}
	ok := catalog.Entry{
		Name:   "host",
		PeerID: "QmHost",
		Caps:   catalog.Caps{NVIDIA: true, GPUName: "RTX 5070"},
		Services: map[string]catalog.ServiceState{
			"exec": {Offered: true, Enabled: true},
		},
	}
	if err := CheckPlacement(target, ok); err != nil {
		t.Fatal(err)
	}
}

func TestCheckPlacementDenyNonExec(t *testing.T) {
	off := false
	on := true
	target := workflow.Target{RequireNVIDIA: &off, DenyNonExec: &on}
	n := catalog.Entry{
		Name: "worker-a",
		Services: map[string]catalog.ServiceState{
			"exec": {Offered: false, Enabled: false, Reason: "service exec is disabled on this host"},
		},
	}
	err := CheckPlacement(target, n)
	if err == nil || !strings.Contains(err.Error(), "exec") {
		t.Fatalf("got %v", err)
	}
	// require_nvidia false and deny_non_exec false: any node is fine
	target.DenyNonExec = &off
	if err := CheckPlacement(target, n); err != nil {
		t.Fatal(err)
	}
}

func TestPickAutoSkipsGateway(t *testing.T) {
	req := true
	peers := []catalog.Entry{
		{Name: "gateway", Services: map[string]catalog.ServiceState{"relay": {Enabled: true}, "exec": {Enabled: false, Reason: "this node has no NVIDIA GPU"}}},
		{Name: "worker-b", Services: map[string]catalog.ServiceState{"exec": {Enabled: true, Offered: false, Reason: "this node has no NVIDIA GPU"}}},
		{Name: "host", Caps: catalog.Caps{NVIDIA: true, VRAMMiB: 12288}, Services: map[string]catalog.ServiceState{"exec": {Enabled: true, Offered: true}}},
	}
	e, err := PickAuto(workflow.Target{RequireNVIDIA: &req}, peers)
	if err != nil {
		t.Fatal(err)
	}
	if e.Name != "host" {
		t.Fatalf("picked %s", e.Name)
	}
}

func TestResolveTarget(t *testing.T) {
	peers := []catalog.Entry{{Name: "host", PeerID: "12D3KooWabc"}, {Name: "worker-b", PeerID: "12D3KooWdef"}}
	e, err := ResolveTarget("host", peers)
	if err != nil || e.Name != "host" {
		t.Fatalf("%v %+v", err, e)
	}
	e, err = ResolveTarget("12D3KooWdef", peers)
	if err != nil || e.Name != "worker-b" {
		t.Fatalf("%v %+v", err, e)
	}
	if _, err := ResolveTarget("gateway", peers); err == nil {
		t.Fatal("expected not found")
	}
}

func TestSeedOnlyPlacementRequiresFilesNotExec(t *testing.T) {
	node := catalog.Entry{Name: "replica", Services: map[string]catalog.ServiceState{
		"exec": {Enabled: false}, "files": {Enabled: true, Offered: true},
	}}
	loaded := workflow.Loaded{SeedOnly: true}
	if err := CheckJobPlacement(loaded, node); err != nil {
		t.Fatal(err)
	}
	node.Services["files"] = catalog.ServiceState{Enabled: false, Offered: false}
	if err := CheckJobPlacement(loaded, node); err == nil {
		t.Fatal("seed-only task accepted files-disabled node")
	}
}
