# 假 CUDA 端到端夹具

[English](README.md) | [中文](README.zh-CN.md)

生成约 2 GiB 的第三方 SDK 数据和由大量 200 KiB 文件组成、合计约 2 GiB 的场景，再通过 `ptc submit` 跑一个替代的「CUDA」作业（Prepare → run → Package result）。生成的 `sdk/` 和 `scene/` 目录已被 gitignore。

```text
go run ./testdata/cuda-e2e/generate.go -dir testdata/cuda-e2e
# Windows
go run ./cmd/ptc submit --workflow testdata/cuda-e2e/workflow.windows.yaml --data-dir %TEMP%\ptc-cuda-e2e
# Linux
go run ./cmd/ptc submit --workflow testdata/cuda-e2e/workflow.yaml --data-dir /tmp/ptc-cuda-e2e
```

`prepare.yaml` 设置了 `require_nvidia: true`。作业会记录 SDK/场景大小、硬链接探测（Windows）、`nvidia-smi`，并写入 `out/results/{summary.md,metrics.json,pred.bin}`。
