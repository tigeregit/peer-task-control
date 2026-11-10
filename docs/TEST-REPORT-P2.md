# P2 测试报告

- 版本：`0.2.0-p2`
- 时区：Asia/Shanghai (UTC+8)

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
- 每节点默认并发 1，其余 FIFO 排队；状态 `queued` / `running` / `succeeded` / `failed` / `cancelled` 写入运行记录。
- 工作树与种子分目录；清工作树不删除 CAS/packages。

## 实地网格验证

记录在下方。Live compose 未挂载 mock GPU 或测试夹具。重启容器时网格会短暂闪断。

### （a）fake-CUDA e2e：Docker 节点 → Windows `host`（真实 RTX 5070）

（待跑：从 gateway 或 worker 向 `host` 下发 `testdata/cuda-e2e/workflow.windows.yaml`。）

### （b）`require_nvidia` 拒绝

（待跑：向 gateway、worker-a / worker-b 下发，应被拒绝。）

### （c）`testdata/submit` → `worker-b`（Linux，`require_nvidia: false`）

（待跑。）

### （d）真实 CUDA：官方 `nvidia/cuda:13.5.0-devel-ubuntu22.04` 容器节点 `cuda`

镜像标签已在 Docker Hub 核实（2026-11-05 推送）。叠加文件：`docker/docker-compose.cuda.yml`。

（待跑：GPU 直通、`nvidia-smi`、从其他节点 `ptc submit --target cuda testdata/cuda-vectoradd`。）

### 磁盘数字

（待跑：`ptc peers show host` 与 WebUI 节点页显示非零的真实 total/free。）

## 未覆盖 / 限制

- 没有 bitswap；大种子整包 tar 下载。
- `ptc clean` 只作用于本机。
- 断开 follow 不取消作业。
- 若 Docker Desktop 无法 `--gpus all`，Ubuntu CUDA 实地项会停止并记录阻碍，不会伪造 nvidia-smi。
