package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/tigeregit/peer-task-control/internal/clean"
)

// CleanRequest is a local or remote cleanup request issued by the CLI.
type CleanRequest struct {
	Node   string   `json:"node,omitempty"`
	Kind   string   `json:"kind"`
	IDs    []string `json:"ids,omitempty"`
	Match  string   `json:"match,omitempty"`
	DryRun bool     `json:"dry_run,omitempty"`
	Force  bool     `json:"force,omitempty"`
	Yes    bool     `json:"yes,omitempty"`
}

// CleanResponse is the outcome of a clean request.
type CleanResponse struct {
	Node   string       `json:"node"`
	PeerID string       `json:"peer_id"`
	Status string       `json:"status"`
	Error  string       `json:"error,omitempty"`
	DryRun bool         `json:"dry_run,omitempty"`
	Plans  []clean.Plan `json:"plans,omitempty"`
	Bytes  int64        `json:"bytes,omitempty"`
	Freed  int64        `json:"freed,omitempty"`
}

func (r CleanResponse) HTTPStatus() int {
	switch r.Status {
	case "ok":
		return http.StatusOK
	case "denied":
		return http.StatusForbidden
	case "not_found":
		return http.StatusNotFound
	case "unavailable":
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}

// CleanError is a structured refusal or failure of a clean request.
type CleanError struct {
	Reply CleanResponse
}

func (e *CleanError) Error() string {
	node := e.Reply.Node
	if node == "" {
		node = "node"
	}
	if e.Reply.Error != "" {
		return fmt.Sprintf("clean on %s: %s", node, e.Reply.Error)
	}
	return fmt.Sprintf("clean on %s: %s", node, e.Reply.Status)
}

// Clean asks the local daemon to clean this node or a named peer.
func (c *Client) Clean(ctx context.Context, req CleanRequest) (CleanResponse, error) {
	var reply CleanResponse
	body, err := json.Marshal(req)
	if err != nil {
		return reply, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/api/v1/clean", bytes.NewReader(body))
	if err != nil {
		return reply, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	client := c.withTimeout(10 * time.Minute)
	resp, err := client.HTTP.Do(httpReq)
	if err != nil {
		return reply, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return reply, err
	}
	if err := json.Unmarshal(payload, &reply); err != nil {
		if resp.StatusCode >= 300 {
			return reply, fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(payload))
		}
		return reply, fmt.Errorf("clean response (%s): %w", resp.Status, err)
	}
	if resp.StatusCode != reply.HTTPStatus() {
		return reply, fmt.Errorf("inconsistent clean response: %s, status %q", resp.Status, reply.Status)
	}
	if reply.Status != "ok" {
		return reply, &CleanError{Reply: reply}
	}
	return reply, nil
}
