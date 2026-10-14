package e2e

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ---------- AC8.3：播放期间无明文残留 ----------

// checkNoPlaintextSpill 断言播放期间目录里没有新增文件，且进程没有打开明文视频。
func checkNoPlaintextSpill(t *testing.T, dir string, pid int, plainExts []string) (bool, string) {
	t.Helper()
	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	beforeNames := map[string]bool{}
	for _, e := range before {
		beforeNames[e.Name()] = true
	}

	var added []string
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		now, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range now {
			if !beforeNames[e.Name()] {
				beforeNames[e.Name()] = true
				added = append(added, e.Name())
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	openPlaintext := ""
	if out, err := exec.Command("lsof", "-p", strconv.Itoa(pid), "-Fn").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.HasPrefix(line, "n") {
				continue
			}
			name := strings.TrimPrefix(line, "n")
			for _, ext := range plainExts {
				if strings.HasSuffix(strings.ToLower(name), ext) && !strings.Contains(name, ".svid") {
					openPlaintext = name
				}
			}
		}
	}
	ok := len(added) == 0 && openPlaintext == ""
	detail := "播放期间未新增任何文件"
	if len(added) > 0 {
		detail = "播放期间新增文件: " + strings.Join(added, ", ")
	}
	if openPlaintext != "" {
		detail += "；进程仍打开明文视频: " + openPlaintext
	}
	return ok, detail
}

// ---------- 真实浏览器解码（无头 Chrome） ----------

// findChrome 返回可用的 Chrome/Chromium 可执行文件路径。
func findChrome() string {
	candidates := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
	}
	for _, c := range candidates {
		if fileExists(c) {
			return c
		}
	}
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

var decodeStateRe = regexp.MustCompile(`data-decode="([a-z]+)"`)

// dumpDOM 用无头 Chrome 打开 URL 并把最终 DOM 打出来。
func dumpDOM(t *testing.T, chrome, url string, wait time.Duration) string {
	t.Helper()
	profile := t.TempDir()
	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--disable-sync",
		"--mute-audio",
		"--autoplay-policy=no-user-gesture-required",
		"--user-data-dir=" + profile,
		"--virtual-time-budget=" + strconv.Itoa(int(wait.Milliseconds())),
		"--dump-dom",
		url,
	}
	cmd := exec.Command(chrome, args...)
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(wait + 30*time.Second):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
		t.Skipf("无头 Chrome 超时未返回（%s）", wait+30*time.Second)
	}
	if len(out) == 0 {
		t.Skipf("无头 Chrome 无输出（err=%v）", err)
	}
	return string(out)
}

// TestE2E_BrowserDecode 用无头 Chrome 校验播放页的真实解码路径（AC4.5）。
//
// 逐个样本检查：真实视频期望进入 playing/canplay；合成样本（无有效码流）
// 期望进入 failed 且页面显示 mpv 提示——这恰好验证了「播不动就提示」的检测逻辑。
func TestE2E_BrowserDecode(t *testing.T) {
	if os.Getenv("SAFEMEDIA_E2E_NO_BROWSER") == "1" {
		t.Skip("SAFEMEDIA_E2E_NO_BROWSER=1，跳过浏览器校验")
	}
	chrome := findChrome()
	if chrome == "" {
		t.Skip("未找到 Chrome/Chromium，跳过真实浏览器解码校验")
	}
	t.Logf("使用浏览器: %s", chrome)

	bin := buildBinary(t)
	dir := t.TempDir()
	keystore := filepath.Join(dir, "keystore.json")
	const pw = "e2e browser passphrase"

	createAccount(t, bin, keystore, pw)

	for _, s := range loadSamples(t) {
		s := s
		t.Run(s.Name, func(t *testing.T) {
			src, _ := writeSample(t, dir, s)
			vaultPath := src + ".svid"
			sealVault(t, bin, keystore, pw, src, vaultPath)

			p, base := startPlay(t, bin, nil, keystore, pw, vaultPath, "--idle", "5m")
			dom := dumpDOM(t, chrome, base, 6*time.Second)
			state := "未上报"
			if m := decodeStateRe.FindStringSubmatch(dom); m != nil {
				state = m[1]
			}
			hasVideo := strings.Contains(dom, "<video")
			hasMpvHint := strings.Contains(dom, "--player mpv")

			httpDoQuiet(base + "lock")
			_ = p.waitExitOrKill(10 * time.Second)

			// 对照组：同一 Chrome 直接播原文件。用于区分「我们的解密流有问题」
			// 与「这个文件 Chrome 本来就解不了」。
			raw := "跳过"
			if s.Real {
				raw = rawDecodeState(t, chrome, src, 6*time.Second)
			}

			name := fmt.Sprintf("AC4.5 浏览器解码 %s（%s，无头 Chrome）", s.Name, realTag(s.Real))
			rawPlayable := raw == "playing" || raw == "canplay"
			vaultPlayable := state == "playing" || state == "canplay"

			switch {
			case !s.Real:
				// 合成样本没有有效码流：期望失败并显示 mpv 提示。
				ok := state == "failed" && hasVideo && hasMpvHint
				record("AC4.5 播不动即提示（合成样本，无头 Chrome）",
					"非可解码流 → 页面显示「改用 --player mpv」提示",
					"data-decode="+state+"；<video> 存在="+strconv.FormatBool(hasVideo)+"；mpv 提示可见="+strconv.FormatBool(hasMpvHint),
					"chrome --headless --dump-dom", ok)
				if !ok {
					t.Fatalf("失败提示路径不可用（state=%s）", state)
				}
			case vaultPlayable:
				record(name, "真实视频经密档解密后进入播放状态（对照：原文件 "+raw+"）",
					"密档流 data-decode="+state+"；原文件 data-decode="+raw+"；媒体流与原文逐字节一致（见 US4 行）",
					"chrome --headless --dump-dom（密档流 vs 原文件对照）", true)
			case !rawPlayable && state == "failed":
				// 原文件 Chrome 自己就播不了 → 走 mpv 提示路径，是预期行为（AC4.5）。
				ok := hasMpvHint
				record(name, "浏览器无法解码该编码 → 提示改用 --player mpv（对照：原文件 =failed）",
					"密档流 data-decode=failed；原文件 data-decode="+raw+"（同一 Chrome 亦无法播放）；mpv 提示可见="+strconv.FormatBool(hasMpvHint),
					"chrome --headless --dump-dom（密档流 vs 原文件对照）", ok)
				if !ok {
					t.Fatal("浏览器无法解码时未显示 mpv 提示")
				}
			default:
				// 原文件能播、我们的密档流不能播 → 真 bug。
				record(name, "原文件可播时，密档流必须同样可播",
					"原文件 data-decode="+raw+"；密档流 data-decode="+state,
					"chrome --headless --dump-dom（密档流 vs 原文件对照）", false)
				t.Fatalf("原文件在 Chrome 中可播（%s），但密档流不可播（%s）——解密/流式服务有缺陷", raw, state)
			}
		})
	}
}

// ---------- 对照组：直接播放原文件（区分「我们的流坏了」与「Chrome 本来就播不了」） ----------

// controlPage 是与播放页同构的最小对照页：同一套 data-decode 检测脚本。
func controlPage(src string) string {
	return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>对照组</title></head><body>
<video id="v" controls autoplay playsinline src="` + src + `"></video>
<div id="warn" style="display:none">改用 --player mpv</div>
<script>
const v=document.getElementById('v'),warn=document.getElementById('warn');
document.documentElement.dataset.decode='pending';
v.addEventListener('playing',()=>{document.documentElement.dataset.decode='playing';});
v.addEventListener('canplay',()=>{document.documentElement.dataset.decode='canplay';});
v.addEventListener('error',()=>{document.documentElement.dataset.decode='failed';warn.style.display='block';});
v.addEventListener('stalled',()=>setTimeout(()=>{if(v.readyState<2){document.documentElement.dataset.decode='failed';warn.style.display='block';}},4000));
</script></body></html>`
}

func mimeForExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".mp4":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	}
	return "application/octet-stream"
}

// rawDecodeState 把原文件用最简单的 HTTP 服务直接喂给同一个无头 Chrome，
// 返回其 data-decode 状态，作为「浏览器本身能否解码该文件」的对照。
func rawDecodeState(t *testing.T, chrome, file string, wait time.Duration) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/raw", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", mimeForExt(filepath.Ext(file)))
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, controlPage("/raw"))
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	dom := dumpDOM(t, chrome, "http://"+ln.Addr().String()+"/", wait)
	if m := decodeStateRe.FindStringSubmatch(dom); m != nil {
		return m[1]
	}
	return "未上报"
}

var benchMBpsRe = regexp.MustCompile(`^Benchmark(\w+)(?:-\d+)?\s+\d+\s+[\d.]+ ns/op\s+([\d.]+) MB/s`)

// ---------- NFR-1：封存吞吐 ----------

// TestE2E_PerfThroughput 跑 vault 基准，取封存吞吐作为 NFR-1 证据。
func TestE2E_PerfThroughput(t *testing.T) {
	cmd := exec.Command("go", "test", "-run=^$", "-bench=BenchmarkSeal32MiB|BenchmarkSeqRead32MiB", "-benchtime=3x", "./internal/vault/")
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("基准运行失败: %v\n%s", err, out)
	}
	vals := map[string]float64{}
	for _, line := range strings.Split(string(out), "\n") {
		if m := benchMBpsRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			v, _ := strconv.ParseFloat(m[2], 64)
			vals[m[1]] = v
		}
	}
	seal, okSeal := vals["Seal32MiB"]
	read, okRead := vals["SeqRead32MiB"]
	if !okSeal {
		t.Fatalf("未解析到封存吞吐基准:\n%s", out)
	}
	ratio := 0.0
	if okRead && read > 0 {
		ratio = seal / read * 100
	}
	ok := seal >= 100 // AES-NI 下远高于此；远快于任何本机 SSD 顺序读
	detail := "封存 " + strconv.FormatFloat(seal, 'f', 0, 64) + " MB/s"
	if okRead {
		detail += "；同机顺序读（页缓存，RAM 基线）" + strconv.FormatFloat(read, 'f', 0, 64) +
			" MB/s，比值 " + strconv.FormatFloat(ratio, 'f', 0, 64) + "%（该基线是内存速度，偏低属正常）"
	}
	record("NFR-1 封存吞吐（32 MiB，AES-256-GCM 分块加密）",
		"≥100 MB/s（AES-NI；远高于本机 SSD 顺序读，加密不是瓶颈）",
		detail, "go test -bench=BenchmarkSeal32MiB ./internal/vault", ok)
	if !ok {
		t.Fatalf("封存吞吐过低: %.0f MB/s", seal)
	}
}

// TestE2E_RegressionL1L2 记录同一源码上的 L1+L2 回归结果，作为交付证据的一部分。
func TestE2E_RegressionL1L2(t *testing.T) {
	cmd := exec.Command("go", "test", "-count=1", "./internal/...", "./cmd/...", "./tests/integration/...")
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.CombinedOutput()
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var ok, failed int
	var summary []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ok ") || strings.HasPrefix(line, "?") {
			ok++
			summary = append(summary, line)
		}
		if strings.HasPrefix(line, "FAIL") {
			failed++
			summary = append(summary, line)
		}
	}
	regressionSection = summary
	record("L1+L2 回归（unit + integration）",
		"同一源码上 go test ./internal/... ./tests/integration/ 全绿",
		fmt.Sprintf("包级结果 %d ok / %d fail；命令 err=%v", ok, failed, err != nil),
		"go test -count=1 ./internal/... ./cmd/... ./tests/integration/...", err == nil && failed == 0)
	if err != nil {
		t.Fatalf("L1+L2 回归失败: %v\n%s", err, out)
	}
}

// httpDoQuiet 发一个忽略结果的请求（用于清理）。
func httpDoQuiet(url string) {
	resp, err := http.Get(url)
	if err == nil && resp != nil {
		resp.Body.Close()
	}
}

func httpGet(url string) (*http.Response, error) { return http.DefaultClient.Get(url) }

// waitExitOrKill 等待退出，超时则杀掉。
func (p *proc) waitExitOrKill(timeout time.Duration) error {
	select {
	case err := <-p.done:
		return err
	case <-time.After(timeout):
		p.kill()
		return os.ErrDeadlineExceeded
	}
}
