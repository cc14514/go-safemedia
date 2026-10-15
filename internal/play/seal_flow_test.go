package play_test

import (
	"crypto/ecdsa"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cc14514/go-safemedia/internal/play"
	"github.com/cc14514/go-safemedia/internal/seal"
)

// fakeQueue 是可控的封存队列替身（T10.1–T10.6 用它观察引擎的行为）。
type fakeQueue struct {
	jobs     []seal.Job
	active   atomic.Bool
	canceled atomic.Bool
	done     chan struct{}
	once     sync.Once
}

func newFakeQueue(jobs []seal.Job) *fakeQueue {
	q := &fakeQueue{jobs: jobs, done: make(chan struct{})}
	q.active.Store(true)
	return q
}

func (q *fakeQueue) Items() []seal.Item {
	items := make([]seal.Item, 0, len(q.jobs))
	for _, j := range q.jobs {
		items = append(items, seal.Item{
			Src: j.Src, Dst: j.Dst, State: seal.StateRunning, Done: 5, Total: 10,
		})
	}
	return items
}

func (q *fakeQueue) Active() bool          { return q.active.Load() }
func (q *fakeQueue) Done() <-chan struct{} { return q.done }
func (q *fakeQueue) Cancel()               { q.canceled.Store(true); q.finish() }
func (q *fakeQueue) Summary() (int, int, int, int) {
	if q.active.Load() {
		return 0, 0, 0, 0
	}
	if q.canceled.Load() {
		return 0, 0, 0, len(q.jobs)
	}
	return len(q.jobs), 0, 0, 0
}

func (q *fakeQueue) finish() {
	q.once.Do(func() {
		q.active.Store(false)
		close(q.done)
	})
}

// sealHarness 包住测试用的库实例与「投递后才会创建」的假队列。
// 注意：队列是在投递时才由工厂创建的，所以必须用取值方法读取，不能提前拷贝指针。
type sealHarness struct {
	lib  *play.Library
	mu   sync.Mutex
	made *fakeQueue
	jobs []seal.Job
}

func (h *sealHarness) queue() *fakeQueue {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.made
}

func (h *sealHarness) jobsSnapshot() []seal.Job {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]seal.Job(nil), h.jobs...)
}

// testLibraryWithSealer 建一个注入假队列的库模式实例。
// priv 为 nil 时以「锁定态」启动（用 Unlock 回调模拟桌面外壳）。
func testLibraryWithSealer(t *testing.T, priv *ecdsa.PrivateKey, idle time.Duration) *sealHarness {
	t.Helper()
	dir := t.TempDir()
	h := &sealHarness{}
	entries := []play.Entry{{Rel: "a.mp4.svid", Name: "a.mp4.svid", Size: 10, Path: filepath.Join(dir, "a.mp4.svid")}}

	opts := play.LibraryOptions{
		Entries:     entries,
		Target:      dir,
		Root:        dir,
		IdleTimeout: idle,
		Sealer: func(jobs []seal.Job, pub *ecdsa.PublicKey) play.SealQueue {
			if pub == nil {
				return nil
			}
			h.mu.Lock()
			defer h.mu.Unlock()
			h.jobs = append(h.jobs, jobs...)
			h.made = newFakeQueue(jobs)
			return h.made
		},
	}
	if priv != nil {
		opts.Priv = priv
	} else {
		key := newKey(t)
		opts.Unlock = func(_, _ string) (*ecdsa.PrivateKey, error) { return key, nil }
	}
	lib, err := play.NewLibrary(opts)
	if err != nil {
		t.Fatalf("构造库失败: %v", err)
	}
	if _, err := lib.Start(); err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	h.lib = lib
	t.Cleanup(func() { _ = lib.Close() })
	return h
}

func jobsSnapshot(t *testing.T, p *[]seal.Job) []seal.Job {
	t.Helper()
	return append([]seal.Job(nil), (*p)...)
}

// T10.1（AC10.7）：锁定态投递被拒且不产生任何文件。
func TestDropRejectedWhenLockedAndWritesNothing(t *testing.T) {
	h := testLibraryWithSealer(t, nil, play.DefaultIdleTimeout)
	lib := h.lib
	out := lib.Root()
	src := filepath.Join(t.TempDir(), "v.mp4")
	writeTestFile(t, src, "mp4")

	code, body := postJSON(t, lib.DropURL(), []string{src})
	if code != http.StatusConflict {
		t.Fatalf("锁定态投递应返回 409，得到 %d（%s）", code, body)
	}
	if !strings.Contains(body, "请先解锁") {
		t.Fatalf("锁定态投递应说明原因，得到 %s", body)
	}
	if h.queue() != nil || len(h.jobsSnapshot()) != 0 {
		t.Fatal("锁定态投递不得创建队列")
	}
	if des, err := os.ReadDir(out); err != nil || len(des) != 0 {
		t.Fatalf("锁定态投递不得产生文件：%v %v", des, err)
	}
}

// T10.1b（AC10.6）：会话 cookie 机制启用时（桌面外壳形态：起锁定态服务 + 登录），
// 投递仍必须成功——拖放来自外壳进程，它不持有 webview 的会话 cookie，
// 投递地址只认随机 token。曾因在 handleDrop 里校验会话 cookie 导致拖入无反应（403）。
func TestDropWorksWithSessionEnabled(t *testing.T) {
	h := testLibraryWithSealer(t, nil, play.DefaultIdleTimeout) // 走 Unlock 路径 → 启用会话 cookie
	lib := h.lib
	if !lib.Locked() {
		t.Fatal("应以便以锁定态启动")
	}
	// 登录解锁
	postForm(t, lib.URL(), "login", url.Values{"password": {"pw"}})

	src := filepath.Join(t.TempDir(), "v.mp4")
	writeTestFile(t, src, "mp4")
	code, body := postJSON(t, lib.DropURL(), []string{src})
	if code != http.StatusOK {
		t.Fatalf("会话启用时投递也应成功（拖放来自外壳进程，不带 cookie）：%d %s", code, body)
	}
	if h.queue() == nil {
		t.Fatal("应创建队列")
	}
}

// T10.2（AC10.6）：投递地址带随机 token，token 不对一律 404（防本机其它页面盲投）。
func TestDropRequiresValidToken(t *testing.T) {
	priv := newKey(t)
	h := testLibraryWithSealer(t, priv, play.DefaultIdleTimeout)
	lib := h.lib
	src := filepath.Join(t.TempDir(), "v.mp4")
	writeTestFile(t, src, "mp4")

	bad := strings.TrimSuffix(lib.DropURL(), "/") + "x"
	code, _ := postJSON(t, bad, []string{src})
	if code != http.StatusNotFound {
		t.Fatalf("错误 token 应返回 404，得到 %d", code)
	}
}

// T10.3（AC10.1/AC10.2/AC10.4）：投递展开为任务、默认输出到库根、非视频逐条给出原因。
func TestDropEnqueuesJobsAndReportsRejects(t *testing.T) {
	priv := newKey(t)
	h := testLibraryWithSealer(t, priv, play.DefaultIdleTimeout)
	lib := h.lib
	out := lib.Root()

	video := filepath.Join(t.TempDir(), "v.mp4")
	writeTestFile(t, video, "mp4")
	notVideo := filepath.Join(t.TempDir(), "notes.txt")
	writeTestFile(t, notVideo, "hello")

	code, body := postJSON(t, lib.DropURL(), []string{video, notVideo})
	if code != http.StatusOK {
		t.Fatalf("投递应成功，得到 %d（%s）", code, body)
	}
	var resp struct{ Jobs, Skipped, Rejects int }
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("投递响应不是 JSON: %v（%s）", err, body)
	}
	if resp.Jobs != 1 || resp.Rejects != 1 {
		t.Fatalf("期望 1 个任务 / 1 条拒绝，得到 %+v", resp)
	}
	if h.queue() == nil {
		t.Fatal("应创建队列")
	}
	jobs := h.jobsSnapshot()
	if len(jobs) != 1 {
		t.Fatalf("队列应收到 1 个任务，得到 %d", len(jobs))
	}
	// 默认输出目录 = 库根（AC10.3）
	if want := filepath.Join(out, "v.mp4.svid"); jobs[0].Dst != want {
		t.Fatalf("输出路径应为库根下的密档：want %s got %s", want, jobs[0].Dst)
	}

	// 面板：进度行 + 未封存原因都可见（AC10.4）
	html := getString(t, lib.URL()+"seal")
	if !strings.Contains(html, "notes.txt") {
		t.Fatalf("面板应逐条列出未封存原因，页面里没有 notes.txt：%s", head(html))
	}
	if !strings.Contains(html, "封存") {
		t.Fatalf("面板应显示封存进度区域")
	}
	st := getJSON(t, lib.URL()+"seal/status")
	if st["active"] != true {
		t.Fatalf("队列应处于运行中：%+v", st)
	}
	items, _ := st["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("状态应含 1 个条目：%+v", st)
	}
}

// T10.4（AC10.3）：面板里可以改输出目录，随后投递落到新目录。
func TestSealOutputDirCanBeChanged(t *testing.T) {
	priv := newKey(t)
	h := testLibraryWithSealer(t, priv, play.DefaultIdleTimeout)
	lib := h.lib
	newOut := t.TempDir()

	postForm(t, lib.URL(), "seal/output", url.Values{"path": {newOut}})
	src := filepath.Join(t.TempDir(), "v.mp4")
	writeTestFile(t, src, "mp4")
	if code, body := postJSON(t, lib.DropURL(), []string{src}); code != http.StatusOK {
		t.Fatalf("投递失败：%d %s", code, body)
	}
	jobs := h.jobsSnapshot()
	if len(jobs) != 1 || filepath.Dir(jobs[0].Dst) != newOut {
		t.Fatalf("输出应落在新目录 %s：%+v", newOut, jobs)
	}
}

// T10.5（AC10.8）：锁定态访问封存面板回到登录页；解锁后可访问。
func TestSealPanelFollowsLockState(t *testing.T) {
	h := testLibraryWithSealer(t, nil, play.DefaultIdleTimeout)
	lib := h.lib
	html := getString(t, lib.URL()+"seal")
	if !strings.Contains(html, `action="/login"`) {
		t.Fatalf("锁定态应回到登录页，得到：%s", head(html))
	}
}

// T10.6（AC10.9）：显式锁定中止封存队列。
func TestLockCancelsSealQueue(t *testing.T) {
	priv := newKey(t)
	h := testLibraryWithSealer(t, priv, play.DefaultIdleTimeout)
	lib := h.lib
	src := filepath.Join(t.TempDir(), "v.mp4")
	writeTestFile(t, src, "mp4")
	if code, body := postJSON(t, lib.DropURL(), []string{src}); code != http.StatusOK {
		t.Fatalf("投递失败：%d %s", code, body)
	}
	if h.queue() == nil || !h.queue().Active() {
		t.Fatal("队列应在运行")
	}
	postForm(t, lib.URL(), "lock", url.Values{})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !h.queue().canceled.Load() {
		time.Sleep(5 * time.Millisecond)
	}
	if !h.queue().canceled.Load() {
		t.Fatal("锁定应中止封存队列（AC10.9）")
	}
}

// T10.7（AC10.9）：封存进行中不自动锁定；队列结束后按空闲时长正常锁定。
func TestIdleWatchSkipsWhileSealing(t *testing.T) {
	priv := newKey(t)
	h := testLibraryWithSealer(t, priv, 60*time.Millisecond)
	lib := h.lib
	src := filepath.Join(t.TempDir(), "v.mp4")
	writeTestFile(t, src, "mp4")
	if code, body := postJSON(t, lib.DropURL(), []string{src}); code != http.StatusOK {
		t.Fatalf("投递失败：%d %s", code, body)
	}
	// 远超空闲阈值，但队列在跑 → 保持解锁
	time.Sleep(300 * time.Millisecond)
	if lib.Locked() {
		t.Fatal("封存进行中不得自动锁定（AC10.9）")
	}
	// 队列结束 → 空闲判定恢复，最终锁定
	h.queue().finish()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !lib.Locked() {
		time.Sleep(20 * time.Millisecond)
	}
	if !lib.Locked() {
		t.Fatal("队列结束后应按空闲阈值锁定")
	}
}

// T10.8（AC10.10）：队列结束后重扫库根（新密档无需重启即可见）。
func TestRescanAfterSealPicksUpNewVault(t *testing.T) {
	priv := newKey(t)
	h := testLibraryWithSealer(t, priv, play.DefaultIdleTimeout)
	lib := h.lib
	out := lib.Root()
	src := filepath.Join(t.TempDir(), "v.mp4")
	writeTestFile(t, src, "mp4")
	if code, body := postJSON(t, lib.DropURL(), []string{src}); code != http.StatusOK {
		t.Fatalf("投递失败：%d %s", code, body)
	}
	// 模拟封存完成：写出一份「密档」再结束队列
	if err := os.WriteFile(filepath.Join(out, "v.mp4.svid"), []byte("fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.queue().finish()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(getString(t, lib.URL()), "v.mp4.svid") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("封存结束后应重扫库根，新密档应出现在索引页")
}

// 未注入 Sealer（CLI 形态）不支持封存：投递给出明确原因。
func TestDropWithoutSealerIsRejected(t *testing.T) {
	priv := newKey(t)
	dir := t.TempDir()
	lib, err := play.NewLibrary(play.LibraryOptions{
		Entries: []play.Entry{{Rel: "a.mp4.svid", Name: "a.mp4.svid", Size: 1, Path: filepath.Join(dir, "a.mp4.svid")}},
		Target:  dir,
		Root:    dir,
		Priv:    priv,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lib.Close() }()

	src := filepath.Join(t.TempDir(), "v.mp4")
	writeTestFile(t, src, "mp4")
	code, body := postJSON(t, lib.DropURL(), []string{src})
	if code != http.StatusConflict || !strings.Contains(body, "不支持封存") {
		t.Fatalf("未注入 Sealer 时应明确拒绝：%d %s", code, body)
	}
}

// ---------- 小工具 ----------

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if content == "mp4" {
		// 合法 ftyp 头，使容器嗅探通过（内容不参与封存判定）。
		b := make([]byte, 64)
		copy(b[4:], "ftypisom")
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func postJSON(t *testing.T, url string, body any) (int, string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("投递失败: %v", err)
	}
	defer resp.Body.Close()
	outBytes, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(outBytes)
}

func getString(t *testing.T, url string) string {
	t.Helper()
	_, s := getStringStatus(t, url)
	return s
}

// getStringStatus 返回状态码与正文（断言状态码时用）。
func getStringStatus(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s 失败: %v", url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func getJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	raw := getString(t, url)
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("%s 不是 JSON: %v（%s）", url, err, head(raw))
	}
	return out
}

func head(s string) string {
	if len(s) > 400 {
		return s[:400]
	}
	return s
}
