# peer-task-control skill

版本：与 `ptc version` 相同（当前 0.1.0-p0）。若本 skill 与机器上的 `ptc version` 不一致，先升级再继续，不要混用命令说明。

你在帮助用户把 CUDA 程序和测试数据派到局域网上的指定机器。当前软件只完成了组网（P0）：可以发现邻居、查看服务、设置本机策略。入库、submit、工作树、WebUI、SSH 安装和远程升级还没有实现；不要虚构这些命令的行为。

## 必须遵守

- 项目配置里只有受信任网段和端口，没有邻居名单。不要向用户要邻居 IP。流程是：先在本机起 `ptc daemon`，再等目录出现，然后用 `ptc peers` 查看。
- 指定网段内的设备都视为可信，加入不做成员验证。网段外不接纳。
- 跨网段依赖 `relay` 网关主动拨号并保持会话，不要假设对端能回连。
- 没有 NVIDIA GPU 的节点（通常是网关）不能作为 CUDA 执行目标。
- 凭证、PSK、SSH 密码不得写入仓库、issue、日志示例或 commit。

## 常用命令

```text
ptc version
ptc skill print
ptc daemon --config config.yaml
ptc peers
ptc policy show
ptc policy allow add <IP|CIDR|节点名|peerID>
ptc policy allow rm <同上>
ptc policy deny add <同上>
ptc policy deny rm <同上>
ptc policy service exec|files|relay on|off
```

`ptc peers` 会列出已知节点，以及执行、文件、中转是否开放。目录里仍能看到关闭了部分服务的节点，只是对应服务会标明关闭原因。

## 排障顺序

1. 确认本机 `ptc version` 与 skill 版本一致。
2. 确认 daemon 在跑，`ptc peers` 能连上本机 API。
3. 确认配置里的 `trusted_cidrs` 覆盖了本机和目标机所在网段，且监听端口是 18765（或配置中的同一端口）。
4. 同网段仍看不见：检查防火墙是否放行该 TCP 端口，等待一轮 mDNS / 探测。
5. 跨网段看不见：确认至少有一台 `relay` 网关能访问两个网段，等待一轮扫描间隔（默认 30 秒），不要去填邻居地址。
6. 对端看得到但请求被拒：用 `ptc policy show` 查看目标机的白名单、黑名单和功能开关。拒绝应带明确原因。
7. 仍然不对：阅读仓库里的 `docs/DESIGN.md`（以及设计 Page）、`docs/CONTRIBUTING.md`，按其中的 issue / PR / commit 规则报告或改代码，不要在节点上私下打补丁。

## 尚未提供（不要调用）

`ptc dataset`、`ptc gc`、`ptc submit`、`ptc status`、`ptc logs`、`ptc artifacts`、`ptc install --ssh`、`ptc update` 属于后续阶段。若用户需要这些，说明当前是 P0，并指向设计文档。
