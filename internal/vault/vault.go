package vault

import (
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"sync"
)

// SealResult 描述一次封存的结果（供 CLI 打印进度/摘要）。
type SealResult struct {
	Container     string
	PlaintextSize int64
	Chunks        uint64
	ChunkSize     uint32
}

// Seal 把明文 r 封存到 w。plaintextSize 必须精确等于 r 的剩余字节数。
// 明文只经过内存缓冲区，不落盘（宪法红线：明文不落盘）。
func Seal(r io.Reader, w io.Writer, pub *ecdsa.PublicKey, plaintextSize int64, hint byte, chunkSize uint32) (*SealResult, error) {
	if plaintextSize < 0 {
		return nil, fmt.Errorf("明文长度非法: %d", plaintextSize)
	}
	if chunkSize == 0 {
		chunkSize = DefaultChunkSize
	}

	fileKey := make([]byte, FileKeySize)
	if _, err := rand.Read(fileKey); err != nil {
		return nil, err
	}
	defer wipe(fileKey)

	ephPub, wrapped, tag, keyNonce, err := WrapFileKey(pub, fileKey)
	if err != nil {
		return nil, err
	}

	var chunkCount uint64
	if plaintextSize > 0 {
		chunkCount = uint64((plaintextSize + int64(chunkSize) - 1) / int64(chunkSize))
	}
	header := &Header{
		Version:       Version,
		CipherSuite:   CipherSuiteECIESGCM,
		ContainerHint: hint,
		ChunkSize:     chunkSize,
		ChunkCount:    chunkCount,
		PlaintextSize: uint64(plaintextSize),
		EphPublicKey:  ephPub,
		WrappedKey:    wrapped,
		KeyTag:        tag,
		KeyNonce:      keyNonce,
	}
	if _, err := w.Write(header.Marshal()); err != nil {
		return nil, err
	}

	aead, err := newAEAD(fileKey)
	if err != nil {
		return nil, err
	}

	plain := make([]byte, chunkSize)
	var written int64
	for i := uint64(0); i < chunkCount; i++ {
		want := header.chunkPlainLen(i)
		n, err := io.ReadFull(r, plain[:want])
		if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			return nil, err
		}
		if n != want {
			return nil, fmt.Errorf("第 %d 块明文不足：读到 %d 字节，期望 %d（封存中断，密档不完整）", i, n, want)
		}
		nonce := make([]byte, ChunkNonceSize)
		if _, err := rand.Read(nonce); err != nil {
			return nil, err
		}
		out := aead.Seal(nonce, nonce, plain[:want], nil)
		if _, err := w.Write(out); err != nil {
			return nil, err
		}
		written += int64(want)
	}
	if written != plaintextSize {
		return nil, fmt.Errorf("封存字节数不符：%d != %d", written, plaintextSize)
	}
	return &SealResult{
		Container:     containerName(hint),
		PlaintextSize: plaintextSize,
		Chunks:        chunkCount,
		ChunkSize:     chunkSize,
	}, nil
}

// SealFile 封存 srcPath 到 dstPath。封存前按白名单嗅探容器类型；明文不落盘。
func SealFile(srcPath, dstPath string, pub *ecdsa.PublicKey) (*SealResult, error) {
	return SealFileWithProgress(srcPath, dstPath, pub, nil)
}

// SealFileWithProgress 与 SealFile 相同，但封存过程中回调已写入的明文字节数
// （onProgress 为 nil 时不回调）。回调由调用方自行节流，vault 不保证频率。
func SealFileWithProgress(srcPath, dstPath string, pub *ecdsa.PublicKey, onProgress func(done, total int64)) (*SealResult, error) {
	src, err := os.Open(srcPath)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return nil, err
	}
	head := make([]byte, sniffLen)
	n, err := src.ReadAt(head, 0)
	if err != nil && err != io.EOF {
		return nil, err
	}
	hint, name, err := DetectContainer(head[:n])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", srcPath, err)
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	defer dst.Close()

	var in io.Reader = src
	if onProgress != nil {
		in = &progressReader{r: src, total: info.Size(), onProgress: onProgress}
	}
	res, err := Seal(in, dst, pub, info.Size(), hint, DefaultChunkSize)
	if err != nil {
		return nil, err
	}
	res.Container = name
	return res, nil
}

// progressReader 统计从源文件读出的明文字节数并回调。
type progressReader struct {
	r          io.Reader
	total      int64
	done       int64
	onProgress func(done, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	p.onProgress(p.done, p.total)
	return n, err
}

// Reader 是只读的启封器：支持随机访问（io.ReaderAt），用于 HTTP Range 服务。
//
// 不缓存明文块：每次 ReadAt 按需解密涉及的块。明文仅存在于内存与 loopback 响应中。
type Reader struct {
	// mu 保护密钥与生命周期状态：ReadAt 持读锁，Close/Wipe 持写锁。
	// 这样「清零密钥」与「正在解密的请求」不会并发访问同一份密钥
	// （软锁定/CTRL-C 时服务仍可能有在处理中的响应）。
	mu      sync.RWMutex
	f       *os.File
	header  *Header
	aead    cipher.AEAD
	fileKey []byte
	closed  bool

	posMu sync.Mutex // 保护 Read 的顺序位置（ReadAt 不使用）
	pos   int64
}

// Open 打开密档并用账户私钥解封文件密钥。私钥不匹配时返回 ErrNotThisAccount。
func Open(path string, priv *ecdsa.PrivateKey) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	head := make([]byte, HeaderSize)
	if _, err := io.ReadFull(f, head); err != nil {
		f.Close()
		return nil, ErrNotAVault
	}
	header, err := ParseHeader(head)
	if err != nil {
		f.Close()
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	expected := int64(HeaderSize) + int64(header.ChunkCount)*int64(ChunkNonceSize+GCMTagSize) + int64(header.PlaintextSize)
	if info.Size() != expected {
		f.Close()
		return nil, fmt.Errorf("%w: 文件长度 %d，期望 %d", ErrTampered, info.Size(), expected)
	}
	fileKey, err := UnwrapFileKey(priv, header.EphPublicKey[:], header.WrappedKey[:], header.KeyTag[:], header.KeyNonce[:])
	if err != nil {
		f.Close()
		return nil, err
	}
	aead, err := newAEAD(fileKey)
	if err != nil {
		wipe(fileKey)
		f.Close()
		return nil, err
	}
	return &Reader{f: f, header: header, aead: aead, fileKey: fileKey}, nil
}

// Header 返回密档头部（只读）。
func (r *Reader) Header() *Header { return r.header }

// Size 返回明文字节数。
func (r *Reader) Size() int64 { return int64(r.header.PlaintextSize) }

// ContainerHint 返回封存时记录的容器提示。
func (r *Reader) ContainerHint() byte { return r.header.ContainerHint }

// ContainerHintName 返回容器提示的可读名称（mp4/mov/webm）。
func (r *Reader) ContainerHintName() string { return containerName(r.header.ContainerHint) }

// ContentType 按解密后明文首字节嗅探 MIME（失败时回退到头部提示）。
func (r *Reader) ContentType() string {
	head := make([]byte, sniffLen)
	n, err := r.ReadAt(head, 0)
	if err != nil && err != io.EOF {
		return ContainerContentType(r.header.ContainerHint)
	}
	return SniffContentType(head[:n], r.header.ContainerHint)
}

// ReadAt 解密 [off, off+len(p)) 范围（io.ReaderAt）。
func (r *Reader) ReadAt(p []byte, off int64) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return 0, os.ErrClosed
	}
	if r.aead == nil {
		// 已锁定：密钥被清零。绝不能继续读，也绝不能 panic（AC4.4）。
		return 0, ErrLocked
	}
	if off < 0 {
		return 0, fmt.Errorf("偏移非法: %d", off)
	}
	size := r.Size()
	if off >= size {
		return 0, io.EOF
	}
	end := off + int64(len(p))
	if end > size {
		end = size
	}
	chunkSize := int64(r.header.ChunkSize)
	first := uint64(off / chunkSize)
	last := uint64((end - 1) / chunkSize)
	buf := make([]byte, chunkSize)
	var done int64
	for i := first; i <= last; i++ {
		plain, err := r.decryptChunk(i, buf)
		if err != nil {
			return int(done), err
		}
		chunkStart := int64(i) * chunkSize
		from := int64(0)
		if off > chunkStart {
			from = off - chunkStart
		}
		to := int64(len(plain))
		if end-chunkStart < to {
			to = end - chunkStart
		}
		n := copy(p[done:], plain[from:to])
		done += int64(n)
	}
	if done < int64(len(p)) {
		return int(done), io.EOF
	}
	return int(done), nil
}

// decryptChunk 解密第 i 块；buf 作为密文/明文复用缓冲（长度 >= chunkSize+tag+nonce）。
func (r *Reader) decryptChunk(i uint64, buf []byte) ([]byte, error) {
	if r.aead == nil {
		return nil, ErrLocked
	}
	clen := r.header.encryptedChunkLen(i)
	if int64(len(buf)) < clen {
		buf = make([]byte, clen)
	}
	enc := buf[:clen]
	if _, err := r.f.ReadAt(enc, r.header.chunkOffset(i)); err != nil {
		return nil, fmt.Errorf("%w: 第 %d 块读取失败", ErrTampered, i)
	}
	nonce := enc[:ChunkNonceSize]
	// dst 必须是 nil 或与密文不重叠的缓冲：Open 返回 dst||明文，
	// 若把 dst 指向 nonce 区，返回值会变成 nonce||明文（曾经的 bug）。
	pt, err := r.aead.Open(nil, nonce, enc[ChunkNonceSize:], nil)
	if err != nil {
		return nil, fmt.Errorf("%w: 第 %d 块认证失败", ErrTampered, i)
	}
	return pt, nil
}

// Read 顺序读取（io.Reader）。
func (r *Reader) Read(p []byte) (int, error) {
	r.posMu.Lock()
	off := r.pos
	r.posMu.Unlock()
	n, err := r.ReadAt(p, off)
	r.posMu.Lock()
	r.pos += int64(n)
	r.posMu.Unlock()
	return n, err
}

// Close 关闭文件并清零内存中的文件密钥。
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	wipe(r.fileKey)
	r.fileKey = nil
	r.aead = nil
	return r.f.Close()
}

// Wipe 显式清零文件密钥（锁定/退出时调用；幂等）。
func (r *Reader) Wipe() {
	r.mu.Lock()
	defer r.mu.Unlock()
	wipe(r.fileKey)
	r.fileKey = nil
	r.aead = nil
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func containerName(hint byte) string {
	switch hint {
	case ContainerMP4:
		return "mp4"
	case ContainerMOV:
		return "mov"
	case ContainerWebM:
		return "webm"
	}
	return "未知"
}
