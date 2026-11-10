# 贡献与排障规则

出错时先读 [DESIGN.md](DESIGN.md) 和这份规则，再提 issue 或开 PR。不要绕开仓库在节点上直接打未经跟踪的补丁。

## Issue

1. 标题用一句话说清现象，不要只写「坏了」。
2. 正文包含：`ptc version` 输出、操作系统、是否跨网段、相关配置（**不要**贴 PSK 或 SSH 凭证）、复现步骤、期望与实际、日志摘要。
3. 组网问题请附 `ptc peers` 的打码输出（可保留节点名和角色，不要贴内网拓扑以外的敏感信息）。

## Commit

- 一行主题，必要时空一行再写正文。
- 主题格式：`type(scope): 摘要`，例如 `feat(net): 网关扫描后推送目录`。
- type 常用：`feat`、`fix`、`docs`、`test`、`refactor`、`chore`。
- 只包含一件事。不要把格式化整棵树和功能改动混在一次提交里。
- 不要提交身份密钥、PSK、SSH 凭证、本机绝对路径里的隐私数据。

## Pull request

1. 对应一个 issue，或在描述里写清要解决的问题。
2. 通过 `go test ./...`。若改了跨网段发现，再跑 `docker compose -f docker/docker-compose.yml up --build --abort-on-container-exit`。
3. 描述里写：行为变化、如何验证、是否需要改 skill 或 example 配置。
4. 协议或发现逻辑的变化，要同步 `docs/DESIGN.md`；若与设计 Page 冲突，先更新 Page 再合入。
