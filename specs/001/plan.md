# specs/001 Plan — go-safemedia MVP 加密视频播放器

- 状态: 草案，待评审
- 上游: docs/requirements.md · specs/001/spec.md（已通过评审门）
- 宪法: .specify/memory/constitution.md v1.0

## 1. Technical Context

| 项 | 值 |
|---|---|
| 语言 | Go 1.25（本机 go1.25.4 darwin/arm64） |
| 模块路径 | `github.com/cc14514/go-safemedia`（仓库改名后生效；改名前本地 build 不受影响） |
| 二进制名 | `safemedia` |
| 平台 | macOS（主）、Linux（次）；仅本机 CLI |
| 依赖 | go-ethereum/crypto（secp256k1、keccak-256、scrypt）、tyler-smith/go-bip39、btcsuite hdkeychain（BIP-44 派生）、spf13/cobra、golang.org/x/term（无回显密码输入）与 golang.org/x/crypto（hkdf）；其余全部标准库 |
| 网络 | 仅 127.0.0.1 监听；无任何出站连接 |

## 2. Constitution Check

| 条款 | 检查 | 结果 |
|---|---|---|
| I 规格先行 | spec.md 已过评审门；本 plan 评审后写 tasks.md 再动代码 | ✅ |
| II 测试门禁 | §10 给出 L1/L2/L3 到 AC 的映射；tasks.md 中测试任务排在实现前 | ✅ |
| III 安全底线 | §4–§6 落实明文不落盘、127.0.0.1 白名单、密钥清零、scrypt N=2^17、每块随机 nonce | ✅ |
| IV 术语一致 | 包名/标识符用 vault、seal、unseal、account、keystore、chunk、playback server | ✅ |
| V 依赖最小化 | 5 个外部依赖全部在 §13 写明理由 | ✅ |
| VI 范围纪律 | 无 DRM/转码/GUI/云端；mov→mp4 转封装明确二期 | ✅ |

## 3. 架构总览

```
cmd/safemedia/          main.go（cobra 根命令）
internal/cli/           子命令: account create|recover|export-recovery, seal, play
internal/account/       密钥派生(BIP-39→BIP-44 m/44'/60'/0'/0/0)、keystore v3 读写、恢复
internal/vault/         .svid 格式: header 编解码、Seal(Reader→Writer)、Open(随机访问 ReaderAt)
internal/play/          Playback Server(127.0.0.1 HTTP Range)、索引页、浏览器/mpv 拉起、空闲锁定、清零
tests/integration/      L2
tests/e2e/              L3 + REPORT.md
```

数据流（播放）：`play` → 读 keystore + 密码 → 私钥入内存 → ECIES 解出文件密钥 → HTTP Range 请求按块解密 → 响应流 → 浏览器 `<video>` / mpv。明文只存在于内存与 loopback 响应中。

## 4. .svid 容器格式（v1）

**Header**（定长 156 字节，小端）：

| 偏移 | 长度 | 字段 |
|---|---|---|
| 0 | 4 | magic `SVID` |
| 4 | 1 | version = 1 |
| 5 | 1 | cipher suite = 1（ECIES 构造：secp256k1 ECDH + HKDF-SHA256 + AES-256-GCM） |
| 6 | 1 | 容器提示 = 1:mp4 / 2:webm / 3:mov（仅用于 Content-Type 选择，播放前仍以明文嗅探为准） |
| 7 | 4 | chunk_size = 4194304（4 MiB，u32） |
| 11 | 8 | chunk_count（u64） |
| 19 | 8 | plaintext_size（u64，用于 Content-Length 与末块长度） |
| 27 | 65 | 临时公钥（uncompressed） |
| 92 | 32 | 封装后的文件密钥密文（AES-256-GCM） |
| 124 | 16 | 文件密钥封装的 GCM 认证标签 |
| 140 | 12 | 文件密钥封装的 nonce |
| 152 | 4 | 保留（0，为二期显式索引留位） |

`plaintext_size` 为必需字段（Range 服务需要总长），它相对密文体积不增加实质信息泄露（密文大小≈明文大小），与 AC3.5 不冲突（AC3.5 禁止的是文件名/时长/分辨率）。**不存账户地址**：播放时直接尝试解封文件密钥，GCM 认证失败即报“该密档不属于当前账户”（支撑 AC7.2）。头部自洽性在解析时校验（`chunk_count` 必须等于由 `plaintext_size`/`chunk_size` 推出的块数，否则按损坏拒绝）。

**Body**：chunk_count 个块顺序排列，每块 = `nonce(12B 随机) ‖ ciphertext(≤4 MiB) ‖ GCM tag(16B)`。块长可算术推导（末块短），任意偏移 O(1) 定位 → **隐式分块索引**（chunk_size+chunk_count 即索引；细化 FR-2.2 措辞，不存显式索引表）。每块 nonce 独立随机：同一文件密钥下重复概率可忽略（birthday bound ≫ 单文件块数）。

**封装（实现说明）**：文件密钥 32B 随机生成/密档。封装按教科书 ECIES 构造显式组合：临时 secp256k1 密钥对 → 与账户公钥做 ECDH（`curve.ScalarMult`，取 x 坐标）→ HKDF-SHA256 派生 32B 封装密钥 → AES-256-GCM 加密文件密钥（tag 16B）。
**为何不用 go-ethereum `crypto/ecies`**：本仓库的账户私钥派生自 btcsuite/btcec 的 secp256k1 曲线对象，而 geth `crypto/ecies` 的类型化 API 会对曲线做指针相等性检查、且其 `GenerateShared` 直接返回原始 ECDH 坐标（不含 KDF）；跨曲线实现调用会以「invalid curve」拒绝。改用标准原语显式组合（ECDH + HKDF-SHA256 + AES-256-GCM）后，只依赖 `go-ethereum/crypto` 的 secp256k1 常量，行为等价、可读、可测（见 `internal/vault/wrap.go` 与 `wrap_test.go`）。

## 5. 账户与密码学

- **创建**：BIP-39 生成 12 词（可选 24）→ seed（空 passphrase）→ BIP-44 `m/44'/60'/0'/0/0` 派生 secp256k1 私钥 → go-ethereum keystore v3 JSON（scrypt，StandardScryptN=2^17）写 `~/.safemedia/keystore.json`。与 MetaMask 导入同一助记词得到同一地址（“eth 处理方式”的完整兑现）。
- **助记词确认**（AC1.1）：终端分词显示，随机抽 3 个位置要求输入对应单词，全对才写盘；写盘前中断不留任何文件。
- **恢复**：助记词 → 同路径重派生（AC2.1/2.2，校验和错误即拒绝）；或恢复 keystore（独立恢复口令的第二份 v3 封装，AC2.3–2.5）→ 重设登录密码。
- **红线**（AC1.3/FR-1.3）：无导入私钥/助记词命令；文档与创建提示中明示。

## 6. 播放服务（Playback Server）

- 监听 `127.0.0.1:0`（随机端口）；单文件模式路由 `/stream`；目录模式路由 `/`（索引页）+ `/v/<i>/stream`。
- HTTP：支持 `Range`（AC4.2 seek）；`Content-Type` 按解密后明文首 12 字节嗅探（`ftyp`→video/mp4；`1A45DFA3`→video/webm），容器提示仅作 fallback。
- 索引页：纯静态 HTML，列出目录下 .svid 文件名（AC5.1–5.3，无额外元数据）。
- 播放页：`<video controls autoplay>` + JS 监听 `video.error`/长时间 `stalled` → 页内提示改用 `--player mpv`（AC4.5）。
- 拉起：macOS `open` / Linux `xdg-open`；`--player mpv` 时改拉 `mpv http://127.0.0.1:<port>/stream`（mpv 不存在→明确报错，AC6.2）。
- **锁定**（US8）：SIGINT/SIGTERM/EOF → 关闭监听、`runtime.KeepAlive` 后对私钥/文件密钥字节切片显式清零（AC8.1）；30 分钟无请求 → 自动关停（AC8.2）。
- **篡改检测**（AC4.4）：GCM open 失败 → 该 Range 请求返回 500 + 页面错误文案，进程不崩溃。

## 7. CLI 接口

```
safemedia account create   [--account <path>] [--words 12|24]
safemedia account recover  [--account <path>] [--recovery <file>]
safemedia account export-recovery [--account <path>] [-o <file>]
safemedia seal <video> -o <out.svid> [--account <path>]
safemedia play <vault.svid|dir> [--account <path>] [--player browser|mpv]
```

退出码：0 成功；1 用户错误（密码错/白名单外/文件不存在）；2 数据错误（篡改/非法头部）；3 环境错误（mpv 缺失）。浏览器拉起失败不退出：打印 URL 供手动访问，服务继续运行（最终按锁定/中断的正常退出码返回）。seal 中断（AC3.6）：直接写目标 .svid（无明文临时文件），中断只可能留下不完整密档，由用户删除或重跑覆盖。

## 8. 错误处理策略

| 场景 | 行为 | 退出码/HTTP |
|---|---|---|
| 密码错误（AC4.3） | 拒绝，提示重试或退出；错误信息不携带 keystore 内容 | 1 |
| 非 .svid / 头部损坏（AC4.6） | “不是密档文件或头部已损坏” | 2 |
| ECIES 解密失败（AC7.2） | “该密档不属于当前账户” | 1 |
| GCM 校验失败=篡改（AC4.4） | 播放中：该 Range 返回 500 + 页面完整性报错，进程不崩溃 | HTTP 500 |
| 白名单外容器（AC3.4） | 拒绝，列出支持格式，提示二期/`--player mpv` | 1 |
| mpv 不存在（AC6.2） | “未检测到 mpv” | 3 |
| 浏览器拉起失败 | 打印 URL 供手动访问，服务器保持运行 | 不退出（继续运行至锁定/中断） |
| 账户文件不存在 | 提示先 `account create` | 1 |
| 目标 keystore 已存在（AC1.3） | 拒绝覆盖，提示 `--account` | 1 |
| seal 中断（AC3.6） | 只可能留下不完整 .svid（直接写目标文件，无明文临时文件） | SIGINT |

## 9. 里程碑

| 里程碑 | 内容 | 覆盖 |
|---|---|---|
| M1 | go.mod、cobra 骨架、Makefile、internal/account（创建/恢复/导出）+ L1 测试 | US1、US2 |
| M2 | internal/vault 格式 + seal 命令 + 白名单 + L1/L2 | US3 |
| M3 | Playback Server（Range/嗅探/拉起/锁定/清零/空闲超时）+ L2 | US4、US8 |
| M4 | 目录索引页 + `--player mpv` + L2 | US5、US6 |
| M5 | E2E：浏览器实测、iPhone mov 实机、双机搬运、REPORT.md（Pre/Post 表）、README、易错点回流 AGENTS.md | US7、L3 |

## 10. 测试策略（三层门禁映射）

- **L1**（`internal/**/*_test.go`）：header 编解码往返；分块加解密往返 + 篡改必检出；隐式索引偏移计算（首/中/末块、末块短于 chunk_size）；容器白名单；BIP-39→BIP-44 派生确定性（固定助记词→固定地址）；助记词确认抽词错误拒绝；keystore 往返（测试用 LightScryptN 提速，生产 StandardScryptN，两处常量集中定义）。
- **L2**（`tests/integration/`）：seal→open→逐段 Range 读取，解密结果与原文逐字节一致（正向）；错误密码、非本账户密档、坏 magic、篡改块（错误路径）；httptest 驱动播放服务：206 Range、Content-Length、Content-Type 嗅探、索引页列表、空闲超时（注入短超时）、锁定后密钥字节断言全零；恢复三流程（AC2.1/2.3/2.4）。每个 Feature 完成前 L2 全绿（回归路径 = 已合并场景全部重跑）。
- **L3**（`tests/e2e/`）：真实浏览器（Chrome/Safari）播放 + 拖进度条；iPhone 实拍 .mov（H.264 与 HEVC 各一）；双机搬运离线播放；mpv 逃生实测；REPORT.md 含 Pre/Post 对比表格，全 PASS 才交付。

## 11. 风险与缓解

| 风险 | 缓解 |
|---|---|
| R1 HEVC mov 浏览器兼容不一（Firefox 不支持） | 嗅探 + 页面检测不可解码即提示 mpv（AC4.5）；L3 用实机文件验收 |
| R2 go-ethereum 传递依赖庞大 | 只 import `crypto` 子包（secp256k1 常量与 keystore 所需原语），**不 import `crypto/ecies`**；封装改为标准原语显式组合（ECDH + HKDF-SHA256 + AES-256-GCM）。若依赖树仍失控，降级方案：直接用 decred secp256k1 原语（工作量+1天），触发即回评审门 |
| R3 Go GC 下密钥清零是尽力而为（可能存在副本） | 显式清零 + 明文不落盘；威胁模型已声明不防内存提取，文档如实标注 |
| R4 Range 并发导致内存峰值 | 单观众场景限并发 Range ≤4，`sync.Pool` 复用 4 MiB 缓冲 |
| R5 仓库改名前 module path 与 remote 不一致 | tasks.md 的 M1 首任务 = 确认改名完成；过渡期 module 用改名后路径（本地 build 不依赖 remote） |

## 12. 开放问题

1. 仓库改名由用户执行（go-safe-dir → go-safemedia），M1 开工前完成即可。
2. 播放页文案语言：中文为主（与 CLI 提示一致），如需双语在 M3 评审时定。

## 13. Complexity Tracking

| 依赖/复杂度 | 理由 | 被拒的替代方案 |
|---|---|---|
| go-ethereum `crypto` | secp256k1 常量与 keccak/scrypt 之外的 keystore 原语、地址推导的久经考验实现 | 自写 keystore（密码学细节风险）；decred（无现成 keystore） |
| 标准原语组合的 ECIES（ECDH+HKDF+AES-GCM） | 与 btcec 派生密钥互通；无跨曲线实现冲突；仅用 `crypto/ecdh` 之外的公开 API 与 `x/crypto/hkdf` | go-ethereum `crypto/ecies`（曲线类型相等性检查会拒绝跨实现调用，且无内置 KDF） |
| tyler-smith/go-bip39 | BIP-39 事实标准，含校验和（AC2.2 依赖） | 自写词表派生（无收益） |
| btcsuite hdkeychain | BIP-44 `m/44'/60'/0'/0/0` 派生 → MetaMask 导入同助记词得同地址 | seed 前 32B 直接当私钥（不兼容钱包，放弃 eth 式卖点） |
| spf13/cobra | 多级子命令标准方案 | 标准库 flag（嵌套子命令样板多） |
| golang.org/x/term | 无回显密码输入 | stty hack（不可移植） |
| 隐式分块索引 | 定长块 O(1) 算术定位，头部仅 156B；version 字段为二期显式索引留位 | 显式索引表（MVP 无变长块需求） |

## 14. 评审门

- [ ] 确认 .svid v1 格式：156B 定长头部、不存账户地址（ECIES 失败即“不属于本账户”）、隐式分块索引
- [ ] 确认依赖清单与 R2 降级预案
- [ ] 确认里程碑 M1–M5 与改名依赖（R5）

通过后 → `specs/001/tasks.md`（每个 User Story 的测试子任务排在实现子任务之前）。
