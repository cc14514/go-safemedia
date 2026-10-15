package play_test

import (
	"crypto/ecdsa"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cc14514/go-safemedia/internal/play"
	"github.com/cc14514/go-safemedia/internal/vault"
)

// testPassword 是门控测试里假解锁回调认的口令。
const testPassword = "correct horse battery staple"

// newLockedServer 造一个「锁定态启动 + 延迟解锁」的单密档服务（桌面外壳的形态）。
// 返回服务与假解锁回调记录到的调用次数。
func newLockedServer(t *testing.T, softLock bool) (*play.Server, *int) {
	t.Helper()
	priv := newKey(t)
	plain := fakeMP4(4096)
	dst := filepath.Join(t.TempDir(), "clip.mp4.svid")
	f, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Seal(strings.NewReader(string(plain)), f, &priv.PublicKey, int64(len(plain)), vault.ContainerMP4, testChunk); err != nil {
		f.Close()
		t.Fatalf("封存失败: %v", err)
	}
	f.Close()

	calls := 0
	s, err := play.New(play.Options{
		Name:        "clip.mp4.svid",
		IdleTimeout: time.Minute,
		SoftLock:    softLock,
		Unlock: func(pw string) (string, *vault.Reader, error) {
			calls++
			if pw != testPassword {
				return "", nil, errors.New("口令错误：无法解锁账户")
			}
			r, err := vault.Open(dst, priv)
			if err != nil {
				return "", nil, err
			}
			return "clip.mp4.svid", r, nil
		},
	})
	if err != nil {
		t.Fatalf("构造播放服务失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, &calls
}

// handlerProvider 同时匹配 *play.Server 与 *play.Library。
type handlerProvider interface{ Handler() http.Handler }

func newTestServer(t *testing.T, p handlerProvider) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(p.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// noRedirect 是不跟随重定向的客户端（用于断言 303 本身）。
func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func bodyOf(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	return string(b)
}

// authedClient 返回一个已登录（持会话 cookie）的客户端。
func authedClient(t *testing.T, ts *httptest.Server, password string) *http.Client {
	t.Helper()
	resp, err := noRedirect().PostForm(ts.URL+"/login", url.Values{"password": {password}})
	if err != nil {
		t.Fatalf("登录请求失败: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("登录状态码 = %d，期望 303", resp.StatusCode)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(ts.URL)
	jar.SetCookies(u, resp.Cookies())
	return &http.Client{Jar: jar}
}

// T1.1：锁定态只服务登录页；媒体地址被拒绝（AC1.1/AC1.6）。
func TestLockedGateServesLoginPageAndRejectsMedia(t *testing.T) {
	s, _ := newLockedServer(t, true)
	ts := newTestServer(t, s)

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("锁定态首页状态码 = %d，期望 200（登录页）", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("登录页 Cache-Control = %q，不得被缓存", cc)
	}
	html := bodyOf(t, resp)
	if !strings.Contains(html, `action="/login"`) {
		t.Fatalf("锁定态未渲染登录表单:\n%s", html)
	}
	if strings.Contains(html, "<video") {
		t.Fatal("锁定态泄露了播放页")
	}

	mresp, err := http.Get(ts.URL + s.MediaPath())
	if err != nil {
		t.Fatal(err)
	}
	if mresp.StatusCode != http.StatusForbidden {
		t.Fatalf("锁定态媒体地址状态码 = %d，期望 403", mresp.StatusCode)
	}
	mresp.Body.Close()

	// 目录索引等其它路径同样只给登录页
	other, err := http.Get(ts.URL + "/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if other.StatusCode != http.StatusOK || !strings.Contains(bodyOf(t, other), `action="/login"`) {
		t.Fatal("锁定态其它路径未返回登录页")
	}
}

// T1.2/T1.4：登录成功下发会话 cookie 并 303；内容路由要求会话（AC1.2）。
func TestLoginSetsSessionCookieAndRequiresIt(t *testing.T) {
	s, calls := newLockedServer(t, true)
	ts := newTestServer(t, s)

	resp, err := noRedirect().PostForm(ts.URL+"/login", url.Values{"password": {testPassword}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("登录成功状态码 = %d，期望 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("登录成功 Location = %q，期望 /", loc)
	}
	var session *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "sv_session" {
			session = c
		}
	}
	if session == nil {
		t.Fatal("登录成功未下发会话 cookie（AC1.2）")
	}
	if !session.HttpOnly {
		t.Fatal("会话 cookie 必须 HttpOnly")
	}
	if session.SameSite != http.SameSiteStrictMode {
		t.Fatalf("会话 cookie SameSite = %v，期望 Strict", session.SameSite)
	}
	if *calls != 1 {
		t.Fatalf("解锁回调调用次数 = %d，期望 1", *calls)
	}
	if s.Locked() {
		t.Fatal("登录成功后不应仍处于锁定态")
	}

	// 持会话 → 播放页
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(ts.URL)
	jar.SetCookies(u, resp.Cookies())
	authed := &http.Client{Jar: jar}
	pr, err := authed.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if pr.StatusCode != http.StatusOK || !strings.Contains(bodyOf(t, pr), "<video") {
		t.Fatal("持会话请求未得到播放页")
	}

	// 无会话（模拟本机其它页面盲扫端口）→ 拒绝
	anon, err := (&http.Client{}).Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if anon.StatusCode != http.StatusForbidden {
		t.Fatalf("无会话内容请求状态码 = %d，期望 403", anon.StatusCode)
	}
	anon.Body.Close()

	// 无会话访问媒体同样被拒
	amedia, err := (&http.Client{}).Get(ts.URL + s.MediaPath())
	if err != nil {
		t.Fatal(err)
	}
	if amedia.StatusCode != http.StatusForbidden {
		t.Fatalf("无会话媒体请求状态码 = %d，期望 403", amedia.StatusCode)
	}
	amedia.Body.Close()
}

// T1.3：口令错误 → 仍锁定、留在登录页、无 cookie、不启动任何内容（AC1.3）。
func TestLoginWrongPasswordKeepsLocked(t *testing.T) {
	s, _ := newLockedServer(t, true)
	ts := newTestServer(t, s)

	resp, err := noRedirect().PostForm(ts.URL+"/login", url.Values{"password": {"wrong-password"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("口令错误状态码 = %d，期望 200（重绘登录页）", resp.StatusCode)
	}
	if len(resp.Cookies()) != 0 {
		t.Fatal("口令错误不应下发 cookie")
	}
	html := bodyOf(t, resp)
	if !strings.Contains(html, "口令错误") {
		t.Fatalf("登录页未显示失败原因:\n%s", html)
	}
	if !s.Locked() {
		t.Fatal("口令错误后仍应是锁定态")
	}

	mresp, err := http.Get(ts.URL + s.MediaPath())
	if err != nil {
		t.Fatal(err)
	}
	if mresp.StatusCode != http.StatusForbidden {
		t.Fatalf("口令错误后媒体状态码 = %d，期望 403", mresp.StatusCode)
	}
	mresp.Body.Close()
}

// T1.3 续：登录页对目标描述做 HTML 转义（密档名可能含任意字符）。
func TestLoginPageEscapesTarget(t *testing.T) {
	evil := `<script>alert(1)</script>`
	priv := newKey(t)
	plain := fakeMP4(1024)
	dst := filepath.Join(t.TempDir(), "x.svid")
	f, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Seal(strings.NewReader(string(plain)), f, &priv.PublicKey, int64(len(plain)), vault.ContainerMP4, testChunk); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	s, err := play.New(play.Options{
		Name: evil,
		Unlock: func(string) (string, *vault.Reader, error) {
			r, err := vault.Open(dst, priv)
			if err != nil {
				return "", nil, err
			}
			return "x.svid", r, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ts := newTestServer(t, s)

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	html := bodyOf(t, resp)
	if strings.Contains(html, evil) {
		t.Fatal("登录页未转义目标描述，存在注入风险")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatalf("登录页未显示（转义后的）目标描述:\n%s", html)
	}
}

// T1.5：预解锁路径（CLI/测试）不出现登录页、不要求会话 cookie（AC6.2 的前提）。
func TestPreUnlockedNeverShowsLoginPage(t *testing.T) {
	plain := fakeMP4(2048)
	s, _ := newServer(t, plain, time.Minute, nil)
	ts := newTestServer(t, s)

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("预解锁首页状态码 = %d，期望 200", resp.StatusCode)
	}
	html := bodyOf(t, resp)
	if strings.Contains(html, `action="/login"`) {
		t.Fatal("预解锁路径不应出现登录页")
	}
	if !strings.Contains(html, "<video") {
		t.Fatal("预解锁路径应直接给播放页")
	}
	// 无 cookie 也能取到明文（预解锁下会话未启用）
	mresp, err := http.Get(ts.URL + s.MediaPath())
	if err != nil {
		t.Fatal(err)
	}
	defer mresp.Body.Close()
	if mresp.StatusCode != http.StatusOK {
		t.Fatalf("预解锁媒体状态码 = %d，期望 200", mresp.StatusCode)
	}
}

// T1.3：软锁定后回到登录页且服务存活；可再次解锁（AC3.1/AC3.2）。
func TestSoftLockReturnsToLoginPage(t *testing.T) {
	s, calls := newLockedServer(t, true)
	ts := newTestServer(t, s)
	client := authedClient(t, ts, testPassword)

	// 已解锁：媒体可用
	before, err := client.Get(ts.URL + s.MediaPath())
	if err != nil {
		t.Fatal(err)
	}
	before.Body.Close()
	if before.StatusCode != http.StatusOK {
		t.Fatalf("解锁后媒体状态码 = %d，期望 200", before.StatusCode)
	}

	s.Lock("测试锁定")
	if !s.Locked() {
		t.Fatal("锁定后 Locked() 应为 true")
	}

	// 服务存活：首页回到登录页
	resp, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("软锁定后服务不应关停: %v", err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(bodyOf(t, resp), `action="/login"`) {
		t.Fatal("软锁定后应回到登录页")
	}
	// 媒体被拒且不 panic
	mresp, err := client.Get(ts.URL + s.MediaPath())
	if err != nil {
		t.Fatalf("软锁定后媒体请求不应断开连接: %v", err)
	}
	mresp.Body.Close()
	if mresp.StatusCode != http.StatusForbidden {
		t.Fatalf("软锁定后媒体状态码 = %d，期望 403", mresp.StatusCode)
	}

	// 再次解锁：回调被再次调用，媒体恢复
	again := authedClient(t, ts, testPassword)
	if *calls != 2 {
		t.Fatalf("解锁回调调用次数 = %d，期望 2", *calls)
	}
	after, err := again.Get(ts.URL + s.MediaPath())
	if err != nil {
		t.Fatal(err)
	}
	after.Body.Close()
	if after.StatusCode != http.StatusOK {
		t.Fatalf("再次解锁后媒体状态码 = %d，期望 200", after.StatusCode)
	}
}

// T1.3：默认（非软锁定）锁定时关停服务，终态一律 410（CLI 既有行为，AC8.2）。
func TestHardLockClosesServiceWith410(t *testing.T) {
	s, _ := newLockedServer(t, false)
	ts := newTestServer(t, s)
	client := authedClient(t, ts, testPassword)

	page, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if page.StatusCode != http.StatusOK {
		t.Fatalf("解锁后首页状态码 = %d，期望 200", page.StatusCode)
	}
	page.Body.Close()

	s.Lock("测试锁定")
	if !s.Locked() {
		t.Fatal("锁定后 Locked() 应为 true")
	}
	resp, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("关停后状态码 = %d，期望 410", resp.StatusCode)
	}
}

// T1.3 续：目录模式（桌面最常用的形态）同样「锁定态只给登录页、解锁后可播、软锁定回到登录页」。
func TestLibraryLockedGateUnlockAndSoftLock(t *testing.T) {
	dir := t.TempDir()
	priv := newKey(t)
	plain := fakeMP4(3000)
	sealVault(t, dir, "clip.mp4.svid", priv, plain)
	entries, err := play.ScanLibrary(dir)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := play.NewLibrary(play.LibraryOptions{
		Entries:     entries,
		Target:      dir,
		IdleTimeout: time.Minute,
		SoftLock:    true,
		Unlock: func(pw string) (*ecdsa.PrivateKey, error) {
			if pw != testPassword {
				return nil, errors.New("口令错误：无法解锁账户")
			}
			return priv, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	ts := newTestServer(t, lib)

	// 锁定态：登录页 + 目标路径，且不列条目
	locked := httpGetString(t, ts.URL+"/")
	if !strings.Contains(locked, `action="/login"`) {
		t.Fatalf("目录模式锁定态未给登录页:\n%s", locked)
	}
	if !strings.Contains(locked, dir) {
		t.Fatalf("登录页未显示目标库路径:\n%s", locked)
	}
	if strings.Contains(locked, "clip.mp4.svid") {
		t.Fatal("锁定态泄露了密档列表")
	}

	// 解锁 → 索引页 → 播放页 → 媒体可用
	client := authedClient(t, ts, testPassword)
	index := bodyOf(t, mustGet(t, client, ts.URL+"/"))
	link := regexp.MustCompile(`href="(/watch/[0-9a-f]+)"`).FindStringSubmatch(index)
	if link == nil {
		t.Fatalf("解锁后索引页无条目:\n%s", index)
	}
	unlockClientPage := bodyOf(t, mustGet(t, client, ts.URL+link[1]))
	media := regexp.MustCompile(`src="(/media/[0-9a-f]+/[0-9a-f]+)"`).FindStringSubmatch(unlockClientPage)
	if media == nil {
		t.Fatalf("解锁后播放页无媒体地址:\n%s", unlockClientPage)
	}
	mresp := mustGet(t, client, ts.URL+media[1])
	if mresp.StatusCode != http.StatusOK {
		t.Fatalf("解锁后媒体状态码 = %d，期望 200", mresp.StatusCode)
	}
	got := bodyOf(t, mresp)
	if got != string(plain) {
		t.Fatal("目录模式解锁后拉流内容与明文不一致")
	}

	// 软锁定 → 回到登录页，服务存活，媒体被拒
	lib.Lock("测试锁定")
	if !lib.Locked() {
		t.Fatal("锁定后 Locked() 应为 true")
	}
	after := bodyOf(t, mustGet(t, client, ts.URL+"/"))
	if !strings.Contains(after, `action="/login"`) {
		t.Fatalf("软锁定后应回到登录页:\n%s", after)
	}
	lockedMedia := mustGet(t, client, ts.URL+media[1])
	lockedMedia.Body.Close()
	if lockedMedia.StatusCode != http.StatusForbidden {
		t.Fatalf("软锁定后媒体状态码 = %d，期望 403", lockedMedia.StatusCode)
	}
}

// mustGet 发一个 GET 并断言无传输错误（客户端可携带会话 cookie）。
func mustGet(t *testing.T, client *http.Client, u string) *http.Response {
	t.Helper()
	resp, err := client.Get(u)
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", u, err)
	}
	return resp
}
