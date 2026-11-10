package gater

import (
	"testing"

	ma "github.com/multiformats/go-multiaddr"

	"github.com/peer-task-control/ptc/internal/config"
)

func TestInterceptAddrDial(t *testing.T) {
	cfg := config.Defaults()
	cfg.TrustedCIDRs = []string{"10.0.0.0/8"}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	g := New(cfg, nil)

	allow, _ := ma.NewMultiaddr("/ip4/10.1.2.3/tcp/18765")
	deny, _ := ma.NewMultiaddr("/ip4/8.8.8.8/tcp/18765")
	circuit, _ := ma.NewMultiaddr("/ip4/10.0.0.1/tcp/18765/p2p/12D3KooWQnQfKqYvFvKqFvKqFvKqFvKqFvKqFvKqFvKqFvKqFvKq/p2p-circuit")

	if !g.InterceptAddrDial("", allow) {
		t.Fatal("trusted address should be allowed")
	}
	if g.InterceptAddrDial("", deny) {
		t.Fatal("public address should be rejected")
	}
	if !g.InterceptAddrDial("", circuit) {
		t.Fatal("circuit addr should be allowed")
	}
}
