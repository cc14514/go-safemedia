# AGENTS.md — go-safemedia

面向在此仓库工作的编码代理的上下文。人类请看 [`README.md`](README.md) 与使用手册 [`docs/manual.md`](docs/manual.md)。

## 这是什么

把视频封存成 `.svid` 密档（分块 AES-256-GCM），只有持有账户私钥的人能启封播放。纯 Go、离线、无 DRM。
威胁模型与不防什么 → `docs/requirements.md`；验收场景 → `specs/001/spec.md`。

## 开发范式（强制，继承全局 AGENTS.md）

规格先行 + 测试先行，不可跳步：

`docs/requirements.md` → `specs/001/spec.md`（用户评审门）→ `specs/001/plan.md` → `specs/001/tasks.md` → 实现。
项目宪法：`.specify/memory/constitution.md`（规格先行 / 测试门禁 / 安全底线 / 术语一致 / 依赖最小化 / 范围纪律）。
`spec.md` 的 Given/When/Then 是测试用例唯一来源；`tasks.md` 里测试子任务排在实现之前。

三层门禁：L1 单元（各包 `*_test.go`）→ L2 集成（`tests/integration/`）→ L3 E2E（`tests/e2e/`，产出 `tests/e2e/REPORT.md` 的 Pre/Post 对比表）。
L2 不过不合并，L3 不过不交付 QA。

## 常用命令

```sh
make build        # -> bin/safemedia
make test-l1      # go test ./internal/... ./cmd/...
make test-l2      # go test ./tests/integration/...
make test         # L1 + L2
make test-l3      # L3 E2E，产出 tests/e2e/REPORT.md（必须全 PASS）
make vet fmt smoke
tests/e2e/fetch-samples.sh /tmp/safemedia-samples   # 下载真实样本
SAFEMEDIA_E2E_SAMPLES=/tmp/safemedia-samples make test-l3
```

## 结构

```
cmd/safemedia/          入口
internal/account/       BIP-39/44 派生、keystore v3（scrypt N=2^17）、口令强度
internal/vault/         .svid 容器：format.go 头部布局 / wrap.go 文件密钥封装 / vault.go Seal+Open+ReadAt
internal/play/          环回 HTTP 服务、Range、空闲锁定、目录索引、mpv
internal/cli/           cobra 命令与终端交互（中文输出）
tests/integration/      L2 黑盒/灰盒集成
tests/e2e/              L3 端到端 + REPORT.md
```

## 硬约束（改代码前先读）

- **明文不落盘**：解密只在内存与 `127.0.0.1` 响应里；不得引入临时明文文件。
- **只绑环回**：播放服务只监听 `127.0.0.1`，不得绑 `0.0.0.0`。
- **只新建账户**：不实现「导入私钥/助记词」，文档与提示中保持红线。
- **格式兼容**：`.svid` v1 头部 156B 定长、布局见 `specs/001/plan.md` §4 与 `internal/vault/format.go`；改布局 = 升 version，不得就地改 v1。
- **依赖白名单**：go-ethereum(crypto)、go-bip39、btcutil/hdkeychain、cobra、golang.org/x/term、golang.org/x/crypto。新增依赖须回评审门。
- **不 import go-ethereum `crypto/ecies`**：账户密钥来自 btcsuite/btcec 的 secp256k1 曲线对象，geth ecies 的类型化曲线相等性检查会拒绝跨实现调用；文件密钥封装用标准原语显式组合（ECDH + HKDF-SHA256 + AES-256-GCM），见 `internal/vault/wrap.go`。
- **中文**：CLI 与播放页的用户可见文案一律中文；术语用「密档/封存/启封」（见 `CONTEXT.md`，勿用「转换/转码/加解密文件名」等）。

## 易错点（踩过的坑，别再踩）

1. **`cipher.AEAD.Open` 的返回值含 dst 前缀**。`Open(dst, nonce, ct, ad)` 返回 `dst ‖ 明文`，不是纯明文。曾误传 `enc[:12]`（nonce）作 dst，导致返回 `nonce+明文`、认证通过但内容全错。解密时一律传 `nil`。
2. **跨曲线实现的 secp256k1 不能混用**：btcec 的 `S256()` 与 go-ethereum 的 `S256()` 是不同 Go 类型，接口比较不相等；ECDH 双方必须用同一曲线对象（本仓库统一用账户密钥上的 `priv.Curve`，并用 `curve.ScalarMult`/`ScalarBaseMult`）。
3. **`ecdsa.PrivateKey` 字段可直接构造**，`ToECDSA()` 出来的 Curve 是 btcec 的实现；不要假设它与 stdlib/其他库的曲线可互换。
4. **keystore/封装测试用 scrypt N=1<<10**（生产 2^17），否则单测慢到不可用。
5. **写大文件要一个一个来**：见「与模型/网关相关的注意事项」。
6. **Go 1.25+ 的 `capacity`/多返回值改动**低风险，但 `go vet` 必须过；`gofmt -w` 在每个里程碑前跑。
7. **`html/template`：`{{template "x" .}}` 里的 `$` 是「本次调用的数据」，不是 `ExecuteTemplate` 的根数据**。递归模板（如目录树）必须显式传上下文（本仓库用 `treeCtx` + `nodeWith`），否则 `$.SomeField` 会解析到子节点上并报 `can't evaluate field`。
8. **不要 `_ = tmpl.Execute(...)`**：模板执行错误会静默把页面截断成半页（曾导致播放页只渲染到一半、无任何报错）。渲染失败必须可见（`renderError`）。

## 目录树（目录模式）实现要点

- `ScanLibrary` **递归**扫描目录（`filepath.WalkDir`），跳过隐藏项（`.` 开头）、符号链接（避免列出库外密档）、非 `.svid`；`Entry.Rel` 是库内相对路径，`Label` 即它。
- `buildTree(entries)` 按 `Rel` 拆目录生成 `treeNode`；索引页与播放页侧栏**共用同一棵树与同一套模板**（`treeCSS` + `treeTmpl`）。
- 树在启动时扫描一次（`Library` 构造时），追加密档后需重跑 `play` 才可见。
- 播放页：`<aside class="tree">` 常驻左侧（`position:sticky; height:100vh`），`li.file.active` 高亮当前条目并 `scrollIntoView`；≤820px 移到播放器上方；全屏时仅 `<video>` 全屏，目录树自然让出。

## 与模型/网关相关的注意事项（本机环境）

本机 opencode 走局域网 yfgw 网关（DashScope 兼容），其思考型模型（glm-5.3 等）思维链无法关闭，重负载轮次会烧光 completion 预算 → **返回空正文**（表现为「卡住」）或工具参数生成不全（`tool.input-incomplete`）。因此：

- 每轮只产出一个大产物；**一次只 write 一个大文件**，多个大文件同批 write 必被截断。
- 先写正文、再调用工具；不要在 reasoning 里长时间起草大文件。
- 手工构造的测试 JSON/参数先过解析器再下结论。
- codegraph MCP 在本机 opencode 下无法连接（`mcp.servers.codegraph.disabled=true`）；CLI `codegraph explore` 可用，仓库已 `codegraph init`。

## 交付门禁

- `make test`（L1+L2）全绿；
- `make test-l3` 生成 `tests/e2e/REPORT.md` 且 **全 PASS**（含真实样本时优先带样本跑）；
- 用户可见行为/命令/退出码变化时，同步更新 `docs/manual.md`；
- 新缺陷 → 记录根因/影响范围/修复文件，**回流本文件的「易错点」**；
- 变更涉及构建/测试/结构/约定时，同步更新本文件与 `specs/001/*.md`。
