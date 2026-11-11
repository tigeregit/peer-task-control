# Docker：部署与测试

[English](README.md) | [中文](README.zh-CN.md)

`docker/` 提供可部署的节点镜像和示例编排。跨网段隔离测试放在 `docker/test/`，不要把测试栈当成默认上线方式。

默认部署编排**不**包含伪装 GPU，也**不**挂载录制的测试任务。网关没有 GPU，也不得作为 CUDA 执行目标。

## 部署一个真实节点或实验室网格

在仓库根目录：

```text
# 国内构建可加上： --build-arg GOPROXY=https://goproxy.cn,direct
docker compose -f docker/docker-compose.yml up -d --build
```

这会启动：

- `gateway`：中转 + 文件，`exec` 关闭，无 GPU。网状端口 `18765/tcp` 和 `18765/udp` 发布到主机；只读看板映射到 `http://127.0.0.1:18790`。
- `files`：只提供文件 / 做种服务。
- `webui`：与网关共用网络命名空间的只读看板。

示例配置在 `docker/config/`。默认只信任组成网络 `10.88.0.0/24`（网关另信任回环，以便探测本机 `host` 节点）。把其它机器加入前，请把真实局域网 CIDR 写进这些配置。

数据在命名卷 `gateway-data` / `files-data` 里。日志与身份密钥都在卷内，不要把它们提交进仓库。

在一台机器上也可以只跑其中一个服务，例如 `docker compose -f docker/docker-compose.yml up -d gateway`。多台机器部署时，通常每台只跑与自己角色相符的一个容器，并把 `trusted_cidrs` 改成真实网段。

本地构建镜像：

```text
docker build -t ptc:local -f docker/Dockerfile .
docker build -t ptc:cuda -f docker/Dockerfile.cuda .
```

发布标签时，GitHub Actions 会尝试把镜像推到 GHCR：

- `ghcr.io/tigeregit/peer-task-control`（linux/amd64、linux/arm64）
- `ghcr.io/tigeregit/peer-task-control-cuda`（linux/amd64，基于 `nvidia/cuda:12.9.2-devel-ubuntu22.04`）

没有现成的已发布镜像时，用上面的 `docker build` / `docker compose build` 即可。

### 可选的 CUDA 节点

需要宿主机已安装 NVIDIA Container Toolkit 或 Docker Desktop GPU 支持，并且下面这条命令能看到真实 GPU：

```text
docker run --rm --gpus all nvidia/cuda:12.9.2-devel-ubuntu22.04 nvidia-smi
```

然后：

```text
docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml up -d --build cuda
docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml stop cuda
docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml rm -f cuda
```

镜像使用官方标签 `nvidia/cuda:12.9.2-devel-ubuntu22.04`。同一系列的 `13.5.0-devel-ubuntu22.04` 曾在驱动 616.56 上被 `nvidia-container-cli` 以 `cuda>=13.5` 拒绝，不要换成未验证的更新标签。不要把 GPU 或这个 overlay 加到网关服务上。

### Windows 本机 GPU 节点

先启动上面的部署网格，再在仓库根目录运行：

```text
powershell -File docker/run-host-node.ps1
powershell -File docker/run-host-node.ps1 -Stop
```

脚本会用 `docker/config/host.yaml` 启动本机 daemon（真实 GPU），数据目录默认是 `docker/data/host`（已忽略，内含 identity，不要提交）。网关通过 `scan_hosts: host.docker.internal` 探测本机 `18765` 端口。

## 跨网段测试（P0-C）

官方隔离测试使用两个互不直通的 `10.87.x` 网段，外加一台可访问两端、无 NVIDIA GPU 的白名单网关。测试镜像会在镜像内编译 `ptc` 和 `tcpprobe`，不必再在宿主机上预编译 `docker/ptc-linux`。

```text
# Windows（已装 Go 与 Docker 时，脚本会直接 compose build）
powershell -File docker/test/p0-test.ps1

# Linux
docker compose -f docker/test/docker-compose.yml -f docker/test/docker-compose.p0-test.yml up -d --build
sh docker/test/p0-test.sh
python3 docker/test/p0-assert.py
docker compose -f docker/test/docker-compose.yml -f docker/test/docker-compose.p0-test.yml down -v
```

`docker/test/docker-compose.p0-test.yml` 会给 worker 挂上伪装的 `nvidia-smi`，只用于角色断言。不要把它和正在使用的真实看板或部署编排叠在一起。

可选：在测试网格上叠加真实 CUDA 节点，或把本机 Windows GPU 节点加进去：

```text
docker compose -f docker/test/docker-compose.yml -f docker/test/docker-compose.cuda.yml up -d --build cuda
powershell -File docker/run-host-node.ps1 -TestMesh
```
