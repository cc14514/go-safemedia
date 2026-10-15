# 001 go-safemedia — Tasks

规格：`specs/001/spec.md` · 方案：`specs/001/plan.md` · 术语：`CONTEXT.md`
规则（宪法 + AGENTS.md）：每个 User Story 先写测试（RED）再实现（GREEN）；L1 单测在各包 `*_test.go`，L2 集成在 `tests/integration/`，L3 E2E 在 `tests/e2e/REPORT.md`。里程碑 M1–M5 顺序推进，每个里程碑以「make test 全绿」为门禁。

---

## M1 — 骨架 + 账户（US1、US2 的账户部分）

- [x] T1.1 (RED) `internal/account` 单测：
  - 助记词生成：12/24 词、BIP-39 校验通过、两次生成不同；
  - 确定性派生：同一助记词 + BIP-44 `m/44'/60'/0'/0/0` → 同一私钥/地址；
  - keystore v3 存/取往返：正确口令解开、错误口令报 `ErrWrongPassphrase`、篡改密文报 MAC 校验失败；
  - 恢复：助记词重建 → 地址与原账户一致；恢复 keystore 用独立恢复口令可重置登录口令。
- [x] T1.2 (GREEN) `internal/account`：`Generate`（BIP-39 entropy→mnemonic→seed→BIP-44→secp256k1）、`Keystore.Save/Open`（go-ethereum keystore v3，scrypt N≥2^17）、`RecoverFromMnemonic`、`ExportRecoveryKeystore`；口令字节用后清零。
- [x] T1.3 CLI：`safemedia account create`（强制抄写+复述确认助记词、红线警告）、`account export-recovery`、`account recover`；cobra 接线；`--account` 覆盖默认路径 `~/.safemedia/`。
- [x] T1.4 门禁：`go test ./...` 全绿；手动冒烟（create→export-recovery→recover 三命令走通）。

## M2 — .svid 容器 + 封存（US3）

- [x] T2.1 (RED) `internal/vault` 单测：
  - 头部 156B 编解码往返（magic/version/cipher_suite/chunk_size/chunk_count/plaintext_size/ECIES 三元组）；
  - Seal→Open 字节级往返（空文件、1B、恰好一块、跨多块、非整除尾块）；
  - 随机访问：任意 offset Range 读取与全量解密一致（拖进度条正确性）；
  - 篡改检测：改头部/改任一密文块/截断 → 明确报错且不崩溃；
  - 错账户：用 B 账户私钥 ECIES 解 A 的密档 → 报「不属于本账户」；
  - 白名单：mp4/mov(ftyp)/webm(EBML) 通过，mkv/avi/未知 magic 拒绝。
- [x] T2.2 (GREEN) `internal/vault`：`Format`（头部布局常量）、`Seal`（每文件随机 file key，ECIES 封装到账户公钥，4MiB 分块 AES-256-GCM，每块随机 nonce）、`Open`+`ReadAt`（隐式索引定位块）。
- [x] T2.3 CLI：`safemedia seal <视频文件|目录> -o <文件|目录>`（目录递归、相对子路径在输出目录重建、跳过隐藏/非视频/已有 .svid）；封存前 magic 白名单校验；进度输出。
- [x] T2.4 门禁：L1 全绿 + `tests/integration` 封存往返用例（L2 起步）。

## M3 — 播放服务 + 单文件播放（US4、US8）

- [x] T3.1 (RED) `internal/play` 单测：HTTP 206 Range 语义、Content-Type 嗅探（ftyp→video/mp4、EBML→video/webm）、只绑 127.0.0.1、空闲 30min 触发锁定、SIGINT 清零密钥并关流。
- [x] T3.2 (GREEN) `internal/play`：本地 HTTP 服务（Range→vault.ReadAt）、登录态内存管理（私钥/file key 驻留内存，超时/退出清零）、播放页 HTML（`<video>` + canplay 检测失败提示 mpv）、自动开浏览器。
- [x] T3.3 CLI：`safemedia play <vault>`：登录（口令 x/term 无回显）→ 起服务 → 开浏览器 → 前台守护到锁定/中断。
- [x] T3.4 (L2) `tests/integration`：seal→play→curl Range 拉流→字节比对原片；错误口令拒绝；30min 空闲（用可注入时钟缩短）自动停。
- [x] T3.5 门禁：L1+L2 全绿；手动冒烟（真实 mp4 浏览器拖进度条）。

## M4 — 目录索引 + mpv（US5、US6）

- [x] T4.1 (RED/GREEN) `play <dir>`：目录**递归**扫描 .svid → 按目录树分组的索引页（仅文件名+大小，不泄元数据）→ 点击逐项播放；播放页左侧常驻目录树可切换片源。
- [x] T4.2 (RED/GREEN) `--player mpv`：探测 mpv 存在性，把流 URL 交给 mpv；HEVC-in-Firefox 场景由此逃生。
- [x] T4.3 (L2) 集成：目录模式索引与目录树/侧栏正确（AC5.1–AC5.5）；mpv 模式参数拼接正确（mpv 不存在时报错清晰）。

## M5 — 恢复 UX + 搬运 + E2E（US2 完整、US7）

- [x] T5.1 (RED/GREEN) 恢复全流程打磨：丢口令（恢复 keystore 重置）、丢账户文件（助记词重建）；重建后旧密档可播（公钥不变）。
- [x] T5.2 (L2) 搬运：keystore+vault 复制到干净 HOME（模拟另一台机）→ recover/import → 可播；AC7.x 全覆盖。
- [x] T5.3 (L3) `tests/e2e`：真实浏览器全场景（create→seal→play→拖动→锁定→恢复→搬运），产出 REPORT.md（Pre/Post 对比表）；缺陷回流 AGENTS.md。
- [x] T5.4 门禁：L3 全 PASS 才可交付 QA（本次 32 PASS / 0 FAIL，含真实 mp4/mov/webm 样本 + 无头 Chrome 对照）。
- [ ] T5.5 人工验收（需外部设备，不阻塞交付）：iPhone 实机 .mov（H.264/HEVC）+ Firefox HEVC 提示、mpv 实播、双机 U 盘搬运——见 REPORT.md「待人工验证」。

---

## 横切事项

- 每里程碑结束跑 `make test`（Makefile 于 T1.2 建立：build/test/lint 三目标）。
- 依赖仅 plan §13 白名单：go-ethereum(crypto/keystore)、go-bip39、btcutil/hdkeychain、cobra、x/term、x/crypto(hkdf/scrypt)。
- 宪法红线复查：明文不落盘、服务只绑 127.0.0.1、无遥测、新建账户不导入。
