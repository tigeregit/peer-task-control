# 系统设计（P0 摘要）

完整草案以 Page 为准：[peer-task-control 设计草案](https://cursor.com/canvas/store-bfdc01c7-fd7a-4053-a2a7-a49302fbb19c/03f3f67c-b25a-4548-a67d-bef09a48a413)。

本文件只保留实现时要遵守的要点，避免和 Page 长期分叉。若冲突，以 Page 为准，并改这里。

## 目标

Windows 上开发 CUDA。任务要把程序和大量测试数据送到正确的机器。数据总体是 TB 级，每份至少有一个副本。机器跨网段；只有对其他网段有白名单访问的主机能主动连出去。这些网关可装 daemon、可存数据，但没有 NVIDIA GPU，不能作为 CUDA 目标。

## 组网（P0）

- 传输和身份用 go-libp2p，固定 TCP 端口 **18765**，不加入公网 IPFS。
- 项目配置只有受信任 CIDR 和端口，**不预置邻居名单**。
- 安全假设：指定网段内的设备都可信，加入不做成员验证。网段外即使拿到二进制也不接纳。可选集群 PSK 只挡网段外误连。
- 同网段：mDNS + 对网段固定端口探测。扫描时先向同一端口的 UDP 发一条极短的身份询问（`PTC/1 IDENT`），拿到 libp2p peer ID 后再走加密连接。这不是另套组网协议。
- 跨网段：只有网关能主动拨号并保持会话；网关定期扫描所有受信任网段的固定端口，握手后把完整邻居与服务目录推给新节点。对端不必能回连网关。
- 服务角色：执行（NVIDIA GPU，可跑 CUDA）、文件（数据副本）、中转（访问其他受信任网段并转发）。一台机器可同时提供多项。
- 本机策略：可按 IP、CIDR、节点名、peer ID 设白名单 / 黑名单，或关闭部分功能。默认名单空、功能开。有白名单则只为名单内提供该服务，黑名单优先。目录里仍能看到该节点，但标出关闭的服务；拒绝必须带原因。这是主机操作策略，不是集群认证。
- CUDA 任务派到网关必须拒绝（P2 实现，P0 在目录中把无 GPU 的 exec 标为不可用）。

## 阶段进度

- P0：组网（见上）。已完成。
- P1：标准 Boxo Bitswap 数据面已实现。受信任主机及现有 circuit-v2 中转连接承载标准 Bitswap；`/ptc/resource-blocks/1.0.0` 将 PTC sha256 资源摘要映射到 raw CID 的传输 manifest，再交换 256KiB payload 块。小资源保留 v1 平面 manifest；超过 10,000 块后自动生成 v2 Merkle 索引：根保存 `index` CID、`height`、资源摘要和 int64 总字节数；索引页保存层高、子树字节数及最多 256 个带字节数的 CID 引用。叶页引用数据块，父页引用索引页，所有页和数据仍是 raw SHA-256 CID。构建仅保留各层未满的一页，接收逐页流式遍历，检查层高、扇出、子树大小、块长度及溢出，索引层高最多 8，足以覆盖 int64 可表示的资源大小。

  目录仍为 PTC DirDigest + tar，不是 UnixFS；资源发现不使用公网 DHT。files 服务和名单策略在根发现、块请求及发包时动态执行。下载完整暂存后校验资源摘要；副本失败尝试下一个，只有协议不支持才回退旧整包协议，v2 格式错误不会回退。新节点读取 v1/v2；大资源的提供者与接收者均须升级。持久块缓存位于种子盘 `blocks/`，暂存位于 `.fetch/`，均计入种子磁盘用量。不再以固定总块数限制资源，但仍受磁盘空间、单块 20 秒和总计 30 分钟超时限制；不承诺 TB 级实测。UnixFS / filestore、CID / dataset 输入、分块 GC 和完整 pin 管理尚未实现。
- P2：工作树任务。任务大纲借鉴 Actions 的 job/step，每个 job 必须以 Prepare 开头、以 Package result 收尾（[PREPARE.md](PREPARE.md)、[PACKAGE-RESULT.md](PACKAGE-RESULT.md)，模板在 [templates/](templates/)）。`ptc submit` 可在本机执行，也可用 `--target`（或 `auto`）经 `/ptc/exec/1.0.0` 下发到指定 exec 节点。目标执行与本机同一套 Prepare → steps → Package result 路径。已发布种子按摘要从最短路径持有者拉取并校验（目录单元用 DirDigest）；仅存在于提交者本地路径的种子先在提交者上发布。exec 节点有持久 FIFO 队列，默认并发 1。`require_nvidia`（默认 true）作业派到无 GPU 或 exec 关闭的节点（包括网关）必须拒绝。工作树与种子分目录存储；清工作树不得删除 CAS/packages。结果包默认临时，可 pin。
- P3：WebUI 与版本绑定 skill 的完整界面。当前已有只读任务看板（任务 / 资源 / 节点，节点展示真实磁盘占用）。官方完整产品界面仍待后续。提交、取消、清理只走 CLI。
- P2 纯做种例外：job 唯一的 `ptc/seed@v0` step 指向 `kind: Seed` 集合。仅获取、校验、保留并公开副本，不创建工作树、不运行、不打包；仍有任务记录和持久队列。远端按 files 策略接受，不要求 exec / GPU；普通工作流仍按 exec 策略接受，不能借此绕过执行权限。bundle 必须为每个种子提供唯一计划，不能在目标上解析提交者的本地路径。示例见 [PREPARE.md](PREPARE.md#纯做种任务)。
- P4：已实现 SSH 单机安装/更新及本机更新。下载官方 Release 并验证 SHA256，失败回滚；SSH 保持严格主机密钥校验，支持 Linux amd64/arm64 和 Windows amd64。本机 Linux/macOS 可自更新，Windows 须更新另一个未运行的 exe。服务安装、daemon 自动重启及全网协调升级仍未实现，参见 [CLI.md](CLI.md)。
