package e2e

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cc14514/go-safemedia/internal/play"
	"github.com/cc14514/go-safemedia/internal/seal"
)

// sealPanelFixture 起一个真实引擎（CLI 形态：构造时即持钥、无会话 cookie），
// 把真实样本投进封存队列，返回面板地址与样本文件名。
func sealPanelFixture(t *testing.T) (base, name string) {
	t.Helper()
	return sealPanelFixtureWith(t, nil)
}

// sealPanelFixtureWith 同上，但可注入选择器（US11）。
func sealPanelFixtureWith(t *testing.T, picker play.FilePicker) (base, name string) {
	t.Helper()
	samples := loadSamples(t)
	if len(samples) == 0 {
		t.Skip("没有可用样本")
	}
	dir := t.TempDir()
	src, _ := writeSample(t, dir, samples[0])

	priv := e2eTestKey(t)
	factory := func(jobs []seal.Job, pub *ecdsa.PublicKey) play.SealQueue {
		q := seal.NewQueue(jobs, seal.Options{RemovePartialOnFailure: true})
		q.Start(pub)
		return q
	}
	lib, err := play.NewLibrary(play.LibraryOptions{
		Priv:   priv,
		Root:   dir,
		Target: dir,
		Sealer: factory,
		Picker: picker,
	})
	if err != nil {
		t.Fatalf("起库失败: %v", err)
	}
	base, err = lib.Start()
	if err != nil {
		t.Fatalf("起服务失败: %v", err)
	}
	t.Cleanup(func() { _ = lib.Close() })

	// 投递视频（等价于外壳把拖入的路径 POST 过来）。
	if code, body := postJSONURL(t, lib.DropURL(), []string{src}); code != 200 {
		t.Fatalf("投递封存失败: code=%d body=%s", code, body)
	}
	return base, filepath.Base(src)
}

// e2ePicker 是 play.FilePicker 的替身（L3 只验证页面侧，不弹真对话框）。
type e2ePicker struct {
	err error
}

func (p *e2ePicker) PickFiles() ([]string, error) { return nil, p.err }
func (p *e2ePicker) PickFolder() (string, error)  { return "", p.err }

// TestE2E_SealPanelPickEntryAndErrorRendered：
// ① 选择入口在真实浏览器里可见（服务端渲染的按钮，不是链接）；
// ② 选择对话框失败时，面板把它渲染成**警示框**（不是只把错误塞进首屏 payload）。
//
// ②的断言故意落在 `id="err"><div class="warn">` 这个**只有 JS 渲染才会产生的序列化结果**上：
// 只查「DOM 文本里含这段错误信息」是假通过——首屏 payload 就在 <script> 里，
// 渲染彻底失败也照样"包含"（AGENTS 易错点 9 / 项目记忆）。
func TestE2E_SealPanelPickEntryAndErrorRendered(t *testing.T) {
	chrome := requireChrome(t)
	pk := &e2ePicker{}
	base, _ := sealPanelFixtureWith(t, pk)

	// ① 入口可见 + 必须是按钮
	dom := dumpDOM(t, chrome, base+"seal/", 1200*time.Millisecond)
	okEntry := strings.Contains(dom, `action="/seal/pick/files"`) &&
		strings.Contains(dom, `action="/seal/pick/folder"`) &&
		!strings.Contains(dom, `<a href="/seal/pick`)
	record("US11 AC11.5 封存面板在浏览器里出现「选择视频文件…／选择文件夹…」按钮",
		"只有拖放入口（且实测拖动无效）", "面板上有两个原生按钮式的选择入口",
		"chrome --headless --dump-dom "+base+"seal/", okEntry)

	// ② 让选择对话框失败，再把面板 HTML 存成文件用浏览器打开（隔离每秒轮询，
	//    这样出现的警示框只可能来自首屏渲染）。
	pk.err = errors.New("选择面板被系统拒绝")
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := noRedirect.PostForm(base+"seal/pick/files", url.Values{})
	if err != nil {
		t.Fatalf("提交选择入口失败: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("选择入口应 303 回面板，得到 %d", resp.StatusCode)
	}

	raw := getBodyE2E(t, base+"seal/")
	htmlPath := filepath.Join(t.TempDir(), "panel-err.html")
	if err := os.WriteFile(htmlPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	domErr := dumpDOM(t, chrome, "file://"+htmlPath, 800*time.Millisecond)
	okWarn := strings.Contains(domErr, `id="err"><div class="warn">`) &&
		strings.Contains(domErr, "选择面板被系统拒绝")
	if !okWarn {
		t.Errorf("对话框失败未渲染成警示框；DOM 片段：%s", clip(domErr, 400))
	}
	record("US11 AC11.8 选择对话框失败在面板上渲染为警示框",
		"失败只体现在首屏 payload（页面看着像没反应）", "浏览器里渲染出警示框并带中文原因",
		"chrome --headless --dump-dom file://<面板 HTML>", okWarn)
}

func getBodyE2E(t *testing.T, u string) string {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("GET %s 失败: %v", u, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestE2E_SealPanelRendersInBrowser 用无头 Chrome 打开真实引擎的封存面板，
// 校验页面在浏览器里真的渲染出条目与其进度（AC10.5）。
func TestE2E_SealPanelRendersInBrowser(t *testing.T) {
	chrome := requireChrome(t)
	base, name := sealPanelFixture(t)
	dom := dumpDOM(t, chrome, base+"seal/", 2*time.Second)
	ok := assertPanelRendered(t, dom, name)
	record("US10 AC10.5 封存面板在浏览器中渲染进度行",
		"无页面", "真实浏览器里看到条目「"+name+"」的进度",
		"chrome --headless --dump-dom "+base+"seal/", ok)
}

// TestE2E_SealPanelBootFirstPaint 只校验**首屏 HTML 自身的渲染**，与每秒轮询解耦。
//
// 做法：把引擎返回的面板 HTML 存成文件、用 file:// 打开——此时页面的
// fetch('/seal/status') 被浏览器拒绝（跨协议），轮询永远不会成功，因此页面上出现的
// 任何条目都只可能来自首屏注入的那份状态。
//
// 曾有一版把首屏状态塞进 <script type="application/json"> 再 JSON.parse(textContent)：
// html/template 在该位置仍按 JS 上下文转义，于是注入的是**字符串字面量**（带引号），
// parse 出来是个字符串、d.items 为 undefined，首屏静默渲染成空——脚本不报错、
// Go 侧测试全绿，只有真正执行 JS 的浏览器能看见。这个用例就是钉住它。
func TestE2E_SealPanelBootFirstPaint(t *testing.T) {
	chrome := requireChrome(t)
	base, name := sealPanelFixture(t)

	resp, err := http.Get(base + "seal/")
	if err != nil {
		t.Fatalf("取面板 HTML 失败: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("面板 HTTP 状态 = %d", resp.StatusCode)
	}
	htmlPath := filepath.Join(t.TempDir(), "panel.html")
	if err := os.WriteFile(htmlPath, raw, 0o644); err != nil {
		t.Fatalf("写面板 HTML 失败: %v", err)
	}

	dom := dumpDOM(t, chrome, "file://"+htmlPath, 800*time.Millisecond)
	ok := assertPanelRendered(t, dom, name)
	record("US10 AC10.5 封存面板首屏自身渲染（轮询被隔离）",
		"首屏只渲染空状态，靠 1s 轮询补救",
		"首屏 HTML 自身已渲染条目「"+name+"」",
		"chrome --headless --dump-dom file://<面板 HTML>", ok)
}

// assertPanelRendered 判断页面是否真的把封存条目渲染进了 DOM。
//
// 注意：不能只看「DOM 文本里有没有文件名」——首屏注入的状态是 <script> 里的 JS 源码，
// --dump-dom 会把它一起打出来，于是即使渲染彻底失败、文件名也照样出现在 DOM 文本里
// （这一度让本用例在缺陷版本上误判为通过）。因此这里检查**只有渲染才会产生的序列化结果**：
// render() 在有条目时会把空状态 div 隐藏，DOM 序列化里会出现 id="empty" style="display: none;"；
// 该属性组合在脚本源码与 CSS 里都不存在。
func assertPanelRendered(t *testing.T, dom, name string) bool {
	t.Helper()
	hidden := strings.Contains(dom, `id="empty" style="display: none;`)
	hasName := strings.Contains(dom, name)
	if !hidden {
		t.Errorf("首屏/页面未渲染条目（空状态仍可见，说明没走渲染路径）；DOM 片段：%s", clip(dom, 300))
	} else if !hasName {
		t.Errorf("渲染了条目但看不到样本名 %s；DOM 片段：%s", name, clip(dom, 300))
	}
	return hidden && hasName
}

// requireChrome 返回无头 Chrome 路径；无浏览器环境时跳过（与其它浏览器用例一致）。
func requireChrome(t *testing.T) string {
	t.Helper()
	if os.Getenv("SAFEMEDIA_E2E_NO_BROWSER") == "1" {
		t.Skip("SAFEMEDIA_E2E_NO_BROWSER=1：跳过浏览器用例")
	}
	chrome := findChrome()
	if chrome == "" {
		t.Skip("未找到无头 Chrome：跳过浏览器用例")
	}
	return chrome
}

// postJSONURL 把字符串数组以 JSON 投递到 url，返回状态码与响应体。
func postJSONURL(t *testing.T, url string, body []string) (int, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("编码失败: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("投递失败: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// e2eTestKey 造一把测试用私钥（封存只需要公钥）。
func e2eTestKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成测试密钥失败: %v", err)
	}
	return priv
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
