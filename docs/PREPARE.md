# Prepare convention

[English](PREPARE.md) | [中文](zh/PREPARE.md)

This document freezes the **prepare** YAML convention. Local `ptc submit` and mesh `--target` dispatch both follow this file. Do not invent a second set of fields.

## Principles

- Syntax and design follow GitHub Actions: `jobs`, `needs`, `steps`, `run`, `if`, `env`, and optional `strategy.matrix`.
- The implementation may copy and extend open-source parse / expression code, but it must not use `actions/runner` or `nektos/act` as the runtime.
- The core extension is the **Prepare step**: it does not expand details in the workflow; it only **points at a Prepare YAML**. Parsing that YAML must yield three implementation contracts:
  1. **Workspace environment** (working directory, shell, environment variables, timeouts, and resources);
  2. **Directory layout** (which directories exist in the worktree, which are read-only, which are writable);
  3. **File-fetch seeds** (content-addressed digest / CID values used to find a replica on the mesh and mount it into the matching directory).

A directory with more than 100 files is automatically treated as a single unit: one digest is computed for the directory, not a hash per small file. A worktree preview then shows only the file count, not the individual files. Directories with 100 files or fewer can still be expanded in the worktree. Bulk data of this kind is generally not split by inner file and reassembled into a new worktree.

CUDA must not be dispatched to a gateway without NVIDIA. Missing files are fetched from a replica; the compute job must not be reassigned to a node that has the data but no GPU.

## Prepare is a required step

The first step of an ordinary exec job must be Prepare, and it must point at that YAML with `with.file`. If it is missing, is not first, or has no file pointer, submit must reject the job and must not create a worktree. Seed-only jobs are the exception; see "Seed-only tasks" below. The conventional form is:

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
```

The workflow keeps only this pointer; the implementation contract all lives in the pointed-at YAML. Full fields are in [templates/prepare.full.yaml](templates/prepare.full.yaml). An example with job context is [templates/workflow.example.yaml](templates/workflow.example.yaml).

Later `run` steps default their working directory to the worktree root for that run. Every job must also end with [Package result](PACKAGE-RESULT.md), which turns results into a fetchable seed and writes the summary. When a step fails, the board shows job / step status the way GitHub Actions does.

## Prepare YAML convention

The parser reads this one file and should extract the `workspace`, `layout`, and `seeds` sections. `target` decides what class of node may run the job.

| Field | Required | Description |
| --- | --- | --- |
| `apiVersion` | yes | Currently fixed at `ptc/v0`. |
| `kind` | yes | Fixed at `Prepare`. |
| `metadata` | no | Name and description, for display only. |
| `target` | yes | Which class of node to select. Gateways and machines without NVIDIA are refused by default. |
| `workspace` | no | The environment later steps see: working directory, shell, `env`, timeouts, resources. |
| `layout` | no | Worktree directory layout. The default is `in/` (read-only), `bin/`, `out/`, and `tmp/` (the last two writable). |
| `seeds` | yes | File-fetch seeds. Each seed gives a content identifier and a destination path. It is materialized as a private copy and does not share a writable hard link with the seeding files. |
| `outputs` | no | Paths to collect into the summary after the run. |
| `data` | no | Pin policy for permanent data and cleanup policy for temporary data. |

A seed `class` is `permanent` (test inputs, third-party dependencies, fixed scene directories) or `temporary` (this debugging program, files that change per run). `seeds` must include at least one entry that locates the executable (`entrypoint: true`, or `workspace.entrypoint`). Every permanent object has at least one replica on the mesh, but the exec node is not guaranteed to have it locally; prepare uses the seed to find a replica and mount it.

## Checks (required in the implementation)

- The first step of the job: `name: Prepare` (or `id: prepare`), `uses` is `ptc/prepare` or `ptc/prepare@<version>`, and `with.file` points at a parseable Prepare YAML.
- `target.require_nvidia` defaults to `true`. The target node's `exec` must be available, or the job is refused with a reason.
- Every `required: true` seed must resolve to at least one replica (or a `local` file provided by the submitter). Otherwise prepare fails and later `run` steps do not execute.
- A seed's `dest` must fall inside a directory declared in `layout`. Permanent seeds must not be written into writable `out/` or `tmp/`.

## Status

Both local execution and `--target` mesh dispatch support `seeds.local` and published sha256 digests. The receiving node verifies the content and publishes it as a local replica. CID / dataset source resolution is not implemented yet. Example hashes in the full template are placeholders only.

## Seed-only tasks

`ptc/seed@v0` is a standalone action and must be the job's only step. It only fetches, verifies, saves, and publishes a resource set. It does not create a worktree, check for a GPU, run commands, or build a result package. A run record is still saved. Remotes still use the persistent queue, and follow / cancel still work. This action cannot be mixed with Prepare / run / Package result, and it does not accept step `run` / `if`.

Workflow:

```yaml
name: Resource replicas
jobs:
  seed:
    steps:
      - name: Seed resources
        uses: ptc/seed@v0
        with:
          file: seeds.yaml
```

`seeds.yaml` (replace the sample digests with real values from `ptc resources --json`):

```yaml
apiVersion: ptc/v0
kind: Seed
metadata:
  name: Disaster recovery replicas
seeds:
  - name: dataset
    digest: "sha256:0000000000000000000000000000000000000000000000000000000000000000"
  - name: program
    digest: "sha256:1111111111111111111111111111111111111111111111111111111111111111"
```

Each item may also use `local: {src: <path relative to the workflow>}`. `required` defaults to `true`; an optional item that fails is logged. Names must be unique. Do not set `workspace`, `layout`, `outputs`, `dest`, or `entrypoint`. The original directory digest is kept; blobs are stored in CAS under their original sha256. Directories may use `data: {pin_permanent: true}` to prevent a normal clean from deleting them; that pin option is not supported for blobs yet.

```text
ptc submit --workflow docs/templates/seed-workflow.example.yaml --dry-run --json
ptc submit --workflow docs/templates/seed-workflow.example.yaml --target replica-node --json
ptc submit --workflow testdata/seed/workflow.yaml --json
```

The first two commands use the [workflow template](templates/seed-workflow.example.yaml) and the [resource-set template](templates/seeds.example.yaml). Digests must be replaced before a real submit. The last command is a local example that does not need mesh resources. When `--target` is omitted, missing digests are fetched through the local daemon. A remote only requires `files` to be on; exec / GPU are not required, so the job can be deployed to a files node or a gateway. For seed-only jobs, `--target auto` selects a files node. To improve disaster recovery explicitly, submit separately to different named nodes. Replicas stay in seed storage and can still be served after the source node goes offline. This is not a periodic replica-repair controller.
