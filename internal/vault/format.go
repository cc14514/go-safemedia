// Package vault 实现 go-safemedia 的 .svid 密档容器：定长头部 + 分块 AES-256-GCM。
//
// 头部 156 字节定长（小端），布局见 plan.md §4。密档不存账户地址：
// 播放时用私钥尝试解封文件密钥，GCM 认证失败即「该密档不属于当前账户」。
package vault

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// 格式常量（v1）。
const (
	MagicSize        = 4
	HeaderSize       = 156
	DefaultChunkSize = 4 << 20 // 4 MiB
	ChunkNonceSize   = 12
	GCMTagSize       = 16
	Version          = 1

	// CipherSuiteECIESGCM = ECIES(secp256k1 ECDH + KDF) 封装文件密钥 + AES-256-GCM。
	CipherSuiteECIESGCM = 1
)

// 容器提示，仅用于 Content-Type 选择；播放前仍以解密后明文嗅探为准。
const (
	ContainerMP4  byte = 1
	ContainerWebM byte = 2
	ContainerMOV  byte = 3
)

// Magic 是 .svid 文件头四字节。
var Magic = [4]byte{'S', 'V', 'I', 'D'}

// 头部字段偏移与长度（小端；总和 = 156）。
const (
	offMagic         = 0
	offVersion       = 4
	offCipherSuite   = 5
	offContainerHint = 6
	offChunkSize     = 7
	offChunkCount    = 11
	offPlaintextSize = 19
	offEphPub        = 27
	ephPubSize       = 65
	offWrappedKey    = 92
	wrappedKeySize   = 32
	offKeyTag        = 124
	keyTagSize       = 16
	offKeyNonce      = 140
	keyNonceSize     = 12
	offReserved      = 152
	reservedSize     = 4
)

// 错误。调用方按语义区分退出码（见 plan.md §退出码表）。
var (
	ErrNotAVault          = errors.New("不是密档文件或头部已损坏")
	ErrUnsupportedVersion = errors.New("不支持的密档版本")
	ErrTampered           = errors.New("密档已损坏或被篡改")
	ErrNotThisAccount     = errors.New("该密档不属于当前账户")
	ErrLocked             = errors.New("会话已锁定：文件密钥已从内存清零")
)

// Header 是 .svid 的定长头部。
type Header struct {
	Version       byte
	CipherSuite   byte
	ContainerHint byte
	ChunkSize     uint32
	ChunkCount    uint64
	PlaintextSize uint64

	EphPublicKey [ephPubSize]byte
	WrappedKey   [wrappedKeySize]byte
	KeyTag       [keyTagSize]byte
	KeyNonce     [keyNonceSize]byte
}

// Marshal 序列化为 156 字节。
func (h *Header) Marshal() []byte {
	b := make([]byte, HeaderSize)
	copy(b[offMagic:], Magic[:])
	b[offVersion] = h.Version
	b[offCipherSuite] = h.CipherSuite
	b[offContainerHint] = h.ContainerHint
	binary.LittleEndian.PutUint32(b[offChunkSize:], h.ChunkSize)
	binary.LittleEndian.PutUint64(b[offChunkCount:], h.ChunkCount)
	binary.LittleEndian.PutUint64(b[offPlaintextSize:], h.PlaintextSize)
	copy(b[offEphPub:], h.EphPublicKey[:])
	copy(b[offWrappedKey:], h.WrappedKey[:])
	copy(b[offKeyTag:], h.KeyTag[:])
	copy(b[offKeyNonce:], h.KeyNonce[:])
	return b
}

// ParseHeader 解析并校验 156 字节头部。
func ParseHeader(b []byte) (*Header, error) {
	if len(b) < HeaderSize {
		return nil, ErrNotAVault
	}
	for i := 0; i < MagicSize; i++ {
		if b[offMagic+i] != Magic[i] {
			return nil, ErrNotAVault
		}
	}
	h := &Header{
		Version:       b[offVersion],
		CipherSuite:   b[offCipherSuite],
		ContainerHint: b[offContainerHint],
		ChunkSize:     binary.LittleEndian.Uint32(b[offChunkSize:]),
		ChunkCount:    binary.LittleEndian.Uint64(b[offChunkCount:]),
		PlaintextSize: binary.LittleEndian.Uint64(b[offPlaintextSize:]),
	}
	if h.Version != Version {
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedVersion, h.Version)
	}
	if h.CipherSuite != CipherSuiteECIESGCM {
		return nil, fmt.Errorf("不支持的加密套件: %d", h.CipherSuite)
	}
	if h.ChunkSize == 0 {
		return nil, ErrNotAVault
	}
	copy(h.EphPublicKey[:], b[offEphPub:])
	copy(h.WrappedKey[:], b[offWrappedKey:])
	copy(h.KeyTag[:], b[offKeyTag:])
	copy(h.KeyNonce[:], b[offKeyNonce:])

	// 头部自洽性：块数必须与明文长度匹配（隐式索引的前提）。
	want := uint64(0)
	if h.PlaintextSize > 0 {
		want = (h.PlaintextSize + uint64(h.ChunkSize) - 1) / uint64(h.ChunkSize)
	}
	if want != h.ChunkCount {
		return nil, ErrNotAVault
	}
	return h, nil
}

// chunkPlainLen 返回第 i 块的明文长度。
func (h *Header) chunkPlainLen(i uint64) int {
	if i >= h.ChunkCount {
		return 0
	}
	remaining := h.PlaintextSize - i*uint64(h.ChunkSize)
	if remaining >= uint64(h.ChunkSize) {
		return int(h.ChunkSize)
	}
	return int(remaining)
}

// encryptedChunkLen 返回第 i 块在文件中的长度（nonce + 密文 + tag）。
func (h *Header) encryptedChunkLen(i uint64) int64 {
	return int64(ChunkNonceSize + h.chunkPlainLen(i) + GCMTagSize)
}

// chunkOffset 返回第 i 块在文件中的起始偏移（隐式索引：O(1) 算术定位）。
func (h *Header) chunkOffset(i uint64) int64 {
	return int64(HeaderSize) + int64(i)*int64(ChunkNonceSize+GCMTagSize) + int64(i)*int64(h.ChunkSize)
}
