package play_test

import (
	"crypto/ecdsa"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cc14514/go-safemedia/internal/play"
)

// ---- 夹具 ----

// newPickerRoot 造一个用于目录选择器的真实密档库：
//
//	root/a/x1.svid root/a/x2.svid root/b/y.svid  （真实密档，可启封播放）
//	root/c/                                      （空目录）
//	root/.hidden/h.svid                          （隐藏目录，应跳过）
//	root/notes.txt                               （非密档文件）
//	root/link -> root/a                          （符号链接，应跳过）
func newPickerRoot(t *testing.T, priv *ecdsa.PrivateKey) string {
	t.Helper()
	root := t.TempDir()
	for _, sub := range []string{"a", "b", "c", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sealVault(t, root, filepath.Join("a", "x1.svid"), priv, fakeMP4(3000))
	sealVault(t, root, filepath.Join("a", "x2.svid"), priv, fakeMP4(2500))
	sealVault(t, root, filepath.Join("b", "y.svid"), priv, fakeMP4(1200))
	mustWrite(t, filepath.Join(root, ".hidden", "h.svid"), []byte("x"))
	mustWrite(t, filepath.Join(root, "notes.txt"), []byte("x"))
	if err := os.Symlink(filepath.Join(root, "a"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	return root
}

// newUnlockedPicker 造一个已解锁（预置私钥）的库服务；onChange 可为 nil。
func newUnlockedPicker(t *testing.T, root string, priv *ecdsa.PrivateKey, onChange func(string) error) *play.Library {
	t.Helper()
	entries, err := play.ScanLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := play.NewLibrary(play.LibraryOptions{
		Priv:            priv,
		Entries:         entries,
		Root:            root,
		Target:          root,
		IdleTimeout:     time.Minute,
		OnLibraryChange: onChange,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lib.Close() })
	return lib
}

// newLockedPicker 造一个「锁定态启动 + 口令解锁」的库服务（桌面外壳形态）。
func newLockedPicker(t *testing.T, root string, priv *ecdsa.PrivateKey) *play.Library {
	t.Helper()
	entries, err := play.ScanLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := play.NewLibrary(play.LibraryOptions{
		Entries:     entries,
		Root:        root,
		Target:      root,
		SoftLock:    true,
		IdleTimeout: time.Minute,
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
	t.Cleanup(func() { lib.Close() })
	return lib
}

// findLink 从页面里取出第一个匹配 pattern 的链接地址（pattern 不含引号）。
func findLink(t *testing.T, html, pattern string) string {
	t.Helper()
	m := regexp.MustCompile(`"(` + pattern + `)"`).FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("页面里找不到 %s 链接:\n%s", pattern, html)
	}
	return m[1]
}

// mustReadAll 读完响应体。
func mustReadAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// T7.1：目录选择页列出子目录（含直含密档数），跳过隐藏项、符号链接与非目录（AC7.1/AC7.2）。
func TestPickerListsSubdirectoriesAndSkipsHidden(t *testing.T) {
	priv := newKey(t)
	root := newPickerRoot(t, priv)
	ts := newTestServer(t, newUnlockedPicker(t, root, priv, nil))

	resp := mustGet(t, http.DefaultClient, ts.URL+"/library")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("选择页状态码 = %d，期望 200", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("选择页 Cache-Control = %q，期望 no-store", cc)
	}
	html := bodyOf(t, resp)
	for _, want := range []string{
		"选择密档库", root, `/library?path=`, ">a/</a>", ">b/</a>", ">c/</a>",
		"2 个密档 · 0 个子目录", "1 个密档 · 0 个子目录",
		`action="/library"`, "使用此目录作为库根",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("选择页缺少 %q:\n%s", want, html)
		}
	}
	for _, bad := range []string{".hidden", "notes.txt", `>link/</a>`} {
		if strings.Contains(html, bad) {
			t.Fatalf("选择页不该列出 %q:\n%s", bad, html)
		}
	}
}

// T7.2：下钻子目录与面包屑同步；非法路径回退到当前库根并给出原因（AC7.2/AC7.5）。
func TestPickerDrillsDownAndHandlesBadPath(t *testing.T) {
	priv := newKey(t)
	root := newPickerRoot(t, priv)
	ts := newTestServer(t, newUnlockedPicker(t, root, priv, nil))

	sub := filepath.Join(root, "a")
	html := bodyOf(t, mustGet(t, http.DefaultClient, ts.URL+"/library?path="+url.QueryEscape(sub)))
	for _, want := range []string{
		"本目录直接包含 2 个密档",
		`<span class="cur">a</span>`,
		`value="` + sub + `"`,
		"↑ 上级目录",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("下钻页面缺少 %q:\n%s", want, html)
		}
	}

	// 非法路径：仍然 200（页面内提示），回退浏览当前库根，说明原因。
	resp := mustGet(t, http.DefaultClient, ts.URL+"/library?path="+url.QueryEscape(filepath.Join(root, "nope")))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("非法路径状态码 = %d，期望 200", resp.StatusCode)
	}
	bad := bodyOf(t, resp)
	if !strings.Contains(bad, "目录不可用") {
		t.Fatalf("非法路径未给出中文原因:\n%s", bad)
	}
	if !strings.Contains(bad, ">a/</a>") {
		t.Fatalf("非法路径未回退到当前库根:\n%s", bad)
	}
}

// T7.3：POST 切换库根 → 303 回索引页、立即重扫、新树可播、回调收到绝对路径（AC7.3/AC7.4）。
func TestPickerSwitchRootRescansAndNotifies(t *testing.T) {
	priv := newKey(t)
	rootA := newPickerRoot(t, priv)
	rootB := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootB, "z"), 0o700); err != nil {
		t.Fatal(err)
	}
	sealVault(t, rootB, filepath.Join("z", "new.svid"), priv, fakeMP4(2048))

	var seen []string
	lib := newUnlockedPicker(t, rootA, priv, func(r string) error {
		seen = append(seen, r)
		return nil
	})
	ts := newTestServer(t, lib)

	resp, err := noRedirect().PostForm(ts.URL+"/library", url.Values{"path": {rootB}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("切换库根状态码 = %d，期望 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("切换后 Location = %q，期望 /", loc)
	}
	if len(seen) != 1 || seen[0] != rootB {
		t.Fatalf("OnLibraryChange 收到 %v，期望 [%s]", seen, rootB)
	}
	if lib.Root() != rootB {
		t.Fatalf("库根 = %q，期望 %q", lib.Root(), rootB)
	}
	if lib.Count() != 1 {
		t.Fatalf("重扫后条目数 = %d，期望 1", lib.Count())
	}

	idx := bodyOf(t, mustGet(t, http.DefaultClient, ts.URL+"/"))
	if !strings.Contains(idx, rootB) || !strings.Contains(idx, "<summary>z</summary>") {
		t.Fatalf("索引页未按新库根展示:\n%s", idx)
	}
	if strings.Contains(idx, "<summary>a</summary>") {
		t.Fatalf("索引页仍显示旧库根内容:\n%s", idx)
	}
	if !strings.Contains(idx, "选择密档库") {
		t.Fatalf("索引页缺少目录选择入口:\n%s", idx)
	}

	// 新库里的密档要能真的启封播放（重扫后 id 重新分配，整条媒体链路仍通）。
	watch := findLink(t, idx, `/watch/[0-9a-f]+`)
	page := bodyOf(t, mustGet(t, http.DefaultClient, ts.URL+watch))
	media := findLink(t, page, `/media/[0-9a-f]+/[0-9a-f]+`)
	mr := mustGet(t, http.DefaultClient, ts.URL+media)
	defer mr.Body.Close()
	if mr.StatusCode != http.StatusOK && mr.StatusCode != http.StatusPartialContent {
		t.Fatalf("切换后媒体请求状态码 = %d", mr.StatusCode)
	}
	if n := len(mustReadAll(t, mr)); n != 2048 {
		t.Fatalf("切换后媒体长度 = %d，期望 2048", n)
	}
}

// T7.4：非法库根一律不改动现有状态（不 panic、不清空目录树）（AC7.5）。
func TestPickerRejectsInvalidRootsAndKeepsState(t *testing.T) {
	priv := newKey(t)
	root := newPickerRoot(t, priv)
	lib := newUnlockedPicker(t, root, priv, nil)
	ts := newTestServer(t, lib)

	cases := []struct{ name, path, want string }{
		{"不存在", filepath.Join(root, "nope"), "目录不可用"},
		{"是文件", filepath.Join(root, "notes.txt"), "不是目录"},
		{"空路径", "   ", "请填写目录路径"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := noRedirect().PostForm(ts.URL+"/library", url.Values{"path": {tc.path}})
			if err != nil {
				t.Fatal(err)
			}
			html := bodyOf(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("状态码 = %d，期望 200（页面内提示错误）", resp.StatusCode)
			}
			if !strings.Contains(html, tc.want) {
				t.Fatalf("未给出 %q:\n%s", tc.want, html)
			}
			if lib.Root() != root {
				t.Fatalf("非法路径改动了库根: %q", lib.Root())
			}
			if lib.Count() != 3 {
				t.Fatalf("非法路径改动了条目数: %d", lib.Count())
			}
			idx := bodyOf(t, mustGet(t, http.DefaultClient, ts.URL+"/"))
			if !strings.Contains(idx, "<summary>a</summary>") || !strings.Contains(idx, "<summary>b</summary>") {
				t.Fatalf("非法路径破坏了目录树:\n%s", idx)
			}
		})
	}
}

// T7.5：持久化失败必须可见，但库根已生效（不静默丢选择）（AC7.4）。
func TestPickerPersistFailureIsVisible(t *testing.T) {
	priv := newKey(t)
	rootA := newPickerRoot(t, priv)
	rootB := t.TempDir()
	lib := newUnlockedPicker(t, rootA, priv, func(string) error { return errors.New("磁盘只读") })
	ts := newTestServer(t, lib)

	resp, err := noRedirect().PostForm(ts.URL+"/library", url.Values{"path": {rootB}})
	if err != nil {
		t.Fatal(err)
	}
	html := bodyOf(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200（页面内提示）", resp.StatusCode)
	}
	if !strings.Contains(html, "保存配置失败") || !strings.Contains(html, "磁盘只读") {
		t.Fatalf("持久化失败未在页面可见:\n%s", html)
	}
	if lib.Root() != rootB {
		t.Fatalf("库根 = %q，期望已切换到 %q", lib.Root(), rootB)
	}
}

// T7.6：锁定态不得进入目录选择页（枚举本机目录）（AC7.6 / NFR-8）。
func TestPickerRequiresUnlockedSession(t *testing.T) {
	priv := newKey(t)
	root := newPickerRoot(t, priv)
	lib := newLockedPicker(t, root, priv)
	ts := newTestServer(t, lib)

	html := bodyOf(t, mustGet(t, http.DefaultClient, ts.URL+"/library"))
	if !strings.Contains(html, `action="/login"`) || strings.Contains(html, "选择密档库") {
		t.Fatalf("锁定态未回登录页:\n%s", html)
	}
	resp, err := noRedirect().PostForm(ts.URL+"/library", url.Values{"path": {t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	posted := bodyOf(t, resp)
	if !strings.Contains(posted, `action="/login"`) {
		t.Fatalf("锁定态 POST 未回登录页:\n%s", posted)
	}
	if lib.Root() != root {
		t.Fatalf("锁定态改动了库根: %q", lib.Root())
	}

	// 解锁后可正常使用选择页。
	client := authedClient(t, ts, testPassword)
	ok := bodyOf(t, mustGet(t, client, ts.URL+"/library"))
	if !strings.Contains(ok, "选择密档库") || !strings.Contains(ok, ">a/</a>") {
		t.Fatalf("解锁后仍进不了选择页:\n%s", ok)
	}
}

// T7.6（配合 -race）：并发「切换库根 + 索引/选择页/播放页/媒体请求」不得有数据竞争。
func TestPickerSetRootUnderConcurrentTraffic(t *testing.T) {
	priv := newKey(t)
	rootA := newPickerRoot(t, priv)
	rootB := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootB, "z"), 0o700); err != nil {
		t.Fatal(err)
	}
	sealVault(t, rootB, filepath.Join("z", "new.svid"), priv, fakeMP4(1024))

	lib := newUnlockedPicker(t, rootA, priv, nil)
	ts := newTestServer(t, lib)

	// 先取一次 watch / media 链接（切换后 id 会变，403/404 都无所谓，只求并发压到同一批状态）。
	idx := bodyOf(t, mustGet(t, http.DefaultClient, ts.URL+"/"))
	watch := findLink(t, idx, `/watch/[0-9a-f]+`)
	page := bodyOf(t, mustGet(t, http.DefaultClient, ts.URL+watch))
	media := findLink(t, page, `/media/[0-9a-f]+/[0-9a-f]+`)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	hit := func(u string) {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			resp, err := http.Get(u)
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}
	}
	for _, u := range []string{ts.URL + "/", ts.URL + "/library", ts.URL + watch, ts.URL + media} {
		wg.Add(1)
		go hit(u)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		client := noRedirect()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			target := rootA
			if i%2 == 1 {
				target = rootB
			}
			resp, err := client.PostForm(ts.URL+"/library", url.Values{"path": {target}})
			if err == nil {
				resp.Body.Close()
			}
		}
	}()

	time.Sleep(250 * time.Millisecond)
	close(stop)
	wg.Wait()
}
