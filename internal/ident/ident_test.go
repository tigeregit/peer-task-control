package ident

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestQueryRoundTrip(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().String()
	_ = pc.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := ServeUDP(ctx, addr, "12D3KooWTestPeer", nil); err != nil {
		t.Fatal(err)
	}
	qctx, qcancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer qcancel()
	id, err := Query(qctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	if id != "12D3KooWTestPeer" {
		t.Fatalf("got %q", id)
	}
}
