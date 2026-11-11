# ptc CLI reference

[English](CLI.md) | [中文](zh/CLI.md)

Applies to development version **0.3.0-dev**. Official builds write the version from the Release tag. `ptc version` and `ptc --help` are authoritative.

Online help:

```text
ptc --help
ptc <command> --help
```

This document only records interfaces that are already implemented. Commands that are not implemented yet are listed at the end.

## Global flags

Every subcommand accepts these flags:

| Flag | Default | Purpose |
| --- | --- | --- |
| `--config` | `config.yaml` under the data directory (see "Data directory" below) | Path to the config file. `ptc daemon` **must** be able to read this file. |
| `--data-dir` | `data_dir` in the config; if the config does not exist yet, the default data directory | Overrides the data directory (identity key, `daemon.lock`). Environment variable `PTC_DATA_DIR`. |
| `--seeds-dir` | `storage.seeds_dir`, defaulting to `data_dir` | Parent of CAS and `packages/`. Environment variable `PTC_SEEDS_DIR`. |
| `--worktrees-dir` | `storage.worktrees_dir`, default `data_dir/worktrees` | Root of per-run worktrees. May be placed on another volume, for example `D:\ptc\worktrees` on Windows. Environment variable `PTC_WORKTREES_DIR`. |
| `--runs-dir` | `storage.runs_dir`, default `data_dir/runs` | Run records and the job queue. Environment variable `PTC_RUNS_DIR`. |
| `--api` | empty: read the API address from `daemon.lock` | Set the local control API `host:port` directly, without reading the lock file. |

Control commands such as `ptc peers` and `ptc policy` do not start the daemon. They talk to an already running daemon through the local loopback API.

Order used to find the API address:

1. If `--api` is passed, use that address.
2. Otherwise read the `api` field in `<data-dir>/daemon.lock`.
3. If the lock file is missing, exit and say whether the daemon appears to be running. `--data-dir` is resolved as: an explicit `--data-dir`, otherwise try to load `--config` and use its `data_dir`, otherwise the default data directory.

## Data directory

The default data directory comes from `os.UserConfigDir()`:

- Windows: `%APPDATA%\ptc` (typically `C:\Users\<user>\AppData\Roaming\ptc`)
- Linux: `$XDG_CONFIG_HOME/ptc`, or `~/.config/ptc` if that is unset
- If no config directory is available: `~/.ptc`; if that also fails, `.ptc` in the current directory

Default layout (you can move these with `storage.*` or the matching flags, including to another drive):

| Path | Description |
| --- | --- |
| `config.yaml` | If the default `--config` path is used, the daemon reads config from here. |
| `identity.key` | The node's libp2p identity key, created on first start (still under `data_dir`). |
| `daemon.lock` | Written when the daemon starts and removed on a clean exit. Contains `pid`, `api`, `peer_id`, `name`. |
| `cas/`, `packages/` | Seeds and result packages (under `seeds_dir`). Separate from worktrees; deleting a worktree does not delete this data. |
| `blocks/` | Persistent Bitswap raw SHA256 block cache under `seeds_dir`. Counts toward the shared seeding quota. Unused blocks are reclaimed during a safe seed clean by walking references of retained resources. |
| `worktrees/` | Private workspace materialized for each run; reclaimed immediately after success, failure, or cancel. Old directories can also be cleaned with `ptc clean worktrees`. |
| `.fetch/` | Download / bundle staging on the seed volume, not on the worktree volume. Seed-only jobs may use it too. |
| `runs/` | Run-record JSON and the persistent job queue under `runs/queue/`. |

### Global storage rules

```yaml
storage:
  size_limit: 40GB
  delete_size: 20GB
  workspace_limit: 0
```

`size_limit` sums file bytes of `packages/` (including output), CAS, Bitswap blocks, and `.fetch/`. It is not an inputs-only counter, and there is no exemption by output / permanent / temporary class. Duplicate storage and hard-linked paths are counted by the logical size of each path. `GB` is decimal; `GiB` or an integer byte count are also accepted. When `delete_size` is omitted it is half of the cap; it must satisfy `0 < delete_size <= size_limit`. `workspace_limit` is the **combined size of all workspaces on the node**, default 0 (unlimited).

If free space on the seed volume is below the configured cap, the effective cap is `max(0, free - 1GB)`, and the cleanup target is `delete_size / size_limit * effective cap`. For example, 40GB/20GB with free=11GB becomes 10GB/5GB. When the cap is reached (or a new input cannot be reserved), the daemon tries to free at least that cleanup target. If there is not enough data that can be deleted safely, it errors clearly; it must not delete a unique replica just to meet the target.

Before a delete, another files node that supports `/ptc/retention/1.0.0` must fully verify the replica, persist a retention commitment, and acknowledge it. Simultaneous cleanup on both sides, a network drop, or a lost ACK must not authorize deleting the last copy. On a negotiation conflict, keep the data and retry in the background with jitter. Commitments do not rely on a TTL. After a restart, the initiator retries unfinished release transactions. Do not manually delete `.retention/`, `.retention-lock/`, `.reservations/`, or `.quota-lock/`. A node on the old protocol cannot be a safe-delete keeper. Upgrades must cover both the daemon and the local CLI; an old CLI must not be able to bypass the protection.

`pin`, inputs of active jobs, and results that are still being written are protected. Finished outputs and other ordinary seeds use the same automatic cleanup rules. If a block index is missing or corrupt, the cache is kept conservatively and the reason is logged. Prepare checks the size of all inputs and concurrent reservations before downloading. If the inputs themselves exceed the cap, the job is marked failure and does not enter a download/cleanup loop. Extra space is also reserved for the block cache, staging, and metadata. Seed-only jobs, standalone `resources pull`, and result publish are subject to the same limits. A local job without a daemon cannot negotiate network cleanup, and errors immediately when space is insufficient.

A workspace must be separate from the actual seed, download, record, and coordination-metadata directories. It must not be the same path or nested with them, including after symlink aliases are resolved. The default may still share `data_dir` as a common parent, where `worktrees/` is a sibling of `cas/`, `packages/`, and the rest. A custom `layout.root` must sit strictly under the configured workspace root. Success, failure, cancel, and Prepare failure all reclaim the workspace before the exec slot is released. On startup, workspaces pointed at by terminal run records are also reclaimed, without deleting paths outside the directory or a workspace that is still locked. The workspace and the seeds use private file copies; writes in the workspace do not change seeded data.

At runtime the workspace is checked about every 250ms, seed space about every 1s, and again at step boundaries. Crossing the limit cancels the process tree and marks the job as failure. Network cleanup requests made during monitoring have a 3-second deadline; network negotiation and filesystem walks may stretch the check interval. This is an application quota, not a hard filesystem quota, so writes may briefly exceed the threshold between checks. Unique or protected resources are still kept, and a capacity-short error is reported. `--keep-worktree` errors on exec jobs. `cleanup.when/evict_worktree` in older YAML no longer allows a terminal workspace to be kept. Run records and queue history are not deleted automatically by these rules.

## Exit codes

- `0`: success (`ptc daemon` also exits `0` after a clean `SIGINT` / `SIGTERM`).
- `1`: error. This includes workflow validation failure, local/remote execution failure, a missing target, a `require_nvidia` job refused because it was sent to a no-GPU / exec-disabled / gateway node, a policy refusal, a full queue, clean without `--yes`, and failure to talk to the daemon. The error is written to stderr (with `--json`, key fields are also in the JSON).
- `1`: the command failed. The error is written to stderr.

## Commands

### `ptc version`

Prints the binary version (development builds are `0.3.0-dev`; Release builds are the tag without `v`), followed by a newline. No config or running daemon is required.

### `ptc skill sync`

The primary agent load path is the user-scope Agent Skills directory: `~/.agents/skills/peer-task-control/` (Windows: `%USERPROFILE%\.agents\skills\peer-task-control\`). `ptc skill sync` writes the pack bundled in this binary there, creating parent directories as needed, and replaces only this skill's directory. A successful local `ptc update` and a mesh update also run that sync. `ptc install --ssh` and `ptc update --ssh` run `skill sync` on the remote host after the binary is replaced; a failure is reported as a warning and does not roll back the binary. If the installed copy's `metadata.version` does not match `ptc version`, human-facing `ptc skill`, `ptc version`, `ptc install`, and `ptc update` commands print a warning on stderr. The notice is suppressed when `--json` is set and when the `CI` environment variable is set. It does not fail the command. Refresh with `ptc skill sync` or `ptc update`.

The canonical pack in the repository is `skills/peer-task-control/`. Do not treat a leftover repository-root or next-to-binary `SKILL.md` as authoritative.

A running daemon is not required.

### `ptc install --ssh HOST` / `ptc update [--ssh HOST|--mesh]`

Downloads the platform binary and `SHA256SUMS` from official `tigeregit/peer-task-control` Releases, verifies, then replaces, rolling back on failure. `install` and SSH / local `update` do not need a daemon. These commands **do not install an OS service**. A successful local `ptc update` writes `install.json` and restarts a running local daemon (skip with `--no-restart`). `--mesh` needs the local daemon. It pushes the same major version in stages to nodes that already have `install.json` and have `update.accept_remote` on. The target pulls the binary with Bitswap, verifies, replaces, and requests a restart. SSH install currently only replaces the remote binary and does not write `install.json`, so the host does not automatically become a mesh-update target.

Mesh updates and a manual daemon restart require the target to have no queued or running jobs; new jobs are refused during the update/restart. A mesh update counts as success only after the target re-announces the new version. If it does not rejoin, the update is marked failed and later batches stop. `--json` still prints a single report, but the command exits non-zero if any failure occurred. `install.json` must point at the binary that daemon is actually running.

```text
ptc install --ssh user@linux-node --version 0.2.0-p2
ptc install --ssh user@windows-node --version 0.2.0-p2 --path C:\Tools\ptc.exe
ptc update --ssh user@windows-node --version 0.2.0-p2 --path C:\Tools\ptc.exe
ptc update --version 0.2.0-p2
# Windows: run a different ptc.exe to update a non-running installed copy
ptc update --version 0.2.0-p2 --path C:\Tools\ptc.exe
ptc update --version 0.3.0 --no-restart
ptc update --mesh --version 0.3.0
ptc update --mesh --dry-run --json
ptc update --mesh --nodes worker-a,worker-b --batch 1
```

| Flag | Default | Purpose |
| --- | --- | --- |
| `--ssh HOST` | empty | Required for install; if omitted on update, the local machine is updated. Accepts an OpenSSH alias, a hostname, an address, or `user@host`. |
| `--version` | `latest` | Latest stable Release; a pre-release must be given as an explicit tag version. |
| `--path` | see below | Local target, or an absolute file path on the SSH target. |
| `--identity` | OpenSSH default | SSH private-key file. Encrypted keys should be used through ssh-agent. |
| `--known-hosts` | OpenSSH default | Alternate known_hosts file; verification is never turned off. |
| `--port` / `-p` | `22` | SSH port. |
| `--mesh` | off | Rolling update of manageable nodes, driven by the local daemon. Cannot be combined with `--ssh`. |
| `--nodes` | all manageable nodes | Only these node names or peer IDs. |
| `--batch` | `1` | How many nodes to update in parallel per stage. The previous stage must have rejoined the catalog before the next stage starts. |
| `--assets-dir` | empty | A local directory containing per-platform binaries and `SHA256SUMS`, for an offline mesh. |
| `--dry-run` | off | Print the mesh plan only; do not change nodes. |
| `--json` | off | `--mesh` only: print JSON. |
| `--no-restart` | off | After a local update, do not restart the local daemon. |

SSH auto-detects Linux amd64/arm64 or native Windows amd64. macOS SSH, Windows ARM64, and UNC paths are not supported. The default install path is `$HOME/.local/bin/ptc` on Linux and `%LOCALAPPDATA%\Programs\ptc\ptc.exe` on Windows. On Windows the initiator uploads the binary and a replace script with the host OpenSSH `scp`, then starts a short encoded PowerShell command, to avoid the default cmd.exe command-length limit and Windows SSH binary stdin issues. The initiator therefore also needs `scp`, and the target must provide SFTP. SHA256 and the version are checked. An update keeps the original target ACL and cleans the temporary deploy directory. An error is reported if the target is locked by a running process.

SSH must already have non-interactive authentication configured, and the target must already be trusted in known_hosts. The commands use `BatchMode=yes` and `StrictHostKeyChecking=yes`, and do not accept a password prompt or an unknown host key. Downloads happen on the machine that runs the command, using the normal HTTPS proxy environment.

Local update supports Linux/macOS self-update. Windows cannot overwrite the currently running exe; you must run a different copy and point `--path` at an installed PTC that is not running. An explicit local target must exist and be detectable with `ptc version`. SSH-only identity/known-hosts/port flags cannot be used in local mode.

`--dry-run`, `--json`, `--assets-dir`, `--nodes`, and `--batch` can only be used with `--mesh`.

`--mesh` defaults to pushing the local binary's version to the mesh (or `--version` / `latest`). A target only accepts an update from a trusted peer, at the same major version, and not older than its current version. The binary first enters CAS on the initiating node, then the target pulls it with Bitswap. This is not each node fetching from GitHub on its own. Nodes need an existing `data_dir/install.json` (local `ptc update` writes this manifest). `ptc policy update off` sets `update.accept_remote` to false and refuses remote pushes (allowed by default).

### `ptc daemon`

Starts the daemon, joins the mesh, and opens the local control API. It runs in the foreground. Logs and prompts go to stderr.

```text
ptc daemon --config config.yaml
ptc daemon --config config.yaml --data-dir D:\ptc-data
```

Behavior:

- The file pointed at by `--config` must be openable. If it is missing, the command tells you to copy `config.example.yaml` and fill in `trusted_cidrs`.
- `--data-dir` overrides `data_dir` in the config.
- It listens on the mesh port (default `0.0.0.0:18765`) and the local API (default `127.0.0.1:18780`).
- The control API only accepts loopback sources.
- Same-segment discovery uses mDNS and a start-up port probe. A gateway with `services.relay: true` scans every trusted segment and pushes the catalog. By default it scans once at start and then discovers/connects passively. Set a positive `scan_interval` when you need periodic probes.
- When the gateway is reached through NAT or a Docker port mapping, map both TCP and UDP on the mesh port. After a node reconnects to the gateway it immediately re-reserves the relay; a failed reservation is logged as a warning.
- Loopback addresses in the catalog belong to the node that advertised them and are not used by other nodes as a direct dial address. When several nodes run on the same host, list each `127.0.0.1:port` explicitly in `scan_hosts`.
- **Do not** put a neighbor list in the config.
- After a successful start, stderr prints the node name, peer ID, and API address, and suggests using `ptc peers` next.
- Stop with Ctrl+C or SIGTERM.
- After a local `ptc update` or a mesh update replaces the binary, the daemon restarts on request (Unix uses exec and keeps the same PID; Windows starts the new process first, then exits, and uses a hidden `--wait-pid` to wait until the old process releases the port).

### `ptc scan`

```text
ptc scan
ptc scan --node gateway
ptc scan --node <relay-peer-id> --json
```

Without `--node`, the local daemon is asked to scan once. A local relay scans every `trusted_cidrs` entry; otherwise only the host's own trusted segments are scanned. Configured `scan_hosts` are still probed. With `--node`, the local daemon sends a `/ptc/scan/1.0.0` request to a known relay. The target must have relay on, and the caller must pass the allow/deny policy check. The command does not guess a dial address for an unknown node, and it does not automatically fall back to scanning on an old daemon.

```yaml
scan_interval: 0s
scan_min_interval: 60s
```

Omitting `scan_interval`, or setting it to `0s`, means an infinite period: scan once at start and do not repeat on a timer. After you set a positive duration such as `5m`, each scan waits that long after it finishes. A manual scan does not enable a periodic task that was turned off. Changing policy itself does not trigger a scan. If an existing config explicitly had `30s`, it still runs on a 30-second period after an upgrade. mDNS, inbound connections, and catalog/relay maintenance on established connections stay enabled when periodic probing is off.

`scan_min_interval` is the minimum accepted interval **shared by every manual request on the target node**, default 60 seconds. The local CLI and all remote peers share one budget. A request is refused immediately if a scan is already queued or running, and refused immediately during cooldown. Refusals are not queued to run later, and a refusal does not extend the cooldown. Start-up and periodic scans do not consume the manual budget, but an in-progress scan still blocks a duplicate manual scan. The budget resets when the daemon restarts.

Success only means the scan was accepted and queued, not that it has finished. `--json` prints `node`, `peer_id`, and `status`. A refusal includes `error` and, when applicable, `retry_after_seconds`, and the command exits non-zero. HTTP 202 means accepted, 409 means busy, 429 means rate-limited with `Retry-After`, and 403 means a policy / non-relay refusal. The local API preserves these statuses when it forwards to a remote.

### `ptc peers`

Lists nodes known to the local daemon and their services. The daemon must already be running.

```text
ptc peers
ptc peers --json
ptc peers show gateway
ptc peers show 12D3KooW…
ptc peers --data-dir D:\ptc-data
ptc peers --api 127.0.0.1:18780
```

| Flag | Default | Purpose |
| --- | --- | --- |
| `--json` | off | Print indented JSON to stdout instead of a table. |

`ptc peers show NAME|PEER` shows one host's hardware (OS, GPU, VRAM, driver, addresses, services), that node's historical tasks, and the seeds it stores. It uses the same data as the WebUI node detail page: `GET /api/v1/status`, `/api/v1/runs`, `/api/v1/resources`, then the same node-filter semantics as the board. `--json` also applies to `show`.

The default output starts with a local-info line, then a tab-separated table:

```text
local <name>  peer <peer ID>  version <version>

NAME  PEER ID  EXEC  FILES  RELAY  ADDRS
```

The `PEER ID` column is truncated with an ellipsis when it is longer than 16 characters. Service column values:

| Cell | Meaning |
| --- | --- |
| `yes` | The service is currently offered (`offered: true`) |
| `no (<reason>)` | Not offered, with a refusal reason |
| `no (disabled)` | This host turned the feature off, and there is no more specific reason |
| `no` | Not offered, no reason |
| `-` | The catalog entry has no such service |

A node that has turned some services off still appears in the catalog; the corresponding column just states the reason. For a gateway with no NVIDIA GPU, `EXEC` may be `no (this node has no NVIDIA GPU)`.

`--json` prints the control API status object, not only the neighbor list. Fields:

```json
{
  "name": "worker-a",
  "peer_id": "<libp2p peer id>",
  "version": "0.3.0-dev",
  "listen": ["<multiaddr>", "..."],
  "peers": [
    {
      "peer_id": "...",
      "name": "gateway",
      "addrs": ["..."],
      "services": {
        "exec":  { "offered": false, "enabled": false, "reason": "this node has no NVIDIA GPU" },
        "files": { "offered": true,  "enabled": true },
        "relay": { "offered": true,  "enabled": true }
      },
      "caps": {
        "nvidia": false,
        "gpu_name": "",
        "vram_mib": 0,
        "driver": "",
        "os": "linux",
        "arch": "amd64"
      },
      "updated_at": "2026-11-10T14:00:00Z"
    }
  ]
}
```

`services.*.enabled` is that node's own feature switch. `offered` is what the catalog actually advertises (for example `exec` also requires a local probe and an NVIDIA GPU). Fields in `caps` that were not probed may be empty or zero. The sample values above only illustrate the shape; runtime output is authoritative.

### `ptc webui`

Opens the local read-only task board, listening on `127.0.0.1:18790` by default. The UI is bilingual (Chinese / English, switchable) and is styled close to agentboard: a top bar, stat cards, and a task list. Clicking a task opens a GitHub Actions-like run summary: status, how it was triggered, duration, jobs and steps, the step that is running, and a log summary.

The board always shows a live task "网格目录监视 / Mesh catalog watch", and it merges local `--tasks` / `--runs` with run records published by other nodes on the mesh (the daemon's `GET /api/v1/runs` pulls `/ptc/runs/1.0.0` from neighbors). Task and resource lists support search, status/type filters, and sorting. The top bar also has a Resources pane: it lists mesh-wide seeds with digest, Bitswap transport CID, size, replica count, created time, and the shortest hop count to the current device (the daemon the board is attached to), and it supports download. The list can be sorted ascending or descending by column header. The digest drops the `sha256:` prefix and by default shows the first 8 hash characters plus `***`; click the eye button to expand it. A result seed in a task summary can be opened on the matching resource page. Clicking a device under Nodes shows hardware, historical tasks, and seeds stored on that node; clicking a task or seed jumps to the matching pane. The CLI and the board share the same local control API and query semantics (`internal/query`). The board is read-only: viewing, filtering, sorting, searching, and seed download for tasks / resources / nodes can all be done from the CLI with the same flags (`ptc tasks`, `ptc resources`, `ptc peers` / `peers show`). The CLI also provides control operations that the board will not proxy: `ptc daemon`, `ptc policy` (allow / deny lists and service switches), `ptc submit` (local or `--target` dispatch), `ptc cancel`, `ptc clean`, and `ptc resources show --list-files` (expand a directory tree with more than 100 files). The board lists jobs that neighbors have finished (including CUDA) but cannot submit or cancel them.

The default Docker deploy compose starts `gateway` (relay + files, no GPU), `files` (seeding only), and a read-only `webui`. You can also start a native `host` node on a Windows machine (real NVIDIA GPU): make sure the deploy mesh is already `up`, then from the repository root run `powershell -File docker/run-host-node.ps1`. The gateway probes the host's `18765` port via `scan_hosts: host.docker.internal`. Stop it with `powershell -File docker/run-host-node.ps1 -Stop`. To join the P0-C test mesh, use `powershell -File docker/run-host-node.ps1 -TestMesh`.

An optional Ubuntu CUDA exec node should not be written into the default deploy compose. On a machine that already has NVIDIA Container Toolkit / Docker Desktop GPU passthrough, start the official `nvidia/cuda:12.9.2-devel-ubuntu22.04` node with the overlay (named `cuda`, exec+files, no relay; the GPU is whatever `nvidia-smi` inside the container reports):

```text
docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml up -d --build cuda
docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml stop cuda
docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml rm -f cuda
```

First confirm that `docker run --rm --gpus all nvidia/cuda:12.9.2-devel-ubuntu22.04 nvidia-smi` sees a real GPU. This repository tried the equally published `13.5.0-devel-ubuntu22.04` on driver 616.56, and `nvidia-container-cli` refused it with `cuda>=13.5`, so the overlay uses 12.9.2. Sample job: `ptc submit --workflow testdata/cuda-vectoradd/workflow.yaml --target cuda`. Full deploy notes are in [docker/README.md](../docker/README.md).

```text
ptc webui
ptc webui --listen 127.0.0.1:18790 --api 127.0.0.1:18780
```

| Flag | Default | Purpose |
| --- | --- | --- |
| `--listen` | `127.0.0.1:18790` | Board listen address. In the Docker simulation this is changed to `0.0.0.0:18790` and mapped to the host's `127.0.0.1:18790`. |
| `--tasks` | empty | Optional recorded-task JSON (`{"tasks":[...]}`). |
| `--report` | empty | Compatibility alias for `--tasks`. |
| `--runs` | empty | Directory of local `runs/*.json`. Mesh records are still merged in by the daemon's `/api/v1/runs`. |

Related endpoints: `GET /`, `GET /live/tasks`, `GET /live/status`, `GET /live/resources`, `GET /live/resources/sha256/{hex}`, `GET /live/resources/sha256/{hex}/content`, `GET /live/meta`. `GET /live/report` redirects to `/live/tasks`.

Both the deploy compose and the cross-segment tests map the board to the host at `http://127.0.0.1:18790`. The default deploy compose does not mount recorded test tasks and does not fake a GPU on container nodes. Node hardware is whatever the daemon actually probes. To run the official P0-C role assertions, use `docker/test/docker-compose.yml` plus `-f docker/test/docker-compose.p0-test.yml` (do not stack that on a live board or a deploy compose you are actually using).


### `ptc resources`

Lists content seeds across the mesh, shows one seed, downloads its content, or pulls a replica into the local store. It shares `GET /api/v1/resources` with the WebUI Resources pane (the daemon pulls `/ptc/resources/1.0.0` from neighbors and computes hop counts from the current node to each replica: 0 for a direct connection, 1 through one circuit relay). The daemon must already be running. The Prepare step registers dependency seeds into packages or CAS, so input data appears in the inventory together with result seeds. The workspace uses a separate copy; modifying or reclaiming a workspace does not pollute seeded data.

With no subcommand, this is the `ptc resources` list.

```text
ptc resources
ptc resources --json
ptc resources --search pred.bin --kind tree --sort size --order desc
ptc resources --node worker-a
ptc resources show sha256:6c3ba3d27d3128f6e53872e87185dff8c27a6f1d34cca9ba305a9d516c033cd8
ptc resources get sha256:6c3ba3d27d3128f6e53872e87185dff8c27a6f1d34cca9ba305a9d516c033cd8 -o result.tar
ptc resources pull sha256:6c3ba3d27d3128f6e53872e87185dff8c27a6f1d34cca9ba305a9d516c033cd8
ptc resources pull sha256:6c3ba3d27d3128f6e53872e87185dff8c27a6f1d34cca9ba305a9d516c033cd8 --json
```

| Subcommand | Purpose |
| --- | --- |
| (default) | List seeds: digest, Bitswap transport CID, name, kind, size, replica count, created time, and the shortest hop count to the current device |
| `show DIGEST` | Details: digest, transport CID, size, replicas, and per-host hop count and path to the current node (local / direct / relay). Tree seeds are treated as a directory unit. When there are more than 100 files, only the file count is printed by default; add `--list-files` for every path. A digest is not computed for each small file. |
| `get DIGEST -o FILE` | Download from the nearest replica. Tree seeds are saved as tar. Omitting `-o`, or using `-o -`, writes to stdout. |
| `pull DIGEST` | Ask the local daemon to pull the seed from a holder with Bitswap, verify the DirDigest (tree) or sha256 (blob), write it as a temporary replica into `packages/replica-*` or CAS, and then offer it to others. If it is already local, the path is reported directly. Delete the replica with `ptc clean resource`. `--json` is accepted. |

List filters have the same semantics as the WebUI Resources pane:

| Flag | Purpose |
| --- | --- |
| `--search` | Case-insensitive substring match on digest, CID, name, kind, file path, and hosting device |
| `--kind` | `all`, `tree`, or `blob` |
| `--node` | Only seeds that have a replica on this node (name or peer id) |
| `--sort` | `name` (default), `size`, `kind`, `hops`, `replicas`, `created`, `digest` |
| `--order` | `asc` or `desc` (`size` / `replicas` default to descending) |
| `--list-files` | `show` only: list every path in the directory tree. By default, when there are more than 100 files only the count is shown, matching the board preview rule |

`--json` applies to the list, `show`, and `pull`. Hop counts are relative to this daemon, not to the computer running the CLI (if the CLI is attached to a gateway, the gateway is the origin). The CLI / HTTP interface is still a content API. Between nodes, 256KiB blocks are preferably transferred with standard Boxo Bitswap: `/ptc/resource-blocks/1.0.0` only discovers the transport-manifest CID; the payload uses standard Bitswap. Fallback to the original tar/blob protocol happens only when an old node does not support that discovery protocol. A verification error does not fall back silently. Replicas are tried in hop order. Content is delivered only after it has been received in full and verified with sha256 / DirDigest.

The CID in the inventory and on the board is the raw SHA-256 CID of the Bitswap transport manifest, not a UnixFS CID. A holder saves and announces it after the first export or a background index. `testdata/bitswap-dispatch` is a dispatch sample that references an ~2 GiB SDK by digest only.

The block count, byte count, and MiB/s reported by pull are logical fetch statistics. They include indexes, cache hits, and repeated references, and do not represent actual network traffic or line rate. `provider` is the resource holder chosen when the request was made, not a precise attribution of every Bitswap peer that actually supplied a block. For a tree resource the CID is stored as a `.cid` sidecar outside the resource directory, so the transferred content itself is not modified.

Input is still a PTC sha256 digest. Direct CID / dataset / UnixFS input is not supported. A single resource with more than 10,000 256KiB blocks automatically switches to a v2 multi-level Merkle index, with at most 256 references per page. You do not need to split volumes by hand, and the whole resource or the full set of block references is not kept in memory. Small resources still get a v1 manifest, and new nodes can read v1. Old Bitswap nodes do not support v2 large resources; both the provider and the receiver must be upgraded. A format or verification error does not fall back to the old protocol. Resource size uses int64. In practice the limits are still available disk space, a 20-second per-block timeout, and a 30-minute overall timeout. Each relay connection is reset after 64 GiB accumulated in either direction, or after 30 minutes; a very large or slow transfer is not guaranteed to continue seamlessly after a reset. None of this claims that terabyte-scale transfers have been measured. Tar overhead of a directory is included in the transfer size.

The cache lives in `blocks/` on the seed volume. Download and verification staging lives in `.fetch/`. No worktree is built. After automatic seed cleanup, unreferenced blocks are marked and reclaimed from the v1/v2 indexes of every retained resource; shared blocks are kept. If an index is missing or corrupt, that round of block reclamation stops and the reason is logged. A standalone `ptc clean resource` does not immediately reclaim the block cache. Seed-disk accounting includes the cache and staging. After files is turned off or the lists are updated, resource discovery and standard Bitswap request / send paths re-check policy.

### `ptc tasks`

Lists or inspects run records published on the mesh. It shares `GET /api/v1/runs` with the WebUI task list. `ptc tasks show` prints the worktree generated from the Prepare YAML (directories, seeds, outputs, and the entrypoint). The task detail page renders the same record.

```text
ptc tasks
ptc tasks --json
ptc tasks --search cuda --status completed --sort started --order desc
ptc tasks --node worker-a
ptc tasks show run-20261110T155326Z-test --node worker-a
```

New run IDs have a random suffix, so the same task run in the same second on different nodes does not overwrite another record. Old records can still be viewed. If an old ID is duplicated across nodes, use `tasks show <id> --node <node name or peer id>` to pick a node. The WebUI also shows each node's record separately.

List filters have the same semantics as the WebUI Tasks pane:

| Flag | Purpose |
| --- | --- |
| `--search` | Case-insensitive substring match on id, Chinese and English names, status, events, actor, target, node, peer id, and summary |
| `--status` | `all`, `in_progress` (includes queued), `completed` (success / cancelled / skipped), `failed`, or an exact status |
| `--node` | Filter by node name or peer id |
| `--sort` | `started` (default), `duration`, `name`, `status`, `node`, `id` |
| `--order` | `asc` or `desc` (`started` / `duration` default to descending) |

### `ptc policy`

Shows or changes **this host's** allow list, deny list, and feature switches. The daemon must already be running. Changes take effect immediately and are written back to the config file the daemon is using.

With no subcommand, this is `ptc policy show`.

#### `ptc policy show`

Prints the current policy:

```text
allow: (empty: accept all peers in trusted CIDRs)
deny:  (empty: accept all peers in trusted CIDRs)
exec:  true
files: true
relay: false
update (accept mesh updates): true
```

When a list is non-empty, entries are joined with a comma and a space. An empty list means: inside the trusted segments, there is no extra membership restriction (the deny list still wins).

#### `ptc policy allow add|rm <entry>`

#### `ptc policy deny add|rm <entry>`

An entry may be an **IP**, a **CIDR**, a **node name**, or a **peer ID**. Add is ignored if the exact same string is already in the list. Remove is an exact string match. When matching a peer, node name and peer ID are case-insensitive; IP / CIDR are judged as addresses.

The deny list wins over the allow list. When the allow list is non-empty, a peer that misses it is refused (reason such as `peer is not on this host's allow list`).

After every successful change, stdout prints the updated policy (same format as `show`).

#### `ptc policy service <exec|files|relay> <on|off>`

Turns one local service on or off. The service name must be `exec`, `files`, or `relay`. Besides `on` / `off`, the switch also accepts `true`, `1`, `enable`, `enabled` and `false`, `0`, `disable`, `disabled` (case-insensitive).

The node still appears in the catalog; the corresponding service is marked unavailable, with a reason (for example `service exec is disabled on this host`). Turning `exec` on does not by itself mean execution is offered: the host still needs an NVIDIA GPU.

#### `ptc policy update <on|off>`

Allows or refuses trusted peers to push a new version to this host (`update.accept_remote`, allowed by default). Writes the `update.accept_remote` field in the config file.

## Configuration file

YAML. Copy `config.example.yaml` and fill in at least one `trusted_cidrs` entry. Do not put neighbor IPs in the config.

| Field | Type | Default | Description |
| --- | --- | --- | --- |
| `name` | string | hostname | Display name in the catalog. |
| `listen` | `host:port` | `0.0.0.0:18765` | Mesh listen address. The port should stay the same across the cluster. |
| `api` | `host:port` | `127.0.0.1:18780` | Local control API. Bind it to loopback; non-loopback sources are refused. |
| `data_dir` | path | see "Data directory" | Location of the identity key and lock file, and the default root of the storage paths. |
| `storage.seeds_dir` | path | `data_dir` | Parent of `cas/` and `packages/`. |
| `storage.worktrees_dir` | path | `data_dir/worktrees` | Worktree root. |
| `storage.runs_dir` | path | `data_dir/runs` | Run records and the queue. |
| `storage.size_limit` | bytes or a unit string | `40GB` | Shared seeding cap for inputs, outputs, CAS, the block cache, and download staging, reduced dynamically from disk free. |
| `storage.delete_size` | bytes or a unit string | `20GB` | Automatic cleanup target per round; defaults to half of the configured cap when omitted. |
| `storage.workspace_limit` | bytes or a unit string | `0` | Combined cap of all workspaces; 0 means unlimited. |
| `exec.concurrency` | integer | `1` | How many jobs this node runs at once; the rest queue. |
| `policy.remote_clean` | bool | `false` | Reserved switch. Today `ptc clean` only cleans locally. |
| `trusted_cidrs` | CIDR list | **required, at least one** | Trust boundary. Connections from outside the segments are always refused. Same-host loopback peering needs an explicit entry such as `127.0.0.0/8`. |
| `psk` | 64 hex characters | empty (disabled) | Optional 32-byte key used to block noise from outside the segments. A `0x` prefix is allowed. Every node in the cluster must use the same value. |
| `services.exec` | bool | `true` | Whether this host intends to offer exec. Without an NVIDIA GPU the catalog still marks it unavailable. |
| `services.files` | bool | `true` | Whether to offer the files service (publish/download of seeds and result packages). |
| `services.relay` | bool | `false` | Whether to act as a gateway: scan trusted segments and push the catalog. An allow-listed host without a GPU should turn this on and turn `exec` off. |
| `policy.allow` | string list | `[]` | Local allow list. |
| `policy.deny` | string list | `[]` | Local deny list, which wins over the allow list. |
| `update.accept_remote` | bool | `true` | Whether to accept a same-major-version mesh upgrade from a trusted node. Change it with `ptc policy update`. |
| `scan_interval` | non-negative duration | `0s` | 0 means an infinite period, so only the start-up scan runs; a positive value is the wait after each scan completes before the next one. |
| `scan_min_interval` | positive duration | `60s` | Cooldown shared by every local/remote manual scan request. Omitting it or 0 uses the default; a negative value is illegal. |
| `disable_mdns` | bool | `false` | When true, mDNS discovery is not used. The Docker cross-segment tests use this. |
| `scan_hosts` | string list | `[]` | Extra probe targets for the gateway beyond the CIDR scan (hostname, IP, or `host:port`). Resolved addresses are also admitted by the connection gater. In the Docker demo the gateway uses `host.docker.internal` to find the Windows host node, instead of scanning the entire Docker Desktop virtual segment. |

A gateway scan skips prefixes broader than `/16` (a segment with more than 65534 usable addresses is not expanded). For a cross-segment topology, write trusted segments as narrower CIDRs, usually `/16` or smaller.

`ptc policy` writes the current policy back to this config file (mode `0600`). Do not paste a PSK or other secrets into issues or logs.

## Local control API

The CLI calls the daemon's loopback API over HTTP (default `http://127.0.0.1:18780`). Only loopback requests are accepted. The default timeout is 5s. Clients that pull resources or drive a mesh upgrade use a longer timeout (`pull` up to 6 hours, `--mesh` up to 3 hours). This is not a public mesh protocol.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/health` | `{"status":"ok"}` |
| `GET` | `/api/v1/version` | `{"version":"0.3.0-dev"}` (a Release returns the actual tag version) |
| `GET` | `/api/v1/status` | The same status object as `ptc peers --json` |
| `GET` | `/api/v1/peers` | `{"peers":[...]}`, without the local `name` / `version` wrapper |
| `GET` | `/api/v1/policy` | Current policy |
| `POST` | `/api/v1/policy` | Change policy, JSON below; success returns the updated policy |
| `GET` | `/api/v1/runs` | Local and neighbor-published run records. Optional query parameters: `q`/`search`, `status`, `node`, `sort`, `order` (same as `ptc tasks`) |
| `GET` | `/api/v1/resources` | Mesh-wide seed inventory (including hosts and hop counts). Optional query parameters: `q`/`search`, `kind`, `node`, `sort`, `order` (same as `ptc resources`) |
| `GET` | `/api/v1/resources/sha256/{hex}` | Size, replicas, and hop information for one seed |
| `GET` | `/api/v1/resources/sha256/{hex}/content` | Download the seed (a tree is tar) |
| `POST` | `/api/v1/resources/sha256/{hex}/pull` | Pull the seed into the local store with Bitswap (the timeout is relaxed for large files) |
| `POST` | `/api/v1/exec?target=<name>&follow=1` | The local daemon dispatches the bundle in the request body to the target; follow returns NDJSON events |
| `GET` | `/api/v1/queue` | Jobs that are queued or running on the mesh |
| `POST` | `/api/v1/cancel` | `{"id":"<run-id or job-id>"}`, cancel a queued or running job |
| `POST` | `/api/v1/scan` | `{}` triggers a local scan; `{"node":"<relay name or peer-id>"}` asks a known relay. 202 accepted, 409 busy, 429 rate-limited (with `Retry-After`), 403 refused, 404 node not found. |
| `POST` | `/api/v1/storage/ensure` | Capacity admission for a local runner: `incoming` bytes, `seeds_dir`, `worktrees_dir`, optional `reservation_id`. Returns 409 if the roots do not match, capacity is insufficient, or a safe cleanup is not possible. It only guarantees that check; the caller must also hold a disk reservation. |
| `POST` | `/api/v1/update/mesh` | Rolling-update request (`version`, `nodes`, `batch`, `assets_dir`, `dry_run`) |
| `POST` | `/api/v1/update/restart` | Ask the local daemon to re-exec the already replaced binary |

`POST /api/v1/policy` request body:

```json
{"op":"allow_add","spec":"10.87.0.10"}
{"op":"allow_rm","spec":"10.87.0.10"}
{"op":"deny_add","spec":"bad-node"}
{"op":"deny_rm","spec":"bad-node"}
{"op":"service","service":"exec","on":false}
{"op":"update","on":false}
```

`op` must be `allow_add`, `allow_rm`, `deny_add`, `deny_rm`, `service`, or `update`. An unknown `op`, an empty entry, or an illegal service name returns HTTP 400.

### `ptc submit`

Runs an Actions-style workflow. An ordinary job's first step must be Prepare and its last step must be Package result, with `run` steps in between. The other legal job is a seed-only workflow that contains a single `ptc/seed@v0` action; see below.

Without `--target`, the job runs **locally**. With `--target <node name|peer id>` (or `auto`), the local daemon dispatches the job to the target exec node over the libp2p protocol `/ptc/exec/1.0.0`. The target runs the same Prepare → steps → Package result path, writes the run record through the shared runs protocol, publishes the result package by digest, and the submitter can fetch it with `ptc resources get`. The WebUI does not dispatch jobs.

A local run without `--target` happens in the CLI process. It does not enter the daemon queue, so it cannot be `ptc cancel`'d and is not limited by `exec.concurrency`. When you need queue / cancel semantics, submit to yourself with `--target <local node name>`: the daemon puts the job on the local exec queue without going over the network (local exec must still be on; a seed-only job requires files; allow / deny lists only constrain other nodes).

Content-addressed seeds (already published on the mesh) are not packed into the bundle. The target pulls them from local CAS/packages or from the shortest-hop holder, and verifies the digest (directory units use DirDigest). Seeds that exist only as a local path on the submitter are published locally first, then pulled by the target. Small local inputs are inlined into the bundle by default (`--inline-max`, default 8MiB); larger ones go through publish+pull.

`target.require_nvidia` (default true) and `target.deny_non_exec` (default true) in `prepare.yaml` are enforced both on the submitter (catalog preflight) and on the target. No NVIDIA, exec disabled, or a gateway must be refused, with a clear error. Exec nodes default to concurrency 1. Later jobs enter the persistent FIFO queue instead of failing immediately as busy.

Follow is the default: status and logs are printed until the job ends. `--detach` returns after the job is queued. If the submitter drops follow, the job is **not** cancelled; use `ptc cancel <run-id>`. For a local or remote submit with `--json --fetch-result`, stdout prints only one JSON object, download messages go to stderr, and a download failure fills JSON `error` and returns a non-zero exit status.

```text
ptc submit --workflow testdata/submit/workflow.yaml --dry-run
ptc submit --workflow testdata/submit/workflow.yaml --data-dir /path/to/ptc-data
ptc submit --workflow testdata/submit/workflow.yaml --job test --force
ptc submit --workflow testdata/cuda-e2e/workflow.windows.yaml --target host --fetch-result D:\ptc\results
ptc submit --workflow testdata/submit/workflow.yaml --target auto --json
ptc submit --workflow testdata/cuda-vectoradd/workflow.yaml --target cuda
```

| Flag | Default | Purpose |
| --- | --- | --- |
| `--workflow` | (required) | Path to the workflow YAML. Prepare / Package result `with.file` values are resolved relative to that file's directory. |
| `--job` | empty | Required when the workflow has more than one job. May be omitted when there is only one. |
| `--target` | empty (local run) | Node name, peer id, or `auto` (pick a qualified exec node). Do not point a CUDA job at a gateway. |
| `--detach` | false | Return after queueing, without following logs. |
| `--fetch-result <dir>` | empty | After a successful follow, unpack the result package into this directory. Local submits also support it, but they need a local daemon; without a daemon the command errors before execution. |
| `--inline-max` | 8388608 | Size cap in bytes for local seeds inlined into the bundle. `-1` means publish everything and pull by digest. |
| `--dry-run` | false | Parse and validate only; do not run or dispatch. |
| `--force` | false | Local runs only: skip the NVIDIA / exec checks. |
| `--keep-worktree` | false | No longer supported for exec jobs. Passing it errors, and every terminal workspace is reclaimed immediately. |
| `--json` | false | Print JSON, for agents. |

The run record (including the worktree snapshot and the submitter) is written under `runs/` on the **exec node**, and the result is published as an ordinary seed into its `packages/`. Every terminal worktree is deleted immediately. Seeds are governed by the shared quota and safe-replica cleanup rules. A directory-tree seed with more than 100 files gets one digest for the whole directory.

#### Seed-only (no workspace, no execution)

`ptc/seed@v0` `with.file` points at a resource-set YAML with `apiVersion: ptc/v0` and `kind: Seed`. The action must be the only step. `run` / `if` are not allowed, and Prepare / Package result are not required. Resources are fetched and verified from `digest` or `local.src` in `seeds`, then placed in CAS/packages and offered as a local replica. No worktree is created, no GPU is checked, no command is run, and no result package is produced. The run record's `worktree` is empty.

```text
ptc submit --workflow testdata/seed/workflow.yaml --dry-run --json
ptc submit --workflow testdata/seed/workflow.yaml --json
ptc submit --workflow docs/templates/seed-workflow.example.yaml --target replica-node --json
```

Full YAML, collection fields, and pin limits are in [Seed-only tasks](PREPARE.md#seed-only-tasks). Digests in the templates must be replaced with values from `ptc resources --json`. A remote needs the `files` service and a matching policy allow; exec / NVIDIA are not required. In this case `--target auto` picks a files node. When target is omitted, missing resources are pulled by the local daemon. A job that uses only local resources does not need a daemon. Queue / follow / cancel semantics are unchanged, but `--fetch-result` has no result to fetch, and `--force` / `--keep-worktree` do not change seed-only behavior. Submitting separately to different nodes can add replicas. After the job finishes the replicas are kept, but replica count is not checked continuously in the background.

### `ptc queue`

Lists jobs in `queued`, `running`, or `cancelling` on each node. Finished jobs are in `ptc tasks` or on the board.

```text
ptc queue
ptc queue --node host --json
```

| Flag | Default | Purpose |
| --- | --- | --- |
| `--node` | empty | Only jobs on this node name or peer id. |
| `--json` | false | Print JSON. |

### `ptc cancel`

Cancels a queued or running job (CLI only). A queued job returns `cancelled` immediately. A running job first returns `cancelling`, then becomes `cancelled` after the process tree is stopped and the record is finished; it still occupies a concurrency slot during that time. A finished job errors. A timeout is recorded as failure, not as a user cancel. Dropping follow does not cancel automatically.

```text
ptc cancel run-20260101T000000Z-test
ptc cancel run-20260101T000000Z-test --json
```

### `ptc clean`

Applies only to **local** storage paths. There is no `--node` remote delete (unless a future target turns on `policy.remote_clean`).

Subcommands: `outputs` (result packages), `runs` (finished run records), `resource` (delete a seed or result by digest), `worktrees` (leftover worktrees; never deletes CAS/packages).

A running worktree is protected by a cross-process lock: preview marks it undeletable, and the lock is checked again before an actual delete. `--force` cannot bypass this protection, a persistent replica commitment, or active-input protection. The plan is rechecked before a resource is actually deleted. `.ptc-locks` under the worktree root is the persistent lock directory; do not delete it by hand. Jobs started by older versions do not hold these locks; wait for those jobs to finish before upgrading. A custom `layout.root` must be a strict subdirectory of the configured worktree root, and must not point at the root, the lock directory, or an external path.

| Flag | Purpose |
| --- | --- |
| `<id…>` / `--match RE` | Filter by run id, name, job, or digest. |
| `--dry-run` | List paths and bytes that would be deleted, without deleting. |
| `--yes` | Required for an actual delete (so a non-interactive agent can be explicit). |
| `--force` | Allow deleting a pinned object. A seed referenced by a queued or running job is still refused. |
| `--json` | Print JSON. |

```text
ptc clean worktrees --dry-run
ptc clean outputs --match 'fake-cuda' --yes
ptc clean resource sha256:abc… --yes
ptc clean runs --match 'sample-local' --yes
```

## Commands not yet implemented

These belong to later phases. The current binary has no such subcommands:

- `ptc dataset`
- `ptc gc`
- `ptc status`
- `ptc logs`
- `ptc artifacts`

Design draft: [peer-task-control design draft](https://cursor.com/canvas/store-bfdc01c7-fd7a-4053-a2a7-a49302fbb19c/03f3f67c-b25a-4548-a67d-bef09a48a413). Offline summary: [DESIGN.md](DESIGN.md).
