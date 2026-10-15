# specs/002 Tasks — 桌面外壳（macOS 首版）

- 上游：`specs/002/spec.md`（已过评审门）· `specs/002/plan.md`（已过评审门）
- 规则：每个 User Story/里程碑内，**测试子任务排在实现子任务之前**（TDD：RED → GREEN → REFACTOR）。
- 门禁：每个里程碑结束跑 `make test`；全部完成跑 `make test-l3`。
- **状态：全部完成**（2026-10-15）。实现与计划的两处用语差异见文末「对齐记录」。

---

## M0 · 前置验证

- [x] **T0.1**（spike）Wails v2.16.0 外壳能否 `location.replace` 到 loopback URL —— 实证通过（marker + UA `wails.io`），见 `docs/adr/0001`。
- [x] **T0.2**（spike，安全红线前置）真实外壳播放后扫描 webview 数据目录，确认**无明文**——**通过，未改用非持久化数据仓库**。
  → 证据：可缓存对照响应 1.48MB 被写入 `~/Library/Caches/com.wails.safemedia/WebKit/NetworkCache`（证明检索方法有效），而引擎同款 `no-store` 响应 1.32MB×2 与真实 mp4 媒体 688KB（Range 流）在缓存/临时目录**零残留**。见 `ctx` 记忆 #61。

## M1 · 引擎门控（US1 登录 + US3 软锁定）

### 测试（先写）
- [x] T1.1 `internal/play/gate_test.go`：`gate` 状态机 —— Locked 下 `GET /` 返回登录页（`<form action="/login">` + `no-store`）。（文件名与计划不同：全部门控用例集中在一个新文件，便于共存）
- [x] T1.2 同上：`POST /login` 口令正确 → 303 + `Set-Cookie: sv_session=…`（HttpOnly/SameSite=Strict）；口令错误 → 重绘登录页 + 不建会话。
- [x] T1.3 同上：软锁定后 `Locked()==true`、端口仍存活、内容回到登录页；且**可再次解锁**。
- [x] T1.4 同上：会话校验 —— 带正确 cookie 200；无/错 cookie 且会话已启用 → 403。
- [x] T1.5 `internal/play/play_test.go`：预解锁（`Options.Reader`）路径**不要求**会话 cookie（CLI 兼容）。
- [x] T1.6 `internal/play/gate_test.go`：`loginPage` 渲染含表单与目标描述、错误时含错误文案、模板失败不静默截断。
- [x] T1.7 `go test -race ./internal/play/...` 通过；**修掉两处真实竞态**（见「对齐记录」）。

### 实现
- [x] T1.8 `core.go`：三态生命周期（Locked/Unlocked/Closed）+ `unlocked/closed atomic.Bool` + `softLock` + 会话值 + `sessionCookieName`。
- [x] T1.9 `core.go`：`gate(content)` 门控中间件；`/login` 处理、锁定态媒体路径 403、Closed 410、`/lock` 仅在解锁态可达。
- [x] T1.10 `core.go`：`lock()` = 清零 + 软锁定/关停；空闲监视在锁定后停止、解锁后重启。
- [x] T1.11 `play.go`：`Options` 增 `Unlock` 与 `SoftLock`；`reader/name` 用 `RWMutex` 保护，媒体路径读锁。
- [x] T1.12 `library.go`：`LibraryOptions` 增 `Unlock` 与 `SoftLock`；`Priv` 可空；私钥读写与 `wipe` 用同一 `Mutex` 串行化。
- [x] T1.13 `page.go`：新增 `loginPage(target, errMsg)` 模板（复用既有风格、中文文案、`renderError` 纪律）。

## M2 · CLI 回归与版本注入（US5、US6）

### 测试（先写）
- [x] T2.1 `tests/integration/play_cli_test.go`：CLI `play` **端到端输出与退出码不变**，且**不出现登录页**（AC6.2）。CLI 的既有单档/目录/锁定用例全部原样通过。
- [x] T2.2 `internal/version`：版本号从 CLI 包抽到独立包（CLI 与外壳共用一份），默认 `0.2.0-dev`；`go build -ldflags -X` 注入验证通过（`safemedia version` 显示注入值）。
- [x] T2.3 CLI 锁定行为不变：CLI 不传 `SoftLock` → 锁定时**关停服务**（既有 L1/L2 用例锁定这一点）。

### 实现
- [x] T2.4 `internal/cli`：CLI 保持预解锁路径（`Options.Reader` / `LibraryOptions.Priv`），不传 `SoftLock`，行为一字不改（**零改动**即达成）。
- [x] T2.5 `internal/version/version.go` + `internal/cli/root.go`：`version` 子命令改用 `version.String()`。

## M3 · 桌面外壳（US1/US2/US3 的落地）

### 测试（先写）
- [x] T3.2 `internal/desktopcfg/target_test.go`：目标解析三级优先级（启动参数 > 配置 > 默认库）单测。
- [x] T3.3 `internal/desktopcfg/target_test.go`：配置读写三态（缺省/损坏/合法）。
- [x] T3.9（新增）`tests/integration/desktop_flow_test.go`：**外壳链路的灰盒集成**——锁定态起服务 → GET `/` 得登录页（含 `no-store`）→ 错误口令停在登录页且无会话 → 正确口令 303 + 会话 cookie → 索引页 → 播放页媒体地址 → 无会话访问媒体 403 → 锁定 → 手动锁定后**未解锁取媒体 403 且不 panic** → 关闭。

### 实现
- [x] T3.4 `desktop/wails.json` + `desktop/frontend/dist/index.html`（引导页，`__BOOT__` JSON 占位，出错时显示中文原因）。
- [x] T3.5 `desktop/main.go`：锁定态起引擎 → embed 引导页 → `assetserver.Middleware` 注入 URL → `OnShutdown` 调 `Close()`。
- [x] T3.6 `desktop/main.go`：目标解析 + 解锁闭包（`account.LoadKeystoreWithPassphrase` → `vault.Open` / `Priv`），口令字节 `account.Wipe` 清零。
- [x] T3.7 登录页显示解析出的目标（库目录/单档名）。

## M4 · 构建与分发（US5）

- [x] T4.1 `Makefile`：`VERSION`（取自 `internal/version`）+ `LDFLAGS` + `desktop` / `desktop-test` / `desktop-run` / `dist` / `release` / `clean`。（未拆 `dist-cli`/`dist-app`：`dist` 依赖 `desktop` 并顺带构建 CLI，少两个入口更不易误用）
- [x] T4.2 `dist` 产物：`dist/safemedia_$(VERSION)_darwin-arm64.zip` 含 `safemedia.app` 与 `safemedia`（+`说明.txt`）；`unzip -l` 结构校验通过；CLI 与 `.app` 版本注入均验证。
- [x] T4.3 `README.md` 与 `docs/manual.md`：桌面版安装、首次使用、Gatekeeper `xattr -dr` 说明。

## M5 · L3 端到端（交付门禁）

- [x] T5.1 `tests/e2e/harness_test.go`：报告新增「002 桌面外壳」段（引擎启动态、登录页、媒体 403、缓存扫描结论）。
- [x] T5.2 该段含 Pre/Post 对比（登录前后、锁定前后、缓存目录扫描）。
- [x] T5.3 `make test-l3` 生成 `tests/e2e/REPORT.md` 且全 PASS。

## M7 · 库根目录选择器（US7，用户 2026-10-15 指示新增）

### 测试（先写）
- [x] T7.1 `internal/play/picker_test.go`：已解锁时 `GET /library` 返回选择页（当前库根、面包屑、子目录列表、每个子目录的密档数）；隐藏项与符号链接不列出（AC7.1/AC7.2）。
- [x] T7.2 同上：`GET /library?path=<子目录>` 下钻、`?path=<不存在>` 显示中文错误且不 500（AC7.2/AC7.5）。
- [x] T7.3 同上：`POST /library path=<新目录>` → 303 回 `/`，索引页立即按新的子目录结构展示（AC7.3）；重扫后条目 id 重新分配、媒体仍可播。
- [x] T7.4 同上：非法路径（不存在 / 是文件 / 空串）→ 页面显示错误、**原库根与目录树不变**、不 panic（AC7.5）。
- [x] T7.5 同上：`OnLibraryChange` 收到绝对路径；回调报错时页面可见且库根**已生效**（不静默丢选择）。
- [x] T7.6 同上：**锁定态** `GET /library` 与 `POST /library` 都回登录页（AC7.6）；`-race` 下「切换库根 + 并发媒体请求」无数据竞争。
- [x] T7.7 `desktop/main_test.go`：单密档目标 → 库根取其所在目录（AC7.7）；持久化回调写 `desktop.json` 且保留 `account`（AC7.4）。
- [x] T7.8 `tests/integration/picker_flow_test.go`（L2）：锁定态起服务 → `POST /login` → 索引页 → `GET /library` → `POST /library` → 303 → 新树可点播 + 媒体 Range 成功（AC7.1–AC7.3）；CLI 浏览器模式同一页面可用但不持久化（AC7.8）。

### 实现
- [x] T7.9 `page.go`：新增 `librarySrc` 选择页模板（面包屑/路径输入/上级/子目录列表/「使用此目录作为库根」），索引页与播放页侧栏加「选择密档库」入口，索引页显示当前库根。
- [x] T7.10 `library.go`：`LibraryOptions` 加 `Root`/`OnLibraryChange`；`SetRoot`（含 `~` 展开、目录校验、重扫、`l.mu` 串行化）；`GET/POST /library` handler；索引/播放/媒体读同一把锁。
- [x] T7.11 `core.go`：登录页目标文案可更新（`setTarget`）。
- [x] T7.12 `internal/cli/play.go`：传 `Root`（不传 `OnLibraryChange`）。
- [x] T7.13 `desktop/main.go`：始终库模式（单档取所在目录）+ `OnLibraryChange` 写 `desktop.json`（保留 `account`）。
- [x] T7.14 文档：`README.md` / `docs/manual.md` 改为「在页面里选库」（不再要求编辑 JSON）、`dist` 的 `说明.txt`、`AGENTS.md` 结构/易错点。
- [x] T7.15 `page_style_test.go`：页面外观守护 —— 四个页面均无裸 `<a>`（必带 class）与 `text-decoration:underline`；索引页主按钮为 `<a class="btn primary">`；按钮含 hover / 按下 / `focus-visible` 三态与 `user-select:none`；目录行整行可点；播放页侧栏换目录入口为按钮（AC7.9）；`page.go` 的 `baseCSS` 三态样式同步实现。

## M8 · 登录页账户选择 + 文案精简（US8，用户 2026-10-15 指示新增）

> 用户指示：「登录页面，需要能够选择账户，如果有多个账户，需要选对了，才能完成登录。」+「把所有的废话说明都去掉」。

### 测试（先写）

- [x] T8.1 `internal/account/keystore_test.go`：`ReadKeystoreAddress`（正常读到 40 hex 地址 / 文件不存在 / 非 keystore JSON / 无 `address` 字段 → 返回错误且不 panic）；`ShortAddress(addr)` 形如 `0x1a2b…9f8e`（AC8.1）。
- [x] T8.2 `internal/desktopcfg/accounts_test.go`：`ListAccounts` 发现规则（顶层 `*.json` + `accounts/*.json` + `cfg.Account` 指定路径）、按绝对路径去重、**非 keystore JSON 排除**（放入 `desktop.json` 与随机 JSON）、配置指定账户置首、名称=文件名、地址短形式（AC8.1）。
- [x] T8.3 `internal/play/accounts_test.go`：登录页渲染账户列表（首项默认选中、显示地址短形式）；**伪造 `account` 标识 → 提示「请选择一个可用账户」且解锁回调未被调用**（回调调用计数断言，AC8.4）；选错账户 → 提示「口令错误或与所选账户不匹配」且无会话 cookie、重绘后保持选中（AC8.3）；空列表 → 不渲染选择器、`accountID` 为空串透传（AC8.5）；回调收到的 accountID 与提交值一致。已做变异验证：把 `core.handleLogin` 的账户标识校验短路掉后 `TestLoginRejectsAccountOutsideListBeforeUnlock` 变红。
- [x] T8.4 `internal/play/page_style_test.go`（扩充）：四个页面均不含说明性段落（`绝不导入`、`明文不落盘`、`元数据`、`环回`）与 `hint`/`redline` 容器（AC8.6）。
- [x] T8.5 `tests/integration/desktop_flow_test.go`（扩充）：两账户全链路 —— 登录页列出两项 → 伪造标识被拒（回调 0 次调用）→ 选 B 用 A 的口令 → 拒绝且无会话 cookie、重绘保持选中 → 选 B 用 B 的口令 → 303 + 会话（AC8.2–8.4）；「一个账户都没有 → 提示 `account create`」落在 `desktop/main_test.go` 的 `TestDesktopLoginWithoutAnyAccount`（AC8.5）。
- [x] T8.6 先确认 T8.1–T8.5 全部失败（RED），再写实现。
- [x] T8.13 `tests/integration/multiaccount_flow_test.go`（L2）：文档化流程的可执行化 —— `--account <数据目录>/accounts/工作.json account create` 能自动创建 `accounts/` 子目录，且 `desktopcfg.ListAccounts` 同时列出两个账户（名称=文件名、地址不同）、配置指定的账户置首（AC8.1/AC8.5）。已做变异验证：把 `SaveKeystore` 的父目录创建改成创建「父目录的父目录」后该用例变红。

### 实现

- [x] T8.7 `internal/account/address.go`（原计划放 keystore.go）：`ReadKeystoreAddress(path) (string, error)`（只读 JSON、不派生密钥、不做 KDF）+ `ShortAddress(addr) string`。
- [x] T8.8 `internal/desktopcfg/accounts.go`：`ListAccounts(home string, cfg Config) ([]Account, error)`（`Account{Path, Name, Address}`，排序把 `cfg.Account` 置首）。
- [x] T8.9 `internal/play`：新增 `AccountOption{ID,Label,Note}`；`Options.Accounts` / `LibraryOptions.Accounts` 用值切片；`Unlock` 签名改为 `func(accountID, password string)`（`Options` 为 `func(string,string) (string,*vault.Reader,error)`）；`core` 存列表 + 提交的选中项（`accountSel`，供重绘保持）；`handleLogin` 在调用回调**之前**校验标识；`loginPage` 渲染 `<select name="account">`（原生控件）与失败原因。
- [x] T8.10 接线：`desktop/main.go`（`desktopcfg.ListAccounts` → 注入 → 解锁闭包按 ID 加载 keystore、把 `account.ErrWrongPassword` 包装为中文文案、对外壳发现集合做二次校验）；`internal/cli/play.go` 与全部 `Unlock:` 调用点（`internal/play/gate_test.go`、`picker_test.go`、`tests/integration/*`）改用新签名。
- [x] T8.11 文案精简（AC8.6）：登录页删 `hint`/`redline` 段落；索引页删「条目只显示密档文件名…」与脚注；播放页删环回说明句（目录树操作提示保留）。
- [x] T8.12 文档：`README.md` / `docs/manual.md`（多账户登录、`accounts/` 归置）、`CONTEXT.md`（新增「账户选择」术语）、`AGENTS.md`（登录页与会话、账户发现、文案红线）、`tests/e2e/REPORT.md`（US8 行）。

## 收尾

- [x] T6.1 `make test`（L1+L2）全绿（含 `-race`）。
- [x] T6.2 `make vet fmt` 干净。
- [x] T6.3 `docs/manual.md` 增补桌面版使用与分发。
- [x] T6.4 回流 `AGENTS.md`（本机环境/结构/分发）与 `specs/002/*`。
- [x] T6.5 `make dist` 产出可双击运行的 `.app`，供用户试用。

---

## 对齐记录（实现与计划的差异，以实现为准）

1. **参数名 `SoftLock` 而非 `CloseOnLock`**：`play.Options` / `play.LibraryOptions` 用 `SoftLock bool`，语义为「锁定时不关停、回到登录页」。CLI 不传（零值 = 关停，行为不变），外壳传 `true`。两条相反的锁定语义共用一份实现，红线不会出现两份。
2. **版本号落在 `internal/version` 而非 `internal/cli`**：CLI 与外壳都要用，放在独立包内可被 `desktop/` 直接 import（`desktop/` 不能 import `internal/cli` 而不引入 cobra 与终端依赖）。
3. **门控测试集中在 `internal/play/gate_test.go`**，未新建 `core_test.go`。
4. **`-race` 暴露并修复两处真实竞态**（非测试问题）：
   - `vault.Reader`：锁定/关闭时清零文件密钥与正在进行的解密读取之间无同步 → 在 `Reader` 内加 `sync.RWMutex`（`ReadAt` 持读锁、`Wipe` 持写锁）。
   - `play.Library`：`wipe()` 清零私钥与媒体请求「锁内取私钥启封」之间无同步 → 统一用 `l.mu` 串行化。
5. **`desktopcfg` 内部 `ensureLibraryDir`**：目标目录不存在时创建（含 `~/.safemedia/library` 默认库），否则首次双击必然报错。
6. **启动参数在 macOS 上不作依赖**（实测偏差，AC2.1）：`open -n -a safemedia.app --args <dir>` 与 `open -a <app> --args <dir>` 都**不把参数送达进程**（新实例的登录页仍显示默认库；另用裸二进制探针复核，argv 为空）。代码保留 argv 支持（仅**终端直接运行二进制**时有效，如 `make desktop-run ARGS=`），但**选库以 `desktop.json` 为准**；README / `docs/manual.md` / `dist` 的 `说明.txt` 中原先承诺的「拖放或 `open --args` 换库」已删除。→ 回流 `AGENTS.md` 易错点 8。
7. **`.gitignore` 用根锚定 `/dist/`**：写成 `dist/` 会同时忽略 `desktop/frontend/dist/index.html`——它是 `//go:embed` 的**源码引导页**，被忽略则全新 clone 构建出的 `.app` 白屏。
8. **`ditto -c -k --norsrc`** 而非 `--sequesterRsrc`：后者会在 zip 里写入 `__MACOSX/` 冗余条目。
9. **页面外观重做（AC7.9，用户反馈「按钮太难看 + 出现超链接就露馅」）**：`page.go` 新增 `baseCSS`（四页共用：按钮/列表行/输入框/工具栏），导航与命令一律用 `<button>` 或 `<a class="btn">`，列表行 `<a class="row">` 整行可点；工具栏改为 `header.toolbar > .tbwrap`（与正文列同宽 `--content:840px`，标题与正文左对齐）；目录行加内联 SVG 文件夹图标。文案顺带去 CLI 化：删掉「关闭命令行窗口即结束会话」（外壳里无 CLI 窗口），改为「明文不落盘」。守护见 `internal/play/page_style_test.go`（T7.15）。
10. **可视化外审流程（可复用）**：临时夹具把四页写到 `/tmp/…`（`go test -run TestZZVisualDump`）→ headless Chrome 截图（必须 `--no-sandbox --user-data-dir=<临时>`，本机无沙箱权限）→ 目视比对「是否像原生 app」。夹具用完即删，不入库。