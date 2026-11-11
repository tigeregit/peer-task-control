package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/tigeregit/peer-task-control/internal/catalog"
	"github.com/tigeregit/peer-task-control/internal/control"
	"github.com/tigeregit/peer-task-control/internal/dispatch"
	"github.com/tigeregit/peer-task-control/internal/policy"
	"github.com/tigeregit/peer-task-control/internal/query"
	"github.com/tigeregit/peer-task-control/internal/resource"
	"github.com/tigeregit/peer-task-control/internal/taskboard"
	"github.com/tigeregit/peer-task-control/internal/version"
)

type Node interface {
	LocalEntry() catalog.Entry
	ListenAddrs() []string
	Peers() []catalog.Entry
	Policy() *policy.Store
	PersistPolicy() error
	CollectRuns(ctx context.Context) []taskboard.Task
	CollectResources(ctx context.Context) []resource.Info
	Resource(ctx context.Context, digest string) (resource.Info, error)
	OpenResource(ctx context.Context, digest string) (resource.Blob, error)
	Dispatch(ctx context.Context, target catalog.Entry, bundle []byte, follow bool, submitter taskboard.Party, ev func(dispatch.Event)) (dispatch.Event, error)
	CollectQueue(ctx context.Context) []dispatch.QueueItem
	Cancel(ctx context.Context, id string) error
	FindPeer(id string) (catalog.Entry, bool)
	// Pull fetches a seed into this node's store with bitswap.
	Pull(ctx context.Context, digest string) (any, error)
	// MeshUpdate rolls a ptc version out to eligible peers.
	MeshUpdate(ctx context.Context, req control.MeshUpdateRequest) (control.MeshUpdateReport, error)
	// RequestRestart re-execs the daemon binary (after ptc update).
	RequestRestart()
	RemoteUpdatePolicy() bool
	SetRemoteUpdatePolicy(on bool) error
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
	mux.HandleFunc("GET /api/v1/runs", s.runs)
	mux.HandleFunc("GET /api/v1/resources", s.resources)
	mux.HandleFunc("GET /api/v1/resources/sha256/{hex}", s.resource)
	mux.HandleFunc("GET /api/v1/resources/sha256/{hex}/content", s.resourceContent)
	mux.HandleFunc("POST /api/v1/resources/sha256/{hex}/pull", s.resourcePull)
	mux.HandleFunc("POST /api/v1/exec", s.exec)
	mux.HandleFunc("GET /api/v1/queue", s.queue)
	mux.HandleFunc("POST /api/v1/cancel", s.cancel)
	mux.HandleFunc("POST /api/v1/update/mesh", s.meshUpdate)
	mux.HandleFunc("POST /api/v1/update/restart", s.restart)
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
	writeJSON(w, http.StatusOK, s.viewPolicy())
}

func (s *Server) meshUpdate(w http.ResponseWriter, r *http.Request) {
	var req control.MeshUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rep, err := s.Node.MeshUpdate(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) restart(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarting"})
	s.Node.RequestRestart()
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
	case "update":
		if ch.On == nil {
			http.Error(w, "update op needs on", http.StatusBadRequest)
			return
		}
		if err := s.Node.SetRemoteUpdatePolicy(*ch.On); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, s.viewPolicy())
		return
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
	writeJSON(w, http.StatusOK, s.viewPolicy())
}

func (s *Server) runs(w http.ResponseWriter, r *http.Request) {
	tasks := s.Node.CollectRuns(r.Context())
	f := query.TaskFilterFrom(r.URL.Query())
	if f.Active() {
		tasks = query.FilterTasks(tasks, f)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (s *Server) resources(w http.ResponseWriter, r *http.Request) {
	local := s.Node.LocalEntry()
	seeds := s.Node.CollectResources(r.Context())
	f := query.ResourceFilterFrom(r.URL.Query())
	if f.Active() {
		seeds = query.FilterResources(seeds, f)
	}
	writeJSON(w, http.StatusOK, resource.List{
		Viewer: local.Name,
		PeerID: local.PeerID,
		Seeds:  seeds,
	})
}

func (s *Server) resource(w http.ResponseWriter, r *http.Request) {
	digest := "sha256:" + r.PathValue("hex")
	info, err := s.Node.Resource(r.Context(), digest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) resourceContent(w http.ResponseWriter, r *http.Request) {
	digest := "sha256:" + r.PathValue("hex")
	blob, err := s.Node.OpenResource(r.Context(), digest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer blob.Body.Close()
	if blob.ContentType != "" {
		w.Header().Set("Content-Type", blob.ContentType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	name := blob.Name
	if name == "" {
		name = resource.Filename(resource.Seed{Digest: digest, Kind: blob.Kind, Name: blob.Name})
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	if blob.Size > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", blob.Size))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, blob.Body)
}

func (s *Server) resourcePull(w http.ResponseWriter, r *http.Request) {
	digest := "sha256:" + r.PathValue("hex")
	res, err := s.Node.Pull(r.Context(), digest)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "result": res})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) viewPolicy() control.PolicyView {
	p := s.Node.Policy()
	exec, files, relay := p.Services()
	return control.PolicyView{
		Allow:        p.AllowList(),
		Deny:         p.DenyList(),
		Exec:         exec,
		Files:        files,
		Relay:        relay,
		RemoteUpdate: s.Node.RemoteUpdatePolicy(),
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
