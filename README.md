# peer-task-control (ptc)

去中心局域网任务分发：每台机器跑同一个 daemon，按内容哈希按需拉取测试数据，把 CUDA 程序派到指定机器。跨网段由白名单网关转发。

## 当前进度

已完成：

- **P0 组网**：受信任 CIDR、mDNS 与端口探测、网关扫描与中转、服务目录、本机白 / 黑名单与功能开关。
- **任务大纲与本机执行**：借鉴 Actions 的 job/step，首步必须是 Prepare、末步必须是 Package result；`ptc submit` 在本机按 Prepare → run → Package result 执行，并保存工作树快照。
- **种子**：目录按整体计算 DirDigest（目录单元），结果包与 Prepare 种子都按摘要发布。
- **网格共享**：运行记录与资源清单在全网可查；目录种子经 libp2p 以 tar 下载，可跨中转。
- **只读看板**：中英双语 WebUI，任务 / 资源 / 节点分栏；与 CLI 共用本机 `/api/v1` 和 `internal/query`，查询结果同源。节点页显示种子盘和工作树盘的真实容量及 ptc 占用。
- **Windows 原生节点**：`docker/run-host-node.ps1` 让本机真实 GPU 以 `host` 节点加入 Docker 网格。
- **P2 网格下发**：`ptc submit --target` 把作业派到指定（或 `auto`）exec 节点，经 `/ptc/exec/1.0.0` 传输；目标按目录最短路径拉取已发布种子并校验摘要。exec 节点有持久 FIFO 队列（默认并发 1）。`ptc queue` / `ptc cancel` 仅 CLI。
- **存储分离**：种子（`cas/` + `packages/`）与每轮工作树分目录、分生命周期；清工作树不会删种子。路径可在配置、环境变量和 `--seeds-dir` / `--worktrees-dir` / `--runs-dir` 里分开设置。`ptc clean` 在本机回收结果包、记录、种子或残留工作树。
- **纯做种任务**：单独的 `ptc/seed@v0` action 获取并公开指定资源集合，不创建 workspace、不执行命令、不打结果包。可下发到只有 files 服务的节点以增加容灾副本；[完整 YAML 与用法](docs/PREPARE.md#纯做种任务)，[可直接运行的本地示例](testdata/seed/workflow.yaml)。
- **Bitswap 数据面**：资源摘要发现仍走受信任 PTC 网格，数据按 256KiB 分块通过标准 Boxo Bitswap 交换，支持中转、校验、持久缓存与副本故障切换。大资源自动使用多层 Merkle 索引，不再受 10,000 块、约 2.44GiB 的固定上限约束；仍受磁盘空间和传输超时限制。小资源保留 v1 manifest；提供与接收大资源的节点均须升级以支持 v2 索引。
- **安装与更新**：`ptc install --ssh HOST`、`ptc update [--ssh HOST]` 从官方 Release 下载并校验二进制，替换失败回滚。SSH 支持 Linux amd64/arm64、Windows amd64，保持严格主机密钥校验；本机更新支持 Linux/macOS，Windows 可更新另一个未运行的 exe。[参数与平台限制](docs/CLI.md)。

未完成：

- **P1 剩余**：UnixFS / filestore、CID / dataset 输入解析、分块缓存 GC；目录 manifest pin 已有，完整 pin 管理尚未提供。
- **P3**：官方完整产品界面（当前只有只读看板）。
- **P4 剩余**：服务安装、daemon 自动重启、全网协调滚动升级；当前 install/update 只替换单机二进制。
- 远程 `ptc clean --node`（清理只作用于本机）。提交者断开 follow 不会取消作业，需显式 `ptc cancel`。

设计草案：[peer-task-control 设计草案](https://cursor.com/canvas/store-bfdc01c7-fd7a-4053-a2a7-a49302fbb19c/03f3f67c-b25a-4548-a67d-bef09a48a413)。离线摘要见 [docs/DESIGN.md](docs/DESIGN.md)。

## 下载

预发布版从 [Releases](https://github.com/tigeregit/peer-task-control/releases) 下载对应平台的二进制，并用附件里的 `SHA256SUMS` 校验。推送 `v…` 标签会由 GitHub Actions 自动编译并发布。

## 要求

- Go 1.26+（以 `go.mod` 为准）
- 可选：Docker，用于跨网段验证

在国内下载依赖可以用：`go env -w GOPROXY=https://goproxy.cn,direct`。

## 快速开始

```text
go build -o ptc ./cmd/ptc
# Windows: go build -o ptc.exe ./cmd/ptc

# 准备配置：填写受信任 CIDR，不要写邻居 IP
copy config.example.yaml config.yaml   # Windows
# cp config.example.yaml config.yaml  # Linux

ptc daemon --config config.yaml
# 另开一个终端
ptc peers
ptc policy show
ptc skill print
```

先起 daemon，再等目录出现。同网段靠 mDNS 和端口探测；跨网段由 `services.relay: true` 的网关扫描受信任网段并推送目录。不要向使用者要邻居地址。

## 运行平台

协议和 daemon **不绑定 Windows**。当前用 Go 编写，已在 Windows（iamsbb2）和 Linux 上编译、跑过单测。第一版产品优先服务 Windows 上的 CUDA 工作流，但 Linux 节点（包括用 Docker 模拟的网关和计算节点）可以同样入网。Windows 服务安装属于后续 P4。

## 验证（按阶段）

- **P0-A** 单元测试：策略、受信任 CIDR、目录合并、身份询问、网段扫描。
- **P0-B** 同机多进程：`127.0.0.1/2/3` 上两台计算节点经一台网关互见。
- **P0-C** Docker 跨网段：两个互不直通的网段，加上一台无 NVIDIA GPU、可访问两端的白名单网关。还可以用 `docker/run-host-node.ps1` 在 Windows 本机再起一个 `host` 节点（真实 GPU），验证原生 Windows daemon 也能入网。

```text
go test ./...

# 在仓库根目录，预先编出 Linux 二进制后做跨网段模拟：
# Windows（已装 Go 和 Docker）:
powershell -File docker/p0-test.ps1
# Linux:
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o docker/ptc-linux ./cmd/ptc
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o docker/tcpprobe-linux ./docker/cmd/tcpprobe
docker compose -f docker/docker-compose.yml up -d --build
sh docker/p0-test.sh
python3 docker/p0-assert.py
docker compose -f docker/docker-compose.yml down -v
```

## 命令

完整参数、默认值、输出格式和本机 API 见 [docs/CLI.md](docs/CLI.md)。P0 测试报告见 [docs/TEST-REPORT-P0.md](docs/TEST-REPORT-P0.md)。


| 命令 | 作用 |
| --- | --- |
| `ptc daemon` | 监听固定端口，加入组网 |
| `ptc peers` | 列出已知节点及执行 / 文件 / 中转服务 |
| `ptc policy` | 查看或设置本机白名单、黑名单和功能开关 |
| `ptc skill print` | 输出与当前版本绑定的 skill |
| `ptc version` | 打印版本 |
| `ptc install --ssh HOST` | 经 SSH 安装经过 SHA256 校验的官方 Release 二进制 |
| `ptc update [--ssh HOST]` | 更新本机或一个 SSH 目标的二进制，失败回滚 |
| `ptc submit` | 本机执行，或 `--target` 下发到网格 exec 节点（默认 follow） |
| `ptc webui` | 本机只读任务看板（中英双语；任务 / 资源 / 节点分栏；查询与下载。不提供改策略或提交作业） |
| `ptc queue` | 列出各节点排队中 / 正在运行的作业 |
| `ptc cancel` | 取消排队中或正在运行的作业（仅 CLI） |
| `ptc tasks` | 列出或查看网格内公开的运行记录（与看板共用接口和筛选语义） |
| `ptc clean` | 本机清理结果包、运行记录、种子或工作树 |
| `ptc resources` | 列出全网种子，查看大小 / 所在设备 / 中转次数，或下载（与看板共用接口；`--list-files` 可列出超大目录树的路径） |

查询类能力与 WebUI 同源（同一套本机 `/api/v1` 和 `internal/query`）。策略变更、`submit`、`cancel`、`clean`、启动 daemon 等控制操作只在 CLI 里提供，看板保持只读。可选的 Ubuntu CUDA 节点见 `docker/docker-compose.cuda.yml`（官方 `nvidia/cuda:12.9.2-devel-ubuntu22.04`，不要写进默认 live compose）。

## 仓库约定

贡献与排障请看 [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md)（含 PR、commit、issue 规则）。Agent 应使用与二进制同版本的 `ptc skill print` 输出。
