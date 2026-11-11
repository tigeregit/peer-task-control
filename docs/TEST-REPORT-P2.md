<!-- Path note: this is a historical test record. The docker/docker-compose.yml used at the time is now the deploy compose. To reproduce cross-segment tests, use docker/test/. A real CUDA node can still use docker/docker-compose.yml with docker/docker-compose.cuda.yml. -->

# P2 test report

[English](TEST-REPORT-P2.md) | [中文](zh/TEST-REPORT-P2.md)

- Version: `0.2.0-p2`
- Time zone: Asia/Shanghai (UTC+8)
- Mesh: compose project `ptc-p0` (image `ptc:p0`, gateway + webui sidecar `127.0.0.1:18790`, worker-a, worker-b, all without a GPU), a native Windows `host` node (`docker/run-host-node.ps1`, real RTX 5070, exec on), and an optional overlay node `cuda` (official `nvidia/cuda:12.9.2-devel-ubuntu22.04`, `--gpus all`).

## Unit and in-process tests

`go test ./...` passed in full on the development machine (Linux box), including:

- `internal/dispatch`: bundle encode/decode, directory seeds not packed into the bundle, oversized local seeds switched to publish, DirDigest verification failure, placement refusing a gateway / no-GPU node, `auto` selecting host, FIFO queue and cancel, and re-queue of a running job after a crash.
- `internal/node.TestDispatchSubmitFixture`: two in-process nodes form a mesh by probing, dispatch `testdata/submit` to `worker-b`, the job succeeds, the result digest can be fetched from the submitter via `OpenResource`, and the run record carries the exec node and the submitter.
- `internal/node.TestDispatchRequireNVIDIARefused`: a `require_nvidia` job is refused by the gateway at catalog preflight.
- `internal/clean`: pinned result packages are kept by default, `--force` can delete them without deleting seeds by mistake; cleaning worktrees does not touch CAS/packages; a seed referenced by a running job is refused for deletion.
- `internal/config.TestStoragePathsAndEnv`: `PTC_SEEDS_DIR` / `PTC_WORKTREES_DIR` / `PTC_RUNS_DIR` and default paths.
- `internal/disk.TestMeasureRealVolume`: reads a real volume total/free (not a fixture).

## Protocol and behavior (implemented; see CLI.md)

- `/ptc/exec/1.0.0` is gated by trusted CIDRs and by the local `policy` / `services.exec`.
- Follow is the default; `--detach` returns as soon as the job is queued. If the submitter drops follow, the job is **not** cancelled; use `ptc cancel`.
- Default concurrency per node is 1; the rest wait in a FIFO. Statuses `queued` / `running` / `succeeded` / `failed` / `cancelled` are written into the run record. The task list and the board keep the existing `success` status name (a successful job is shown as `success` in `ptc tasks` / the WebUI).
- Worktrees and seeds use separate directories; cleaning a worktree does not delete CAS/packages.

## Live mesh verification

Recorded below. The default live compose did not mount a mock GPU or test fixtures. Rebuilding `ptc:p0` and restarting gateway/worker caused a brief mesh blip; starting the `cuda` overlay did not rebuild the other containers.

The skill documentation style was informed by Anthropic agent skills (frontmatter + progressive disclosure), `gh --json`/`--jq`, and the kubectl and docker CLIs (`--detach`, machine-readable output, error recovery).

### (a) fake-CUDA e2e: Docker node → Windows `host` (real RTX 5070)

On `host`, the e2e seeds were first published into local packages (hard-linked, so the ~4 GiB seeds were not packed into the bundle):

- `third_party_sdk` `sha256:dcefeca789cf079dbd1db45f9c3ecaf73bf9f83fa5bd529b8088e1afe7f7a132` (3 files, 2147483748 B)
- `scene-a` `sha256:137c28921e86b89218b790b4b5577300f991743d98a4d3f048a616f899f753c1` (10485 files, 2147328000 B)
- `app` `sha256:07973b3a493d8f9a8f015f5d61807f4b7e7ab46891d75221723b832b19166b07`

From `worker-a`, a digest-only prepare (no large seeds inlined) was dispatched through the relay to `host`:

```text
# on worker-a
ptc submit --workflow /tmp/e2e/workflow.windows.yaml --target host --fetch-result /tmp/e2e-result
```

- Run record: `run-20261110T182856Z-test` (started 2026-11-11 02:28:56 Asia/Shanghai, ended 02:29:12 Asia/Shanghai)
- Exec node: `host`; submitter: `worker-a`; status: `success`
- Job logs included `nvidia-smi NVIDIA GeForce RTX 5070, 12227 MiB`, `hardlink_probe=cam_00000.bin count=4`, `kernel_scan files=41 … elapsed_ms=101`
- Result seed: `sha256:76b76c622d209bf5067d74be93cc421838d94f7cd3fb7df40851c1cab69bd266`
- Retrieved by the submitter: 5 files / 33556313 bytes; the worktree was cleaned
- Seeds were materialized on `host` local packages after DirDigest verification, and were not packed into the bundle

### (b) `require_nvidia` refusal

From `worker-a`, `testdata/cuda-vectoradd` (`require_nvidia` default true) was pointed at no-GPU nodes and refused at preflight (the job was not queued):

- `--target gateway`: `refusing to dispatch a require_nvidia job to gateway: it is a relay/gateway or has no NVIDIA GPU (this node has no NVIDIA GPU)`
- `--target worker-b`: `refusing to dispatch a require_nvidia job to worker-b: this node has no NVIDIA GPU`

The same job was later accepted by `cuda` and succeeded (see (d)).

### (c) `testdata/submit` → `worker-b` (Linux, `require_nvidia: false`)

The BusyBox worker has no `bash`. The first dispatch still used `run: bash bin/app.sh` and failed:

- `run-20261110T182712Z-test` (2026-11-11 02:27:12 Asia/Shanghai), node=`worker-b`, status=`failure`, log `/bin/sh: bash: not found` (exit 127)

After `testdata/submit/workflow.yaml` was changed to `run: sh bin/app.sh`, a retry succeeded:

```text
# on worker-a
ptc submit --workflow /tmp/submit/workflow.yaml --target worker-b --fetch-result /tmp/submit-result
```

- `run-20261110T182748Z-test` (2026-11-11 02:27:48 Asia/Shanghai), node=`worker-b`, status=`success`
- Result seed: `sha256:99bd0ef0b160b32870b81357a533c5b523025a6d5393d54f14b6cc758bcd3026`
- Retrieved by the submitter: 4 files / 732 bytes, summary `ok scene=scene-a fixture`

### (d) Real CUDA: official `nvidia/cuda:12.9.2-devel-ubuntu22.04` container node `cuda`

Image tags were verified on Docker Hub; none were invented.

1. The already published `nvidia/cuda:13.5.0-devel-ubuntu22.04` was tried first (pushed 2026-11-05, digest `sha256:b44fc069…`). `docker run --rm --gpus all … nvidia-smi` failed with exit 125: `nvidia-container-cli: requirement error: unsatisfied condition: cuda>=13.5, please update your driver to a newer version, or use an earlier cuda container`. The host driver was 616.56. nvidia-smi was not faked; an earlier published tag was used instead.
2. `nvidia/cuda:12.9.2-devel-ubuntu22.04` (digest `sha256:e0203e3a0d3d02b05f4210867b973b7f48997c71bc99b994f2bd350ee60f77a1`) succeeded on the same host with `docker run --rm --gpus all … nvidia-smi --query-gpu=name,memory.total,driver_version --format=csv`: `NVIDIA GeForce RTX 5070, 12227 MiB, 616.56`.
3. Overlay start (already running gateway/worker containers were not rebuilt):

```text
docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml up -d --build cuda
docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml stop cuda
docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml rm -f cuda
```

`nvidia-smi` inside the container also saw RTX 5070 / 12227 MiB / 616.56. `ptc peers show cuda`: linux/amd64, gpu=`NVIDIA GeForce RTX 5070`, exec yes, files yes, relay no, peer `12D3KooWRdN9zg8mU3pNJ8vARg9Xz14Rq8sCBABFLGHkimaJc17x`.

A real nvcc job was dispatched from `worker-a`:

```text
# on worker-a
ptc submit --workflow /tmp/cuda-vectoradd/workflow.yaml --target cuda --fetch-result /tmp/cuda-result
```

- `run-20261110T184028Z-test` (2026-11-11 02:40:28 Asia/Shanghai), node=`cuda`, status=`success`, submitter `worker-a`
- Source seed (inline): `sha256:bff04522c1d6eef1f9a76de8040591d4770942241919f935ae22f376b081db41` (10 files, 8894 B)
- Step logs: `NVIDIA GeForce RTX 5070, 12227 MiB, 616.56`; compiled with `nvcc`; `ok vector_add n=65536 device=NVIDIA GeForce RTX 5070 cc=12.0`
- Result seed: `sha256:f8eeea96bb4780e49c02ab4e2179389e454a8bc37c3415da2569373555da3c5a`
- Retrieved by the submitter: 4 files / 914 bytes (`summary.md` / `nvidia-smi.txt` / `run.txt`); the worktree was cleaned

This was real CUDA execution inside an Ubuntu container, complementary to the native Windows fake-CUDA e2e in (a).

### Board and disk figures

`ptc tasks` (host) and gateway `GET /api/v1/runs` (the same API as the WebUI sidecar; the board at `http://127.0.0.1:18790` returned HTTP 200) listed the same run records:

| ID | Status | Node | Name |
| --- | --- | --- | --- |
| `run-20261110T184028Z-test` | success | cuda | cuda-vector-add |
| `run-20261110T182856Z-test` | success | host | fake-cuda-scene-a |
| `run-20261110T182748Z-test` | success | worker-b | sample-local-job |
| `run-20261110T182712Z-test` | failure | worker-b | sample-local-job |

Live `ptc peers show` (not fixtures):

- `host`: `C:\Users\iamsbb2\Documents\code\peer-task-control\docker\testdata\host-data` (seeds,worktrees,runs) 452 GiB free / 953 GiB total (501 GiB used); ptc seeds 4.0 GiB, worktrees 0 B, outputs 32 MiB, records 7.5 KiB. GPU RTX 5070, 12227 MiB, driver 616.56.
- `cuda`: `/data` 921 GiB free / 1007 GiB total (86 GiB used, Docker Desktop / existing WSL2 backend volume); ptc seeds 9.7 KiB, worktrees 0 B, outputs 914 B, records 5.3 KiB. The same RTX 5070.

## Uncovered / limits

- No Bitswap; large seeds were downloaded as a whole tar.
- `ptc clean` applies only locally (no remote `--node`).
- Dropping follow does not cancel the job.
- Run-record event streams use `succeeded`; the task list / board success state remains `success` (historical query semantics, not forcibly unified).
- The published CUDA 13.5 image was rejected by driver 616.56; the overlay used the published 12.9.2 instead. If `docker run --gpus all nvidia/cuda:12.9.2-devel-ubuntu22.04 nvidia-smi` fails on a machine, do not start that overlay, and do not fake nvidia-smi.
