# Contributing and troubleshooting

[English](CONTRIBUTING.md) | [中文](zh/CONTRIBUTING.md)

When something goes wrong, read [DESIGN.md](DESIGN.md), [ROADMAP.md](ROADMAP.md), and this guide before you open an issue or a PR. Do not patch a running node out of tree.

Documentation is bilingual. English is the default GitHub path (`README.md`, `docs/*.md`, `docker/README.md`). Chinese lives in `README.zh-CN.md`, `docs/zh/`, and `docker/README.zh-CN.md`. If you change a document, update both languages so they stay in sync.

## Issues

GitHub issue and pull request templates are in English (`.github/ISSUE_TEMPLATE/`, `.github/PULL_REQUEST_TEMPLATE.md`). Please use them. You may write the issue body in Chinese if that is easier; still fill every required field.

1. Use a one-line title that states the symptom. Do not just write "it's broken".
2. Include: the output of `ptc version`, the operating system, whether the problem crosses segments, relevant config (**do not** paste a PSK or SSH credentials), reproduction steps, expected vs actual behavior, and a log summary.
3. For mesh issues, attach a redacted `ptc peers` output (node names and roles may stay; do not paste sensitive information beyond the private topology).

Prefer the templates in `.github/ISSUE_TEMPLATE/`.

## Commits

- One-line subject; add a blank line and a body when needed.
- Subject format: `type(scope): summary`, for example `feat(net): push the catalog after a gateway scan`.
- Common types: `feat`, `fix`, `docs`, `test`, `refactor`, `chore`.
- Keep each commit to one change. Do not mix a tree-wide reformat with a feature change.
- Do not commit identity keys, PSKs, SSH credentials, or private data from local absolute paths.

## Pull requests

1. Reference an issue, or describe the problem in the PR body.
2. Pass `go test ./...`. If you changed cross-segment discovery, also run the P0-C tests:

   ```text
   powershell -File docker/test/p0-test.ps1
   # or:
   docker compose -f docker/test/docker-compose.yml -f docker/test/docker-compose.p0-test.yml up -d --build
   sh docker/test/p0-test.sh
   python3 docker/test/p0-assert.py
   docker compose -f docker/test/docker-compose.yml -f docker/test/docker-compose.p0-test.yml down -v
   ```

3. In the description, write: what behavior changed, how to verify it, and whether the skill pack or example configs need updates.
4. Protocol or discovery changes must update `docs/DESIGN.md` and `docs/zh/DESIGN.md`. If they conflict with the design Page, update the Page first, then merge.
5. If you change the agent skill: edit `skills/peer-task-control/` (the folder name must match frontmatter `name: peer-task-control`). Put long material in `references/` and keep `SKILL.md` short. The pack is English only and is embedded from that tree. `ptc skill sync` and a successful `ptc update` / `ptc install` write it to `~/.agents/skills/peer-task-control/`. Set `metadata.version` to the same value as `internal/version/version.go`; release builds stamp the running binary version at sync/read time. Do not treat a leftover repository-root or next-to-binary `SKILL.md` as authoritative.

Default Bitswap tests cover index pagination, level carry, malformed indexes, and interoperability with standard Boxo. When you change the large-resource export path, also run the streaming test above the old limit (3GiB) to verify SHA256 and persistent-cache reopen after the source goes offline:

```powershell
$env:PTC_TEST_LARGE_BITSWAP = "1"
go test ./internal/bitswap -run TestLargeResourceExportAndFetch -count=1 -timeout 8m -v
Remove-Item Env:\PTC_TEST_LARGE_BITSWAP
```

This test uses repeated data to keep disk use down. It verifies logical resource size; it does not claim to have transferred 3GiB of non-deduplicable network data.

## Releases

1. The development version is in `internal/version/version.go`. Release builds write the exact version with `-ldflags -X github.com/tigeregit/peer-task-control/internal/version.Version=<tag without v>`. The canonical skill pack is `skills/peer-task-control/`; `metadata.version` in the source index should match the development version, and sync/read stamp the running binary version. Do not treat a leftover repository-root or next-to-binary `SKILL.md` as authoritative.
2. Write release notes in `docs/release-notes/v<version>.md` (and the Chinese counterpart under `docs/zh/release-notes/`). The English file name must match the tag you are about to push.
3. After merging to `main`, tag and push: `git tag v0.2.0-p2 && git push origin v0.2.0-p2`.
4. The GitHub Actions workflow `.github/workflows/release.yml` cross-compiles binaries, packs the skill, generates `SHA256SUMS`, creates the Release, and tries to push images to GHCR. A tag name that contains `-` (for example `-p2` or `-rc`) is marked as a pre-release and does not receive the `latest` image tag.
5. On pushes to `main` and on pull requests, `.github/workflows/ci.yml` runs `gofmt`, `go vet`, and `go test ./...`.

Do not commit `vendor/`, a local identity, or built binaries to the repository.

`.gitattributes` pins shell scripts to LF. Do not convert `.sh` files to CRLF on a Windows checkout, or `sh` on Linux / Docker will treat the carriage returns as part of the command.
