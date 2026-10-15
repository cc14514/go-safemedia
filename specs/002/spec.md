# specs/002 — go-safemedia 桌面外壳（macOS 首版）

- 状态：草稿，待评审（评审门通过后才进入 plan.md）
- 上游：`docs/requirements.md` 的 FR-6；决策依据 `docs/adr/0001-desktop-shell-wails-loopback.md`
- 术语：`CONTEXT.md`（引擎 / 桌面外壳 / 登录页）
- 被继承的规格：`specs/001/spec.md`（引擎行为不变，本规格只加外壳与登录页）
- 日期：2026-10-15

## Overview

外壳 = 一个系统 webview 窗口（Wails v2，WKWebView）套住**既有引擎**：外壳以**锁定态**启动引擎的 `127.0.0.1` 播放服务，窗口加载**登录页**，提交账户口令后解锁私钥并自动重定向到引擎原有的目录树索引页 / 播放页。外壳**不重复实现**封存、启封、目录树与播放 UI，只提供「窗口 + 启动 + 登录 + 禁用 webview 缓存 + 打包」。

首版仅 **macOS arm64**、不签名不公证，经 GitHub Release 分发（zip 内含 `.app` 与 CLI）。

## 依据（已实证，非假设）

`spike`：Wails v2.16.0 在本机 macOS 27.0.1/arm64 上 `wails build -s` 产出 9.1 MB `.app`；直接运行后 WKWebView 由引导页实际导航到 Go 进程的 `127.0.0.1` 随机端口页面并执行其脚本（服务端 marker 的 UA 含 `wails.io`）。构建**不需要 Node/npm**。

## Scope

**包含**：单窗口外壳；loopback 登录页与会话（锁定态启动 → 口令解锁 → 索引页）；复用引擎播放服务与目录树 UI；空闲锁定回到登录页；退出即关端口并清密钥；禁用 webview 磁盘缓存；`make dist` / `make release`（macOS arm64）；版本号经 ldflags 注入。

**不含**：Windows/Linux 外壳（cgo 外壳无法从 macOS 交叉编译，见 ADR 0001）；签名与公证；封存 / 建账户 / 恢复的图形界面（仍走 CLI，外壳只提示命令）；外壳内的 mpv 通道；自动更新；多账户切换。

## User Stories & Acceptance Scenarios

> 以下 Given/When/Then 是测试用例的唯一来源（L1/L2/L3 均从此派生）。

### US1 启动与登录

- **AC1.1** Given 已安装 `safemedia.app` 且账户文件存在，When 双击启动，Then 打开一个系统 webview 窗口并显示登录页；**全程不出现终端窗口**。
- **AC1.2** Given 登录页，When 输入正确账户口令并提交，Then 解锁并进入引擎原有的索引页 / 播放界面，无需再操作终端。
- **AC1.3** Given 登录页，When 输入错误口令，Then 页面提示口令错误，不进行任何解密，可重试；错误信息不泄露 keystore 内容。
- **AC1.4** Given 账户文件不存在，When 启动外壳，Then 登录页提示需先在命令行创建账户并给出命令，**不自动创建**（红线：只新建、不导入）。
- **AC1.5** Given 外壳已启动，When 检查监听地址，Then 播放服务仅监听 `127.0.0.1` 且端口随机；不得监听 `0.0.0.0`。
- **AC1.6** Given 未登录（锁定态），When 直接请求媒体 URL，Then 被拒绝且不 panic。

### US2 浏览与播放（复用引擎 UI）

- **AC2.1** Given 登录成功，When 选择一个密档库目录（或单个密档），Then 显示与 CLI 相同的递归目录树索引页，点选条目进入播放。
- **AC2.2** Given 播放页，When 拖动进度条，Then 行为与 CLI/浏览器路径一致（分块独立解密，seek 首字节 < 1s）。
- **AC2.3** Given 播放页左侧常驻目录树，When 点击其它条目，Then 切换片源，与 CLI 目录模式行为一致（**同一份引擎 UI，外壳不另写一套**）。
- **AC2.4** Given 密档被篡改，When 播放或 seek 到被篡改分块，Then 明确报告完整性校验失败，外壳进程不崩溃。
- **AC2.5** Given 白名单内但 WKWebView 无法解码的编码，When 播放，Then 页面给出无法播放的提示（不静默黑屏）。

### US3 锁定与退出

- **AC3.1** Given 播放服务空闲超过 30 分钟（无任何流请求），When 计时到达，Then 服务停止并锁定，窗口回到登录页。
- **AC3.2** Given 已锁定，When 检查窗口，Then 显示登录页且需重新口令才能进入；对媒体 URL 的请求沿锁定态守卫被拒。
- **AC3.3** Given 关闭窗口，When 外壳进程退出，Then `127.0.0.1` 端口关闭、内存中密钥材料清零。
- **AC3.4** Given 外壳已退出，When 检查磁盘，Then 不存在 safemedia 产生的明文视频数据。

### US4 明文不落盘（红线不因外壳放宽）

- **AC4.1** Given 已通过外壳完整播放一个密档，When 检查 webview 缓存目录（`~/Library/Caches/<bundle-id>/`、`~/Library/WebKit/<bundle-id>/`），Then 不存在明文视频数据（外壳必须禁用 webview 磁盘缓存）。
- **AC4.2** Given 播放响应，When 检查响应头，Then 含 `Cache-Control: no-store`。

### US5 构建与分发

- **AC5.1** Given 干净仓库且本机具备 Go 与 Wails CLI，When 执行 `make dist`，Then 在 `dist/` 产出 `safemedia.app`（arm64）、同名 CLI 二进制与一个含版本号的 zip；**构建过程不需要 npm/Node**。
- **AC5.2** Given 构建出的 `.app`，When 在本机双击，Then 能启动（arm64 上的 ad-hoc 签名由 Go 工具链自动满足）。
- **AC5.3** Given 版本注入，When 执行 `safemedia --version`，Then 显示由 `-ldflags -X` 注入的版本（未注入时为 `dev`）。
- **AC5.4** Given `gh` 已登录且已打 tag，When 执行 `make release`，Then 创建 GitHub Release 并把 zip 作为资产上传。
- **AC5.5** Given 发行包，When 解压，Then 其中的 CLI 与 `.app` 来自同一版本、同一提交。

### US6 引擎与 CLI 回归（不退化）

- **AC6.1** Given 引入登录页与外壳的改动，When 运行 `make test`（L1+L2），Then 全绿。
- **AC6.2** Given CLI 既有的 `account` / `seal` / `play` 命令，When 在本改动后运行，Then 用户可见行为与退出码**不变**（`internal/play` 从「先解锁再起服务」重构为「起锁定态服务后可解锁」，但 CLI 流程与输出不变）。
- **AC6.3** Given `make test-l3`，When 执行，Then `tests/e2e/REPORT.md` 仍全 PASS。

## Non-Functional Requirements

- **NFR-1 体积**：`.app`（含引擎）≤ 15 MB（spike 实测空壳 9.1 MB）。
- **NFR-2 构建**：`make dist` 在本机完成，**不依赖 Node/npm**（`wails build -s` 跳过前端构建）。
- **NFR-3 平台**：首版仅 macOS arm64；不签名、不公证；README 说明 Gatekeeper 的 `xattr -d com.apple.quarantine` 处置。
- **NFR-4 安全**：沿用引擎的分块 AEAD 与 keystore 参数（不因外壳放宽）；外壳禁用 webview 磁盘缓存；服务仅绑环回；媒体 URL 带随机 token。
- **NFR-5 明文不落盘**：外壳不新增任何明文落盘点。
- **NFR-6 依赖**：新增依赖仅 Wails v2（及其传递依赖），须回评审门；不得引入 Node 运行时依赖。
- **NFR-7 可测性**：外壳中可在无 GUI 环境验证的部分（登录页 HTTP、锁定守卫、版本注入、产物命名）必须进 L2；真实窗口行为进 L3。

## 三层测试映射（供 tasks 阶段派生用例）

| 层 | 覆盖 |
|----|------|
| L1 单元 | 登录页 handler（口令校验、会话 cookie、锁定态守卫）、未登录拒绝媒体请求、版本注入读取、产物命名函数 |
| L2 集成 | 进程内/进程外「锁定态起服务 → 登录页 POST → 索引页 → 媒体 Range」全链路（AC1.2/AC1.4/AC1.6）；锁定后媒体请求被拒（AC3.2）；`--version` 注入（AC5.3）；CLI 回归（AC6.2） |
| L3 E2E | 真实 `.app` 启动 → 手工登录 → 播放（AC1.1/AC2.1/AC2.2/AC2.3）→ 篡改（AC2.4）→ 空闲锁定（AC3.1）→ 退出清密钥（AC3.3）→ **检查 webview 缓存目录无明文**（AC4.1）；`REPORT.md` 含 Pre/Post 对比表 |

## 开放决策（评审门需确认）

- **Q9 mpv 逃生通道**：外壳首版是否保留「用 mpv 打开」？**默认选「不做」**（WKWebView 即 Safari 引擎，已覆盖白名单内全部容器含 HEVC；本机无 mpv 无法实测）；CLI 的 `--player mpv` 保持不变。若改为「做」，需新增一条 AC 并在 L3 标为人工验证项。

## 评审门（用户确认后才写 plan.md）

- [ ] US1–US6 的验收场景全部认可
- [ ] Scope（含/不含）认可
- [ ] NFR 认可（体积上限、不签名不公证、不依赖 Node）
- [ ] Q9 mpv 决策确认（默认：外壳不做）