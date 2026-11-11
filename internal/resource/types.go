package resource

import (
	"io"
	"time"
)

// Seed is a content-addressed object advertised on the mesh.
type Seed struct {
	Digest string `json:"digest"`
	// CID is the bitswap transport root of the same content (a raw SHA-256
	// CID of the chunk manifest), advertised so the board can show it and
	// holders can be compared. It is not a UnixFS DAG CID.
	CID       string     `json:"cid,omitempty"`
	Name      string     `json:"name,omitempty"`
	Kind      string     `json:"kind"`           // "tree" or "blob"
	Unit      string     `json:"unit,omitempty"` // "directory" = one digest for the whole tree
	Size      int64      `json:"size"`
	FileCount int        `json:"file_count,omitempty"`
	Files     []File     `json:"files,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	Pin       bool       `json:"pin,omitempty"`
	Replica   bool       `json:"replica,omitempty"`
	RunOK     *bool      `json:"run_ok,omitempty"`
}

type File struct {
	Path   string `json:"path"`
	Digest string `json:"digest,omitempty"`
	Size   int64  `json:"size,omitempty"`
	Dir    bool   `json:"dir,omitempty"`
}

// Location is one replica of a seed, with the route from the querying node.
type Location struct {
	Node      string `json:"node"`
	PeerID    string `json:"peer_id"`
	Local     bool   `json:"local,omitempty"`
	Connected bool   `json:"connected"`
	Hops      int    `json:"hops"`
	Via       string `json:"via"` // local, direct, or relay
}

// Info is a seed plus every known replica.
type Info struct {
	Seed
	Locations []Location `json:"locations"`
}

type List struct {
	Viewer string `json:"viewer"`
	PeerID string `json:"peer_id"`
	Seeds  []Info `json:"seeds"`
}

// Blob is a readable download of a seed.
type Blob struct {
	Digest      string
	Name        string
	Kind        string
	Size        int64 // 0 if unknown (chunked)
	ContentType string
	Body        io.ReadCloser
}
