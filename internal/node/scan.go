package node

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/tigeregit/peer-task-control/internal/config"
	"github.com/tigeregit/peer-task-control/internal/control"
)

const ProtocolScan protocol.ID = "/ptc/scan/1.0.0"

type scanState struct {
	mu         sync.Mutex
	active     bool
	lastManual time.Time
}

func (n *Node) queueScan(manual bool, now time.Time) (control.ScanResponse, error) {
	reply := control.ScanResponse{Node: n.Cfg.Name, PeerID: n.Host.ID().String()}
	n.scanning.mu.Lock()
	defer n.scanning.mu.Unlock()
	interval := n.Cfg.ScanMinInterval
	if interval == 0 {
		interval = config.DefaultScanMinInterval
	}
	remaining := interval - now.Sub(n.scanning.lastManual)
	if !n.scanning.lastManual.IsZero() && remaining > 0 {
		reply.RetryAfterSeconds = int64(remaining / time.Second)
		if remaining%time.Second != 0 {
			reply.RetryAfterSeconds++
		}
	}
	switch {
	case n.ctx.Err() != nil:
		reply.Status, reply.Error = "unavailable", "daemon is stopping"
	case n.scanning.active:
		reply.Status, reply.Error = "busy", "a scan is already pending or running"
		reply.RetryAfterSeconds = max(1, reply.RetryAfterSeconds)
	case manual && reply.RetryAfterSeconds > 0:
		reply.Status, reply.Error = "rate_limited", "global manual scan cooldown has not elapsed"
	default:
		select {
		case n.scanNow <- struct{}{}:
			n.scanning.active = true
			if manual {
				n.scanning.lastManual = now
			}
			reply.Status, reply.RetryAfterSeconds = "accepted", 0
			return reply, nil
		default:
			reply.Status, reply.Error, reply.RetryAfterSeconds = "busy", "a scan is already pending", 1
		}
	}
	return reply, &control.ScanError{Reply: reply}
}

func (n *Node) finishScan() {
	n.scanning.mu.Lock()
	n.scanning.active = false
	n.scanning.mu.Unlock()
}

// Scan shares one gate between local requests and all remote callers.
func (n *Node) Scan(ctx context.Context, target string) (control.ScanResponse, error) {
	if err := ctx.Err(); err != nil {
		return control.ScanResponse{}, err
	}
	if target == "" || target == n.Cfg.Name || target == n.Host.ID().String() {
		return n.queueScan(true, time.Now())
	}
	entry, ok := n.FindPeer(target)
	if !ok {
		reply := control.ScanResponse{Node: target, Status: "not_found", Error: "node not found"}
		return reply, &control.ScanError{Reply: reply}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pid, err := peer.Decode(entry.PeerID)
	if err != nil {
		return control.ScanResponse{}, err
	}
	stream, err := n.openStream(ctx, pid, ProtocolScan)
	if err != nil {
		return control.ScanResponse{}, fmt.Errorf("request scan on %s: %w", target, err)
	}
	defer stream.Close()
	stop := context.AfterFunc(ctx, func() { _ = stream.Reset() })
	defer stop()
	_ = stream.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(stream).Encode(control.ScanRequest{}); err != nil {
		return control.ScanResponse{}, err
	}
	var reply control.ScanResponse
	if err := json.NewDecoder(io.LimitReader(stream, 4096)).Decode(&reply); err != nil {
		return control.ScanResponse{}, err
	}
	if reply.PeerID != entry.PeerID {
		return control.ScanResponse{}, fmt.Errorf("scan response peer does not match target")
	}
	if reply.Status != "accepted" {
		return reply, &control.ScanError{Reply: reply}
	}
	return reply, nil
}

func (n *Node) handleScan(stream network.Stream) {
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(5 * time.Second))
	var req control.ScanRequest
	if err := json.NewDecoder(io.LimitReader(stream, 4096)).Decode(&req); err != nil {
		return
	}
	reply := control.ScanResponse{Node: n.Cfg.Name, PeerID: n.Host.ID().String()}
	switch {
	case req.Node != "":
		reply.Status, reply.Error = "denied", "scan requests cannot be forwarded by the target"
	default:
		if decision := n.peerDecision("relay", stream.Conn().RemotePeer()); !decision.Allow {
			reply.Status, reply.Error = "denied", decision.Reason
		} else {
			reply, _ = n.queueScan(true, time.Now())
		}
	}
	if err := json.NewEncoder(stream).Encode(reply); err != nil {
		n.Log.Debug("scan response was not delivered", "peer", stream.Conn().RemotePeer(), "err", err)
	}
}
