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
internal/cli/                 CLI：保持「终端口令 → 预解锁起服务」，不传 SoftLock（零值 = 锁定即关停）
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

- `core` 新增：`unlocked atomic.Bool`、`closed atomic.Bool`、`softLock bool`（参数名以实现为准，见 `tasks.md` 对齐记录 1）、会话值。
- `core.lock(reason)`：清零密钥 + 置 Locked；**`softLock=false`（零值）** 则 shutdown 并置 Closed（CLI，行为与改动前一致）；否则保持存活（外壳，AC3.1/3.2）。
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
- **目标解析（AC2.1 的实现偏差，见 §12）**：
  1. 配置文件 `~/.safemedia/desktop.json` 的 `library`（推荐；双击即生效）
  2. 默认库 `~/.safemedia/library`（不存在则创建；空库合法）
  启动参数（argv）仍受支持，但**仅在从终端直接运行二进制时有效**——macOS 经 LaunchServices 启动（双击 / `open`）时参数不会送达（实测 macOS 27，见 `tasks.md` 对齐记录），故不作依赖。
  原生目录选择器（`runtime.OpenDirectoryDialog`）列为后续项，不进首版。
- **解锁闭包**：`account.LoadKeystoreWithPassphrase(cli.AccountPath(), pw)` → 单档 `vault.Open` / 目录取 `Priv`；口令字节用 `account.Wipe` 清零。
- 不需要 Node：前端只有一个内嵌引导页（`wails build -s`）。
- 外壳**不实现**封存 / 建账户 / 恢复 / mpv（Scope、AC6.3）。

### 6b. 库根目录选择器（US7，引擎侧实现，外壳零逻辑）

- **路由**：`GET /library?path=<dir>` 目录选择页（面包屑 + 上级 + 子目录列表；只列目录、跳过隐藏项与符号链接，与 `ScanLibrary` 同口径；每个子目录显示其**直接**包含的密档数与子目录数）；`POST /library`（表单字段 `path`）切换库根。
- **`LibraryOptions` 新增**：`Root string`（当前库根，供选择页显示与下钻）与 `OnLibraryChange func(root string) error`（持久化回调；nil = 只在本会话生效）。
- **`Library.SetRoot(root)`**：`filepath.Abs` + 展开 `~` → 必须是存在的目录 → `ScanLibrary` 重扫 → 在同一把 `l.mu` 下重建 `byID/order/tree/root` 并刷新登录页目标文案；失败**不改动**任何现有状态。索引页/播放页/媒体请求读同一把锁（`-race` 必过）。
- **门控**：两个路由都挂在 `gate` 之内 → 锁定态自动回落登录页（AC7.6、NFR-8 不允许锁定态枚举目录）。
- **持久化**：外壳注入回调写 `~/.safemedia/desktop.json` 的 `library`（读改写，保留 `account`；写失败要在页面上可见，不静默丢选择）；CLI 传 nil（AC7.8）。
- **外壳始终库模式**（AC7.7）：单密档目标取 `filepath.Dir(path)` 为库根，使目录选择器在任何启动方式下都可用；CLI 的单档模式（`play.Options`）保持不变。

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

## 14. 账户选择（US8：引擎侧渲染 + 外壳侧发现）

**数据流**：外壳启动 → 扫描账户（`desktopcfg.ListAccounts`）→ 以 `[]play.AccountOption` 注入引擎 → 锁定态登录页渲染账户列表（首项默认选中）→ `POST /login` 带 `account` + `password` → 引擎**先校验标识在列表内**（不在则拒绝，不调回调）→ `Unlock(accountID, password)`（外壳按该路径加载 keystore）→ 成功 303 + 会话 cookie。

**发现规则**（`internal/desktopcfg.ListAccounts(home, cfg)`）：

1. `~/.safemedia/*.json`（顶层）；
2. `~/.safemedia/accounts/*.json`（子目录，供多账户归置）；
3. `cfg.Account` 指定的路径（可在数据目录之外，如 CLI 的 `--account`）。

按绝对路径去重；**只有能解析出 `address` 的 keystore v3 才算账户**（`desktop.json`、任意 JSON 自动排除）；`desktop.json` 指定的账户排首位；每项 `{Path, Name, Address}`，`Name` = 文件名，短地址由 `ShortAddress` 生成（形如 `0x1a2b…9f8e`）。

**引擎 API（`play.Options` 与 `play.LibraryOptions` 同形）**：

```go
type AccountOption struct{ ID, Label, Note string } // ID 不透明；外壳传 keystore 路径
Accounts []AccountOption                            // 空 = 不渲染选择器（登录页仅口令）
Unlock   func(accountID, password string) (...)      // 取代原 func(password)
```

- `len(Accounts) > 0` 时 `POST /login` 的 `account` 必须在列表内，否则渲染「请选择一个可用账户」且**不触发解锁回调**（AC8.4）。
- 失败重绘时把上次提交的 `account` 保持为选中项（AC8.3）。
- `len(Accounts) == 0` 时不渲染选择器、`accountID` 为空串透传——既有 ad-hoc 解锁路径（测试）行为不变。

**安全边界**（AC8.7，与 NFR-8 同款）：

- 账户发现在**外壳侧**启动时做一次；引擎不遍历文件系统，只渲染注入的列表。
- 只读 keystore 的公开 `address` 字段，**不需要口令**；口令错误时提示「口令错误或与所选账户不匹配」（外壳把 `account.ErrWrongPassword` 包装为该中文文案）。
- 锁定态登录页不列目录、不列密档；服务仍只绑 `127.0.0.1`，响应仍 `no-store`。
- 外壳侧对 `accountID` 再校验一次（必须在其发现的集合内），防引擎被绕过时按任意路径加载。

**文案精简（AC8.6）**：登录页删除 `hint` / `redline` 段落，只留 标题 / 库根 / 账户列表 / 口令 / 解锁按钮 / 失败原因；索引页删除「条目只显示密档文件名…」与脚注；播放页删除环回说明句（保留目录树操作提示）。红线提示仍在 `account create` 输出与文档中（宪法 III 不要求页面展示）。

**风险**：R6 账户列表在登录页是**启动时快照**（运行中新增账户要重启才出现在登录页）——与库根扫描同口径；**账户管理页每次渲染重新发现**（AC9.1），因此管理动作后立刻可见。

## 16. 账户管理（US9：引擎侧页面 + 外壳侧命令层）

**包边界不破**：`internal/play` 依然**不 import `internal/account`**。管理动作以**注入式接口**交给调用方（外壳），引擎只负责页面、状态机与门控——与 `Unlock` / `Accounts` 的做法一致：

```go
// internal/play：只用字符串，不引用账户类型
type AccountsAdmin interface {
    List() []AccountOption                                                  // 每次渲染重新发现（AC9.1）
    DefaultTargetName() string                                             // 无账户时=默认账户文件名（AC9.4）
    Create(req AccountCreateRequest) (AccountOption, string /*助记词*/, error)
    ConfirmMnemonicAndSave(token, answers string) (AccountOption, error)     // 复述抽查通过才写盘（AC9.2/9.3）
    RecoverFromMnemonic(req AccountRecoverRequest) (AccountOption, error)
    RecoverFromRecoveryFile(req AccountRecoverRequest) (AccountOption, error)
    Delete(path, password string) error                                     // 口令授权 + 移入回收目录（AC9.8/9.9）
}
```

实现落在 `internal/desktopcfg`（该包已 import `internal/account`，且已持有数据目录与发现逻辑）：`CreateAccount` / `RecoverAccountFromMnemonic` / `RecoverAccountFromRecoveryFile` / `DeleteAccount` / `ValidateAccountName` / `AccountTargetPath`。scryptN 由调用方注入（生产 `account.DefaultScryptN`，测试 `1<<10`）。

**路由与状态表**（全部**仅锁定态**，AC9.10；已解锁访问 → 303 到 `/`）：

| 路由 | 方法 | 行为 |
|---|---|---|
| `/accounts` | GET | 账户列表（名称 + 短地址 + 是否配置指定）+ 入口（创建 / 恢复 / 删除）；空列表显示创建入口 |
| `/accounts/create` | GET | 表单：名称（无账户时固定为默认账户名）+ 口令 + 二次确认 |
| `/accounts/create` | POST | 校验名称与口令 → 生成账户 → **保留待确认状态**（随机 token，内存，含助记词）→ 渲染助记词页（编号网格 + 3 个抽查序号，无复制按钮） |
| `/accounts/create/confirm` | POST | 抽查答案全对 → 写 keystore → 303 到 `/accounts`（成功提示）；任一错 → 重绘助记词页并提示（**不写盘**，AC9.3） |
| `/accounts/create/cancel` | POST | 丢弃待确认状态（不写盘），回 `/accounts` |
| `/accounts/recover` | GET/POST | 二选一：`from=mnemonic`（助记词 + 名称 + 新口令）/ `from=recovery`（恢复文件路径 + 恢复口令 + 新口令）→ 写盘（已存在则先 `.bak`）→ 303 |
| `/accounts/delete` | POST | `path` + `password`：口令正确且路径合法 → 移入回收目录 → 303（AC9.8/9.9） |

**待确认的助记词状态**（AC9.12）：`pending{token, name, account, mnemonic, answers, tries}` 只存在内存；确认/取消/失败超限/锁定/`Close()` 时清零并从 map 删除；抽查答案用 `crypto/rand` 生成（12 词抽 3）；答案逐词比较（与 CLI `ConfirmMnemonic` 同语义）；**页面 `no-store`、无复制按钮、绝不写日志**。

**目标路径策略**（AC9.4，拒覆盖）：
- 无任何账户 → `<数据目录>/keystore.json`（与 CLI 默认账户一致，使 `safemedia seal` 立刻可用）；
- 已有账户 → `<数据目录>/accounts/<名称>.json`；
- 名称校验：非空、≤64、无路径分隔符 / `..` / 控制字符、不以 `.` 开头、补 `.json`、**目标不存在**。

**删除**（AC9.8/9.9）：路径必须 ∈ 发现列表 **且** 在数据目录内（`cfg.Account` 指向外部 → 拒绝并提示手动处理）；口令经 `LoadKeystoreWithPassphrase` 验证（错 → 不删）；移动目标 `<数据目录>/trash/<时间戳>-<名称>`（同文件系统 `os.Rename`，冲突则加序号）；页面明确「可还原」「该账户封的密档将永久无法启封，密档文件本身保留」。删的是**账户文件**，绝不触碰任何密档。

**配置同步**（AC9.11）：外壳在删除后若发现 `desktop.json` 的 `account` 指向被删文件（或该文件已不存在），清空该字段并落盘（保留 `library`）。

**风险**：R7 管理动作由页面触发，误操作面比 CLI 大——缓解＝删除需口令 + 回收目录 + 明确后果文案 + 锁定态专属；R8 助记词进入 DOM——缓解＝无复制按钮、`no-store`、待确认态短生命周期、离开即清（不落盘已由 NFR-5/AC4.1 的缓存对照实验覆盖）。

## 17. 图形化封存（US10：引擎侧队列 + 外壳侧原生拖放）

**拖放通道**：外壳开 `options.DragAndDrop{EnableFileDrop:true}`，用 `runtime.OnFileDrop(ctx, func(x, y int, paths []string))` 拿**绝对路径**。这是 Wails 原生通道（macOS 侧即 `NSWindow` 的 drop target → `wails:file-drop` 事件，已读 v2.16 源码确认），**不经 DOM、不经 webview**，因此不受 webview 的 DOM 拖放/安全限制（无需 crypto.subtle）影响。外壳把路径 POST 给引擎的**令牌 URL** `/drop/<token>`（随机 token、环回、生命周期同服务，与 `/media/<token>` 同款），然后让 webview 跳转 `/seal`（`runtime.WindowExecJS`）。外壳仍是零业务逻辑，引擎是唯一实现处。

**引擎侧新增 `internal/seal`**（纯函数，便于 L1 直测）：
- `Collect(paths []string, outDir string) (jobs []Job, rejects []Reject)`：把 CLI 的 `collectSealJobs` / `walkSealDir` / `sealFileOutput` **搬到这里**，**CLI 改为调用它**（AC10.11：一套展开逻辑，两处呈现）；`Reject{Path, Reason}` 承载「非白名单 / 已是密档 / 目标已存在」等原因。
- `Run(ctx, jobs, pub, opts)`：顺序执行（不并发，AC10.5），每条目回调 `Progress{State, Done, Total, Err}`。
- **不覆盖**：目标用 `O_CREATE|O_EXCL` 打开，在写任何字节**之前**判定（`errors.Is(err, fs.ErrExist)` → `ErrExists` 哨兵；CLI 据此打印原样的「已存在（用 --force 覆盖）」，页面显示「已存在（跳过）」）。
- **失败/中止清理半成品**（AC10.8）：与 CLI 同语义，删除未完成的 `.svid`（其中不含明文）。

**取消**（AC10.9）：`internal/vault` 增加 `SealFileContext(ctx, src, dst, pub, onProgress)`（`SealFileWithProgress` 保留并委托 `context.Background()`，CLI/测试不受影响）；`progressReader` 在每次读前检查 `ctx.Err()`，取消即返回 `context.Canceled` → `SealFileContext` 中止并让 `seal.Run` 清理半成品。

**页面**：索引页工具栏加「封存」按钮 → `/seal` 面板（当前输出目录 + 「更改输出目录…」+ 条目表：名称 / 进度条 / 已写与总字节 / 状态 / 失败原因），面板内 `setInterval` 每秒 GET `/seal/status`（JSON）刷新。**`/seal/status` 不调用 `touch()`**——自动刷新不得把空闲锁定刷掉。`/seal/output` 复用目录选择页（`purpose=seal`）改输出目录（仅本次会话）。

**锁定边界**（AC10.7）：`/drop/<token>` 与 `/seal/status` 在锁定态一律拒绝（409/落到登录页），外壳据 409 弹原生提示「请先解锁」。空闲看门狗在 `runner.Active()` 为真时**跳过**判定（AC10.9 前半）；显式锁定 `runner.Cancel()`（后半）。

**完成后刷新**（AC10.10）：队列结束（含部分失败）后调用库根重扫（与 US7 同一函数）。

**风险**：R9 未解锁时拖入易困惑 → 外壳据 409 弹原生提示；R10 封存与重扫并发 → 重扫只在队列结束后触发一次。

## 15. 评审门

- [x] 确认引擎三态生命周期与 `SoftLock` 语义（CLI 保持关停，外壳软锁定回登录页）——已实现并过 L1/L2（参数名以实现为准，见 `tasks.md` 对齐记录）
- [x] 确认 webview 缓存策略（R1）：**对照实验已证伪**——可缓存对照落入 `NetworkCache`（检索有效），引擎 `no-store` 响应与真实 mp4 流零残留；无需改用非持久化数据存储（证据见 `tests/e2e/REPORT.md` 002 段）
- [x] 确认 `dist` / `release` 目标与版本注入方式（`make dist VERSION=` / `make release VERSION=`，`-ldflags -X`）
- [x] 确认目标解析优先级（启动参数 > 配置 > 默认库）——**实测偏差**：macOS 经 LaunchServices 启动不送达 argv，故实际以界面内目录选择器 + `desktop.json` 为准（见 `tasks.md` 对齐记录 6）
- [x] 确认 US8 账户选择范围：列表 = `~/.safemedia`（含 `accounts/`）+ 配置指定路径，启动时扫描一次；外壳不提供建账户 UI

通过后 → `specs/002/tasks.md`（每个 User Story 的测试子任务排在实现子任务之前）。