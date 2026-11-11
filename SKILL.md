---
name: peer-task-control
description: >
  Dispatch CUDA and other workflows across a trusted-LAN ptc mesh.
  Use when the user wants to run ptc, submit or cancel a job, pick a GPU
  node, inspect tasks/resources/queue/disk, or clean local storage.
---

# peer-task-control

Version-bound skill pack for **0.3.0-dev**.

`ptc skill print` and `ptc skill print [topic]` always print the copy
embedded in this binary and append `bound-to-version`. Check it against
`ptc version`. Do not use a checkout or leftover `SKILL.md` after
`ptc update`. List topics with `ptc skill list`. Load only the topic you need.

You help the user send programs (often CUDA) and test data to the right
machine on a trusted LAN. Cross-segment traffic goes through a whitelist
gateway that has no NVIDIA GPU and must never be an exec target for CUDA jobs.

## When to use

- Start or talk to a `ptc daemon`, list peers, or inspect policy.
- Run or dispatch a Prepare / run / Package result workflow with `ptc submit`.
- Retain resource replicas without a workspace or execution using `ptc/seed@v0`.
- Watch, cancel, or clean jobs; list seeds; read the live board.
- Install a release over SSH or update a local/remote/mesh ptc binary.
- Pull a verified seed replica with `ptc resources pull`.

Details, flags, exit codes, and the HTTP API live in [docs/CLI.md](docs/CLI.md).
Do not invent commands that are not listed here or there.

## Core workflow

1. `ptc version` and `ptc skill print` — confirm the skill matches the binary.
2. Ensure `ptc daemon --config config.yaml` is running on this machine
   (`trusted_cidrs` only; never ask the user for neighbor IPs).
3. `ptc peers --json` (or `ptc peers show <name> --json`) — wait until the
   catalog lists the intended node. Prefer `--json` over scraping tables
   (same idea as `gh` / `kubectl`).
4. Choose a target (see rules below). CUDA jobs go to an exec node with a
   real NVIDIA GPU (`host` on Windows, or the optional `cuda` Ubuntu
   container), never to `gateway` or a no-GPU worker.
5. `ptc submit --workflow <file> --target <name|peer-id|auto> --json` to
   dispatch; omit `--target` to run locally. Default is follow (stream until
   done). Use `--detach` only when the user wants to return immediately,
   then watch with `ptc queue` / `ptc tasks`.
6. After success, the result digest is in the JSON (and printed in follow
   mode). Fetch with `ptc resources get <digest> -o <file>` or
   `--fetch-result <dir>` on a followed submit.

WebUI (`ptc webui`) is **read-only**. Do not try to submit, cancel, change
policy, or clean through the board. Those are CLI-only.

## Decision rules: picking a target

- `--target` omitted → run on this machine (local `ptc submit`).
- `--target <name|peer-id>` → pre-check the catalog, then send
  `/ptc/exec/1.0.0`. The target enforces `prepare.yaml` (`require_nvidia`
  and `deny_non_exec` default **true**).
- `--target auto` → pick an eligible exec node (NVIDIA + exec offered).
  Never resolves to a gateway for CUDA jobs.
- **Never target a gateway** (or any node with `exec` off / no NVIDIA) for a
  `require_nvidia` job. Expect a clear refusal; do not retry the same target.
- Seed-only tasks (`uses: ptc/seed@v0`) need files, not exec or NVIDIA;
  `--target auto` then selects a files node.
- Content-addressed seeds already on the mesh are **not** shipped in the
  bundle. The target pulls them with Bitswap and verifies DirDigest / sha256.
- One node runs `exec.concurrency` jobs at a time (default 1); later jobs
  **queue** (FIFO). Follow disconnect does **not** cancel the job; use
  `ptc cancel <run-id>`.

Details: `ptc skill print submit` and `ptc skill print resources`.

## Machine-readable output

Agents should pass `--json` on `peers`, `peers show`, `tasks`, `tasks show`,
`resources`, `resources show`, `resources pull`, `submit`, `queue`, `cancel`,
`clean`, `update --mesh`, and `skill list`. Do not parse human tables.

## Topics

| topic | what it covers | command |
| --- | --- | --- |
| `submit` | Workflows, seed-only tasks, placement, queue/cancel | `ptc skill print submit` |
| `resources` | DirDigest vs board CID, Bitswap, pull/download | `ptc skill print resources` |
| `install` | `ptc install --ssh`, `ptc update`, mesh rolling upgrade | `ptc skill print install` |
| `discovery` | Trusted CIDRs, scans, cooldown, relays | `ptc skill print discovery` |
| `storage` | Seeds vs worktrees, quotas, `ptc clean` | `ptc skill print storage` |
| `errors` | Recovery for common failures | `ptc skill print errors` |

`ptc skill print` with no topic prints this index.

## Commands (this version)

```text
ptc version
ptc skill print [topic]
ptc skill list [--json]
ptc install --ssh HOST [--version VERSION] [--path PATH]
ptc update [--ssh HOST] [--version VERSION] [--path PATH] [--mesh] [--no-restart]
ptc daemon --config config.yaml
ptc peers [--json]
ptc peers show <name|peer> [--json]
ptc policy show|allow|deny|service|update
ptc scan [--node <relay>] [--json]
ptc submit --workflow <file> [--target <name|peer|auto>] [--detach] [--fetch-result <dir>] [--json] [--dry-run]
ptc queue [--node <name>] [--json]
ptc cancel <run-id> [--json]
ptc tasks / tasks show <id> [--node <name-or-peer-id>] [--json]
ptc resources / resources show|get|pull <digest> [--json]
ptc clean outputs|runs|resource|worktrees [--match RE] [--dry-run] [--yes] [--force] [--json]
ptc webui
```

## Not provided (do not call)

`ptc dataset`, `ptc gc`, `ptc status`, `ptc logs`, `ptc artifacts`,
UnixFS/filestore and full pin management, resolving inputs by CID or
dataset name, remote `ptc clean --node` (local only unless a future
`policy.remote_clean` protocol is added), and submitting or cancelling
from the WebUI. Official full P3 UI and OS service installation are not
done. SSH install does not write a remote `install.json`, so those nodes
are not mesh-update targets until a local update records the install.
Standard Bitswap transport is implemented; see `ptc skill print resources`
for its limits.

Full flags and the loopback API: [docs/CLI.md](docs/CLI.md).
Prepare / Package result: [docs/PREPARE.md](docs/PREPARE.md),
[docs/PACKAGE-RESULT.md](docs/PACKAGE-RESULT.md).
