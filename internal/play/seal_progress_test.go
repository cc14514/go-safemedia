package play_test

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cc14514/go-safemedia/internal/play"
	"github.com/cc14514/go-safemedia/internal/seal"
)

// T10.8（AC10.5）：进度面板不出现「0 B / 0 B」这类噪音，汇总也不列零计数。
//
// 跳过的条目没有大小可言（Total=0），此时应只显示状态与原因；
// 汇总若把「完成 0 · 取消 0」都列出来，反而看不出重点。
func TestSealStatusOmitsEmptySizesAndZeroCounts(t *testing.T) {
	priv := newKey(t)
	h := testLibraryWithSealer(t, priv, play.DefaultIdleTimeout)
	lib := h.lib

	video := filepath.Join(t.TempDir(), "clip.mp4")
	writeTestFile(t, video, "mp4")
	if code, body := postJSON(t, lib.DropURL(), []string{video}); code != http.StatusOK {
		t.Fatalf("投递应成功，得到 %d（%s）", code, body)
	}
	q := h.queue()
	if q == nil {
		t.Fatal("应创建队列")
	}
	jobs := h.jobsSnapshot()
	if len(jobs) != 1 {
		t.Fatalf("应有 1 个任务：%+v", jobs)
	}
	// 造一个「已跳过」的条目（Total=0，语义同「目标密档已存在」）。
	q.itemsOverride = []seal.Item{{
		Src: jobs[0].Src, Dst: jobs[0].Dst,
		State: seal.StateSkipped, Total: 0, Done: 0, Reason: "已存在（跳过）",
	}}

	code, body := getStringStatus(t, lib.URL()+"seal/status")
	if code != http.StatusOK {
		t.Fatalf("状态接口应 200，得到 %d", code)
	}
	var st struct {
		Summary string `json:"summary"`
		Items   []struct {
			State  string `json:"state"`
			Done   string `json:"done"`
			Total  string `json:"total"`
			Reason string `json:"reason"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatalf("状态不是 JSON: %v（%s）", err, body)
	}
	if len(st.Items) != 1 {
		t.Fatalf("应有 1 个条目：%s", body)
	}
	it := st.Items[0]
	if it.Done != "" || it.Total != "" {
		t.Fatalf("跳过的条目不应报告大小，得到 done=%q total=%q", it.Done, it.Total)
	}
	if it.Reason == "" {
		t.Fatalf("跳过的条目必须给出原因：%s", body)
	}
	if strings.Contains(st.Summary, "完成 0") || strings.Contains(st.Summary, "取消 0") ||
		strings.Contains(st.Summary, "已取消 0") {
		t.Fatalf("汇总不应列出零计数，得到 %q", st.Summary)
	}
	// 面板首屏同样不能出现 0 B 噪音（服务端渲染，AC10.5）。
	if html := getString(t, lib.URL()+"seal"); strings.Contains(html, "0 B / 0 B") {
		t.Fatalf("面板首屏出现了「0 B / 0 B」噪音：%s", head(html))
	}
}

// T10.7（AC10.3）：进度必须是**实时**的 —— 队列没结束、条目还在封存中时，
// `/seal/status` 就要报出中间进度，面板首屏也必须already显示它（不是等结束一次性出现）。
func TestSealStatusShowsLiveProgress(t *testing.T) {
	priv := newKey(t)
	h := testLibraryWithSealer(t, priv, play.DefaultIdleTimeout)
	lib := h.lib

	video := filepath.Join(t.TempDir(), "clip.mp4")
	writeTestFile(t, video, "mp4")
	if code, body := postJSON(t, lib.DropURL(), []string{video}); code != http.StatusOK {
		t.Fatalf("投递应成功，得到 %d（%s）", code, body)
	}
	q := h.queue()
	if q == nil {
		t.Fatal("应创建队列")
	}

	// 队列仍在运行：状态里必须已经能看到中间进度，而不是「等待」或「完成」。
	if !q.Active() {
		t.Fatal("假队列应仍在运行，才谈得上实时进度")
	}
	code, body := getStringStatus(t, lib.URL()+"seal/status")
	if code != http.StatusOK {
		t.Fatalf("状态接口应 200，得到 %d", code)
	}
	var st struct {
		Active bool `json:"active"`
		Items  []struct {
			Name    string `json:"name"`
			State   string `json:"state"`
			Percent int    `json:"percent"`
			Done    string `json:"done"`
			Total   string `json:"total"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatalf("状态不是 JSON: %v（%s）", err, body)
	}
	if !st.Active {
		t.Fatalf("active 应为 true：%s", body)
	}
	if len(st.Items) != 1 {
		t.Fatalf("应有 1 个条目：%s", body)
	}
	it := st.Items[0]
	if it.State != "封存中" {
		t.Fatalf("条目应处于「封存中」，得到 %q（%s）", it.State, body)
	}
	if it.Percent <= 0 || it.Percent >= 100 {
		t.Fatalf("中间进度应在 (0,100) 之间，得到 %d（%s）", it.Percent, body)
	}
	if it.Done == "" || it.Total == "" {
		t.Fatalf("应报告已写/总字节：%s", body)
	}

	// 面板首屏（服务端渲染）也必须带上这份中间进度。
	html := getString(t, lib.URL()+"seal")
	if !strings.Contains(html, "封存中") {
		t.Fatalf("面板首屏应显示「封存中」：%s", head(html))
	}
	if !strings.Contains(html, "50%") {
		t.Fatalf("面板首屏应显示实时百分比 50%%，页面里没有：%s", head(html))
	}
}
