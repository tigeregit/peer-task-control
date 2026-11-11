package node

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tigeregit/peer-task-control/internal/cas"
	"github.com/tigeregit/peer-task-control/internal/clean"
	"github.com/tigeregit/peer-task-control/internal/quota"
	"github.com/tigeregit/peer-task-control/internal/resource"
	"github.com/tigeregit/peer-task-control/internal/retention"
)

func storagePair(t *testing.T) (*Node, *Node, context.Context) {
	t.Helper()
	a := startTestNode(t, "collector-a", "127.0.0.1", false, []string{"127.0.0.0/8"})
	b := startTestNode(t, "collector-b", "127.0.0.2", false, []string{"127.0.0.0/8"})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	if err := a.Probe(ctx, listenHostPort(b)); err != nil {
		t.Fatal(err)
	}
	return a, b, ctx
}

func storageSeed(t *testing.T, n *Node, name string, pin, output bool) resource.Seed {
	t.Helper()
	path := filepath.Join(n.Cfg.PackagesDir(), name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "asset"), bytes.Repeat([]byte(name), 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	options := resource.CommitOpts{Pin: pin}
	if output {
		success := true
		options.RunOK = &success
	}
	s, err := resource.CommitTree(path, name, time.Now(), options)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func verifyStored(t *testing.T, n *Node, digest string) bool {
	t.Helper()
	_, path, _, ok := resource.FindByDigest(n.Cfg.PackagesDir(), n.Cfg.CASDir(), digest)
	if !ok {
		return false
	}
	got, _, err := cas.DirDigest(path)
	if err != nil || got != digest {
		t.Fatalf("surviving copy is corrupt: %s, %v", got, err)
	}
	return true
}

func TestConcurrentCollectionPreservesLastVerifiedReplica(t *testing.T) {
	a, b, ctx := storagePair(t)
	seed := storageSeed(t, a, "shared", false, false)
	storageSeed(t, b, "shared", false, false)
	a.indexLocalCIDs()
	b.indexLocalCIDs()
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, n := range []*Node{a, b} {
		wg.Add(1)
		go func(n *Node) {
			defer wg.Done()
			<-start
			_ = n.collectSeeds(ctx, 1, math.MaxInt64)
		}(n)
	}
	close(start)
	wg.Wait()
	if !verifyStored(t, a, seed.Digest) && !verifyStored(t, b, seed.Digest) {
		t.Fatal("simultaneous cleanup deleted every replica")
	}
	if err := a.recoverRetention(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.recoverRetention(ctx); err != nil {
		t.Fatal(err)
	}
	_ = a.collectSeeds(ctx, 1, math.MaxInt64)
	_ = b.collectSeeds(ctx, 1, math.MaxInt64)
	copies := 0
	for _, n := range []*Node{a, b} {
		if verifyStored(t, n, seed.Digest) {
			copies++
		}
	}
	if copies != 1 {
		t.Fatalf("cleanup did not make safe progress; copies=%d", copies)
	}
}

func TestCollectionTreatsOutputsAsOrdinarySeedsAndProtectsActiveData(t *testing.T) {
	a, b, ctx := storagePair(t)
	unique := storageSeed(t, a, "unique", false, false)
	pinned := storageSeed(t, a, "pinned", true, false)
	active := storageSeed(t, a, "active", false, false)
	output := storageSeed(t, a, "output", false, true)
	for _, name := range []string{"pinned", "active", "output"} {
		storageSeed(t, b, name, false, false)
	}
	guard, err := quota.Start(a.Cfg.SeedsDir(), quota.Reservation{ID: "active-job", Digests: []string{active.Digest}})
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	before, err := quota.SeedUsage(a.Cfg.SeedsDir())
	if err != nil {
		t.Fatal(err)
	}
	goal := int64(len("output") * 4096)
	if err := a.collectSeeds(ctx, goal, math.MaxInt64); err != nil {
		t.Fatal(err)
	}
	after, err := quota.SeedUsage(a.Cfg.SeedsDir())
	if err != nil || before-after < goal {
		t.Fatalf("minimum deletion target not met: freed=%d goal=%d err=%v", before-after, goal, err)
	}
	if verifyStored(t, a, output.Digest) || !verifyStored(t, b, output.Digest) {
		t.Fatal("output did not follow ordinary duplicate-seed cleanup rules")
	}
	for _, s := range []resource.Seed{unique, pinned, active} {
		if !verifyStored(t, a, s.Digest) {
			t.Fatalf("protected seed removed: %s", s.Name)
		}
	}
	if err := a.collectSeeds(ctx, 1, math.MaxInt64); err == nil {
		t.Fatal("claimed to free unique, pinned or active data")
	}
}

func TestRetentionSurvivesRestartAndFencesDelayedHolds(t *testing.T) {
	a, b, ctx := storagePair(t)
	seed := storageSeed(t, a, "retained", false, false)
	storageSeed(t, b, "retained", false, false)
	record := retention.Record{Token: strings.Repeat("a", 32), Peer: b.Host.ID().String(), Digest: seed.Digest}
	lock, err := retention.Lock(a.Cfg.SeedsDir())
	if err != nil {
		t.Fatal(err)
	}
	err = retention.Save(a.Cfg.SeedsDir(), record, true)
	lock.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.sendRetention(ctx, record, "hold"); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Start(context.Background(), b.Cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.Close() })
	if held, err := retention.Held(restarted.Cfg.SeedsDir(), seed.Digest); err != nil || !held {
		t.Fatalf("promise lost at restart: %v, %v", held, err)
	}
	plans, err := clean.PlanResources(restarted.Cfg, clean.Options{IDs: []string{seed.Digest}, Force: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].Allowed {
		t.Fatal("cleanup preview did not show the durable promise")
	}
	plans[0].Allowed = true
	if _, err := clean.Apply(plans); err == nil {
		t.Fatal("manual --force bypassed a durable replica promise")
	}
	if err := a.Probe(ctx, listenHostPort(restarted)); err != nil {
		t.Fatal(err)
	}
	if err := a.recoverRetention(ctx); err != nil {
		t.Fatal(err)
	}
	if held, err := retention.Held(restarted.Cfg.SeedsDir(), seed.Digest); err != nil || held {
		t.Fatalf("recovery did not release orphan transaction: %v, %v", held, err)
	}
	if err := a.sendRetention(ctx, record, "hold"); err == nil {
		t.Fatal("a delayed hold resurrected a released transaction")
	}
}

func TestBadOrLegacyKeeperCannotAuthorizeCleanup(t *testing.T) {
	a, b, ctx := storagePair(t)
	s := storageSeed(t, a, "replica", false, false)
	storageSeed(t, b, "replica", false, false)
	b.Host.RemoveStreamHandler(ProtocolRetention)
	if err := a.collectSeeds(ctx, 1, math.MaxInt64); err == nil {
		t.Fatal("legacy peer authorized deletion")
	}
	if !verifyStored(t, a, s.Digest) {
		t.Fatal("removed source without a durable acknowledgement")
	}
	b.Host.SetStreamHandler(ProtocolRetention, b.handleRetention)
	if err := a.recoverRetention(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.Cfg.PackagesDir(), "replica", "asset"), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.collectSeeds(ctx, 1, math.MaxInt64); err == nil || !verifyStored(t, a, s.Digest) {
		t.Fatalf("trusted catalog metadata instead of verifying keeper bytes: %v", err)
	}
}
