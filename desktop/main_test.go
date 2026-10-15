package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
