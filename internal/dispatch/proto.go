package dispatch

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/tigeregit/peer-task-control/internal/taskboard"
)

const (
	// ProtocolID is the libp2p stream protocol for mesh dispatch.
	ProtocolID = "/ptc/exec/1.0.0"
	// ProtocolQueue lists or cancels jobs on a peer.
	ProtocolQueue = "/ptc/queue/1.0.0"
)

// Request is the JSON line the submitter writes before the bundle tar.
type Request struct {
	Type      string          `json:"type"` // submit
	JobID     string          `json:"job_id,omitempty"`
	JobName   string          `json:"job_name,omitempty"`
	Follow    bool            `json:"follow,omitempty"`
	Timeout   string          `json:"timeout,omitempty"`
	Size      int64           `json:"size"`
	Submitter taskboard.Party `json:"submitter"`
}

// Event is a JSON line the target writes back (accepted, log, status, done, error).
type Event struct {
	Type    string `json:"type"`
	JobID   string `json:"job_id,omitempty"`
	RunID   string `json:"run_id,omitempty"`
	Status  string `json:"status,omitempty"`
	Digest  string `json:"digest,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	Queued  int    `json:"queued,omitempty"`
	At      string `json:"at,omitempty"`
}

// QueueMessage is the JSON request/reply on ProtocolQueue.
type QueueMessage struct {
	Type   string      `json:"type"` // list, cancel, reply
	RunID  string      `json:"run_id,omitempty"`
	Node   string      `json:"node,omitempty"`
	PeerID string      `json:"peer_id,omitempty"`
	Jobs   []QueueItem `json:"jobs,omitempty"`
	Error  string      `json:"error,omitempty"`
}

// QueueItem is one job in a node's queue or currently running.
type QueueItem struct {
	JobID     string          `json:"job_id"`
	RunID     string          `json:"run_id,omitempty"`
	JobName   string          `json:"job_name,omitempty"`
	Status    string          `json:"status"`
	Node      string          `json:"node,omitempty"`
	PeerID    string          `json:"peer_id,omitempty"`
	QueuedAt  *time.Time      `json:"queued_at,omitempty"`
	StartedAt *time.Time      `json:"started_at,omitempty"`
	EndedAt   *time.Time      `json:"ended_at,omitempty"`
	Submitter taskboard.Party `json:"submitter,omitempty"`
	Digest    string          `json:"digest,omitempty"`
	Error     string          `json:"error,omitempty"`
}

func WriteLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

func ReadLine(r io.Reader) ([]byte, *bufio.Reader, error) {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	line, err := br.ReadBytes('\n')
	return line, br, err
}

func DecodeLine[T any](line []byte, out *T) error {
	if err := json.Unmarshal(line, out); err != nil {
		return fmt.Errorf("protocol message: %w", err)
	}
	return nil
}
