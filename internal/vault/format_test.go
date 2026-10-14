package vault_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/cc14514/go-safemedia/internal/vault"
)

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

func readEquals(t *testing.T, r *vault.Reader, want []byte) bool {
	t.Helper()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("顺序读取失败: %v", err)
	}
	return bytes.Equal(got, want)
}

func TestHeaderMarshalRoundTrip(t *testing.T) {
	h := &vault.Header{
		Version:       vault.Version,
		CipherSuite:   vault.CipherSuiteECIESGCM,
		ContainerHint: vault.ContainerWebM,
		ChunkSize:     vault.DefaultChunkSize,
		ChunkCount:    3,
		PlaintextSize: 3*vault.DefaultChunkSize - 7,
	}
	h.EphPublicKey[0] = 0x04
	h.WrappedKey[31] = 0xAB
	h.KeyTag[0] = 0xCD
	h.KeyNonce[11] = 0xEF

	raw := h.Marshal()
	if len(raw) != vault.HeaderSize {
		t.Fatalf("头部长度 = %d, want %d", len(raw), vault.HeaderSize)
	}
	got, err := vault.ParseHeader(raw)
	if err != nil {
		t.Fatalf("ParseHeader: %v", err)
	}
	if got.Version != h.Version || got.CipherSuite != h.CipherSuite ||
		got.ContainerHint != h.ContainerHint || got.ChunkSize != h.ChunkSize ||
		got.ChunkCount != h.ChunkCount || got.PlaintextSize != h.PlaintextSize {
		t.Fatalf("标量字段不一致: %+v", got)
	}
	if got.EphPublicKey != h.EphPublicKey || got.WrappedKey != h.WrappedKey ||
		got.KeyTag != h.KeyTag || got.KeyNonce != h.KeyNonce {
		t.Fatal("密钥封装字段不一致")
	}
}

func TestParseHeaderRejects(t *testing.T) {
	valid := &vault.Header{
		Version:       vault.Version,
		CipherSuite:   vault.CipherSuiteECIESGCM,
		ContainerHint: vault.ContainerMP4,
		ChunkSize:     1024,
		ChunkCount:    1,
		PlaintextSize: 500,
	}
	raw := valid.Marshal()

	cases := []struct {
		name string
		mut  func([]byte) []byte
		want error
	}{
		{"太短", func(b []byte) []byte { return b[:100] }, vault.ErrNotAVault},
		{"坏 magic", func(b []byte) []byte { b[1] = 'X'; return b }, vault.ErrNotAVault},
		{"版本不支持", func(b []byte) []byte { b[4] = 9; return b }, vault.ErrUnsupportedVersion},
		{"块数为零", func(b []byte) []byte { b[7] = 0; b[8] = 0; b[9] = 0; b[10] = 0; return b }, vault.ErrNotAVault},
		{"块数与明文不符", func(b []byte) []byte { b[11] = 5; return b }, vault.ErrNotAVault},
	}
	for _, c := range cases {
		b := append([]byte(nil), raw...)
		_, err := vault.ParseHeader(c.mut(b))
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}

func TestHeaderChunkArithmetic(t *testing.T) {
	priv := newKey(t)
	// 用小块尺寸验证隐式索引：5 块 + 非整除尾块
	plain := fakeContainer("mp4", 5*testChunk+333)
	path := sealToFileChunk(t, plain, &priv.PublicKey, vault.ContainerMP4, testChunk)

	r, err := vault.Open(path, priv)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Header().ChunkCount != 6 {
		t.Fatalf("chunk_count = %d, want 6", r.Header().ChunkCount)
	}
	// 逐块边界读取（每块首尾各取 8 字节）
	for i := uint64(0); i < r.Header().ChunkCount; i++ {
		start := int64(i) * int64(testChunk)
		for _, off := range []int64{start, start + int64(testChunk) - 8} {
			if off < 0 || off >= int64(len(plain)) {
				continue
			}
			n := 8
			if off+int64(n) > int64(len(plain)) {
				n = len(plain) - int(off)
			}
			got := make([]byte, n)
			if _, err := r.ReadAt(got, off); err != nil && err != io.EOF {
				t.Fatalf("块 %d off=%d: %v", i, off, err)
			}
			if !bytes.Equal(got, plain[off:off+int64(n)]) {
				t.Fatalf("块 %d off=%d 内容不一致", i, off)
			}
		}
	}
}
