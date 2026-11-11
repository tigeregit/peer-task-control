package node

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/tigeregit/peer-task-control/internal/dispatch"
	"github.com/tigeregit/peer-task-control/internal/resource"
	"github.com/tigeregit/peer-task-control/internal/taskboard"
	"github.com/tigeregit/peer-task-control/internal/workflow"
)

func TestSeedOnlyDispatchToFilesNode(t *testing.T) {
	cidrs := []string{"127.0.0.1/32", "127.0.0.2/32"}
	a := startTestNode(t, "origin", "127.0.0.1", false, cidrs)
	b := startTestNode(t, "replica", "127.0.0.2", false, cidrs)
	b.Store.SetService("exec", false)
	if err := b.PersistPolicy(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := a.Probe(ctx, listenHostPort(b)); err != nil {
		t.Fatal(err)
	}
	waitPeers(t, a, 2)
	loaded, err := workflow.LoadJob(filepath.Join("..", "..", "testdata", "seed", "workflow.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}
	pub, err := resource.PublishTreeTo(a.Cfg.PackagesDir(), "origin-scene", filepath.Join("..", "..", "testdata", "submit", "scene"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	loaded.Prepare.Seeds = []workflow.Seed{{Name: "scene-replica", Digest: pub.Digest}}
	// Bundle reloads the YAML on the target, so write the digest document as well.
	prep := filepath.Join(t.TempDir(), "seeds.yaml")
	doc := "apiVersion: ptc/v0\nkind: Seed\nseeds:\n  - name: scene-replica\n    digest: " + pub.Digest + "\n"
	if err := os.WriteFile(prep, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded.PreparePath = prep
	bundle, err := dispatch.Build(loaded, dispatch.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	target := b.LocalEntry()
	if err := dispatch.CheckJobPlacement(loaded, target); err != nil {
		t.Fatal(err)
	}
	ev, err := a.Dispatch(ctx, target, bundle.Data, true, taskboard.Party{Node: a.Cfg.Name, PeerID: a.Host.ID().String()}, nil)
	if err != nil || ev.Status != dispatch.StatusSucceeded {
		t.Fatalf("seed dispatch: %+v, %v", ev, err)
	}
	if _, err := os.Stat(b.Cfg.WorktreesDir()); !os.IsNotExist(err) {
		t.Fatalf("seed task created worktrees: %v", err)
	}
	if _, ok := resource.Lookup(b.localResourceInfos(), pub.Digest); !ok {
		t.Fatal("replica not advertised")
	}
	normal, err := workflow.LoadJob(filepath.Join("..", "..", "testdata", "submit", "workflow.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}
	normalBundle, err := dispatch.Build(normal, dispatch.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Dispatch(ctx, target, normalBundle.Data, false, taskboard.Party{Node: a.Cfg.Name, PeerID: a.Host.ID().String()}, nil); err == nil {
		t.Fatal("files permission allowed normal execution with exec disabled")
	}
	b.Store.SetService("files", false)
	if _, err := a.Dispatch(ctx, target, bundle.Data, false, taskboard.Party{Node: a.Cfg.Name, PeerID: a.Host.ID().String()}, nil); err == nil {
		t.Fatal("files-disabled node accepted seed-only task")
	}
	b.Store.SetService("files", true)
	// The replica must remain usable without the originating peer.
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reader := startTestNode(t, "reader", "127.0.0.1", false, cidrs)
	if err := reader.Probe(ctx, listenHostPort(b)); err != nil {
		t.Fatal(err)
	}
	blob, err := reader.OpenResource(ctx, pub.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, blob.Body); err != nil {
		t.Fatal(err)
	}
	blob.Body.Close()
	for _, task := range b.CollectRuns(ctx) {
		if task.ID == ev.RunID {
			if task.Worktree != nil || len(task.Jobs[0].Steps) != 1 || task.Jobs[0].Steps[0].ID != "seed" {
				t.Fatalf("seed-only task executed: %+v", task)
			}
			return
		}
	}
	t.Fatal("missing seed-only run record")
}
func TestDispatchSubmitFixture(t *testing.T) {
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
