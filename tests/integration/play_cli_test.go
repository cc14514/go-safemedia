package integration

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cc14514/go-safemedia/internal/cli"
)

// syncBuffer 是并发安全的输出缓冲（CLI 在后台运行，测试轮询其输出）。
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// startCLI 后台执行 CLI（play 会阻塞），返回输出缓冲与完成通道。
func startCLI(t *testing.T, s *script, args ...string) (*syncBuffer, <-chan error) {
	t.Helper()
	old := cli.Prompt
	oldN := cli.ScryptN
	buf := &syncBuffer{}
	p := s.prompter()
	p.Out = buf
	cli.Prompt = p
	cli.ScryptN = 1 << 10
	t.Cleanup(func() {
		cli.Prompt = old
		cli.ScryptN = oldN
	})

	cmd := cli.NewRootCmd()
	cmd.SetArgs(args)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	return buf, done
}

var urlRe = regexp.MustCompile(`http://127\.0\.0\.1:\d+/`)

func waitForURL(t *testing.T, buf *syncBuffer) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if u := urlRe.FindString(buf.String()); u != "" {
			return u
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待播放服务地址超时，输出:\n%s", buf.String())
	return ""
}

func TestM3PlayServesRangeAndLocks(t *testing.T) {
	dir := t.TempDir()
	const pw = "play passphrase"
	keystore := newAccountForTest(t, dir, pw)

	src := filepath.Join(dir, "clip.mp4")
	plain := fakeMP4(9000)
	if err := os.WriteFile(src, plain, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "clip.svid")
	if out, err := runCLI(t, &script{secrets: []string{pw}}, "--account", keystore, "seal", src, "-o", dst); err != nil {
		t.Fatalf("seal 失败: %v\n%s", err, out)
	}

	buf, done := startCLI(t, &script{secrets: []string{pw}},
		"--account", keystore, "play", dst, "--no-browser", "--idle", "10m")
	base := waitForURL(t, buf)

	// 播放页：含 <video> 与受 token 保护的媒体地址
	resp, err := http.Get(base)
	if err != nil {
		t.Fatalf("取播放页失败: %v", err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("播放页状态码 = %d", resp.StatusCode)
	}
	// AC6.2：CLI 走预解锁路径，行为与改动前一致——直接给播放页，绝不出现登录页。
	if strings.Contains(string(page), `action="/login"`) {
		t.Fatalf("CLI play 不应出现登录页（AC6.2）:\n%s", page)
	}
	if !strings.Contains(string(page), "<video") {
		t.Fatalf("CLI play 播放页缺少 <video>（AC6.2）:\n%s", page)
	}
	m := regexp.MustCompile(`src="(/media/[0-9a-f]+)"`).FindStringSubmatch(string(page))
	if m == nil {
		t.Fatalf("播放页没有媒体地址:\n%s", page)
	}
	media := base + strings.TrimPrefix(m[1], "/")

	// 拖进度条：Range 拉流必须与原文逐字节一致
	req, _ := http.NewRequest(http.MethodGet, media, nil)
	req.Header.Set("Range", "bytes=4000-8999")
	rresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rresp.Body)
	rresp.Body.Close()
	if rresp.StatusCode != http.StatusPartialContent {
		t.Fatalf("Range 状态码 = %d, 期望 206", rresp.StatusCode)
	}
	if !bytes.Equal(body, plain[4000:9000]) {
		t.Fatal("Range 拉流内容与原文不一致")
	}

	// 全量拉流一致
	fresp, err := http.Get(media)
	if err != nil {
		t.Fatal(err)
	}
	full, _ := io.ReadAll(fresp.Body)
	fresp.Body.Close()
	if !bytes.Equal(full, plain) {
		t.Fatal("全量拉流内容与原文不一致")
	}

	// 手动锁定：命令应随即退出且无错误
	lresp, err := http.Post(base+"lock", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	lresp.Body.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("play 退出返回错误: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("锁定后 play 未退出，输出:\n%s", buf.String())
	}
}

func TestM3PlayRejectsWrongPassword(t *testing.T) {
	dir := t.TempDir()
	keystore := newAccountForTest(t, dir, "right passphrase")

	src := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(src, fakeMP4(2000), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "clip.svid")
	if _, err := runCLI(t, &script{secrets: []string{"right passphrase"}}, "--account", keystore, "seal", src, "-o", dst); err != nil {
		t.Fatal(err)
	}

	// AC4.3：口令错误必须拒绝，且不得启动播放服务
	buf, done := startCLI(t, &script{secrets: []string{"wrong passphrase"}},
		"--account", keystore, "play", dst, "--no-browser")
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("错误口令应当导致 play 失败")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("错误口令下 play 未退出（可能误启动了服务）")
	}
	if urlRe.MatchString(buf.String()) {
		t.Fatalf("口令错误却启动了播放服务:\n%s", buf.String())
	}
}

func TestM3PlayRejectsNonVault(t *testing.T) {
	dir := t.TempDir()
	keystore := newAccountForTest(t, dir, "some passphrase")

	fake := filepath.Join(dir, "not-a-vault.svid")
	if err := os.WriteFile(fake, []byte("SVXX not a vault at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf, done := startCLI(t, &script{secrets: []string{"some passphrase"}},
		"--account", keystore, "play", fake, "--no-browser")
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("AC4.6：非密档文件应当报错")
		}
		if !strings.Contains(err.Error(), "不是密档") {
			t.Fatalf("错误信息不明确: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("非密档文件下 play 未退出")
	}
	if urlRe.MatchString(buf.String()) {
		t.Fatalf("非密档文件却启动了播放服务:\n%s", buf.String())
	}
}

func TestM3PlayTamperedHeaderIsReported(t *testing.T) {
	dir := t.TempDir()
	const pw = "tamper passphrase"
	keystore := newAccountForTest(t, dir, pw)

	src := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(src, fakeMP4(2000), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "clip.svid")
	if _, err := runCLI(t, &script{secrets: []string{pw}}, "--account", keystore, "seal", src, "-o", dst); err != nil {
		t.Fatal(err)
	}

	// 篡改 magic（AC4.6）
	raw, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] ^= 0xFF
	if err := os.WriteFile(dst, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	_, done := startCLI(t, &script{secrets: []string{pw}},
		"--account", keystore, "play", dst, "--no-browser")
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("头部损坏的密档应当报错")
		}
		if !strings.Contains(err.Error(), "不是密档") {
			t.Fatalf("错误信息不明确: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("头部损坏时 play 未退出: %s", fmt.Sprint(done))
	}
}
