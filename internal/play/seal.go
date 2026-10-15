package play

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cc14514/go-safemedia/internal/seal"
)

// SealQueue 是引擎对封存队列的最小依赖（实现为 internal/seal.Queue）。
// 引擎只读它的状态、必要时中止它；不关心它怎么封存。
type SealQueue interface {
	Items() []seal.Item
	Active() bool
	Cancel()
	Done() <-chan struct{}
	Summary() (done, failed, skipped, canceled int)
}

// SealFactory 由调用方提供：把展开好的任务交给一个队列并启动
// （返回时队列必须已在运行；pub 是解锁账户的公钥）。返回 nil 表示无法启动。
type SealFactory func(jobs []seal.Job, pub *ecdsa.PublicKey) SealQueue

// sealReject 是一条未封存的输入（供面板逐条展示原因）。
type sealReject struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// sealItemJSON 是面板与 /seal/status 共用的条目视图。
type sealItemJSON struct {
	Name    string `json:"name"`
	Src     string `json:"src"`
	State   string `json:"state"`
	Percent int    `json:"percent"`
	Done    string `json:"done"`
	Total   string `json:"total"`
	Reason  string `json:"reason"`
}

// sealStatus 是 /seal/status 的响应，也是面板首屏渲染的数据。
type sealStatus struct {
	Active  bool           `json:"active"`
	Output  string         `json:"output"`
	Items   []sealItemJSON `json:"items"`
	Rejects []sealReject   `json:"rejects"`
	Summary string         `json:"summary"`
}

// sealAvailable 表示当前模式支持封存（库模式注入 Sealer 后为真）。
func (c *core) sealAvailable() bool { return c.sealer != nil }

// dropURL 返回拖放投递地址（随机 token；与媒体地址同款）。
func (c *core) dropURL() string {
	return strings.TrimSuffix(c.url, "/") + "/drop/" + c.dropToken
}

// sealOutputDir 返回当前输出目录：面板里改过就用改过的，否则用调用方给的默认（库根）。
func (c *core) sealOutputDir() string {
	c.sealMu.Lock()
	if c.sealOut != "" {
		out := c.sealOut
		c.sealMu.Unlock()
		return out
	}
	c.sealMu.Unlock()
	if c.sealDefaultOut != nil {
		return c.sealDefaultOut()
	}
	return ""
}

// setSealOutputDir 记录本次会话的输出目录（不落盘）。
func (c *core) setSealOutputDir(dir string) {
	c.sealMu.Lock()
	c.sealOut = dir
	c.sealMu.Unlock()
}

// queueActive 表示封存队列正在运行（空闲锁定据此跳过判定，AC10.9）。
func (c *core) queueActive() bool {
	c.sealMu.Lock()
	q := c.queue
	c.sealMu.Unlock()
	return q != nil && q.Active()
}

// cancelSeal 中止当前队列（显式锁定/用户点取消时调用）。
func (c *core) cancelSeal() {
	c.sealMu.Lock()
	q := c.queue
	c.sealMu.Unlock()
	if q != nil {
		q.Cancel()
	}
}

// sealStatusNow 汇总当前状态（面板首屏与轮询共用）。
func (c *core) sealStatusNow() sealStatus {
	st := sealStatus{Output: c.sealOutputDir()}
	c.sealMu.Lock()
	q := c.queue
	st.Rejects = append(st.Rejects, c.sealRejects...)
	c.sealMu.Unlock()

	if q == nil {
		return st
	}
	st.Active = q.Active()
	items := q.Items()
	st.Items = make([]sealItemJSON, 0, len(items))
	for _, it := range items {
		st.Items = append(st.Items, sealItemJSON{
			Name:    baseName(it.Src),
			Src:     it.Src,
			State:   it.State.Label(),
			Percent: it.Percent(),
			Done:    humanBytes(it.Done),
			Total:   humanBytes(it.Total),
			Reason:  it.Reason,
		})
	}
	done, failed, skipped, canceled := q.Summary()
	parts := []string{fmt.Sprintf("完成 %d", done)}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("失败 %d", failed))
	}
	if skipped > 0 {
		parts = append(parts, fmt.Sprintf("跳过 %d", skipped))
	}
	if canceled > 0 {
		parts = append(parts, fmt.Sprintf("已取消 %d", canceled))
	}
	st.Summary = strings.Join(parts, " · ")
	return st
}

// handleDrop 接收外壳通过原生拖放投递的路径（POST /drop/<token>）。
//
// 鉴权与媒体路由同一路数：只认「随机 token」——投递来自外壳进程、不持有会话 cookie；
// 该地址从不渲染进页面，128 位随机 token 就是凭证。锁定态一律拒绝且不写任何文件（AC10.7）。
func (c *core) handleDrop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只接受 POST", http.StatusMethodNotAllowed)
		return
	}
	if got := strings.TrimPrefix(r.URL.Path, "/drop/"); got != c.dropToken || c.dropToken == "" {
		http.Error(w, "未知的投递地址", http.StatusNotFound)
		return
	}
	if !c.unlocked.Load() {
		writeJSONError(w, http.StatusConflict, "请先解锁后再拖入视频")
		return
	}
	if !c.sealAvailable() {
		writeJSONError(w, http.StatusConflict, "当前模式不支持封存")
		return
	}

	var paths []string
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&paths); err != nil {
		writeJSONError(w, http.StatusBadRequest, "投递内容无法解析")
		return
	}

	c.touch()
	out := c.sealOutputDir()
	res, _ := seal.Collect(paths, out) // 致命输入错误由 rejects 逐条呈现（AC10.4）

	c.sealMu.Lock()
	c.sealRejects = nil
	for _, rj := range res.Rejects {
		c.sealRejects = append(c.sealRejects, sealReject{Path: baseName(rj.Path), Reason: rj.Reason})
	}
	c.sealMu.Unlock()

	started := 0
	if len(res.Jobs) > 0 {
		if q := c.startSeal(res.Jobs); q != nil {
			started = len(res.Jobs)
		}
	}
	c.touch()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jobs":    started,
		"skipped": res.Skipped,
		"rejects": len(res.Rejects),
	})
}

// startSeal 构造并启动队列；返回 nil 表示无法启动（未解锁/无公钥）。
func (c *core) startSeal(jobs []seal.Job) SealQueue {
	if c.startSealFn == nil {
		return nil
	}
	q := c.startSealFn(jobs)
	if q == nil {
		return nil
	}
	c.sealMu.Lock()
	c.queue = q
	c.sealMu.Unlock()

	go func() {
		<-q.Done()
		c.touch()
		if c.onSealDone != nil {
			c.onSealDone() // 队列结束（含部分失败）后重扫库根（AC10.10）
		}
	}()
	return q
}

// handleSeal 渲染封存面板（解锁态；锁定态由 gate 送回登录页）。
func (c *core) handleSeal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "只接受 GET", http.StatusMethodNotAllowed)
		return
	}
	writeNoCacheHeaders(w)
	st := c.sealStatusNow()
	v := sealPanelView{Target: c.target, Status: st}
	page := sealPanelPage(v)
	if page == "" {
		return
	}
	_, _ = w.Write([]byte(page))
}

// handleSealStatus 返回进度 JSON（面板每秒轮询）。
//
// 注意：这里**不 touch()** 之外的额外处理——轮询算作活动（用户正开着面板看进度），
// 但队列运行时空闲锁定本来就会跳过（AC10.9）。
func (c *core) handleSealStatus(w http.ResponseWriter, r *http.Request) {
	writeNoCacheHeaders(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(c.sealStatusNow())
}

// handleSealCancel 中止队列（面板上的「取消封存」）。
func (c *core) handleSealCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只接受 POST", http.StatusMethodNotAllowed)
		return
	}
	c.cancelSeal()
	http.Redirect(w, r, "/seal", http.StatusSeeOther)
}

// handleSealOutput 处理「更改输出目录」（复用目录选择页，purpose=seal）。
func (c *core) handleSealOutput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只接受 POST", http.StatusMethodNotAllowed)
		return
	}
	dir := strings.TrimSpace(r.FormValue("path"))
	if dir == "" {
		http.Redirect(w, r, "/seal", http.StatusSeeOther)
		return
	}
	c.setSealOutputDir(dir)
	http.Redirect(w, r, "/seal", http.StatusSeeOther)
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// baseName 只取最后一段（面板展示用；完整路径仍在 JSON 里）。
func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 && i+1 < len(p) {
		return p[i+1:]
	}
	return p
}
