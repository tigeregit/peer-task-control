# ptc CLI 参考

对应版本：**0.2.0-p2**。与二进制不一致时，以 `ptc version` 和 `ptc --help` 为准。

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
| `worktrees/` | 每次运行物化出的工作空间。成功后按 Package result 清理；也可用 `ptc clean worktrees`。 |
| `runs/` | 运行记录 JSON 与 `runs/queue/` 中的持久作业队列。 |

## 退出码

- `0`：成功（`ptc daemon` 在收到 `SIGINT` / `SIGTERM` 后正常退出也是 `0`）。
- `1`：错误。包括工作流校验失败、本机/远端执行失败、目标不存在、`require_nvidia` 作业被派到无 GPU / exec 关闭 / 网关而被拒绝、策略拒绝、队列已满、清理缺少 `--yes`、以及与 daemon 通信失败。错误信息写到 stderr（`--json` 时关键字段也在 JSON 里）。
- `1`：命令失败。错误写到 stderr。

## 命令

### `ptc version`

打印二进制版本（当前为 `0.2.0-p2`），后接换行。不需要配置或正在运行的 daemon。

### `ptc skill print`

打印与当前二进制绑定的 agent skill 正文，并在末尾追加一行 `# bound-to-version <版本>`。

查找 `SKILL.md` 的顺序：源码树中的 `SKILL.md`（开发时）、可执行文件同目录、可执行文件上一级目录、当前工作目录。都找不到时，使用编译进二进制的内嵌文本。

不需要正在运行的 daemon。

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
- 同网段用 mDNS 和端口探测发现邻居；跨网段由 `services.relay: true` 的网关按 `scan_interval` 扫描受信任网段并推送目录。
- 配置里**不要**填写邻居名单。
- 启动成功后 stderr 会打印节点名、peer ID 和 API 地址，并提示随后使用 `ptc peers`。
- 用 Ctrl+C 或 SIGTERM 停止。

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
  "version": "0.2.0-p2",
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

看板会持续显示一条实时任务「网格目录监视 / Mesh catalog watch」，并汇总本机 `--tasks` / `--runs` 以及网格里其他节点公开的运行记录（守护进程 `GET /api/v1/runs` 会向邻居拉取 `/ptc/runs/1.0.0`）。任务和资源列表都支持搜索、状态/类型筛选和排序。顶栏还有「资源」分栏：列出全网种子的大小、所在节点数、创建时间，以及到当前设备（看板所连接的 daemon）的最短中转次数，并支持下载。列表可按列头升序或降序排序；digest 去掉 `sha256:` 前缀，默认只显示哈希前 8 位加 `***`，点眼睛按钮可展开。任务摘要里的结果种子可点进对应资源页。点击「节点」里的某一台设备，可查看硬件信息、历史任务和该节点存储的种子；再点具体任务或种子会跳到对应分栏。CLI 与看板共用同一套本机控制接口和查询语义（`internal/query`）。看板是只读界面：任务 / 资源 / 节点的查看、筛选、排序、搜索和种子下载都可以在 CLI 里用同样的参数完成（`ptc tasks`、`ptc resources`、`ptc peers` / `peers show`）。CLI 额外提供控制能力，看板不会代理这些写操作：`ptc daemon`、`ptc policy`（改白名单 / 黑名单 / 服务开关）、`ptc submit`（本机或 `--target` 下发）、`ptc cancel`、`ptc clean`，以及 `ptc resources show --list-files`（展开超过 100 个文件的目录树）。看板会列出邻居执行完的作业（包括 CUDA），但不能提交或取消。

Docker 演示里除了 `gateway` / `worker-a` / `worker-b` 三个容器，还可以在 Windows 本机启动原生 `host` 节点（真实 NVIDIA GPU）：先保证网格已 `up`，再在仓库根目录运行 `powershell -File docker/run-host-node.ps1`。网关通过 `scan_hosts: host.docker.internal` 探测本机 `18765` 端口。停止用 `powershell -File docker/run-host-node.ps1 -Stop`。

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

Docker 跨网段模拟会把该看板映射到主机的 `http://127.0.0.1:18790`。默认 compose 不会挂载录制的测试任务，也不会给容器节点伪装 GPU；节点硬件以 daemon 探测到的真实信息为准。需要跑官方 P0-C 角色断言时，另加 `-f docker/docker-compose.p0-test.yml`（不要和正在使用的看板叠在一起）。


### `ptc resources`

列出全网内容种子、查看某个种子，或把它下载下来。与 WebUI 资源分栏共用 `GET /api/v1/resources`（daemon 会向邻居拉取 `/ptc/resources/1.0.0`，并计算从当前节点到各副本的中转次数：直连为 0，经过一层电路中继为 1）。需要 daemon 已在运行。Prepare 步骤会把依赖种子登记为 `packages/seed-<name>` 树（优先硬链接，不另占一份完整拷贝），因此输入数据会和结果种子一起出现在清单里。

不带子命令时等同于 `ptc resources` 列表。

```text
ptc resources
ptc resources --json
ptc resources --search pred.bin --kind tree --sort size --order desc
ptc resources --node worker-a
ptc resources show sha256:6c3ba3d27d3128f6e53872e87185dff8c27a6f1d34cca9ba305a9d516c033cd8
ptc resources get sha256:6c3ba3d27d3128f6e53872e87185dff8c27a6f1d34cca9ba305a9d516c033cd8 -o result.tar
```

| 子命令 | 作用 |
| --- | --- |
| （默认） | 列出种子：digest、名称、类型、大小、所在节点数、创建时间、到当前设备的最短中转次数 |
| `show DIGEST` | 详细信息：大小、副本、每台设备到当前节点的中转次数与路径（local / direct / relay）；tree 种子按目录为操作单位。超过 100 个文件时默认只打印文件数，需要具体路径时加 `--list-files`；不为每个小文件单独计算 digest |
| `get DIGEST -o FILE` | 从最近的副本下载。tree 种子保存为 tar；省略 `-o` 或 `-o -` 时写到 stdout |

列表筛选与 WebUI 资源分栏语义相同：

| 参数 | 作用 |
| --- | --- |
| `--search` | 在 digest、名称、类型、文件路径和所在设备上做不区分大小写的子串匹配 |
| `--kind` | `all`、`tree` 或 `blob` |
| `--node` | 只列出该节点（名称或 peer id）上存有副本的种子 |
| `--sort` | `name`（默认）、`size`、`kind`、`hops`、`replicas`、`created`、`digest` |
| `--order` | `asc` 或 `desc`（`size` / `replicas` 默认降序） |
| `--list-files` | 仅 `show`：列出目录树里的每一条路径。默认在文件数超过 100 时只显示数量，与看板预览规则一致 |

`--json` 适用于列表和 `show`。hop 次数是相对于当前这个 daemon 的，不是相对于查看 CLI 的那台电脑（若 CLI 连的是网关，就以网关为起点）。下载走内容接口，不是 bitswap；网格按 CID 取种仍未实现。

### `ptc tasks`

列出或查看网格内公开的运行记录。与 WebUI 任务列表共用 `GET /api/v1/runs`。`ptc tasks show` 会打印由 Prepare YAML 生成的工作树（目录、种子、输出和入口），任务详情页也按同一份记录渲染。

```text
ptc tasks
ptc tasks --json
ptc tasks --search cuda --status completed --sort started --order desc
ptc tasks --node worker-a
ptc tasks show run-20261110T155326Z-test
```

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
| `exec.concurrency` | 整数 | `1` | 本节点同时执行的作业数；其余排队。 |
| `policy.remote_clean` | 布尔 | `false` | 为预留开关；当前 `ptc clean` 只清理本机。 |
| `trusted_cidrs` | CIDR 列表 | **必填，至少一条** | 信任边界。网段外的连接一律拒绝。本机回环互连需要显式加上如 `127.0.0.0/8`。 |
| `psk` | 64 个十六进制字符 | 空（不启用） | 可选，32 字节密钥，用于挡住网段外噪声。可带 `0x` 前缀。集群内必须相同。 |
| `services.exec` | 布尔 | `true` | 是否打算提供执行。无 NVIDIA GPU 时目录仍会标为不可用。 |
| `services.files` | 布尔 | `true` | 是否提供文件服务（种子与结果包的发布/下载）。 |
| `services.relay` | 布尔 | `false` | 是否作为网关：扫描受信任网段并推送目录。无 GPU 的白名单主机应开这个、并关闭 `exec`。 |
| `policy.allow` | 字符串列表 | `[]` | 本机白名单。 |
| `policy.deny` | 字符串列表 | `[]` | 本机黑名单，优先于白名单。 |
| `scan_interval` | 时长 | `30s` | 网关扫描受信任网段的间隔。小于等于 0 时回落到 30s。 |
| `disable_mdns` | 布尔 | `false` | 为 true 时不做 mDNS 发现。Docker 跨网段测试会用到。 |
| `scan_hosts` | 字符串列表 | `[]` | 网关在 CIDR 扫描之外额外探测的目标（主机名、IP，或 `host:port`）。解析到的地址也会被连接门禁放行。Docker 演示里网关用 `host.docker.internal` 找到 Windows 本机节点，而不去扫描整个 Docker Desktop 虚拟网段。 |

网关扫描会跳过大于 `/16` 的前缀（可用地址超过 65534 的网段不会被展开）。跨网段拓扑应把受信任网段写成较窄的 CIDR，通常是 `/16` 或更小。

`ptc policy` 的修改会把当前策略写回这份配置文件（权限 `0600`）。不要把 PSK 或其它密钥贴进 issue / 日志。

## 本机控制 API

CLI 通过 HTTP 调用 daemon 的 loopback API（默认 `http://127.0.0.1:18780`）。只接受来自 loopback 的请求，超时 5s。这不是对外的网格协议。

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| `GET` | `/api/v1/health` | `{"status":"ok"}` |
| `GET` | `/api/v1/version` | `{"version":"0.2.0-p2"}` |
| `GET` | `/api/v1/status` | 与 `ptc peers --json` 相同的状态对象 |
| `GET` | `/api/v1/peers` | `{"peers":[...]}`，不含本机 `name` / `version` 包装 |
| `GET` | `/api/v1/policy` | 当前策略 |
| `POST` | `/api/v1/policy` | 修改策略，JSON 见下；成功返回更新后的策略 |
| `GET` | `/api/v1/runs` | 本机及邻居公开的运行记录。可选查询参数：`q`/`search`、`status`、`node`、`sort`、`order`（与 `ptc tasks` 相同） |
| `GET` | `/api/v1/resources` | 全网种子清单（含所在设备与 hop 数）。可选查询参数：`q`/`search`、`kind`、`node`、`sort`、`order`（与 `ptc resources` 相同） |
| `GET` | `/api/v1/resources/sha256/{hex}` | 单个种子的大小、副本与 hop 信息 |
| `GET` | `/api/v1/resources/sha256/{hex}/content` | 下载该种子（tree 为 tar） |
| `POST` | `/api/v1/exec?target=<name>&follow=1` | 本机 daemon 把请求体中的 bundle 下发到目标；follow 时返回 NDJSON 事件 |
| `GET` | `/api/v1/queue` | 网格内排队中 / 正在运行的作业 |
| `POST` | `/api/v1/cancel` | `{"id":"<run-id 或 job-id>"}`，取消排队或运行中的作业 |

`POST /api/v1/policy` 请求体：

```json
{"op":"allow_add","spec":"10.87.0.10"}
{"op":"allow_rm","spec":"10.87.0.10"}
{"op":"deny_add","spec":"bad-node"}
{"op":"deny_rm","spec":"bad-node"}
{"op":"service","service":"exec","on":false}
```

`op` 只能是 `allow_add`、`allow_rm`、`deny_add`、`deny_rm`、`service`。未知 `op`、空条目或非法服务名返回 HTTP 400。

### `ptc submit`

执行一份 Actions 风格的工作流：第一个步骤必须是 Prepare，最后一个必须是 Package result，中间是 `run` 步骤。

不加 `--target` 时在**本机**执行。加上 `--target <节点名|peer id>`（或 `auto`）时，本机 daemon 经 libp2p 协议 `/ptc/exec/1.0.0` 把作业下发到目标 exec 节点，目标走同一套 Prepare → steps → Package result，并把运行记录写入共享的 runs 协议，结果包按摘要发布，提交者可用 `ptc resources get` 取回。WebUI 不下发作业。

内容寻址的种子（已在网格上发布）不打进 bundle；目标从本地 CAS/packages 或最短跳数的持有者拉取，并校验摘要（目录单元用 DirDigest）。只在提交者本地路径上的种子会先在本机发布，再让目标来拉。小的本地输入默认打进 bundle（`--inline-max`，默认 8MiB）；更大的走发布+拉取。

`prepare.yaml` 的 `target.require_nvidia`（默认 true）和 `target.deny_non_exec`（默认 true）在提交者（目录预检）和目标上都会执行。无 NVIDIA、exec 关闭、或网关必须拒绝，并给出明确错误。exec 节点默认并发 1，后来的作业进入持久 FIFO 队列，而不是直接报忙。

默认 follow：直到作业结束都打印状态和日志。`--detach` 在入队后返回。提交者断开 follow **不会**取消作业；要用 `ptc cancel <run-id>`。

```text
ptc submit --workflow testdata/submit/workflow.yaml --dry-run
ptc submit --workflow testdata/submit/workflow.yaml --data-dir /path/to/ptc-data
ptc submit --workflow testdata/submit/workflow.yaml --job test --force --keep-worktree
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
| `--fetch-result <dir>` | 空 | follow 成功后把结果包解压到该目录。 |
| `--inline-max` | 8388608 | 打进 bundle 的本地种子大小上限（字节）；`-1` 表示全部改为发布后按摘要拉取。 |
| `--dry-run` | false | 只解析和校验，不执行、不下发。 |
| `--force` | false | 仅本机执行：跳过 NVIDIA / exec 检查。 |
| `--keep-worktree` | false | 仅本机执行：忽略 Package result 中的清理设置，保留工作树。 |
| `--json` | false | 打印 JSON，供 agent 使用。 |

成功后会在**执行节点**的 `runs/` 写入记录（含工作树快照与提交者），在其 `packages/` 发布结果种子。工作树在成功且未 `--keep-worktree` 时按清理规则删除，种子保留。超过 100 个文件的目录树种子按整个目录计算一个 digest。

### `ptc queue`

列出各节点处于 `queued` 或 `running` 的作业。已结束的作业看 `ptc tasks` 或看板。

```text
ptc queue
ptc queue --node host --json
```

| 参数 | 默认值 | 作用 |
| --- | --- | --- |
| `--node` | 空 | 只显示该节点名或 peer id 上的作业。 |
| `--json` | false | 打印 JSON。 |

### `ptc cancel`

取消一个排队中或正在运行的作业（仅 CLI）。已结束的作业会报错。Follow 断开不会自动取消。

```text
ptc cancel run-20260101T000000Z-test
ptc cancel run-20260101T000000Z-test --json
```

### `ptc clean`

只作用于**本机**存储路径。没有 `--node` 远程删除（除非将来目标打开 `policy.remote_clean`）。

子命令：`outputs`（结果包）、`runs`（已结束的运行记录）、`resource`（按 digest 删种子或结果）、`worktrees`（残留工作树，绝不删除 CAS/packages）。

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
- `ptc install --ssh`
- `ptc update`

设计草案：[peer-task-control 设计草案](https://cursor.com/canvas/store-bfdc01c7-fd7a-4053-a2a7-a49302fbb19c/03f3f67c-b25a-4548-a67d-bef09a48a413)。离线摘要见 [DESIGN.md](DESIGN.md)。
