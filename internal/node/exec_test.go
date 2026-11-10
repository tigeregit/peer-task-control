package node

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/peer-task-control/ptc/internal/dispatch"
	"github.com/peer-task-control/ptc/internal/resource"
	"github.com/peer-task-control/ptc/internal/taskboard"
	"github.com/peer-task-control/ptc/internal/workflow"
)

func TestDispatchSubmitFixture(t *testing.T) {
	if runtime.GOOS == "windows" {
		// The Linux fixture uses bash; the Windows workflow is covered live.
	}
	cidrs := []string{"127.0.0.1/32", "127.0.0.2/32"}
	a := startTestNode(t, "submitter", "127.0.0.1", false, cidrs)
	b := startTestNode(t, "worker-b", "127.0.0.2", false, cidrs)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := a.Probe(ctx, listenHostPort(b)); err != nil {
		t.Fatal(err)
	}
	waitPeers(t, a, 2)

	name := "workflow.yaml"
	if runtime.GOOS == "windows" {
		name = "workflow.windows.yaml"
	}
	loaded, err := workflow.LoadJob("../../testdata/submit/"+name, "")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := dispatch.Build(loaded, dispatch.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	target, ok := a.FindPeer("worker-b")
	if !ok {
		t.Fatal("worker-b not in catalog")
	}
	if err := dispatch.CheckPlacement(loaded.Prepare.Target, target); err != nil {
		t.Fatal(err)
	}
	sub := taskboard.Party{Node: a.Cfg.Name, PeerID: a.Host.ID().String()}
	ev, err := a.Dispatch(ctx, target, bundle.Data, true, sub, nil)
	if err != nil {
		t.Fatalf("dispatch: %v (ev=%+v)", err, ev)
	}
	if ev.Status != dispatch.StatusSucceeded || ev.Digest == "" {
		t.Fatalf("ev=%+v", ev)
	}
	// result is on the executor and should be fetchable from the submitter
	deadline := time.Now().Add(10 * time.Second)
	var info resource.Info
	for time.Now().Before(deadline) {
		info, err = a.Resource(ctx, ev.Digest)
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("result not visible on submitter: %v", err)
	}
	blob, err := a.OpenResource(ctx, ev.Digest)
	if err != nil {
		t.Fatal(err)
	}
	blob.Body.Close()
	if info.Digest != ev.Digest {
		t.Fatalf("digest %s", info.Digest)
	}

	// run appears in shared runs
	var found bool
	for _, task := range a.CollectRuns(ctx) {
		if task.ID == ev.RunID {
			found = true
			if task.Node != "worker-b" && task.Target != "worker-b" {
				t.Fatalf("run not stamped with executor: %+v", task)
			}
			if task.Submitter == nil || task.Submitter.Node != "submitter" {
				t.Fatalf("missing submitter: %+v", task)
			}
		}
	}
	if !found {
		t.Fatalf("run %s not in collect runs", ev.RunID)
	}
}

func TestDispatchRequireNVIDIARefused(t *testing.T) {
	cidrs := []string{"127.0.0.1/32", "127.0.0.2/32"}
	a := startTestNode(t, "submitter", "127.0.0.1", false, cidrs)
	gw := startTestNode(t, "gateway", "127.0.0.2", true, cidrs)
	gw.Store.SetService("exec", false)
	_ = gw.PersistPolicy()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := a.Probe(ctx, listenHostPort(gw)); err != nil {
		t.Fatal(err)
	}
	waitPeers(t, a, 2)
	target, ok := a.FindPeer("gateway")
	if !ok {
		t.Fatal("gateway not found")
	}
	req := true
	deny := true
	err := dispatch.CheckPlacement(workflow.Target{RequireNVIDIA: &req, DenyNonExec: &deny}, target)
	if err == nil {
		t.Fatal("expected placement refusal for require_nvidia on gateway")
	}
}
