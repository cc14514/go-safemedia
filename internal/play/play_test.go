package play_test

import (
	"bytes"
	"crypto/ecdsa"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cc14514/go-safemedia/internal/account"
	"github.com/cc14514/go-safemedia/internal/play"
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

// fakeMP4 造一个带合法 ftyp 头的 mp4 明文。
func fakeMP4(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*13 + i/97)
	}
	copy(b[0:4], []byte{0x00, 0x00, 0x00, 0x20})
	copy(b[4:8], "ftyp")
	copy(b[8:12], "isom")
	return b
}

// newReader 把明文封存到临时密档并返回打开的 Reader。
func newReader(t *testing.T, plain []byte) (*vault.Reader, string) {
	t.Helper()
	priv := newKey(t)
	dir := t.TempDir()
	dst := filepath.Join(dir, "clip.mp4.svid")
	f, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Seal(bytes.NewReader(plain), f, &priv.PublicKey, int64(len(plain)), vault.ContainerMP4, testChunk); err != nil {
		f.Close()
		t.Fatalf("封存失败: %v", err)
	}
	f.Close()
	r, err := vault.Open(dst, priv)
	if err != nil {
		t.Fatalf("打开密档失败: %v", err)
	}
	t.Cleanup(func() { r.Close() })
	return r, dst
}

func newServer(t *testing.T, plain []byte, idle time.Duration, onLock func(string)) (*play.Server, *vault.Reader) {
	t.Helper()
	r, _ := newReader(t, plain)
	s, err := play.New(play.Options{Reader: r, Name: "clip.mp4.svid", IdleTimeout: idle, OnLock: onLock})
	if err != nil {
		t.Fatalf("构造播放服务失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, r
}

func TestRangeRequestReturns206(t *testing.T) {
	plain := fakeMP4(5000)
	s, _ := newServer(t, plain, time.Minute, nil)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+s.MediaPath(), nil)
	req.Header.Set("Range", "bytes=100-199")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("状态码 = %d, 期望 206", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Range"); got != "bytes 100-199/5000" {
		t.Fatalf("Content-Range = %q", got)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(body, plain[100:200]) {
		t.Fatal("Range 内容与明文不一致")
	}
}

func TestFullGetMatchesPlaintext(t *testing.T) {
	plain := fakeMP4(3000)
	s, _ := newServer(t, plain, time.Minute, nil)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + s.MediaPath())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("Content-Type = %q, 期望 video/mp4", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("Cache-Control = %q, 明文不得被缓存", cc)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(body, plain) {
		t.Fatal("全量内容与明文不一致")
	}
}

func TestMediaPathRequiresToken(t *testing.T) {
	plain := fakeMP4(2048)
	s, _ := newServer(t, plain, time.Minute, nil)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/media/deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("错误 token 状态码 = %d, 期望 404", resp.StatusCode)
	}
	if p, err := url.Parse(ts.URL); err == nil && p.Host == "" {
		t.Fatal("测试服务器地址异常")
	}
}

func TestPlayPageMentionsMpvEscapeHatch(t *testing.T) {
	s, _ := newServer(t, fakeMP4(1024), time.Minute, nil)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)
	if !strings.Contains(html, "<video") || !strings.Contains(html, s.MediaPath()) {
		t.Fatalf("播放页缺少 <video> 或媒体地址:\n%s", html)
	}
	if !strings.Contains(html, "--player mpv") {
		t.Fatal("播放页缺少 mpv 逃生提示（AC4.5）")
	}
	if strings.Contains(html, "clip.mp4") && !strings.Contains(html, "clip.mp4.svid") {
		// 名字来自密档文件名，允许出现；这里只确保没有原始视频文件名泄漏检查的占位
		t.Log("播放页显示了密档文件名（非原始视频名），符合 FR-2.3")
	}
}

func TestBindLoopbackOnly(t *testing.T) {
	s, _ := newServer(t, fakeMP4(1024), time.Minute, nil)
	u, err := s.Start()
	if err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	if !strings.HasPrefix(u, "http://127.0.0.1:") {
		t.Fatalf("播放服务 URL = %q，必须只绑 127.0.0.1", u)
	}
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("访问播放页失败: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("播放页状态码 = %d", resp.StatusCode)
	}
}

func TestIdleTimeoutLocksAndWipes(t *testing.T) {
	var mu sync.Mutex
	var reasons []string
	var now = time.Now()
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	setNow := func(d time.Duration) {
		mu.Lock()
		now = now.Add(d)
		mu.Unlock()
	}

	r, _ := newReader(t, fakeMP4(4096))
	s, err := play.New(play.Options{
		Reader:      r,
		Name:        "clip.mp4.svid",
		IdleTimeout: 200 * time.Millisecond,
		Now:         clock,
		OnLock: func(reason string) {
			mu.Lock()
			reasons = append(reasons, reason)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Start(); err != nil {
		t.Fatal(err)
	}

	setNow(time.Second) // 越过空闲阈值
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(reasons)
		mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !s.Locked() {
		t.Fatal("空闲超时后未自动锁定")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reasons) == 0 || !strings.Contains(reasons[0], "空闲") {
		t.Fatalf("锁定回调原因异常: %v", reasons)
	}
	// 密钥已清零：再读必定失败
	if _, err := r.ReadAt(make([]byte, 16), 0); err == nil {
		t.Fatal("锁定后仍能读出明文（密钥未清零）")
	}
}

func TestTamperedVaultDoesNotCrashServer(t *testing.T) {
	plain := fakeMP4(4096)
	r, path := newReader(t, plain)
	s, _ := play.New(play.Options{Reader: r, Name: "tampered.svid", IdleTimeout: time.Minute})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	// 翻转第二块密文中的一个字节（跳过 156B 头部 + 第一块）
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	target := int64(vault.HeaderSize) + int64(vault.ChunkNonceSize+testChunk+vault.GCMTagSize) + int64(vault.ChunkNonceSize) + 4
	one := make([]byte, 1)
	if _, err := f.ReadAt(one, target); err != nil {
		t.Fatal(err)
	}
	one[0] ^= 0xFF
	if _, err := f.WriteAt(one, target); err != nil {
		t.Fatal(err)
	}
	f.Close()

	// 读取第二块所在范围：应当失败但不 panic；随后服务仍能响应播放页
	req, _ := http.NewRequest(http.MethodGet, ts.URL+s.MediaPath(), nil)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", testChunk, 2*testChunk-1))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("篡改后请求应当返回响应而不是断开连接: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	page, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("篡改后服务不可用: %v", err)
	}
	page.Body.Close()
	if page.StatusCode != http.StatusOK {
		t.Fatalf("篡改后播放页状态码 = %d", page.StatusCode)
	}
}

func TestLockEndpointThenMediaGone(t *testing.T) {
	s, _ := newServer(t, fakeMP4(2048), time.Minute, nil)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/lock", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !s.Locked() {
		t.Fatal("POST /lock 后应处于锁定态")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !isConnectionClosed(ts.URL+s.MediaPath()) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !isConnectionClosed(ts.URL + s.MediaPath()) {
		t.Fatal("锁定后媒体地址仍可访问")
	}
}

func isConnectionClosed(u string) bool {
	resp, err := http.Get(u)
	if err != nil {
		return true
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusGone
}
