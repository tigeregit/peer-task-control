# ptc P0 完整测试报告

- **结论：全部通过。** 版本 `0.1.0-p0`。生成时间：2026-11-10 22:59 Asia/Shanghai (UTC+8)。
- 测试主机：iamsbb2（Windows），Go 1.27.3，Docker Engine 29.8.0（linux/amd64 容器）。
- 模拟网络仍保持运行：`seg_a` `10.87.0.0/24`（worker-a `10.87.0.10`）、`seg_b` `10.87.1.0/24`（worker-b `10.87.1.10`），白名单网关 `10.87.0.2` / `10.87.1.2`（无 NVIDIA GPU，不作为执行目标）。
- 可打开的界面：本机 [http://127.0.0.1:18790](http://127.0.0.1:18790)（P0 组网与测试看板，只读）。**P3 任务历史 WebUI 尚未实现**，当前页面不是那套界面。

## 测试项目

| 编号 | 项目 | 结果 | 说明 |
| --- | --- | --- | --- |
| P0-A | 单元测试（策略、受信任 CIDR、目录、身份询问、扫描） | 通过 | `go test ./...` 退出码 0。通过包：`internal/catalog`、`config`、`gater`、`ident`、`node`、`policy`、`scan`。 |
| P0-B | 同机多进程：`127.0.0.1/2/3` 经网关互见 | 通过 | 含在 `internal/node` 的 `TestGatewayScanPushesCatalog`，本轮实跑 0.571s。 |
| P0-C.1 | 网段隔离：计算节点不能直连另一网段 | 通过 | worker-a 拨 `10.87.1.10:18765` 超时；worker-b 拨 `10.87.0.10:18765` 超时。 |
| P0-C.2 | 网关可达：白名单网关能拨两个网段 | 通过 | gateway 成功连接 `10.87.0.10:18765` 与 `10.87.1.10:18765`。 |
| P0-C.3 | 三台 daemon 本机 API 就绪 | 通过 | worker-a、worker-b、gateway 的 `ptc peers` 均成功。 |
| P0-C.4 | 经网关发现完整邻居目录 | 通过 | 每台都能看到另外两台。 |
| P0-C.5 | 角色：网关无 GPU 不可执行，计算节点可执行 | 通过 | worker-a/b 提供 exec（容器内模拟 NVIDIA GeForce RTX 4090）；gateway 的 exec 为 no，原因 `this node has no NVIDIA GPU`；gateway 提供 relay 与 files。 |
| 看板 | 本机组网与测试看板可打开 | 通过 | `http://127.0.0.1:18790` 返回 200，并读到实时目录。 |

此前一次 Docker 构建曾因 Debian 源 502 失败；本轮已改为预编译 Linux 静态二进制，不再依赖 apt。

## 本轮仍在运行的邻居目录

数据来自看板在 2026-11-10 22:59 Asia/Shanghai (UTC+8) 读取的 gateway `/api/v1/status`。Peer ID 会在容器重建后变化。

| 名称 | 地址 | Exec | Files | Relay | 能力 |
| --- | --- | --- | --- | --- | --- |
| gateway | `10.87.0.2:18765`，`10.87.1.2:18765` | no（this node has no NVIDIA GPU） | yes | yes | linux/amd64，无 NVIDIA |
| worker-a | `10.87.0.10:18765` | yes | yes | no（service relay is disabled on this host） | 模拟 NVIDIA GeForce RTX 4090，24564 MiB，驱动 560.00 |
| worker-b | `10.87.1.10:18765` | yes | yes | no（service relay is disabled on this host） | 模拟 NVIDIA GeForce RTX 4090，24564 MiB，驱动 560.00 |

对应 peer ID：

- gateway：`12D3KooWBQtRnmpKoX5ybrd8EeFwW9yaekVJWfN62ZsrqZAGMggJ`
- worker-a：`12D3KooWRvDDGz7ZgiSXc6cPU5M67ERBpZafTA5jiQ61imDiiqM2`
- worker-b：`12D3KooWASuYFVx7JVLEozjc4cirdMTjCxhGK3SG4hZpAvGMXsqx`

## 范围与未测项

本次覆盖 P0 组网：发现邻居、目录、角色与跨网段隔离。以下尚未实现，因此没有功能测试：

- P1 数据面（数据集入库、去重、GC）
- P2 任务工作树与 `ptc submit`
- P3 任务历史 WebUI
- P4 SSH 安装与 `ptc update`

停止模拟网络（需要时再执行）：

```text
docker compose -f docker/docker-compose.yml down -v
```
