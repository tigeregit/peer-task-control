Thanks for the contribution. Please confirm:

- [ ] This change has a matching issue, or the problem it solves is described below
- [ ] `go test ./...` has been run
- [ ] If cross-segment discovery changed, the P0-C tests under `docker/test` have been run as described in [docker/README.md](../docker/README.md)
- [ ] If skill text changed, `skills/peer-task-control/` is the only pack, and `go test ./cmd/ptc ./skills -run Test` passes

## What changed

## How to verify

## Notes

Protocol or discovery changes should update `docs/DESIGN.md` and `docs/zh/DESIGN.md`. Do not commit identity keys, a PSK, SSH credentials, or built binaries.
