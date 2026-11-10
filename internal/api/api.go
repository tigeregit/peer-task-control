package api

import (
	"encoding/json"
	"net"
	"net/http"

	"github.com/peer-task-control/ptc/internal/catalog"
	"github.com/peer-task-control/ptc/internal/control"
	"github.com/peer-task-control/ptc/internal/policy"
	"github.com/peer-task-control/ptc/internal/version"
)

type Node interface {
	LocalEntry() catalog.Entry
	ListenAddrs() []string
	Peers() []catalog.Entry
	Policy() *policy.Store
	PersistPolicy() error
}

type Server struct {
	Node Node
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("GET /api/v1/version", s.ver)
	mux.HandleFunc("GET /api/v1/status", s.status)
	mux.HandleFunc("GET /api/v1/peers", s.peers)
	mux.HandleFunc("GET /api/v1/policy", s.getPolicy)
	mux.HandleFunc("POST /api/v1/policy", s.postPolicy)
	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ver(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": version.Version})
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	local := s.Node.LocalEntry()
	writeJSON(w, http.StatusOK, control.Status{
		Name:    local.Name,
		PeerID:  local.PeerID,
		Version: version.Version,
		Listen:  s.Node.ListenAddrs(),
		Peers:   s.Node.Peers(),
	})
}

func (s *Server) peers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"peers": s.Node.Peers()})
}

func (s *Server) getPolicy(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, viewPolicy(s.Node.Policy()))
}

func (s *Server) postPolicy(w http.ResponseWriter, r *http.Request) {
	var ch control.PolicyChange
	if err := json.NewDecoder(r.Body).Decode(&ch); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p := s.Node.Policy()
	var err error
	switch ch.Op {
	case "allow_add":
		err = p.Add("allow", ch.Spec)
	case "allow_rm":
		err = p.Remove("allow", ch.Spec)
	case "deny_add":
		err = p.Add("deny", ch.Spec)
	case "deny_rm":
		err = p.Remove("deny", ch.Spec)
	case "service":
		if ch.On == nil || ch.Service == "" {
			http.Error(w, "service op needs service and on", http.StatusBadRequest)
			return
		}
		err = p.SetService(ch.Service, *ch.On)
	default:
		http.Error(w, "unknown op", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.Node.PersistPolicy(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, viewPolicy(p))
}

func viewPolicy(p *policy.Store) control.PolicyView {
	exec, files, relay := p.Services()
	return control.PolicyView{
		Allow: p.AllowList(),
		Deny:  p.DenyList(),
		Exec:  exec,
		Files: files,
		Relay: relay,
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// LocalOnly wraps a handler so only loopback or otherwise already-accepted
// listeners can use it. The API listener should already be bound to loopback.
func LocalOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			http.Error(w, "bad remote addr", http.StatusForbidden)
			return
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			http.Error(w, "api is loopback only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
