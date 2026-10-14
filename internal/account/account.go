// Package account 实现 go-safemedia 的账户：以太坊格式的 secp256k1 密钥对、
// BIP-39 助记词恢复与 Web3 Secret Storage v3 keystore 存取。
//
// 完全离线：不上链、不联网。项目红线：只新建账户，绝不导入持有真实资产的钱包密钥。
package account

import (
	"crypto/ecdsa"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/tyler-smith/go-bip39"
)

// ethDerivationPath 是以太坊标准 BIP-44 首账户路径 m/44'/60'/0'/0/0。
// 与 MetaMask 一致：同一助记词导入 MetaMask 会得到同一地址。
var ethDerivationPath = []uint32{
	hdkeychain.HardenedKeyStart + 44,
	hdkeychain.HardenedKeyStart + 60,
	hdkeychain.HardenedKeyStart + 0,
	0,
	0,
}

// Account 是一个已解锁的账户。私钥仅存在于内存中。
type Account struct {
	priv     *ecdsa.PrivateKey
	addr     string
	mnemonic string // 仅创建/助记词恢复时携带；从 keystore 载入时为空
}

// Generate 生成新账户，words 只能是 12 或 24。
func Generate(words int) (*Account, error) {
	var entropyBits int
	switch words {
	case 12:
		entropyBits = 128
	case 24:
		entropyBits = 256
	default:
		return nil, fmt.Errorf("助记词词数只支持 12 或 24，收到 %d", words)
	}
	entropy, err := bip39.NewEntropy(entropyBits)
	if err != nil {
		return nil, fmt.Errorf("生成熵失败: %w", err)
	}
	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return nil, fmt.Errorf("生成助记词失败: %w", err)
	}
	acct, err := fromMnemonic(mnemonic)
	if err != nil {
		return nil, err
	}
	acct.mnemonic = mnemonic
	return acct, nil
}

// RecoverFromMnemonic 用助记词重建账户（确定性推导，结果与创建时完全一致）。
func RecoverFromMnemonic(mnemonic string) (*Account, error) {
	if !bip39.IsMnemonicValid(mnemonic) {
		return nil, errors.New("助记词无效：请检查词数与拼写")
	}
	acct, err := fromMnemonic(mnemonic)
	if err != nil {
		return nil, err
	}
	acct.mnemonic = mnemonic
	return acct, nil
}

// ValidMnemonic 报告助记词是否是合法的 BIP-39 助记词（含校验和）。
func ValidMnemonic(mnemonic string) bool {
	return bip39.IsMnemonicValid(mnemonic)
}

func fromMnemonic(mnemonic string) (*Account, error) {
	seed := bip39.NewSeed(mnemonic, "")
	master, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	if err != nil {
		return nil, fmt.Errorf("派生主密钥失败: %w", err)
	}
	key := master
	for _, idx := range ethDerivationPath {
		key, err = key.Derive(idx)
		if err != nil {
			return nil, fmt.Errorf("派生路径失败: %w", err)
		}
	}
	priv, err := key.ECPrivKey()
	if err != nil {
		return nil, fmt.Errorf("取出私钥失败: %w", err)
	}
	ecdsaPriv := priv.ToECDSA()
	return &Account{
		priv: ecdsaPriv,
		addr: ethcrypto.PubkeyToAddress(ecdsaPriv.PublicKey).Hex(),
	}, nil
}

// Mnemonic 返回助记词；从 keystore 载入的账户返回空串。
func (a *Account) Mnemonic() string { return a.mnemonic }

// Address 返回账户地址（0x 开头的 42 字符十六进制）。
func (a *Account) Address() string { return a.addr }

// PrivateKey 返回私钥（供密档封装用途）。调用方不得持久化。
func (a *Account) PrivateKey() *ecdsa.PrivateKey { return a.priv }
