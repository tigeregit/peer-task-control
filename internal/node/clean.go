package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	manet "github.com/multiformats/go-multiaddr/net"
	multistream "github.com/multiformats/go-multistream"

	"github.com/tigeregit/peer-task-control/internal/clean"
	"github.com/tigeregit/peer-task-control/internal/control"
	"github.com/tigeregit/peer-task-control/internal/policy"
)

const ProtocolClean protocol.ID = "/ptc/clean/1.0.0"

const cleanTimeout = 10 * time.Minute

func (n *Node) peerTrustDecision(p peer.ID) policy.Decision {
	name := ""
	if e, ok := n.Catalog.Get(p.String()); ok {
		name = e.Name
	}
	conns := n.Host.Network().ConnsToPeer(p)
	if len(conns) == 0 {
		return n.Store.CheckPeer(p.String(), name, nil)
	}
	for _, c := range conns {
		var ip net.IP
		if !isCircuit(c.RemoteMultiaddr()) {
			ip, _ = manet.ToIP(c.RemoteMultiaddr())
		}
		if d := n.Store.CheckPeer(p.String(), name, ip); !d.Allow {
			return d
		}
	}
	return policy.Decision{Allow: true}
}

// Clean runs cleanup on this node, or asks a named peer to do the same.
func (n *Node) Clean(ctx context.Context, req control.CleanRequest) (control.CleanResponse, error) {
	if err := ctx.Err(); err != nil {
		return control.CleanResponse{}, err
	}
	if req.Node == "" || req.Node == n.Cfg.Name || req.Node == n.Host.ID().String() {
		return n.cleanLocal(ctx, req)
	}
	entry, ok := n.FindPeer(req.Node)
	if !ok {
		reply := control.CleanResponse{Node: req.Node, Status: "not_found", Error: "node not found"}
		return reply, &control.CleanError{Reply: reply}
	}
	ctx, cancel := context.WithTimeout(ctx, cleanTimeout)
	defer cancel()
	pid, err := peer.Decode(entry.PeerID)
	if err != nil {
		return control.CleanResponse{}, err
	}
	stream, err := n.openStream(ctx, pid, ProtocolClean)
	if err != nil {
		return remoteCleanError(req.Node, err)
	}
	defer stream.Close()
	stop := context.AfterFunc(ctx, func() { _ = stream.Reset() })
	defer stop()
	_ = stream.SetDeadline(time.Now().Add(cleanTimeout))
	forward := req
	forward.Node = ""
	if err := json.NewEncoder(stream).Encode(forward); err != nil {
		return remoteCleanError(req.Node, err)
	}
	var reply control.CleanResponse
	if err := json.NewDecoder(io.LimitReader(stream, 1<<20)).Decode(&reply); err != nil {
		return remoteCleanError(req.Node, err)
	}
	if reply.PeerID != "" && reply.PeerID != entry.PeerID {
		return control.CleanResponse{}, fmt.Errorf("clean response peer does not match target")
	}
	if reply.Status != "ok" {
		return reply, &control.CleanError{Reply: reply}
	}
	return reply, nil
}

func remoteCleanError(node string, err error) (control.CleanResponse, error) {
	if isUnsupportedClean(err) {
		return control.CleanResponse{}, fmt.Errorf("node %s does not support remote clean (upgrade ptc)", node)
	}
	return control.CleanResponse{}, fmt.Errorf("request clean on %s: %w", node, err)
}

func isUnsupportedClean(err error) bool {
	var unsupported multistream.ErrNotSupported[protocol.ID]
	if errors.As(err, &unsupported) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "protocols not supported") || strings.Contains(msg, "protocol not supported")
}

func (n *Node) handleClean(stream network.Stream) {
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(cleanTimeout))
	var req control.CleanRequest
	if err := json.NewDecoder(io.LimitReader(stream, 1<<20)).Decode(&req); err != nil {
		return
	}
	reply := control.CleanResponse{Node: n.Cfg.Name, PeerID: n.Host.ID().String()}
	switch {
	case req.Node != "":
		reply.Status, reply.Error = "denied", "clean requests cannot be forwarded by the target"
	case !n.Cfg.AllowsRemoteClean():
		reply.Status, reply.Error = "denied", "remote clean is disabled (enable policy.remote_clean on the target)"
	default:
		if decision := n.peerTrustDecision(stream.Conn().RemotePeer()); !decision.Allow {
			reply.Status, reply.Error = "denied", decision.Reason
		} else {
			reply, _ = n.cleanLocal(n.ctx, req)
		}
	}
	if err := json.NewEncoder(stream).Encode(reply); err != nil {
		n.Log.Debug("clean response was not delivered", "peer", stream.Conn().RemotePeer(), "err", err)
	}
}

func (n *Node) cleanLocal(ctx context.Context, req control.CleanRequest) (control.CleanResponse, error) {
	reply := control.CleanResponse{Node: n.Cfg.Name, PeerID: n.Host.ID().String(), DryRun: req.DryRun}
	if n.ctx.Err() != nil {
		reply.Status, reply.Error = "unavailable", "daemon is stopping"
		return reply, &control.CleanError{Reply: reply}
	}
	if !req.DryRun && !req.Yes {
		reply.Status, reply.Error = "denied", "refusing to delete without yes (pass dry_run to list)"
		return reply, &control.CleanError{Reply: reply}
	}
	kind := clean.Kind(req.Kind)
	switch kind {
	case clean.Outputs, clean.Runs, clean.Resources, clean.Worktrees:
	default:
		reply.Status, reply.Error = "denied", fmt.Sprintf("unknown clean kind %q", req.Kind)
		return reply, &control.CleanError{Reply: reply}
	}
	holders := clean.HolderCounts(n.CollectResources(ctx))
	opt := clean.Options{IDs: req.IDs, Match: req.Match, Force: req.Force, Holders: holders}
	active := clean.ActiveDigests(n.Cfg, n.queue)
	var (
		plans []clean.Plan
		err   error
	)
	switch kind {
	case clean.Outputs:
		plans, err = clean.PlanOutputs(n.Cfg, opt, active)
	case clean.Runs:
		plans, err = clean.PlanRuns(n.Cfg, opt)
	case clean.Resources:
		plans, err = clean.PlanResources(n.Cfg, opt, active)
	case clean.Worktrees:
		plans, err = clean.PlanWorktrees(n.Cfg, opt)
	}
	if err != nil {
		reply.Status, reply.Error = "denied", err.Error()
		return reply, &control.CleanError{Reply: reply}
	}
	reply.Plans = plans
	reply.Bytes = clean.Total(plans, true)
	if !req.DryRun {
		freed, applyErr := clean.Apply(plans)
		reply.Freed = freed
		if applyErr != nil {
			reply.Status, reply.Error = "unavailable", applyErr.Error()
			return reply, &control.CleanError{Reply: reply}
		}
	}
	reply.Status = "ok"
	return reply, nil
}
