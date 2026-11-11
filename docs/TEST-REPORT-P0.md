<!-- Path note: this is a historical test record. Cross-segment tests have since moved to docker/test/. Deploy compose is described in docker/README.md. -->

# ptc P0 full test report

[English](TEST-REPORT-P0.md) | [中文](zh/TEST-REPORT-P0.md)

- **Result: all passed.** Version `0.1.0-p0`. Generated at 2026-11-10 22:59 Asia/Shanghai (UTC+8).
- Test host: iamsbb2 (Windows), Go 1.27.3, Docker Engine 29.8.0 (linux/amd64 containers).
- The simulated network was still running: `seg_a` `10.87.0.0/24` (worker-a `10.87.0.10`), `seg_b` `10.87.1.0/24` (worker-b `10.87.1.10`), allow-listed gateway `10.87.0.2` / `10.87.1.2` (no NVIDIA GPU, not an execution target).
- Openable UI: local [http://127.0.0.1:18790](http://127.0.0.1:18790) (P0 mesh and test board, read-only). **The P3 task-history WebUI was not implemented yet**; the page at the time was not that UI.

## Test items

| ID | Item | Result | Notes |
| --- | --- | --- | --- |
| P0-A | Unit tests (policy, trusted CIDRs, catalog, identity query, scan) | passed | `go test ./...` exit code 0. Packages that passed: `internal/catalog`, `config`, `gater`, `ident`, `node`, `policy`, `scan`. |
| P0-B | Same-host multi-process: `127.0.0.1/2/3` see each other through a gateway | passed | Covered by `internal/node`'s `TestGatewayScanPushesCatalog`, 0.571s in this run. |
| P0-C.1 | Segment isolation: compute nodes cannot dial the other segment directly | passed | worker-a dialing `10.87.1.10:18765` timed out; worker-b dialing `10.87.0.10:18765` timed out. |
| P0-C.2 | Gateway reachability: the allow-listed gateway can dial both segments | passed | The gateway connected to `10.87.0.10:18765` and `10.87.1.10:18765`. |
| P0-C.3 | Local APIs of all three daemons are ready | passed | `ptc peers` succeeded on worker-a, worker-b, and gateway. |
| P0-C.4 | Full neighbor catalog discovered through the gateway | passed | Each node could see the other two. |
| P0-C.5 | Roles: the gateway has no GPU and cannot execute; compute nodes can execute | passed | worker-a/b offer exec (simulated NVIDIA GeForce RTX 4090 inside the container); gateway exec is no, reason `this node has no NVIDIA GPU`; the gateway offers relay and files. |
| Board | The local mesh and test board can be opened | passed | `http://127.0.0.1:18790` returned 200 and read a live catalog. |

An earlier Docker build had failed with a Debian mirror 502. This run switched to a prebuilt Linux static binary and no longer depended on apt.

## Neighbor catalog still running in this session

Data came from the board reading gateway `/api/v1/status` at 2026-11-10 22:59 Asia/Shanghai (UTC+8). Peer IDs change when containers are recreated.

| Name | Address | Exec | Files | Relay | Capabilities |
| --- | --- | --- | --- | --- | --- |
| gateway | `10.87.0.2:18765`, `10.87.1.2:18765` | no (this node has no NVIDIA GPU) | yes | yes | linux/amd64, no NVIDIA |
| worker-a | `10.87.0.10:18765` | yes | yes | no (service relay is disabled on this host) | simulated NVIDIA GeForce RTX 4090, 24564 MiB, driver 560.00 |
| worker-b | `10.87.1.10:18765` | yes | yes | no (service relay is disabled on this host) | simulated NVIDIA GeForce RTX 4090, 24564 MiB, driver 560.00 |

Corresponding peer IDs:

- gateway: `12D3KooWBQtRnmpKoX5ybrd8EeFwW9yaekVJWfN62ZsrqZAGMggJ`
- worker-a: `12D3KooWRvDDGz7ZgiSXc6cPU5M67ERBpZafTA5jiQ61imDiiqM2`
- worker-b: `12D3KooWASuYFVx7JVLEozjc4cirdMTjCxhGK3SG4hZpAvGMXsqx`

## Scope and untested items

This run covered P0 mesh: neighbor discovery, catalog, roles, and cross-segment isolation. The following were not implemented yet, so they had no functional tests:

- P1 data plane (dataset ingest, dedup, GC)
- P2 job worktrees and `ptc submit`
- P3 task-history WebUI
- P4 SSH install and `ptc update`

To stop the simulated network (run only when you want to):

```text
docker compose -f docker/docker-compose.yml down -v
```
