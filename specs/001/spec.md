# specs/001 — go-safemedia MVP：加密视频播放器

- 状态：草稿，待评审（评审门通过后才进入 plan.md）
- 上游：`docs/requirements.md`
- 术语：`CONTEXT.md`
- 日期：2026-10-14

## Overview

CLI 工具 `safemedia`：把 `.mp4` / `.mov` / `.webm` 视频**封存**为 `.svid` 密档（每密档一把随机文件密钥，4 MiB 分块独立 AES-256-GCM 加密，文件密钥经 ECIES 封装到 secp256k1 账户公钥）；播放时用以太坊式 keystore 账户**登录**，解密流经 `127.0.0.1` 本地 HTTP 送到系统浏览器 `<video>`，`--player mpv` 为全格式逃生通道。完全离线，明文不落盘。

## Scope

**MVP 包含**：账户创建/恢复（助记词 + 恢复文件）、封存（容器白名单）、单文件播放、目录索引页、mpv 模式、锁定语义、可搬运性。

**MVP 不含**：DRM/防录屏、多用户授权、mov→mp4 转封装（二期）、GUI、云端。

## User Stories & Acceptance Scenarios

> 以下 Given/When/Then 是测试用例的唯一来源（L1/L2/L3 均从此派生）。

### US1 账户创建

- **AC1.1** Given 首次使用，When 执行 `safemedia account create` 并设置合格登录密码，Then 生成 secp256k1 密钥对，写出 keystore JSON（Web3 Secret Storage v3，scrypt）到 `~/.safemedia/`，显示 12 个 BIP-39 助记词，要求按随机位置复述若干词确认后才算创建完成。
- **AC1.2** Given 弱密码（< 8 位或常见弱口令），When 创建账户，Then 拒绝并要求重新输入。
- **AC1.3** Given `~/.safemedia/` 已存在账户文件，When 再次创建，Then 不覆盖已有文件，提示改用 `--account` 指定其他路径。
- **AC1.4** When 创建账户，Then 输出红线提示：绝不导入持有真实资产的钱包私钥/助记词，只能新建。
- **AC1.5** Given 创建时选择 24 词，When 完成创建，Then 助记词为 24 词且同样能完整恢复账户。

### US2 账户恢复

- **AC2.1** Given 忘记密码但持有助记词，When 执行 `safemedia account recover` 输入助记词与新密码，Then 重建出与原账户地址一致的账户，写出新 keystore。
- **AC2.2** Given 助记词错误或残缺（校验和不过），When 恢复，Then 拒绝且不写出任何文件。
- **AC2.3** Given 账户可登录，When 执行 `safemedia account export-recovery` 并设置独立恢复口令，Then 导出恢复文件。
- **AC2.4** Given 只持有恢复文件，When 执行 `safemedia account recover --recovery <file>` 输入恢复口令与新登录密码，Then 重建账户（地址一致）。
- **AC2.5** Given 恢复口令错误，When 恢复，Then 拒绝。

### US3 封存

- **AC3.1** Given 一个 `.mp4`（H.264/AAC）文件，When 执行 `safemedia seal video.mp4 -o out.svid`，Then 生成 `.svid` 密档；原文件不被修改；不转码（密档体积 ≈ 原体积 + 头部与分块认证标签开销）。
- **AC3.2** Given iPhone 录制的 `.mov`（H.264 或 HEVC），When 封存，Then 成功。
- **AC3.3** Given 一个 `.webm`（VP9/Opus），When 封存，Then 成功。
- **AC3.4** Given 白名单外容器（`.mkv` / `.avi` / `.flv` 等），When 封存，Then 拒绝，并提示该文件可留待二期或以 `--player mpv` 方式观看。
- **AC3.5** Given 封存成功，When 检查 `.svid` 头部，Then 不含原始文件名、时长、分辨率等明文元数据。
- **AC3.6** Given 封存过程中断（Ctrl-C / 进程被杀），When 检查输出位置，Then 不存在明文中间文件；只可能残留未完成的 `.svid`（可安全删除）。
- **AC3.7** Given 第三方仅持有 `.svid` 而无账户，When 用任意播放器或十六进制查看器打开，Then 无法播放，且看不到可识别的视频流结构（对应威胁模型 a）。

### US4 播放（单文件）

- **AC4.1** Given 有效账户与密档，When 执行 `safemedia play out.svid` 并输入正确登录密码，Then 在 `127.0.0.1` 随机端口启动播放服务并自动打开系统浏览器开始播放。
- **AC4.2** Given 播放中，When 拖动进度条到任意位置，Then 1 秒内完成 seek 并继续播放（分块独立解密）。
- **AC4.3** Given 登录密码错误，When 播放，Then 拒绝，不启动播放服务；错误信息不泄露 keystore 内容。
- **AC4.4** Given 密档被篡改（任意字节翻转），When 播放或 seek 到被篡改分块，Then 明确报告完整性校验失败，进程不崩溃。
- **AC4.5** Given HEVC 编码的 `.mov` 密档与不支持 HEVC 的浏览器（如 Firefox），When 打开播放页，Then 页面检测到无法解码并提示改用 `--player mpv`。
- **AC4.6** Given 非 `.svid` 文件或头部损坏的文件，When 播放，Then 报告“不是密档文件”。

### US5 播放（目录索引）

- **AC5.1** Given 目录内有多个 `.svid`，When 执行 `safemedia play dir/` 并登录，Then 显示索引页列出全部密档（以密档文件名为条目），点选其一进入播放。
- **AC5.2** Given 目录内无 `.svid`，When 播放目录，Then 显示空列表提示。
- **AC5.3** Given 索引页，When 查看条目，Then 不显示任何原始文件名等额外元数据（与 FR-2.3 一致）。

### US6 mpv 模式

- **AC6.1** Given 本机已安装 mpv，When 执行 `safemedia play out.svid --player mpv`，Then 调起 mpv 播放解密流，HEVC mov 亦可正常播放。
- **AC6.2** Given 本机未安装 mpv，When 使用 `--player mpv`，Then 明确提示未检测到 mpv 并退出，不启动浏览器流程。

### US7 可搬运性

- **AC7.1** Given 账户文件与 `.svid` 拷贝到另一台装有 safemedia 的机器，When 播放并输入正确密码，Then 离线正常播放（全程无网络请求）。
- **AC7.2** Given 只有 `.svid` 而无账户文件，When 尝试播放，Then 无法进行（威胁模型 a 的直接验证）。

### US8 锁定

- **AC8.1** Given 播放结束（Ctrl-C 退出进程），When 进程退出，Then `127.0.0.1` 端口关闭，内存中密钥材料清零。
- **AC8.2** Given 播放服务空闲超过 30 分钟（无任何流请求），When 计时到达，Then 自动停止播放服务并锁定。
- **AC8.3** Given 播放进程已退出，When 检查磁盘，Then 不存在 safemedia 产生的明文视频数据（对应威胁模型 b）。

## Non-Functional Requirements

- **NFR-1 性能**：封存吞吐不低于磁盘顺序读速度的 50%（依赖 AES-NI）；seek 首字节延迟 < 1s。
- **NFR-2 安全**：keystore 使用 scrypt（强度不低于 N=2^17）；每分块独立 nonce；文件密钥每密档随机生成；ECIES 封装使用 secp256k1。
- **NFR-3 兼容**：macOS 与 Linux；浏览器为 Chrome / Safari / Firefox 最新稳定版。
- **NFR-4 依赖**：最小化；secp256k1 + keystore + BIP-39 允许引入 go-ethereum（或其子包）作为唯一重依赖。
- **NFR-5 明文不落盘**：封存与播放全程不产生明文临时文件。

## 三层测试映射（供 tasks 阶段派生用例）

| 层 | 覆盖 |
|----|------|
| L1 单元 | 分块加解密与 nonce 派生、`.svid` 头部编解码、BIP-39 生成/校验、keystore 读写、容器白名单、ECIES 封装/解封装 |
| L2 集成 | seal→play 全链路、恢复流程（US2 全部）、篡改检测（AC4.4）、目录索引、中断残留（AC3.6） |
| L3 E2E | 真实浏览器播放 + 拖进度条、iPhone 实机 mov 封存播放、双机搬运实测、mpv 实测、空闲锁定实测；REPORT.md 含 Pre/Post 对比表 |

## 评审门（用户确认后才写 plan.md）

- [ ] US1–US8 的验收场景全部认可
- [ ] Scope（含/不含）认可
- [ ] NFR 认可（尤其 scrypt 强度与 30 分钟锁定）
