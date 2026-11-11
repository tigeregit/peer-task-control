<!-- 路径说明：本文是历史测试记录。当时使用的 docker/docker-compose.yml 现为部署编排；复现跨网段测试请改用 docker/test/，真实 CUDA 节点仍可用 docker/docker-compose.yml 与 docker/docker-compose.cuda.yml。 -->

# P2 测试报告

- 版本：`0.2.0-p2`
- 时区：Asia/Shanghai (UTC+8)
- 网格：compose 项目 `ptc-p0`（镜像 `ptc:p0`，gateway + webui sidecar `127.0.0.1:18790`、worker-a、worker-b，均无 GPU），Windows 原生 `host` 节点（`docker/run-host-node.ps1`，真实 RTX 5070，exec 开），以及可选叠加节点 `cuda`（官方 `nvidia/cuda:12.9.2-devel-ubuntu22.04`，`--gpus all`）。

## 单元与进程内测试

在开发机（Linux box）上 `go test ./...` 全部通过，包括：

- `internal/dispatch`：bundle 编解码、目录种子不打进包、超限本地种子改为发布、DirDigest 校验失败、placement 拒绝网关/无 GPU 节点、`auto` 选 host、FIFO 队列与取消、崩溃后 running 作业重新入队。
- `internal/node.TestDispatchSubmitFixture`：两台进程内节点经探测组网，把 `testdata/submit` 下发到 `worker-b`，作业成功，结果摘要可从提交者 `OpenResource` 取回，运行记录带执行节点和提交者。
- `internal/node.TestDispatchRequireNVIDIARefused`：`require_nvidia` 作业在目录预检阶段被网关拒绝。
- `internal/clean`：已 pin 结果包默认不删、`--force` 可删且不误删种子；清工作树不碰 CAS/packages；被运行中作业引用的种子拒绝删除。
- `internal/config.TestStoragePathsAndEnv`：`PTC_SEEDS_DIR` / `PTC_WORKTREES_DIR` / `PTC_RUNS_DIR` 与默认路径。
- `internal/disk.TestMeasureRealVolume`：读到真实的 volume total/free（非虚构）。

## 协议与行为说明（已实现，见 CLI.md）

- `/ptc/exec/1.0.0` 经受信任 CIDR 门禁和本机 `policy` / `services.exec` 约束。
- 默认 follow；`--detach` 入队即返回。提交者断开 follow **不会**取消作业，需 `ptc cancel`。
- 每节点默认并发 1，其余 FIFO 排队；状态 `queued` / `running` / `succeeded` / `failed` / `cancelled` 写入运行记录。任务列表与看板沿用既有 `success` 状态名（成功的作业在 `ptc tasks` / WebUI 显示为 `success`）。
- 工作树与种子分目录；清工作树不删除 CAS/packages。

## 实地网格验证

记录在下方。默认 live compose 未挂载 mock GPU 或测试夹具。重建 `ptc:p0` 并重启 gateway/worker 时网格会短暂闪断；叠加启动 `cuda` 时其它容器未被重建。

Skill 文档写法参考了 Anthropic agent skills（frontmatter + 渐进披露）、`gh --json`/`--jq`、kubectl 与 docker CLI（`--detach`、机器可读输出、错误恢复）。

### （a）fake-CUDA e2e：Docker 节点 → Windows `host`（真实 RTX 5070）

在 `host` 上先把 e2e 种子发布到本机 packages（硬链接，避免把约 4 GiB 种子打进 bundle）：

- `third_party_sdk` `sha256:dcefeca789cf079dbd1db45f9c3ecaf73bf9f83fa5bd529b8088e1afe7f7a132`（3 files, 2147483748 B）
- `scene-a` `sha256:137c28921e86b89218b790b4b5577300f991743d98a4d3f048a616f899f753c1`（10485 files, 2147328000 B）
- `app` `sha256:07973b3a493d8f9a8f015f5d61807f4b7e7ab46891d75221723b832b19166b07`

从 `worker-a` 下发 digest-only prepare（不内联大种子），经中继到 `host`：

```text
# on worker-a
ptc submit --workflow /tmp/e2e/workflow.windows.yaml --target host --fetch-result /tmp/e2e-result
```

- 运行记录：`run-20261110T182856Z-test`（开始 2026-11-11 02:28:56 Asia/Shanghai，结束 02:29:12 Asia/Shanghai）
- 执行节点：`host`；提交者：`worker-a`；状态：`success`
- 作业日志含 `nvidia-smi NVIDIA GeForce RTX 5070, 12227 MiB`、`hardlink_probe=cam_00000.bin count=4`、`kernel_scan files=41 … elapsed_ms=101`
- 结果种子：`sha256:76b76c622d209bf5067d74be93cc421838d94f7cd3fb7df40851c1cab69bd266`
- 提交者取回：5 files / 33556313 bytes；工作树已清理
- 种子在 `host` 本机 packages 按 DirDigest 校验后物化，未打进 bundle

### （b）`require_nvidia` 拒绝

从 `worker-a` 用 `testdata/cuda-vectoradd`（`require_nvidia` 默认 true）分别指向无 GPU 节点，预检拒绝（作业未入队）：

- `--target gateway`：`refusing to dispatch a require_nvidia job to gateway: it is a relay/gateway or has no NVIDIA GPU (this node has no NVIDIA GPU)`
- `--target worker-b`：`refusing to dispatch a require_nvidia job to worker-b: this node has no NVIDIA GPU`

同一作业随后被 `cuda` 接受并成功（见（d））。

### （c）`testdata/submit` → `worker-b`（Linux，`require_nvidia: false`）

BusyBox worker 没有 `bash`。首次下发仍写 `run: bash bin/app.sh`，失败：

- `run-20261110T182712Z-test`（2026-11-11 02:27:12 Asia/Shanghai），node=`worker-b`，status=`failure`，日志 `/bin/sh: bash: not found`（exit 127）

将 `testdata/submit/workflow.yaml` 改为 `run: sh bin/app.sh` 后重试成功：

```text
# on worker-a
ptc submit --workflow /tmp/submit/workflow.yaml --target worker-b --fetch-result /tmp/submit-result
```

- `run-20261110T182748Z-test`（2026-11-11 02:27:48 Asia/Shanghai），node=`worker-b`，status=`success`
- 结果种子：`sha256:99bd0ef0b160b32870b81357a533c5b523025a6d5393d54f14b6cc758bcd3026`
- 提交者取回：4 files / 732 bytes，摘要 `ok scene=scene-a fixture`

### （d）真实 CUDA：官方 `nvidia/cuda:12.9.2-devel-ubuntu22.04` 容器节点 `cuda`

镜像标签均在 Docker Hub 核实，未臆造。

1. 先试已发布的 `nvidia/cuda:13.5.0-devel-ubuntu22.04`（2026-11-05 推送，digest `sha256:b44fc069…`）。`docker run --rm --gpus all … nvidia-smi` 失败，退出码 125：`nvidia-container-cli: requirement error: unsatisfied condition: cuda>=13.5, please update your driver to a newer version, or use an earlier cuda container`。主机驱动为 616.56。未伪造 nvidia-smi，改用更早的已发布标签。
2. `nvidia/cuda:12.9.2-devel-ubuntu22.04`（digest `sha256:e0203e3a0d3d02b05f4210867b973b7f48997c71bc99b994f2bd350ee60f77a1`）在同一主机上 `docker run --rm --gpus all … nvidia-smi --query-gpu=name,memory.total,driver_version --format=csv` 成功：`NVIDIA GeForce RTX 5070, 12227 MiB, 616.56`。
3. 叠加启动（未重建已运行的 gateway/worker）：

```text
docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml up -d --build cuda
docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml stop cuda
docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml rm -f cuda
```

容器内 `nvidia-smi` 同样看到 RTX 5070 / 12227 MiB / 616.56。`ptc peers show cuda`：linux/amd64，gpu=`NVIDIA GeForce RTX 5070`，exec yes，files yes，relay no，peer `12D3KooWRdN9zg8mU3pNJ8vARg9Xz14Rq8sCBABFLGHkimaJc17x`。

从 `worker-a` 下发真实 nvcc 作业：

```text
# on worker-a
ptc submit --workflow /tmp/cuda-vectoradd/workflow.yaml --target cuda --fetch-result /tmp/cuda-result
```

- `run-20261110T184028Z-test`（2026-11-11 02:40:28 Asia/Shanghai），node=`cuda`，status=`success`，提交者 `worker-a`
- 源种子（inline）：`sha256:bff04522c1d6eef1f9a76de8040591d4770942241919f935ae22f376b081db41`（10 files, 8894 B）
- 步骤日志：`NVIDIA GeForce RTX 5070, 12227 MiB, 616.56`；`nvcc` 编译；`ok vector_add n=65536 device=NVIDIA GeForce RTX 5070 cc=12.0`
- 结果种子：`sha256:f8eeea96bb4780e49c02ab4e2179389e454a8bc37c3415da2569373555da3c5a`
- 提交者取回：4 files / 914 bytes（`summary.md` / `nvidia-smi.txt` / `run.txt`）；工作树已清理

这是 Ubuntu 容器内真实 CUDA 执行；与（a）的 Windows 原生 fake-CUDA e2e 互补。

### 看板与磁盘数字

`ptc tasks`（host）与网关 `GET /api/v1/runs`（WebUI sidecar 同源接口，看板 `http://127.0.0.1:18790` 返回 HTTP 200）列出同一组运行记录：

| ID | 状态 | 节点 | 名称 |
| --- | --- | --- | --- |
| `run-20261110T184028Z-test` | success | cuda | cuda-vector-add |
| `run-20261110T182856Z-test` | success | host | fake-cuda-scene-a |
| `run-20261110T182748Z-test` | success | worker-b | sample-local-job |
| `run-20261110T182712Z-test` | failure | worker-b | sample-local-job |

实地 `ptc peers show`（非虚构）：

- `host`：`C:\Users\iamsbb2\Documents\code\peer-task-control\docker\testdata\host-data`（seeds,worktrees,runs）452 GiB free / 953 GiB total（501 GiB used）；ptc seeds 4.0 GiB，worktrees 0 B，outputs 32 MiB，records 7.5 KiB。GPU RTX 5070，12227 MiB，driver 616.56。
- `cuda`：`/data` 921 GiB free / 1007 GiB total（86 GiB used，Docker Desktop / 已有 WSL2 后端卷）；ptc seeds 9.7 KiB，worktrees 0 B，outputs 914 B，records 5.3 KiB。同一块 RTX 5070。

## 未覆盖 / 限制

- 没有 bitswap；大种子整包 tar 下载。
- `ptc clean` 只作用于本机（无远程 `--node`）。
- 断开 follow 不取消作业。
- 运行记录事件流使用 `succeeded`，任务列表/看板成功态仍为 `success`（历史查询语义，未强行统一）。
- 已发布的 CUDA 13.5 镜像被驱动 616.56 拒绝；叠加层改用已发布的 12.9.2。若某台机器上 `docker run --gpus all nvidia/cuda:12.9.2-devel-ubuntu22.04 nvidia-smi` 失败，不要启动该叠加，也不要伪造 nvidia-smi。
