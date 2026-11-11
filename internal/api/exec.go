package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/tigeregit/peer-task-control/internal/dispatch"
	"github.com/tigeregit/peer-task-control/internal/taskboard"
)

func (s *Server) exec(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("target")
	if target == "" {
		http.Error(w, "pass target=<node name or peer id>", http.StatusBadRequest)
		return
	}
	if target == "auto" {
		http.Error(w, "resolve --target auto in the CLI before contacting the daemon", http.StatusBadRequest)
		return
	}
	follow := r.URL.Query().Get("follow") == "1" || r.URL.Query().Get("follow") == "true"
	bundle, err := io.ReadAll(io.LimitReader(r.Body, dispatch.MaxBundleBytes+1))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if int64(len(bundle)) > dispatch.MaxBundleBytes {
		http.Error(w, "bundle too large", http.StatusRequestEntityTooLarge)
		return
	}
	entry, ok := s.Node.FindPeer(target)
	if !ok {
		http.Error(w, "node "+target+" not found", http.StatusNotFound)
		return
	}
	local := s.Node.LocalEntry()
	sub := taskboard.Party{Node: local.Name, PeerID: local.PeerID}
	if follow {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "follow requires a flushable connection", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		_, err := s.Node.Dispatch(r.Context(), entry, bundle, true, sub, func(ev dispatch.Event) {
			b, _ := json.Marshal(ev)
			_, _ = w.Write(append(b, '\n'))
			flusher.Flush()
		})
		if err != nil {
			b, _ := json.Marshal(dispatch.Event{Type: "error", Error: err.Error()})
			_, _ = w.Write(append(b, '\n'))
			flusher.Flush()
		}
		return
	}
	ev, err := s.Node.Dispatch(r.Context(), entry, bundle, false, sub, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, ev)
}

func (s *Server) queue(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"jobs": s.Node.CollectQueue(r.Context())})
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.ID == "" {
		http.Error(w, `pass {"id":"<run-id or job-id>"}`, http.StatusBadRequest)
		return
	}
	status, err := s.Node.Cancel(r.Context(), in.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status, "id": in.ID})
}
