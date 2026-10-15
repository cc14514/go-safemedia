package play

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// challengesTTL 是待确认创建在引擎侧的展示副本存活时长（与实现侧的 pending 同量级）。
const challengesTTL = 5 * time.Minute

// challengeEntry 是待确认创建的**展示副本**（助记词与抽查序号）。
//
// 权威状态在调用方注入的实现里（它负责校验与落盘）；引擎只留一份用于渲染，
// 因此这份副本被篡改也不影响校验，只影响「页面上显示什么」。
type challengeEntry struct {
	ch      CreateChallenge
	expires time.Time
}

// handleAccounts 分发账户管理路由（US9）。
//
// 前提（由 gate 保证）：仅在锁定态可达；解锁后调用方会 303 回索引页，避免
// 「已解锁的播放页面里再改账户」（AC9.10）。
func (c *core) handleAccounts(w http.ResponseWriter, r *http.Request) {
	if c.admin == nil {
		http.NotFound(w, r)
		return
	}
	if c.unlocked.Load() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	c.touch()
	switch r.URL.Path {
	case "/accounts":
		c.renderAccountsList(w, r.URL.Query().Get("done"))
	case "/accounts/new":
		c.handleAccountsCreate(w, r)
	case "/accounts/new/confirm":
		c.handleAccountsConfirm(w, r)
	case "/accounts/new/cancel":
		c.handleAccountsCancel(w, r)
	case "/accounts/recover":
		c.handleAccountsRecover(w, r)
	case "/accounts/delete":
		c.handleAccountsDelete(w, r)
	default:
		http.NotFound(w, r)
	}
}

// renderAccountsList 渲染账户列表页（动作后回到这里，AC9.1/AC9.2）。
func (c *core) renderAccountsList(w http.ResponseWriter, done string) {
	view := accountsView{Accounts: c.accountList()}
	switch done {
	case "created":
		view.Notice = "账户已创建。"
	case "recovered":
		view.Notice = "账户已恢复。"
	case "deleted":
		view.Notice = "账户已删除，文件已移入回收目录（密档未受影响）。"
	}
	c.renderHTML(w, accountsPage(view))
}

// handleAccountsCreate 处理创建页：GET 渲染表单，POST 生成账户并转到复述抽查。
func (c *core) handleAccountsCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		c.renderHTML(w, createPage(createView{Name: c.admin.DefaultTargetName(), Words: 12}))
		return
	}
	form, err := readForm(r)
	if err != nil {
		c.renderHTML(w, createPage(createView{Name: c.admin.DefaultTargetName(), Words: 12, Err: "请求解析失败，请重试"}))
		return
	}
	name := strings.TrimSpace(form.Get("name"))
	words := 12
	if raw := strings.TrimSpace(form.Get("words")); raw != "" {
		n, convErr := strconv.Atoi(raw)
		if convErr != nil || (n != 12 && n != 24) {
			c.renderHTML(w, createPage(createView{Name: name, Words: 12, Err: "助记词词数只能是 12 或 24"}))
			return
		}
		words = n
	}
	// 目标名缺省时用实现给出的默认名（首个账户固定写入默认账户路径，AC9.4）。
	if name == "" {
		name = c.admin.DefaultTargetName()
	}
	if form.Get("password") != form.Get("password2") {
		c.renderHTML(w, createPage(createView{Name: name, Words: words, Err: "两次输入的口令不一致"}))
		return
	}
	ch, err := c.admin.Create(name, form.Get("password"), words)
	if err != nil {
		c.renderHTML(w, createPage(createView{Name: name, Words: words, Err: err.Error()}))
		return
	}
	c.putChallenge(ch)
	http.Redirect(w, r, "/accounts/new/confirm?token="+ch.Token, http.StatusSeeOther)
}

// handleAccountsConfirm 处理复述抽查：GET 展示助记词，POST 提交复述。
func (c *core) handleAccountsConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		c.renderConfirm(w, r.URL.Query().Get("token"), "")
		return
	}
	form, err := readForm(r)
	if err != nil {
		c.renderConfirm(w, r.URL.Query().Get("token"), "请求解析失败，请重试")
		return
	}
	token := form.Get("token")
	answers := []string{
		strings.TrimSpace(form.Get("word1")),
		strings.TrimSpace(form.Get("word2")),
		strings.TrimSpace(form.Get("word3")),
	}
	got, err := c.admin.ConfirmCreate(token, answers)
	if err != nil {
		// 抽查失败/会话失效：留在本页显示原因（口令与助记词不回显到页面）。
		c.renderConfirm(w, token, err.Error())
		return
	}
	c.takeChallenge(token)
	_ = got
	http.Redirect(w, r, "/accounts?done=created", http.StatusSeeOther)
}

// handleAccountsCancel 丢弃待确认创建（AC9.12）。
func (c *core) handleAccountsCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "请用 POST 取消创建", http.StatusMethodNotAllowed)
		return
	}
	form, _ := readForm(r)
	token := form.Get("token")
	c.takeChallenge(token)
	c.admin.CancelCreate(token)
	http.Redirect(w, r, "/accounts", http.StatusSeeOther)
}

// handleAccountsRecover 处理恢复页（助记词 / 恢复文件两种模式，AC9.6/AC9.7）。
func (c *core) handleAccountsRecover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		c.renderHTML(w, recoverPage(recoverView{Mode: "mnemonic"}))
		return
	}
	form, err := readForm(r)
	if err != nil {
		c.renderHTML(w, recoverPage(recoverView{Mode: "mnemonic", Err: "请求解析失败，请重试"}))
		return
	}
	mode := strings.TrimSpace(form.Get("mode"))
	view := recoverView{
		Mode:         mode,
		Name:         strings.TrimSpace(form.Get("name")),
		RecoveryPath: strings.TrimSpace(form.Get("recovery_path")),
	}
	if mode != "recovery" {
		view.Mode = "mnemonic"
	}
	if form.Get("password") != form.Get("password2") {
		view.Err = "两次输入的口令不一致"
		c.renderHTML(w, recoverPage(view))
		return
	}
	var rErr error
	if view.Mode == "recovery" {
		_, rErr = c.admin.RecoverFromRecoveryFile(view.Name, view.RecoveryPath, form.Get("recovery_password"), form.Get("password"))
	} else {
		_, rErr = c.admin.RecoverFromMnemonic(view.Name, form.Get("mnemonic"), form.Get("password"))
	}
	if rErr != nil {
		view.Err = rErr.Error()
		c.renderHTML(w, recoverPage(view))
		return
	}
	http.Redirect(w, r, "/accounts?done=recovered", http.StatusSeeOther)
}

// handleAccountsDelete 处理删除确认页：GET 展示后果与口令框，POST 执行（AC9.8/AC9.9）。
func (c *core) handleAccountsDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		c.renderDelete(w, r.URL.Query().Get("path"), "")
		return
	}
	form, err := readForm(r)
	if err != nil {
		http.Error(w, "请求解析失败", http.StatusBadRequest)
		return
	}
	path := strings.TrimSpace(form.Get("path"))
	if path == "" {
		http.Error(w, "缺少账户路径", http.StatusBadRequest)
		return
	}
	if _, err := c.admin.Delete(path, form.Get("password")); err != nil {
		c.renderDelete(w, path, err.Error())
		return
	}
	http.Redirect(w, r, "/accounts?done=deleted", http.StatusSeeOther)
}

// renderDelete 渲染删除确认页；path 必须来自账户列表（AC9.9：不接受任意路径）。
func (c *core) renderDelete(w http.ResponseWriter, path, errMsg string) {
	if path == "" {
		c.renderHTML(w, deletePage(deleteView{Err: "缺少账户路径：请从账户管理页选择要删除的账户"}))
		return
	}
	if _, ok := findAccount(c.accountList(), path); !ok {
		c.renderHTML(w, deletePage(deleteView{Err: "该账户不在账户管理页的列表中，未做任何改动"}))
		return
	}
	c.renderHTML(w, deletePage(deleteView{Path: path, Err: errMsg}))
}

// renderConfirm 渲染复述抽查页（challenge 缺失或过期 → 提示重新开始）。
func (c *core) renderConfirm(w http.ResponseWriter, token, errMsg string) {
	ch, ok := c.challenge(token)
	if !ok {
		c.renderHTML(w, createPage(createView{
			Name: c.admin.DefaultTargetName(),
			Err:  firstNonEmpty(errMsg, "创建会话已失效，请重新开始"),
		}))
		return
	}
	c.renderHTML(w, mnemonicPage(mnemonicView{
		Token:   ch.Token,
		Words:   numberedWords(ch.Mnemonic),
		Prompts: prompts(ch.Indexes),
		Err:     errMsg,
		Path:    ch.Path,
	}))
}

// putChallenge / challenge / takeChallenge 维护引擎侧的展示副本。
func (c *core) putChallenge(ch CreateChallenge) {
	c.accMu.Lock()
	defer c.accMu.Unlock()
	if c.challenges == nil {
		c.challenges = map[string]challengeEntry{}
	}
	c.challenges[ch.Token] = challengeEntry{ch: ch, expires: c.now().Add(challengesTTL)}
}

func (c *core) challenge(token string) (CreateChallenge, bool) {
	c.accMu.Lock()
	defer c.accMu.Unlock()
	e, ok := c.challenges[token]
	if !ok || c.now().After(e.expires) {
		delete(c.challenges, token)
		return CreateChallenge{}, false
	}
	return e.ch, true
}

func (c *core) takeChallenge(token string) {
	c.accMu.Lock()
	defer c.accMu.Unlock()
	delete(c.challenges, token)
}

// renderHTML 输出一个不缓存的 HTML 页面；渲染失败必须可见（AGENTS.md 易错点 5）。
func (c *core) renderHTML(w http.ResponseWriter, body string) {
	writeNoCacheHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, body)
}

// readForm 解析表单（ParseForm 自带请求体上限，避免超大 POST 打满内存）。
func readForm(r *http.Request) (url.Values, error) {
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	return r.PostForm, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// numberedWord 是助记词网格中的一项。
type numberedWord struct {
	Index int
	Word  string
}

// numberedWords 把助记词切成带编号的项（页面上按编号网格展示，FR-1.1）。
func numberedWords(mnemonic string) []numberedWord {
	fields := strings.Fields(mnemonic)
	out := make([]numberedWord, len(fields))
	for i, w := range fields {
		out[i] = numberedWord{Index: i + 1, Word: w}
	}
	return out
}

// prompts 把抽查序号渲染成「第 N 个词」的提示。
func prompts(indexes []int) []promptItem {
	out := make([]promptItem, len(indexes))
	for i, idx := range indexes {
		out[i] = promptItem{Seq: i + 1, Index: idx, Field: fmt.Sprintf("word%d", i+1)}
	}
	return out
}

// promptItem 是一次复述抽查（页面上一个输入框）。
type promptItem struct {
	Seq   int
	Index int
	Field string
}
