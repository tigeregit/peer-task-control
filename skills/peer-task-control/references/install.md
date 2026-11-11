# Install, update, and mesh rolling upgrade

`ptc install --ssh HOST` installs an official release; `ptc update
[--ssh HOST]` updates one local or remote binary. Use `--version VERSION`
to select a prerelease; the default `latest` selects the latest stable
release. Both verify SHA256SUMS and roll back failed replacements. SSH
supports Linux amd64/arm64 and Windows amd64, requires noninteractive
authentication and an already trusted host key, and never disables
StrictHostKeyChecking. Optional SSH flags are `--identity`,
`--known-hosts`, and `--port` / `-p`. Windows targets also require a local
OpenSSH `scp` client and remote SFTP.

Default remote paths are `$HOME/.local/bin/ptc` on Linux and
`%LOCALAPPDATA%\Programs\ptc\ptc.exe` on Windows; override with `--path`.
Local Linux/macOS can self-update. Windows must run a different ptc
executable and use `update --path C:\Tools\ptc.exe` to replace an existing,
non-running copy.

These commands do not install OS services. A successful local `ptc update`
writes `data_dir/install.json` and restarts a running local daemon (Unix
re-exec keeps the same PID; Windows starts a successor that waits for the
old PID with hidden `--wait-pid`). Pass `--no-restart` to skip the restart.

`ptc update --mesh` rolls the same-major version to install-managed peers
that have `install.json` and `update.accept_remote` enabled (default on).
The initiator fetches official assets (or `--assets-dir` for an offline
bundle), stages the binary in CAS, and targets pull it with bitswap, then
replace and restart. `--nodes`, `--batch`, `--dry-run`, and `--json` apply
to `--mesh` only. SSH install does not write a remote `install.json`, so
SSH-only nodes are not mesh-update targets until a local `ptc update`
records the install. Refuse mesh updates with `ptc policy update off`.

After `ptc update` or `ptc install`, refresh the installed skill with
`ptc skill sync` (local and mesh updates also do this). Prefer the copy at
`~/.agents/skills/peer-task-control/` (Windows: `%USERPROFILE%\.agents\skills\peer-task-control\`).
If that directory is missing, `ptc skill read [references/...]` prints the
pack bundled in the new binary. Do not keep using a leftover repository-root
or next-to-binary `SKILL.md`. See [docs/CLI.md](docs/CLI.md).
