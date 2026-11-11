package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type ScanRequest struct {
	Node string `json:"node,omitempty"`
}

type ScanResponse struct {
	Node              string `json:"node"`
	PeerID            string `json:"peer_id"`
	Status            string `json:"status"`
	RetryAfterSeconds int64  `json:"retry_after_seconds,omitempty"`
	Error             string `json:"error,omitempty"`
}

func (r ScanResponse) HTTPStatus() int {
	switch r.Status {
	case "accepted":
		return http.StatusAccepted
	case "busy":
		return http.StatusConflict
	case "rate_limited":
		return http.StatusTooManyRequests
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

type ScanError struct {
	Reply ScanResponse
}

func (e *ScanError) Error() string {
	message := fmt.Sprintf("scan on %s: %s", e.Reply.Node, e.Reply.Error)
	if e.Reply.RetryAfterSeconds > 0 {
		message += fmt.Sprintf("; retry after %ds", e.Reply.RetryAfterSeconds)
	}
	return message
}

func (c *Client) Scan(ctx context.Context, node string) (ScanResponse, error) {
	var reply ScanResponse
	body, err := json.Marshal(ScanRequest{Node: node})
	if err != nil {
		return reply, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+"/api/v1/scan", bytes.NewReader(body))
	if err != nil {
		return reply, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return reply, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return reply, err
	}
	if err := json.Unmarshal(payload, &reply); err != nil {
		if resp.StatusCode >= 300 {
			return reply, fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(payload))
		}
		return reply, fmt.Errorf("scan response (%s): %w", resp.Status, err)
	}
	if resp.StatusCode != reply.HTTPStatus() {
		return reply, fmt.Errorf("inconsistent scan response: %s, status %q", resp.Status, reply.Status)
	}
	if reply.Status != "accepted" {
		return reply, &ScanError{Reply: reply}
	}
	return reply, nil
}
