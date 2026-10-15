# go-safemedia

加密视频播放器——离线、以账户登录的密档封存与播放工具（Go 实现）。

> 本文件是项目词汇表。术语是文档、规格与代码中的唯一规范名称；_Avoid_ 列出的词禁止出现在交付物中。

## Language

**密档**（Vault）：
封存后的加密视频文件，扩展名 `.svid`。没有对应账户的私钥，内容不可读、不可播放。
_Avoid_：加密视频、加密文件、转换后的文件、encrypted video

**封存**（Seal）：
把原始视频文件加密包装成密档的过程。不转码、不改变视频编码，只加密原始字节。
输入可以是单个视频文件，也可以是一个目录（递归遍历其中的视频）。
_Avoid_：转换、转码、convert、encrypt（对用户文案中）

**启封**（Unseal）：
播放时解出文件密钥并解密数据流的过程。只发生在内存与本地播放流中，明文不落盘。
_Avoid_：解密（对用户文案中）、decrypt

**账户**（Account）：
一把密钥对及其登录凭据，以太坊式格式（不上链、不联网）。是能播放密档的唯一身份。
_Avoid_：钱包、证书、用户、wallet、certificate

**账户文件**（Keystore）：
账户的加密封装文件，标准 keystore JSON（Web3 Secret Storage v3），私钥由登录密码经 scrypt 保护。默认位置 `~/.safemedia/`，可用 `--account` 覆盖。
_Avoid_：证书文件、私钥文件

**账户地址**（Address）：
由账户公钥派生的稳定标识，用于显示与核验。
_Avoid_：公钥哈希

**助记词**（Mnemonic）：
创建账户时生成的 BIP-39 恢复单词，默认 12 个（可选 24），可完整重建账户。创建时强制抄写并确认。
_Avoid_：私钥备份、种子、seed phrase（对用户文案中）

**恢复文件**（Recovery Keystore）：
账户的第二个加密封装，由独立于登录密码的恢复口令保护，用于忘记密码时重置登录密码。
_Avoid_：备份文件

**文件密钥**（File Key）：
封存时随机生成的一次性对称密钥，加密密档正文；被账户公钥（ECIES）封装后存于密档头部。一密档一密钥。
_Avoid_：内容密钥、对称密钥

**分块**（Chunk）：
密档正文的独立加密单元（固定 4 MiB），使密档支持随机读取（拖进度条）。
_Avoid_：分片、block

**容器白名单**（Container Allowlist）：
允许封存的视频容器集合：`.mp4` / `.mov` / `.webm`。不在名单内的容器在封存时被拒绝。
_Avoid_：支持格式、supported formats

**登录**（Login）：
播放前加载账户文件、验证密码、解锁私钥的动作。
_Avoid_：解锁、认证、unlock、authenticate

**锁定**（Lock）：
播放结束（进程退出或空闲超时）后，密钥从内存清除、播放服务关闭的状态。
_Avoid_：注销、logout

**播放服务**（Playback Server）：
播放期间在 `127.0.0.1` 上临时开启的本地 HTTP 流服务，浏览器或 mpv 从这里获取解密后的视频流。
_Avoid_：流媒体服务器、streaming server
