# peer-task-control (ptc)

去中心局域网任务分发：每台机器跑同一个 daemon，按内容哈希按需拉取测试数据，把 CUDA 程序派到指定机器。跨网段由白名单网关转发。

当前实现到 **P0：组网**。数据面、任务工作树、WebUI、SSH 安装与网格升级按设计分期后续再做。

设计草案：[peer-task-control 设计草案](https://cursor.com/canvas/store-bfdc01c7-fd7a-4053-a2a7-a49302fbb19c/03f3f67c-b25a-4548-a67d-bef09a48a413)。离线摘要见 [docs/DESIGN.md](docs/DESIGN.md)。

## 要求

- Go 1.23+（开发机已验证 1.27.3）
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

## 命令（P0）

完整参数、默认值、输出格式和本机 API 见 [docs/CLI.md](docs/CLI.md)。P0 测试报告见 [docs/TEST-REPORT-P0.md](docs/TEST-REPORT-P0.md)。


| 命令 | 作用 |
| --- | --- |
| `ptc daemon` | 监听固定端口，加入组网 |
| `ptc peers` | 列出已知节点及执行 / 文件 / 中转服务 |
| `ptc policy` | 查看或设置本机白名单、黑名单和功能开关 |
| `ptc skill print` | 输出与当前版本绑定的 skill |
| `ptc version` | 打印版本 |
| `ptc submit` | 在本机执行工作流（Prepare → run → Package result）。网格下发尚未实现 |
| `ptc webui` | 本机只读任务看板（中英双语；任务 / 资源 / 节点分栏；查询与下载。不提供改策略或提交作业） |
| `ptc tasks` | 列出或查看网格内公开的运行记录（与看板共用接口和筛选语义） |
| `ptc resources` | 列出全网种子，查看大小 / 所在设备 / 中转次数，或下载（与看板共用接口；`--list-files` 可列出超大目录树的路径） |

查询类能力与 WebUI 同源（同一套本机 `/api/v1` 和 `internal/query`）。策略变更、本机 `submit`、启动 daemon 等控制操作只在 CLI 里提供，看板保持只读。

## 仓库约定

贡献与排障请看 [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md)（含 PR、commit、issue 规则）。Agent 应使用与二进制同版本的 `ptc skill print` 输出。
