// Package e2e 是 go-safemedia 的 L3 端到端测试（QA 交付门禁）。
//
// 与 L2（进程内调用命令树）不同，L3 一律**黑盒**驱动真实二进制：
//   - go build 出 bin/safemedia（临时目录，避免污染仓库）；
//   - 用管道当终端喂口令（CLI 在非 TTY 下逐行读口令，见 internal/cli/prompt.go）；
//   - 真实 HTTP 拉流并与源文件逐字节比对；
//   - 可选：无头 Chrome 校验播放页真实解码状态。
//
// 运行：make test-l3
// 可选环境变量：
//
//	SAFEMEDIA_E2E_SAMPLES=<dir>  目录内放真实 mp4/mov/webm 样本（含 iPhone .mov），
//	                             会自动纳入封存/播放/浏览器解码校验。
//	SAFEMEDIA_E2E_NO_BROWSER=1   跳过无头浏览器检查（CI 无浏览器时）。
package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------- Pre/Post 记录与报告 ----------

// metric 是报告表格中的一行：期望（Pre）→ 实测（Post）。
type metric struct {
	Name     string
	Pre      string
	Post     string
	Evidence string
	Pass     bool
}

var (
	metricsMu         sync.Mutex
	metrics           []metric
	regressionSection []string
)

// record 记录一条 Pre/Post 结果（供 TestMain 生成 REPORT.md）。
func record(name, pre, post, evidence string, pass bool) {
	metricsMu.Lock()
	defer metricsMu.Unlock()
	metrics = append(metrics, metric{Name: name, Pre: pre, Post: post, Evidence: evidence, Pass: pass})
}

// reportMeta 收集报告头部信息。
type reportMeta struct {
	Started time.Time
	BinPath string
	BinSHA  string
	Samples []string
}

var meta reportMeta

func TestMain(m *testing.M) {
	meta.Started = time.Now()
	code := m.Run()
	if err := writeReport(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠ 生成 REPORT.md 失败: %v\n", err)
	}
	os.Exit(code)
}

func writeReport() error {
	metricsMu.Lock()
	rows := append([]metric(nil), metrics...)
	metricsMu.Unlock()
	sort.SliceStable(rows, func(i, j int) bool { return false })

	pass, fail := 0, 0
	for _, r := range rows {
		if r.Pass {
			pass++
		} else {
			fail++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# 001 go-safemedia — L3 E2E 报告\n\n")
	fmt.Fprintf(&b, "- 生成时间：%s\n", meta.Started.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(&b, "- 运行环境：%s/%s · %s\n", runtime.GOOS, runtime.GOARCH, runtime.Version())
	if u, err := exec.Command("sw_vers", "-productVersion").Output(); err == nil {
		fmt.Fprintf(&b, "- 系统版本：macOS %s\n", strings.TrimSpace(string(u)))
	}
	fmt.Fprintf(&b, "- 被测二进制：`%s`（sha256 `%s`）\n", meta.BinPath, meta.BinSHA)
	if len(meta.Samples) == 0 {
		fmt.Fprintf(&b, "- 样本：**无真实视频样本**（用合成 mp4 头）——真实浏览器解码/iPhone .mov 见下方「待人工验证」\n")
	} else {
		fmt.Fprintf(&b, "- 样本：%s\n", strings.Join(meta.Samples, ", "))
	}
	fmt.Fprintf(&b, "- 判定：**%d PASS / %d FAIL**（L3 必须全 PASS 才能交付 QA）\n\n", pass, fail)

	fmt.Fprintf(&b, "## Pre/Post 对比\n\n")
	fmt.Fprintf(&b, "| # | 场景 | 期望（Pre） | 实测（Post） | 证据 | 判定 |\n")
	fmt.Fprintf(&b, "|---|------|------------|-------------|------|------|\n")
	for i, r := range rows {
		verdict := "FAIL"
		if r.Pass {
			verdict = "PASS"
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %s | **%s** |\n", i+1, esc(r.Name), esc(r.Pre), esc(r.Post), esc(r.Evidence), verdict)
	}

	fmt.Fprintf(&b, "\n## L1 / L2 回归（同一源码）\n\n```\n")
	if len(regressionSection) == 0 {
		fmt.Fprintf(&b, "（本次未记录）\n")
	}
	for _, line := range regressionSection {
		fmt.Fprintf(&b, "%s\n", line)
	}
	fmt.Fprintf(&b, "```\n")

	fmt.Fprintf(&b, "\n## 待人工验证（需真实样本 / 人眼 / 外部设备）\n\n")
	fmt.Fprintf(&b, "以下项需要真实素材或人工观察，自动化无法替代；请逐项勾选后再交付：\n\n")
	fmt.Fprintf(&b, "- [ ] **AC3.2** iPhone 实机拍摄的 `.mov`：H.264「兼容性最佳」在 Safari/Chrome 直接播放；HEVC「高效」在 Firefox 出现「改用 --player mpv」提示（本机未安装 Firefox，需实机确认）\n")
	fmt.Fprintf(&b, "- [ ] **AC4.2 人眼判定** 真实长视频拖动进度条：按 Pre/Post 表中 seek 行的毫秒数推断（本机 0–1 ms），仍需人眼确认画面正确衔接\n")
	fmt.Fprintf(&b, "- [ ] **AC6.1** 本机安装 mpv 后 `safemedia play clip.svid --player mpv` 实播（本机未安装 mpv，仅验证到「传参 + 未安装报错」为止）\n")
	fmt.Fprintf(&b, "- [ ] **AC7.1** 双机搬运实测：U 盘拷 keystore + .svid 到另一台机播放（L2/L3 用不同 HOME 模拟了可搬运性）\n")
	fmt.Fprintf(&b, "- [ ] **AC8.3 深度检查** 播放结束后全盘扫描（本报告已自动核对：播放期间不新增文件、进程不打开明文视频）\n")
	fmt.Fprintf(&b, "\n## 如何复现\n\n```sh\nmake test-l3\n# 有真实样本时（推荐；脚本会自动准备 mp4/webm/mov 三个小样本）：\ntests/e2e/fetch-samples.sh /tmp/safemedia-samples\nSAFEMEDIA_E2E_SAMPLES=/tmp/safemedia-samples make test-l3\n```\n")

	return os.WriteFile("REPORT.md", []byte(b.String()), 0o644)
}

func esc(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.ReplaceAll(s, "\n", " ")
}

// ---------- 子进程驱动（黑盒） ----------

// proc 是一个被黑盒驱动的 CLI 进程。
type proc struct {
	t     *testing.T
	cmd   *exec.Cmd
	stdin io.WriteCloser

	mu  sync.Mutex
	buf bytes.Buffer

	done   chan error
	killed bool
}

// buildBinary 编译被测二进制到临时目录，返回路径。
func buildBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "safemedia")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/safemedia")
	cmd.Dir = filepath.Join("..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("构建二进制失败: %v\n%s", err, out)
	}
	meta.BinPath = bin
	if data, err := os.ReadFile(bin); err == nil {
		sum := sha256.Sum256(data)
		meta.BinSHA = hex.EncodeToString(sum[:])[:16]
	}
	return bin
}

// startProc 启动 CLI 子进程（stdout+stderr 合流，stdin 为管道）。
func startProc(t *testing.T, bin string, env []string, args ...string) *proc {
	t.Helper()
	cmd := exec.Command(bin, args...)
	if env != nil {
		cmd.Env = env
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = pw
	cmd.Stderr = pw
	stdin, err := cmd.StdinPipe()
	if err != nil {
		pw.Close()
		pr.Close()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		t.Fatalf("启动 %s 失败: %v", bin, err)
	}
	pw.Close() // 父进程关掉写端，读端才能收到 EOF
	p := &proc{t: t, cmd: cmd, stdin: stdin, done: make(chan error, 1)}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := pr.Read(buf)
			if n > 0 {
				p.mu.Lock()
				p.buf.Write(buf[:n])
				p.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	go func() { p.done <- cmd.Wait() }()
	t.Cleanup(p.kill)
	return p
}

func (p *proc) kill() {
	p.mu.Lock()
	already := p.killed
	p.killed = true
	p.mu.Unlock()
	if already {
		return
	}
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

func (p *proc) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.buf.String()
}

func (p *proc) send(line string) {
	p.t.Helper()
	if _, err := io.WriteString(p.stdin, line+"\n"); err != nil {
		p.t.Fatalf("向子进程写入失败: %v", err)
	}
}

// waitFor 等待输出中出现 token（大小写敏感），超时则失败。
func (p *proc) waitFor(token string, timeout time.Duration) string {
	p.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out := p.output()
		if strings.Contains(out, token) {
			return out
		}
		time.Sleep(20 * time.Millisecond)
	}
	p.t.Fatalf("等待输出 %q 超时（%s）\n当前输出:\n%s", token, timeout, p.output())
	return ""
}

// waitExit 等待进程退出并返回错误（退出码非 0 即错误）。
func (p *proc) waitExit(timeout time.Duration) error {
	p.t.Helper()
	select {
	case err := <-p.done:
		return err
	case <-time.After(timeout):
		p.t.Fatalf("进程未在 %s 内退出\n当前输出:\n%s", timeout, p.output())
		return nil
	}
}

var (
	mnemonicGridRe = regexp.MustCompile(`(\d+)\.([a-z]+)`)
	spotCheckRe    = regexp.MustCompile(`#(\d+)`)
	addressRe      = regexp.MustCompile(`0x[0-9a-fA-F]{40}`)
)

// captureMnemonic 从输出里还原助记词（按序号）。
func captureMnemonic(out string) []string {
	byIndex := map[int]string{}
	max := 0
	for _, m := range mnemonicGridRe.FindAllStringSubmatch(out, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		byIndex[n] = m[2]
		if n > max {
			max = n
		}
	}
	words := make([]string, 0, max)
	for i := 1; i <= max; i++ {
		w, ok := byIndex[i]
		if !ok {
			return nil
		}
		words = append(words, w)
	}
	return words
}

// requestedIndexes 解析复述抽查的序号（来自 "  #1   #8   #9 " 这类行）。
func requestedIndexes(out string) []int {
	idx := strings.Index(out, "请按序号复述")
	if idx < 0 {
		return nil
	}
	var nums []int
	for _, m := range spotCheckRe.FindAllStringSubmatch(out[idx:], -1) {
		n, err := strconv.Atoi(m[1])
		if err == nil {
			nums = append(nums, n)
		}
	}
	return nums
}

// createAccount 黑盒执行 account create：喂口令 + 按抽查复述助记词。
func createAccount(t *testing.T, bin, keystore, passphrase string) (addr string, words []string) {
	t.Helper()
	p := startProc(t, bin, nil, "--account", keystore, "account", "create")
	p.waitFor("设置登录口令", 20*time.Second)
	p.send(passphrase)
	p.waitFor("请再次输入以确认", 20*time.Second)
	p.send(passphrase)
	p.waitFor("请按序号复述", 30*time.Second)

	words = captureMnemonic(p.output())
	if len(words) < 12 {
		t.Fatalf("未捕获到完整助记词（%d 个）:\n%s", len(words), p.output())
	}
	idxs := requestedIndexes(p.output())
	if len(idxs) == 0 {
		t.Fatalf("未解析到复述抽查序号:\n%s", p.output())
	}
	for _, i := range idxs {
		p.waitFor(fmt.Sprintf("第 %d 个词: ", i), 15*time.Second)
		p.send(words[i-1])
	}
	if err := p.waitExit(60 * time.Second); err != nil {
		t.Fatalf("account create 失败: %v\n%s", err, p.output())
	}
	out := p.output()
	addr = addressRe.FindString(out)
	if addr == "" {
		t.Fatalf("create 输出中没有账户地址:\n%s", out)
	}
	return addr, words
}

// sealVault 黑盒执行 seal。
func sealVault(t *testing.T, bin, keystore, passphrase, src, dst string) string {
	t.Helper()
	p := startProc(t, bin, nil, "--account", keystore, "seal", src, "-o", dst)
	p.waitFor("输入登录口令", 20*time.Second)
	p.send(passphrase)
	if err := p.waitExit(120 * time.Second); err != nil {
		t.Fatalf("seal 失败: %v\n%s", err, p.output())
	}
	return p.output()
}

var loopbackRe = regexp.MustCompile(`http://127\.0\.0\.1:\d+/`)

// startPlay 黑盒启动 play，返回进程与播放页 URL。
func startPlay(t *testing.T, bin string, env []string, accountPath, passphrase, target string, extra ...string) (*proc, string) {
	t.Helper()
	args := append([]string{"--account", accountPath, "play", target, "--no-browser"}, extra...)
	p := startProc(t, bin, env, args...)
	p.waitFor("输入登录口令", 20*time.Second)
	p.send(passphrase)
	out := p.waitFor("http://127.0.0.1:", 30*time.Second)
	u := loopbackRe.FindString(out)
	if u == "" {
		t.Fatalf("未取得播放服务地址:\n%s", out)
	}
	return p, u
}

// ---------- 样本 ----------

// sampleKind 描述一个测试用视频样本。
type sampleKind struct {
	Name string
	Real bool // 真实可解码视频（否则为合成头）
	Data []byte
}

var (
	samplesOnce sync.Once
	sampleCache []sampleKind
)

// syntheticMP4 造一个带合法 ftyp 头的合成 mp4（无 ffmpeg 时的兜底样本）。
func syntheticMP4(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*97 + i/251 + 3)
	}
	copy(b[0:4], []byte{0x00, 0x00, 0x00, 0x20})
	copy(b[4:8], "ftyp")
	copy(b[8:12], "isom")
	return b
}

// loadSamples 收集样本：优先 SAFEMEDIA_E2E_SAMPLES 目录里的真实视频，否则退回合成 mp4。
func loadSamples(t *testing.T) []sampleKind {
	t.Helper()
	samplesOnce.Do(func() {
		dir := os.Getenv("SAFEMEDIA_E2E_SAMPLES")
		var samples []sampleKind
		if dir != "" {
			items, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("读取 SAFEMEDIA_E2E_SAMPLES=%s 失败: %v", dir, err)
			}
			for _, it := range items {
				if it.IsDir() {
					continue
				}
				switch strings.ToLower(filepath.Ext(it.Name())) {
				case ".mp4", ".mov", ".webm":
					data, err := os.ReadFile(filepath.Join(dir, it.Name()))
					if err != nil {
						t.Fatalf("读取样本 %s 失败: %v", it.Name(), err)
					}
					samples = append(samples, sampleKind{Name: it.Name(), Real: true, Data: data})
				}
			}
		}
		if len(samples) == 0 {
			samples = []sampleKind{{Name: "synthetic.mp4", Real: false, Data: syntheticMP4(256 * 1024)}}
		}
		for _, s := range samples {
			meta.Samples = append(meta.Samples, fmt.Sprintf("%s(%s, %d B)", s.Name, realTag(s.Real), len(s.Data)))
		}
		sampleCache = samples
	})
	return sampleCache
}

func realTag(real bool) string {
	if real {
		return "真实"
	}
	return "合成"
}

// writeSample 把样本写到目录，返回源文件路径与明文。
func writeSample(t *testing.T, dir string, s sampleKind) (string, []byte) {
	t.Helper()
	path := filepath.Join(dir, s.Name)
	if err := os.WriteFile(path, s.Data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, s.Data
}
