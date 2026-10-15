package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cc14514/go-safemedia/internal/account"
	"github.com/cc14514/go-safemedia/internal/desktopcfg"
)

// T7.7：外壳始终以「库模式」构造引擎——单密档目标取其所在目录为库根（AC7.7），
// 这样页面上的目录选择器与目录树在任何启动方式下都可用；引擎仍以锁定态起步（AC1.1）。
func TestBuildEngineUsesLibraryModeForFileTarget(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(clip, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	eng, label, err := buildEngine(desktopcfg.Target{
		Kind:    desktopcfg.KindFile,
		Path:    clip,
		Account: filepath.Join(home, "keystore.json"),
	}, home, desktopcfg.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	if label != filepath.Base(dir) {
		t.Fatalf("窗口标签 = %q，期望 %q", label, filepath.Base(dir))
	}
	rooted, ok := eng.(interface{ Root() string })
	if !ok {
		t.Fatalf("引擎未以库模式构造：%T", eng)
	}
	if rooted.Root() != dir {
		t.Fatalf("库根 = %q，期望单密档所在目录 %q", rooted.Root(), dir)
	}
	if !eng.Locked() {
		t.Fatal("引擎应以锁定态起步（先出登录页）")
	}
}

// T7.7：页面里换目录后，选择要写回 desktop.json 并保留同文件的 account（AC7.4）。
func TestPersistLibraryWritesDesktopJSONKeepingAccount(t *testing.T) {
	home := t.TempDir()
	cfg := desktopcfg.Config{Library: "/old/library", Account: "/tmp/keep-account.json"}
	if err := desktopcfg.Save(home, cfg); err != nil {
		t.Fatal(err)
	}

	if err := persistLibrary(home, cfg)("/new/library"); err != nil {
		t.Fatalf("persistLibrary 失败: %v", err)
	}
	got, err := desktopcfg.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if got.Library != "/new/library" {
		t.Fatalf("desktop.json 的 library = %q，期望 /new/library", got.Library)
	}
	if got.Account != "/tmp/keep-account.json" {
		t.Fatalf("写入库根时改动了 account：%q", got.Account)
	}
}

// T3.1：引导页必须拿到运行时注入的地址（否则窗口停在空白引导页）。
func TestRenderBootstrapInjectsURL(t *testing.T) {
	out, err := renderBootstrap(bootInfo{URL: "http://127.0.0.1:12345/", Title: "go-safemedia", Version: "go-safemedia v1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, bootstrapPlaceholder) {
		t.Fatalf("占位符未被替换:\n%s", s)
	}
	if !strings.Contains(s, `"url":"http://127.0.0.1:12345/"`) {
		t.Fatalf("注入的 url 缺失:\n%s", s)
	}
}

// T3.1：错误事件必须能把原因带到页面上，且不能因错误文本而破坏 <script>。
func TestRenderBootstrapEscapesScriptBreakingText(t *testing.T) {
	out, err := renderBootstrap(bootInfo{Error: `账户损坏</script><script>alert(1)</script>`})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// json.Marshal 会把 < > & 转义成 \u003c 等，绝不出现原始闭合标签
	if strings.Contains(s, "</script><script>alert") {
		t.Fatalf("错误文本可逃逸出脚本上下文:\n%s", s)
	}
	if !strings.Contains(s, `\u003c/script\u003e`) {
		t.Fatalf("错误文本未被 JSON 转义:\n%s", s)
	}
}

// T3.1：注入内容必须是合法 JSON（否则前端拿不到 url/error）。
func TestBootstrapPayloadIsValidJSON(t *testing.T) {
	out, err := renderBootstrap(bootInfo{URL: "http://127.0.0.1:1/", Error: "x", Version: "v"})
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(out), "var BOOT = ")
	if start < 0 {
		t.Fatal("未找到 BOOT 赋值")
	}
	rest := string(out)[start+len("var BOOT = "):]
	end := strings.Index(rest, ";\n")
	var got bootInfo
	if err := json.Unmarshal([]byte(rest[:end]), &got); err != nil {
		t.Fatalf("注入的不是合法 JSON: %v\n%s", err, rest[:end])
	}
	if got.URL != "http://127.0.0.1:1/" || got.Error != "x" {
		t.Fatalf("JSON 往返不一致: %+v", got)
	}
}

// T3.1：中间件只接管入口路径，其余请求放行给内嵌资源服务。
func TestBootstrapMiddlewareScopesToEntryPaths(t *testing.T) {
	mw := bootstrapMiddleware(bootInfo{URL: "http://127.0.0.1:9/"})
	sawNext := 0
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawNext++
		w.WriteHeader(http.StatusTeapot)
	}))

	for _, p := range []string{"/", "/index.html"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s 状态码 = %d", p, rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
			t.Fatalf("%s 缺少 no-store: %q", p, cc)
		}
		if !strings.Contains(rec.Body.String(), "127.0.0.1:9") {
			t.Fatalf("%s 未得到引导页", p)
		}
	}
	if sawNext != 0 {
		t.Fatalf("入口路径不应下探到内嵌资源服务，实际下探 %d 次", sawNext)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/wails/ipc.js", nil))
	if rec.Code != http.StatusTeapot || sawNext != 1 {
		t.Fatalf("非入口路径应放行：code=%d sawNext=%d", rec.Code, sawNext)
	}
}

// T3.1：启动参数解析需忽略框架/系统注入的 - 开头参数。
func TestAppArgsFiltersFlags(t *testing.T) {
	old := appArgsOverride
	defer func() { appArgsOverride = old }()

	appArgsOverride = []string{"-psn_0_12345", "/tmp/lib", "--flag", "~/clips"}
	got := appArgs()
	want := []string{"/tmp/lib", "~/clips"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

// 默认库为空目录时也必须能起服务（首启没有密档是合法状态）。
func TestEmptyLibraryDirIsAccepted(t *testing.T) {
	// 由 internal/desktopcfg 的 L1 覆盖路径决策；这里只确认引导页在「无 URL」时给出明确提示。
	out, err := renderBootstrap(bootInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "缺少播放服务地址") {
		t.Fatal("无 url/error 时引导页未给出明确提示")
	}
}

// T8.5/T8.7（AC8.2–AC8.5、AC8.7）：外壳侧的账户接线 ——
// 发现本机账户 → 作为选项注入登录页 → 引擎只认列表内的标识 → 口令错误给出中文原因。
func TestDesktopAccountWiring(t *testing.T) {
	home := t.TempDir()
	dataDir := desktopcfg.DefaultDir(home)
	if err := os.MkdirAll(filepath.Join(dataDir, "accounts"), 0o700); err != nil {
		t.Fatal(err)
	}
	const pwMain, pwWork = "desktop main passphrase", "desktop work passphrase"
	mainKs := filepath.Join(dataDir, "keystore.json")
	workKs := filepath.Join(dataDir, "accounts", "work.json")
	writeTestKeystore(t, mainKs, pwMain)
	writeTestKeystore(t, workKs, pwWork)
	// 列表之外的账户（未被发现）：即便口令正确也不得通过登录页解锁。
	outside := filepath.Join(t.TempDir(), "outside.json")
	writeTestKeystore(t, outside, "outside passphrase")

	libDir := filepath.Join(t.TempDir(), "lib")
	if err := os.MkdirAll(libDir, 0o700); err != nil {
		t.Fatal(err)
	}
	eng, _, err := buildEngine(desktopcfg.Target{
		Kind: desktopcfg.KindLibrary, Path: libDir, Account: mainKs,
	}, home, desktopcfg.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	base, err := eng.Start()
	if err != nil {
		t.Fatal(err)
	}

	// ① 登录页把两个账户都列出来（名称），并只认列表内的标识
	page := desktopGet(t, base)
	for _, want := range []string{
		`<select id="account" name="account">`,
		`value="` + mainKs + `"`, "keystore.json",
		`value="` + workKs + `"`, "work.json",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("登录页缺少 %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, filepath.Base(outside)) {
		t.Fatalf("列表外的账户出现在登录页:\n%s", page)
	}

	// ② 列表外的账户：引擎在触达解锁回调之前就拒绝（AC8.7 第一道防线）。
	body, code := desktopPost(t, base+"login", url.Values{
		"account": {outside}, "password": {"outside passphrase"},
	})
	if code == http.StatusSeeOther {
		t.Fatalf("列表外账户不该解锁（code=%d）", code)
	}
	if !strings.Contains(body, "请选择一个可用账户") {
		t.Fatalf("列表外账户未给出拒绝提示（也不该是「找不到账户文件」）:\n%s", body)
	}

	// ③ 列表内但口令错：给出外壳的中文原因，且不下发会话（AC8.3）
	body, code = desktopPost(t, base+"login", url.Values{
		"account": {mainKs}, "password": {"wrong passphrase"},
	})
	if code == http.StatusSeeOther {
		t.Fatalf("错口令不该解锁（code=%d）", code)
	}
	if !strings.Contains(body, "口令错误或与所选账户不匹配") {
		t.Fatalf("错口令未给出中文原因:\n%s", body)
	}

	// ④ 选对账户 + 对口令 → 303 进入内容区（AC8.2）
	if _, code = desktopPost(t, base+"login", url.Values{
		"account": {workKs}, "password": {pwWork},
	}); code != http.StatusSeeOther {
		t.Fatalf("选对账户应 303，实际 code=%d", code)
	}
}

// T8.5（AC8.5）：一个账户都没有时不渲染选择器（只留口令框），并提示先用 CLI 新建账户。
func TestDesktopLoginWithoutAnyAccount(t *testing.T) {
	home := t.TempDir()
	libDir := filepath.Join(t.TempDir(), "lib")
	if err := os.MkdirAll(libDir, 0o700); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(home, ".safemedia", "keystore.json")

	eng, _, err := buildEngine(desktopcfg.Target{
		Kind: desktopcfg.KindLibrary, Path: libDir, Account: missing,
	}, home, desktopcfg.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	base, err := eng.Start()
	if err != nil {
		t.Fatal(err)
	}

	page := desktopGet(t, base)
	if strings.Contains(page, `<select id="account"`) {
		t.Fatalf("无账户却渲染了账户选择器:\n%s", page)
	}
	body, code := desktopPost(t, base+"login", url.Values{"password": {"whatever passphrase"}})
	if code == http.StatusSeeOther {
		t.Fatalf("无账户不该解锁成功（code=%d）", code)
	}
	if !strings.Contains(body, "safemedia account create") {
		t.Fatalf("未提示先用 CLI 新建账户:\n%s", body)
	}
}

// writeTestKeystore 造一个低成本 scrypt 的测试账户。
func writeTestKeystore(t *testing.T, path, password string) {
	t.Helper()
	acct, err := account.Generate(12)
	if err != nil {
		t.Fatal(err)
	}
	if err := account.SaveKeystore(path, acct, password, 1<<10); err != nil {
		t.Fatal(err)
	}
}

func desktopGet(t *testing.T, u string) string {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// desktopPost 提交表单且不跟随重定向（要断言 303 本身）。
func desktopPost(t *testing.T, u string, form url.Values) (string, int) {
	t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := c.PostForm(u, form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), resp.StatusCode
}
