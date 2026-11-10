package catalog

import (
	"testing"
	"time"
)

func TestMergePrefersNewer(t *testing.T) {
	b := New()
	t0 := time.Now().Add(-time.Minute)
	t1 := time.Now()
	b.Put(Entry{PeerID: "p1", Name: "old", UpdatedAt: t0})
	if b.Put(Entry{PeerID: "p1", Name: "old", UpdatedAt: t0}) {
		t.Fatal("identical older entry should not count as change")
	}
	if !b.Put(Entry{PeerID: "p1", Name: "new", UpdatedAt: t1}) {
		t.Fatal("newer entry should replace")
	}
	if b.Put(Entry{PeerID: "p1", Name: "stale", UpdatedAt: t0}) {
		t.Fatal("stale entry should be ignored")
	}
	e, _ := b.Get("p1")
	if e.Name != "new" {
		t.Fatalf("got %q", e.Name)
	}
}

func TestListSorted(t *testing.T) {
	b := New()
	b.Put(Entry{PeerID: "b", Name: "z", UpdatedAt: time.Now()})
	b.Put(Entry{PeerID: "a", Name: "a", UpdatedAt: time.Now()})
	list := b.List()
	if len(list) != 2 || list[0].Name != "a" || list[1].Name != "z" {
		t.Fatalf("unexpected order: %+v", list)
	}
}
