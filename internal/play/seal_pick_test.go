package play_test

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cc14514/go-safemedia/internal/play"
)

// fakePicker 是系统原生选择对话框的替身（US11）。
//
// 它模拟的是外壳注入的实现：只回**路径**；用户取消 → 空值 + nil error；
// 对话框自身失败 → error。测试据此观察引擎侧的行为（引擎不 import Wails）。
type fakePicker struct {
	mu     sync.Mutex
	files  []string
	folder string
	err    error
	calls  atomic.Int64
}

func (p *fakePicker) PickFiles() ([]string, error) {
	p.calls.Add(1)
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.files, p.err
}

func (p *fakePicker) PickFolder() (string, error) {
	p.calls.Add(1)
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.folder, p.err
}

func (p *fakePicker) setFiles(paths ...string) {
	p.mu.Lock()
	p.files = paths
	p.mu.Unlock()
}

// T11.1/T11.2（AC11.1/AC11.2/AC11.6）：选择文件与拖入走**同一条管线**：
// 同一套展开规则、同一套拒绝原因、默认落到库根、队列照常启动。
func TestPickFilesEnqueuesLikeDrop(t *testing.T) {
	priv := newKey(t)
	picker := &fakePicker{}
	video := filepath.Join(t.TempDir(), "v.mp4")
	writeTestFile(t, video, "mp4")
	notVideo := filepath.Join(t.TempDir(), "notes.txt")
	writeTestFile(t, notVideo, "hello")
	picker.setFiles(video, notVideo)

	h := testLibraryWithPicker(t, priv, picker)
	lib := h.lib

	resp := postForm(t, lib.URL(), "seal/pick/files", url.Values{})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("选择后应 303 回面板，得到 %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/seal" {
		t.Fatalf("应回到 /seal，得到 %q", loc)
	}
	if picker.calls.Load() != 1 {
		t.Fatalf("应正好调用一次选择器，得到 %d", picker.calls.Load())
	}

	jobs := h.jobsSnapshot()
	if len(jobs) != 1 {
		t.Fatalf("白名单内的选择应展开为 1 个任务，得到 %d", len(jobs))
	}
	if want := filepath.Join(lib.Root(), "v.mp4.svid"); jobs[0].Dst != want {
		t.Fatalf("默认输出目录应为库根：want %s got %s", want, jobs[0].Dst)
	}
	if h.queue() == nil {
		t.Fatal("应创建队列（与拖入相同）")
	}

	// 非白名单条目仍逐条给出原因（AC10.4 对选择入口同样成立）。
	st := getJSON(t, lib.URL()+"seal/status")
	if st["active"] != true {
		t.Fatalf("队列应处于运行中：%+v", st)
	}
	rejects, _ := st["rejects"].([]any)
	if len(rejects) != 1 {
		t.Fatalf("应有一条拒绝记录：%+v", st)
	}
	if !strings.Contains(st["rejects"].([]any)[0].(map[string]any)["reason"].(string), "不支持") {
		t.Fatalf("拒绝原因应说明容器不受支持：%+v", rejects)
	}
}

// T11.2b（AC11.3）：选择文件夹 = 目录输入，递归展开（与把文件夹拖进来一致）。
func TestPickFolderWalksDirectory(t *testing.T) {
	priv := newKey(t)
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "a.mp4"), "mp4")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "sub", "b.mp4"), "mp4")

	picker := &fakePicker{folder: root}
	h := testLibraryWithPicker(t, priv, picker)
	lib := h.lib

	resp := postForm(t, lib.URL(), "seal/pick/folder", url.Values{})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("选择文件夹后应 303，得到 %d", resp.StatusCode)
	}
	jobs := h.jobsSnapshot()
	if len(jobs) != 2 {
		t.Fatalf("文件夹内应递归展开为 2 个任务，得到 %d：%+v", len(jobs), jobs)
	}
	// 保留子目录结构（与 CLI/拖入同一套展开规则）。
	var sawSub bool
	for _, j := range jobs {
		if filepath.Dir(j.Dst) == filepath.Join(lib.Root(), "sub") {
			sawSub = true
		}
	}
	if !sawSub {
		t.Fatalf("子目录结构应被保留：%+v", jobs)
	}
}

// T11.3（AC11.4）：用户取消选择（返回空）→ 只回面板，**不改变任何状态**：
// 不启动空队列、不清空已展示的进度、不清掉上一次的拒绝原因。
func TestPickCancelKeepsState(t *testing.T) {
	priv := newKey(t)
	picker := &fakePicker{}
	video := filepath.Join(t.TempDir(), "v.mp4")
	writeTestFile(t, video, "mp4")
	notVideo := filepath.Join(t.TempDir(), "notes.txt")
	writeTestFile(t, notVideo, "hello")
	picker.setFiles(video, notVideo)

	h := testLibraryWithPicker(t, priv, picker)
	lib := h.lib

	// 先来一次有效选择：建立「有队列 + 有拒绝记录」的状态。
	if resp := postForm(t, lib.URL(), "seal/pick/files", url.Values{}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("首次选择应 303，得到 %d", resp.StatusCode)
	}
	firstQueue := h.queue()
	if firstQueue == nil {
		t.Fatal("首次选择应创建队列")
	}
	before := getJSON(t, lib.URL()+"seal/status")
	beforeRejects, _ := before["rejects"].([]any)
	if len(beforeRejects) != 1 {
		t.Fatalf("前置条件：应有 1 条拒绝记录：%+v", before)
	}
	beforeItems, _ := before["items"].([]any)

	// 取消：选择器返回空。
	picker.setFiles()
	if resp := postForm(t, lib.URL(), "seal/pick/files", url.Values{}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("取消也应 303 回面板，得到 %d", resp.StatusCode)
	}
	// 取消文件夹同理。
	picker.mu.Lock()
	picker.folder = ""
	picker.mu.Unlock()
	if resp := postForm(t, lib.URL(), "seal/pick/folder", url.Values{}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("取消文件夹也应 303 回面板，得到 %d", resp.StatusCode)
	}

	if h.queue() != firstQueue {
		t.Fatal("取消不得替换/新建队列")
	}
	after := getJSON(t, lib.URL()+"seal/status")
	afterRejects, _ := after["rejects"].([]any)
	if len(afterRejects) != len(beforeRejects) {
		t.Fatalf("取消不得清空拒绝记录：before=%d after=%d", len(beforeRejects), len(afterRejects))
	}
	afterItems, _ := after["items"].([]any)
	if len(afterItems) != len(beforeItems) {
		t.Fatalf("取消不得清空进度条目：before=%d after=%d", len(beforeItems), len(afterItems))
	}
	if after["error"] != "" {
		t.Fatalf("取消不是错误，不应记录错误提示：%+v", after["error"])
	}
}

// T11.5（AC11.8）：选择器自身失败 → 面板显示中文原因，且不影响已入队条目。
func TestPickErrorSurfacesInPanel(t *testing.T) {
	priv := newKey(t)
	picker := &fakePicker{}
	video := filepath.Join(t.TempDir(), "v.mp4")
	writeTestFile(t, video, "mp4")
	picker.setFiles(video)

	h := testLibraryWithPicker(t, priv, picker)
	lib := h.lib
	if resp := postForm(t, lib.URL(), "seal/pick/files", url.Values{}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("首次选择应 303，得到 %d", resp.StatusCode)
	}
	firstQueue := h.queue()

	// 再选一次，这次对话框失败。
	picker.mu.Lock()
	picker.err = errors.New("面板不可用")
	picker.mu.Unlock()
	if resp := postForm(t, lib.URL(), "seal/pick/files", url.Values{}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("失败也应 303 回面板（错误在面板里显示），得到 %d", resp.StatusCode)
	}

	if h.queue() != firstQueue {
		t.Fatal("选择器失败不得新建队列")
	}
	st := getJSON(t, lib.URL()+"seal/status")
	msg, _ := st["error"].(string)
	if !strings.Contains(msg, "打开文件选择框失败") || !strings.Contains(msg, "面板不可用") {
		t.Fatalf("面板应显示可读的失败原因，得到 %q", msg)
	}
	// 面板首屏的 payload 也要带上它（渲染成警示框由 L3 的浏览器用例断言：
	// 只查 HTML 源码里有没有这段字符串是**假通过**，见 AGENTS 易错点 9/项目记忆）。
	html := getString(t, lib.URL()+"seal")
	if !strings.Contains(html, `id="err"`) || !strings.Contains(html, "打开文件选择框失败") {
		t.Fatalf("面板首屏 payload 应带上错误提示：%s", head(html))
	}
	// 成功一次之后错误应被清掉。
	picker.mu.Lock()
	picker.err = nil
	picker.mu.Unlock()
	if resp := postForm(t, lib.URL(), "seal/pick/files", url.Values{}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("再次成功应 303，得到 %d", resp.StatusCode)
	}
	if st := getJSON(t, lib.URL()+"seal/status"); st["error"] != "" {
		t.Fatalf("成功后应清掉错误提示：%+v", st["error"])
	}
}

// T11.4（AC11.5）：未注入选择器（CLI/无外壳）时面板不渲染两个按钮，
// 且路由给出明确原因（而不是 404/panic）。
func TestPickButtonsHiddenWithoutPicker(t *testing.T) {
	priv := newKey(t)
	h := testLibraryWithSealer(t, priv, play.DefaultIdleTimeout)
	lib := h.lib

	html := getString(t, lib.URL()+"seal")
	if strings.Contains(html, "选择视频文件") || strings.Contains(html, "选择文件夹") {
		t.Fatalf("未注入选择器时不应渲染选择按钮：%s", head(html))
	}
	if !strings.Contains(html, "拖到窗口上") {
		t.Fatalf("应保留拖入提示：%s", head(html))
	}

	resp := postForm(t, lib.URL(), "seal/pick/files", url.Values{})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("应 303 回面板，得到 %d", resp.StatusCode)
	}
	st := getJSON(t, lib.URL()+"seal/status")
	if msg, _ := st["error"].(string); !strings.Contains(msg, "不支持") {
		t.Fatalf("应说明当前模式不支持选择：%+v", st)
	}
}

// T11.6（AC11.5）：注入选择器后按钮出现在封存面板（原生外观，遵守 AC7.9）。
func TestPickButtonsRenderedWithPicker(t *testing.T) {
	priv := newKey(t)
	h := testLibraryWithPicker(t, priv, &fakePicker{})
	html := getString(t, h.lib.URL()+"seal")

	for _, want := range []string{
		`action="/seal/pick/files"`,
		`action="/seal/pick/folder"`,
		"选择视频文件…",
		"选择文件夹…",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("封存面板应包含 %q：%s", want, head(html))
		}
	}
	// 外观约束：只能是 <button>（不是裸 <a>）。
	if strings.Contains(html, "<a href=\"/seal/pick") {
		t.Fatalf("选择入口必须是提交按钮，不能是链接：%s", head(html))
	}
}

// T11.4b（AC11.7）：锁定态的选择路由被门控挡下 —— 回登录页，且**不写任何文件**、
// 不创建队列、也不枚举本机目录（选择器根本不被调用）。
func TestPickRejectedWhenLocked(t *testing.T) {
	picker := &fakePicker{folder: t.TempDir()}
	h := testLibraryWithPicker(t, nil, picker) // nil priv → 锁定态启动
	lib := h.lib
	if !lib.Locked() {
		t.Fatal("应以锁定态启动")
	}

	for _, path := range []string{"seal/pick/files", "seal/pick/folder"} {
		resp := postForm(t, lib.URL(), path, url.Values{})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("锁定态 %s 应落到登录页（200），得到 %d", path, resp.StatusCode)
		}
		page := bodyOf(t, resp)
		if !strings.Contains(page, `action="/login"`) {
			t.Fatalf("锁定态 %s 应渲染登录页，得到：%s", path, head(page))
		}
	}
	if picker.calls.Load() != 0 {
		t.Fatalf("锁定态不得调用选择器（不枚举本机目录），得到 %d 次", picker.calls.Load())
	}
	if h.queue() != nil || len(h.jobsSnapshot()) != 0 {
		t.Fatal("锁定态选择不得创建队列")
	}
	if des, err := os.ReadDir(lib.Root()); err != nil || len(des) != 0 {
		t.Fatalf("锁定态选择不得产生任何文件：%v %v", des, err)
	}
}
