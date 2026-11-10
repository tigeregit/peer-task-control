package node

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/peer-task-control/ptc/internal/taskboard"
)

const (
	ProtocolRuns protocol.ID = "/ptc/runs/1.0.0"
	runsTimeout              = 5 * time.Second
)

type runsMessage struct {
	Type   string           `json:"type"`
	Node   string           `json:"node,omitempty"`
	PeerID string           `json:"peer_id,omitempty"`
	Tasks  []taskboard.Task `json:"tasks,omitempty"`
}

func (n *Node) runsDir() string {
	if n.Cfg == nil || n.Cfg.DataDir == "" {
		return ""
	}
	return filepath.Join(n.Cfg.DataDir, "runs")
}

func (n *Node) localRuns() []taskboard.Task {
	return taskboard.Stamp(taskboard.LoadDir(n.runsDir()), n.Cfg.Name, n.Host.ID().String())
}

// CollectRuns returns this node's recorded runs plus those published by peers.
func (n *Node) CollectRuns(ctx context.Context) []taskboard.Task {
	out := n.localRuns()
	if ctx == nil {
		ctx = n.ctx
	}
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
			pctx, cancel := context.WithTimeout(ctx, runsTimeout)
			defer cancel()
			tasks, err := n.fetchRuns(pctx, e.PeerID)
			if err != nil {
				n.Log.Debug("fetch runs failed", "peer", e.Name, "id", e.PeerID, "err", err)
				return
			}
			mu.Lock()
			out = taskboard.MergeTasks(out, tasks...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

func (n *Node) handleRuns(s network.Stream) {
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(runsTimeout))
	dec := json.NewDecoder(s)
	enc := json.NewEncoder(s)
	var msg runsMessage
	if err := dec.Decode(&msg); err != nil {
		n.Log.Debug("runs stream decode", "err", err, "peer", s.Conn().RemotePeer())
		return
	}
	_ = enc.Encode(runsMessage{
		Type:   "list",
		Node:   n.Cfg.Name,
		PeerID: n.Host.ID().String(),
		Tasks:  n.localRuns(),
	})
}

func (n *Node) fetchRuns(ctx context.Context, id string) ([]taskboard.Task, error) {
	pid, err := peer.Decode(id)
	if err != nil {
		return nil, err
	}
	s, err := n.openStream(ctx, pid, ProtocolRuns)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	_ = s.SetDeadline(timeNowDeadline(ctx, runsTimeout))
	if err := json.NewEncoder(s).Encode(runsMessage{Type: "list"}); err != nil {
		return nil, err
	}
	var reply runsMessage
	if err := json.NewDecoder(s).Decode(&reply); err != nil {
		return nil, err
	}
	node, peerID := reply.Node, reply.PeerID
	if node == "" {
		node = id
	}
	if peerID == "" {
		peerID = id
	}
	return taskboard.Stamp(reply.Tasks, node, peerID), nil
}
