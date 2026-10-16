package integration

import (
	"encoding/json"
	"errors"
	"net/http"
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

// staticPicker 是外壳原生选择对话框的替身（US11 的 L2 链路用）。
//
// 只回路径；用户取消 → 空值 + nil；对话框失败 → error。
type staticPicker struct {
	mu     sync.Mutex
	files  []string
	folder string
	err    error
	calls  int
}

func (p *staticPicker) PickFiles() ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.files, p.err
}

func (p *staticPicker) PickFolder() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.folder, p.err
}

func (p *staticPicker) set(files []string, folder string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.files, p.folder, p.err = files, folder, err
}

func (p *staticPicker) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// noRedirectClient 是不跟随重定向的客户端（断言 303 本身）。
func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// sealStatusOf 取一次 /seal/status（避免与已有测试的辅助函数重名）。
func sealStatusOf(t *testing.T, base string) map[string]any {
	t.Helper()
	raw := get(t, http.DefaultClient, base+"seal/status")
	var st map[string]any
	if err := json.Unmarshal([]byte(raw.body), &st); err != nil {
		t.Fatalf("状态不是 JSON: %v（%s）", err, raw.body)
	}
	return st
}

// T11.9（AC11.1–AC11.3、AC11.6、AC10.10）：页内选择 → 真封存 → 密档能启封 → 索引页可见。
//
// 这是「点按钮选择」的进程内等价链路：外壳的原生对话框只负责把路径交给引擎，
// 其余（展开、入队、进度、不覆盖、重扫）全在引擎里 —— 与拖入共用一条管线。
func TestM11PickSealsAndShowsProgress(t *testing.T) {
	home := t.TempDir()
	const pw = "pick passphrase"
	keystore := newAccountForTest(t, home, pw)

	libDir := filepath.Join(home, "library")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 一个待选的文件夹：两个视频（其中一个在子目录）+ 一个非白名单文件
	pickDir := filepath.Join(home, "待封存")
	if err := os.MkdirAll(filepath.Join(pickDir, "子目录"), 0o755); err != nil {
		t.Fatal(err)
	}
	plainA := fakeMP4(2 << 20)
	if err := os.WriteFile(filepath.Join(pickDir, "a.mp4"), plainA, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pickDir, "子目录", "b.mp4"), fakeMP4(1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pickDir, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	acct, err := account.LoadKeystoreWithPassphrase(keystore, []byte(pw))
	if err != nil {
		t.Fatalf("加载账户失败: %v", err)
	}
	picker := &staticPicker{folder: pickDir}
	lib, err := play.NewLibrary(play.LibraryOptions{
		Target:      libDir,
		Root:        libDir,
		Priv:        acct.PrivateKey(),
		IdleTimeout: play.DefaultIdleTimeout,
		Sealer:      newRealSealer(),
		Picker:      picker,
	})
	if err != nil {
		t.Fatalf("构造库失败: %v", err)
	}
	base, err := lib.Start()
	if err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	defer func() { _ = lib.Close() }()

	// 面板上有选择入口（按钮，不是链接）
	panel := get(t, http.DefaultClient, base+"seal")
	for _, want := range []string{`action="/seal/pick/files"`, `action="/seal/pick/folder"`} {
		if !strings.Contains(panel.body, want) {
			t.Fatalf("选择器注入后封存面板应含 %q：\n%s", want, panel.body)
		}
	}
	if strings.Contains(panel.body, `<a href="/seal/pick`) {
		t.Fatalf("选择入口必须是按钮：\n%s", panel.body)
	}

	// 「选择文件夹…」：等价于把文件夹拖进来 —— 递归展开、保留子目录结构
	resp := postForm(t, noRedirectClient(), base+"seal/pick/folder", url.Values{})
	if resp.code != http.StatusSeeOther {
		t.Fatalf("选择文件夹后应 303 回面板，得到 %d", resp.code)
	}
	if loc := resp.header.Get("Location"); loc != "/seal" {
		t.Fatalf("应回到 /seal，得到 %q", loc)
	}
	if picker.callCount() != 1 {
		t.Fatalf("应调用一次选择器，得到 %d", picker.callCount())
	}

	// 进度：真队列，轮询到结束
	deadline := time.Now().Add(60 * time.Second)
	var st map[string]any
	for time.Now().Before(deadline) {
		st = sealStatusOf(t, base)
		if st["active"] == false {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st["active"] != false {
		t.Fatal("封存队列未在 60s 内结束")
	}
	items, _ := st["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("状态应含 2 个条目（递归展开）：%+v", st)
	}
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m["state"] != "完成" {
			t.Fatalf("文件应封存完成：%+v", m)
		}
		if m["percent"] != float64(100) {
			t.Fatalf("进度应到 100%%：%+v", m)
		}
	}
	rejects, _ := st["rejects"].([]any)
	if len(rejects) != 1 {
		t.Fatalf("非白名单文件应逐条给出原因：%+v", st)
	}

	// 落盘结构与内容
	top := filepath.Join(libDir, "a.mp4.svid")
	sub := filepath.Join(libDir, "子目录", "b.mp4.svid")
	if !fileExists(top) || !fileExists(sub) {
		t.Fatalf("库根下应生成两份密档（保留子目录结构）：%s / %s", top, sub)
	}
	r, err := vault.Open(top, acct.PrivateKey())
	if err != nil {
		t.Fatalf("Open 密档失败: %v", err)
	}
	defer r.Close()
	got := make([]byte, r.Size())
	if _, err := r.ReadAt(got, 0); err != nil {
		t.Fatalf("ReadAt 失败: %v", err)
	}
	if string(got) != string(plainA) {
		t.Fatal("启封内容与原文不一致")
	}
	// 明文本身不得落在库根（红线）
	if fileExists(filepath.Join(libDir, "a.mp4")) {
		t.Fatal("库根里出现了明文视频")
	}

	// 封存结束后重扫：新密档出现在索引页（AC10.10）
	idx := get(t, http.DefaultClient, base+"/")
	if !strings.Contains(idx.body, "a.mp4.svid") {
		t.Fatalf("索引页应能看到新密档：\n%s", idx.body)
	}

	// 再选**文件**（另一条入口）也必须走同一条管线
	picker.set([]string{filepath.Join(home, "待封存", "a.mp4")}, "", nil)
	if resp := postForm(t, noRedirectClient(), base+"seal/pick/files", url.Values{}); resp.code != http.StatusSeeOther {
		t.Fatalf("选择文件后应 303，得到 %d", resp.code)
	}
	if picker.callCount() != 2 {
		t.Fatalf("应调用两次选择器，得到 %d", picker.callCount())
	}
	// 同名密档已存在 → 不覆盖（AC10.5），条目为「跳过」而不是重新写入
	st = map[string]any{}
	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st = sealStatusOf(t, base)
		if st["active"] == false {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	items, _ = st["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("第二次选择应只有 1 个条目：%+v", st)
	}
	m, _ := items[0].(map[string]any)
	if m["state"] != "跳过" {
		t.Fatalf("已存在的密档不得被覆盖，条目应为「跳过」：%+v", m)
	}
	// 原有密档没被改写（能正常启封）
	if r2, err := vault.Open(top, acct.PrivateKey()); err != nil {
		t.Fatalf("已有密档被破坏了: %v", err)
	} else {
		_ = r2.Close()
	}
}

// T11.10（AC11.4/AC11.8）：用户取消（对话框返回空）→ 状态不变、不产生任何文件；
// 对话框报错 → 面板显示中文原因，同样不产生文件。
func TestM11PickCancelAndErrorKeepState(t *testing.T) {
	home := t.TempDir()
	const pw = "pick cancel passphrase"
	keystore := newAccountForTest(t, home, pw)
	libDir := filepath.Join(home, "library")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	acct, err := account.LoadKeystoreWithPassphrase(keystore, []byte(pw))
	if err != nil {
		t.Fatal(err)
	}
	// 非白名单文件 → 只产生一条拒绝记录（用来观察「取消不得清空状态」）
	notVideo := filepath.Join(home, "notes.txt")
	if err := os.WriteFile(notVideo, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	picker := &staticPicker{files: []string{notVideo}}
	lib, err := play.NewLibrary(play.LibraryOptions{
		Target: libDir, Root: libDir, Priv: acct.PrivateKey(),
		IdleTimeout: play.DefaultIdleTimeout, Sealer: newRealSealer(), Picker: picker,
	})
	if err != nil {
		t.Fatal(err)
	}
	base, err := lib.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lib.Close() }()

	// 一次有效选择（全是非白名单 → 不建队列，但记录拒绝原因）
	if resp := postForm(t, noRedirectClient(), base+"seal/pick/files", url.Values{}); resp.code != http.StatusSeeOther {
		t.Fatalf("应 303，得到 %d", resp.code)
	}
	st := sealStatusOf(t, base)
	beforeRejects, _ := st["rejects"].([]any)
	if len(beforeRejects) != 1 {
		t.Fatalf("前置条件：应有 1 条拒绝记录：%+v", st)
	}

	// 取消（空）→ 状态不变
	picker.set(nil, "", nil)
	if resp := postForm(t, noRedirectClient(), base+"seal/pick/files", url.Values{}); resp.code != http.StatusSeeOther {
		t.Fatalf("取消也应 303，得到 %d", resp.code)
	}
	if resp := postForm(t, noRedirectClient(), base+"seal/pick/folder", url.Values{}); resp.code != http.StatusSeeOther {
		t.Fatalf("取消文件夹也应 303，得到 %d", resp.code)
	}
	st = sealStatusOf(t, base)
	afterRejects, _ := st["rejects"].([]any)
	if len(afterRejects) != len(beforeRejects) {
		t.Fatalf("取消不得清空拒绝记录：before=%d after=%d", len(beforeRejects), len(afterRejects))
	}
	if st["error"] != "" {
		t.Fatalf("取消不是错误：%+v", st["error"])
	}

	// 对话框报错 → 面板显示中文原因
	picker.set(nil, "", errors.New("选择面板被系统拒绝"))
	if resp := postForm(t, noRedirectClient(), base+"seal/pick/files", url.Values{}); resp.code != http.StatusSeeOther {
		t.Fatalf("报错也应 303，得到 %d", resp.code)
	}
	st = sealStatusOf(t, base)
	msg, _ := st["error"].(string)
	if !strings.Contains(msg, "打开文件选择框失败") || !strings.Contains(msg, "选择面板被系统拒绝") {
		t.Fatalf("状态应带可读错误：%+v", st)
	}

	// 无论取消还是报错，库根里都不得出现任何文件
	des, err := os.ReadDir(libDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(des) != 0 {
		t.Fatalf("取消/报错都不得产生文件，库根出现 %d 项", len(des))
	}
}
