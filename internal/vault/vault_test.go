package vault_test

import (
	"bytes"
	"crypto/ecdsa"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cc14514/go-safemedia/internal/account"
	"github.com/cc14514/go-safemedia/internal/vault"
)

const testChunk = 1024

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	acct, err := account.Generate(12)
	if err != nil {
		t.Fatalf("生成账户失败: %v", err)
	}
	return acct.PrivateKey()
}

// pattern 生成可复现的明文。
func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i/251)
	}
	return b
}

// fakeContainer 造一个带指定容器头的最小视频样本（样本过小时只填充可容纳的部分）。
func fakeContainer(hint string, size int) []byte {
	b := pattern(size)
	patch := func(off int, data []byte) {
		if size >= off+len(data) {
			copy(b[off:], data)
		}
	}
	switch hint {
	case "mp4":
		patch(4, []byte("ftypisom"))
	case "mov":
		patch(4, []byte("ftypqt  "))
	case "webm":
		patch(0, []byte{0x1A, 0x45, 0xDF, 0xA3})
		patch(8, []byte("webm"))
	}
	return b
}

func sealToFile(t *testing.T, plain []byte, pub *ecdsa.PublicKey, hint byte) string {
	t.Helper()
	return sealToFileChunk(t, plain, pub, hint, testChunk)
}

func sealToFileChunk(t *testing.T, plain []byte, pub *ecdsa.PublicKey, hint byte, chunk uint32) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sample.svid")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := vault.Seal(bytes.NewReader(plain), f, pub, int64(len(plain)), hint, chunk); err != nil {
		t.Fatalf("Seal 失败: %v", err)
	}
	return path
}

func TestSealOpenRoundTrip(t *testing.T) {
	priv := newKey(t)
	for _, size := range []int{0, 1, 12, 1023, 1024, 1025, 3072, 4097, 10000} {
		plain := fakeContainer("mp4", size)
		path := sealToFile(t, plain, &priv.PublicKey, vault.ContainerMP4)

		r, err := vault.Open(path, priv)
		if err != nil {
			t.Fatalf("size=%d Open 失败: %v", size, err)
		}
		got := make([]byte, size)
		if n, err := r.ReadAt(got, 0); err != nil && err != io.EOF {
			t.Fatalf("size=%d ReadAt 失败: %v", size, err)
		} else if n != size {
			t.Fatalf("size=%d 读取 %d 字节", size, n)
		}
		if !bytes.Equal(got, plain) {
			t.Fatalf("size=%d 明文不一致", size)
		}
		if r.Size() != int64(size) {
			t.Fatalf("size=%d Size()=%d", size, r.Size())
		}
		r.Close()
	}
}

// newKeyFrom 注释已删除：每个测试用单个账户做封存/启封配对。
func TestSealOpenWithSameAccount(t *testing.T) {
	priv := newKey(t)
	plain := fakeContainer("mov", 5000)
	path := sealToFile(t, plain, &priv.PublicKey, vault.ContainerMOV)

	r, err := vault.Open(path, priv)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer r.Close()
	got := make([]byte, len(plain))
	if _, err := r.ReadAt(got, 0); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("明文不一致")
	}
	if r.ContainerHint() != vault.ContainerMOV {
		t.Fatalf("容器提示 = %d", r.ContainerHint())
	}
	if ct := r.ContentType(); ct != "video/quicktime" {
		t.Fatalf("Content-Type = %s, want video/quicktime", ct)
	}
}

func TestReadAtRandomOffsets(t *testing.T) {
	priv := newKey(t)
	plain := fakeContainer("webm", 10000)
	path := sealToFile(t, plain, &priv.PublicKey, vault.ContainerWebM)
	r, err := vault.Open(path, priv)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	cases := []struct{ off, n int }{
		{0, 10}, {0, 2000}, {1000, 100}, {1023, 2}, {1024, 1}, {1025, 10},
		{5000, 4096}, {9999, 1}, {9999, 100}, {9998, 3},
	}
	for _, c := range cases {
		got := make([]byte, c.n)
		n, err := r.ReadAt(got, int64(c.off))
		want := c.n
		if c.off+c.n > len(plain) {
			want = len(plain) - c.off
		}
		if n != want {
			t.Fatalf("off=%d n=%d 读到 %d, want %d (err=%v)", c.off, c.n, n, want, err)
		}
		if !bytes.Equal(got[:want], plain[c.off:c.off+want]) {
			t.Fatalf("off=%d n=%d 内容不一致", c.off, c.n)
		}
	}
	if _, err := r.ReadAt(make([]byte, 1), int64(len(plain))); err != io.EOF {
		t.Fatalf("越界读应返回 io.EOF, got %v", err)
	}
}

func TestSequentialReadMatches(t *testing.T) {
	priv := newKey(t)
	plain := fakeContainer("mp4", 6000)
	path := sealToFile(t, plain, &priv.PublicKey, vault.ContainerMP4)
	r, err := vault.Open(path, priv)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("顺序读取内容不一致")
	}
}

func TestOpenRejectsWrongAccount(t *testing.T) {
	owner := newKey(t)
	other := newKey(t)
	path := sealToFile(t, fakeContainer("mp4", 2000), &owner.PublicKey, vault.ContainerMP4)

	_, err := vault.Open(path, other)
	if !errors.Is(err, vault.ErrNotThisAccount) {
		t.Fatalf("他人私钥应报 ErrNotThisAccount, got %v", err)
	}
}

func TestTamperedCiphertextDetected(t *testing.T) {
	priv := newKey(t)
	path := sealToFile(t, fakeContainer("mp4", 3000), &priv.PublicKey, vault.ContainerMP4)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[vault.HeaderSize+vault.ChunkNonceSize+10] ^= 0xFF // 翻转第一块密文中一个字节
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	r, err := vault.Open(path, priv)
	if err != nil {
		t.Fatalf("头部未损坏，Open 应成功: %v", err)
	}
	defer r.Close()
	_, err = r.ReadAt(make([]byte, 100), 0)
	if !errors.Is(err, vault.ErrTampered) {
		t.Fatalf("篡改块应报 ErrTampered, got %v", err)
	}
	// 未受影响的后块仍可读（不崩溃）
	if _, err := r.ReadAt(make([]byte, 10), 2500); err != nil {
		t.Fatalf("未篡改块应可读: %v", err)
	}
}

func TestTruncatedVaultDetected(t *testing.T) {
	priv := newKey(t)
	path := sealToFile(t, fakeContainer("mp4", 3000), &priv.PublicKey, vault.ContainerMP4)
	raw, _ := os.ReadFile(path)
	if err := os.WriteFile(path, raw[:len(raw)-5], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Open(path, priv); !errors.Is(err, vault.ErrTampered) {
		t.Fatalf("截断密档应报 ErrTampered, got %v", err)
	}
}

func TestBadMagicDetected(t *testing.T) {
	priv := newKey(t)
	path := sealToFile(t, fakeContainer("mp4", 1000), &priv.PublicKey, vault.ContainerMP4)
	raw, _ := os.ReadFile(path)
	copy(raw[0:4], []byte("XVID"))
	os.WriteFile(path, raw, 0o600)
	if _, err := vault.Open(path, priv); !errors.Is(err, vault.ErrNotAVault) {
		t.Fatalf("坏 magic 应报 ErrNotAVault, got %v", err)
	}

	// 完全不是密档的文件
	garbage := filepath.Join(t.TempDir(), "nope.svid")
	os.WriteFile(garbage, []byte("this is not a vault at all"), 0o600)
	if _, err := vault.Open(garbage, priv); !errors.Is(err, vault.ErrNotAVault) {
		t.Fatalf("非密档应报 ErrNotAVault, got %v", err)
	}
}

func TestCloseWipesFileKey(t *testing.T) {
	priv := newKey(t)
	path := sealToFile(t, fakeContainer("mp4", 2000), &priv.PublicKey, vault.ContainerMP4)
	r, err := vault.Open(path, priv)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	if _, err := r.ReadAt(make([]byte, 1), 0); err == nil {
		t.Fatal("关闭后 ReadAt 应报错")
	}
	if _, err := r.ReadAt(make([]byte, 1), 0); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("关闭后应报 os.ErrClosed, got %v", err)
	}
}
