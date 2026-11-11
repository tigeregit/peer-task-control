# Status and roadmap

[English](ROADMAP.md) | [中文](zh/ROADMAP.md)

Implementation constraints are in [DESIGN.md](DESIGN.md). The full draft is the [design Page](https://cursor.com/canvas/store-bfdc01c7-fd7a-4053-a2a7-a49302fbb19c/03f3f67c-b25a-4548-a67d-bef09a48a413).

The current development version is `0.3.0-dev` (see `internal/version/version.go`). Published tags on GitHub `main` are listed in Releases.

## Done

- **P0 mesh**: trusted CIDRs, mDNS and port probes, gateway scan and relay, the service catalog, and local allow / deny lists plus feature switches.
- **Task outlines and local execution**: Actions-style jobs and steps; the first step must be Prepare and the last must be Package result. `ptc submit` runs Prepare → run → Package result locally and keeps a worktree snapshot.
- **Seeds**: directories get a DirDigest over the whole tree (directory units). Result packages and Prepare seeds are both published by digest.
- **Mesh sharing**: run records and the resource inventory are visible across the mesh. Directory seeds can be downloaded over the mesh, including across relays.
- **Read-only board**: a bilingual (Chinese / English) WebUI with Tasks, Resources, and Nodes panes. Resources list the digest and the Bitswap transport CID. The board shares the local `/api/v1` and `internal/query` with the CLI. The node page shows real seed-disk and worktree-disk capacity and ptc usage.
- **Native Windows node**: `docker/run-host-node.ps1` joins a local real GPU to the Docker mesh as the `host` node.
- **P2 mesh dispatch**: `ptc submit --target` sends a job to a named (or `auto`) exec node over `/ptc/exec/1.0.0`. The target pulls published seeds from the shortest-path holder and verifies the digest. Exec nodes have a persistent FIFO queue (default concurrency 1). `ptc queue` / `ptc cancel` are CLI-only.
- **Storage split**: seeds (`cas/` + `packages/`) and per-run worktrees have separate directories and lifecycles; cleaning a worktree does not delete seeds. Paths can be set separately in config, environment variables, and `--seeds-dir` / `--worktrees-dir` / `--runs-dir`. `ptc clean` reclaims local result packages, records, seeds, or leftover worktrees.
- **Seed-only jobs**: a standalone `ptc/seed@v0` action fetches and publishes a named resource set. It does not create a workspace, run commands, or build a result package. It can be dispatched to a files-only node to add a disaster-recovery replica. [Full YAML and usage](PREPARE.md#seed-only-tasks); [a runnable local example](../testdata/seed/workflow.yaml).
- **Bitswap data plane**: resource-digest discovery still uses the trusted PTC mesh. Data is exchanged as 256KiB blocks through standard Boxo Bitswap, with relay, verification, a persistent cache, and replica failover. Large resources automatically use a multi-level Merkle index and are no longer capped at 10,000 blocks (~2.44GiB). Disk space, transfer timeouts, and relay connection quotas still apply. Small resources keep a v1 manifest. Nodes that provide or receive large resources must be upgraded for v2 indexes.
- **Replica pull and CID**: `ptc resources pull` writes a verified replica locally and announces the transport CID. The CID on the board is a transport manifest, not UnixFS.
- **Install and update**: `ptc install --ssh HOST` and `ptc update [--ssh HOST|--mesh]` download and verify official Release binaries, with rollback on a failed replace. Local `ptc update` writes `install.json` and restarts a running daemon (`--no-restart` skips that). `ptc update --mesh` rolls the same major version to recorded installs that allow remote updates; targets pull the binary with Bitswap. SSH supports Linux amd64/arm64 and Windows amd64, with strict host-key checking. Local update supports Linux/macOS; on Windows you can update a different exe that is not running. SSH install does not yet write `install.json` on the remote. [Flags and platform limits](CLI.md).

## Not done

- **P1 remaining**: UnixFS / filestore, CID / dataset input parsing, and block-cache GC. Directory-manifest pins exist; full pin management does not.
- **P3**: the official complete product UI (currently only the read-only board).
- **P4 remaining**: installing as an OS service; SSH install still does not write `install.json` on the remote. Daemon restart after a local / mesh update, and same-major-version mesh rolling updates, are already available.
- Remote `ptc clean --node` (clean only applies locally). If the submitter drops follow, the job is not cancelled; use an explicit `ptc cancel`.

## Verification

- **P0-A** unit tests: policy, trusted CIDRs, catalog merge, identity query, and segment scan. Run: `go test ./...`.
- **P0-B** same-host multi-process: two compute nodes on `127.0.0.1/2/3` see each other through one gateway.
- **P0-C** Docker cross-segment: two segments that cannot reach each other, plus an allow-listed gateway with no NVIDIA GPU that can reach both. The test stack is in `docker/test/`; usage is in [docker/README.md](../docker/README.md). You can also start a `host` node on a Windows machine with a real GPU via `docker/run-host-node.ps1 -TestMesh`.

Archived P0 / P2 test records are in [TEST-REPORT-P0.md](TEST-REPORT-P0.md) and [TEST-REPORT-P2.md](TEST-REPORT-P2.md).
