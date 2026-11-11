package policy

import (
	"fmt"
	"net"
	"strings"
	"sync"
)

type Decision struct {
	Allow  bool
	Reason string
}

type Store struct {
	mu    sync.RWMutex
	allow []string
	deny  []string
	exec  bool
	files bool
	relay bool
}

func New(allow, deny []string, exec, files, relay bool) *Store {
	return &Store{
		allow: append([]string(nil), allow...),
		deny:  append([]string(nil), deny...),
		exec:  exec,
		files: files,
		relay: relay,
	}
}

func (s *Store) AllowList() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.allow...)
}

func (s *Store) DenyList() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.deny...)
}

func (s *Store) Services() (exec, files, relay bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.exec, s.files, s.relay
}

func (s *Store) ServiceEnabled(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch name {
	case "exec":
		return s.exec
	case "files":
		return s.files
	case "relay":
		return s.relay
	default:
		return false
	}
}

func (s *Store) SetService(name string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch name {
	case "exec":
		s.exec = on
	case "files":
		s.files = on
	case "relay":
		s.relay = on
	default:
		return fmt.Errorf("unknown service %q (want exec, files, or relay)", name)
	}
	return nil
}

func (s *Store) Add(list, spec string) error {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return fmt.Errorf("empty policy entry")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch list {
	case "allow":
		if !contains(s.allow, spec) {
			s.allow = append(s.allow, spec)
		}
	case "deny":
		if !contains(s.deny, spec) {
			s.deny = append(s.deny, spec)
		}
	default:
		return fmt.Errorf("unknown list %q", list)
	}
	return nil
}

func (s *Store) Remove(list, spec string) error {
	spec = strings.TrimSpace(spec)
	s.mu.Lock()
	defer s.mu.Unlock()
	switch list {
	case "allow":
		s.allow = remove(s.allow, spec)
	case "deny":
		s.deny = remove(s.deny, spec)
	default:
		return fmt.Errorf("unknown list %q", list)
	}
	return nil
}

// Check reports whether a remote peer may use a local service.
// peerID, name and ip are optional identifying attributes of the caller.
func (s *Store) Check(service, peerID, name string, ip net.IP) Decision {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if matchAny(s.deny, peerID, name, ip) {
		return Decision{Allow: false, Reason: "peer is on this host's deny list"}
	}
	if len(s.allow) > 0 && !matchAny(s.allow, peerID, name, ip) {
		return Decision{Allow: false, Reason: "peer is not on this host's allow list"}
	}

	enabled := false
	switch service {
	case "exec":
		enabled = s.exec
	case "files":
		enabled = s.files
	case "relay":
		enabled = s.relay
	default:
		return Decision{Allow: false, Reason: fmt.Sprintf("unknown service %q", service)}
	}
	if !enabled {
		return Decision{Allow: false, Reason: fmt.Sprintf("service %s is disabled on this host", service)}
	}
	return Decision{Allow: true, Reason: ""}
}

// CheckPeer applies allow/deny lists without requiring a service to be on.
func (s *Store) CheckPeer(peerID, name string, ip net.IP) Decision {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if matchAny(s.deny, peerID, name, ip) {
		return Decision{Allow: false, Reason: "peer is on this host's deny list"}
	}
	if len(s.allow) > 0 && !matchAny(s.allow, peerID, name, ip) {
		return Decision{Allow: false, Reason: "peer is not on this host's allow list"}
	}
	return Decision{Allow: true, Reason: ""}
}

func matchAny(list []string, peerID, name string, ip net.IP) bool {
	for _, spec := range list {
		if match(spec, peerID, name, ip) {
			return true
		}
	}
	return false
}

func match(spec, peerID, name string, ip net.IP) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return false
	}
	if peerID != "" && strings.EqualFold(spec, peerID) {
		return true
	}
	if name != "" && strings.EqualFold(spec, name) {
		return true
	}
	if ip != nil {
		if strings.Contains(spec, "/") {
			_, n, err := net.ParseCIDR(spec)
			if err == nil && n.Contains(ip) {
				return true
			}
		} else if parsed := net.ParseIP(spec); parsed != nil && parsed.Equal(ip) {
			return true
		}
	}
	return false
}

func contains(list []string, spec string) bool {
	for _, x := range list {
		if x == spec {
			return true
		}
	}
	return false
}

func remove(list []string, spec string) []string {
	out := list[:0:0]
	for _, x := range list {
		if x != spec {
			out = append(out, x)
		}
	}
	return out
}
