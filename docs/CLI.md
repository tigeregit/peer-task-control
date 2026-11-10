# ptc CLI 参考

对应版本：**0.1.0-p0**（P0 组网）。与二进制不一致时，以 `ptc version` 和 `ptc --help` 为准。

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
| `--data-dir` | 配置里的 `data_dir`；若配置还不存在，则用默认数据目录 | 覆盖数据目录（身份密钥、`daemon.lock`）。 |
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

该目录会存放：

| 文件 | 说明 |
| --- | --- |
| `config.yaml` | 若使用默认 `--config` 路径，daemon 从这里读配置。 |
| `identity.key` | 节点 libp2p 身份密钥，首次启动生成。 |
| `daemon.lock` | daemon 启动时写入，正常退出时删除。含 `pid`、`api`、`peer_id`、`name`。 |

## 退出码

- `0`：成功（`ptc daemon` 在收到 `SIGINT` / `SIGTERM` 后正常退出也是 `0`）。
- `1`：命令失败。错误写到 stderr。

## 命令

### `ptc version`

打印二进制版本（当前为 `0.1.0-p0`），后接换行。不需要配置或正在运行的 daemon。

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
ptc peers --data-dir D:\ptc-data
ptc peers --api 127.0.0.1:18780
```

| 参数 | 默认 | 作用 |
| --- | --- | --- |
| `--json` | 关闭 | 向 stdout 打印缩进 JSON，而不是表格。 |

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
  "version": "0.1.0-p0",
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
| `data_dir` | 路径 | 见「数据目录」 | 身份密钥和 lock 文件位置。 |
| `trusted_cidrs` | CIDR 列表 | **必填，至少一条** | 信任边界。网段外的连接一律拒绝。本机回环互连需要显式加上如 `127.0.0.0/8`。 |
| `psk` | 64 个十六进制字符 | 空（不启用） | 可选，32 字节密钥，用于挡住网段外噪声。可带 `0x` 前缀。集群内必须相同。 |
| `services.exec` | 布尔 | `true` | 是否打算提供执行。无 NVIDIA GPU 时目录仍会标为不可用。 |
| `services.files` | 布尔 | `true` | 是否提供文件服务（数据面在后续阶段落地）。 |
| `services.relay` | 布尔 | `false` | 是否作为网关：扫描受信任网段并推送目录。无 GPU 的白名单主机应开这个、并关闭 `exec`。 |
| `policy.allow` | 字符串列表 | `[]` | 本机白名单。 |
| `policy.deny` | 字符串列表 | `[]` | 本机黑名单，优先于白名单。 |
| `scan_interval` | 时长 | `30s` | 网关扫描受信任网段的间隔。小于等于 0 时回落到 30s。 |
| `disable_mdns` | 布尔 | `false` | 为 true 时不做 mDNS 发现。Docker 跨网段测试会用到。 |

网关扫描会跳过大于 `/16` 的前缀（可用地址超过 65534 的网段不会被展开）。跨网段拓扑应把受信任网段写成较窄的 CIDR，通常是 `/16` 或更小。

`ptc policy` 的修改会把当前策略写回这份配置文件（权限 `0600`）。不要把 PSK 或其它密钥贴进 issue / 日志。

## 本机控制 API

CLI 通过 HTTP 调用 daemon 的 loopback API（默认 `http://127.0.0.1:18780`）。只接受来自 loopback 的请求，超时 5s。这不是对外的网格协议。

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| `GET` | `/api/v1/health` | `{"status":"ok"}` |
| `GET` | `/api/v1/version` | `{"version":"0.1.0-p0"}` |
| `GET` | `/api/v1/status` | 与 `ptc peers --json` 相同的状态对象 |
| `GET` | `/api/v1/peers` | `{"peers":[...]}`，不含本机 `name` / `version` 包装 |
| `GET` | `/api/v1/policy` | 当前策略 |
| `POST` | `/api/v1/policy` | 修改策略，JSON 见下；成功返回更新后的策略 |

`POST /api/v1/policy` 请求体：

```json
{"op":"allow_add","spec":"10.87.0.10"}
{"op":"allow_rm","spec":"10.87.0.10"}
{"op":"deny_add","spec":"bad-node"}
{"op":"deny_rm","spec":"bad-node"}
{"op":"service","service":"exec","on":false}
```

`op` 只能是 `allow_add`、`allow_rm`、`deny_add`、`deny_rm`、`service`。未知 `op`、空条目或非法服务名返回 HTTP 400。

## 尚未实现的命令

以下属于后续阶段，当前二进制没有这些子命令。若用户需要，说明现在是 P0，并指向设计文档：

- `ptc dataset`
- `ptc gc`
- `ptc submit`
- `ptc status`
- `ptc logs`
- `ptc artifacts`
- `ptc install --ssh`
- `ptc update`

设计草案：[peer-task-control 设计草案](https://cursor.com/canvas/store-bfdc01c7-fd7a-4053-a2a7-a49302fbb19c/03f3f67c-b25a-4548-a67d-bef09a48a413)。离线摘要见 [DESIGN.md](DESIGN.md)。
