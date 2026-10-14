# go-safemedia 项目宪法

> 版本 1.0 · 2026-10-14 · 本文件是硬门禁：plan.md 必须逐条通过 Constitution Check，违反任何一条不得进入实现。

## Article I: 规格先行（SDD）

- 任何代码变更必须先有已评审通过的 spec.md 与对应 tasks.md；禁止先写代码再补文档。
- spec 的 Acceptance Scenarios（Given/When/Then）是测试用例的唯一来源。
- 实现中发现 spec 有误：先改 spec、重新过评审门，再改代码。

## Article II: 测试门禁（TDD）

- 每个实现任务先写失败测试（RED → GREEN → REFACTOR）；禁止提交无对应测试的实现代码。
- L1 单元测试：每个公开函数必须有测试。
- L2 集成测试（tests/integration/）：正向 + 错误 + 回归路径 100% 覆盖，是 Feature 完成门禁；L2 不过不合并。
- L3 E2E（tests/e2e/REPORT.md）：全部 PASS 才能交付 QA；报告必须含 Pre/Post 对比表格；E2E 缺陷必须记录根因并回流 AGENTS.md。

## Article III: 安全底线

- **明文不落盘**：封存与播放全程，视频明文不得写入磁盘（包括临时文件、交换文件）；解密只发生在内存与 127.0.0.1 响应流中。
- **网络白名单**：只允许监听 127.0.0.1；除用户显式 `--player mpv` 拉起本地进程外，不建立任何对外连接；无遥测。
- **密钥清零**：进程退出、空闲锁定（30 分钟）时，内存中的私钥与文件密钥必须显式清零。
- **只新建账户**：产品与文档一律拒绝导入持有真实资产的钱包私钥/助记词。
- **加密参数不回退**：keystore 用 scrypt（N ≥ 2^17）；正文 AES-256-GCM；每块独立随机 nonce；每密档独立随机文件密钥。

## Article IV: 术语一致

- 代码标识符、文档、UI 文案一律使用 CONTEXT.md 词汇表的规范名。
- _Avoid_ 列出的禁用词不得出现在新增代码与文档中（存量作废内容除外）。

## Article V: 依赖最小化

- 优先 Go 标准库。
- 引入重量级依赖必须在 plan.md 的 Complexity Tracking 中写明理由与替代方案对比。
- 不为假想的未来需求引入依赖。

## Article VI: 范围纪律

- MVP 不做：DRM/防录屏、多用户授权、转码/转封装、GUI、云端服务、移动端。
- 二期候选（mov→mp4 转封装等）不得进入 MVP 代码。
- 超出 spec 范围的任何复杂度必须记录在 plan.md 的 Complexity Tracking 并说明正当性。
