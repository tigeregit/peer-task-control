# 真实 CUDA vector-add 夹具

[English](README.md) | [中文](README.zh-CN.md)

一份很小的 `vector_add.cu`，在官方 `nvidia/cuda` Ubuntu 容器里用 `nvcc` 编译。这不是 Windows 上的假 CUDA 端到端夹具。

```text
# 加入部署网格（不要用 P0-C 测试编排跑真实 GPU 作业）：
docker compose -f docker/docker-compose.yml -f docker/docker-compose.cuda.yml up -d --build cuda

# 从网格中的另一台节点（网关 sidecar、files 节点，或 Windows 本机）：
ptc submit --workflow testdata/cuda-vectoradd/workflow.yaml --target cuda --fetch-result /tmp/cuda-vector-add
```

`prepare.yaml` 设置了 `require_nvidia: true`。没有 GPU 的网关和只有 files 服务的节点会拒绝该作业。
