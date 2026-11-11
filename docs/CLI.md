# ptc CLI 参考

对应开发版本：**0.3.0-dev**。正式构建的版本由 Release 标签写入；以 `ptc version` 和 `ptc --help` 为准。

在线帮助：

```text
ptc --help
ptc <命令> --help
```

本文只记录已经实现的接口。尚未实现的命令见文末。

## 全局参数

所有子命令都接受这些参数：

| 参数 | 默认值 | 作用 |
| --- | --- | --- |
| `--config` | 数据目录下的 `config.yaml`（见下方「数据目录」） | 配置文件路径。`ptc daemon` **必须**能读到这份文件。 |
| `--data-dir` | 配置里的 `data_dir`；若配置还不存在，则用默认数据目录 | 覆盖数据目录（身份密钥、`daemon.lock`）。环境变量 `PTC_DATA_DIR`。 |
| `--seeds-dir` | `storage.seeds_dir`，默认等于 `data_dir` | CAS 与 `packages/` 的父目录。环境变量 `PTC_SEEDS_DIR`。 |
| `--worktrees-dir` | `storage.worktrees_dir`，默认 `data_dir/worktrees` | 每轮运行的工作树根目录。可设到另一块盘，例如 Windows 的 `D:\ptc\worktrees`。环境变量 `PTC_WORKTREES_DIR`。 |
| `--runs-dir` | `storage.runs_dir`，默认 `data_dir/runs` | 运行记录和作业队列。环境变量 `PTC_RUNS_DIR`。 |
| `--api` | 空：从 `daemon.lock` 读取 API 地址 | 直接指定本机控制 API 的 `host:port`，不再读 lock 文件。 |

`ptc peers`、`ptc policy` 等控制命令不启动 daemon，而是通过本机 loopback API 与已在运行的 daemon 通信。

查找 API 地址的顺序：

1. 若传了 `--api`，使用该地址。
2. 否则读取 `<data-dir>/daemon.lock` 里的 `api` 字段。
3. 找不到 lock 文件时退出，并提示 daemon 是否在运行。确定 `--data-dir` 的方式是：显式 `--data-dir`，否则尝试加载 `--config` 并使用其中的 `data_dir`，再否则使用默认数据目录。

## 数据目录

默认数据目录由 `os.UserConfigDir()` 决定：

- Windows：`%APPDATA%\ptc`（通常是 `C:\Users\<用户>\AppData\Roaming\ptc`）
- Linux：`$XDG_CONFIG_HOME/ptc`，未设置时为 `~/.config/ptc`
- 若拿不到配置目录：`~/.ptc`；再不行则是当前目录下的 `.ptc`

默认约定（可用 `storage.*` 或对应参数改到别的路径，包括另一盘符）：

| 路径 | 说明 |
| --- | --- |
| `config.yaml` | 若使用默认 `--config` 路径，daemon 从这里读配置。 |
| `identity.key` | 节点 libp2p 身份密钥，首次启动生成（仍在 `data_dir`）。 |
| `daemon.lock` | daemon 启动时写入，正常退出时删除。含 `pid`、`api`、`peer_id`、`name`。 |
| `cas/`、`packages/` | 种子与结果包（在 `seeds_dir` 下）。与工作树分离；删除工作树不会删除这里的数据。 |
| `blocks/` | `seeds_dir` 下持久化的 Bitswap raw SHA256 分块缓存，计入统一做种限额；安全清种时按保留资源的引用回收无用块。 |
| `worktrees/` | 每次运行物化出的私有工作空间；成功、失败、取消后立即回收。也可用 `ptc clean worktrees` 清理旧目录。 |
| `.fetch/` | 种子存储中的下载 / bundle 暂存，不在工作树盘；纯做种任务也可使用。 |
| `runs/` | 运行记录 JSON 与 `runs/queue/` 中的持久作业队列。 |

### 全局存储规则

```yaml
storage:
  size_limit: 40GB
  delete_size: 20GB
  workspace_limit: 0
```

`size_limit` 统计 `packages/`（包括 output）、CAS、Bitswap blocks 和 `.fetch/` 的文件字节合计；不是仅统计输入，也不按 output、permanent、temporary 分类豁免。重复存储及硬链接路径按各路径的逻辑大小计量。`GB` 为十进制，也接受 `GiB` 或整数 bytes。省略 `delete_size` 时取上限的一半；要求 `0 < delete_size <= size_limit`。`workspace_limit` 是节点**全部 workspace 合计**，默认 0，不限制。

若做种盘的 free 小于配置上限，实际上限为 `max(0, free - 1GB)`，清理目标为 `delete_size / size_limit * 实际上限`。例如 40GB/20GB、free=11GB，实际为 10GB/5GB。达到上限（或新输入预留空间无法容纳）时，daemon 尝试至少释放该清理目标；没有足够可安全删除的数据时明确报错，不能为了达标删除唯一副本。

删除前必须让另一个支持 `/ptc/retention/1.0.0` 的 files 节点完整校验副本，并把保留承诺持久化后回复确认。双方同时清理、断网或 ACK 丢失都不能授权删除最后一份；协商冲突时保守保留，后台带抖动重试。承诺不依赖 TTL；发起方重启后重试释放未完成事务。不要手动删除 `.retention/`、`.retention-lock/`、`.reservations/`、`.quota-lock/`。旧协议节点不能作为安全删除的 keeper；升级须覆盖 daemon 和本机 CLI，不能用旧 CLI 绕过保护。

`pin`、活跃任务输入及正在写的结果受保护；已完成的 output 与其他普通种子使用同样的自动清理规则。块索引缺失或损坏时保守保留缓存并记录原因。Prepare 在下载前检查全部输入大小及并发预留，输入本身超限直接将 job 标为 failure，不进入下载/清除循环；还会为块缓存、暂存及元数据预留额外空间。纯做种、独立 `resources pull` 和结果发布同样受限。没有 daemon 的本地任务不能协商网络清理，空间不足时直接报错。

workspace 必须与实际做种、下载、记录和协调元数据目录分离，不得相同或互相嵌套；符号链接别名也会校验。默认仍可共同位于 `data_dir`，其中 `worktrees/` 与 `cas/`、`packages/` 等是兄弟目录。自定义 `layout.root` 必须严格位于配置的 workspace 根目录下。任务的成功、失败、取消及 Prepare 失败都会在释放执行 slot 前回收 workspace；启动时也会回收终态记录指向的旧 workspace，不删除目录外路径或仍被锁定的工作区。工作区与种子使用私有文件副本，工作区写入不改变做种数据。

运行时 workspace 每约 250ms 检查一次，做种空间每约 1s 检查一次，并在步骤边界复查；超限取消进程树并将 job 标为 failure。监控中的网络清理请求带 3 秒期限，网络协商及文件系统遍历可能延长检查间隔。这是应用层配额，不是文件系统硬配额，写入可能在检查间隔内短暂超过阈值。唯一或受保护资源仍保留并报告容量不足。`--keep-worktree` 对执行任务报错；旧 YAML 的 `cleanup.when/evict_worktree` 不再允许保留终态 workspace。运行记录和队列历史不在本规则中自动删除。

## 退出码

- `0`：成功（`ptc daemon` 在收到 `SIGINT` / `SIGTERM` 后正常退出也是 `0`）。
- `1`：错误。包括工作流校验失败、本机/远端执行失败、目标不存在、`require_nvidia` 作业被派到无 GPU / exec 关闭 / 网关而被拒绝、策略拒绝、队列已满、清理缺少 `--yes`、以及与 daemon 通信失败。错误信息写到 stderr（`--json` 时关键字段也在 JSON 里）。
- `1`：命令失败。错误写到 stderr。

## 命令

### `ptc version`

打印二进制版本（开发构建为 `0.3.0-dev`，Release 构建为标签去掉 `v`），后接换行。不需要配置或正在运行的 daemon。

### `ptc skill print [topic]` / `ptc skill list`

打印与当前二进制绑定的 agent skill。`ptc skill print` 输出索引；`ptc skill print <topic>` 输出指定主题（`submit`、`resources`、`install`、`discovery`、`storage`、`errors`）；`ptc skill list` 列出主题，可加 `--json`。

正文始终来自编译进二进制的内嵌文本，并在 `print` 末尾追加一行 `# bound-to-version <版本>`。不再读取当前目录或旧安装残留的 `SKILL.md`，避免更新后 usage 与实际二进制能力不一致。仓库根目录 `SKILL.md` 必须与 `cmd/ptc/skill/SKILL.md` 保持一致。

不需要正在运行的 daemon。

### `ptc install --ssh HOST` / `ptc update [--ssh HOST|--mesh]`

从官方 `tigeregit/peer-task-control` Release 下载对应平台的二进制及 `SHA256SUMS`，校验后替换，失败时回滚。`install` 与 SSH / 本机 `update` 无需 daemon。这些命令**不安装系统服务**。本机 `ptc update` 成功后会写入 `install.json`，并重启正在运行的本机 daemon（可用 `--no-restart` 跳过）。`--mesh` 需要本机 daemon，向已有 `install.json` 且 `update.accept_remote` 为开的节点分阶段推送同一主版本；目标节点用 bitswap 拉取二进制、校验、替换并请求重启。SSH 安装目前只替换远端二进制，不会写入 `install.json`，因此不会自动成为 mesh 更新目标。

mesh 更新和手工 daemon 重启要求目标没有排队或运行中的任务；更新/重启期间拒绝新任务。mesh 更新只有在目标重新通告新版本后才算成功；未重入会标为失败并停止后续批次。`--json` 仍输出单个报告，但出现失败时命令退出码非零。`install.json` 必须指向该 daemon 正在运行的二进制。

```text
ptc install --ssh user@linux-node --version 0.2.0-p2
ptc install --ssh user@windows-node --version 0.2.0-p2 --path C:\Tools\ptc.exe
ptc update --ssh user@windows-node --version 0.2.0-p2 --path C:\Tools\ptc.exe
ptc update --version 0.2.0-p2
# Windows: run a different ptc.exe to update a non-running installed copy
ptc update --version 0.2.0-p2 --path C:\Tools\ptc.exe
ptc update --version 0.3.0 --no-restart
ptc update --mesh --version 0.3.0
ptc update --mesh --dry-run --json
ptc update --mesh --nodes worker-a,worker-b --batch 1
```

| 参数 | 默认值 | 作用 |
| --- | --- | --- |
| `--ssh HOST` | 空 | install 必需；update 省略时更新本机。接受 OpenSSH alias、主机名、地址或 `user@host`。 |
| `--version` | `latest` | 最新稳定 Release；预发布版须显式指定标签版本。 |
| `--path` | 见下文 | 本机目标，或 SSH 目标上的绝对文件路径。 |
| `--identity` | OpenSSH 默认 | SSH 私钥文件；加密私钥建议通过 ssh-agent 使用。 |
| `--known-hosts` | OpenSSH 默认 | 替代 known_hosts 文件；不会关闭校验。 |
| `--port` / `-p` | `22` | SSH 端口。 |
| `--mesh` | 关 | 由本机 daemon 向可管理节点滚动升级；不能与 `--ssh` 同时使用。 |
| `--nodes` | 全部可管理节点 | 仅这些节点名或 peer ID。 |
| `--batch` | `1` | 每阶段并行更新的节点数；上一阶段须重新加入目录后才进入下一阶段。 |
| `--assets-dir` | 空 | 含各平台二进制和 `SHA256SUMS` 的本地目录，供离线网格使用。 |
| `--dry-run` | 关 | 只打印 mesh 计划，不改节点。 |
| `--json` | 关 | 仅 `--mesh`：打印 JSON。 |
| `--no-restart` | 关 | 本机更新后不重启本机 daemon。 |

SSH 自动探测 Linux amd64/arm64 或原生 Windows amd64；macOS SSH、Windows ARM64、UNC 路径不支持。Linux 默认安装到 `$HOME/.local/bin/ptc`，Windows 为 `%LOCALAPPDATA%\Programs\ptc\ptc.exe`。Windows 通过本机 OpenSSH `scp` 上传二进制和替换脚本，再以短编码 PowerShell 命令启动，避免默认 cmd.exe 的命令长度限制及 Windows SSH 二进制 stdin 问题；因此发起端还须有 `scp`，目标须提供 SFTP。检查 SHA256 和版本，更新时保留原目标 ACL，清理临时部署目录；目标被运行中的进程锁定时会报错。

SSH 必须已配置非交互认证，且 known_hosts 已信任目标；使用 `BatchMode=yes` 和 `StrictHostKeyChecking=yes`，不接受密码提示或未知主机密钥。下载发生在发起命令的本机，使用正常 HTTPS 代理环境。

本机 update 支持 Linux/macOS 自更新。Windows 不能覆盖当前运行的 exe，须从另一个副本执行并用 `--path` 指定未运行的已安装 PTC。显式本机目标必须存在且通过 `ptc version` 探测。SSH 专用的 identity/known-hosts/port 参数不能用于本机模式。

`--dry-run`、`--json`、`--assets-dir`、`--nodes`、`--batch` 只能与 `--mesh` 一起用。

`--mesh` 默认把本机二进制的版本推到网格（也可 `--version` / `latest`）。目标只接受受信对端、同一主版本且不低于当前版本的更新。二进制先进入发起节点的 CAS，再由目标用 bitswap 拉取；不要把它理解成各节点各自访问 GitHub。节点需已有 `data_dir/install.json`（本机 `ptc update` 会写这份清单）。`ptc policy update off` 会把 `update.accept_remote` 设为 false，拒绝远端推送（默认允许）。

### `ptc daemon`

启动 daemon，加入组网，并在本机打开控制 API。前台运行，日志和提示写到 stderr。

```text
ptc daemon --config config.yaml
ptc daemon --config config.yaml --data-dir D:\ptc-data
```

行为要点：

- 必须能打开 `--config` 指向的文件；文件不存在时会提示先复制 `config.example.yaml` 并填写 `trusted_cidrs`。
- `--data-dir` 会覆盖配置里的 `data_dir`。
- 监听组网端口（默认 `0.0.0.0:18765`）和本机 API（默认 `127.0.0.1:18780`）。
- 控制 API 只接受 loopback 来源。
- 同网段用 mDNS 和启动时的端口探测发现邻居；`services.relay: true` 的网关扫描全部受信任网段并推送目录。默认只在启动时扫描一次，后续被动接收发现/连接；需要周期探测时显式配置正数 `scan_interval`。
- 经 NAT 或 Docker 端口映射访问网关时，应同时映射组网端口的 TCP 和 UDP。节点与网关重连后会立即重新预约中继；预约失败会记录警告。
- 目录中的 loopback 地址属于公布地址的节点，不会被其他节点当作直连地址。同机运行多个节点时，可以用 `scan_hosts` 显式指定各自的 `127.0.0.1:端口`。
- 配置里**不要**填写邻居名单。
- 启动成功后 stderr 会打印节点名、peer ID 和 API 地址，并提示随后使用 `ptc peers`。
- 用 Ctrl+C 或 SIGTERM 停止。
- 本机 `ptc update` 或 mesh 更新替换二进制后，daemon 会按请求重启（Unix 用 exec 保持同一 PID；Windows 先拉起新进程再退出，并用隐藏的 `--wait-pid` 等待旧进程释放端口）。

### `ptc scan`

```text
ptc scan
ptc scan --node gateway
ptc scan --node <relay-peer-id> --json
```

省略 `--node` 时请求本机 daemon 扫描一次；本机为 relay 时扫描全部 `trusted_cidrs`，否则只扫描本机受信任网段。配置的 `scan_hosts` 仍会一并探测。指定 `--node` 时经本机 daemon 向已知 relay 发出 `/ptc/scan/1.0.0` 请求；目标必须开启 relay，且允许调用者通过 allow/deny 策略检查。不向未知节点猜测连接地址，也不支持旧 daemon 自动回退扫描。

```yaml
scan_interval: 0s
scan_min_interval: 60s
```

`scan_interval` 省略或为 `0s` 表示无限周期：启动时仅扫描一次，之后不会自行重复扫描。改成 `5m` 等正数后，每次扫描结束再等待该周期；手动扫描不会启用原本关闭的周期任务。修改策略本身不触发扫描。原配置若明确写有 `30s`，升级后仍按 30 秒周期执行。mDNS、入站连接和已建立连接上的目录/中继维护不因关闭周期探测而停用。

`scan_min_interval` 是**目标节点全部手动请求共用**的最小接受间隔，默认 60 秒；本机 CLI、所有远端 peer 都共用一个额度。扫描已排队/运行时立即拒绝；冷却期内立即拒绝，不排队补跑，也不因拒绝延长冷却。启动和周期扫描不消耗手动额度，但进行中仍阻止重复手动扫描。额度在 daemon 重启后重置。

成功仅表示已接受排队，不表示扫描已经完成。`--json` 输出 `node`、`peer_id`、`status`，拒绝时包含 `error` 和适用的 `retry_after_seconds`，命令退出码非零。HTTP 接受返回 202，忙返回 409，限频返回 429 并带 `Retry-After`，策略/非 relay 拒绝返回 403；本地 API 转发到远端时保留这些状态。

### `ptc peers`

列出本机 daemon 已知的节点及其服务。需要 daemon 已在运行。

```text
ptc peers
ptc peers --json
ptc peers show gateway
ptc peers show 12D3KooW…
ptc peers --data-dir D:\ptc-data
ptc peers --api 127.0.0.1:18780
```

| 参数 | 默认 | 作用 |
| --- | --- | --- |
| `--json` | 关闭 | 向 stdout 打印缩进 JSON，而不是表格。 |

`ptc peers show NAME|PEER` 查看一台设备的硬件信息（系统、GPU、显存、驱动、地址、服务）、该节点的历史任务，以及它存储的种子。与 WebUI 节点详情页使用同一套数据：`GET /api/v1/status`、`/api/v1/runs`、`/api/v1/resources`，再用与看板相同的节点过滤语义组合。`--json` 同样适用于 `show`。

默认输出第一行是本机信息，随后是制表符分隔的表格：

```text
local <名称>  peer <peer ID>  version <版本>

NAME  PEER ID  EXEC  FILES  RELAY  ADDRS
```

`PEER ID` 列超过 16 个字符时截断并加省略号。服务列的取值：

| 单元格 | 含义 |
| --- | --- |
| `yes` | 该服务当前对外提供（`offered: true`） |
| `no (<原因>)` | 不提供，并带拒绝原因 |
| `no (disabled)` | 本机关闭了该功能，且没有更具体的原因 |
| `no` | 不提供，无原因 |
| `-` | 目录条目里没有该项服务 |

关闭了部分服务的节点仍会出现在目录中，只是对应列会标明原因。例如没有 NVIDIA GPU 的网关，`EXEC` 可能是 `no (this node has no NVIDIA GPU)`。

`--json` 输出的是控制 API 的状态对象，不只是邻居列表，字段如下：

```json
{
  "name": "worker-a",
  "peer_id": "<libp2p peer id>",
  "version": "0.3.0-dev",
  "listen": ["<multiaddr>", "..."],
  "peers": [
    {
      "peer_id": "...",
      "name": "gateway",
      "addrs": ["..."],
      "services": {
        "exec":  { "offered": false, "enabled": false, "reason": "this node has no NVIDIA GPU" },
        "files": { "offered": true,  "enabled": true },
        "relay": { "offered": true,  "enabled": true }
      },
      "caps": {
        "nvidia": false,
        "gpu_name": "",
        "vram_mib": 0,
        "driver": "",
        "os": "linux",
        "arch": "amd64"
      },
      "updated_at": "2026-11-10T14:00:00Z"
    }
  ]
}
```

`services.*.enabled` 是该节点自己的功能开关；`offered` 是目录里实际对外宣称可提供（例如 `exec` 还要求本机检测与 NVIDIA GPU）。`caps` 中未探测到的字段可能为空或为零值。上面的示例值仅说明结构，运行时以实际输出为准。

### `ptc webui`

在本机打开只读任务看板，默认监听 `127.0.0.1:18790`。界面为中英双语（可切换），风格接近 agentboard：顶栏、统计卡片、任务列表。点击一条任务可查看类似 GitHub Actions 的运行摘要：状态、触发方式、耗时、作业与步骤、正在运行的步骤，以及日志摘要。

看板会持续显示一条实时任务「网格目录监视 / Mesh catalog watch」，并汇总本机 `--tasks` / `--runs` 以及网格里其他节点公开的运行记录（守护进程 `GET /api/v1/runs` 会向邻居拉取 `/ptc/runs/1.0.0`）。任务和资源列表都支持搜索、状态/类型筛选和排序。顶栏还有「资源」分栏：列出全网种子的 digest、bitswap 传输 CID、大小、所在节点数、创建时间，以及到当前设备（看板所连接的 daemon）的最短中转次数，并支持下载。列表可按列头升序或降序排序；digest 去掉 `sha256:` 前缀，默认只显示哈希前 8 位加 `***`，点眼睛按钮可展开。任务摘要里的结果种子可点进对应资源页。点击「节点」里的某一台设备，可查看硬件信息、历史任务和该节点存储的种子；再点具体任务或种子会跳到对应分栏。CLI 与看板共用同一套本机控制接口和查询语义（`internal/query`）。看板是只读界面：任务 / 资源 / 节点的查看、筛选、排序、搜索和种子下载都可以在 CLI 里用同样的参数完成（`ptc tasks`、`ptc resources`、`ptc peers` / `peers show`）。CLI 额外提供控制能力，看板不会代理这些写操作：`ptc daemon`、`ptc policy`（改白名单 / 黑名单 / 服务开关）、`ptc submit`（本机或 `--target` 下发）、`ptc cancel`、`ptc clean`，以及 `ptc resources show --list-files`（展开超过 100 个文件的目录树）。看板会列出邻居执行完的作业（包括 CUDA），但不能提交或取消。

Docker 部署编排默认启动 `gateway`（中转 + 文件，无 GPU）、`files`（只做种）和只读 `webui`。还可以在 Windows 本机启动原生 `host` 节点（真实 NVIDIA GPU）：先保证部署网格已 `up`，再在仓库根目录运行 `powershell -File docker/run-host-node.ps1`。网关通过 `scan_hosts: host.docker.internal` 探测本机 `18765` 端口。停止用 `powershell -File docker/run-host-node.ps1 -Stop`。若要加入 P0-C 测试网格，使用 `powershell -File docker/run-host-node.ps1 -TestMesh`。

可选的 Ubuntu CUDA 执行节点不要写进默认部署编排。在已有 NVIDIA Container Toolkit / Docker Desktop GPU 直通的机器上，用叠加文件启动官方 `nvidia/cuda:12.9.2-devel-ubuntu22.04` 节点（名为 `cuda`，exec+files，无中继，GPU 以容器内 `nvidia-smi` 为准）：

```text
docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml up -d --build cuda
docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml stop cuda
docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml rm -f cuda
```

先确认 `docker run --rm --gpus all nvidia/cuda:12.9.2-devel-ubuntu22.04 nvidia-smi` 能看到真实 GPU。本仓库在驱动 616.56 上试过同样已发布的 `13.5.0-devel-ubuntu22.04`，被 `nvidia-container-cli` 以 `cuda>=13.5` 拒绝，因此叠加层使用 12.9.2。示例作业：`ptc submit --workflow testdata/cuda-vectoradd/workflow.yaml --target cuda`。完整部署说明见 [docker/README.md](../docker/README.md)。


```text
ptc webui
ptc webui --listen 127.0.0.1:18790 --api 127.0.0.1:18780
```

| 参数 | 默认值 | 作用 |
| --- | --- | --- |
| `--listen` | `127.0.0.1:18790` | 看板监听地址。Docker 模拟里会改成 `0.0.0.0:18790`，并映射到主机的 `127.0.0.1:18790`。 |
| `--tasks` | 空 | 可选的已记录任务 JSON（`{"tasks":[...]}`）。 |
| `--report` | 空 | `--tasks` 的兼容别名。 |
| `--runs` | 空 | 本机 `runs/*.json` 目录；网格记录仍由 daemon 的 `/api/v1/runs` 合并进来。 |

相关接口：`GET /`、`GET /live/tasks`、`GET /live/status`、`GET /live/resources`、`GET /live/resources/sha256/{hex}`、`GET /live/resources/sha256/{hex}/content`、`GET /live/meta`。`GET /live/report` 会重定向到 `/live/tasks`。

部署编排和跨网段测试都会把看板映射到主机的 `http://127.0.0.1:18790`。默认部署 compose 不会挂载录制的测试任务，也不会给容器节点伪装 GPU；节点硬件以 daemon 探测到的真实信息为准。需要跑官方 P0-C 角色断言时，使用 `docker/test/docker-compose.yml` 并加上 `-f docker/test/docker-compose.p0-test.yml`（不要和正在使用的真实看板或部署编排叠在一起）。


### `ptc resources`

列出全网内容种子、查看某个种子、下载内容，或把副本拉取并保存在本机仓库。与 WebUI 资源分栏共用 `GET /api/v1/resources`（daemon 会向邻居拉取 `/ptc/resources/1.0.0`，并计算从当前节点到各副本的中转次数：直连为 0，经过一层电路中继为 1）。需要 daemon 已在运行。Prepare 步骤会把依赖种子登记到 packages 或 CAS，因此输入数据会和结果种子一起出现在清单里；workspace 使用独立副本，修改和回收工作区不会污染做种数据。

不带子命令时等同于 `ptc resources` 列表。

```text
ptc resources
ptc resources --json
ptc resources --search pred.bin --kind tree --sort size --order desc
ptc resources --node worker-a
ptc resources show sha256:6c3ba3d27d3128f6e53872e87185dff8c27a6f1d34cca9ba305a9d516c033cd8
ptc resources get sha256:6c3ba3d27d3128f6e53872e87185dff8c27a6f1d34cca9ba305a9d516c033cd8 -o result.tar
ptc resources pull sha256:6c3ba3d27d3128f6e53872e87185dff8c27a6f1d34cca9ba305a9d516c033cd8
ptc resources pull sha256:6c3ba3d27d3128f6e53872e87185dff8c27a6f1d34cca9ba305a9d516c033cd8 --json
```

| 子命令 | 作用 |
| --- | --- |
| （默认） | 列出种子：digest、bitswap 传输 CID、名称、类型、大小、所在节点数、创建时间、到当前设备的最短中转次数 |
| `show DIGEST` | 详细信息：digest、传输 CID、大小、副本、每台设备到当前节点的中转次数与路径（local / direct / relay）；tree 种子按目录为操作单位。超过 100 个文件时默认只打印文件数，需要具体路径时加 `--list-files`；不为每个小文件单独计算 digest |
| `get DIGEST -o FILE` | 从最近的副本下载。tree 种子保存为 tar；省略 `-o` 或 `-o -` 时写到 stdout |
| `pull DIGEST` | 让本机 daemon 用 bitswap 向持有者拉取该种子，校验 DirDigest（tree）或 sha256（blob）后，作为临时副本写入 `packages/replica-*` 或 CAS，随后本机也可对外提供。已在本机则直接报告路径。可用 `ptc clean resource` 删除副本。`--json` 可用。 |

列表筛选与 WebUI 资源分栏语义相同：

| 参数 | 作用 |
| --- | --- |
| `--search` | 在 digest、CID、名称、类型、文件路径和所在设备上做不区分大小写的子串匹配 |
| `--kind` | `all`、`tree` 或 `blob` |
| `--node` | 只列出该节点（名称或 peer id）上存有副本的种子 |
| `--sort` | `name`（默认）、`size`、`kind`、`hops`、`replicas`、`created`、`digest` |
| `--order` | `asc` 或 `desc`（`size` / `replicas` 默认降序） |
| `--list-files` | 仅 `show`：列出目录树里的每一条路径。默认在文件数超过 100 时只显示数量，与看板预览规则一致 |

`--json` 适用于列表、`show` 和 `pull`。hop 次数是相对于当前这个 daemon 的，不是相对于查看 CLI 的那台电脑（若 CLI 连的是网关，就以网关为起点）。CLI / HTTP 仍是内容接口，节点之间优先使用标准 Boxo Bitswap 传输 256KiB 分块：`/ptc/resource-blocks/1.0.0` 仅发现传输 manifest 的 CID，payload 使用标准 Bitswap。旧节点不支持该发现协议时才回退原 tar/blob 协议，校验错误不会静默回退。按跳数依次尝试副本，接收完整并校验 sha256 / DirDigest 后再交付内容。

清单和看板上的 CID 是 Bitswap 传输 manifest 的 raw SHA-256 CID，不是 UnixFS CID；持有者在首次导出或后台索引后保存并通告它。`testdata/bitswap-dispatch` 提供只按 digest 引用约 2 GiB SDK 的派发样例。

pull 的块数、字节数和 MiB/s 是逻辑取块统计，包含索引、缓存命中与重复引用，不代表实际网络流量或线速；provider 表示请求时选定的资源持有者，不是 Bitswap 每个实际供块 peer 的精确归因。树资源的 CID 保存为资源目录外的 `.cid` sidecar，避免修改传输内容自身。

输入仍是 PTC sha256 摘要，不支持直接使用 CID / dataset / UnixFS 输入。单资源超过 10,000 个 256KiB 分块时自动切换为 v2 多层 Merkle 索引，每页最多 256 个引用；无需手动分卷，也不把整个资源或全部块引用放进内存。小资源仍生成 v1 manifest，新节点兼容读取 v1。旧 Bitswap 节点不支持 v2 大资源，提供者与接收者均须升级；格式或校验错误不会回退旧协议。资源大小使用 int64，实际仍受可用磁盘空间、单块 20 秒和总计 30 分钟超时限制。每条中继连接在任一方向累计 64 GiB 或持续 30 分钟后会被重置；超大或慢速传输不保证在重置后无缝继续。以上不代表已经实测 TB 级传输。目录的 tar 开销计入传输大小。

缓存位于种子盘 `blocks/`，下载与校验暂存位于 `.fetch/`，不构建工作树。自动清种后按所有保留资源的 v1/v2 索引标记并回收未引用块，共享块保留；索引缺失或损坏时中止该轮块回收并记录原因。单独执行 `ptc clean resource` 不会立即回收块缓存；种子磁盘统计包含缓存和暂存。关闭 files 或更新名单后，资源发现与标准 Bitswap 的请求 / 发包都会重新检查策略。

### `ptc tasks`

列出或查看网格内公开的运行记录。与 WebUI 任务列表共用 `GET /api/v1/runs`。`ptc tasks show` 会打印由 Prepare YAML 生成的工作树（目录、种子、输出和入口），任务详情页也按同一份记录渲染。

```text
ptc tasks
ptc tasks --json
ptc tasks --search cuda --status completed --sort started --order desc
ptc tasks --node worker-a
ptc tasks show run-20261110T155326Z-test --node worker-a
```

新运行 ID 带随机后缀，同秒在不同节点上运行相同任务也不会互相覆盖。旧记录仍可查看；若旧 ID 在多个节点重复，使用 `tasks show <id> --node <节点名或 peer id>` 指定节点，WebUI 也会分别显示各节点的记录。

列表筛选与 WebUI 任务分栏语义相同：

| 参数 | 作用 |
| --- | --- |
| `--search` | 在 id、中英文名称、状态、事件、触发者、目标、节点、peer id、摘要上做不区分大小写的子串匹配 |
| `--status` | `all`、`in_progress`（含 queued）、`completed`（success / cancelled / skipped）、`failed`，或精确状态 |
| `--node` | 按节点名或 peer id 过滤 |
| `--sort` | `started`（默认）、`duration`、`name`、`status`、`node`、`id` |
| `--order` | `asc` 或 `desc`（`started` / `duration` 默认降序） |

### `ptc policy`

查看或修改**本机**白名单、黑名单和功能开关。需要 daemon 已在运行。修改会立即生效，并写回 daemon 正在使用的配置文件。

不带子命令时等同于 `ptc policy show`。

#### `ptc policy show`

打印当前策略：

```text
allow: (empty: accept all peers in trusted CIDRs)
deny:  (empty: accept all peers in trusted CIDRs)
exec:  true
files: true
relay: false
update (accept mesh updates): true
```

名单非空时，条目以逗号加空格拼接。空名单表示：在受信任网段内，不另做成员限制（黑名单仍优先）。

#### `ptc policy allow add|rm <条目>`

#### `ptc policy deny add|rm <条目>`

条目可以是 **IP**、**CIDR**、**节点名**或 **peer ID**。添加时若列表里已有完全相同的字符串则忽略；删除按字符串精确匹配。匹配对端时，节点名和 peer ID 不区分大小写；IP / CIDR 按地址判断。

黑名单优先于白名单。白名单非空时，未命中白名单的对端会被拒绝（原因如 `peer is not on this host's allow list`）。

每次成功修改后，stdout 打印更新后的策略（格式与 `show` 相同）。

#### `ptc policy service <exec|files|relay> <on|off>`

打开或关闭本机某项服务。服务名只能是 `exec`、`files`、`relay`。开关除 `on` / `off` 外，也接受 `true`、`1`、`enable`、`enabled` 和 `false`、`0`、`disable`、`disabled`（不区分大小写）。

目录里仍能看到该节点，只是对应服务会标为不可用，并给出原因（例如 `service exec is disabled on this host`）。把 `exec` 打开并不等于一定对外提供执行：本机还需要有 NVIDIA GPU。

#### `ptc policy update <on|off>`

允许或拒绝受信对端把新版本推到本机（`update.accept_remote`，默认允许）。写入配置文件的 `update.accept_remote` 字段。

## 配置文件

YAML。复制 `config.example.yaml` 后填写至少一条 `trusted_cidrs`。不要在配置里写邻居 IP。

| 字段 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `name` | 字符串 | 主机名 | 目录中的显示名。 |
| `listen` | `host:port` | `0.0.0.0:18765` | 组网监听地址。端口建议在整个集群保持一致。 |
| `api` | `host:port` | `127.0.0.1:18780` | 本机控制 API。应绑 loopback；非 loopback 来源会被拒绝。 |
| `data_dir` | 路径 | 见「数据目录」 | 身份密钥和 lock 文件位置；也是各存储路径的默认根。 |
| `storage.seeds_dir` | 路径 | `data_dir` | `cas/` 与 `packages/` 的父目录。 |
| `storage.worktrees_dir` | 路径 | `data_dir/worktrees` | 工作树根目录。 |
| `storage.runs_dir` | 路径 | `data_dir/runs` | 运行记录与队列。 |
| `storage.size_limit` | bytes 或单位字符串 | `40GB` | 输入、输出、CAS、块缓存及下载暂存共用的做种上限，按磁盘 free 动态缩减。 |
| `storage.delete_size` | bytes 或单位字符串 | `20GB` | 每轮自动清理目标；省略时为配置上限的一半。 |
| `storage.workspace_limit` | bytes 或单位字符串 | `0` | 所有 workspace 合计上限；0 不限制。 |
| `exec.concurrency` | 整数 | `1` | 本节点同时执行的作业数；其余排队。 |
| `policy.remote_clean` | 布尔 | `false` | 为预留开关；当前 `ptc clean` 只清理本机。 |
| `trusted_cidrs` | CIDR 列表 | **必填，至少一条** | 信任边界。网段外的连接一律拒绝。本机回环互连需要显式加上如 `127.0.0.0/8`。 |
| `psk` | 64 个十六进制字符 | 空（不启用） | 可选，32 字节密钥，用于挡住网段外噪声。可带 `0x` 前缀。集群内必须相同。 |
| `services.exec` | 布尔 | `true` | 是否打算提供执行。无 NVIDIA GPU 时目录仍会标为不可用。 |
| `services.files` | 布尔 | `true` | 是否提供文件服务（种子与结果包的发布/下载）。 |
| `services.relay` | 布尔 | `false` | 是否作为网关：扫描受信任网段并推送目录。无 GPU 的白名单主机应开这个、并关闭 `exec`。 |
| `policy.allow` | 字符串列表 | `[]` | 本机白名单。 |
| `policy.deny` | 字符串列表 | `[]` | 本机黑名单，优先于白名单。 |
| `update.accept_remote` | 布尔 | `true` | 是否接受受信任节点发起的同主版本网格升级。可用 `ptc policy update` 修改。 |
| `scan_interval` | 非负时长 | `0s` | 0 表示无限周期，仅启动扫描一次；正数为每次扫描完成后到下一次扫描的间隔。 |
| `scan_min_interval` | 正时长 | `60s` | 全部本地/远端手动扫描请求共用的冷却间隔；省略或 0 取默认，负数非法。 |
| `disable_mdns` | 布尔 | `false` | 为 true 时不做 mDNS 发现。Docker 跨网段测试会用到。 |
| `scan_hosts` | 字符串列表 | `[]` | 网关在 CIDR 扫描之外额外探测的目标（主机名、IP，或 `host:port`）。解析到的地址也会被连接门禁放行。Docker 演示里网关用 `host.docker.internal` 找到 Windows 本机节点，而不去扫描整个 Docker Desktop 虚拟网段。 |

网关扫描会跳过大于 `/16` 的前缀（可用地址超过 65534 的网段不会被展开）。跨网段拓扑应把受信任网段写成较窄的 CIDR，通常是 `/16` 或更小。

`ptc policy` 的修改会把当前策略写回这份配置文件（权限 `0600`）。不要把 PSK 或其它密钥贴进 issue / 日志。

## 本机控制 API

CLI 通过 HTTP 调用 daemon 的 loopback API（默认 `http://127.0.0.1:18780`）。只接受来自 loopback 的请求，默认超时 5s。拉取资源与网格升级的客户端超时更长（`pull` 最多 6 小时，`--mesh` 最多 3 小时）。这不是对外的网格协议。

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| `GET` | `/api/v1/health` | `{"status":"ok"}` |
| `GET` | `/api/v1/version` | `{"version":"0.3.0-dev"}`（Release 返回实际标签版本） |
| `GET` | `/api/v1/status` | 与 `ptc peers --json` 相同的状态对象 |
| `GET` | `/api/v1/peers` | `{"peers":[...]}`，不含本机 `name` / `version` 包装 |
| `GET` | `/api/v1/policy` | 当前策略 |
| `POST` | `/api/v1/policy` | 修改策略，JSON 见下；成功返回更新后的策略 |
| `GET` | `/api/v1/runs` | 本机及邻居公开的运行记录。可选查询参数：`q`/`search`、`status`、`node`、`sort`、`order`（与 `ptc tasks` 相同） |
| `GET` | `/api/v1/resources` | 全网种子清单（含所在设备与 hop 数）。可选查询参数：`q`/`search`、`kind`、`node`、`sort`、`order`（与 `ptc resources` 相同） |
| `GET` | `/api/v1/resources/sha256/{hex}` | 单个种子的大小、副本与 hop 信息 |
| `GET` | `/api/v1/resources/sha256/{hex}/content` | 下载该种子（tree 为 tar） |
| `POST` | `/api/v1/resources/sha256/{hex}/pull` | 用 bitswap 把该种子拉进本机仓库（超时按大文件拉取放宽） |
| `POST` | `/api/v1/exec?target=<name>&follow=1` | 本机 daemon 把请求体中的 bundle 下发到目标；follow 时返回 NDJSON 事件 |
| `GET` | `/api/v1/queue` | 网格内排队中 / 正在运行的作业 |
| `POST` | `/api/v1/cancel` | `{"id":"<run-id 或 job-id>"}`，取消排队或运行中的作业 |
| `POST` | `/api/v1/scan` | `{}` 触发本机，`{"node":"<relay 名称或 peer-id>"}` 请求已知 relay；202 已接受、409 忙、429 限频（含 `Retry-After`）、403 拒绝、404 节点不存在。 |
| `POST` | `/api/v1/storage/ensure` | 本地 runner 的容量准入：`incoming` bytes、`seeds_dir`、`worktrees_dir`、可选 `reservation_id`；根目录不匹配、容量不足或不能安全清理返回 409。只保证当次检查，调用方还须持有磁盘 reservation。 |
| `POST` | `/api/v1/update/mesh` | 滚动升级请求（`version`、`nodes`、`batch`、`assets_dir`、`dry_run`） |
| `POST` | `/api/v1/update/restart` | 请求本机 daemon 用已替换的二进制重新 exec |

`POST /api/v1/policy` 请求体：

```json
{"op":"allow_add","spec":"10.87.0.10"}
{"op":"allow_rm","spec":"10.87.0.10"}
{"op":"deny_add","spec":"bad-node"}
{"op":"deny_rm","spec":"bad-node"}
{"op":"service","service":"exec","on":false}
{"op":"update","on":false}
```

`op` 只能是 `allow_add`、`allow_rm`、`deny_add`、`deny_rm`、`service`、`update`。未知 `op`、空条目或非法服务名返回 HTTP 400。

### `ptc submit`

执行一份 Actions 风格的工作流：普通任务的第一个步骤必须是 Prepare，最后一个必须是 Package result，中间是 `run` 步骤。另一种合法任务是仅包含一个 `ptc/seed@v0` action 的纯做种工作流，见下方。

不加 `--target` 时在**本机**执行。加上 `--target <节点名|peer id>`（或 `auto`）时，本机 daemon 经 libp2p 协议 `/ptc/exec/1.0.0` 把作业下发到目标 exec 节点，目标走同一套 Prepare → steps → Package result，并把运行记录写入共享的 runs 协议，结果包按摘要发布，提交者可用 `ptc resources get` 取回。WebUI 不下发作业。

不加 `--target` 的本机执行发生在 CLI 进程里，不进入 daemon 队列，因此不能 `ptc cancel`，也不受 `exec.concurrency` 限制。需要排队 / 取消语义时，用 `--target <本机节点名>` 提交给自己：daemon 不经网络，直接把作业放入本机 exec 队列（仍要求本机开启 exec，纯做种任务则要求 files；白名单 / 黑名单只约束其他节点）。

内容寻址的种子（已在网格上发布）不打进 bundle；目标从本地 CAS/packages 或最短跳数的持有者拉取，并校验摘要（目录单元用 DirDigest）。只在提交者本地路径上的种子会先在本机发布，再让目标来拉。小的本地输入默认打进 bundle（`--inline-max`，默认 8MiB）；更大的走发布+拉取。

`prepare.yaml` 的 `target.require_nvidia`（默认 true）和 `target.deny_non_exec`（默认 true）在提交者（目录预检）和目标上都会执行。无 NVIDIA、exec 关闭、或网关必须拒绝，并给出明确错误。exec 节点默认并发 1，后来的作业进入持久 FIFO 队列，而不是直接报忙。

默认 follow：直到作业结束都打印状态和日志。`--detach` 在入队后返回。提交者断开 follow **不会**取消作业；要用 `ptc cancel <run-id>`。本机或远程提交使用 `--json --fetch-result` 时，stdout 只输出一个 JSON 对象，下载提示写到 stderr；下载失败会填入 JSON 的 `error` 并返回非零退出状态。

```text
ptc submit --workflow testdata/submit/workflow.yaml --dry-run
ptc submit --workflow testdata/submit/workflow.yaml --data-dir /path/to/ptc-data
ptc submit --workflow testdata/submit/workflow.yaml --job test --force
ptc submit --workflow testdata/cuda-e2e/workflow.windows.yaml --target host --fetch-result D:\ptc\results
ptc submit --workflow testdata/submit/workflow.yaml --target auto --json
ptc submit --workflow testdata/cuda-vectoradd/workflow.yaml --target cuda
```

| 参数 | 默认值 | 作用 |
| --- | --- | --- |
| `--workflow` | （必填） | 工作流 YAML 路径。Prepare / Package result 的 `with.file` 相对该文件所在目录解析。 |
| `--job` | 空 | 工作流里有多个 job 时必须指定名称。只有一个 job 时可省略。 |
| `--target` | 空（本机执行） | 节点名、peer id，或 `auto`（选一台合格的 exec 节点）。不要把 CUDA 作业指向网关。 |
| `--detach` | false | 入队后返回，不跟随日志。 |
| `--fetch-result <dir>` | 空 | follow 成功后把结果包解压到该目录。本机提交也支持，但需要本机 daemon；没有 daemon 时在执行前报错。 |
| `--inline-max` | 8388608 | 打进 bundle 的本地种子大小上限（字节）；`-1` 表示全部改为发布后按摘要拉取。 |
| `--dry-run` | false | 只解析和校验，不执行、不下发。 |
| `--force` | false | 仅本机执行：跳过 NVIDIA / exec 检查。 |
| `--keep-worktree` | false | 执行任务不再支持；传入会报错，所有终态 workspace 都立即回收。 |
| `--json` | false | 打印 JSON，供 agent 使用。 |

运行记录（含工作树快照与提交者）写入**执行节点**的 `runs/`，结果作为普通种子发布到其 `packages/`。所有终态工作树立即删除；种子受统一容量和安全副本清理规则管理。超过 100 个文件的目录树种子按整个目录计算一个 digest。

#### 纯做种（不创建 workspace、不执行）

`ptc/seed@v0` 的 `with.file` 指向 `apiVersion: ptc/v0`、`kind: Seed` 的资源集合 YAML。该 action 必须是唯一 step；不允许 run / if，也不需要 Prepare / Package result。按 `seeds` 中的 `digest` 或 `local.src` 获取并校验资源后，放入 CAS/packages，作为本机副本继续提供；不创建工作树、不检查 GPU、不运行命令、不产生结果包，任务记录的 `worktree` 为空。

```text
ptc submit --workflow testdata/seed/workflow.yaml --dry-run --json
ptc submit --workflow testdata/seed/workflow.yaml --json
ptc submit --workflow docs/templates/seed-workflow.example.yaml --target replica-node --json
```

完整 YAML、集合字段及 pin 限制见 [纯做种任务](PREPARE.md#纯做种任务)。模板中的摘要必须替换为 `ptc resources --json` 查到的值。远端需要 `files` 服务及对应策略允许，不要求 exec / NVIDIA；`--target auto` 此时挑选 files 节点。省略 target 时缺失资源由本机 daemon 拉取；仅使用本地资源则无需 daemon。队列 / follow / cancel 语义不变，但 `--fetch-result` 没有结果可取，`--force` / `--keep-worktree` 不改变纯做种行为。向不同节点分别提交可以增加副本；任务完成后副本保留，但不会后台持续检查副本数。

### `ptc queue`

列出各节点处于 `queued`、`running` 或 `cancelling` 的作业。已结束的作业看 `ptc tasks` 或看板。

```text
ptc queue
ptc queue --node host --json
```

| 参数 | 默认值 | 作用 |
| --- | --- | --- |
| `--node` | 空 | 只显示该节点名或 peer id 上的作业。 |
| `--json` | false | 打印 JSON。 |

### `ptc cancel`

取消一个排队中或正在运行的作业（仅 CLI）。排队任务立即返回 `cancelled`；运行中任务先返回 `cancelling`，停止进程树并完成记录后变为 `cancelled`，在此期间仍占用并发槽。已结束的作业会报错，超时记为失败而不是用户取消。Follow 断开不会自动取消。

```text
ptc cancel run-20260101T000000Z-test
ptc cancel run-20260101T000000Z-test --json
```

### `ptc clean`

只作用于**本机**存储路径。没有 `--node` 远程删除（除非将来目标打开 `policy.remote_clean`）。

子命令：`outputs`（结果包）、`runs`（已结束的运行记录）、`resource`（按 digest 删种子或结果）、`worktrees`（残留工作树，绝不删除 CAS/packages）。

运行中的工作树由跨进程锁保护：预览会标记为不可删除，实际删除前也会重新检查；`--force` 不能绕过此保护，也不能绕过持久副本承诺或活跃输入保护。实际删除资源前会重查计划是否过期。工作树根目录的 `.ptc-locks` 是持久锁目录，不要手动删除。旧版本的运行任务不持有这些锁，升级前须等待旧任务结束。自定义 `layout.root` 必须是配置工作树根目录的严格子目录，不能指向根目录、锁目录或外部路径。

| 参数 | 作用 |
| --- | --- |
| `<id…>` / `--match RE` | 按 run id、名称、job 或 digest 筛选。 |
| `--dry-run` | 列出将删除的路径和字节，不删。 |
| `--yes` | 实际删除时必须带上（方便非交互 agent）。 |
| `--force` | 允许删除已 pin 的对象。被排队/运行中作业引用的种子仍会拒绝。 |
| `--json` | 打印 JSON。 |

```text
ptc clean worktrees --dry-run
ptc clean outputs --match 'fake-cuda' --yes
ptc clean resource sha256:abc… --yes
ptc clean runs --match 'sample-local' --yes
```

## 尚未实现的命令

以下属于后续阶段，当前二进制没有这些子命令：

- `ptc dataset`
- `ptc gc`
- `ptc status`
- `ptc logs`
- `ptc artifacts`

设计草案：[peer-task-control 设计草案](https://cursor.com/canvas/store-bfdc01c7-fd7a-4053-a2a7-a49302fbb19c/03f3f67c-b25a-4548-a67d-bef09a48a413)。离线摘要见 [DESIGN.md](DESIGN.md)。
