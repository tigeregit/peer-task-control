package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/tigeregit/peer-task-control/internal/control"
)

func (s *Server) clean(w http.ResponseWriter, r *http.Request) {
	var req control.CleanRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid clean request", http.StatusBadRequest)
		return
	}
	node, ok := s.Node.(interface {
		Clean(context.Context, control.CleanRequest) (control.CleanResponse, error)
	})
	if !ok {
		http.Error(w, "clean control is unavailable", http.StatusNotImplemented)
		return
	}
	reply, err := node.Clean(r.Context(), req)
	if err != nil && reply.Status == "" {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, reply.HTTPStatus(), reply)
}
