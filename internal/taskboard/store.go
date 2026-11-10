package taskboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// LoadDir reads recorded task JSON files from a runs directory.
func LoadDir(dir string) []Task {
	if dir == "" {
		return nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Task
	for _, e := range ents {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var t Task
		if json.Unmarshal(b, &t) != nil || t.ID == "" {
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		return taskTime(out[i]) > taskTime(out[j])
	})
	return out
}

func taskTime(t Task) int64 {
	if t.StartedAt != nil {
		return t.StartedAt.UnixNano()
	}
	if t.EndedAt != nil {
		return t.EndedAt.UnixNano()
	}
	return 0
}

// Stamp marks tasks as published by a mesh node.
func Stamp(tasks []Task, node, peerID string) []Task {
	out := make([]Task, len(tasks))
	for i, t := range tasks {
		t.Node = node
		t.PeerID = peerID
		out[i] = t
	}
	return out
}

// MergeTasks appends incoming tasks, dropping exact id+peer duplicates.
// If the same id appears without a peer and later with a peer, the stamped
// copy replaces the unstamped one.
func MergeTasks(dst []Task, extra ...Task) []Task {
	idx := map[string]int{}
	for i, t := range dst {
		idx[taskKey(t)] = i
		if t.PeerID == "" && t.ID != "" {
			idx[t.ID] = i
		}
	}
	for _, t := range extra {
		if t.ID == "" {
			continue
		}
		key := taskKey(t)
		if j, ok := idx[key]; ok {
			if dst[j].Node == "" && t.Node != "" {
				dst[j] = t
			}
			continue
		}
		if t.PeerID != "" {
			if j, ok := idx[t.ID]; ok && dst[j].PeerID == "" {
				dst[j] = t
				idx[key] = j
				continue
			}
		}
		idx[key] = len(dst)
		if t.PeerID == "" {
			idx[t.ID] = len(dst)
		}
		dst = append(dst, t)
	}
	return dst
}

func taskKey(t Task) string {
	if t.PeerID != "" {
		return t.ID + "@" + t.PeerID
	}
	return t.ID
}
