# 桌面版以 Wails v2 外壳复用既有 loopback 播放服务

- Status: accepted（2026-10-15）

核心引擎（账户、密档、播放服务、目录树 UI）已经完整且被 L3 验证；桌面版只应承担「一个窗口 + 打包」，不重复实现任何封存、启封、登录逻辑。因此选 Wails v2 作外壳：它用系统自带 webview（macOS WKWebView），以 Go **直接 import 本模块的 `internal/` 包**，运行期不需要 Node 或前端构建 —— 窗口只加载一个引导页，引导页把 webview 重定向到引擎自己起的 `127.0.0.1` 播放服务 URL。

## Considered Options

- **webview_go / Swift+WKWebView**：更轻，但前者要自管窗口与打包；后者 import 不了 Go 的 `internal/` 包，引擎得做成 sidecar + IPC。
- **Tauri**：需引入 Rust 工具链 + Go sidecar + IPC，跨语言胶水收益不划算。
- **Electron**：约 150 MB，已否决。
- **纯 Go 启动器**（拉起系统浏览器 `--app` 窗口）：零 cgo、可交叉编译，但窗口是浏览器进程，macOS 上依赖已安装的 Chrome（Safari 无 app 模式）。
- **浏览器 `--app` 占位**：零新代码，但口令仍留在终端。

## Consequences

- **GUI 无法从 macOS 交叉编译**：Wails 依赖 cgo + 平台原生 webview；交叉编译 Windows GUI 需要 mingw-w64 等目标平台 C 工具链（实测 `clang: error: unsupported option '-mthreads' for target 'arm64-apple-darwin27.0.0'`）。故首版只发 **macOS arm64**；Windows GUI 将来在 Windows runner 或 Docker 上构建。**纯 Go 引擎（CLI）不受影响**，仍可 `CGO_ENABLED=0` 交叉编译出 Windows/darwin 双平台。
- **引擎需新增 loopback 登录页**：口令由网页表单进入服务，播放生命周期由「先解锁账户 → 再起服务」反转为「起服务（锁定态）→ 登录页 → 解锁 → 索引页」，锁定后回到登录页。
- **必须禁用 webview 缓存**：webview 自带磁盘缓存会破坏「明文不落盘」红线；除既有 `Cache-Control: no-store` 外，外壳还须在 webview 层禁用缓存。
- **首版不签名、不公证**：Go/Wails 在 arm64 上已自动做 ad-hoc 签名（`codesign` 显示 `Signature=adhoc`），本机可直接运行；但从 GitHub Release 下载后带 quarantine 属性会被 Gatekeeper 拦，文档须给出 `xattr -d com.apple.quarantine` 说明。
- **定位为播放外壳**：封存、建账户、恢复仍走 CLI。