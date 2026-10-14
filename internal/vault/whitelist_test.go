package vault_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cc14514/go-safemedia/internal/vault"
)

// TestDetectContainerWhitelist 覆盖 T2.1 白名单：mp4/mov(ftyp)、webm(EBML) 通过，
// mkv(matroska)/未知/非视频 拒绝。
func TestDetectContainerWhitelist(t *testing.T) {
	cases := []struct {
		name     string
		head     []byte
		wantHint byte
		wantName string
		wantErr  bool
	}{
		{"mp4", fakeContainer("mp4", 64), vault.ContainerMP4, "mp4", false},
		{"mov(qt)", fakeContainer("mov", 64), vault.ContainerMOV, "mov", false},
		{"webm", fakeContainer("webm", 64), vault.ContainerWebM, "webm", false},
		{"mkv", append([]byte{0x1A, 0x45, 0xDF, 0xA3}, []byte(".......matroska")...), 0, "", true},
		{"ebml-unknown", append([]byte{0x1A, 0x45, 0xDF, 0xA3}, []byte(".......nonsense")...), 0, "", true},
		{"avi", []byte("RIFF....AVI "), 0, "", true},
		{"flv", []byte("FLV\x01......."), 0, "", true},
		{"text", []byte("just some text, definitely not video"), 0, "", true},
		{"too-short", []byte("fty"), 0, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint, name, err := vault.DetectContainer(tc.head)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("应当拒绝，却得到 hint=%d name=%q", hint, name)
				}
				if !errors.Is(err, vault.ErrUnsupportedContainer) {
					t.Fatalf("错误类型不是 ErrUnsupportedContainer: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("应当通过，却报错: %v", err)
			}
			if hint != tc.wantHint || name != tc.wantName {
				t.Fatalf("hint/name = %d/%q, 期望 %d/%q", hint, name, tc.wantHint, tc.wantName)
			}
		})
	}
}

// TestContentTypeSniffing 覆盖 T3.1：ftyp→video/mp4、qt→video/quicktime、EBML→video/webm。
func TestContentTypeSniffing(t *testing.T) {
	cases := []struct {
		name string
		head []byte
		want string
	}{
		{"mp4", fakeContainer("mp4", 64), "video/mp4"},
		{"mov", fakeContainer("mov", 64), "video/quicktime"},
		{"webm", fakeContainer("webm", 64), "video/webm"},
	}
	for _, tc := range cases {
		if got := vault.SniffContentType(tc.head, 0); got != tc.want {
			t.Fatalf("%s: SniffContentType = %q, 期望 %q", tc.name, got, tc.want)
		}
	}
	// 嗅探失败时回退到头部记录的提示
	if got := vault.SniffContentType([]byte("garbage"), vault.ContainerWebM); got != "video/webm" {
		t.Fatalf("回退 Content-Type = %q", got)
	}
	// 头部提示未知时回退到八位字节流
	if got := vault.SniffContentType([]byte("garbage"), 0); got != "application/octet-stream" {
		t.Fatalf("未知提示回退 = %q", got)
	}
}

// TestSealedVaultContentTypeFallsBackToHint 覆盖：密档的 ContentType 使用解密后明文嗅探。
func TestSealedVaultContentTypeFallsBackToHint(t *testing.T) {
	priv := newKey(t)
	cases := []struct {
		hint byte
		want string
	}{
		{vault.ContainerMP4, "video/mp4"},
		{vault.ContainerMOV, "video/quicktime"},
		{vault.ContainerWebM, "video/webm"},
	}
	for _, tc := range cases {
		plain := fakeContainer(map[byte]string{
			vault.ContainerMP4: "mp4", vault.ContainerMOV: "mov", vault.ContainerWebM: "webm",
		}[tc.hint], 800)
		path := sealToFile(t, plain, &priv.PublicKey, tc.hint)
		r, err := vault.Open(path, priv)
		if err != nil {
			t.Fatalf("Open 失败: %v", err)
		}
		if got := r.ContentType(); got != tc.want {
			r.Close()
			t.Fatalf("hint=%d ContentType = %q, 期望 %q", tc.hint, got, tc.want)
		}
		r.Close()
	}
}

// errAfterReader 先吐出 data，再永久报错，用来模拟封存过程中断。
type errAfterReader struct {
	data []byte
	err  error
	sent bool
}

func (e *errAfterReader) Read(p []byte) (int, error) {
	if e.sent {
		return 0, e.err
	}
	e.sent = true
	n := copy(p, e.data)
	return n, nil
}

// TestSealInterruptedLeavesNoPlaintext 覆盖 AC3.6：封存中断时输出里没有明文中间数据。
func TestSealInterruptedLeavesNoPlaintext(t *testing.T) {
	priv := newKey(t)
	plain := fakeContainer("mp4", 4*testChunk)
	sentinel := []byte("TOPSECRET-PLAINTEXT-MARKER-1234567890")
	copy(plain[100:], sentinel)

	dir := t.TempDir()
	path := filepath.Join(dir, "interrupted.svid")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	// 声称有 4 块，但源流只给 1 块多一点就报错
	src := &errAfterReader{data: plain[:testChunk+17], err: errors.New("模拟中断")}
	_, err = vault.Seal(src, f, &priv.PublicKey, int64(len(plain)), vault.ContainerMP4, testChunk)
	f.Close()
	if err == nil {
		t.Fatal("中断的封存应当返回错误")
	}

	raw, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if bytes.Contains(raw, sentinel) {
		t.Fatal("AC3.6：中断的密档里出现了明文标记")
	}
	if bytes.Contains(raw, []byte("ftyp")) {
		t.Fatal("AC3.6：中断的密档里出现了明文视频结构")
	}
}

// TestVaultHidesVideoStructure 覆盖 AC3.7：只有密档时看不到可识别的视频流结构。
func TestVaultHidesVideoStructure(t *testing.T) {
	priv := newKey(t)
	plain := fakeContainer("mp4", 3*testChunk)
	path := sealToFile(t, plain, &priv.PublicKey, vault.ContainerMP4)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("ftyp")) {
		t.Fatal("AC3.7：密档中可见 ftyp 明文结构")
	}
	if bytes.Contains(raw, []byte("isom")) {
		t.Fatal("AC3.7：密档中可见 isom 明文结构")
	}
	if bytes.HasPrefix(raw, []byte{0x00, 0x00, 0x00}) {
		t.Fatal("AC3.7：密档以 ISO BMFF box 尺寸起始，可能被误识别为视频")
	}
	// 明文头部（前 16 字节）不得原样出现
	if bytes.Contains(raw, plain[:16]) {
		t.Fatal("AC3.7：密档中出现了明文首块片段")
	}
}

// TestSealRejectsUnsupportedContainerAtSeal 覆盖 AC3.4：文件级封存入口也守白名单。
func TestSealRejectsUnsupportedContainerAtSeal(t *testing.T) {
	priv := newKey(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "movie.avi")
	plain := append([]byte("RIFF....AVI "), bytes.Repeat([]byte{0x41}, 2000)...)
	if err := os.WriteFile(src, plain, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "movie.svid")

	_, err := vault.SealFileWithProgress(src, dst, &priv.PublicKey, nil)
	if err == nil {
		t.Fatal("AC3.4：白名单外容器应当拒绝")
	}
	if !strings.Contains(err.Error(), "mp4") {
		t.Fatalf("错误信息未提示支持的容器: %v", err)
	}
	if _, statErr := os.Stat(dst); statErr == nil {
		t.Fatal("AC3.4：被拒绝的封存不应留下密档")
	}
}
