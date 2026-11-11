# Storage, quotas, and clean

Seeds and worktrees are **different stores**. Cleaning a worktree never
deletes CAS/packages.

Storage paths (`storage.seeds_dir`, `storage.worktrees_dir`,
`storage.runs_dir`) default under `data_dir` and can be overridden with
`--seeds-dir` / `--worktrees-dir` / `--runs-dir` or `PTC_*_DIR`. Windows
may put worktrees on a fast drive such as `D:\ptc\worktrees`.

## Quotas

Storage quota defaults: `storage.size_limit: 40GB`, `delete_size: 20GB`
(half the limit when omitted), `workspace_limit: 0` (unlimited aggregate
workspace size). Packages, outputs, CAS, blocks and fetch staging share the
seeding quota; outputs are ordinary seeds, not exempt data. If free space
is below the configured limit, effective limit is `max(0, free - 1GB)` and
deletion scales proportionally: 40/20GB with 11GB free becomes 10/5GB.

GC deletes only after another compatible files peer verifies and durably
promises to retain a complete replica; unavailable or legacy peers cannot
authorize deletion. Promises have no expiry; do not remove coordination
metadata. Pin and active jobs remain protected, including against manual
`--force`.

Prepare checks all input sizes and concurrent reservations before
downloading; oversized inputs fail the job without a download/cleanup loop.
Cache/staging overhead also needs capacity. A daemon is required to
negotiate safe network cleanup. Workspace roots must be isolated from
actual seeding/record/coordination directories, and custom layout roots
must be strict children of the configured workspace root. Materialization
uses private copies, never writable hard links into seeds. All terminal
workspaces (success/failure/cancel/Prepare failure) are reclaimed before
releasing the slot; normal jobs reject `--keep-worktree`, and YAML cleanup
settings cannot retain terminal workspaces. Runtime checks are
application-level polling (workspace about 250ms, seeds about 1s), not
filesystem hard quotas; transient overshoot is possible. Unique/protected
data is never deleted just to meet a target; capacity failure is explicit.
Run/queue history is not automatically removed by this policy.

The persistent `seeds_dir/blocks` cache has no GC; `ptc clean resource`
does not reclaim it. Cache and staging count toward seed disk usage.

## `ptc clean` (local only)

`ptc clean outputs|runs|resource|worktrees` reclaims local data. Use
`--dry-run` first, then `--yes`. Pinned items need `--force`. Seeds
referenced by queued/running jobs are refused. There is no remote
`ptc clean --node`.

Directory-only `data.pin_permanent: true` protects replicas from normal
clean; blob pin is not supported.

Active worktrees are protected from `clean worktrees` by process locks.
Drain tasks started by older versions before cleaning, and do not remove
the reserved `.ptc-locks` directory.

Disk pressure: `ptc peers show <name> --json` shows real volume totals and
ptc usage. See [docs/CLI.md](docs/CLI.md#全局存储规则).
