package play_test

import (
	"crypto/ecdsa"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cc14514/go-safemedia/internal/play"
)

// 两个「账户文件路径」用假路径即可：引擎只把账户标识当作**不透明令牌**在列表内比对，
// 真正的读取/解密发生在调用方注入的 Unlock 回调里（AC8.4：列表外的标识根本不会触达读取逻辑）。
const (
	acctA = "/Users/me/.safemedia/keystore.json"
	acctB = "/Users/me/.safemedia/accounts/工作.json"
)

// passA / passB 分别是账户 A / B 的口令，用来构造「选错账户」的场景（AC8.3）。
const (
	passA = "account-a passphrase"
	passB = "account-b passphrase"
)

// newAccountsLibrary 造一个「锁定态启动 + 多账户」的库服务（桌面外壳形态）。
// 返回服务、假解锁回调的调用次数、以及最后一次收到的账户标识。
// accounts=false 时不注入列表（CLI 形态：没有账户选择器）。
func newAccountsLibrary(t *testing.T, accounts bool) (*play.Library, *int, *string) {
	t.Helper()
	priv := newKey(t)
	root := newPickerRoot(t, priv)
	entries, err := play.ScanLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	last := ""
	opts := play.LibraryOptions{
		Entries:     entries,
		Root:        root,
		Target:      root,
		SoftLock:    true,
		IdleTimeout: time.Minute,
		Unlock: func(accountID, pw string) (*ecdsa.PrivateKey, error) {
			calls++
			last = accountID
			switch {
			case accountID == "" && pw == passA: // 无账户列表的 CLI 形态
				return priv, nil
			case accountID == acctA && pw == passA:
				return priv, nil
			case accountID == acctB && pw == passB:
				return priv, nil
			default:
				// 桌面外壳把「选错账户/口令错误」统一折叠成这句话（AC8.3）。
				return nil, errors.New("口令错误或与所选账户不匹配")
			}
		},
	}
	if accounts {
		opts.Accounts = func() []play.AccountOption {
			return []play.AccountOption{
				{ID: acctA, Label: "keystore.json — 0x1a2b…9f8e"},
				{ID: acctB, Label: "工作.json — 0x9e7d…da40"},
			}
		}
	}
	lib, err := play.NewLibrary(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lib.Close() })
	return lib, &calls, &last
}

// postLogin 提交登录表单，返回响应（不跟随重定向）。
func postLogin(t *testing.T, ts string, form url.Values) *http.Response {
	t.Helper()
	resp, err := noRedirect().PostForm(ts+"/login", form)
	if err != nil {
		t.Fatalf("登录请求失败: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// sessionCookie 取出会话 cookie（没有则返回 nil）。
func sessionCookie(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == "sv_session" {
			return c
		}
	}
	return nil
}

// T8.3a（AC8.1）：登录页列出注入的账户，首项默认选中，显示文件名 + 地址短形式。
func TestLoginPageListsAccountsWithDefaultSelection(t *testing.T) {
	lib, _, _ := newAccountsLibrary(t, true)
	ts := newTestServer(t, lib)

	page := bodyOf(t, mustGet(t, http.DefaultClient, ts.URL+"/"))
	sel := regexp.MustCompile(`(?s)<select id="account".*?</select>`).FindString(page)
	if sel == "" {
		t.Fatalf("登录页没有账户选择器:\n%s", page)
	}
	if n := strings.Count(sel, "<option"); n != 2 {
		t.Fatalf("选择器有 %d 项，期望 2：%s", n, sel)
	}
	// 首项默认选中（配置指定的账户由调用方排在首位）
	if !strings.Contains(sel, `value="`+acctA+`" selected`) {
		t.Fatalf("首项未默认选中: %s", sel)
	}
	if strings.Contains(sel, `value="`+acctB+`" selected`) {
		t.Fatalf("第二项不该被选中: %s", sel)
	}
	// 名称 + 地址短形式都在标签里
	for _, want := range []string{"keystore.json", "0x1a2b…9f8e", "工作.json", "0x9e7d…da40"} {
		if !strings.Contains(sel, want) {
			t.Fatalf("标签缺少 %q：%s", want, sel)
		}
	}
	// 选择器在表单里、method=post（原生控件，不用 JS 提交）
	if !strings.Contains(page, `method="post"`) || !strings.Contains(page, `action="/login"`) {
		t.Fatalf("登录表单不是 POST /login:\n%s", page)
	}
}

// T8.3b（AC8.2/AC8.4）：选对账户 + 正确口令 → 303 + 会话 cookie；回调收到的标识与提交值一致。
func TestLoginSelectsSubmittedAccountOnSuccess(t *testing.T) {
	lib, calls, last := newAccountsLibrary(t, true)
	ts := newTestServer(t, lib)

	resp := postLogin(t, ts.URL, url.Values{"account": {acctB}, "password": {passB}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("登录状态码 = %d，期望 303", resp.StatusCode)
	}
	if sessionCookie(resp) == nil {
		t.Fatal("登录成功未下发会话 cookie（AC8.2）")
	}
	if *last != acctB {
		t.Fatalf("解锁回调收到的账户标识 = %q，期望 %q", *last, acctB)
	}
	if *calls != 1 {
		t.Fatalf("解锁回调调用次数 = %d，期望 1", *calls)
	}
	if lib.Locked() {
		t.Fatal("登录成功后不应仍处于锁定态")
	}
}

// T8.3c（AC8.4）：列表外的账户标识（伪造/篡改）在**调用解锁回调之前**被拒，且不建会话。
func TestLoginRejectsAccountOutsideListBeforeUnlock(t *testing.T) {
	lib, calls, _ := newAccountsLibrary(t, true)
	ts := newTestServer(t, lib)

	resp := postLogin(t, ts.URL, url.Values{
		"account":  {"/etc/passwd"},
		"password": {passA},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200（重绘登录页）", resp.StatusCode)
	}
	if body := bodyOf(t, resp); !strings.Contains(body, "请选择一个可用账户") {
		t.Fatalf("未提示账户不可用:\n%s", body)
	}
	if *calls != 0 {
		t.Fatalf("伪造标识不该触达解锁回调，实际调用 %d 次", *calls)
	}
	if sessionCookie(resp) != nil {
		t.Fatal("被拒的登录不该下发会话 cookie")
	}
	if !lib.Locked() {
		t.Fatal("被拒后应仍处于锁定态")
	}
}

// T8.3d（AC8.3）：选错账户（用别的账户的口令）→ 中文原因、无会话、**保持上次选中的账户**。
func TestLoginWrongAccountKeepsSelectionAndRejects(t *testing.T) {
	lib, calls, _ := newAccountsLibrary(t, true)
	ts := newTestServer(t, lib)

	resp := postLogin(t, ts.URL, url.Values{"account": {acctB}, "password": {passA}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200（重绘登录页）", resp.StatusCode)
	}
	body := bodyOf(t, resp)
	if !strings.Contains(body, "口令错误或与所选账户不匹配") {
		t.Fatalf("未给出选错账户的中文原因:\n%s", body)
	}
	if sessionCookie(resp) != nil {
		t.Fatal("口令错误不该下发会话 cookie（AC8.3）")
	}
	if *calls != 1 {
		t.Fatalf("解锁回调调用次数 = %d，期望 1", *calls)
	}
	if !lib.Locked() {
		t.Fatal("口令错误后应仍处于锁定态")
	}
	// 重绘后仍保持刚才选中的账户（不让用户重新找一遍）
	if !strings.Contains(body, `value="`+acctB+`" selected`) {
		t.Fatalf("重绘后未保持选中的账户 %s:\n%s", acctB, body)
	}

	// 换成正确口令即可解锁（同一服务、无需重启）
	resp2 := postLogin(t, ts.URL, url.Values{"account": {acctB}, "password": {passB}})
	if resp2.StatusCode != http.StatusSeeOther {
		t.Fatalf("正确口令状态码 = %d，期望 303", resp2.StatusCode)
	}
}

// T8.3e（AC8.5 / CLI 形态）：不注入账户列表 → 页面没有选择器，账户标识以空串透传。
func TestLoginWithoutAccountListPassesEmptyID(t *testing.T) {
	lib, calls, last := newAccountsLibrary(t, false)
	ts := newTestServer(t, lib)

	page := bodyOf(t, mustGet(t, http.DefaultClient, ts.URL+"/"))
	if strings.Contains(page, `<select id="account"`) {
		t.Fatalf("未注入账户列表却渲染了选择器:\n%s", page)
	}
	if strings.Contains(page, `name="account"`) {
		t.Fatalf("未注入账户列表却渲染了 account 字段:\n%s", page)
	}

	resp := postLogin(t, ts.URL, url.Values{"password": {passA}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("状态码 = %d，期望 303", resp.StatusCode)
	}
	if *calls != 1 || *last != "" {
		t.Fatalf("回调调用次数 = %d、账户标识 = %q，期望 1 与空串", *calls, *last)
	}
}
