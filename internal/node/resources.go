package node

import (
	"archive/tar"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/peer-task-control/ptc/internal/cas"
	"github.com/peer-task-control/ptc/internal/resource"
)

const (
	ProtocolResources protocol.ID = "/ptc/resources/1.0.0"
	resourcesTimeout              = 20 * time.Second
)

type resourcesMessage struct {
	Type   string          `json:"type"`
	Digest string          `json:"digest,omitempty"`
	Node   string          `json:"node,omitempty"`
	PeerID string          `json:"peer_id,omitempty"`
	Seeds  []resource.Seed `json:"seeds,omitempty"`
	Name   string          `json:"name,omitempty"`
	Kind   string          `json:"kind,omitempty"`
	Size   int64           `json:"size,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func (n *Node) localResourceInfos() []resource.Info {
	loc := n.RouteTo(n.Host.ID().String())
	var out []resource.Info
	for _, s := range resource.Inventory(n.Cfg.DataDir) {
		out = append(out, resource.Info{Seed: s, Locations: []resource.Location{loc}})
	}
	return out
}

// CollectResources returns every advertised seed plus replica locations and hop counts from this node.
func (n *Node) CollectResources(ctx context.Context) []resource.Info {
	if ctx == nil {
		ctx = n.ctx
	}
	out := n.localResourceInfos()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, e := range n.Peers() {
		if e.PeerID == "" || e.PeerID == n.Host.ID().String() {
			continue
		}
		e := e
		wg.Add(1)
		go func() {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, resourcesTimeout)
			defer cancel()
			inv, err := n.fetchResources(pctx, e.PeerID)
			if err != nil {
				n.Log.Info("fetch resources failed", "peer", e.Name, "id", e.PeerID, "err", err)
				return
			}
			loc := n.RouteTo(e.PeerID)
			if loc.Node == "" {
				loc.Node = e.Name
			}
			var infos []resource.Info
			for _, s := range inv {
				infos = append(infos, resource.Info{Seed: s, Locations: []resource.Location{loc}})
			}
			mu.Lock()
			out = resource.Merge(out, infos...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

func (n *Node) Resource(ctx context.Context, digest string) (resource.Info, error) {
	digest = resource.Normalize(digest)
	if digest == "" {
		return resource.Info{}, fmt.Errorf("invalid digest")
	}
	info, ok := resource.Lookup(n.CollectResources(ctx), digest)
	if !ok {
		return resource.Info{}, fmt.Errorf("seed %s not found on the mesh", digest)
	}
	if len(info.Files) == 0 {
		info.Seed = n.fillSeedFiles(ctx, info)
	}
	return info, nil
}

func lookupSeeds(dataDir, digest string) []resource.Seed {
	digest = resource.Normalize(digest)
	var out []resource.Seed
	for _, s := range resource.Inventory(dataDir) {
		if s.Digest == digest {
			out = append(out, s)
		}
	}
	return out
}

func (n *Node) fillSeedFiles(ctx context.Context, info resource.Info) resource.Seed {
	for _, s := range resource.Inventory(n.Cfg.DataDir) {
		if s.Digest == info.Digest && len(s.Files) > 0 {
			return s
		}
	}
	for _, loc := range info.Locations {
		if loc.Local || loc.PeerID == "" || loc.PeerID == n.Host.ID().String() {
			continue
		}
		seeds, err := n.fetchResourceShow(ctx, loc.PeerID, info.Digest)
		if err != nil || len(seeds) == 0 {
			continue
		}
		s := seeds[0]
		if s.Name == "" {
			s.Name = info.Name
		}
		if s.Size == 0 {
			s.Size = info.Size
		}
		return s
	}
	return info.Seed
}

func writeResMsg(w io.Writer, msg resourcesMessage) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

func readResMsg(r io.Reader) (resourcesMessage, *bufio.Reader, error) {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	line, err := br.ReadBytes('\n')
	if err != nil {
		return resourcesMessage{}, br, err
	}
	var msg resourcesMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return resourcesMessage{}, br, err
	}
	return msg, br, nil
}

func (n *Node) handleResources(s network.Stream) {
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(resourcesTimeout))
	msg, _, err := readResMsg(s)
	if err != nil {
		return
	}
	switch msg.Type {
	case "list":
		seeds := resource.Inventory(n.Cfg.DataDir)
		for i := range seeds {
			seeds[i].Files = nil
		}
		_ = writeResMsg(s, resourcesMessage{Type: "list", Node: n.Cfg.Name, PeerID: n.Host.ID().String(), Seeds: seeds})
	case "show":
		_ = writeResMsg(s, resourcesMessage{Type: "show", Node: n.Cfg.Name, PeerID: n.Host.ID().String(), Seeds: lookupSeeds(n.Cfg.DataDir, msg.Digest)})
	case "blob":
		blob, err := n.openLocal(msg.Digest)
		if err != nil {
			_ = writeResMsg(s, resourcesMessage{Type: "blob", Error: err.Error()})
			return
		}
		defer blob.Body.Close()
		_ = s.SetDeadline(time.Now().Add(30 * time.Minute))
		if err := writeResMsg(s, resourcesMessage{Type: "blob", Digest: blob.Digest, Name: blob.Name, Kind: blob.Kind, Size: blob.Size}); err != nil {
			return
		}
		_, _ = io.Copy(s, blob.Body)
	default:
		_ = writeResMsg(s, resourcesMessage{Type: msg.Type, Error: "unknown type"})
	}
}

func (n *Node) fetchResources(ctx context.Context, id string) ([]resource.Seed, error) {
	pid, err := peer.Decode(id)
	if err != nil {
		return nil, err
	}
	s, err := n.openStream(ctx, pid, ProtocolResources)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	_ = s.SetDeadline(timeNowDeadline(ctx, resourcesTimeout))
	if err := writeResMsg(s, resourcesMessage{Type: "list"}); err != nil {
		return nil, err
	}
	reply, _, err := readResMsg(s)
	if err != nil {
		return nil, err
	}
	if reply.Error != "" {
		return nil, fmt.Errorf("%s", reply.Error)
	}
	return reply.Seeds, nil
}

func (n *Node) fetchResourceShow(ctx context.Context, id, digest string) ([]resource.Seed, error) {
	pid, err := peer.Decode(id)
	if err != nil {
		return nil, err
	}
	s, err := n.openStream(ctx, pid, ProtocolResources)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	_ = s.SetDeadline(timeNowDeadline(ctx, resourcesTimeout))
	if err := writeResMsg(s, resourcesMessage{Type: "show", Digest: digest}); err != nil {
		return nil, err
	}
	reply, _, err := readResMsg(s)
	if err != nil {
		return nil, err
	}
	if reply.Error != "" {
		return nil, fmt.Errorf("%s", reply.Error)
	}
	return reply.Seeds, nil
}

// OpenResource opens a seed locally or from the closest replica.
func (n *Node) OpenResource(ctx context.Context, digest string) (resource.Blob, error) {
	digest = resource.Normalize(digest)
	if digest == "" {
		return resource.Blob{}, fmt.Errorf("invalid digest")
	}
	if blob, err := n.openLocal(digest); err == nil {
		return blob, nil
	}
	info, err := n.Resource(ctx, digest)
	if err != nil {
		return resource.Blob{}, err
	}
	var best *resource.Location
	for i := range info.Locations {
		loc := &info.Locations[i]
		if loc.Local || loc.PeerID == n.Host.ID().String() {
			continue
		}
		if best == nil || loc.Hops < best.Hops || (loc.Hops == best.Hops && loc.Connected && !best.Connected) {
			best = loc
		}
	}
	if best == nil {
		return resource.Blob{}, fmt.Errorf("seed %s has no reachable replica", digest)
	}
	return n.fetchBlob(ctx, best.PeerID, digest)
}

func (n *Node) fetchBlob(ctx context.Context, id, digest string) (resource.Blob, error) {
	pid, err := peer.Decode(id)
	if err != nil {
		return resource.Blob{}, err
	}
	s, err := n.openStream(ctx, pid, ProtocolResources)
	if err != nil {
		return resource.Blob{}, err
	}
	_ = s.SetDeadline(timeNowDeadline(ctx, 30*time.Minute))
	if err := writeResMsg(s, resourcesMessage{Type: "blob", Digest: digest}); err != nil {
		s.Close()
		return resource.Blob{}, err
	}
	reply, rest, err := readResMsg(s)
	if err != nil {
		s.Close()
		return resource.Blob{}, err
	}
	if reply.Error != "" {
		s.Close()
		return resource.Blob{}, fmt.Errorf("%s", reply.Error)
	}
	ct := "application/octet-stream"
	if reply.Kind == "tree" {
		ct = "application/x-tar"
	}
	name := reply.Name
	if name == "" {
		name = resource.Filename(resource.Seed{Digest: digest, Kind: reply.Kind})
	}
	return resource.Blob{Digest: digest, Name: name, Kind: reply.Kind, Size: reply.Size, ContentType: ct, Body: struct {
		io.Reader
		io.Closer
	}{rest, s}}, nil
}

func (n *Node) openLocal(digest string) (resource.Blob, error) {
	digest = resource.Normalize(digest)
	if dir := resource.FindPackageDir(n.Cfg.DataDir, digest); dir != "" {
		pr, pw := io.Pipe()
		go func() {
			err := writeTar(dir, pw)
			_ = pw.CloseWithError(err)
		}()
		inv := resource.Inventory(n.Cfg.DataDir)
		name := resource.Hex(digest) + ".tar"
		var size int64
		for _, s := range inv {
			if s.Digest == digest {
				name = resource.Filename(s)
				size = 0
				break
			}
		}
		return resource.Blob{Digest: digest, Name: name, Kind: "tree", Size: size, ContentType: "application/x-tar", Body: pr}, nil
	}
	store := cas.New(filepath.Join(n.Cfg.DataDir, "cas"))
	f, err := store.Open(digest)
	if err != nil {
		return resource.Blob{}, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return resource.Blob{}, err
	}
	return resource.Blob{Digest: digest, Name: resource.Hex(digest), Kind: "blob", Size: st.Size(), ContentType: "application/octet-stream", Body: f}, nil
}

func writeTar(dir string, w io.Writer) error {
	tw := tar.NewWriter(w)
	defer tw.Close()
	return filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		f.Close()
		return err
	})
}

// RouteTo describes how this node reaches peerID, including relay hop count.
func (n *Node) RouteTo(peerID string) resource.Location {
	loc := resource.Location{PeerID: peerID}
	if e, ok := n.Catalog.Get(peerID); ok {
		loc.Node = e.Name
	}
	if peerID == n.Host.ID().String() {
		loc.Node, loc.Local, loc.Connected, loc.Hops, loc.Via = n.Cfg.Name, true, true, 0, "local"
		return loc
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		loc.Via = "unknown"
		return loc
	}
	minHops := -1
	via := ""
	for _, c := range n.Host.Network().ConnsToPeer(pid) {
		h := resource.CircuitHops(c.RemoteMultiaddr())
		if h == 0 {
			minHops, via = 0, "direct"
			break
		}
		if minHops < 0 || h < minHops {
			minHops, via = h, "relay"
		}
	}
	if minHops >= 0 {
		loc.Hops, loc.Via, loc.Connected = minHops, via, true
		return loc
	}
	for _, a := range n.Host.Peerstore().Addrs(pid) {
		if resource.CircuitHops(a) == 0 {
			loc.Hops, loc.Via, loc.Connected = 0, "direct", false
			return loc
		}
	}
	loc.Hops, loc.Via, loc.Connected = 1, "relay", false
	return loc
}
