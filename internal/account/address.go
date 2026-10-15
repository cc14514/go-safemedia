package account

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// ReadKeystoreAddress 读取 keystore 里的**公开地址**（Web3 Secret Storage v3 的 address 字段）。
//
// 用途：桌面外壳的登录页要列出本机可用账户（FR-6.7），需要一个「不用口令就能显示的名字」。
// 地址是公开信息：读取它不做 KDF、不派生密钥、不接触密文，因此可以安全地在锁定态调用。
// 非 keystore（例如 ~/.safemedia/desktop.json、任意 JSON）返回错误，便于调用方把列表过滤干净。
func ReadKeystoreAddress(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取账户文件失败: %w", err)
	}
	var ks keystoreJSON
	if err := json.Unmarshal(raw, &ks); err != nil {
		return "", fmt.Errorf("不是有效的 JSON: %w", err)
	}
	// 「是 keystore」的判据：有密文与 KDF 字段，且 address 是 40 位十六进制。
	if ks.Crypto.CipherText == "" || ks.Crypto.KDF == "" {
		return "", errors.New("不是 keystore 文件（缺少 crypto/kdf 字段）")
	}
	addr := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ks.Address)), "0x")
	if len(addr) != 40 {
		return "", fmt.Errorf("keystore 的 address 字段长度非法: %q", ks.Address)
	}
	if _, err := hex.DecodeString(addr); err != nil {
		return "", fmt.Errorf("keystore 的 address 字段不是十六进制: %q", ks.Address)
	}
	return checksumAddress(addr), nil
}

// checksumAddress 按 EIP-55 把 40 位小写地址加大小写校验和（与 `account.Address()`
// 、MetaMask/geth 的显示形式一致，避免同一账户在不同地方长得不一样）。
func checksumAddress(lower string) string {
	hash := hex.EncodeToString(ethcrypto.Keccak256([]byte(lower)))
	out := make([]byte, 0, 42)
	out = append(out, '0', 'x')
	for i := 0; i < len(lower); i++ {
		c := lower[i]
		if c >= '0' && c <= '9' || hash[i] < '8' {
			out = append(out, c)
			continue
		}
		out = append(out, c-'a'+'A')
	}
	return string(out)
}

// ShortAddress 把 0x 开头的 40 位地址缩为 0x1a2b…9f8e（登录页显示用）。
// 非法输入原样返回，方便调用方直接展示。
func ShortAddress(addr string) string {
	a := strings.TrimPrefix(strings.TrimSpace(addr), "0x")
	if len(a) != 40 {
		return addr
	}
	return "0x" + a[:4] + "…" + a[36:]
}
