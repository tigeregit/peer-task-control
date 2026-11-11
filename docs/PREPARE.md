# 任务 Prepare 大纲

本文冻结 **prepare** 的 YAML 约定。本机 `ptc submit` 和网格 `--target` 下发都按这份文档执行，不要另发明一套字段。

## 原则

- 语法和设计理念借鉴 GitHub Actions：`jobs`、`needs`、`steps`、`run`、`if`、`env`、可选的 `strategy.matrix`。
- 实现可以参考并 copy 开源解析/表达式代码再扩展，但不把 `actions/runner` 或 `nektos/act` 直接当作运行时。
- 我们扩展的核心是 **Prepare 步骤**：它不在 workflow 里展开细节，只**指向一份 Prepare YAML**。解析这份 YAML，就能得到三件实现约定：
  1. **workspace 环境**（工作目录、shell、环境变量、超时与资源）；
  2. **目录结构**（工作树里有哪些目录、哪些只读、哪些可写）；
  3. **文件获取种子**（用内容寻址的 digest / CID 等，到网格里找副本并挂入对应目录）。

超过 100 个文件的目录会自动按整个目录为操作单位：只为该目录计算一个 digest，不为每个小文件单独哈希。工作树预览展开时只显示文件数量，不列出具体文件。不超过 100 个文件的目录仍可在工作树中展开查看文件。这类大体量数据一般也不会按内部文件拆开再组合成新的工作树。

CUDA 不得派到无 NVIDIA 的网关。缺文件时只从副本取数，不能因此把计算改派到只有数据、没有 GPU 的节点。

## Prepare 是必须步骤

普通执行 job 的第一个 step 必须是 Prepare，并且用 `with.file` 指向那份 YAML。缺少、不是第一条、或没有文件指针，提交时应直接拒绝，不建工作树。纯做种 job 是例外，见下方「纯做种任务」。约定写法：

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

workflow 里只保留这一处指针；实现约定全部写在被指向的 YAML 里。完整字段见 [templates/prepare.full.yaml](templates/prepare.full.yaml)，带 job 上下文的示例见 [templates/workflow.example.yaml](templates/workflow.example.yaml)。

后续 `run` 的工作目录默认是该次运行的工作树根目录。每个 job 还必须以 [Package result](PACKAGE-RESULT.md) 作为最后一个步骤，把结果打成可获取的种子并写入摘要。步骤失败时，看板按 GitHub Actions 的方式展示 job / step 状态。

## Prepare YAML 约定

解析器读这一份文件，应能抽出 `workspace`、`layout`、`seeds` 三段；`target` 决定跑在哪类节点。

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `apiVersion` | 是 | 目前固定 `ptc/v0`。 |
| `kind` | 是 | 固定 `Prepare`。 |
| `metadata` | 否 | 名称与说明，仅用于展示。 |
| `target` | 是 | 选哪类节点；默认拒绝网关和没有 NVIDIA 的机器。 |
| `workspace` | 否 | 后续步骤看到的环境：工作目录、shell、`env`、超时、资源。 |
| `layout` | 否 | 工作树目录结构。缺省为 `in/`（只读）、`bin/`、`out/`、`tmp/`（后两者可写）。 |
| `seeds` | 是 | 文件获取种子。每条种子给出内容标识和落盘路径；本机已有则去重硬链接。 |
| `outputs` | 否 | 运行结束后要收集到摘要里的路径。 |
| `data` | 否 | 永久数据 pin、临时数据清理策略。 |

种子的 `class` 为 `permanent`（测试输入、第三方依赖、固定场景目录）或 `temporary`（本次调试程序、按 run 变化的文件）。`seeds` 里至少要有一条能定位到可执行程序（`entrypoint: true`，或 `workspace.entrypoint`）。网格中每份永久数据至少有一个副本，但不保证执行节点本地已有；prepare 用种子去找副本并挂入。

## 校验（实现时必须做）

- job 的第一个 step：`name: Prepare`（或 `id: prepare`），`uses` 为 `ptc/prepare` 或 `ptc/prepare@<version>`，且 `with.file` 指向一份可解析的 Prepare YAML。
- `target.require_nvidia` 默认为 `true`；目标节点 `exec` 必须可用，否则拒绝并写明原因。
- 每条 `required: true` 的种子必须能解析到至少一份副本（或提交端提供的 `local` 文件），否则 prepare 失败，后续 `run` 不执行。
- 种子的 `dest` 必须落在 `layout` 声明过的目录内；永久种子不得写到可写的 `out/`、`tmp/`。

## 状态

本机与 `--target` 网格下发均支持 `seeds.local` 和已发布的 sha256 digest；接收节点校验后自动发布为本机副本。CID / dataset 源解析尚未实现。完整模板里的示例哈希只是占位。

## 纯做种任务

`ptc/seed@v0` 是独立 action，必须是 job 唯一的 step。它只获取、校验、保存并公开资源集合；不会创建工作树、检查 GPU、运行命令或打结果包。运行记录仍会保存，远端仍使用持久队列，可 follow / cancel。此 action 不能与 Prepare / run / Package result 混用，也不接受 step `run` / `if`。

workflow：

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

`seeds.yaml`（把示例摘要替换为 `ptc resources --json` 中的真实值）：

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

每项也可用 `local: {src: <相对于 workflow 的路径>}`，默认 `required: true`；可选项失败会记日志。名称必须唯一。不填写 `workspace`、`layout`、`outputs`、`dest` 或 `entrypoint`。目录原摘要保留，blob 按原 sha256 放入 CAS；目录可用 `data: {pin_permanent: true}` 防止普通 clean 删除，blob 暂不支持此 pin 选项。

```text
ptc submit --workflow docs/templates/seed-workflow.example.yaml --dry-run --json
ptc submit --workflow docs/templates/seed-workflow.example.yaml --target replica-node --json
ptc submit --workflow testdata/seed/workflow.yaml --json
```

前两条使用 [workflow 模板](templates/seed-workflow.example.yaml) 和 [资源集合模板](templates/seeds.example.yaml)，实际提交前必须替换摘要。最后一条是无需网格资源即可运行的本地示例。省略 `--target` 时，缺失的 digest 通过本机 daemon 获取；远程只要求 `files` 开启，不要求 exec / GPU，因此可部署到文件节点或网关。`--target auto` 对纯做种任务只选择 files 节点；要明确提升容灾性，应向指定的不同节点分别提交。副本留在种子存储，源节点离线后仍可继续提供资源；不是周期性副本修复控制器。
