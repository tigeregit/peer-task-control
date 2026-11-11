# Error recovery

1. Version mismatch → install a matching binary; run `ptc skill sync` and
   reread the installed skill (or the `references/` file you need).
2. `daemon lock not found` → start `ptc daemon --config config.yaml` with
   the same `--data-dir`.
3. Target not in the catalog → wait for a scan/relay push; check
   `trusted_cidrs` and, across segments, that a `relay` gateway is up. Do not invent neighbor IPs. Nodes may use `scan_hosts` (e.g.
   `host.docker.internal` for the Windows `host` node, or `127.0.0.1:port`
   for another local node). For a NAT-mapped relay, forward both TCP and
   UDP on the mesh port. Nodes re-reserve on relay reconnection; check
   daemon warnings if reservation fails.
4. Placement / policy refusal → pick another node; show the error; do not
   `--force` a CUDA job onto a no-GPU machine. Never retry the same
   gateway or no-GPU target for a `require_nvidia` job.
5. `queue is full` or job sits in `queued` → `ptc queue --json`; wait or
   `ptc cancel`.
6. Digest missing on the target → confirm the submitter published it
   (`ptc resources --node <submitter>`) and the mesh resource protocol can
   reach that replica. To persist a local replica, `ptc resources pull
   <digest>`.
7. Disk pressure → `ptc peers show <name> --json` (real volume totals and
   ptc usage). Free space with `ptc clean … --dry-run` then `--yes`.
   Worktrees and seeds are **different stores**; cleaning a worktree never
   deletes CAS/packages. Pinned items and unique-on-network resources need
   `--force`. Seeds referenced by queued/running jobs are refused. To clean
   another node, use `ptc clean --node <name>` after that node enables
   `policy.remote_clean`.
8. Still stuck → read `docs/DESIGN.md`, `docs/CLI.md`,
   `docs/CONTRIBUTING.md`. Do not patch a running node out of tree.

More context: `references/discovery.md`, `references/storage.md`,
`references/resources.md`.
