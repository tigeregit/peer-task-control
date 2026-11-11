---
name: peer-task-control
description: >
  Dispatch CUDA and other workflows across a trusted-LAN ptc mesh.
  Use when the user wants to run ptc, submit or cancel a job, pick a GPU
  node, inspect tasks/resources/queue/disk, or clean local storage.
---

# peer-task-control

Version-bound: `ptc skill print` always prints the copy embedded in that binary and appends its actual `bound-to-version`. Check it against `ptc version`; do not use an old external SKILL.md after an update.

You help the user send programs (often CUDA) and test data to the right machine on a trusted LAN. Cross-segment traffic goes through a whitelist gateway that has no NVIDIA GPU and must never be an exec target for CUDA jobs.

## When to use

- Start or talk to a `ptc daemon`, list peers, or inspect policy.
- Run or dispatch a Prepare / run / Package result workflow with `ptc submit`.
- Retain resource replicas without a workspace or execution using a single `ptc/seed@v0` action.
- Watch, cancel, or clean jobs; list seeds; read the live board.
- Install a release over SSH or update a local/remote ptc binary.

Details, flags, exit codes, and the HTTP API live in [docs/CLI.md](docs/CLI.md). Do not invent commands that are not listed here or there.

## Core workflow

1. `ptc version` and, if acting as an agent, `ptc skill print` — confirm the skill matches the binary.
2. Ensure `ptc daemon --config config.yaml` is running on this machine (`trusted_cidrs` only; never ask the user for neighbor IPs).
3. `ptc peers --json` (or `ptc peers show <name> --json`) — wait until the catalog lists the intended node. Prefer `--json` over scraping tables (same idea as `gh` / `kubectl`).
4. Choose a target (see rules below). CUDA jobs go to an exec node with a real NVIDIA GPU (`host` on Windows, or the optional `cuda` Ubuntu container), never to `gateway` or a no-GPU worker.
5. `ptc submit --workflow <file> --target <name|peer-id|auto> --json` to dispatch; omit `--target` to run locally. Default is follow (stream until done). Use `--detach` only when the user wants to return immediately, then watch with `ptc queue` / `ptc tasks`.
6. After success, the result digest is in the JSON (and printed in follow mode). Fetch with `ptc resources get <digest> -o <file>` or `--fetch-result <dir>` on a followed submit.

WebUI (`ptc webui`) is **read-only**. Do not try to submit, cancel, change policy, or clean through the board. Those are CLI-only.

## Seed-only tasks (supported action)

Use a workflow whose only step is `uses: ptc/seed@v0`, with `with.file: seeds.yaml`.
That file must have `apiVersion: ptc/v0`, `kind: Seed` and a nonempty `seeds` list
of unique `name` plus `digest` (sha256) or `local.src`. `required` defaults true.
Do not add workspace, layout, outputs, dest, entrypoint, run, if or Package result.
This action fetches, verifies, retains and advertises the resources; it does not
construct a worktree, check GPU capabilities, execute commands or package results.
It still records a task and supports remote queue/follow/cancel. A remote target
needs files enabled and policy permission, not exec or NVIDIA. `--target auto`
selects a files node for this action; submit explicitly to different nodes to
increase replica count. It is not a continuous replication controller.

```text
ptc submit --workflow testdata/seed/workflow.yaml --dry-run --json
ptc submit --workflow testdata/seed/workflow.yaml --json
ptc submit --workflow docs/templates/seed-workflow.example.yaml --target replica-node --json
```

The testdata example uses local sources and needs no daemon. The disaster-recovery
template references `docs/templates/seeds.example.yaml`: replace its placeholder
digests using `ptc resources --json` first. With no target, missing digest resources
are fetched through the local daemon. Directory digests and raw blob hashes remain
unchanged. Directory-only `data.pin_permanent: true` protects replicas from normal
clean; blob pin is not supported. See [docs/PREPARE.md](docs/PREPARE.md) for YAML.

## Install and update

`ptc install --ssh HOST` installs an official release; `ptc update [--ssh HOST]`
updates one local or remote binary. Use `--version VERSION` to select a prerelease;
the default `latest` selects the latest stable release. Both verify SHA256SUMS
and roll back failed replacements. SSH supports Linux amd64/arm64 and Windows
amd64, requires noninteractive authentication and an already trusted host key,
and never disables StrictHostKeyChecking. Optional SSH flags are `--identity`,
`--known-hosts`, and `--port` / `-p`.
Windows targets also require a local OpenSSH `scp` client and remote SFTP.

Default remote paths are `$HOME/.local/bin/ptc` on Linux and
`%LOCALAPPDATA%\Programs\ptc\ptc.exe` on Windows; override with `--path`.
Local Linux/macOS can self-update. Windows must run a different ptc executable
and use `update --path C:\Tools\ptc.exe` to replace an existing, non-running copy.
These commands only replace a binary; they do not install services, restart
daemons, or coordinate mesh upgrades. See [docs/CLI.md](docs/CLI.md).

## Decision rules: picking a target

- `--target` omitted → run on this machine (local `ptc submit`).
- `--target <name|peer-id>` → pre-check the catalog, then send `/ptc/exec/1.0.0`. The target enforces `prepare.yaml` (`require_nvidia` and `deny_non_exec` default **true**).
- `--target auto` → pick an eligible exec node (NVIDIA + exec offered). Never resolves to a gateway for CUDA jobs.
- **Never target a gateway** (or any node with `exec` off / no NVIDIA) for a `require_nvidia` job. Expect a clear refusal; do not retry the same target.
- Content-addressed seeds already on the mesh are **not** shipped in the bundle. The target tries holders by hop count using standard Boxo Bitswap blocks and verifies DirDigest / sha256 before delivering content. Legacy peers negotiate the old tar/blob path. A seed that exists only as a local path on the submitter is published there first.
- One node runs `exec.concurrency` jobs at a time (default 1); later jobs **queue** (FIFO). They show as `queued` / `running` / `succeeded` / `failed` / `cancelled` in `ptc tasks`, `ptc queue`, and the WebUI.
- Follow disconnect does **not** cancel the job. Cancel explicitly with `ptc cancel <run-id>`.
- Bitswap uses 256-KiB chunks and a PTC raw-CID transport manifest, not UnixFS. Inputs remain sha256 digests; direct CID/dataset resolution is unsupported. Single-resource transport is limited to 10,000 chunks (about 2.44 GiB including tree tar overhead), with 20 seconds per block and 30 minutes overall. Do not claim TB-scale single resources work. The persistent `seeds_dir/blocks` cache has no GC; `ptc clean resource` does not reclaim it. Cache/staging count toward seed disk usage. Files policy is rechecked on block requests and sends, including existing connections.

## Machine-readable output

Agents should pass `--json` on `peers`, `peers show`, `tasks`, `tasks show`, `resources`, `resources show`, `submit`, `queue`, `cancel`, and `clean`. Do not parse human tables. This follows `gh --json` / `kubectl -o json` conventions.

## Error recovery

1. Version mismatch → install a matching binary; reread `ptc skill print`.
2. `daemon lock not found` → start `ptc daemon --config config.yaml` with the same `--data-dir`.
3. Target not in the catalog → wait for a scan/relay push; check `trusted_cidrs` and, across segments, that a `relay` gateway is up. Do not invent neighbor IPs. Gateways may use `scan_hosts` (e.g. `host.docker.internal` for the Windows `host` node).
4. Placement / policy refusal → pick another node; show the error; do not `--force` a CUDA job onto a no-GPU machine.
5. `queue is full` or job sits in `queued` → `ptc queue --json`; wait or `ptc cancel`.
6. Digest missing on the target → confirm the submitter published it (`ptc resources --node <submitter>`) and the mesh resource protocol can reach that replica.
7. Disk pressure → `ptc peers show <name> --json` (real volume totals and ptc usage). Free space with `ptc clean … --dry-run` then `--yes`. Worktrees and seeds are **different stores**; cleaning a worktree never deletes CAS/packages. Pinned items need `--force`. Seeds referenced by queued/running jobs are refused. `ptc clean` is local-only.
8. Still stuck → read `docs/DESIGN.md`, `docs/CLI.md`, `docs/CONTRIBUTING.md`. Do not patch a running node out of tree.

## Commands (this version)

```text
ptc version
ptc skill print
ptc install --ssh HOST [--version VERSION] [--path PATH]
ptc update [--ssh HOST] [--version VERSION] [--path PATH]
ptc daemon --config config.yaml
ptc peers[--json]
ptc peers show <name|peer> [--json]
ptc policy show|allow|deny|service
ptc submit --workflow <file> [--target <name|peer|auto>] [--detach] [--fetch-result <dir>] [--json] [--dry-run]
ptc queue [--node <name>] [--json]
ptc cancel <run-id> [--json]
ptc tasks / tasks show <id> [--json]
ptc resources / resources show|get <digest> [--json]
ptc clean outputs|runs|resource|worktrees [--match RE] [--dry-run] [--yes] [--force] [--json]
ptc webui
```

Storage paths (`storage.seeds_dir`, `storage.worktrees_dir`, `storage.runs_dir`) default under `data_dir` and can be overridden with `--seeds-dir` / `--worktrees-dir` / `--runs-dir` or `PTC_*_DIR`. Windows may put worktrees on a fast drive such as `D:\ptc\worktrees`.

Optional Ubuntu CUDA node (official `nvidia/cuda:12.9.2-devel-ubuntu22.04`, GPU passthrough): `docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml up -d --build cuda`. The default live compose stays free of mocked GPUs. Native Windows GPU node: `docker/run-host-node.ps1`.

## Not provided (do not call)

`ptc dataset`, `ptc gc`, `ptc status`, `ptc logs`, `ptc artifacts`, UnixFS/filestore and full pin management, remote `ptc clean --node` (local only unless a future policy.remote_clean protocol is added), and submitting or cancelling from the WebUI. Official full P3 UI, service installation and coordinated mesh upgrades are not done. Standard Bitswap transport is implemented with the limits described above.

Full flag lists and the loopback API are in [docs/CLI.md](docs/CLI.md). Prepare / Package result contracts: [docs/PREPARE.md](docs/PREPARE.md), [docs/PACKAGE-RESULT.md](docs/PACKAGE-RESULT.md).
