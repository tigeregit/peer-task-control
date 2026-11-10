package catalog

import (
	"sort"
	"sync"
	"time"

	"github.com/peer-task-control/ptc/internal/disk"
)

type ServiceState struct {
	Offered bool   `json:"offered"`
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
}

type Caps struct {
	NVIDIA  bool   `json:"nvidia"`
	GPUName string `json:"gpu_name,omitempty"`
	VRAMMiB int    `json:"vram_mib,omitempty"`
	Driver  string `json:"driver,omitempty"`
	OS      string `json:"os,omitempty"`
	Arch    string `json:"arch,omitempty"`
}

type Entry struct {
	PeerID    string                  `json:"peer_id"`
	Name      string                  `json:"name"`
	Addrs     []string                `json:"addrs"`
	Services  map[string]ServiceState `json:"services"`
	Caps      Caps                    `json:"caps"`
	Disk      *disk.Usage             `json:"disk,omitempty"`
	UpdatedAt time.Time               `json:"updated_at"`
}

type Message struct {
	Type    string  `json:"type"` // "push" or "pull"
	Entries []Entry `json:"entries"`
}

type Book struct {
	mu      sync.RWMutex
	entries map[string]Entry
}

func New() *Book {
	return &Book{entries: make(map[string]Entry)}
}

func (b *Book) Put(e Entry) (changed bool) {
	if e.PeerID == "" {
		return false
	}
	if e.Services == nil {
		e.Services = map[string]ServiceState{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	old, ok := b.entries[e.PeerID]
	if ok && !e.UpdatedAt.After(old.UpdatedAt) && equalEntry(old, e) {
		return false
	}
	if ok && e.UpdatedAt.Before(old.UpdatedAt) {
		return false
	}
	b.entries[e.PeerID] = e
	return !ok || !equalEntry(old, e)
}

func (b *Book) Merge(entries []Entry) (added int) {
	for _, e := range entries {
		if b.Put(e) {
			added++
		}
	}
	return added
}

func (b *Book) Get(id string) (Entry, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	e, ok := b.entries[id]
	return e, ok
}

func (b *Book) List() []Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]Entry, 0, len(b.entries))
	for _, e := range b.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].PeerID < out[j].PeerID
	})
	return out
}

func (b *Book) Snapshot() []Entry {
	return b.List()
}

func equalEntry(a, b Entry) bool {
	if a.PeerID != b.PeerID || a.Name != b.Name || a.Caps != b.Caps {
		return false
	}
	if len(a.Addrs) != len(b.Addrs) {
		return false
	}
	for i := range a.Addrs {
		if a.Addrs[i] != b.Addrs[i] {
			return false
		}
	}
	if len(a.Services) != len(b.Services) {
		return false
	}
	for k, v := range a.Services {
		if b.Services[k] != v {
			return false
		}
	}
	return true
}
