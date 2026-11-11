// Package retention stores durable promises to keep a replica for another peer.
// Promises do not expire: a partition must never authorize both replicas to delete.
package retention

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/tigeregit/peer-task-control/internal/worktreeguard"
)

var tokenPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

type Record struct {
	Token    string `json:"token"`
	Digest   string `json:"digest"`
	Peer     string `json:"peer"`
	Released bool   `json:"released,omitempty"`
}

func Lock(root string) (*worktreeguard.Guard, error) {
	dir := filepath.Join(root, ".retention-lock")
	return worktreeguard.TryLock(dir, dir)
}

// Acquire is for local writers only. Distributed hold handlers must use the
// nonblocking Lock, otherwise simultaneous collectors can deadlock each other.
func Acquire(ctx context.Context, root string) (*worktreeguard.Guard, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		guard, err := Lock(root)
		if !errors.Is(err, worktreeguard.ErrBusy) {
			return guard, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func directory(root string, outgoing bool) string {
	if outgoing {
		return filepath.Join(root, ".retention", "outgoing")
	}
	return filepath.Join(root, ".retention", "promises")
}

// Save must be called under Lock. Released records are durable tombstones:
// a delayed hold request cannot resurrect an already released promise.
func Save(root string, r Record, outgoing bool) error {
	if !tokenPattern.MatchString(r.Token) {
		return fmt.Errorf("invalid retention token")
	}
	dir := directory(root, outgoing)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".promise-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Sync(); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Close(); err != nil {
		return err
	}
	return commit(f.Name(), filepath.Join(dir, r.Token+".json"))
}

func Records(root string, outgoing bool) ([]Record, error) {
	dir := directory(root, outgoing)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var r Record
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, fmt.Errorf("corrupt retention promise %s: %w", entry.Name(), err)
		}
		if !tokenPattern.MatchString(r.Token) || entry.Name() != r.Token+".json" || r.Peer == "" || r.Digest == "" {
			return nil, fmt.Errorf("invalid retention promise %s", entry.Name())
		}
		out = append(out, r)
	}
	return out, nil
}

func Held(root, digest string) (bool, error) {
	rs, err := Records(root, false)
	if err != nil {
		return false, err
	}
	for _, r := range rs {
		if !r.Released && r.Digest == digest {
			return true, nil
		}
	}
	return false, nil
}

func Forget(root, token string) error {
	if !tokenPattern.MatchString(token) {
		return fmt.Errorf("invalid retention token")
	}
	err := os.Remove(filepath.Join(directory(root, true), token+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
