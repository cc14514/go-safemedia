package account

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"golang.org/x/crypto/scrypt"
)

// DefaultScryptN 是生产环境的 scrypt 成本参数（项目宪法要求 N >= 2^17）。
const DefaultScryptN = 1 << 17

const (
	scryptR = 8
	scryptP = 1
	dkLen   = 32
)

// ErrWrongPassword 表示口令错误，或 keystore 文件被篡改/损坏。
var ErrWrongPassword = errors.New("口令错误或 keystore 已损坏")

// keystoreJSON 是 Web3 Secret Storage v3 格式（MetaMask/geth 可识别）。
type keystoreJSON struct {
	Version int        `json:"version"`
	ID      string     `json:"id"`
	Address string     `json:"address"`
	Crypto  cryptoJSON `json:"crypto"`
}

type cryptoJSON struct {
	CipherText   string     `json:"ciphertext"`
	CipherParams paramsJSON `json:"cipherparams"`
	Cipher       string     `json:"cipher"`
	KDF          string     `json:"kdf"`
	KDFParams    kdfJSON    `json:"kdfparams"`
	MAC          string     `json:"mac"`
}

type paramsJSON struct {
	IV string `json:"iv"`
}

type kdfJSON struct {
	DKLen int    `json:"dklen"`
	Salt  string `json:"salt"`
	N     int    `json:"n"`
	R     int    `json:"r"`
	P     int    `json:"p"`
}

// SaveKeystore 把账户以 keystore v3 格式写入 path，口令保护，权限 0600。
// scryptN 为 0 时使用 DefaultScryptN（测试可传更低成本参数）。
func SaveKeystore(path string, acct *Account, password string, scryptN int) error {
	if acct == nil || acct.priv == nil {
		return errors.New("账户未解锁，无法保存 keystore")
	}
	if password == "" {
		return errors.New("口令不能为空")
	}
	if scryptN == 0 {
		scryptN = DefaultScryptN
	}

	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("生成盐失败: %w", err)
	}
	dk, err := scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, dkLen)
	if err != nil {
		return fmt.Errorf("scrypt 派生失败: %w", err)
	}

	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return fmt.Errorf("生成 IV 失败: %w", err)
	}
	plain := ethcrypto.FromECDSA(acct.priv)
	cipherText := make([]byte, len(plain))
	block, err := aes.NewCipher(dk[:16])
	if err != nil {
		return fmt.Errorf("初始化 AES 失败: %w", err)
	}
	cipher.NewCTR(block, iv).XORKeyStream(cipherText, plain)

	mac := ethcrypto.Keccak256(append(append([]byte{}, dk[16:32]...), cipherText...))

	ks := keystoreJSON{
		Version: 3,
		ID:      newUUID(),
		Address: strings.TrimPrefix(strings.ToLower(acct.addr), "0x"),
		Crypto: cryptoJSON{
			CipherText:   hex.EncodeToString(cipherText),
			CipherParams: paramsJSON{IV: hex.EncodeToString(iv)},
			Cipher:       "aes-128-ctr",
			KDF:          "scrypt",
			KDFParams: kdfJSON{
				DKLen: dkLen,
				Salt:  hex.EncodeToString(salt),
				N:     scryptN,
				R:     scryptR,
				P:     scryptP,
			},
			MAC: hex.EncodeToString(mac),
		},
	}

	raw, err := json.MarshalIndent(ks, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 keystore 失败: %w", err)
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("创建目录失败: %w", err)
		}
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("写入 keystore 失败: %w", err)
	}
	return nil
}

// LoadKeystore 读取 path 处的 keystore 并用口令解锁。
// 返回的账户不带助记词。
func LoadKeystore(path, password string) (*Account, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 keystore 失败: %w", err)
	}
	var ks keystoreJSON
	if err := json.Unmarshal(raw, &ks); err != nil {
		return nil, fmt.Errorf("解析 keystore 失败: %w", err)
	}
	if ks.Version != 3 {
		return nil, fmt.Errorf("不支持的 keystore 版本: %d", ks.Version)
	}
	if ks.Crypto.Cipher != "aes-128-ctr" || ks.Crypto.KDF != "scrypt" {
		return nil, fmt.Errorf("不支持的 keystore 算法: cipher=%s kdf=%s", ks.Crypto.Cipher, ks.Crypto.KDF)
	}

	salt, err := hex.DecodeString(ks.Crypto.KDFParams.Salt)
	if err != nil {
		return nil, fmt.Errorf("盐格式错误: %w", err)
	}
	iv, err := hex.DecodeString(ks.Crypto.CipherParams.IV)
	if err != nil {
		return nil, fmt.Errorf("IV 格式错误: %w", err)
	}
	cipherText, err := hex.DecodeString(ks.Crypto.CipherText)
	if err != nil {
		return nil, fmt.Errorf("密文格式错误: %w", err)
	}
	macWant, err := hex.DecodeString(ks.Crypto.MAC)
	if err != nil {
		return nil, fmt.Errorf("MAC 格式错误: %w", err)
	}

	dk, err := scrypt.Key([]byte(password), salt,
		ks.Crypto.KDFParams.N, ks.Crypto.KDFParams.R, ks.Crypto.KDFParams.P, ks.Crypto.KDFParams.DKLen)
	if err != nil {
		return nil, fmt.Errorf("scrypt 派生失败: %w", err)
	}
	macGot := ethcrypto.Keccak256(append(append([]byte{}, dk[16:32]...), cipherText...))
	if !hmacEqual(macGot, macWant) {
		return nil, ErrWrongPassword
	}

	block, err := aes.NewCipher(dk[:16])
	if err != nil {
		return nil, fmt.Errorf("初始化 AES 失败: %w", err)
	}
	plain := make([]byte, len(cipherText))
	cipher.NewCTR(block, iv).XORKeyStream(plain, cipherText)

	priv, err := ethcrypto.ToECDSA(plain)
	if err != nil {
		return nil, fmt.Errorf("私钥解析失败: %w", err)
	}
	return &Account{
		priv: priv,
		addr: ethcrypto.PubkeyToAddress(priv.PublicKey).Hex(),
	}, nil
}

func hmacEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// newUUID 生成 RFC 4122 v4 UUID（keystore 规范要求 id 字段）。
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Wipe 就地清零字节切片（口令、私钥派生结果用后清零）。
func Wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// SaveKeystoreWithPassphrase 语义同 SaveKeystore，但接收 []byte 口令并在用后清零。
//
// 注意：Go 字符串不可变，内部 string(passphrase) 的临时副本无法擦除；
// CLI 层因此一律用本函数而非 SaveKeystore。
func SaveKeystoreWithPassphrase(path string, acct *Account, passphrase []byte, scryptN int) error {
	defer Wipe(passphrase)
	return SaveKeystore(path, acct, string(passphrase), scryptN)
}

// LoadKeystoreWithPassphrase 语义同 LoadKeystore，但接收 []byte 口令并在用后清零。
func LoadKeystoreWithPassphrase(path string, passphrase []byte) (*Account, error) {
	defer Wipe(passphrase)
	return LoadKeystore(path, string(passphrase))
}

// ExportRecoveryKeystore 导出一份由独立恢复口令保护的恢复文件。
// 它是同一账户的第二份 keystore 封装：登录口令丢失时用恢复口令打开它，
// 再以新登录口令重存主 keystore。
func ExportRecoveryKeystore(path string, acct *Account, recoveryPassphrase []byte, scryptN int) error {
	return SaveKeystoreWithPassphrase(path, acct, recoveryPassphrase, scryptN)
}
