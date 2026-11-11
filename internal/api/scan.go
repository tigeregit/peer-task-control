package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/tigeregit/peer-task-control/internal/control"
)

func (s *Server) scan(w http.ResponseWriter, r *http.Request) {
	var req control.ScanRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "invalid scan request", http.StatusBadRequest)
		return
	}
	node, ok := s.Node.(interface {
		Scan(context.Context, string) (control.ScanResponse, error)
	})
	if !ok {
		http.Error(w, "scan control is unavailable", http.StatusNotImplemented)
		return
	}
	reply, err := node.Scan(r.Context(), req.Node)
	if err != nil && reply.Status == "" {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if reply.RetryAfterSeconds > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(reply.RetryAfterSeconds, 10))
	}
	writeJSON(w, reply.HTTPStatus(), reply)
}
