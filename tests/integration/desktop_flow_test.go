package integration

import (
	"crypto/ecdsa"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cc14514/go-safemedia/internal/account"
	"github.com/cc14514/go-safemedia/internal/play"
)

// T3.4：桌面外壳的「起锁定态服务 → 登录页 → 解锁 → 库索引/媒体 → 软锁定回登录页」整条链路。
//
// 外壳本身很薄，这一段才是它的行为契约，对应 specs/002 的 US1/US2/US3。
func TestDesktopFlowLockedToUnlocked(t *testing.T) {
	dir := t.TempDir()
	const pw = "desktop passphrase"
	keystore := newAccountForTest(t, dir, pw)

	libDir := filepath.Join(dir, "lib")
	if err := os.MkdirAll(libDir, 0o700); err != nil {
		t.Fatal(err)
	}
	plain := fakeMP4(7000)
	src := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(src, plain, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(libDir, "clip.svid")
	if out, err := runCLI(t, &script{secrets: []string{pw}}, "--account", keystore, "seal", src, "-o", dst); err != nil {
		t.Fatalf("seal 失败: %v\n%s", err, out)
	}

	entries, err := play.ScanLibrary(libDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("库内密档数 = %d，期望 1", len(entries))
	}

	unlockAttempts := 0
	lib, err := play.NewLibrary(play.LibraryOptions{
		Entries:     entries,
		Target:      libDir,
		SoftLock:    true, // 外壳模式：锁定回登录页，不关停
		IdleTimeout: play.DefaultIdleTimeout,
		Unlock: func(password string) (*ecdsa.PrivateKey, error) {
			unlockAttempts++
			acct, err := account.LoadKeystoreWithPassphrase(keystore, []byte(password))
			if err != nil {
				return nil, err
			}
			return acct.PrivateKey(), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()

	base, err := lib.Start()
	if err != nil {
		t.Fatal(err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	// 不跟随重定向的客户端：用来断言「登录成功 → 303 + 会话 cookie」这一语义本身。
	// 它与 client 共用 cookie jar，所以后续用 client 取内容时会带上会话。
	noredir := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	baseURL, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	hasSession := func() bool {
		for _, ck := range jar.Cookies(baseURL) {
			if ck.Name == "sv_session" && ck.Value != "" {
				return true
			}
		}
		return false
	}

	// ① 锁定态：只给登录页
	first := get(t, client, base)
	if first.code != http.StatusOK || !strings.Contains(first.body, `action="/login"`) {
		t.Fatalf("锁定态首页应为登录页：code=%d\n%s", first.code, first.body)
	}
	if cc := first.header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("登录页缺少 no-store: %q", cc)
	}

	// ② 口令错误：停在登录页，且不下发会话 cookie
	wrong := postForm(t, noredir, base+"login", url.Values{"password": {"wrong"}})
	if wrong.code == http.StatusSeeOther || wrong.code == http.StatusFound {
		t.Fatalf("口令错误不该重定向: %d", wrong.code)
	}
	if !strings.Contains(wrong.body, `action="/login"`) {
		t.Fatalf("口令错误后应重绘登录页:\n%s", wrong.body)
	}
	if hasSession() {
		t.Fatal("口令错误却下发了会话 cookie")
	}
	stillLocked := get(t, client, base)
	if !strings.Contains(stillLocked.body, `action="/login"`) {
		t.Fatalf("口令错误后不应进入内容区:\n%s", stillLocked.body)
	}

	// ③ 口令正确：303 + 会话 cookie（HttpOnly/SameSite）
	okLogin := postForm(t, noredir, base+"login", url.Values{"password": {pw}})
	if okLogin.code != http.StatusSeeOther {
		t.Fatalf("登录成功应 303，实际 code=%d\n%s", okLogin.code, okLogin.body)
	}
	if !hasSession() {
		t.Fatal("登录成功未下发会话 cookie")
	}
	if unlockAttempts < 2 {
		t.Fatalf("解锁回调调用次数 = %d，期望含失败尝试在内 ≥2", unlockAttempts)
	}
	idx := get(t, client, base)
	if idx.code != http.StatusOK || !strings.Contains(idx.body, "clip") {
		t.Fatalf("解锁后应能看到库索引：code=%d\n%s", idx.code, idx.body)
	}

	// ④ 会话可用：进入播放页并拉取媒体（与原文一致）
	watch := get(t, client, findWatchURL(t, idx.body, base))
	if watch.code != http.StatusOK {
		t.Fatalf("播放页状态码 = %d", watch.code)
	}
	media := findMediaURL(t, watch.body, base)
	mr := get(t, client, media)
	if mr.code != http.StatusOK || len(mr.bytes) != len(plain) {
		t.Fatalf("媒体拉取失败：code=%d len=%d 期望 %d", mr.code, len(mr.bytes), len(plain))
	}

	// ⑤ 软锁定：回落到登录页（服务不退出），且媒体随即不可读
	lock := postForm(t, client, base+"lock", url.Values{})
	if lock.code != http.StatusOK && lock.code != http.StatusNoContent && lock.code != http.StatusSeeOther {
		t.Fatalf("锁定请求失败: code=%d", lock.code)
	}
	afterLock := get(t, client, base)
	if !strings.Contains(afterLock.body, `action="/login"`) {
		t.Fatalf("锁定后应回到登录页:\n%s", afterLock.body)
	}
	lockedMedia := get(t, client, media)
	if lockedMedia.code == http.StatusOK || lockedMedia.code == http.StatusPartialContent {
		t.Fatalf("锁定后媒体应被拒，实际 code=%d", lockedMedia.code)
	}

	// ⑥ 可再次解锁（软锁定不是一次性）
	again := postForm(t, noredir, base+"login", url.Values{"password": {pw}})
	if again.code != http.StatusSeeOther {
		t.Fatalf("软锁定后应可再次解锁，实际 code=%d", again.code)
	}

	// ⑦ 关闭：服务关停（连接被拒）或已进入终态（410），二者都算正确
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get(base)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusGone {
			t.Fatalf("关闭后仍能取得内容：code=%d", resp.StatusCode)
		}
	}
}

type httpResult struct {
	code   int
	body   string
	bytes  []byte
	header http.Header
}

func get(t *testing.T, c *http.Client, u string) httpResult {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return httpResult{code: resp.StatusCode, body: string(b), bytes: b, header: resp.Header}
}

func postForm(t *testing.T, c *http.Client, u string, form url.Values) httpResult {
	t.Helper()
	resp, err := c.PostForm(u, form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return httpResult{code: resp.StatusCode, body: string(b), bytes: b, header: resp.Header}
}

var (
	// 单文件模式是 /media/<token>，目录模式是 /media/<token>/<id>。
	mediaRe = regexp.MustCompile(`src="(/media/[0-9a-f]+(?:/[0-9a-f]+)?)"`)
	watchRe = regexp.MustCompile(`href="(/watch/[0-9a-f]+)"`)
)

func findWatchURL(t *testing.T, page, base string) string {
	t.Helper()
	m := watchRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("索引页内没有播放条目:\n%s", page)
	}
	return base + strings.TrimPrefix(m[1], "/")
}

func findMediaURL(t *testing.T, page, base string) string {
	t.Helper()
	m := mediaRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("页面内没有媒体地址:\n%s", page)
	}
	return base + strings.TrimPrefix(m[1], "/")
}
