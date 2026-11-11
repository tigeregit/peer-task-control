package node

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/tigeregit/peer-task-control/internal/disk"
	"github.com/tigeregit/peer-task-control/internal/quota"
	"github.com/tigeregit/peer-task-control/internal/resource"
	"github.com/tigeregit/peer-task-control/internal/retention"
	"github.com/tigeregit/peer-task-control/internal/run"
	"github.com/tigeregit/peer-task-control/internal/taskboard"
	"github.com/tigeregit/peer-task-control/internal/worktreeguard"
)

type storageState struct {
	mu   sync.Mutex
	done chan struct{}
}

func (n *Node) storageLimits() (int64, int64, error) {
	free, err := disk.Free(n.Cfg.SeedsDir())
	if err != nil {
		return 0, 0, err
	}
	limit, deletion := quota.Adjust(int64(n.Cfg.Storage.SizeLimit), int64(n.Cfg.Storage.DeleteSize), free)
	return limit, deletion, nil
}

func (n *Node) ensureSeedSpace(ctx context.Context, incoming int64) error {
	for !n.storage.mu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	defer n.storage.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	limit, deletion, err := n.storageLimits()
	if err != nil {
		return err
	}
	if incoming < 0 || (incoming > 0 && incoming >= limit) {
		return fmt.Errorf("seeding quota: incoming %d bytes cannot fit effective limit %d", incoming, limit)
	}
	used, err := quota.SeedUsage(n.Cfg.SeedsDir())
	if err != nil {
		return err
	}
	reservations, err := quota.Active(n.Cfg.SeedsDir())
	if err != nil {
		return err
	}
	for _, r := range reservations {
		if r.ID != quota.ReservationID(ctx) {
			if r.SeedBytes > math.MaxInt64-incoming {
				return fmt.Errorf("seeding reservation size overflow")
			}
			incoming += r.SeedBytes
		}
	}
	if limit == 0 && incoming == 0 {
		if used == 0 {
			return nil
		}
		return n.collectSeeds(ctx, used, 1)
	}
	if incoming >= limit {
		return fmt.Errorf("seeding quota: active jobs reserve %d bytes, effective limit %d", incoming, limit)
	}
	if used < limit-incoming {
		return nil
	}
	goal := max(deletion, used-(limit-incoming)+1)
	return n.collectSeeds(ctx, goal, limit-incoming)
}

func (n *Node) collectSeeds(ctx context.Context, goal, ceiling int64) error {
	guard, err := retention.Lock(n.Cfg.SeedsDir())
	if err != nil {
		return fmt.Errorf("seeding maintenance is busy: %w", err)
	}
	defer guard.Close()
	before, err := quota.SeedUsage(n.Cfg.SeedsDir())
	if err != nil {
		return err
	}
	active, err := quota.Active(n.Cfg.SeedsDir())
	if err != nil {
		return err
	}
	protected := map[string]bool{}
	for _, r := range active {
		for _, digest := range r.Digests {
			protected[digest] = true
		}
	}
	pending, err := retention.Records(n.Cfg.SeedsDir(), true)
	if err != nil {
		return err
	}
	unreleased := map[string]bool{}
	for _, record := range pending {
		unreleased[record.Digest+"|"+record.Peer] = true
	}
	infos := n.CollectResources(ctx)
	sort.SliceStable(infos, func(i, j int) bool {
		a, b := time.Time{}, time.Time{}
		if infos[i].CreatedAt != nil {
			a = *infos[i].CreatedAt
		}
		if infos[j].CreatedAt != nil {
			b = *infos[j].CreatedAt
		}
		if a.Equal(b) {
			return infos[i].Digest < infos[j].Digest
		}
		return a.Before(b)
	})
	for _, info := range infos {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, path, local, ok := resource.FindByDigest(n.Cfg.PackagesDir(), n.Cfg.CASDir(), info.Digest)
		if !ok || local.Pin || protected[info.Digest] {
			continue
		}
		for _, r := range active {
			if r.Package != "" {
				same, err := worktreeguard.Same(path, r.Package)
				if err != nil {
					return err
				}
				if same {
					protected[info.Digest] = true
				}
			}
		}
		if protected[info.Digest] {
			continue
		}
		held, err := retention.Held(n.Cfg.SeedsDir(), info.Digest)
		if err != nil {
			return err
		}
		if held {
			continue
		}
		for _, loc := range info.Locations {
			if loc.Local || loc.PeerID == n.Host.ID().String() || loc.PeerID == "" || unreleased[info.Digest+"|"+loc.PeerID] {
				continue
			}
			var token [16]byte
			if _, err := rand.Read(token[:]); err != nil {
				return err
			}
			rec := retention.Record{Token: hex.EncodeToString(token[:]), Digest: info.Digest, Peer: loc.PeerID}
			if err := retention.Save(n.Cfg.SeedsDir(), rec, true); err != nil {
				return err
			}
			err := n.sendRetention(ctx, rec, "hold")
			if err == nil {
				err = resource.DeleteLocal(n.Cfg.PackagesDir(), n.Cfg.CASDir(), info.Digest)
				if err == nil {
					n.Log.Info("auto-clean removed redundant seed", "digest", info.Digest, "keeper", loc.PeerID)
					roots := []string{}
					for _, s := range resource.InventoryAt(n.Cfg.PackagesDir(), n.Cfg.CASDir()) {
						if s.CID != "" {
							roots = append(roots, s.CID)
						}
					}

					if _, pruneErr := n.blocks.Prune(ctx, roots); pruneErr != nil {
						n.Log.Warn("block-cache cleanup deferred", "err", pruneErr)
					}
				}
			}
			releaseErr := n.sendRetention(ctx, rec, "release")
			if releaseErr == nil {
				releaseErr = retention.Forget(n.Cfg.SeedsDir(), rec.Token)
			}
			if releaseErr != nil {
				unreleased[rec.Digest+"|"+rec.Peer] = true
				n.Log.Warn("replica promise release pending; keeper remains protected", "digest", rec.Digest, "err", releaseErr)
			}
			if err != nil {
				n.Log.Info("redundant seed was not removable", "digest", info.Digest, "peer", loc.PeerID, "err", err)
				continue
			}
			break
		}
		used, err := quota.SeedUsage(n.Cfg.SeedsDir())
		if err != nil {
			return err
		}
		if before-used >= goal && used < ceiling {
			n.bumpLocal()
			return nil
		}
	}
	after, err := quota.SeedUsage(n.Cfg.SeedsDir())
	if err != nil {
		return err
	}
	return fmt.Errorf("seeding quota: safely freed %d bytes, require at least %d; unique, pinned, active or retained resources cannot be removed (used=%d, ceiling=%d)",
		before-after, goal, after, ceiling)
}

func (n *Node) storageLoop() {
	defer close(n.storage.done)
	if n.ctx.Err() != nil {
		return
	}
	if err := n.reclaimTerminalWorkspaces(); err != nil {
		n.Log.Error("terminal workspace recovery failed", "err", err)
	}
	for {
		// Different nodes must not repeatedly enter the same lock negotiation
		// in unison. Contention remains fail-closed even without this jitter.
		var jitter [1]byte
		if _, err := rand.Read(jitter[:]); err != nil {
			n.Log.Error("storage maintenance jitter failed", "err", err)
			return
		}
		select {
		case <-n.ctx.Done():
			return
		case <-time.After(15*time.Second + time.Duration(jitter[0])*20*time.Millisecond):
		}
		if n.ctx.Err() != nil {
			return
		}
		n.storage.mu.Lock()
		err := n.recoverRetention(n.ctx)
		n.storage.mu.Unlock()
		if err != nil {
			n.Log.Warn("retention recovery pending", "err", err)
		}
		if err := n.ensureSeedSpace(n.ctx, 0); err != nil && !errors.Is(err, context.Canceled) {
			n.Log.Warn("automatic seeding cleanup could not reach target", "err", err)
		}
	}
}

func (n *Node) EnsureSeedSpace(ctx context.Context, incoming int64) error {
	return n.ensureSeedSpace(ctx, incoming)
}

func (n *Node) ValidateStorageRoots(seeds, worktrees string) error {
	for _, pair := range [][2]string{{seeds, n.Cfg.SeedsDir()}, {worktrees, n.Cfg.WorktreesDir()}} {
		if pair[0] == "" {
			return fmt.Errorf("storage admission must identify local storage roots")
		}
		same, err := worktreeguard.Same(pair[0], pair[1])
		if err != nil {
			return err
		}
		if !same {
			return fmt.Errorf("CLI storage root %s does not match daemon root %s; use the corresponding daemon/config", pair[0], pair[1])
		}
	}
	return nil
}

func (n *Node) reserveTransfer(ctx context.Context, digest string, incoming int64) (context.Context, func() error, error) {
	lock, err := quota.Acquire(ctx, n.Cfg.SeedsDir())
	if err != nil {
		return nil, nil, fmt.Errorf("%w: admission busy: %v", quota.ErrExceeded, err)
	}
	defer lock.Close()
	mutation, err := retention.Acquire(ctx, n.Cfg.SeedsDir())
	if err != nil {
		return nil, nil, fmt.Errorf("%w: maintenance busy: %v", quota.ErrExceeded, err)
	}
	defer mutation.Close()
	id, err := run.TaskID(time.Now(), "transfer")
	if err != nil {
		return nil, nil, err
	}
	guard, err := quota.Start(n.Cfg.SeedsDir(), quota.Reservation{ID: id, Digests: []string{digest}, SeedBytes: incoming})
	if err != nil {
		return nil, nil, err
	}
	release := func() error { return errors.Join(quota.End(n.Cfg.SeedsDir(), id), guard.Close()) }
	next := quota.WithReservation(ctx, id)
	err = mutation.Close()
	if err == nil {
		err = n.ensureSeedSpace(next, incoming)
	}
	if err == nil {
		err = lock.Close()
	}
	if err != nil {
		return nil, nil, errors.Join(fmt.Errorf("%w: %v", quota.ErrExceeded, err), release())
	}
	return next, release, nil
}

func (n *Node) discardUpdateStage(path string) {
	guard, err := retention.Lock(n.Cfg.SeedsDir())
	if err != nil {
		n.Log.Warn("update staging retained during storage maintenance", "path", path, "err", err)
		return
	}
	defer guard.Close()
	digest := resource.Normalize(filepath.Base(path))
	if digest == "" {
		n.Log.Error("refusing to remove unrecognized update staging", "path", path)
		return
	}
	held, err := retention.Held(n.Cfg.SeedsDir(), digest)
	if err != nil || held {
		n.Log.Info("update staging retained for replica promise", "digest", digest, "err", err)
		return
	}
	active, err := quota.Active(n.Cfg.SeedsDir())
	if err != nil {
		n.Log.Error("cannot inspect update staging reservations", "err", err)
		return
	}
	for _, reservation := range active {
		for _, input := range reservation.Digests {
			if input == digest {
				n.Log.Info("update staging retained for active transfer", "digest", digest)
				return
			}
		}
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		n.Log.Error("remove update staging", "path", path, "err", err)
		return
	}
	if err := resource.SetCID(path, ""); err != nil {
		n.Log.Error("remove update staging CID", "path", path, "err", err)
	}
}

func (n *Node) reclaimTerminalWorkspaces() error {
	var result error
	for _, task := range taskboard.LoadDir(n.Cfg.RunsDir()) {
		if task.Worktree == nil || task.Worktree.Root == "" {
			continue
		}
		switch task.Status {
		case "success", "failure", "cancelled", "canceled":
		default:
			continue
		}
		path := task.Worktree.Root
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			result = errors.Join(result, err)
			continue
		}
		if err := worktreeguard.Child(n.Cfg.WorktreesDir(), path); err != nil {
			result = errors.Join(result, err)
			continue
		}
		containsLocks, err := worktreeguard.ContainsLockDir(n.Cfg.WorktreesDir(), path)
		if err != nil || containsLocks {
			result = errors.Join(result, fmt.Errorf("unsafe terminal workspace %s: contains locks=%v: %v", path, containsLocks, err))
			continue
		}
		guard, err := worktreeguard.TryLock(n.Cfg.WorktreesDir(), path)
		if errors.Is(err, worktreeguard.ErrBusy) {
			continue
		}
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		result = errors.Join(result, os.RemoveAll(path), guard.Close())
	}
	return result
}

func (n *Node) recoverRetention(ctx context.Context) error {
	guard, err := retention.Lock(n.Cfg.SeedsDir())
	if err != nil {
		return err
	}
	defer guard.Close()
	records, err := retention.Records(n.Cfg.SeedsDir(), true)
	if err != nil {
		return err
	}
	var result error
	for _, rec := range records {
		if err := n.sendRetention(ctx, rec, "release"); err != nil {
			result = errors.Join(result, err)
		} else {
			result = errors.Join(result, retention.Forget(n.Cfg.SeedsDir(), rec.Token))
		}
	}
	return result
}
