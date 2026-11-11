# bitswap 下发检查

[English](README.md) | [中文](README.zh-CN.md)

向可选的 `cuda` Ubuntu 节点下发一个真实 CUDA 作业。Prepare 步骤只按 digest
引用假 CUDA SDK 种子（`third_party_sdk`，约 2 GiB），因此执行节点必须在运行
开始前用 bitswap 从网格持有者拉取，并校验其 DirDigest。

```text
ptc submit --workflow testdata/bitswap-dispatch/workflow.yaml --target cuda --json
```

若网格里还没有这份 SDK 种子，先用 `testdata/cuda-e2e` 生成，并把
`prepare.yaml` 中的 digest 改成实际值。
