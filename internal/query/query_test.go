package query

import (
	"testing"
	"time"

	"github.com/tigeregit/peer-task-control/internal/resource"
	"github.com/tigeregit/peer-task-control/internal/taskboard"
)

func ts(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return &t
}

func sampleTasks() []taskboard.Task {
	return []taskboard.Task{
		{ID: "run-old", Name: taskboard.Text{ZH: "旧任务", EN: "old job"}, Status: "success", Node: "worker-a", PeerID: "12D3KooWaaa", StartedAt: ts("2026-11-10T10:00:00Z"), EndedAt: ts("2026-11-10T10:01:00Z"), Summary: taskboard.Text{ZH: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
		{ID: "run-cuda", Name: taskboard.Text{ZH: "假 CUDA", EN: "fake cuda"}, Status: "success", Node: "worker-a", PeerID: "12D3KooWaaa", StartedAt: ts("2026-11-10T15:53:26Z"), EndedAt: ts("2026-11-10T15:54:00Z"), Summary: taskboard.Text{EN: "scene-a results"}},
		{ID: "run-fail", Name: taskboard.Text{EN: "broken pack"}, Status: "failure", Node: "worker-b", PeerID: "12D3KooWbbb", StartedAt: ts("2026-11-10T16:00:00Z"), EndedAt: ts("2026-11-10T16:05:00Z")},
		{ID: "run-live", Name: taskboard.Text{ZH: "监视", EN: "watch"}, Status: "in_progress", Node: "gateway", PeerID: "12D3KooWccc", StartedAt: ts("2026-11-10T09:00:00Z")},
		{ID: "run-queue", Name: taskboard.Text{EN: "queued work"}, Status: "queued", Node: "worker-b", PeerID: "12D3KooWbbb"},
	}
}

func TestFilterTasksStatusSearchSort(t *testing.T) {
	all := sampleTasks()

	got := FilterTasks(all, TaskFilter{Status: "failed"})
	if len(got) != 1 || got[0].ID != "run-fail" {
		t.Fatalf("failed filter: %+v", ids(got))
	}

	got = FilterTasks(all, TaskFilter{Status: "in_progress"})
	if len(got) != 2 {
		t.Fatalf("in_progress should include queued, got %v", ids(got))
	}

	got = FilterTasks(all, TaskFilter{Q: "cuda"})
	if len(got) != 1 || got[0].ID != "run-cuda" {
		t.Fatalf("search cuda: %v", ids(got))
	}

	got = FilterTasks(all, TaskFilter{Node: "worker-a"})
	if len(got) != 2 {
		t.Fatalf("node worker-a: %v", ids(got))
	}
	got = FilterTasks(all, TaskFilter{Node: "12D3KooWbbb"})
	if len(got) != 2 {
		t.Fatalf("peer prefix: %v", ids(got))
	}

	got = FilterTasks(all, TaskFilter{Sort: "started", Order: "desc"})
	if got[0].ID != "run-fail" || got[1].ID != "run-cuda" {
		t.Fatalf("started desc: %v", ids(got))
	}
	got = FilterTasks(all, TaskFilter{Sort: "name", Order: "asc"})
	if taskName(got[0]) != "queued work" && got[0].Name.EN != "broken pack" && taskName(got[0]) != "假 CUDA" {
		// just check it's sorted by lowercased display name; first should be "broken pack"
	}
	if taskName(got[0]) != "broken pack" {
		t.Fatalf("name asc first=%q %v", taskName(got[0]), ids(got))
	}
}

func TestFilterResources(t *testing.T) {
	seeds := []resource.Info{
		{Seed: resource.Seed{Digest: "sha256:aaa", Name: "fake-cuda-scene-a-results", Kind: "tree", Size: 33555529, Files: []resource.File{{Path: "out/results/pred.bin"}}}, Locations: []resource.Location{{Node: "worker-a", PeerID: "12D3KooWaaa", Hops: 0}}},
		{Seed: resource.Seed{Digest: "sha256:bbb", Name: "small-blob", Kind: "blob", Size: 128}, Locations: []resource.Location{{Node: "worker-b", PeerID: "12D3KooWbbb", Hops: 1}}},
		{Seed: resource.Seed{Digest: "sha256:ccc", Name: "also-tree", Kind: "tree", Size: 1024}, Locations: []resource.Location{{Node: "worker-a", Hops: 0}, {Node: "worker-b", Hops: 1}}},
	}

	got := FilterResources(seeds, ResourceFilter{Kind: "tree"})
	if len(got) != 2 {
		t.Fatalf("kind tree: %d", len(got))
	}
	got = FilterResources(seeds, ResourceFilter{Q: "pred.bin"})
	if len(got) != 1 || got[0].Name != "fake-cuda-scene-a-results" {
		t.Fatalf("search path: %+v", got)
	}
	got = FilterResources(seeds, ResourceFilter{Node: "worker-b"})
	if len(got) != 2 {
		t.Fatalf("on worker-b: %d", len(got))
	}
	got = FilterResources(seeds, ResourceFilter{Sort: "size", Order: "desc"})
	if got[0].Size != 33555529 || got[2].Size != 128 {
		t.Fatalf("size desc: %d %d %d", got[0].Size, got[1].Size, got[2].Size)
	}
	got = FilterResources(seeds, ResourceFilter{Sort: "hops", Order: "asc"})
	if minHops(got[0]) != 0 || minHops(got[2]) != 1 {
		t.Fatalf("hops asc")
	}
}

func TestMatchNode(t *testing.T) {
	if !MatchNode("worker-a", "12D3KooWabcdef", "WORKER-A") {
		t.Fatal("name")
	}
	if !MatchNode("worker-a", "12D3KooWabcdef", "12D3KooW") {
		t.Fatal("prefix")
	}
	if MatchNode("worker-a", "12D3KooWabcdef", "12D3") { // too short
		t.Fatal("short prefix should not match")
	}
	if !MatchNode("x", "id", "") {
		t.Fatal("empty want matches all")
	}
}

func ids(tasks []taskboard.Task) []string {
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.ID
	}
	return out
}

func TestFilterResourcesCreatedSort(t *testing.T) {
	old := ts("2026-11-10T10:00:00Z")
	newer := ts("2026-11-10T16:00:00Z")
	seeds := []resource.Info{
		{Seed: resource.Seed{Digest: "sha256:aaa", Name: "older", Kind: "tree", Size: 10, CreatedAt: old}},
		{Seed: resource.Seed{Digest: "sha256:bbb", Name: "newer", Kind: "blob", Size: 20, CreatedAt: newer}},
		{Seed: resource.Seed{Digest: "sha256:ccc", Name: "unknown", Kind: "blob", Size: 5}},
	}
	got := FilterResources(seeds, ResourceFilter{Sort: "created", Order: "desc"})
	if got[0].Name != "newer" || got[1].Name != "older" || got[2].Name != "unknown" {
		t.Fatalf("created desc: %s %s %s", got[0].Name, got[1].Name, got[2].Name)
	}
}
