# specs/002 Plan — go-safemedia 桌面外壳（macOS 首版）

- 状态：草案，待评审
- 上游：`specs/002/spec.md`（已过评审门）· `docs/requirements.md` FR-6 · `docs/adr/0001-desktop-shell-wails-loopback.md`
- 宪法：`.specify/memory/constitution.md` v1.0
- 被继承的规格：`specs/001/spec.md`（引擎行为不变，本 plan 只加「门」与外壳）

## 1. Technical Context

| 项 | 值 |
|---|---|
| 语言 | Go 1.26（`go.mod`）· darwin/arm64 |
| 模块路径 | `github.com/cc14514/go-safemedia` |
| 外壳框架 | **Wails v2.16.0**（唯一新增重依赖）；webview = 系统 WKWebView |
| 平台 | 首版仅 macOS arm64；不签名、不公证 |
| 构建 | 外壳 `wails build -s`（跳过前端构建，**不需要 Node/npm**）；CLI 纯 Go 交叉编译 |
| 网络 | 仅 `127.0.0.1` 监听；无出站连接 |

## 2. Constitution Check

| 条款 | 检查 | 结果 |
|---|---|---|
| I 规格先行 | `specs/002/spec.md` 已过评审门；本 plan 评审后写 tasks.md 再动代码 | ✅ |
| II 测试门禁 | §10 给出 L1/L2/L3 到 AC 的映射；tasks.md 中测试任务排在实现前 | ✅ |
| III 安全底线 | §5 落实：登录页仅环回、软锁定与退出都清密钥、明文不落盘（no-store 扩大到全路由）、webview 缓存前置验证 | ✅ |
| IV 术语一致 | 新增「引擎 / 桌面外壳 / 登录页」入 `CONTEXT.md`；标识符用 shell / gate / unlock / lock | ✅ |
| V 依赖最小化 | 引擎侧**零新依赖**（登录页复用既有 `html/template`）；外壳侧仅 Wails v2，理由见 §13 | ✅ |
| VI 范围纪律 | 不做签名/公证、Windows/Linux 外壳、封存 GUI、外壳内 mpv；「GUI 非目标」已按本规格修订并记录在 requirements §5 | ✅ |

## 3. 架构总览

```
desktop/                      Wails 外壳（同模块，直接 import internal/*）
  main.go                     窗口 + 引导页 + 目标解析 + 解锁闭包 + OnShutdown
  wails.json
  frontend/dist/index.html    引导页（含 __LOOPBACK_URL__ 占位，运行时注入）
internal/play/                引擎：新增「门（gate）」= 登录页 + 软锁定 + 三态生命周期
internal/cli/                 CLI：保持「终端口令 → 预解锁起服务」，closeOnLock=true
```

单文件形态与目录形态**共用同一个门控**（`core`）。外壳不放任何业务逻辑：窗口只负责「起服务 / 加载 URL / 关服务」。

数据流（外壳）：启动 → 解析目标（库目录或单档）→ 引擎以**锁定态**起 loopback 服务 → 窗口加载引导页 → `location.replace` 到服务 → **登录页** → `POST /login`（口令）→ 外壳解锁闭包（`account.LoadKeystoreWithPassphrase` → `vault.Open` / 取 `Priv`）→ 303 进入索引页 → 播放（全链路 `no-store`）→ 空闲或手动锁定 → **回到登录页**。

## 4. 引擎改动：门（gate）与三态生命周期

核心是把 `core` 从「锁定即关停」改成三态，使「锁定后回登录页」（外壳）与「锁定即关停」（CLI）共用一套实现。

| 状态 | 含义 | 服务行为 |
|---|---|---|
| **Locked** | 密钥已清零，可再次解锁 | 存活；只服务登录页 |
| **Unlocked** | 密钥在内存 | 服务内容路由 |
| **Closed** | 终态 | 已关停，返回 410 |

- `core` 新增：`unlocked atomic.Bool`、`closed atomic.Bool`、`closeOnLock bool`、会话值。
- `core.lock(reason)`：清零密钥 + 置 Locked；**若 `closeOnLock`** 则 shutdown 并置 Closed（CLI，行为同 AC8.2）；否则保持存活（外壳，AC3.1/3.2）。
- 新增 `core.gate(content http.Handler) http.Handler` 取代现有 `c.wrap`：

| 请求 | 状态 | 行为 |
|---|---|---|
| `POST /login` | Locked | 调 `unlockFn(口令)`；成功 → 303 到 `/` + `Set-Cookie` 会话；失败 → 重绘登录页并提示（AC1.3） |
| 媒体路径 | Locked | `403` 未登录（AC1.6，不 panic） |
| 其它 GET | Locked | 登录页 |
| 任意 | Closed | `410` |
| `POST /lock` | Unlocked | 现有 `handleLockRequest` |
| 其它 | Unlocked | `content` |

- **会话 cookie**：登录成功下发 `sv_session=<随机>`（`HttpOnly`、`SameSite=Strict`、`Path=/`）；内容路由校验，防本机其它页面盲扫端口。媒体 URL 的随机 token 继续保留（双保险）。
- **解锁回调**（关键：保持 `play` 不依赖 `account`，回调由调用方注入）：
  - `play.Options` 新增 `Unlock func(password string) (name string, r *vault.Reader, err error)`；原 `Reader` 字段成为「**预解锁**」路径（CLI 与测试用）。
  - `play.LibraryOptions` 新增 `Unlock func(password string) (*ecdsa.PrivateKey, error)`；原 `Priv` 字段成为预解锁路径。
  - 两者都新增 `SoftLock bool`（默认 `false` = 锁定时关停服务；外壳传 `true` = 软锁定回登录页）。
  - reader / priv 改用 `sync.RWMutex` 保护（解锁、锁定与并发请求）。
- 新增页面 `loginPage(target string, errMsg string) string`（`page.go`），沿用既有模板纪律：**不得** `_ = tmpl.Execute(...)`，渲染失败走 `renderError`。

## 5. 安全与红线（本节是评审重点）

- **明文不落盘**：外壳不引入任何落盘点；引擎**所有**含明文的响应（登录页、索引、播放页、媒体、重定向）一律 `Cache-Control: no-store`（把现有 `writeNoCacheHeaders` 扩大到全路由，不只是媒体）。
- **webview 磁盘缓存（AC4.1）**：机制 = 明文只经 loopback HTTP 且 `no-store`，WKWebView 不持久化响应体；引导页内嵌资源不含明文。**实现前置验证（tasks 的第一项，先于相关实现）**：在真实外壳里播放后扫描 `~/Library/WebKit/<bundle-id>`、`~/Library/Caches/<bundle-id>` 等 WebKit 数据目录，确认无明文；若发现残留 → 改用 Wails 的**非持久化 WKWebsiteDataStore**；两者都不行才回评审门。**不采用「退出时删缓存」这类补丁式缓解。**
- **只绑环回**：沿用 `net.Listen("tcp", "127.0.0.1:0")`；不得出现 `0.0.0.0`。
- **密钥清零**：软锁定与进程退出都走 `onLockExtra`（`vault.Reader.Wipe` / `wipePrivateKey`）。
- **红线**：登录页**不提供**「导入助记词/私钥」；账户文件不存在时只提示 `safemedia account create`（AC1.4）。

## 6. 外壳（`desktop/`）设计

- `desktop/main.go`：以**锁定态**起引擎 loopback 服务 → embed 引导页 → `assetserver.Middleware` 注入 `__LOOPBACK_URL__`（spike 已实证）→ `OnShutdown` 调引擎 `Close()`（AC3.3）。
- **目标解析（AC2.1）**，优先级：
  1. 启动参数（Finder 双击 `.svid` / `open -a safemedia.app <path>` / 拖放）
  2. 配置 `~/.safemedia/desktop.json` 的 `library_dir`
  3. 默认 `~/.safemedia/library`
  原生目录选择器（`runtime.OpenDirectoryDialog`）列为后续项，不进首版。
- **解锁闭包**：`account.LoadKeystoreWithPassphrase(cli.AccountPath(), pw)` → 单档 `vault.Open` / 目录取 `Priv`；口令字节用 `account.Wipe` 清零。
- 不需要 Node：前端只有一个内嵌引导页（`wails build -s`）。
- 外壳**不实现**封存 / 建账户 / 恢复 / mpv（Scope、AC6.3）。

## 7. CLI 改动（保持行为不变 = AC6.2）

- `internal/cli/play.go`：构造服务时**不传 `SoftLock`**（零值 = 锁定时关停，与改动前一致），走**预解锁**路径（先 `unlockAccount()` 再 `vault.Open` / `NewLibrary`），**不暴露登录页**；输出与退出码不变。
- `internal/cli/root.go`：`version` 改为包级 `var Version = "dev"`，`version` 子命令经 `-ldflags -X` 注入（AC5.3）；对外提供 `cli.Version()` 供外壳读取。

## 8. 构建与分发（AC5）

```make
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/cc14514/go-safemedia/internal/cli.Version=$(VERSION)

dist-cli:   # 纯 Go 交叉编译
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/safemedia ./cmd/safemedia
dist-app:   # Wails 外壳；-s 跳过前端构建（不需要 npm）
	cd desktop && wails build -s -ldflags "$(LDFLAGS)"
dist: dist-cli dist-app   # 组装 dist/safemedia_$(VERSION)_darwin-arm64.zip（含 safemedia.app 与 safemedia）
release: dist             # gh release create v$(VERSION) dist/*.zip
```

- macOS 的 ad-hoc 签名由 Go 工具链 / `wails build` 负责；不签名、不公证（NFR-3），README 说明 Gatekeeper 的 `xattr -d com.apple.quarantine` 处置。
- `dist-cli` 本身可纯 Go 交叉编译到 windows/linux，但本版 `dist` 只出 darwin/arm64（Scope）。

## 9. 里程碑

| M | 内容 | 覆盖 AC |
|---|---|---|
| M1 | 引擎门控：`core` 三态 + `gate` + 登录页 + 会话 + `Options.Unlock`/`SoftLock` + L1 | AC1.3 / 1.5 / 1.6 / 3.2 |
| M2 | CLI 回归：预解锁路径（`SoftLock` 保持零值）+ 版本注入 + L2 | AC6.2 / 5.3 |
| M3 | `desktop/` 外壳（引导页 + 目标解析 + 解锁闭包 + `OnShutdown`）+ **缓存前置验证** | AC1.1 / 1.2 / 1.4 / 2.1 |
| M4 | Makefile `dist` / `dist-cli` / `dist-app` / `release` + 打包 | AC5.1 / 5.2 / 5.4 / 5.5 |
| M5 | L3：真实 `.app` 全流程 + 缓存目录扫描 + `REPORT.md` Pre/Post | AC2.4 / 3.1 / 3.3 / 4.1 |

## 10. 测试策略（三层门禁映射）

- **L1**（`internal/play/*_test.go`）：登录页 handler（口令对 → 303+会话 cookie；口令错 → 仍登录页且无 cookie）；`gate` 状态机（Locked 下媒体 → 403、Closed → 410、软锁定后回登录页）；`CloseOnLock` 语义；版本注入默认值。→ 实际实现见 `gate_test.go` 与 `SoftLock` 语义。
- **L2**（`tests/integration/`）：锁定态起服务 → `GET /` 得登录页 → `POST /login` → 303 + cookie → 索引页 → 媒体 Range **逐字节等于明文**（正向）；错口令（错误）；软锁定后媒体 403 且不 panic（回归，AC3.2）；**CLI `play` 全流程输出与退出码不变**（回归，AC6.2）。
- **L3**（`tests/e2e/`）：真实 `.app` 双击 → 登录 → 播放/seek → 篡改报错 → 空闲锁定回登录页 → 退出清密钥 → **扫描 webview 数据目录确认无明文**；`REPORT.md` 含 Pre/Post 对比表。

## 11. 风险与缓解

| 风险 | 缓解 |
|---|---|
| R1 webview 持久化缓存可能落盘，破坏「明文不落盘」 | 前置验证（M3 首任务）+ 全路由 `no-store`；失败则改用非持久化 `WKWebsiteDataStore`；**不采用删缓存补丁** |
| R2 门控改动触及 CLI 既有路径（回归） | `SoftLock` 零值（关停）与外壳显式 `true`（软锁定）区分；AC6.2 + L2 回归全跑 |
| R3 口令经 loopback 表单明文提交 | 仅 `127.0.0.1`、`HttpOnly`+`SameSite=Strict`、服务短命；环回上不加 TLS（无意义）；口令不写日志 |
| R4 reader/priv 并发访问（解锁/锁定 vs 请求） | `sync.RWMutex` + atomic 状态；L1 跑 `-race` |
| R5 Wails v2 传递依赖庞大 | 仅 `desktop/` import，CLI 不引 Wails；§13 记录 |

## 12. 开放问题

1. 登录页是否需要「记住上次目标库」的持久化？首版否（每次由启动参数/默认解析）。
2. Bundle ID 命名（决定缓存目录扫描路径）：拟 `com.cc14514.safemedia`，M3 定。

## 13. Complexity Tracking

| 依赖/复杂度 | 理由 | 被拒的替代方案 |
|---|---|---|
| **Wails v2** | 系统 webview 外壳；自带 `.app` 打包与原生对话框；同语言可直接 import 引擎；运行期不需要 Node | webview_go（窗口/打包全自写）、Swift+WKWebView（需 sidecar+IPC，仅 mac）、Tauri（需 Rust + Go sidecar）、Electron（150 MB） |
| **引擎内新增「门 / 三态生命周期」** | 外壳要「登录前锁定、锁定后回登录页」，CLI 要「锁定即关停」；两者必须共用一套 `core`，否则红线（明文不落盘/只绑环回/清零）会有两份实现 | 外壳自己写登录页并另起一个服务（重复引擎逻辑、双份红线、两处易错） |

## 14. 评审门

- [x] 确认引擎三态生命周期与 `SoftLock` 语义（CLI 保持关停，外壳软锁定回登录页）——已实现并过 L1/L2（参数名以实现为准，见 `tasks.md` 对齐记录）
- [ ] 确认 webview 缓存策略（R1：前置验证 + 非持久化数据存储；不接受补丁式删缓存）
- [ ] 确认 `dist` / `release` 目标与版本注入方式
- [ ] 确认目标解析优先级（启动参数 > 配置 > 默认库）

通过后 → `specs/002/tasks.md`（每个 User Story 的测试子任务排在实现子任务之前）。