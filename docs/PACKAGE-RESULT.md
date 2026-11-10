# 任务 Package result 大纲

本文冻结 **Package result** 的 YAML 约定。本机执行和网格下发都按这份文档打包结果。

## 原则

- 与 Prepare 对称：workflow 里这一步不展开细节，只**指向一份 YAML**。解析这份 YAML，就能得到三件实现约定：
  1. **要收集的路径**（工作树里哪些结果、日志进入包装）；
  2. **包装与发布方式**（目录结构如何打成内容寻址对象，作为新的文件种子）；
  3. **摘要与清理**（看板展示什么，以及包装完成后如何回收临时数据，避免 `out/`、`tmp/` 把磁盘占满）。
- 结果默认是临时数据。若要长期保留，必须在 YAML 里明确 `publish.pin: true`，并优先放到 files 节点，而不是长期堆在 exec 节点的工作树里。
- 不能把包装步骤派到无 NVIDIA 的网关去「补跑」CUDA；包装在本次 job 的执行节点上进行，发布时可以把种子推到 files / relay 节点。

## Package result 是必须步骤

每个 job 的**最后一个** step 必须是 Package result，并用 `with.file` 指向那份 YAML。缺少、不是最后一条、或没有文件指针，提交时应直接拒绝。约定写法：

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

完整字段见 [templates/package-result.full.yaml](templates/package-result.full.yaml)。前面的 `run` 无论成功或失败都应收尾到这一步（失败时至少打包日志），除非 job 在 Prepare 阶段就已经失败、尚未建立工作树。

## Package result YAML 约定

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `apiVersion` | 是 | 目前固定 `ptc/v0`。 |
| `kind` | 是 | 固定 `PackageResult`。 |
| `metadata` | 否 | 名称与说明，仅用于展示。 |
| `collect` | 是 | 从工作树收集的路径列表；也可设 `from_prepare_outputs: true` 先纳入 Prepare 里声明的 `outputs`。 |
| `package` | 否 | 包装格式与体积上限。默认按内容寻址目录树（与 P1 的 UnixFS / CAS 一致），保留相对目录结构。 |
| `publish` | 否 | 包装结果如何成为可获取的种子，以及是否 pin、送到哪类节点。 |
| `summary` | 否 | 写入任务摘要的文件与退出码，供 WebUI 按 GitHub Actions 风格展示。 |
| `cleanup` | 否 | 包装完成后如何清理工作树和未纳入包装的临时文件。 |

`collect[].path` 必须落在本次 Prepare 的 `layout` 之内，且通常来自可写的 `out/`（日志也可以来自 `tmp/`）。包装成功后会得到一条结果种子（digest / CID），后续任务可以用它作为 Prepare 的 `seeds` 再取回来。

## 校验（实现时必须做）

- job 的最后一个 step：`name: Package result`（或 `id: package-result`），`uses` 为 `ptc/package-result` 或 `ptc/package-result@<version>`，且 `with.file` 指向一份可解析的 Package result YAML。
- 每条 `required: true` 的收集路径在成功的 run 之后必须存在；若 run 已失败，则允许缺失，但仍应尽力打包已有日志。
- 包装体积不得超过 `package.max_bytes`（若已设置）。
- `cleanup` 不得删掉尚未发布成功的结果。

## 状态

本机已可用 `ptc submit` 执行 Package result：收集路径、计算内容寻址种子、写入 `packages/<id>/manifest.json` 和任务摘要。结果包按摘要发布后，其他节点可通过现有资源下载协议拉取（不是 bitswap）。
