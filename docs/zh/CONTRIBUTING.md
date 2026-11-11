# 贡献与排障规则

[English](../CONTRIBUTING.md) | [中文](CONTRIBUTING.md)

出错时先读 [DESIGN.md](DESIGN.md)、[ROADMAP.md](ROADMAP.md) 和这份规则，再提 issue 或开 PR。不要绕开仓库在节点上直接打未经跟踪的补丁。

文档是中英双语。英文是 GitHub 默认路径（`README.md`、`docs/*.md`、`docker/README.md`）。中文在 `README.zh-CN.md`、`docs/zh/` 和 `docker/README.zh-CN.md`。改文档时请同时更新两种语言，避免分叉。

## Issue

1. 标题用一句话说清现象，不要只写「坏了」。
2. 正文包含：`ptc version` 输出、操作系统、是否跨网段、相关配置（**不要**贴 PSK 或 SSH 凭证）、复现步骤、期望与实际、日志摘要。
3. 组网问题请附 `ptc peers` 的打码输出（可保留节点名和角色，不要贴内网拓扑以外的敏感信息）。

仓库提供了 GitHub issue 模板（`.github/ISSUE_TEMPLATE/`，英文）。请优先使用；正文可以用中文写，但必填项都要填。

## Commit

- 一行主题，必要时空一行再写正文。
- 主题格式：`type(scope): 摘要`，例如 `feat(net): 网关扫描后推送目录`。
- type 常用：`feat`、`fix`、`docs`、`test`、`refactor`、`chore`。
- 只包含一件事。不要把格式化整棵树和功能改动混在一次提交里。
- 不要提交身份密钥、PSK、SSH 凭证、本机绝对路径里的隐私数据。

## Pull request

1. 对应一个 issue，或在描述里写清要解决的问题。
2. 通过 `go test ./...`。若改了跨网段发现，再跑 P0-C 测试：

   ```text
   powershell -File docker/test/p0-test.ps1
   # 或：
   docker compose -f docker/test/docker-compose.yml -f docker/test/docker-compose.p0-test.yml up -d --build
   sh docker/test/p0-test.sh
   python3 docker/test/p0-assert.py
   docker compose -f docker/test/docker-compose.yml -f docker/test/docker-compose.p0-test.yml down -v
   ```

3. 描述里写：行为变化、如何验证、是否需要改 skill 或 example 配置。
4. 协议或发现逻辑的变化，要同步 `docs/DESIGN.md` 和 `docs/zh/DESIGN.md`；若与设计 Page 冲突，先更新 Page 再合入。
5. 若改了 agent skill：保持仓库根目录 `SKILL.md` 与 `cmd/ptc/skill/SKILL.md` 一致，主题文件放在 `cmd/ptc/skill/`，并由 `go:embed` 打进二进制。skill 文本只使用英文。`ptc skill print` 输出索引，`ptc skill print <topic>` 输出主题。不要让工作目录中的旧 skill 覆盖二进制内嵌 usage。

Bitswap 的默认测试覆盖索引分页、层级进位、异常索引和标准 Boxo 互通。修改大资源导出路径时，额外运行超过旧上限的 3GiB 流式测试，验证 SHA256 和源离线后的持久缓存重开读取：

```powershell
$env:PTC_TEST_LARGE_BITSWAP = "1"
go test ./internal/bitswap -run TestLargeResourceExportAndFetch -count=1 -timeout 8m -v
Remove-Item Env:\PTC_TEST_LARGE_BITSWAP
```

此测试使用重复数据降低磁盘占用，验证的是逻辑资源大小，并不声称传输了 3GiB 不可去重的网络数据。

## 发版

1. 开发版号在 `internal/version/version.go`；Release 构建通过 `-ldflags -X github.com/tigeregit/peer-task-control/internal/version.Version=<标签去掉 v>` 写入准确版本。仓库 `SKILL.md` 必须与内嵌的 `cmd/ptc/skill/SKILL.md` 一致，`ptc skill print` 追加实际绑定版本；不要让工作目录中的旧 skill 覆盖二进制内嵌 usage。
2. 在 `docs/release-notes/v<版本>.md` 写发布说明，并同步 `docs/zh/release-notes/` 下的中文版本。英文文件名必须与即将推送的标签一致。
3. 合并到 `main` 后打标签并推送：`git tag v0.2.0-p2 && git push origin v0.2.0-p2`。
4. GitHub Actions 工作流 `.github/workflows/release.yml` 会交叉编译二进制、打包 skill、生成 `SHA256SUMS` 并创建 Release，同时尝试把镜像推到 GHCR。标签名含 `-`（如 `-p2`、`-rc`）时自动标为预发布，且不会打 `latest` 镜像标签。
5. 推到 `main` 或打开 PR 时，`.github/workflows/ci.yml` 会运行 `gofmt`、`go vet` 和 `go test ./...`。

不要把 `vendor/`、本机 identity、或编译出的二进制提交进仓库。

`.gitattributes` 将 shell 脚本固定为 LF；Windows checkout 也不要把 `.sh` 转成 CRLF，否则传给 Linux / Docker 后 `sh` 会把回车当作命令内容。
