package integration

import (
	"crypto/ecdsa"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cc14514/go-safemedia/internal/account"
	"github.com/cc14514/go-safemedia/internal/play"
)

// T7.8（L2）：桌面外壳形态的完整链路——锁定态起服务 → 登录 → 索引页 → 目录选择页 →
// 切换库根 → 新库按子目录结构展示且可播；锁定态访问 /library 一律回登录页。
//
// 对应 specs/002 US7（AC7.1–AC7.8）与 US1/US2 的库根行为。
func TestPickerFlowAcrossLibrarySwitch(t *testing.T) {
	dir := t.TempDir()
	const pw = "picker passphrase"
	keystore := newAccountForTest(t, dir, pw)

	// sealInto 把一段假 mp4 封存到 <库>/<相对路径>。
	sealInto := func(libName, rel string, n int) {
		t.Helper()
		host := filepath.Dir(filepath.Join(dir, libName, rel))
		if err := os.MkdirAll(host, 0o700); err != nil {
			t.Fatal(err)
		}
		src := filepath.Join(dir, "src.mp4")
		if err := os.WriteFile(src, fakeMP4(n), 0o644); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(dir, libName, rel)
		if out, err := runCLI(t, &script{secrets: []string{pw}}, "--account", keystore, "seal", src, "-o", dst); err != nil {
			t.Fatalf("seal %s/%s 失败: %v\n%s", libName, rel, err, out)
		}
	}
	libA := filepath.Join(dir, "alpha")
	libB := filepath.Join(dir, "beta")
	sealInto("alpha", "one.svid", 5000)
	sealInto("beta", "two.svid", 6100)
	sealInto("beta", filepath.Join("nested", "three.svid"), 2500)

	entriesA, err := play.ScanLibrary(libA)
	if err != nil {
		t.Fatal(err)
	}
	if len(entriesA) != 1 {
		t.Fatalf("库 A 密档数 = %d，期望 1", len(entriesA))
	}

	var persisted []string
	lib, err := play.NewLibrary(play.LibraryOptions{
		Entries:     entriesA,
		Root:        libA,
		Target:      libA,
		SoftLock:    true, // 外壳模式：锁定回登录页，不关停
		IdleTimeout: play.DefaultIdleTimeout,
		Unlock: func(password string) (*ecdsa.PrivateKey, error) {
			acct, err := account.LoadKeystoreWithPassphrase(keystore, []byte(password))
			if err != nil {
				return nil, err
			}
			return acct.PrivateKey(), nil
		},
		OnLibraryChange: func(root string) error { persisted = append(persisted, root); return nil },
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
	noredir := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	client := &http.Client{Jar: jar}

	// ① 锁定态：/library 不出目录选择页（锁定态不得枚举本机目录，AC7.6）
	locked := get(t, client, base+"library")
	if locked.code != http.StatusOK || !strings.Contains(locked.body, `action="/login"`) {
		t.Fatalf("锁定态 /library 应为登录页：code=%d\n%s", locked.code, locked.body)
	}
	if strings.Contains(locked.body, "选择密档库") {
		t.Fatalf("锁定态泄露了目录选择页:\n%s", locked.body)
	}

	// ② 口令解锁
	if lr := postForm(t, noredir, base+"login", url.Values{"password": {pw}}); lr.code != http.StatusSeeOther {
		t.Fatalf("登录失败：code=%d\n%s", lr.code, lr.body)
	}

	// ③ 索引页显示当前库根与目录选择入口（AC2.1）
	idx := get(t, client, base)
	if idx.code != http.StatusOK || !strings.Contains(idx.body, libA) || !strings.Contains(idx.body, "选择密档库") {
		t.Fatalf("索引页未显示库根/选择入口：code=%d\n%s", idx.code, idx.body)
	}

	// ④ 目录选择页默认浏览当前库根，并给出本目录密档数（AC7.1）
	pick := get(t, client, base+"library")
	if pick.code != http.StatusOK {
		t.Fatalf("选择页状态码 = %d", pick.code)
	}
	for _, want := range []string{"选择密档库", "当前库根", libA, "本目录直接包含 1 个密档", "使用此目录作为库根"} {
		if !strings.Contains(pick.body, want) {
			t.Fatalf("选择页缺少 %q:\n%s", want, pick.body)
		}
	}

	// ⑤ 可在选择页里浏览到上级目录（面包屑/上级链接，AC7.2）
	up := get(t, client, base+"library?path="+url.QueryEscape(dir))
	if up.code != http.StatusOK || !strings.Contains(up.body, ">alpha/</a>") || !strings.Contains(up.body, ">beta/</a>") {
		t.Fatalf("未能在选择页浏览上级目录：code=%d\n%s", up.code, up.body)
	}

	// ⑥ 非法路径：页面内提示，库根不变（AC7.5）
	bad := postForm(t, noredir, base+"library", url.Values{"path": {filepath.Join(dir, "nope")}})
	if bad.code != http.StatusOK || !strings.Contains(bad.body, "目录不可用") {
		t.Fatalf("非法库根未在页面内提示：code=%d\n%s", bad.code, bad.body)
	}
	if after := get(t, client, base); !strings.Contains(after.body, libA) {
		t.Fatalf("非法路径改动了库根:\n%s", after.body)
	}

	// ⑦ 切换库根到 B → 303 回索引页，并按新库的子目录结构展示（AC7.3/AC7.4）
	sw := postForm(t, noredir, base+"library", url.Values{"path": {libB}})
	if sw.code != http.StatusSeeOther {
		t.Fatalf("切换库根状态码 = %d，期望 303\n%s", sw.code, sw.body)
	}
	if len(persisted) != 1 || persisted[0] != libB {
		t.Fatalf("持久化回调收到 %v，期望 [%s]", persisted, libB)
	}
	idxB := get(t, client, base)
	if idxB.code != http.StatusOK || !strings.Contains(idxB.body, libB) {
		t.Fatalf("索引页未显示新库根：code=%d\n%s", idxB.code, idxB.body)
	}
	if !strings.Contains(idxB.body, "<summary>nested</summary>") {
		t.Fatalf("新库未按子目录结构展示:\n%s", idxB.body)
	}
	if strings.Contains(idxB.body, "one.svid") {
		t.Fatalf("索引页仍显示旧库条目:\n%s", idxB.body)
	}

	// ⑧ 新库里的密档要能启封拉取（重扫后 id 重新分配，媒体链路仍通，AC7.3）
	watch := get(t, client, findWatchURL(t, idxB.body, base))
	if watch.code != http.StatusOK {
		t.Fatalf("新库播放页状态码 = %d", watch.code)
	}
	media := get(t, client, findMediaURL(t, watch.body, base))
	if media.code != http.StatusOK || (len(media.bytes) != 2500 && len(media.bytes) != 6100) {
		t.Fatalf("新库媒体拉取失败：code=%d len=%d（期望 2500 或 6100）", media.code, len(media.bytes))
	}
}
