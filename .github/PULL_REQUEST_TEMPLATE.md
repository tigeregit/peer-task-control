感谢提交。请确认：

- [ ] 对应一个 issue，或在下面写清要解决的问题
- [ ] 已运行 `go test ./...`
- [ ] 若改了跨网段发现，已按 [docker/README.md](../docker/README.md) 跑 `docker/test` 下的 P0-C 测试
- [ ] 若改了 skill 文本，仓库根目录 `SKILL.md` 与 `cmd/ptc/skill/` 保持同步，且 `go test ./cmd/ptc -run TestSkill` 通过

## 做了什么

## 如何验证

## 备注

协议或发现逻辑的变化请同步 `docs/DESIGN.md`。不要提交身份密钥、PSK、SSH 凭证或编译出的二进制。
