package scan

import (
	"net"
	"testing"
)

func TestHostsSlash30(t *testing.T) {
	_, n, err := net.ParseCIDR("10.0.0.0/30")
	if err != nil {
		t.Fatal(err)
	}
	ips := Hosts(n)
	if len(ips) != 2 || ips[0].String() != "10.0.0.1" || ips[1].String() != "10.0.0.2" {
		t.Fatalf("got %v", ips)
	}
}

func TestHostsSlash32(t *testing.T) {
	_, n, _ := net.ParseCIDR("127.0.0.1/32")
	ips := Hosts(n)
	if len(ips) != 1 || ips[0].String() != "127.0.0.1" {
		t.Fatalf("got %v", ips)
	}
}

func TestSkipHugeNet(t *testing.T) {
	_, n, _ := net.ParseCIDR("10.0.0.0/8")
	if ips := Hosts(n); ips != nil {
		t.Fatalf("expected skip, got %d hosts", len(ips))
	}
}
