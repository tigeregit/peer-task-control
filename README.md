# peer-task-control (ptc)

去中心局域网任务分发：每台机器跑同一个 daemon，按内容哈希按需拉取测试数据，把 CUDA 程序派到指定机器。跨网段由白名单网关转发。

当前实现到 **P0：组网**。数据面、任务工作树、WebUI、SSH 安装与网格升级按设计分期后续再做。

设计草案：[peer-task-control 设计草案](https://cursor.com/canvas/store-bfdc01c7-fd7a-4053-a2a7-a49302fbb19c/03f3f67c-b25a-4548-a67d-bef09a48a413)。离线摘要见 [docs/DESIGN.md](docs/DESIGN.md)。

## 要求

- Go 1.23+（开发机已验证 1.27.3）
- 可选：Docker，用于跨网段验证

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

## 验证

```text
go test ./...
```

跨网段（两段 + 一台网关）可用 Docker：

```text
docker compose -f docker/docker-compose.yml up --build --abort-on-container-exit
```

## 命令（P0）

| 命令 | 作用 |
| --- | --- |
| `ptc daemon` | 监听固定端口，加入组网 |
| `ptc peers` | 列出已知节点及执行 / 文件 / 中转服务 |
| `ptc policy` | 查看或设置本机白名单、黑名单和功能开关 |
| `ptc skill print` | 输出与当前版本绑定的 skill |
| `ptc version` | 打印版本 |

## 仓库约定

贡献与排障请看 [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md)（含 PR、commit、issue 规则）。Agent 应使用与二进制同版本的 `ptc skill print` 输出。
