# Fake CUDA end-to-end fixture

[English](README.md) | [中文](README.zh-CN.md)

Generates a ~2 GiB third-party SDK blob and a scene of many 200 KiB files totaling ~2 GiB, then runs a stand-in "CUDA" job through `ptc submit` (Prepare → run → Package result). The generated `sdk/` and `scene/` trees are gitignored.

```text
go run ./testdata/cuda-e2e/generate.go -dir testdata/cuda-e2e
# Windows
go run ./cmd/ptc submit --workflow testdata/cuda-e2e/workflow.windows.yaml --data-dir %TEMP%\ptc-cuda-e2e
# Linux
go run ./cmd/ptc submit --workflow testdata/cuda-e2e/workflow.yaml --data-dir /tmp/ptc-cuda-e2e
```

`prepare.yaml` sets `require_nvidia: true`. The job records SDK/scene sizes, a hardlink probe (Windows), `nvidia-smi`, and writes `out/results/{summary.md,metrics.json,pred.bin}`.
