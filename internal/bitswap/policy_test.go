package bitswap

import (
	"context"
	"testing"

	bsmsg "github.com/ipfs/boxo/bitswap/message"
	bsnetwork "github.com/ipfs/boxo/bitswap/network"
	blocks "github.com/ipfs/go-block-format"
	"github.com/libp2p/go-libp2p/core/peer"
)

type recordingNetwork struct {
	bsnetwork.BitSwapNetwork
	sends int
}

func (n *recordingNetwork) SendMessage(context.Context, peer.ID, bsmsg.BitSwapMessage) error {
	n.sends++
	return nil
}

func TestQueuedResponsesRecheckFilesPolicyAtSend(t *testing.T) {
	wire := &recordingNetwork{}
	allowed := true
	n := circuitNetwork{BitSwapNetwork: wire, authorize: func(peer.ID) bool { return allowed }}
	b := blocks.NewBlock([]byte("queued before files policy was disabled"))
	m := bsmsg.New(false)
	m.AddBlock(b)
	if err := n.SendMessage(context.Background(), "requester", m); err != nil {
		t.Fatal(err)
	}
	allowed = false
	if err := n.SendMessage(context.Background(), "requester", m); err == nil {
		t.Fatal("queued block bypassed policy revocation")
	}
	have := bsmsg.New(false)
	have.AddHave(b.Cid())
	if err := n.SendMessage(context.Background(), "requester", have); err == nil {
		t.Fatal("queued HAVE bypassed policy revocation")
	}
	if wire.sends != 1 {
		t.Fatalf("denied responses reached wire: %d sends", wire.sends)
	}
	// A DONT_HAVE discloses no stored content.
	want := bsmsg.New(false)
	want.AddDontHave(b.Cid())
	if err := n.SendMessage(context.Background(), "requester", want); err != nil {
		t.Fatalf("non-serving message was blocked: %v", err)
	}
}
