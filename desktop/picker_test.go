package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cc14514/go-safemedia/internal/desktopcfg"
)

// 外壳的「页内选择」接线（US11）：原生对话框 → 引擎的选择路由 → 与拖入同一条管线。

// fakePicker 是 play.FilePicker 的替身（只回路径，不碰窗口）。
type fakePicker struct {
	files  []string
	folder string
	err    error
}

func (p *fakePicker) PickFiles() ([]string, error) { return p.files, p.err }
func (p *fakePicker) PickFolder() (string, error)  { return p.folder, p.err }

// T11.11（AC11.5）：外壳真的把选择器接进了引擎 —— 解锁后封存面板上应该有这两个按钮。
// （注入点掉线是这类改动的典型退化：函数签名改了、调用方忘了传，面板悄悄少两个按钮。）
func TestDesktopSealPanelOffersPickerButtons(t *testing.T) {
	home := t.TempDir()
	const pw = "shell pick passphrase"
	keystore := filepath.Join(home, "keystore.json")
	writeTestKeystore(t, keystore, pw)
	libDir := filepath.Join(home, "library")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := desktopcfg.Save(home, desktopcfg.Config{Library: libDir, Account: keystore}); err != nil {
		t.Fatal(err)
	}

	appArgsOverride = nil
	app := &App{}
	base, _, err := prepare(app, home)
	if err != nil {
		t.Fatalf("prepare 失败：%v", err)
	}
	defer app.shutdown(t.Context())

	// 锁定态：封存面板不可见（回登录页），但登录页本身可用。
	client := loginClient(t, base, keystore, pw)

	page := getBody(t, client, base+"seal")
	for _, want := range []string{`action="/seal/pick/files"`, `action="/seal/pick/folder"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("封存面板应含选择入口 %q（外壳没接上选择器？）：\n%s", want, truncate(page, 600))
		}
	}
	if strings.Contains(page, `<a href="/seal/pick`) {
		t.Fatalf("选择入口必须是按钮而不是链接：\n%s", truncate(page, 600))
	}
}

// T11.12（AC11.8）：外壳的选择器在「窗口未就绪」时必须给出可读原因，
// 而不是 panic/静默 —— 面板上要能看到中文提示，且不产生任何文件。
//
// 这条走的是真实接线（prepare 注入的真 sealPicker），因此也钉住「对话框失败会被
// 引擎写进面板」这条链路。
func TestDesktopPickWithoutWindowShowsReadableError(t *testing.T) {
	home := t.TempDir()
	const pw = "shell pick nowindow passphrase"
	keystore := filepath.Join(home, "keystore.json")
	writeTestKeystore(t, keystore, pw)
	libDir := filepath.Join(home, "library")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := desktopcfg.Save(home, desktopcfg.Config{Library: libDir, Account: keystore}); err != nil {
		t.Fatal(err)
	}

	appArgsOverride = nil
	app := &App{}
	base, _, err := prepare(app, home)
	if err != nil {
		t.Fatalf("prepare 失败：%v", err)
	}
	defer app.shutdown(t.Context())

	client := loginClient(t, base, keystore, pw)

	// 进程内没有窗口上下文（Startup 未被 Wails 调用）→ 对话框必然失败。
	resp, err := client.PostForm(base+"seal/pick/files", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("选择入口应 303 回面板（错误显示在面板里），得到 %d", resp.StatusCode)
	}

	raw := getBody(t, client, base+"seal/status")
	var st map[string]any
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatalf("状态不是 JSON：%v（%s）", err, truncate(raw, 200))
	}
	msg, _ := st["error"].(string)
	if !strings.Contains(msg, "打开文件选择框失败") || !strings.Contains(msg, "窗口尚未就绪") {
		t.Fatalf("面板应显示可读的失败原因，得到 %q", msg)
	}
	// 不产生任何文件
	des, err := os.ReadDir(libDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(des) != 0 {
		t.Fatalf("对话框失败不得产生文件，库根出现 %d 项", len(des))
	}
}

// T11.13（AC11.5）：buildEngine 必须把传入的选择器真的交给引擎
// （否则外壳会「有按钮但点了没反应」这种更难查的形态）。
func TestBuildEngineWiresPicker(t *testing.T) {
	home := t.TempDir()
	libDir := filepath.Join(home, "library")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 账户放在默认发现目录里，才能走正规的登录路径解锁（AC9.1）。
	const pw = "build engine pick passphrase"
	acctDir := filepath.Join(home, ".safemedia")
	if err := os.MkdirAll(acctDir, 0o700); err != nil {
		t.Fatal(err)
	}
	keystore := filepath.Join(acctDir, "keystore.json")
	writeTestKeystore(t, keystore, pw)

	eng, _, err := buildEngine(desktopcfg.Target{Kind: desktopcfg.KindLibrary, Path: libDir}, home, desktopcfg.Config{}, &fakePicker{folder: libDir})
	if err != nil {
		t.Fatalf("构造引擎失败：%v", err)
	}
	defer func() { _ = eng.Close() }()
	base, err := eng.Start()
	if err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	page := getBody(t, loginClient(t, base, keystore, pw), base+"seal")
	for _, want := range []string{`action="/seal/pick/files"`, `action="/seal/pick/folder"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("注入选择器后引擎封存面板应含 %q：\n%s", want, truncate(page, 600))
		}
	}
	// 不注入（CLI 之外的空壳形态）→ 不渲染按钮
	eng2, _, err := buildEngine(desktopcfg.Target{Kind: desktopcfg.KindLibrary, Path: libDir}, home, desktopcfg.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = eng2.Close() }()
	base2, err := eng2.Start()
	if err != nil {
		t.Fatal(err)
	}
	page2 := getBody(t, loginClient(t, base2, keystore, pw), base2+"seal")
	if strings.Contains(page2, `action="/seal/pick/files"`) {
		t.Fatalf("未注入选择器时不应渲染按钮：\n%s", truncate(page2, 600))
	}
}

// T11.15（AC11.1）：过滤器模式只能由 `*.ext` 这类**具体扩展名**组成。
// Wails 会把模式里的 "*." 剥掉，再把剩下的字符串当扩展名交给系统面板
// （macOS 走 `UTType typeWithFilenameExtension:`）。实测：通配 "*" 得到的是
// **无意义的 dynamic UTType**（`dyn.ah62d4rv4ge8wy`，匹配不到任何真实文件），
// 空扩展名才返回 nil。写了通配用户就会在面板里看到一个选不出东西的过滤器，
// 所以这里钉死「只给具体扩展名」。
func TestPickerFiltersUseConcreteExtensions(t *testing.T) {
	for _, f := range videoFilters() {
		for _, pat := range strings.Split(f.Pattern, ";") {
			pat = strings.TrimSpace(pat)
			if !strings.HasPrefix(pat, "*.") {
				t.Fatalf("过滤器模式 %q 必须是 *.ext 形式（会被当作扩展名交给系统面板）", pat)
			}
			if ext := strings.TrimPrefix(pat, "*."); ext == "" || strings.ContainsAny(ext, "*?") {
				t.Fatalf("过滤器模式 %q 的扩展名无效：%q（通配/空扩展名在 macOS 上会变成无意义过滤器或 nil）", pat, ext)
			}
		}
	}
}

// T11.14（AC11.5）：窗口未就绪时 sealPicker 返回可辨识的错误（不是空值 —— 空值会被
// 引擎当成「用户取消」，让失败看起来像没发生）。
func TestSealPickerWithoutWindowFails(t *testing.T) {
	p := &sealPicker{app: &App{}} // 未 Startup → 无窗口上下文
	if _, err := p.PickFiles(); !errors.Is(err, errNoWindow) {
		t.Fatalf("无窗口时应返回 errNoWindow，得到 %v", err)
	}
	if _, err := p.PickFolder(); !errors.Is(err, errNoWindow) {
		t.Fatalf("无窗口时应返回 errNoWindow，得到 %v", err)
	}
	if msg := errNoWindow.Error(); !strings.Contains(msg, "窗口") {
		t.Fatalf("错误文本应说明窗口未就绪：%q", msg)
	}
	// 白名单过滤器必须覆盖三种容器（否则系统面板里选不到合法文件）。
	var s string
	for _, f := range videoFilters() {
		s += f.Pattern
	}
	for _, ext := range []string{"*.mp4", "*.mov", "*.webm"} {
		if !strings.Contains(s, ext) {
			t.Fatalf("文件选择过滤器应含 %s：%q", ext, s)
		}
	}
}

// ---------- 小工具 ----------

// loginClient 登录后返回带会话 cookie 的客户端。
func loginClient(t *testing.T, base, keystore, pw string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.PostForm(base+"login", url.Values{"account": {keystore}, "password": {pw}})
	if err != nil {
		t.Fatalf("登录失败：%v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("登录应 303，得到 %d", resp.StatusCode)
	}
	return client
}

func getBody(t *testing.T, client *http.Client, u string) string {
	t.Helper()
	resp, err := client.Get(u)
	if err != nil {
		t.Fatalf("GET %s 失败：%v", u, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
