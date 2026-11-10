package policy

import (
	"net"
	"testing"
)

func TestDefaultOpen(t *testing.T) {
	s := New(nil, nil, true, true, true)
	d := s.Check("files", "QmX", "node-a", net.ParseIP("10.0.0.2"))
	if !d.Allow {
		t.Fatalf("expected allow, got %+v", d)
	}
}

func TestDenyWins(t *testing.T) {
	s := New([]string{"10.0.0.2"}, []string{"10.0.0.2"}, true, true, true)
	d := s.Check("files", "", "", net.ParseIP("10.0.0.2"))
	if d.Allow || d.Reason == "" {
		t.Fatalf("expected deny with reason, got %+v", d)
	}
}

func TestAllowList(t *testing.T) {
	s := New([]string{"worker-a", "12D3KooWabc"}, nil, true, true, true)
	if s.Check("exec", "", "worker-b", net.ParseIP("10.0.0.3")).Allow {
		t.Fatal("worker-b should be rejected")
	}
	if !s.Check("exec", "", "worker-a", nil).Allow {
		t.Fatal("worker-a should be allowed by name")
	}
	if !s.Check("exec", "12D3KooWabc", "", nil).Allow {
		t.Fatal("peer id should be allowed")
	}
}

func TestDisabledService(t *testing.T) {
	s := New(nil, nil, false, true, true)
	d := s.Check("exec", "", "x", net.ParseIP("10.0.0.1"))
	if d.Allow || !containsStr(d.Reason, "disabled") {
		t.Fatalf("expected disabled reason, got %+v", d)
	}
}

func TestCIDRMatch(t *testing.T) {
	s := New(nil, []string{"10.1.0.0/16"}, true, true, true)
	if s.Check("files", "", "", net.ParseIP("10.1.2.3")).Allow {
		t.Fatal("should deny CIDR")
	}
	if !s.Check("files", "", "", net.ParseIP("10.2.0.1")).Allow {
		t.Fatal("other CIDR should pass")
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})())
}
