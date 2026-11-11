# 状态与路线图

[English](../ROADMAP.md) | [中文](ROADMAP.md)

实现时要遵守的设计要点见 [DESIGN.md](DESIGN.md)。完整草案以 [设计 Page](https://cursor.com/canvas/store-bfdc01c7-fd7a-4053-a2a7-a49302fbb19c/03f3f67c-b25a-4548-a67d-bef09a48a413) 为准。

当前版本是 `0.3.0`（见 `internal/version/version.go`）。GitHub `main` 上的已发布标签以 Releases 为准。

## 已完成

- **P0 组网**：受信任 CIDR、mDNS 与端口探测、网关扫描与中转、服务目录、本机白 / 黑名单与功能开关。
- **任务大纲与本机执行**：借鉴 Actions 的 job/step，首步必须是 Prepare、末步必须是 Package result；`ptc submit` 在本机按 Prepare → run → Package result 执行，并保存工作树快照。
- **种子**：目录按整体计算 DirDigest（目录单元），结果包与 Prepare 种子都按摘要发布。
- **网格共享**：运行记录与资源清单在全网可查；目录种子可经网格下载，并可跨中转。
- **只读看板**：中英双语 WebUI，任务 / 资源 / 节点分栏；资源列出 digest 与 bitswap 传输 CID；与 CLI 共用本机 `/api/v1` 和 `internal/query`。节点页显示种子盘和工作树盘的真实容量及 ptc 占用。
- **Windows 原生节点**：`docker/run-host-node.ps1` 让本机真实 GPU 以 `host` 节点加入 Docker 网格。
- **P2 网格下发**：`ptc submit --target` 把作业派到指定（或 `auto`）exec 节点，经 `/ptc/exec/1.0.0` 传输；目标按目录最短路径拉取已发布种子并校验摘要。exec 节点有持久 FIFO 队列（默认并发 1）。`ptc queue` / `ptc cancel` 仅 CLI。
- **存储分离**：种子（`cas/` + `packages/`）与每轮工作树分目录、分生命周期；清工作树不会删种子。路径可在配置、环境变量和 `--seeds-dir` / `--worktrees-dir` / `--runs-dir` 里分开设置。`ptc clean` 在本机回收结果包、记录、种子或残留工作树。
- **纯做种任务**：单独的 `ptc/seed@v0` action 获取并公开指定资源集合，不创建 workspace、不执行命令、不打结果包。可下发到只有 files 服务的节点以增加容灾副本；[完整 YAML 与用法](PREPARE.md#纯做种任务)，[可直接运行的本地示例](../../testdata/seed/workflow.yaml)。
- **Bitswap 数据面**：资源摘要发现仍走受信任 PTC 网格，数据按 256KiB 分块通过标准 Boxo Bitswap 交换，支持中转、校验、持久缓存与副本故障切换。大资源自动使用多层 Merkle 索引，不再受 10,000 块、约 2.44GiB 的固定上限约束；仍受磁盘空间、传输超时和中继连接配额限制。小资源保留 v1 manifest；提供与接收大资源的节点均须升级以支持 v2 索引。
- **副本拉取与 CID**：`ptc resources pull` 把校验后的副本写入本机并通告传输 CID；看板上的 CID 是传输 manifest，不是 UnixFS。
- **安装与更新**：`ptc install --ssh HOST`、`ptc update [--ssh HOST|--mesh]` 从官方 Release 下载并校验二进制，替换失败回滚。本机 `ptc update` 会写入 `install.json` 并重启正在运行的 daemon（`--no-restart` 可跳过）。`ptc update --mesh` 向已记录安装且允许远端更新的同主版本节点滚动升级，目标用 bitswap 拉取二进制。SSH 支持 Linux amd64/arm64、Windows amd64，保持严格主机密钥校验；本机更新支持 Linux/macOS，Windows 可更新另一个未运行的 exe。SSH 安装暂不写入远端 `install.json`。[参数与平台限制](CLI.md)。

## 未完成

- **P1 剩余**：UnixFS / filestore、CID / dataset 输入解析、分块缓存 GC；目录 manifest pin 已有，完整 pin 管理尚未提供。
- **P3**：官方完整产品界面（当前只有只读看板）。
- **P4 剩余**：操作系统服务安装；SSH 安装仍不写入远端 `install.json`。本机 / mesh 更新后的 daemon 重启，以及同主版本网格滚动升级已经提供。
- 提交者断开 follow 不会取消作业，需显式 `ptc cancel`。

## 验证

- **P0-A** 单元测试：策略、受信任 CIDR、目录合并、身份询问、网段扫描。运行：`go test ./...`。
- **P0-B** 同机多进程：`127.0.0.1/2/3` 上两台计算节点经一台网关互见。
- **P0-C** Docker 跨网段：两个互不直通的网段，加上一台无 NVIDIA GPU、可访问两端的白名单网关。测试栈在 `docker/test/`，用法见 [docker/README.md](../../docker/README.zh-CN.md)。还可以用 `docker/run-host-node.ps1 -TestMesh` 在 Windows 本机再起一个 `host` 节点（真实 GPU）。

P0 / P2 的已归档测试记录见 [TEST-REPORT-P0.md](TEST-REPORT-P0.md) 和 [TEST-REPORT-P2.md](TEST-REPORT-P2.md)。
