package vault_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cc14514/go-safemedia/internal/account"
	"github.com/cc14514/go-safemedia/internal/vault"
)

// benchSize 是吞吐基准的明文体积（32 MiB）。
const benchSize = 32 << 20

func benchKey(b *testing.B) *ecdsa.PrivateKey {
	b.Helper()
	acct, err := account.Generate(12)
	if err != nil {
		b.Fatalf("生成账户失败: %v", err)
	}
	return acct.PrivateKey()
}

// BenchmarkSeal32MiB 量封存吞吐（NFR-1 证据）。go test 会按 SetBytes 打印 MB/s。
func BenchmarkSeal32MiB(b *testing.B) {
	priv := benchKey(b)
	plain := make([]byte, benchSize)
	if _, err := rand.Read(plain); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(plain)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var sink bytes.Buffer
		if _, err := vault.Seal(bytes.NewReader(plain), &sink, &priv.PublicKey,
			int64(len(plain)), vault.ContainerMP4, vault.DefaultChunkSize); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkOpenRead32MiB 量启封后的顺序读吞吐。
func BenchmarkOpenRead32MiB(b *testing.B) {
	priv := benchKey(b)
	plain := make([]byte, benchSize)
	if _, err := rand.Read(plain); err != nil {
		b.Fatal(err)
	}
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.svid")
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := vault.Seal(bytes.NewReader(plain), f, &priv.PublicKey, int64(len(plain)), vault.ContainerMP4, vault.DefaultChunkSize); err != nil {
		b.Fatal(err)
	}
	f.Close()

	r, err := vault.Open(path, priv)
	if err != nil {
		b.Fatal(err)
	}
	defer r.Close()

	buf := make([]byte, 1<<20)
	b.SetBytes(int64(len(plain)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for off := int64(0); off < int64(len(plain)); off += int64(len(buf)) {
			if _, err := r.ReadAt(buf, off); err != nil && err != io.EOF {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkSeqRead32MiB 是同机顺序读明文文件的参照基准（NFR-1 的对比项）。
func BenchmarkSeqRead32MiB(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "plain.bin")
	data := make([]byte, benchSize)
	if _, err := rand.Read(data); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, err := os.Open(path)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, f); err != nil {
			f.Close()
			b.Fatal(err)
		}
		f.Close()
	}
}
