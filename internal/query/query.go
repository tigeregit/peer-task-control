package query

import (
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/peer-task-control/ptc/internal/resource"
	"github.com/peer-task-control/ptc/internal/taskboard"
)

// TaskFilter is the shared task list query used by the CLI and the HTTP API.
// Status groups match the WebUI chips: in_progress includes queued, completed
// includes success/cancelled/skipped, failed is failure.
type TaskFilter struct {
	Q      string
	Status string
	Node   string
	Sort   string
	Order  string
}

// ResourceFilter is the shared seed list query used by the CLI and the HTTP API.
type ResourceFilter struct {
	Q     string
	Kind  string
	Node  string
	Sort  string
	Order string
}

func TaskFilterFrom(v url.Values) TaskFilter {
	q := v.Get("q")
	if q == "" {
		q = v.Get("search")
	}
	return TaskFilter{Q: q, Status: v.Get("status"), Node: v.Get("node"), Sort: v.Get("sort"), Order: v.Get("order")}
}

func ResourceFilterFrom(v url.Values) ResourceFilter {
	q := v.Get("q")
	if q == "" {
		q = v.Get("search")
	}
	return ResourceFilter{Q: q, Kind: v.Get("kind"), Node: v.Get("node"), Sort: v.Get("sort"), Order: v.Get("order")}
}

func (f TaskFilter) Active() bool {
	return strings.TrimSpace(f.Q) != "" || statusKey(f.Status) != "" || strings.TrimSpace(f.Node) != "" || strings.TrimSpace(f.Sort) != ""
}

func (f ResourceFilter) Active() bool {
	return strings.TrimSpace(f.Q) != "" || kindKey(f.Kind) != "" || strings.TrimSpace(f.Node) != "" || strings.TrimSpace(f.Sort) != ""
}

// MatchNode reports whether a record belongs to the named device (node name,
// full peer id, or a unique prefix of the peer id).
func MatchNode(name, peerID, want string) bool {
	want = strings.TrimSpace(strings.ToLower(want))
	if want == "" {
		return true
	}
	if strings.ToLower(name) == want {
		return true
	}
	id := strings.ToLower(peerID)
	return id == want || (len(want) >= 8 && strings.HasPrefix(id, want))
}

func FilterTasks(tasks []taskboard.Task, f TaskFilter) []taskboard.Task {
	out := make([]taskboard.Task, 0, len(tasks))
	for _, t := range tasks {
		if !matchTaskStatus(t.Status, f.Status) {
			continue
		}
		if !MatchNode(t.Node, t.PeerID, f.Node) {
			continue
		}
		if !matchQuery(f.Q, taskHaystack(t)) {
			continue
		}
		out = append(out, t)
	}
	sortTasks(out, f.Sort, f.Order)
	return out
}

func FilterResources(seeds []resource.Info, f ResourceFilter) []resource.Info {
	out := make([]resource.Info, 0, len(seeds))
	wantKind := kindKey(f.Kind)
	for _, s := range seeds {
		if wantKind != "" && strings.ToLower(s.Kind) != wantKind {
			continue
		}
		if strings.TrimSpace(f.Node) != "" && !resourceOnNode(s, f.Node) {
			continue
		}
		if !matchQuery(f.Q, resourceHaystack(s)) {
			continue
		}
		out = append(out, s)
	}
	sortResources(out, f.Sort, f.Order)
	return out
}

func resourceOnNode(s resource.Info, want string) bool {
	for _, loc := range s.Locations {
		if MatchNode(loc.Node, loc.PeerID, want) {
			return true
		}
	}
	return false
}

func statusKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == "all" {
		return ""
	}
	if s == "fail" || s == "failure" {
		return "failed"
	}
	return s
}

func kindKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == "all" {
		return ""
	}
	return s
}

func matchTaskStatus(got, want string) bool {
	switch statusKey(want) {
	case "":
		return true
	case "in_progress":
		return got == "in_progress" || got == "queued"
	case "completed":
		return got == "success" || got == "cancelled" || got == "skipped"
	case "failed":
		return got == "failure"
	default:
		return got == statusKey(want)
	}
}

func matchQuery(q, haystack string) bool {
	q = strings.TrimSpace(strings.ToLower(q))
	if q == "" {
		return true
	}
	return strings.Contains(strings.ToLower(haystack), q)
}

func taskHaystack(t taskboard.Task) string {
	var b strings.Builder
	add := func(s string) {
		if s == "" {
			return
		}
		b.WriteString(s)
		b.WriteByte(' ')
	}
	add(t.ID)
	add(t.Name.ZH)
	add(t.Name.EN)
	add(t.Status)
	add(t.Event)
	add(t.Actor)
	add(t.Target)
	add(t.Node)
	add(t.PeerID)
	add(t.Summary.ZH)
	add(t.Summary.EN)
	return b.String()
}

func resourceHaystack(s resource.Info) string {
	var b strings.Builder
	add := func(v string) {
		if v == "" {
			return
		}
		b.WriteString(v)
		b.WriteByte(' ')
	}
	add(s.Digest)
	add(s.Name)
	add(s.Kind)
	for _, f := range s.Files {
		add(f.Path)
		add(f.Digest)
	}
	for _, loc := range s.Locations {
		add(loc.Node)
		add(loc.PeerID)
	}
	return b.String()
}

func taskDuration(t taskboard.Task) time.Duration {
	if t.StartedAt == nil {
		return -1
	}
	end := time.Now()
	if t.EndedAt != nil {
		end = *t.EndedAt
	}
	d := end.Sub(*t.StartedAt)
	if d < 0 {
		return 0
	}
	return d
}

func startedUnix(t taskboard.Task) int64 {
	if t.StartedAt == nil {
		return 0
	}
	return t.StartedAt.UnixNano()
}

func sortTasks(tasks []taskboard.Task, key, order string) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		key = "started"
	}
	desc := descending(order, key == "started" || key == "duration")
	sort.SliceStable(tasks, func(i, j int) bool {
		return cmpOrder(taskCmp(tasks[i], tasks[j], key), desc)
	})
}

func taskCmp(a, b taskboard.Task, key string) int {
	switch key {
	case "name":
		return strings.Compare(strings.ToLower(taskName(a)), strings.ToLower(taskName(b)))
	case "status":
		return strings.Compare(a.Status, b.Status)
	case "node":
		return strings.Compare(strings.ToLower(a.Node), strings.ToLower(b.Node))
	case "id":
		return strings.Compare(a.ID, b.ID)
	case "duration":
		return cmpInt64(int64(taskDuration(a)), int64(taskDuration(b)))
	default:
		return cmpInt64(startedUnix(a), startedUnix(b))
	}
}

func taskName(t taskboard.Task) string {
	if t.Name.ZH != "" {
		return t.Name.ZH
	}
	return t.Name.EN
}

func minHops(s resource.Info) int {
	if len(s.Locations) == 0 {
		return 1 << 30
	}
	m := s.Locations[0].Hops
	for _, loc := range s.Locations[1:] {
		if loc.Hops < m {
			m = loc.Hops
		}
	}
	return m
}

func sortResources(seeds []resource.Info, key, order string) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		key = "name"
	}
	desc := descending(order, key == "size" || key == "replicas" || key == "created" || key == "created_at")
	sort.SliceStable(seeds, func(i, j int) bool {
		return cmpOrder(resourceCmp(seeds[i], seeds[j], key), desc)
	})
}

func resourceCmp(a, b resource.Info, key string) int {
	switch key {
	case "size":
		return cmpInt64(a.Size, b.Size)
	case "kind":
		return strings.Compare(a.Kind, b.Kind)
	case "hops":
		return cmpInt64(int64(minHops(a)), int64(minHops(b)))
	case "replicas", "nodes":
		return cmpInt64(int64(len(a.Locations)), int64(len(b.Locations)))
	case "digest":
		return strings.Compare(a.Digest, b.Digest)
	case "created", "created_at":
		return cmpInt64(createdUnix(a), createdUnix(b))
	default:
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.Digest, b.Digest)
	}
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func cmpOrder(c int, desc bool) bool {
	if c == 0 {
		return false
	}
	if desc {
		return c > 0
	}
	return c < 0
}

func descending(order string, defaultDesc bool) bool {
	switch strings.ToLower(strings.TrimSpace(order)) {
	case "asc", "ascending":
		return false
	case "desc", "descending":
		return true
	default:
		return defaultDesc
	}
}

func createdUnix(s resource.Info) int64 {
	if s.CreatedAt == nil || s.CreatedAt.IsZero() {
		return 0
	}
	return s.CreatedAt.UnixNano()
}
