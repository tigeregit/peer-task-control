# Submit, seed-only tasks, and target selection

## Ordinary workflows

`ptc submit --workflow <file>` runs locally when `--target` is omitted.
To dispatch: `ptc submit --workflow <file> --target <name|peer-id|auto> --json`.

Default is follow (stream until done). Use `--detach` only when the user
wants to return immediately, then watch with `ptc queue` / `ptc tasks`.
After success, the result digest is in the JSON (and printed in follow
mode). Fetch with `ptc resources get <digest> -o <file>` or
`--fetch-result <dir>` on a followed submit. `--dry-run` validates without
running.

Jobs must start with Prepare and end with Package result. Contracts:
[docs/PREPARE.md](docs/PREPARE.md),
[docs/PACKAGE-RESULT.md](docs/PACKAGE-RESULT.md).
Templates live in [docs/templates/](docs/templates/).

## Decision rules: picking a target

- `--target` omitted → run on this machine (local `ptc submit`).
- `--target <name|peer-id>` → pre-check the catalog, then send
  `/ptc/exec/1.0.0`. The target enforces `prepare.yaml` (`require_nvidia`
  and `deny_non_exec` default **true**).
- `--target auto` → pick an eligible exec node (NVIDIA + exec offered).
  Never resolves to a gateway for CUDA jobs.
- **Never target a gateway** (or any node with `exec` off / no NVIDIA) for a
  `require_nvidia` job. Expect a clear refusal; do not retry the same target.
- Content-addressed seeds already on the mesh are **not** shipped in the
  bundle. The target tries holders by hop count using standard Boxo Bitswap
  blocks and verifies DirDigest / sha256 before delivering content. Legacy
  peers negotiate the old tar/blob path. A seed that exists only as a local
  path on the submitter is published there first. `ptc resources pull
  <digest>` persists a verified replica on this node (tree under
  `packages/replica-*`, blob in CAS) and advertises the transport-manifest
  CID on the board. `OpenResource` uses the same pull path when it can.
- One node runs `exec.concurrency` jobs at a time (default 1); later jobs
  **queue** (FIFO). They show as `queued` / `running` / `succeeded` /
  `failed` / `cancelled` in `ptc tasks`, `ptc queue`, and the WebUI.
- Follow disconnect does **not** cancel the job. Cancel explicitly with
  `ptc cancel <run-id>`; running jobs report `cancelling` until their
  processes stop and the final record is `cancelled`.

CUDA jobs go to an exec node with a real NVIDIA GPU (`host` on Windows, or
the optional `cuda` Ubuntu container), never to `gateway` or a no-GPU worker.

## Seed-only tasks (supported action)

Use a workflow whose only step is `uses: ptc/seed@v0`, with
`with.file: seeds.yaml`. That file must have `apiVersion: ptc/v0`,
`kind: Seed` and a nonempty `seeds` list of unique `name` plus `digest`
(sha256) or `local.src`. `required` defaults true. Do not add workspace,
layout, outputs, dest, entrypoint, run, if or Package result. This action
fetches, verifies, retains and advertises the resources; it does not
construct a worktree, check GPU capabilities, execute commands or package
results. It still records a task and supports remote queue/follow/cancel.
A remote target needs files enabled and policy permission, not exec or
NVIDIA. `--target auto` selects a files node for this action; submit
explicitly to different nodes to increase replica count. It is not a
continuous replication controller.

```text
ptc submit --workflow testdata/seed/workflow.yaml --dry-run --json
ptc submit --workflow testdata/seed/workflow.yaml --json
ptc submit --workflow docs/templates/seed-workflow.example.yaml --target replica-node --json
```

The testdata example uses local sources and needs no daemon. The
disaster-recovery template references `docs/templates/seeds.example.yaml`:
replace its placeholder digests using `ptc resources --json` first. With no
target, missing digest resources are fetched through the local daemon.
Directory digests and raw blob hashes remain unchanged. Directory-only
`data.pin_permanent: true` protects replicas from normal clean; blob pin is
not supported. See [docs/PREPARE.md](docs/PREPARE.md) for YAML.

## GPU nodes (deploy, not fixtures)

Optional Ubuntu CUDA node (official `nvidia/cuda:12.9.2-devel-ubuntu22.04`,
GPU passthrough):

```text
docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml up -d --build cuda
```

The default deploy compose stays free of mocked GPUs and recorded fixture
tasks. Gateways have no GPU and must never be CUDA exec targets. Native
Windows GPU node: `docker/run-host-node.ps1`. Cross-segment P0-C tests live
under `docker/test/` and are not the deploy path. See
[docker/README.md](docker/README.md).
