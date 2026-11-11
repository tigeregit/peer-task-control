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

## 发版

1. 开发版号在 `internal/version/version.go`；Release 构建通过 `-ldflags -X github.com/tigeregit/peer-task-control/internal/version.Version=<标签去掉 v>` 写入准确版本。`SKILL.md` 和内嵌 `cmd/ptc/skill.txt` 必须一致，`ptc skill print` 追加实际绑定版本；不要让工作目录中的旧 skill 覆盖二进制内嵌 usage。
2. 在 `docs/release-notes/v<版本>.md` 写发布说明，文件名必须与即将推送的标签一致。
3. 合并到 `main` 后打标签并推送：`git tag v0.2.0-p2 && git push origin v0.2.0-p2`。
4. GitHub Actions 工作流 `.github/workflows/release.yml` 会交叉编译二进制、生成 `SHA256SUMS`，并创建 Release。标签名含 `-`（如 `-p2`、`-rc`）时自动标为预发布。

不要把 `vendor/`、本机 identity、或编译出的二进制提交进仓库。

`.gitattributes` 将 shell 脚本固定为 LF；Windows checkout 也不要把 `.sh` 转成 CRLF，否则传给 Linux / Docker 后 `sh` 会把回车当作命令内容。
