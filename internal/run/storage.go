package run

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/tigeregit/peer-task-control/internal/cas"
	"github.com/tigeregit/peer-task-control/internal/disk"
	"github.com/tigeregit/peer-task-control/internal/quota"
	"github.com/tigeregit/peer-task-control/internal/resource"
	"github.com/tigeregit/peer-task-control/internal/retention"
	"github.com/tigeregit/peer-task-control/internal/workflow"
)

func (opt Options) storageLimits() (int64, int64, error) {
	free, err := disk.Free(opt.seedsDir())
	if err != nil {
		return 0, 0, err
	}
	size, deletion := int64(opt.Storage.SizeLimit), int64(opt.Storage.DeleteSize)
	if size == 0 {
		size, deletion = 40*quota.GB, 20*quota.GB
	}
	limit, deletion := quota.Adjust(size, deletion, free)
	return limit, deletion, nil
}

func (opt Options) reserveStorage(loaded workflow.Loaded, workRoot, pkg string) (quota.Reservation, func() error, error) {
	r := quota.Reservation{ID: opt.RunID, Workspace: workRoot, Package: pkg}
	lock, err := quota.Acquire(opt.Context, opt.seedsDir())
	if err != nil {
		return r, nil, fmt.Errorf("another storage admission is in progress: %w", err)
	}
	defer lock.Close()
	mutation, err := retention.Acquire(opt.Context, opt.seedsDir())
	if err != nil {
		return r, nil, fmt.Errorf("seed maintenance is in progress: %w", err)
	}
	defer mutation.Close()
	limit, _, err := opt.storageLimits()
	if err != nil {
		return r, nil, err
	}
	var input int64
	seen := map[string]bool{}
	for _, seed := range loaded.Prepare.Seeds {
		digest, local := seed.Digest, ""
		if override, ok := opt.SeedOverrides[seed.Name]; ok {
			digest, local = override.Digest, override.Dir
		} else if seed.Local != nil {
			local = seed.Local.Src
			if !filepath.IsAbs(local) {
				local = filepath.Join(loaded.Dir, local)
			}
		}
		var size int64
		var inspectErr error
		if local != "" {
			if _, err := os.Stat(local); err != nil {
				if os.IsNotExist(err) && digest != "" {
					local = ""
				} else {
					inspectErr = err
				}
			}
		}
		if local != "" {
			if inspectErr == nil {
				size, inspectErr = quota.Size(local)
			}
			if inspectErr == nil && digest == "" {
				st, err := os.Stat(local)
				if err != nil {
					return r, nil, err
				}
				if st.IsDir() {
					digest, _, inspectErr = cas.DirDigest(local)
				} else if inspectErr == nil {
					digest, inspectErr = fileSHA256(local)
				}
			}
		} else {
			_, _, s, ok := resource.FindByDigest(opt.packagesDir(), opt.casDir(), digest)
			if ok {
				size = s.Size
			} else if opt.InspectResource != nil {
				var info resource.Info
				info, inspectErr = opt.InspectResource(opt.Context, digest)
				size = info.Size
			} else {
				inspectErr = fmt.Errorf("cannot inspect seed %s before downloading", digest)
			}
		}
		if inspectErr != nil {
			if !seed.IsRequired() {
				opt.skipSeeds[seed.Name] = true
				continue
			}
			return r, nil, fmt.Errorf("prepare storage admission: %s: %w", seed.Name, inspectErr)
		}
		if size < 0 || size > math.MaxInt64-input {
			return r, nil, fmt.Errorf("invalid input size for %s", seed.Name)
		}
		input += size
		if input >= limit {
			return r, nil, fmt.Errorf("prepare inputs need %d bytes, effective seeding limit is %d; refusing before download", input, limit)
		}
		if !seen[digest] {
			seen[digest] = true
			r.Digests = append(r.Digests, digest)
			if _, _, _, exists := resource.FindByDigest(opt.packagesDir(), opt.casDir(), digest); !exists {
				// Remote data needs a verified copy and a block cache. Leave room
				// for manifests and tar headers without double-counting local inputs.
				factor := int64(3)
				if local != "" {
					factor = 1
				}
				if size > (math.MaxInt64-r.SeedBytes-1_048_576)/factor {
					return r, nil, fmt.Errorf("input storage reservation overflow")
				}
				r.SeedBytes += factor*size + 1_048_576
			}
		}
	}
	if !loaded.SeedOnly {
		r.WorkspaceBytes = input
	}
	active, err := quota.Active(opt.seedsDir())
	if err != nil {
		return r, nil, err
	}
	if opt.Storage.WorkspaceLimit > 0 && !loaded.SeedOnly {
		used, err := quota.WorkspaceUsage(opt.worktreesDir())
		if err != nil {
			return r, nil, err
		}
		for _, other := range active {
			actual, err := quota.WorkspaceUsage(other.Workspace)
			if err != nil {
				return r, nil, err
			}
			extra := max(0, other.WorkspaceBytes-actual)
			if extra > math.MaxInt64-used {
				return r, nil, fmt.Errorf("workspace reservation size overflow")
			}
			used += extra
		}
		if input > int64(opt.Storage.WorkspaceLimit)-used {
			return r, nil, fmt.Errorf("workspace quota: %d bytes required plus %d in use/reserved, limit %d", input, used, opt.Storage.WorkspaceLimit)
		}
	}
	guard, err := quota.Start(opt.seedsDir(), r)
	if err != nil {
		return r, nil, err
	}
	release := func() error {
		return errors.Join(quota.End(opt.seedsDir(), r.ID), guard.Close())
	}
	if err := mutation.Close(); err != nil {
		return r, nil, errors.Join(err, release())
	}
	if opt.EnsureSpace != nil {
		err = opt.EnsureSpace(quota.WithReservation(opt.Context, r.ID), r.SeedBytes)
	} else {
		var used int64
		used, err = quota.SeedUsage(opt.seedsDir())
		for _, other := range active {
			if other.SeedBytes > math.MaxInt64-used {
				err = fmt.Errorf("seeding reservation size overflow")
				break
			}
			used += other.SeedBytes
		}
		if err == nil && (r.SeedBytes >= limit || used >= limit-r.SeedBytes) {
			err = fmt.Errorf("seeding quota: %d used/reserved plus %d incoming, effective limit %d; a running daemon is required for safe network cleanup", used, r.SeedBytes, limit)
		}
	}
	if err != nil {
		return r, nil, errors.Join(err, release())
	}
	return r, release, nil
}

func (opt Options) monitorStorage(cancel context.CancelCauseFunc) func() {
	ctx, stop := context.WithCancel(opt.Context)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		ticks := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if opt.Storage.WorkspaceLimit > 0 {
				used, err := quota.WorkspaceUsage(opt.worktreesDir())
				if err != nil {
					cancel(err)
					return
				}
				if used > int64(opt.Storage.WorkspaceLimit) {
					cancel(fmt.Errorf("aggregate workspace quota exceeded: %d > %d", used, opt.Storage.WorkspaceLimit))
					return
				}
			}
			ticks++
			if ticks%4 != 0 {
				continue
			}
			used, err := quota.SeedUsage(opt.seedsDir())
			if err != nil {
				cancel(err)
				return
			}
			limit, _, err := opt.storageLimits()
			if err != nil {
				cancel(err)
				return
			}
			if used >= limit {
				if opt.EnsureSpace == nil {
					cancel(fmt.Errorf("seeding quota exceeded: %d >= %d", used, limit))
					return
				}
				attempt, done := context.WithTimeout(ctx, 3*time.Second)
				err := opt.EnsureSpace(attempt, 0)
				done()
				if err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	return func() { stop(); <-done }
}

func (opt Options) checkStorage() error {
	if opt.Storage.WorkspaceLimit > 0 {
		used, err := quota.WorkspaceUsage(opt.worktreesDir())
		if err != nil {
			return err
		}
		if used > int64(opt.Storage.WorkspaceLimit) {
			return fmt.Errorf("aggregate workspace quota exceeded: %d > %d", used, opt.Storage.WorkspaceLimit)
		}
	}
	used, err := quota.SeedUsage(opt.seedsDir())
	if err != nil {
		return err
	}
	limit, _, err := opt.storageLimits()
	if err != nil {
		return err
	}
	if used < limit {
		return nil
	}
	if opt.EnsureSpace != nil {
		return opt.EnsureSpace(opt.Context, 0)
	}
	return fmt.Errorf("seeding quota exceeded: %d >= %d", used, limit)
}
