package node

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tigeregit/peer-task-control/internal/control"
	"github.com/tigeregit/peer-task-control/internal/resource"
)

func TestRemoteCleanDeniedWhenDisabled(t *testing.T) {
	a, b, ctx := storagePair(t)
	reply, err := a.Clean(ctx, control.CleanRequest{Node: b.Cfg.Name, Kind: "worktrees", DryRun: true})
	if err == nil || reply.Status != "denied" || !strings.Contains(err.Error(), "policy.remote_clean") {
		t.Fatalf("disabled remote clean: %+v, %v", reply, err)
	}
}

func TestRemoteCleanUnknownNode(t *testing.T) {
	a := startTestNode(t, "clean-local", "127.0.0.1", false, []string{"127.0.0.0/8"})
	reply, err := a.Clean(context.Background(), control.CleanRequest{Node: "missing", Kind: "worktrees", DryRun: true})
	if err == nil || reply.Status != "not_found" {
		t.Fatalf("missing node: %+v, %v", reply, err)
	}
}

func TestRemoteCleanUnsupportedProtocol(t *testing.T) {
	a, b, ctx := storagePair(t)
	b.Cfg.Policy.RemoteClean = true
	b.Host.RemoveStreamHandler(ProtocolClean)
	_, err := a.Clean(ctx, control.CleanRequest{Node: b.Cfg.Name, Kind: "worktrees", DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "does not support remote clean") {
		t.Fatalf("unsupported peer: %v", err)
	}
}

func TestRemoteCleanWorktreesWhenEnabled(t *testing.T) {
	a, b, ctx := storagePair(t)
	b.Cfg.Policy.RemoteClean = true
	reply, err := a.Clean(ctx, control.CleanRequest{Node: b.Cfg.Name, Kind: "worktrees", DryRun: true})
	if err != nil || reply.Status != "ok" || reply.Node != b.Cfg.Name {
		t.Fatalf("enabled remote clean: %+v, %v", reply, err)
	}
}

func TestRemoteCleanUniqueRequiresForce(t *testing.T) {
	a, b, ctx := storagePair(t)
	b.Cfg.Policy.RemoteClean = true
	seed := storageSeed(t, b, "unique-only", false, false)
	refused, err := a.Clean(ctx, control.CleanRequest{Node: b.Cfg.Name, Kind: "resource", IDs: []string{seed.Digest}, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(refused.Plans) != 1 || refused.Plans[0].Allowed || !strings.Contains(refused.Plans[0].Reason, "unique on the network") {
		t.Fatalf("unique resource should be refused: %+v", refused)
	}
	if resource.FindPackageDirAt(b.Cfg.PackagesDir(), seed.Digest) == "" {
		t.Fatal("unique resource was deleted without --force")
	}
	forced, err := a.Clean(ctx, control.CleanRequest{Node: b.Cfg.Name, Kind: "resource", IDs: []string{seed.Digest}, Yes: true, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(forced.Plans) != 1 || !forced.Plans[0].Allowed || forced.Freed == 0 {
		t.Fatalf("unique resource should be cleaned with --force: %+v", forced)
	}
	if resource.FindPackageDirAt(b.Cfg.PackagesDir(), seed.Digest) != "" {
		t.Fatal("forced unique resource is still present")
	}
}

func TestRemoteCleanReplicaAllowedWithoutForce(t *testing.T) {
	a, b, ctx := storagePair(t)
	b.Cfg.Policy.RemoteClean = true
	seed := storageSeed(t, a, "shared-clean", false, false)
	storageSeed(t, b, "shared-clean", false, false)
	deadline := time.Now().Add(10 * time.Second)
	for {
		infos := b.CollectResources(ctx)
		info, ok := resource.Lookup(infos, seed.Digest)
		if ok && len(info.Locations) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("replica was not visible on the mesh: ok=%v info=%+v", ok, info)
		}
		time.Sleep(100 * time.Millisecond)
	}
	reply, err := a.Clean(ctx, control.CleanRequest{Node: b.Cfg.Name, Kind: "resource", IDs: []string{seed.Digest}, Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Plans) != 1 || reply.Plans[0].Unique || !reply.Plans[0].Allowed || reply.Freed == 0 {
		t.Fatalf("non-unique resource should be cleaned without --force: %+v", reply)
	}
	if resource.FindPackageDirAt(b.Cfg.PackagesDir(), seed.Digest) != "" {
		t.Fatal("replica on the target was not deleted")
	}
	if resource.FindPackageDirAt(a.Cfg.PackagesDir(), seed.Digest) == "" {
		t.Fatal("cleaning a replica deleted the other copy")
	}
}
