# Package result convention

[English](PACKAGE-RESULT.md) | [中文](zh/PACKAGE-RESULT.md)

This document freezes the **Package result** YAML convention. Both local execution and mesh dispatch package results according to this file.

## Principles

- Symmetric with Prepare: this step in the workflow does not expand the details; it only **points at a YAML file**. Parsing that YAML must yield three implementation contracts:
  1. **Paths to collect** (which results and logs in the worktree go into the package);
  2. **Packaging and publish method** (how the directory tree becomes a content-addressed object, as a new file seed);
  3. **Summary and cleanup** (what the board shows, and how temporary data is reclaimed after packaging so `out/` and `tmp/` do not fill the disk).
- Results are temporary by default. To keep them long term, the YAML must set `publish.pin: true` explicitly, and they should preferably land on a files node rather than sit in an exec node's worktree.
- The packaging step must not be dispatched to a GPU-less gateway as a way to "finish" a CUDA job. Packaging runs on the exec node for this job; publishing may push the seed to files / relay nodes.

## Package result is a required step

The **last** step of every job must be Package result, and it must point at that YAML with `with.file`. If it is missing, is not last, or has no file pointer, submit must reject the job. The conventional form is:

```yaml
jobs:
  test:
    steps:
      - name: Prepare
        uses: ptc/prepare@v0
        with:
          file: prepare.yaml
      - name: Run tests
        run: .\bin\app.exe --data in\scene --out out\results
      - name: Package result
        uses: ptc/package-result@v0
        with:
          file: package-result.yaml
```

Full fields are in [templates/package-result.full.yaml](templates/package-result.full.yaml). Earlier `run` steps, whether they succeeded or failed, should still finish at this step (on failure, at least package the logs), unless the job already failed during Prepare and no worktree was created.

## Package result YAML convention

| Field | Required | Description |
| --- | --- | --- |
| `apiVersion` | yes | Currently fixed at `ptc/v0`. |
| `kind` | yes | Fixed at `PackageResult`. |
| `metadata` | no | Name and description, for display only. |
| `collect` | yes | Paths to collect from the worktree. You may also set `from_prepare_outputs: true` to include the `outputs` declared in Prepare first. |
| `package` | no | Package format and size cap. The default is a content-addressed directory tree (consistent with P1 UnixFS / CAS), preserving the relative layout. |
| `publish` | no | How the package becomes a fetchable seed, whether it is pinned, and which class of node it should go to. |
| `summary` | no | Files and the exit code written into the job summary, for a GitHub Actions-style display in the WebUI. |
| `cleanup` | no | How to clean the worktree and unpackaged temporary files after packaging. |

`collect[].path` must fall inside this run's Prepare `layout`, and usually comes from writable `out/` (logs may also come from `tmp/`). A successful package produces a result seed (digest / CID) that later jobs can take back as a Prepare `seed`.

## Checks (required in the implementation)

- The last step of the job: `name: Package result` (or `id: package-result`), `uses` is `ptc/package-result` or `ptc/package-result@<version>`, and `with.file` points at a parseable Package result YAML.
- Every `required: true` collect path must exist after a successful run. If the run already failed, missing paths are allowed, but existing logs should still be packaged when possible.
- The package must not exceed `package.max_bytes` when that field is set.
- `cleanup` must not delete a result that has not been published successfully.

## Status

Both local and mesh execution can run Package result through `ptc submit`: collect paths, compute a content-addressed seed, and write `packages/<id>/manifest.json` plus the job summary. After a result package is published by digest, other nodes preferably pull it in blocks through standard Bitswap; older nodes fall back to the previous resource protocol after negotiation. The seed-only action does not run Package result and does not produce a result package.
