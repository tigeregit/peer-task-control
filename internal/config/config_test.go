package config

import (
	"net"
	"path/filepath"
	"testing"
)

func TestNormalizeRequiresCIDR(t *testing.T) {
	c := Defaults()
	c.TrustedCIDRs = nil
	if err := c.Normalize(); err == nil {
		t.Fatal("expected error for empty trusted_cidrs")
	}
}

func TestLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	src := Defaults()
	src.Path = path
	src.DataDir = dir
	src.TrustedCIDRs = []string{"10.0.0.0/8", "127.0.0.0/8"}
	src.Services.Relay = true
	src.PSK = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := src.Normalize(); err != nil {
		t.Fatal(err)
	}
	if err := src.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Services.Relay || got.ListenPort() != 18765 {
		t.Fatalf("unexpected config: %+v", got)
	}
	if len(got.PSKBytes) != 32 || got.PSKBytes[0] != 0x01 {
		t.Fatalf("psk bytes = %x", got.PSKBytes)
	}
	if !got.InTrustedIP(net.ParseIP("10.9.1.2")) || !got.InTrustedIP(net.ParseIP("127.0.0.1")) {
		t.Fatal("trusted IP check failed")
	}
	if got.InTrustedIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public IP should be rejected")
	}
}

func TestParsePSKRejectsShort(t *testing.T) {
	if _, err := ParsePSK("abcd"); err == nil {
		t.Fatal("expected error")
	}
}
