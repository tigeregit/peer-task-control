package gater

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/network"
	ma "github.com/multiformats/go-multiaddr"

	"github.com/tigeregit/peer-task-control/internal/config"
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

type fakeAddrs struct{ local, remote ma.Multiaddr }

func (f fakeAddrs) LocalMultiaddr() ma.Multiaddr  { return f.local }
func (f fakeAddrs) RemoteMultiaddr() ma.Multiaddr { return f.remote }

func TestInterceptAcceptCircuit(t *testing.T) {
	cfg := config.Defaults()
	cfg.TrustedCIDRs = []string{"10.0.0.0/8"}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	g := New(cfg, nil)
	local, _ := ma.NewMultiaddr("/ip4/10.0.0.2/tcp/18765")
	if !g.InterceptAccept(fakeAddrs{local, circuitAddr(t)}) {
		t.Fatal("relayed accept should be allowed")
	}
	var _ network.ConnMultiaddrs = fakeAddrs{}
}

func circuitAddr(t *testing.T) ma.Multiaddr {
	t.Helper()
	a, err := ma.NewMultiaddr("/ip4/10.0.0.1/tcp/18765/p2p/12D3KooWBQtRnmpKoX5ybrd8EeFwW9yaekVJWfN62ZsrqZAGMggJ/p2p-circuit")
	if err != nil {
		t.Fatal(err)
	}
	return a
}
