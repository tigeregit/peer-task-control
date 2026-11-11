package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tigeregit/peer-task-control/internal/worktreeguard"
)

type Reservation struct {
	ID             string   `json:"id"`
	Digests        []string `json:"digests"`
	SeedBytes      int64    `json:"seed_bytes"`
	WorkspaceBytes int64    `json:"workspace_bytes"`
	Workspace      string   `json:"workspace"`
	Package        string   `json:"package"`
}

type contextKey struct{}

func WithReservation(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

func ReservationID(ctx context.Context) string {
	id, _ := ctx.Value(contextKey{}).(string)
	return id
}

func Lock(root string) (*worktreeguard.Guard, error) {
	dir := filepath.Join(root, ".quota-lock")
	return worktreeguard.TryLock(dir, dir)
}

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

func reservationDir(root string) string { return filepath.Join(root, ".reservations") }

func validID(id string) bool {
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id && !strings.ContainsAny(id, `\/:`)
}

func Start(root string, r Reservation) (*worktreeguard.Guard, error) {
	if !validID(r.ID) {
		return nil, fmt.Errorf("invalid reservation ID")
	}
	g, err := worktreeguard.TryLock(reservationDir(root), filepath.Join(reservationDir(root), r.ID))
	if err != nil {
		return nil, err
	}
	if err := SaveReservation(root, r); err != nil {
		return nil, errors.Join(err, g.Close())
	}
	return g, nil
}

func SaveReservation(root string, r Reservation) error {
	if !validID(r.ID) || r.SeedBytes < 0 || r.WorkspaceBytes < 0 {
		return fmt.Errorf("invalid storage reservation")
	}
	path := filepath.Join(reservationDir(root), r.ID+".json")
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".reservation-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func End(root, id string) error {
	if !validID(id) {
		return fmt.Errorf("invalid reservation ID")
	}
	err := os.Remove(filepath.Join(reservationDir(root), id+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Active ignores crashed runners only after acquiring their OS lock. JSON
// without a live runner is not a permanent quota reservation.
func Active(root string) ([]Reservation, error) {
	dir := reservationDir(root)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Reservation
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := e.Name()[:len(e.Name())-5]
		g, err := worktreeguard.TryLock(dir, filepath.Join(dir, id))
		if err == nil {
			err = errors.Join(End(root, id), g.Close())
			if err != nil {
				return nil, err
			}
			continue
		}
		if !errors.Is(err, worktreeguard.ErrBusy) {
			return nil, err
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		var r Reservation
		if err := json.Unmarshal(b, &r); err != nil || r.ID != id || !validID(id) || r.SeedBytes < 0 || r.WorkspaceBytes < 0 {
			return nil, fmt.Errorf("invalid active storage reservation %s: %v", id, err)
		}
		out = append(out, r)
	}
	return out, nil
}
