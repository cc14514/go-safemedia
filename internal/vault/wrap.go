package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math/big"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"golang.org/x/crypto/hkdf"
)

// FileKeySize 是每个密档一把的文件密钥长度（AES-256）。
const FileKeySize = 32

// wrapInfo 是 HKDF 的 domain separation 标签（v1 格式固定）。
const wrapInfo = "go-safemedia/svid-v1/filekey"

// WrapFileKey 用账户 secp256k1 公钥封装文件密钥。
//
// 方案（cipher suite = 1）：临时 secp256k1 密钥对与账户公钥做 ECDH(secp256k1)，
// 共享点 x 坐标经 HKDF-SHA256 派生 32 字节封装密钥，再用 AES-256-GCM 加密文件密钥。
// 头部只存临时公钥(65B)+密文(32B)+tag(16B)+nonce(12B)，不存账户地址：
// 换用别的账户私钥 → 派生出的封装密钥不同 → GCM 认证失败 → 「不属于本账户」。
func WrapFileKey(pub *ecdsa.PublicKey, fileKey []byte) (ephPub [ephPubSize]byte, wrapped [wrappedKeySize]byte, tag [keyTagSize]byte, nonce [keyNonceSize]byte, err error) {
	if len(fileKey) != FileKeySize {
		err = fmt.Errorf("文件密钥长度必须为 %d", FileKeySize)
		return
	}
	curve := ethcrypto.S256()
	if pub == nil || pub.X == nil || pub.Y == nil || !curve.IsOnCurve(pub.X, pub.Y) {
		err = errors.New("账户公钥不在 secp256k1 曲线上")
		return
	}

	// 临时密钥：随机标量 d，对应公钥 d*G。
	var d *big.Int
	var ephX, ephY *big.Int
	for {
		d, err = rand.Int(rand.Reader, curve.Params().N)
		if err != nil {
			return
		}
		if d.Sign() == 0 {
			continue
		}
		ephX, ephY = curve.ScalarBaseMult(d.Bytes())
		if ephX != nil && ephY != nil {
			break
		}
	}
	shared, err := ecdhX(curve, pub.X, pub.Y, d)
	if err != nil {
		return
	}
	defer wipe(shared)

	sealingKey, err := hkdfKey(shared)
	if err != nil {
		return
	}
	defer wipe(sealingKey)
	aead, err := newAEAD(sealingKey)
	if err != nil {
		return
	}
	if _, err = rand.Read(nonce[:]); err != nil {
		return
	}
	sealed := aead.Seal(nil, nonce[:], fileKey, nil)
	copy(wrapped[:], sealed[:wrappedKeySize])
	copy(tag[:], sealed[wrappedKeySize:])

	ephPub[0] = 4 // uncompressed point
	ephX.FillBytes(ephPub[1:33])
	ephY.FillBytes(ephPub[33:65])
	return
}

// UnwrapFileKey 用账户私钥解出文件密钥；封装密钥不匹配时返回 ErrNotThisAccount。
func UnwrapFileKey(priv *ecdsa.PrivateKey, ephPub, wrapped, tag, nonce []byte) ([]byte, error) {
	if len(ephPub) != ephPubSize || len(wrapped) != wrappedKeySize ||
		len(tag) != keyTagSize || len(nonce) != keyNonceSize {
		return nil, ErrNotAVault
	}
	if priv == nil || priv.D == nil || ephPub[0] != 4 {
		return nil, ErrNotAVault
	}
	curve := ethcrypto.S256()
	x := new(big.Int).SetBytes(ephPub[1:33])
	y := new(big.Int).SetBytes(ephPub[33:65])
	shared, err := ecdhX(curve, x, y, priv.D)
	if err != nil {
		return nil, ErrNotAVault
	}
	defer wipe(shared)

	sealingKey, err := hkdfKey(shared)
	if err != nil {
		return nil, err
	}
	defer wipe(sealingKey)
	aead, err := newAEAD(sealingKey)
	if err != nil {
		return nil, err
	}
	sealed := make([]byte, 0, wrappedKeySize+keyTagSize)
	sealed = append(sealed, wrapped...)
	sealed = append(sealed, tag...)
	fileKey, err := aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, ErrNotThisAccount
	}
	return fileKey, nil
}

// ecdhX 计算 d*(x,y) 的 x 坐标（定长 32 字节，大端）。
func ecdhX(curve elliptic.Curve, x, y *big.Int, d *big.Int) ([]byte, error) {
	if x == nil || y == nil || !curve.IsOnCurve(x, y) {
		return nil, errors.New("对端公钥不在曲线上")
	}
	sx, _ := curve.ScalarMult(x, y, d.Bytes())
	if sx == nil {
		return nil, errors.New("ECDH 结果为无穷远点")
	}
	out := make([]byte, 32)
	sx.FillBytes(out)
	return out, nil
}

// hkdfKey 用 HKDF-SHA256 从共享秘密派生封装密钥。
func hkdfKey(shared []byte) ([]byte, error) {
	key := make([]byte, FileKeySize)
	if _, err := io.ReadFull(hkdf.New(sha256.New, shared, nil, []byte(wrapInfo)), key); err != nil {
		return nil, err
	}
	return key, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
