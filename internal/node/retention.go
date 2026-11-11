package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/tigeregit/peer-task-control/internal/cas"
	"github.com/tigeregit/peer-task-control/internal/resource"
	"github.com/tigeregit/peer-task-control/internal/retention"
)

const ProtocolRetention protocol.ID = "/ptc/retention/1.0.0"

type retentionMessage struct {
	Action string `json:"action"`
	Token  string `json:"token"`
	Digest string `json:"digest"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

func (n *Node) handleRetention(s network.Stream) {
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(3 * time.Minute))
	var req retentionMessage
	if err := json.NewDecoder(io.LimitReader(s, 4096)).Decode(&req); err != nil {
		return
	}
	err := n.applyRetention(s.Conn().RemotePeer(), req)
	req.OK = err == nil
	if err != nil {
		req.Error = err.Error()
	}
	_ = json.NewEncoder(s).Encode(req)
}

func (n *Node) applyRetention(from peer.ID, req retentionMessage) error {
	if d := n.resourcePeerDecision(from); !d.Allow {
		return fmt.Errorf("retention denied: %s", d.Reason)
	}
	if resource.Normalize(req.Digest) != req.Digest || (req.Action != "hold" && req.Action != "release") {
		return fmt.Errorf("invalid retention request")
	}
	guard, err := retention.Lock(n.Cfg.SeedsDir())
	if err != nil {
		return fmt.Errorf("local store is being maintained: %w", err)
	}
	defer guard.Close()
	records, err := retention.Records(n.Cfg.SeedsDir(), false)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.Token != req.Token {
			continue
		}
		if record.Peer != from.String() || record.Digest != req.Digest {
			return fmt.Errorf("retention token belongs to another request")
		}
		if record.Released && req.Action == "hold" {
			return fmt.Errorf("retention transaction was already released")
		}
	}
	if req.Action == "hold" {
		kind, path, _, ok := resource.FindByDigest(n.Cfg.PackagesDir(), n.Cfg.CASDir(), req.Digest)
		if !ok {
			return fmt.Errorf("complete local replica not found")
		}
		var digest string
		if kind == "tree" {
			digest, _, err = cas.DirDigest(path)
		} else {
			var f *os.File
			f, err = os.Open(path)
			if err == nil {
				h := sha256.New()
				_, err = io.Copy(h, f)
				closeErr := f.Close()
				if err == nil {
					err = closeErr
				}
				digest = "sha256:" + hex.EncodeToString(h.Sum(nil))
			}
		}
		if err != nil {
			return err
		}
		if digest != req.Digest {
			return fmt.Errorf("local replica failed digest verification")
		}
	}
	return retention.Save(n.Cfg.SeedsDir(), retention.Record{Token: req.Token, Digest: req.Digest,
		Peer: from.String(), Released: req.Action == "release"}, false)
}

func (n *Node) sendRetention(ctx context.Context, rec retention.Record, action string) error {
	pid, err := peer.Decode(rec.Peer)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	s, err := n.openStream(ctx, pid, ProtocolRetention)
	if err != nil {
		return err
	}
	defer s.Close()
	stop := context.AfterFunc(ctx, func() { _ = s.Reset() })
	defer stop()
	_ = s.SetDeadline(time.Now().Add(3 * time.Minute))
	req := retentionMessage{Action: action, Token: rec.Token, Digest: rec.Digest}
	if err := json.NewEncoder(s).Encode(req); err != nil {
		return err
	}
	var reply retentionMessage
	if err := json.NewDecoder(io.LimitReader(s, 4096)).Decode(&reply); err != nil {
		return err
	}
	if !reply.OK || reply.Token != req.Token || reply.Digest != req.Digest || reply.Action != action {
		return fmt.Errorf("replica retention refused: %s", reply.Error)
	}
	return nil
}
