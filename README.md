# go-safemedia

把一个视频**封存**成 `.svid` 密档，只有持有你账户私钥的人能**启封**播放——解密只在本机内存里发生，明文永不落盘。
用于「视频文件存在 U 盘/移动硬盘/网盘同步目录里，不想让捡到硬盘的人直接看到内容」的场景。

- 纯 Go，单二进制，**完全离线**：不上链、不联网、无遥测。
- 账户复用以太坊账户格式：BIP-39 助记词 → BIP-44 `m/44'/60'/0'/0/0` → secp256k1 → 标准 keystore v3（可用 MetaMask 等钱包打开验证）。
- 密档 `.svid` = 156 字节定长头部 + 4 MiB 分块 AES-256-GCM（每块独立随机 nonce，可随机访问，**拖进度条不需要从头解密**）。
- 播放走 `127.0.0.1` 环回 HTTP 流给系统浏览器 `<video>`；装了 mpv 可用 `--player mpv` 作全格式逃生通道。
- 另有一个**桌面版外壳**（macOS 首版）：一个窗口 + 系统 webview，内部复用同一套环回播放页面，双击即用，不需要终端。

## 威胁模型（说清楚什么防、什么不防）

| 防 | 不防 |
|---|---|
| **静态泄露**：密档文件被拷走 / 硬盘被捡到 → 没有账户私钥打不开 | **DRM / 防翻录**：播放时录屏、拍照、转发，本工具不做任何对抗 |
| **本机明文窥探**：解密只经环回端口，明文不落盘，播放期间磁盘上不产生视频明文 | **内存提取**：威胁模型不含「攻击者已能读你的进程内存」 |
| **搬运途中**：U 盘里的密档离开你的机器仍然安全 | **口令弱**：账户口令/助记词的强度由你自己负责 |

## 安装与构建

```sh
go build -o bin/safemedia ./cmd/safemedia     # 需要 Go 1.26+（见 go.mod）
# 或
make build
```

📖 **完整使用手册见 [`docs/manual.md`](docs/manual.md)**（安装、命令详解、备份与恢复、兼容性、故障排查、FAQ）。

## 快速开始

```sh
# 1) 新建账户（会显示 12 词助记词并要求抄写 + 抽查复述；账户文件默认 ~/.safemedia/keystore.json）
safemedia account create

# 2) 封存视频（不转码；mp4 / mov / webm）
safemedia seal ~/Movies/clip.mp4 -o ~/vaults/clip.mp4.svid   # 单个视频
safemedia seal ~/Movies -o ~/vaults                          # 整个目录（递归）→ 另一个目录

# 3) 播放（起本地服务 + 自动开浏览器；Ctrl-C 或空闲 30 分钟自动锁定）
safemedia play ~/vaults/clip.svid
safemedia play ~/vaults/                  # 目录索引（递归子目录，按目录树展示），点选播放
safemedia play ~/vaults/clip.svid --player mpv      # 交给 mpv（HEVC 等）
```

## 桌面版（macOS，首版）

不想开终端就用桌面版：一个原生窗口套系统 webview，内部仍是同一个环回引擎，播放页与目录树完全一致（首次进入是登录页，输入账户口令后即可浏览与播放；空闲 30 分钟自动回到登录页）。

登录页会在**下拉框里列出本机所有账户**（`~/.safemedia/*.json` 与 `~/.safemedia/accounts/*.json`；每项显示文件名 + 地址短形式），**必须选对账户并输入该账户的口令**才能解锁——选错账户不会解密任何内容，只会提示「口令错误或与所选账户不匹配」。

**从 Release 安装**（推荐）：

1. 下载 `safemedia_<版本>_darwin-arm64.zip` 并解压；
2. 首次双击 `safemedia.app` 若被 Gatekeeper 拦住（未签名/未公证），执行一次：

   ```sh
   xattr -dr com.apple.quarantine /path/to/safemedia.app
   ```

   然后重新双击即可。（首版不做签名与公证，仅 Apple Silicon；Intel 需自行从源码构建。）

**从源码构建**（需要 `wails` CLI 与 Xcode 命令行工具，不需要 Node）：

```sh
make desktop        # -> desktop/build/bin/safemedia.app
make desktop-run    # 构建并直接运行
```

**看哪个库**：打开后**在界面里选** —— 索引页点「选择密档库（换扫描目录）…」，像文件浏览器一样逐级进入目录（面包屑、上级、每个子目录显示直含密档数），点「使用此目录作为库根」立即按该目录的子目录结构重扫并展示；选择会被记住（写入 `~/.safemedia/desktop.json`），下次启动仍用它。

默认库是 `~/.safemedia/library`（不存在时自动创建，空库会提示「没有密档」）。也可以直接手写配置（适合脚本化部署）：

```json
{ "library": "/Users/you/vaults" }
```

配置文件里还可以加 `"account"` 指定别的账户文件（默认 `~/.safemedia/keystore.json`）；它同时决定登录页里默认选中的是哪个账户，其余账户只要放在 `~/.safemedia/accounts/` 下就会被自动列出。

**账户管理也在 app 里**：登录页点「账户管理」，可以**创建**（当场显示助记词并要求复述抽查）、**恢复**（助记词 / 恢复文件）、**删除**（需输该账户口令，文件移入回收目录、密档不动）。这些动作只在锁定态可用。命令行仍是等价的备用通道（`safemedia account create` / `account recover`，用 `--account ~/.safemedia/accounts/工作.json` 归置多个账户）。

> 从源码调试时可以直接带参数启动（`make desktop-run ARGS=~/vaults`）；但**双击启动的 `.app` 不会收到启动参数**（macOS 经 LaunchServices 启动时不送达），所以选库请用上面的配置文件。封存视频仍然用 CLI（`safemedia seal`）。

**发布**（维护者）：

```sh
make dist VERSION=0.2.0      # -> dist/safemedia_0.2.0_darwin-arm64.zip（.app + CLI）
make release VERSION=0.2.0   # 上传到 GitHub Release（需 gh 已登录）
```

## 账户与恢复（两条独立路径）

| 坏了什么 | 怎么救 |
|---|---|
| 忘了登录口令 | `safemedia account export-recovery` 事先导出的**恢复文件**（独立恢复口令）→ `account recover` 重置登录口令 |
| 丢了整个账户文件 | `safemedia account recover` 用 **12/24 词助记词**重建（派生路径固定，重建后地址不变 → 旧密档无需重新封存） |

**红线**：`go-safemedia` 只有「新建账户」，**没有导入私钥/助记词**的功能。请不要把持有真实资产的钱包私钥/助记词用在加密视频这件事上——密钥复用意味着一处失窃、两处全完。

## 子命令

```
safemedia account create            新建账户
safemedia account export-recovery   导出恢复文件
safemedia account recover           助记词重建 / 恢复文件重置口令
safemedia seal <video>...           封存为 .svid
safemedia play <vault|dir>          启封播放
safemedia version                   版本信息
```

全局 `--account <path>` 覆盖账户文件位置；`play` 另有 `--player browser|mpv`、`--idle 30m`、`--no-browser`。

## 开发（SDD + TDD）

规格先行：`docs/requirements.md` → `specs/001/spec.md`（验收场景，用户评审门）→ `specs/001/plan.md` → `specs/001/tasks.md`；项目宪法 `.specify/memory/constitution.md`。

```sh
make test-l1     # L1 单元测试（开发者自检）
make test-l2     # L2 集成测试（Feature 完成门禁）
make test        # L1 + L2
make test-l3     # L3 E2E（交付门禁，产出 tests/e2e/REPORT.md）
make vet fmt smoke
```

L3 会做黑盒端到端：建账户 → 封存真实视频 → 起服务 → Range 拉流逐字节比对 → 篡改检测 → 锁定清零 → 助记词重建 → 搬运。有真实样本时可让无头 Chrome 做真实解码校验（含「原文件 vs 密档流」对照）：

```sh
tests/e2e/fetch-samples.sh /tmp/safemedia-samples
SAFEMEDIA_E2E_SAMPLES=/tmp/safemedia-samples make test-l3
```

## 术语

密档（Vault）/ 封存（Seal）/ 启封（Unseal）/ 账户（Account）/ 助记词（Mnemonic）/ 引擎（Engine）/ 桌面外壳（Desktop Shell）——详见 [`CONTEXT.md`](CONTEXT.md)。

## 许可

见 [`LICENSE`](LICENSE)。