# AGENTS.md — go-safemedia

面向在此仓库工作的编码代理。人类请看 [`README.md`](README.md) 与手册 [`docs/manual.md`](docs/manual.md)。

## 这是什么

把视频封存成 `.svid` 密档（4 MiB 分块 AES-256-GCM），持有账户私钥者才能启封播放。纯 Go、离线、无 DRM。
威胁模型 → `docs/requirements.md`（含 FR-6 桌面外壳）；验收场景（测试用例唯一来源）→ `specs/001/spec.md`（密档与播放）、`specs/002/spec.md`（桌面外壳）；术语表 → `CONTEXT.md`；桌面外壳选型 → `docs/adr/0001-desktop-shell-wails-loopback.md`。

## 开发范式（强制，继承全局 AGENTS.md）

规格先行 + 测试先行，不可跳步：`docs/requirements.md` → `specs/001/spec.md`（用户评审门）→ `plan.md` → `tasks.md` → 实现。项目宪法：`.specify/memory/constitution.md`。`tasks.md` 中测试子任务必须排在实现之前。
三层门禁：L1 单元（各包 `*_test.go`）→ L2 集成（`tests/integration/`）→ L3 E2E（`tests/e2e/`，产出 `REPORT.md` 的 Pre/Post 对比表）。**L2 不过不合并，L3 不过不交付 QA。**

## 常用命令

```sh
make build          # -> bin/safemedia（go 1.26，见 go.mod；README 的「1.25+」已过时）
make test           # L1 + L2（= internal/... cmd/... desktop/... 与 tests/integration/...）
make test-l3        # L3 E2E，产出 tests/e2e/REPORT.md（必须全 PASS）
make vet fmt smoke  # go vet ./... / gofmt -l -w . / ./bin/safemedia --help
go test ./internal/vault -run TestSealOpen   # 跑单个包 / 单个测试
go test -race ./internal/play/...            # 门控/锁定改动后必跑（清密钥与解密读的竞争）

# 桌面外壳与分发（首版仅 macOS/arm64）
make desktop                       # wails build -s -> desktop/build/bin/safemedia.app（需 wails CLI + Xcode CLT）
make dist VERSION=0.2.0            # -> dist/safemedia_<ver>_darwin-arm64.zip（.app + CLI + 说明.txt）
make release VERSION=0.2.0         # gh release create/upload（需 gh 已登录）
make desktop-test                  # 只跑外壳相关单测，不需要 wails CLI

# L3 真实样本（fetch-samples.sh 需联网下载 4 个样本）
tests/e2e/fetch-samples.sh /tmp/safemedia-samples
SAFEMEDIA_E2E_SAMPLES=/tmp/safemedia-samples make test-l3
SAFEMEDIA_E2E_NO_BROWSER=1 make test-l3      # 无 Chrome 时跳过真实解码校验
```

## 结构

```
cmd/safemedia/       入口（只调 cli.Execute()）
internal/account/    BIP-39/44 派生、keystore v3、口令强度
internal/vault/      format.go 头部布局 / wrap.go 文件密钥封装 / vault.go Seal+Open+ReadAt / whitelist.go 容器嗅探
internal/play/       core.go 单文件与目录共用的生命周期 / play.go 单文件 / library.go 目录模式 / page.go 模板
internal/cli/        cobra 命令（中文输出）、prompt.go 交互、exit.go 退出码
internal/version/    构建时注入的版本号（CLI 与外壳共用，见 Makefile 的 LDFLAGS）
internal/desktopcfg/ 外壳的目标解析（启动参数 > desktop.json > 默认库）与配置读写
desktop/             Wails v2 外壳：main.go（接线）+ frontend/dist/index.html（引导页）+ wails.json
tests/integration/   L2 黑盒/灰盒（进程内执行 CLI；desktop_flow_test.go 覆盖外壳链路）
tests/e2e/           L3 端到端 + REPORT.md + fetch-samples.sh
```

## 硬约束（改代码前先读）

- **明文不落盘**：解密只在内存与 `127.0.0.1` 响应里；不得引入临时明文文件。
- **只绑环回**：播放服务只监听 `127.0.0.1`，不得绑 `0.0.0.0`；媒体 URL 带随机 token（防本机其它网页盲扫 localhost 偷流）。
- **只新建账户**：不实现「导入私钥/助记词」，文档与提示保持红线（见 `internal/cli/root.go` 的 `redlineWarning`）。
- **格式兼容**：`.svid` v1 头部 156B 定长、4 MiB 分块（`internal/vault/format.go`；布局见 `specs/001/plan.md` §4）。改布局 = 升 version，不得就地改 v1。
- **依赖白名单**（按实际直接 import 列出）：go-ethereum `crypto`、go-bip39、btcsuite/btcd 的 `btcutil/hdkeychain` 与 `chaincfg`、cobra、`golang.org/x/term`、`golang.org/x/crypto`（hkdf/scrypt）。新增依赖须回评审门。
- **引擎侧零额外依赖**：Wails v2 **只许出现在 `desktop/`**；`internal/...` 不得 import 它（登录页复用既有的 `html/template`）。
- **不 import go-ethereum `crypto/ecies`**：账户密钥来自 btcsuite/btcec 派生的 secp256k1，geth ecies 的曲线类型相等检查会拒绝跨实现调用；文件密钥封装用标准原语显式组合（ECDH + HKDF-SHA256 + AES-256-GCM），见 `internal/vault/wrap.go`。
- **中文**：CLI 与播放页用户可见文案一律中文；术语用「密档/封存/启封」，禁用词见 `CONTEXT.md` 的 _Avoid_ 列。

## 退出码约定（`internal/cli/exit.go`，plan.md §7）

`0` 成功 · `1` 用户/输入错误（口令错、白名单外、文件不存在、不属于本账户）· `2` 数据错误（非密档/头部损坏/被篡改）· `3` 环境错误（如 mpv 缺失）。
`ExitCodeOf` 先看错误是否带 `ExitCode()`，否则 `vault.ErrNotAVault`/`ErrTampered`/`ErrUnsupportedVersion` → 2，其余 → 1。E2E 有用例锁定这些映射，改错误归类会打破它们。

## 测试要点

- **L2 在进程内跑 CLI**：用 `cli.NewRootCmd()` 执行命令，注入脚本化 `cli.Prompt`、临时调低 `cli.ScryptN`（仅影响测试进程）；不要为测试另建入口。
- **L3 起真实服务**，做 Range 逐字节比对、篡改检测、锁定清零、搬运；有 Chrome 时用无头 Chrome 做「原文件 vs 密档流」解码对照。
- 无真实样本时 L3 退回合成 mp4。`fetch-samples.sh` 联网下载 4 个样本，其中 filesamples 的 `sample_640x360.mp4/.mov` 连原文件在 Chrome 里也不可解码（正常，走 mpv 提示路径）——不要误判为解密流缺陷。

## 易错点（踩过的坑，别再踩）

1. **`cipher.AEAD.Open(dst, ...)` 返回 `dst‖明文` 而非纯明文**：曾误传 `enc[:12]`（nonce）作 dst，认证通过但内容全错。解密一律传 `nil`。
2. **跨实现 secp256k1 不能混用**：btcec 与 go-ethereum 的 `S256()` 是不同 Go 类型、接口比较不相等；ECDH 双方必须用同一曲线对象（本仓库统一用 `priv.Curve` + `ScalarMult`/`ScalarBaseMult`）。`ecdsa.PrivateKey` 字段可直接构造，`ToECDSA()` 出来的 Curve 是 btcec 实现，别假设可与 stdlib/其他库曲线互换。
3. **keystore/封装测试用 scrypt N=1<<10**（生产 2^17），否则单测慢到不可用。
4. **`html/template`：`{{template "x" .}}` 里的 `$` 是「本次调用的数据」，不是 `ExecuteTemplate` 的根数据**。递归模板必须显式传上下文（本仓库 `treeCtx` + `nodeWith`），否则 `$.Field` 解析到子节点并报 `can't evaluate field`。
5. **不要 `_ = tmpl.Execute(...)`**：模板执行错误会静默把页面截断成半页（曾致播放页只渲染一半且无报错）。渲染失败必须可见（`renderError`）。
6. **桌面外壳没有任何自动解锁路径，但起真实 `.app` 做冒烟测试会在用户屏幕上弹窗**：用户很可能直接在里面输入口令，此时观察到的「已解锁」是**用户操作**，不是测试结果（曾据此误判为 bug、排查数轮）。要断言解锁语义就用 `tests/integration/desktop_flow_test.go` 的进程内灰盒；起真 `.app` 只用来确认「窗口能看到页面」。
7. **清密钥/换库根必须与读取互斥**：`vault.Reader.ReadAt`（解密读）与 `Wipe`（锁定时清零文件密钥）、`play.Library` 的「锁内取私钥启封」与 `wipe()`、`handleIndex` 读 `tree/root` 与 `SetRoot` 的原子替换，都必须串行化；漏一处 `-race` 就报竞争（软锁定「边流边清零」与并发换库根必现）。改门控/锁定/库根后跑 `go test -race ./internal/play/...`。
8. **桌面 `.app` 收不到启动参数**：macOS 经 LaunchServices 启动（双击 / `open`）时 argv 不送达（实测 macOS 27：`open -n -a safemedia.app --args <dir>` 的新实例仍用默认库）。**选库以界面里的「选择密档库」为准**（会写回 `~/.safemedia/desktop.json`），也支持手写该文件的 `library`；argv 只在终端直接运行二进制时有效（`make desktop-run ARGS=`）。别在文档里承诺「拖放/`open --args` 换库」。

## 目录树 / 播放页（目录模式）要点

- `ScanLibrary` 递归扫描（`filepath.WalkDir`），跳过隐藏项、符号链接与白名单外文件；`Entry.Rel` 为库内相对路径（`Label` 即它）。
- 容器按**明文首字节嗅探**（`DetectContainer`：`ftyp` brand `"qt  "` → mov；EBML DocType `webm`/`matroska` 区分，mkv 明确拒绝），不信任文件扩展名。
- 索引页与播放页侧栏**共用同一棵树与同一套模板**（`treeCSS` + `treeTmpl`）；树在 `Library` 构造时扫描一次，追加密档后需重跑 `play` 才可见。
- 播放页 `<aside class="tree">` 常驻左侧（`position:sticky; height:100vh`），`li.file.active` 高亮并 `scrollIntoView`；≤820px 移到播放器上方；全屏只 `<video>` 全屏。

## 桌面外壳（`desktop/`，specs/002）

- 外壳**零 UI、零逻辑**：只做「解账户（登录页回调）→ 起 loopback 服务 → 把系统 webview 指向它」；目录树/播放页/空闲锁定全部复用 `internal/play` 的既有页面。引导页 `desktop/frontend/dist/index.html` 由 `assetserver.Options.Middleware` 把 `__BOOT__` 换成 JSON（url 或 error），出错时窗口里显示中文原因而不是白屏。
- **锁定语义靠 `SoftLock`**：外壳传 `true`（锁定回登录页、端口存活、可再次解锁）；CLI 不传（零值 = 锁定时关停，行为与改动前一致）。改 `internal/play` 门控后**必须同时验证两条语义**（CLI 回归 + 外壳链路）。
- 登录流程：`GET /login`（表单）→ `POST /login` → 会话 cookie `sv_session`；会话启用时内容路由校验该 cookie，锁定态访问媒体路径 403（不 panic）。
- **库根在页面里可换**（US7）：索引页 → `/library` 目录选择页（逐级浏览 + 每目录直含密档数）→ `POST /library` → `SetRoot` 规范路径、重扫、原子替换 root/tree/byID/order（`id` 重新分配）→ 回调 `LibraryOptions.OnLibraryChange`（外壳写 `desktop.json` 并保留 account 字段；CLI 不传该回调，选择只对本次会话有效）。非法路径**不改动任何状态**、在页面内显示中文原因；`~` 会展开、相对路径规范为绝对路径。锁定态一律回登录页（不枚举本机目录）。
- 分发：`make desktop` → `make dist`（`.app` + CLI 打成 zip）→ `make release`（`gh`）。首版仅 macOS/arm64、不签名不公证；arm64 上 Go 自动 ad-hoc 签名，用户只需 `xattr -dr com.apple.quarantine`。
- **明文红线在 webview 缓存上已验证**：WKWebView 用持久化数据仓库（`~/Library/Caches/<bundle-id>/WebKit`、`~/Library/WebKit/<bundle-id>`），但它尊重 `Cache-Control: no-store`——对照实验（可缓存对照有残留、引擎 `no-store` 响应与真实 mp4 流零残留）见 `tests/e2e/REPORT.md` 的 002 段。**改播放页/媒体响应头时不得去掉 `no-store`。**

## 本机环境（opencode 网关）

本机 opencode 走局域网 yfgw 网关的思考型模型，思维链无法关闭，重负载轮次会烧光 completion 预算 → **空正文**或工具参数生成不全（`tool.input-incomplete`）。因此：**一次只 write 一个大文件**（多个大文件同批必被截断）；先写正文再调用工具，不要在 reasoning 里起草大文件；手工构造的测试 JSON 先过解析器再下结论。codegraph MCP 在本机不可用（已 `disabled`），CLI `codegraph explore` 可用。

## 本机环境（工具链怪癖）

本机装了 WorkBuddy 的「brokered FS」shim，会**随机**让裸调用的核心工具以 `Brokered program policy check unavailable`（exit 13）失败，`npm` 更是直接崩溃（`tryBrokerFileTokenSync: connect failed: Connection refused`）。规避：核心工具用绝对路径（`/bin/ls`、`/usr/bin/tail`）；**不要依赖 npm** —— 桌面外壳的前端构建用 `wails build -s` 跳过。

## 交付门禁

- `make test`（L1+L2）全绿；`make test-l3` 生成 `tests/e2e/REPORT.md` 且**全 PASS**（优先带真实样本跑）。
- 发版用 `make dist VERSION=x.y.z` 产出可双击运行的 `.app`（Windows/签名公证不在首版范围）。
- 用户可见行为/命令/退出码变化 → 同步更新 `docs/manual.md`。
- 新缺陷 → 记录根因/影响范围/修复文件，**回流本文件「易错点」**；构建/测试/结构/约定变化 → 同步本文件与 `specs/001/*.md`。