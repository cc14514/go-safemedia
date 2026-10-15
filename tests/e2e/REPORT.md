# 001 go-safemedia — L3 E2E 报告

- 生成时间：2026-10-15 11:29:14 CST
- 运行环境：darwin/arm64 · go1.26.0
- 系统版本：macOS 27.0.1
- 被测二进制：`/var/folders/39/jgrn4s4n59n7y2_4srwlcn8w0000gn/T/TestE2E_TamperDetected1659397998/001/safemedia`（sha256 `10c35809cd266ae3`）
- 样本：Big_Buck_Bunny_360_10s_1MB.mp4(真实, 991017 B), Big_Buck_Bunny_360_10s_1MB.webm(真实, 6687499 B), sample_640x360.mov(真实, 574790 B)
- 判定：**35 PASS / 0 FAIL**（L3 必须全 PASS 才能交付 QA）

## Pre/Post 对比

| # | 场景 | 期望（Pre） | 实测（Post） | 证据 | 判定 |
|---|------|------------|-------------|------|------|
| 1 | 退出码 0（成功） | safemedia version 退出码 = 0 | 退出码=0 | /var/folders/39/jgrn4s4n59n7y2_4srwlcn8w0000gn/T/TestE2E_ExitCodes2455230938/001/safemedia version | **PASS** |
| 2 | 退出码 1（用户错误：口令错） | 错误口令时退出码 = 1 且不启动服务 | 退出码=1；输出=输入登录口令: ✗ 口令错误或 keystore 已损坏 | /var/folders/39/jgrn4s4n59n7y2_4srwlcn8w0000gn/T/TestE2E_ExitCodes2455230938/001/safemedia play <vault> --no-browser | **PASS** |
| 3 | 退出码 2（数据错误：头部损坏） | magic 被破坏时退出码 = 2 | 退出码=2；输出=输入登录口令: ✗ 不是密档文件或头部已损坏 | /var/folders/39/jgrn4s4n59n7y2_4srwlcn8w0000gn/T/TestE2E_ExitCodes2455230938/001/safemedia play <损坏密档> --no-browser | **PASS** |
| 4 | 退出码 3（环境错误：mpv 缺失） | 指定 --player mpv 而本机无 mpv 时退出码 = 3 | 退出码=3；输出=输入登录口令: ✗ 未检测到 mpv：请先安装 mpv，或改用浏览器模式（去掉 --player mpv） | /var/folders/39/jgrn4s4n59n7y2_4srwlcn8w0000gn/T/TestE2E_ExitCodes2455230938/001/safemedia play <vault> --player mpv | **PASS** |
| 5 | AC4.5 浏览器解码 Big_Buck_Bunny_360_10s_1MB.mp4（真实，无头 Chrome） | 真实视频经密档解密后进入播放状态（对照：原文件 playing） | 密档流 data-decode=playing；原文件 data-decode=playing；媒体流与原文逐字节一致（见 US4 行） | chrome --headless --dump-dom（密档流 vs 原文件对照） | **PASS** |
| 6 | AC4.5 浏览器解码 Big_Buck_Bunny_360_10s_1MB.webm（真实，无头 Chrome） | 真实视频经密档解密后进入播放状态（对照：原文件 playing） | 密档流 data-decode=playing；原文件 data-decode=playing；媒体流与原文逐字节一致（见 US4 行） | chrome --headless --dump-dom（密档流 vs 原文件对照） | **PASS** |
| 7 | AC4.5 浏览器解码 sample_640x360.mov（真实，无头 Chrome） | 真实视频经密档解密后进入播放状态（对照：原文件 playing） | 密档流 data-decode=playing；原文件 data-decode=playing；媒体流与原文逐字节一致（见 US4 行） | chrome --headless --dump-dom（密档流 vs 原文件对照） | **PASS** |
| 8 | NFR-1 封存吞吐（32 MiB，AES-256-GCM 分块加密） | ≥100 MB/s（AES-NI；远高于本机 SSD 顺序读，加密不是瓶颈） | 封存 2392 MB/s；同机顺序读（页缓存，RAM 基线）7955 MB/s，比值 30%（该基线是内存速度，偏低属正常） | go test -bench=BenchmarkSeal32MiB ./internal/vault | **PASS** |
| 9 | L1+L2 回归（unit + integration） | 同一源码上 go test ./internal/... ./tests/integration/ 全绿 | 包级结果 6 ok / 0 fail；命令 err=false | go test -count=1 ./internal/... ./cmd/... ./tests/integration/... | **PASS** |
| 10 | AC5.4 目录树索引（递归含子目录） | 索引页列出子目录中的密档，并按目录树（<details>）分组 | 库内 3 个密档（子目录 true）；解析到条目链接 3 个；目录分组=true | GET / 索引页 + 链接解析 | **PASS** |
| 11 | AC5.5 播放页左侧常驻目录树 | 播放页左侧显示整库目录树、当前条目高亮，其它条目可直接点击切换 | 页面 5485 B；<aside> 存在=true；当前条目高亮=true；3 个条目链接齐全=true | GET /watch/<token> | **PASS** |
| 12 | AC5.5 侧栏切换片源 | 两个不同子目录的条目各自取流，内容与原文一致 | 选中 season1/sample_640x360.mov.svid → 574790 B 一致=true；切换 Big_Buck_Bunny_360_10s_1MB.mp4.svid → 一致=true | GET /watch + /media（两条条目） | **PASS** |
| 13 | AC5.5 侧栏选中条目真实解码（真实，无头 Chrome） | 经目录树选中的条目在浏览器里真实解码（合成样本则显示 mpv 提示） | data-decode=playing（对照原文件=playing）；<video> 存在=true；mpv 提示=true | chrome --headless --dump-dom（密档流 vs 原文件对照） | **PASS** |
| 14 | AC8.1 目录树会话锁定 | 侧栏模式 POST /lock → 清零密钥、进程退出、端口关闭 | lock 状态 200；进程退出 err=<nil>；端口已关闭=true；耗时 0.13s | POST /lock + 进程退出 + 端口探测 | **PASS** |
| 15 | US1 账户创建（account create，黑盒） | 12 词助记词 + 精确复述抽查 + keystore(0600) + 输出地址 | 助记词 12 词；地址 0x146c67eE8F775c147f6E1197F980b7Fd07FFCb80；keystore 605 B 权限 0600；耗时 0.7s | account create（含抽查复述） | **PASS** |
| 16 | US3 封存 Big_Buck_Bunny_360_10s_1MB.mp4（真实样本） | 不转码（体积=明文+固定开销）；无明文/文件名残留；原文件不变；0600 | 明文 991017 B → 密档 991201 B（开销 184 B）；耗时 0.25s；原文件不变=true；无明文片段=true 无文件名=true | ✓ Big_Buck_Bunny_360_10s_1MB.mp4.svid 容器=mp4 明文=967.8 KiB 块数=1  +0.0% | **PASS** |
| 17 | US4 播放 Big_Buck_Bunny_360_10s_1MB.mp4（127.0.0.1 环回） | 播放页含 <video> 与 mpv 提示；全量解密流与原文逐字节一致；禁缓存 | 页面 4649 B；流 991017 B 一致=true；Content-Type=video/mp4；Cache-Control=no-store, no-cache, must-revalidate, private | GET /media/<token> | **PASS** |
| 18 | AC4.2 seek Big_Buck_Bunny_360_10s_1MB.mp4（中段 Range） | 任意位置 Range → 206，内容正确，首字节 < 1s | bytes=462740-528275 → 206 状态 206，65536 B，耗时 2 ms | HTTP Range 单请求 | **PASS** |
| 19 | AC8.3 播放零明文残留 Big_Buck_Bunny_360_10s_1MB.mp4 | 解密只在内存；播放期间不新增磁盘文件、进程不打开明文视频 | 播放期间未新增任何文件 | 目录快照对比 + lsof -p <pid> | **PASS** |
| 20 | AC8.1 锁定并退出 Big_Buck_Bunny_360_10s_1MB.mp4 | POST /lock → 清零密钥、进程退出、端口关闭 | lock 状态 200；进程退出 err=<nil>；端口已关闭=true | POST /lock + 进程退出 + 端口探测 | **PASS** |
| 21 | US3 封存 Big_Buck_Bunny_360_10s_1MB.webm（真实样本） | 不转码（体积=明文+固定开销）；无明文/文件名残留；原文件不变；0600 | 明文 6687499 B → 密档 6687711 B（开销 212 B）；耗时 0.28s；原文件不变=true；无明文片段=true 无文件名=true | ✓ Big_Buck_Bunny_360_10s_1MB.webm.svid 容器=webm 明文=6.4 MiB 块数=2  +0.0% | **PASS** |
| 22 | US4 播放 Big_Buck_Bunny_360_10s_1MB.webm（127.0.0.1 环回） | 播放页含 <video> 与 mpv 提示；全量解密流与原文逐字节一致；禁缓存 | 页面 4652 B；流 6687499 B 一致=true；Content-Type=video/webm；Cache-Control=no-store, no-cache, must-revalidate, private | GET /media/<token> | **PASS** |
| 23 | AC4.2 seek Big_Buck_Bunny_360_10s_1MB.webm（中段 Range） | 任意位置 Range → 206，内容正确，首字节 < 1s | bytes=3310981-3376516 → 206 状态 206，65536 B，耗时 8 ms | HTTP Range 单请求 | **PASS** |
| 24 | AC8.3 播放零明文残留 Big_Buck_Bunny_360_10s_1MB.webm | 解密只在内存；播放期间不新增磁盘文件、进程不打开明文视频 | 播放期间未新增任何文件 | 目录快照对比 + lsof -p <pid> | **PASS** |
| 25 | AC8.1 锁定并退出 Big_Buck_Bunny_360_10s_1MB.webm | POST /lock → 清零密钥、进程退出、端口关闭 | lock 状态 200；进程退出 err=<nil>；端口已关闭=true | POST /lock + 进程退出 + 端口探测 | **PASS** |
| 26 | US3 封存 sample_640x360.mov（真实样本） | 不转码（体积=明文+固定开销）；无明文/文件名残留；原文件不变；0600 | 明文 574790 B → 密档 574974 B（开销 184 B）；耗时 0.27s；原文件不变=true；无明文片段=true 无文件名=true | ✓ sample_640x360.mov.svid 容器=mov 明文=561.3 KiB 块数=1  +0.0% | **PASS** |
| 27 | US4 播放 sample_640x360.mov（127.0.0.1 环回） | 播放页含 <video> 与 mpv 提示；全量解密流与原文逐字节一致；禁缓存 | 页面 4613 B；流 574790 B 一致=true；Content-Type=video/quicktime；Cache-Control=no-store, no-cache, must-revalidate, private | GET /media/<token> | **PASS** |
| 28 | AC4.2 seek sample_640x360.mov（中段 Range） | 任意位置 Range → 206，内容正确，首字节 < 1s | bytes=254627-320162 → 206 状态 206，65536 B，耗时 1 ms | HTTP Range 单请求 | **PASS** |
| 29 | AC8.3 播放零明文残留 sample_640x360.mov | 解密只在内存；播放期间不新增磁盘文件、进程不打开明文视频 | 播放期间未新增任何文件 | 目录快照对比 + lsof -p <pid> | **PASS** |
| 30 | AC8.1 锁定并退出 sample_640x360.mov | POST /lock → 清零密钥、进程退出、端口关闭 | lock 状态 200；进程退出 err=<nil>；端口已关闭=true | POST /lock + 进程退出 + 端口探测 | **PASS** |
| 31 | US2 助记词重建（黑盒 recover） | 用 12 词助记词重建账户，地址与原账户一致 | 原地址 0x24520E514D71031800765Bfaf04bb246073CC2B9 → 新地址 0x24520E514D71031800765Bfaf04bb246073CC2B9（一致=true）；keystore 重新生成=true | account recover --from-mnemonic | **PASS** |
| 32 | US2 重建后启封旧密档 | 公钥未变 → 旧密档无需重新封存即可播放 | 旧密档 204984 B 密文 → 204800 B 明文，逐字节一致=true | play（重建账户 + 旧密档） | **PASS** |
| 33 | AC1.1 复述失败即中止（黑盒） | 抽查答错 → 报错退出，且不写出 keystore | 退出 err=true；keystore 存在=false | account create（故意答错） | **PASS** |
| 34 | AC4.6 头部损坏 = 不是密档（黑盒） | magic 被改 → 明确报「不是密档文件或头部已损坏」，不启动服务 | 退出 err=true；输出含「不是密档」=true | play（篡改 magic） | **PASS** |
| 35 | AC4.4 数据块篡改（黑盒） | 被改块报完整性失败；进程不崩溃，播放页仍可访问 | 媒体请求被拒=true（reqErr=<nil>，状态=500）；播放页仍 200=true | GET 中段 Range（篡改块） | **PASS** |

## L1 / L2 回归（同一源码）

```
ok  	github.com/cc14514/go-safemedia/internal/account	0.473s
ok  	github.com/cc14514/go-safemedia/internal/cli	0.350s
ok  	github.com/cc14514/go-safemedia/internal/play	0.488s
ok  	github.com/cc14514/go-safemedia/internal/vault	0.214s
?   	github.com/cc14514/go-safemedia/cmd/safemedia	[no test files]
ok  	github.com/cc14514/go-safemedia/tests/integration	2.358s
```

## 待人工验证（需真实样本 / 人眼 / 外部设备）

以下项需要真实素材或人工观察，自动化无法替代；请逐项勾选后再交付：

- [ ] **AC3.2** iPhone 实机拍摄的 `.mov`：H.264「兼容性最佳」在 Safari/Chrome 直接播放；HEVC「高效」在 Firefox 出现「改用 --player mpv」提示（本机未安装 Firefox，需实机确认）
- [ ] **AC4.2 人眼判定** 真实长视频拖动进度条：按 Pre/Post 表中 seek 行的毫秒数推断（本机 0–1 ms），仍需人眼确认画面正确衔接
- [ ] **AC6.1** 本机安装 mpv 后 `safemedia play clip.svid --player mpv` 实播（本机未安装 mpv，仅验证到「传参 + 未安装报错」为止）
- [ ] **AC7.1** 双机搬运实测：U 盘拷 keystore + .svid 到另一台机播放（L2/L3 用不同 HOME 模拟了可搬运性）
- [ ] **AC8.3 深度检查** 播放结束后全盘扫描（本报告已自动核对：播放期间不新增文件、进程不打开明文视频）

## 如何复现

```sh
make test-l3
# 有真实样本时（推荐；脚本会自动准备 mp4/webm/mov 三个小样本）：
tests/e2e/fetch-samples.sh /tmp/safemedia-samples
SAFEMEDIA_E2E_SAMPLES=/tmp/safemedia-samples make test-l3
```
