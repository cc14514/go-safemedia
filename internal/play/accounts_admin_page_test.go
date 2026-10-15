package play_test

import (
	"crypto/ecdsa"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/cc14514/go-safemedia/internal/play"
)

// fakeAdmin 记录引擎对账户管理动作的每一次调用（AC9.1–9.3/9.8/9.12）。
type fakeAdmin struct {
	mu        sync.Mutex
	creates   []string
	confirms  []string
	answers   [][]string
	cancels   []string
	recovers  []string
	deletes   [][2]string
	challenge play.CreateChallenge
	createErr error
	confErr   error
	recoverEr error
	deleteErr error
}

func (f *fakeAdmin) DefaultTargetName() string { return "keystore.json" }

func (f *fakeAdmin) Create(name, password string, words int) (play.CreateChallenge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return play.CreateChallenge{}, f.createErr
	}
	f.creates = append(f.creates, name)
	return f.challenge, nil
}

func (f *fakeAdmin) ConfirmCreate(token string, answers []string) (play.AccountOption, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.confErr != nil {
		return play.AccountOption{}, f.confErr
	}
	f.confirms = append(f.confirms, token)
	f.answers = append(f.answers, answers)
	return play.AccountOption{ID: "/tmp/new.json", Label: "new.json — 0x1111…2222"}, nil
}

func (f *fakeAdmin) CancelCreate(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancels = append(f.cancels, token)
}

func (f *fakeAdmin) RecoverFromMnemonic(name, mnemonic, password string) (play.AccountOption, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.recoverEr != nil {
		return play.AccountOption{}, f.recoverEr
	}
	f.recovers = append(f.recovers, "mnemonic:"+name)
	return play.AccountOption{ID: "/tmp/r.json", Label: "r.json"}, nil
}

func (f *fakeAdmin) RecoverFromRecoveryFile(name, src, recoveryPassword, newPassword string) (play.AccountOption, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.recoverEr != nil {
		return play.AccountOption{}, f.recoverEr
	}
	f.recovers = append(f.recovers, "file:"+src)
	return play.AccountOption{ID: "/tmp/r.json", Label: "r.json"}, nil
}

func (f *fakeAdmin) Delete(path, password string) (play.DeleteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return play.DeleteResult{}, f.deleteErr
	}
	f.deletes = append(f.deletes, [2]string{path, password})
	return play.DeleteResult{Trashed: "/tmp/trash/x-" + path, WasConfigured: true}, nil
}

func (f *fakeAdmin) counts() (int, int, int, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.creates), len(f.confirms), len(f.cancels), len(f.recovers), len(f.deletes)
}

// newAdminServer 复用既有夹具，附加账户管理接口（返回服务根 URL）。
func newAdminServer(t *testing.T, admin play.AccountsAdmin, accounts bool) string {
	t.Helper()
	priv := newKey(t)
	root := newPickerRoot(t, priv)
	entries, err := play.ScanLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	opts := play.LibraryOptions{
		Entries:       entries,
		Root:          root,
		Target:        root,
		SoftLock:      true,
		Unlock:        func(string, string) (*ecdsa.PrivateKey, error) { return priv, nil },
		AccountsAdmin: admin,
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
	ts := newTestServer(t, lib)
	return ts.URL
}

func postForm(t *testing.T, ts, path string, form url.Values) *http.Response {
	t.Helper()
	resp, err := noRedirect().PostForm(ts+path, form)
	if err != nil {
		t.Fatalf("POST %s 失败: %v", path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// T9.5a（AC9.1/AC9.10）：`/accounts*` 只在锁定态可用；没有管理实现时 404。
func TestAccountsRoutesLockedOnly(t *testing.T) {
	admin := &fakeAdmin{challenge: play.CreateChallenge{Token: "tk1", Mnemonic: "a b c", Indexes: []int{1, 2, 3}}}
	base := newAdminServer(t, admin, true)

	// 锁定态：管理页可达（创建/恢复/删除都在锁定态进行）。
	page := bodyOf(t, mustGet(t, http.DefaultClient, base+"/accounts"))
	if !strings.Contains(page, "账户管理") {
		t.Fatalf("锁定态应能看到账户管理页:\n%s", page)
	}

	// 无管理实现（CLI 形态）→ 404，且不泄露任何账户信息。
	cli := newAdminServer(t, nil, false)
	resp := mustGet(t, http.DefaultClient, cli+"/accounts")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("未启用管理时应 404，得到 %d", resp.StatusCode)
	}

	// 已解锁 → 管理路由一律 303 回索引（AC9.10）。
	priv := newKey(t)
	root := newPickerRoot(t, priv)
	entries, err := play.ScanLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	unlocked, err := play.NewLibrary(play.LibraryOptions{
		Priv: priv, Entries: entries, Root: root, Target: root, AccountsAdmin: admin,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unlocked.Close() })
	ts := newTestServer(t, unlocked)
	for _, p := range []string{"/accounts", "/accounts/new", "/accounts/recover", "/accounts/delete"} {
		r := mustGet(t, noRedirect(), ts.URL+p)
		if r.StatusCode != http.StatusSeeOther {
			t.Fatalf("已解锁时 %s 应 303，得到 %d", p, r.StatusCode)
		}
	}
}

// T9.5b（AC9.1）：管理页列出账户（名称 + 短地址 + 配置指定标记）与三个动作入口。
func TestAccountsPageListsAndOffersActions(t *testing.T) {
	admin := &fakeAdmin{}
	base := newAdminServer(t, admin, true)
	page := bodyOf(t, mustGet(t, http.DefaultClient, base+"/accounts"))

	for _, want := range []string{"keystore.json", "0x1a2b…9f8e", "工作.json", "创建账户", "恢复账户", "删除"} {
		if !strings.Contains(page, want) {
			t.Fatalf("管理页缺少 %q:\n%s", want, page)
		}
	}
	// 可点元素必须是按钮或整行可点，不得是裸链接（AC7.9）。
	if regexp.MustCompile(`(?s)<a(?:\s[^>]*)?>`).MatchString(page) {
		for _, m := range regexp.MustCompile(`(?s)<a[^>]*>`).FindAllString(page, -1) {
			if !strings.Contains(m, `class="btn`) {
				t.Fatalf("出现非按钮链接: %s", m)
			}
		}
	}
	// 空列表时给出空状态提示。
	empty := newAdminServer(t, admin, false)
	if p := bodyOf(t, mustGet(t, http.DefaultClient, empty+"/accounts")); !strings.Contains(p, "还没有账户") {
		t.Fatalf("空列表应有空状态提示:\n%s", p)
	}
}

// T9.5c（AC9.2/AC9.3/AC9.12）：创建分两步 —— 提交后展示助记词并抽查；
// 抽查通过才调用实现；助记词页不得有「复制」按钮、必须 no-store。
func TestCreateFlowRequiresMnemonicConfirmation(t *testing.T) {
	admin := &fakeAdmin{challenge: play.CreateChallenge{
		Token:    "tk-123",
		Mnemonic: "alpha bravo charlie delta echo foxtrot",
		Indexes:  []int{2, 5, 6},
	}}
	base := newAdminServer(t, admin, true)

	// 表单页
	form := bodyOf(t, mustGet(t, http.DefaultClient, base+"/accounts/new"))
	if !strings.Contains(form, `action="/accounts/new"`) {
		t.Fatalf("创建页应有 POST /accounts/new 表单:\n%s", form)
	}
	// 红线提示只在创建页（AC9.5）。
	if !strings.Contains(form, "绝不导入") {
		t.Fatalf("创建页应显示红线提示:\n%s", form)
	}

	// 两次口令不一致 → 不调用实现，页面报错。
	resp := postForm(t, base, "/accounts/new", url.Values{
		"name": {"keystore.json"}, "password": {"pw-one-two-three"}, "password2": {"pw-one-two-four"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("口令不一致应重绘页面（200），得到 %d", resp.StatusCode)
	}
	if c, _, _, _, _ := admin.counts(); c != 0 {
		t.Fatal("口令不一致不得触达创建逻辑")
	}

	// 口令一致 → 303 到抽查页。
	resp = postForm(t, base, "/accounts/new", url.Values{
		"name": {"keystore.json"}, "password": {"pw-one-two-three"}, "password2": {"pw-one-two-three"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("应 303 到抽查页，得到 %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "token=tk-123") {
		t.Fatalf("抽查页应带上待确认 token，得到 %s", loc)
	}

	// 抽查页：编号网格 + 抽查序号 + 无「复制」按钮 + no-store。
	resp = mustGet(t, http.DefaultClient, base+loc)
	confirm := bodyOf(t, resp)
	if got := resp.Header.Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("抽查页必须 no-store，得到 %q", got)
	}
	if strings.Contains(confirm, "复制") {
		t.Fatalf("助记词页不得提供复制按钮:\n%s", confirm)
	}
	for i, w := range strings.Fields(admin.challenge.Mnemonic) {
		if !strings.Contains(confirm, w) {
			t.Fatalf("抽查页应展示第 %d 个词 %q", i+1, w)
		}
	}
	for _, idx := range []string{"2", "5", "6"} {
		if !regexp.MustCompile(`第\s*` + idx + `\s*个`).MatchString(confirm) {
			t.Fatalf("抽查页应提示复述序号 %s:\n%s", idx, confirm)
		}
	}
	if !strings.Contains(confirm, `name="word1"`) {
		t.Fatalf("抽查表单应有 word1 输入:\n%s", confirm)
	}

	// 抽查通过 → 调用实现并回到管理页。
	resp = postForm(t, base, "/accounts/new/confirm", url.Values{
		"token": {"tk-123"}, "word1": {"bravo"}, "word2": {"echo"}, "word3": {"foxtrot"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("抽查通过应 303 回管理页，得到 %d", resp.StatusCode)
	}
	if _, c, _, _, _ := admin.counts(); c != 1 {
		t.Fatalf("抽查通过应调用一次确认，得到 %d 次", c)
	}
	admin.mu.Lock()
	got := admin.answers[0]
	admin.mu.Unlock()
	if len(got) != 3 || got[0] != "bravo" || got[1] != "echo" || got[2] != "foxtrot" {
		t.Fatalf("传给实现的复述答案不对: %v", got)
	}

	// 抽查失败 → 留在页面上并显示原因（不得跳转、不得再调用实现）。
	admin.mu.Lock()
	admin.confErr = errors.New("第 1 个词（序号 2）不正确，请核对后重试")
	admin.mu.Unlock()
	resp = postForm(t, base, "/accounts/new/confirm", url.Values{
		"token": {"tk-123"}, "word1": {"wrong"}, "word2": {"echo"}, "word3": {"foxtrot"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("抽查失败应重绘（200），得到 %d", resp.StatusCode)
	}
	if p := bodyOf(t, resp); !strings.Contains(p, "不正确") {
		t.Fatalf("抽查失败应显示原因:\n%s", p)
	}
}

// T9.5d（AC9.12）：取消创建 → 调用取消并回管理页。
func TestCreateCancelDiscardsPending(t *testing.T) {
	admin := &fakeAdmin{}
	base := newAdminServer(t, admin, true)
	resp := postForm(t, base, "/accounts/new/cancel", url.Values{"token": {"tk-9"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("取消应 303 回管理页，得到 %d", resp.StatusCode)
	}
	_, _, cancels, _, _ := admin.counts()
	if cancels != 1 {
		t.Fatalf("应调用一次取消，得到 %d", cancels)
	}
}

// T9.5e（AC9.6/AC9.7）：恢复两态 —— 助记词与恢复文件；口令不一致不触达实现。
func TestRecoverFlows(t *testing.T) {
	admin := &fakeAdmin{}
	base := newAdminServer(t, admin, true)

	page := bodyOf(t, mustGet(t, http.DefaultClient, base+"/accounts/recover"))
	if !strings.Contains(page, `value="mnemonic"`) || !strings.Contains(page, `value="recovery"`) {
		t.Fatalf("恢复页应有两种模式:\n%s", page)
	}

	// 助记词模式
	resp := postForm(t, base, "/accounts/recover", url.Values{
		"mode": {"mnemonic"}, "name": {""},
		"mnemonic": {"alpha bravo charlie"},
		"password": {"pw-one-two-three"}, "password2": {"pw-one-two-three"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("助记词恢复应 303，得到 %d", resp.StatusCode)
	}
	// 恢复文件模式：口令不一致 → 不触达实现
	resp = postForm(t, base, "/accounts/recover", url.Values{
		"mode": {"recovery"}, "name": {""}, "recovery_path": {"/tmp/r.json"},
		"recovery_password": {"rp"}, "password": {"pw-one-two-three"}, "password2": {"pw-x-y-z"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("口令不一致应重绘，得到 %d", resp.StatusCode)
	}
	resp = postForm(t, base, "/accounts/recover", url.Values{
		"mode": {"recovery"}, "name": {""}, "recovery_path": {"/tmp/r.json"},
		"recovery_password": {"rp"}, "password": {"pw-one-two-three"}, "password2": {"pw-one-two-three"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("恢复文件模式应 303，得到 %d", resp.StatusCode)
	}
	_, _, _, recovers, _ := admin.counts()
	if recovers != 2 {
		t.Fatalf("应调用两次恢复（助记词 + 恢复文件），得到 %d", recovers)
	}
	// 失败原因回显
	admin.mu.Lock()
	admin.recoverEr = errors.New("恢复口令错误或恢复文件无效")
	admin.mu.Unlock()
	resp = postForm(t, base, "/accounts/recover", url.Values{
		"mode": {"recovery"}, "name": {""}, "recovery_path": {"/tmp/r.json"},
		"recovery_password": {"rp"}, "password": {"pw-one-two-three"}, "password2": {"pw-one-two-three"},
	})
	if p := bodyOf(t, resp); !strings.Contains(p, "恢复口令错误") {
		t.Fatalf("恢复失败应显示原因:\n%s", p)
	}
}

// T9.5f（AC9.8/AC9.9）：删除需要口令授权，失败原因回显，成功后回管理页。
func TestDeleteFlowRequiresPassword(t *testing.T) {
	admin := &fakeAdmin{}
	base := newAdminServer(t, admin, true)

	confirm := bodyOf(t, mustGet(t, http.DefaultClient, base+"/accounts/delete?path="+url.QueryEscape(acctB)))
	if !strings.Contains(confirm, acctB) {
		t.Fatalf("删除确认页应显示要删除的账户路径:\n%s", confirm)
	}
	if !strings.Contains(confirm, "永久无法启封") {
		t.Fatalf("删除确认页必须说明后果（AC9.9）:\n%s", confirm)
	}

	admin.mu.Lock()
	admin.deleteErr = errors.New("口令错误，未删除任何文件")
	admin.mu.Unlock()
	resp := postForm(t, base, "/accounts/delete", url.Values{"path": {acctB}, "password": {"nope"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("删除失败应重绘，得到 %d", resp.StatusCode)
	}
	if p := bodyOf(t, resp); !strings.Contains(p, "口令错误") {
		t.Fatalf("删除失败应显示原因:\n%s", p)
	}

	admin.mu.Lock()
	admin.deleteErr = nil
	admin.mu.Unlock()
	resp = postForm(t, base, "/accounts/delete", url.Values{"path": {acctB}, "password": {"pw"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("删除成功应 303 回管理页，得到 %d", resp.StatusCode)
	}
	admin.mu.Lock()
	got := admin.deletes
	admin.mu.Unlock()
	if len(got) != 1 || got[0][0] != acctB || got[0][1] != "pw" {
		t.Fatalf("应把路径与口令透传给实现: %v", got)
	}
	// 删除页缺参数 → 400，且不调用实现。
	resp = postForm(t, base, "/accounts/delete", url.Values{"password": {"pw"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("缺少路径应 400，得到 %d", resp.StatusCode)
	}
}
