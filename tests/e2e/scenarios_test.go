package e2e

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cc14514/go-safemedia/internal/vault"
)

// ---------- HTTP 助手 ----------

type httpResult struct {
	Status  int
	Header  http.Header
	Body    []byte
	Elapsed time.Duration
}

func httpDo(t *testing.T, method, url, rangeHeader string) httpResult {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 %s 失败: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取 %s 响应失败: %v", url, err)
	}
	return httpResult{Status: resp.StatusCode, Header: resp.Header, Body: body, Elapsed: time.Since(start)}
}

// mediaPath 从单文件播放页解析媒体地址。
func mediaPath(t *testing.T, base string) string {
	t.Helper()
	res := httpDo(t, "GET", base, "")
	if res.Status != http.StatusOK {
		t.Fatalf("播放页状态码 = %d", res.Status)
	}
	m := regexp.MustCompile(`src="(/media/[0-9a-f]+)"`).FindStringSubmatch(string(res.Body))
	if m == nil {
		t.Fatalf("播放页没有媒体地址:\n%s", res.Body)
	}
	return m[1]
}

// portClosed 判断服务端口是否已关闭（进程退出后应成立）。
func portClosed(base string) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base)
		if err != nil {
			return true
		}
		resp.Body.Close()
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// ---------- 主链路 ----------

// TestE2E_MainFlow 覆盖 US1（账户创建）+ US3（封存）+ US4（播放/seek）+ US8（锁定）的黑盒全链路。
func TestE2E_MainFlow(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	keystore := filepath.Join(dir, "keystore.json")
	const pw = "e2e main passphrase 123"

	start := time.Now()
	addr, words := createAccount(t, bin, keystore, pw)
	info, err := os.Stat(keystore)
	if err != nil {
		t.Fatalf("keystore 未生成: %v", err)
	}
	perm := info.Mode().Perm()
	ok := len(words) == 12 && addr != "" && perm == 0o600
	record("US1 账户创建（account create，黑盒）",
		"12 词助记词 + 精确复述抽查 + keystore(0600) + 输出地址",
		fmt.Sprintf("助记词 %d 词；地址 %s；keystore %d B 权限 %#o；耗时 %.1fs", len(words), addr, info.Size(), perm, time.Since(start).Seconds()),
		"account create（含抽查复述）", ok)
	if !ok {
		t.Fatalf("账户创建不符合预期：words=%d addr=%q perm=%#o", len(words), addr, perm)
	}

	for _, s := range loadSamples(t) {
		s := s
		t.Run(s.Name, func(t *testing.T) { e2eSealPlayLock(t, bin, keystore, pw, dir, s) })
	}
}

// e2eSealPlayLock 对单个样本跑 封存 → 播放（全量 + 中段 Range）→ 锁定 的黑盒流程。
func e2eSealPlayLock(t *testing.T, bin, keystore, pw, dir string, s sampleKind) {
	src, plain := writeSample(t, dir, s)
	vaultPath := src + ".svid"

	// ---- 封存 ----
	sealStart := time.Now()
	out := sealVault(t, bin, keystore, pw, src, vaultPath)
	sealDur := time.Since(sealStart)

	srcAfter, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	srcUnchanged := bytes.Equal(srcAfter, plain)

	vInfo, err := os.Stat(vaultPath)
	if err != nil {
		t.Fatalf("密档未生成: %v", err)
	}
	// 不转码判据：密档体积 = 明文 + 156B 头部 + 每块 12B nonce + 16B tag
	chunk := int64(vault.DefaultChunkSize)
	var chunks int64
	if n := int64(len(plain)); n > 0 {
		chunks = (n + chunk - 1) / chunk
	}
	expected := int64(len(plain)) + int64(vault.HeaderSize) + chunks*int64(vault.ChunkNonceSize+vault.GCMTagSize)
	exactSize := vInfo.Size() == expected

	raw, err := os.ReadFile(vaultPath)
	if err != nil {
		t.Fatal(err)
	}
	head := plain[:min(64, len(plain))]
	noPlain := !bytes.Contains(raw, head)
	noName := !bytes.Contains(raw, []byte(s.Name))
	noFtyp := true
	if !s.Real {
		noFtyp = !bytes.Contains(raw, []byte("ftyp"))
	}
	sealOK := srcUnchanged && exactSize && noPlain && noName && noFtyp && vInfo.Mode().Perm() == 0o600
	record(fmt.Sprintf("US3 封存 %s（%s样本）", s.Name, realTag(s.Real)),
		"不转码（体积=明文+固定开销）；无明文/文件名残留；原文件不变；0600",
		fmt.Sprintf("明文 %d B → 密档 %d B（开销 %d B）；耗时 %.2fs；原文件不变=%v；无明文片段=%v 无文件名=%v",
			len(plain), vInfo.Size(), vInfo.Size()-int64(len(plain)), sealDur.Seconds(), srcUnchanged, noPlain, noName),
		summaryLine(out), sealOK)
	if !sealOK {
		t.Fatalf("封存结果不符合预期（期望 %d B，实得 %d B；原文件不变=%v 无明文=%v 无文件名=%v 无 ftyp=%v 权限=%#o）",
			expected, vInfo.Size(), srcUnchanged, noPlain, noName, noFtyp, vInfo.Mode().Perm())
	}

	// ---- 播放 ----
	p, base := startPlay(t, bin, nil, keystore, pw, vaultPath, "--idle", "5m")
	page := httpDo(t, "GET", base, "")
	pageOK := page.Status == http.StatusOK &&
		bytes.Contains(page.Body, []byte("<video")) &&
		bytes.Contains(page.Body, []byte("--player mpv"))
	media := mediaPath(t, base)
	mediaURL := base + strings.TrimPrefix(media, "/")

	full := httpDo(t, "GET", mediaURL, "")
	fullOK := full.Status == http.StatusOK &&
		bytes.Equal(full.Body, plain) &&
		full.Header.Get("Content-Type") != "" &&
		strings.Contains(full.Header.Get("Cache-Control"), "no-store")
	record(fmt.Sprintf("US4 播放 %s（127.0.0.1 环回）", s.Name),
		"播放页含 <video> 与 mpv 提示；全量解密流与原文逐字节一致；禁缓存",
		fmt.Sprintf("页面 %d B；流 %d B 一致=%v；Content-Type=%s；Cache-Control=%s",
			len(page.Body), len(full.Body), bytes.Equal(full.Body, plain),
			full.Header.Get("Content-Type"), full.Header.Get("Cache-Control")),
		"GET /media/<token>", pageOK && fullOK)
	if !pageOK || !fullOK {
		t.Fatalf("播放不符合预期：pageOK=%v fullOK=%v\n页面:\n%s", pageOK, fullOK, page.Body)
	}

	// ---- 拖进度条（中段 Range）----
	mid := int64(0)
	if len(plain) > 128*1024 {
		mid = int64(len(plain))/2 - 32768
	}
	end := min(mid+65535, int64(len(plain))-1)
	if end >= mid {
		rg := httpDo(t, "GET", mediaURL, fmt.Sprintf("bytes=%d-%d", mid, end))
		rangeOK := rg.Status == http.StatusPartialContent &&
			bytes.Equal(rg.Body, plain[mid:end+1]) &&
			rg.Elapsed < time.Second
		record(fmt.Sprintf("AC4.2 seek %s（中段 Range）", s.Name),
			"任意位置 Range → 206，内容正确，首字节 < 1s",
			fmt.Sprintf("bytes=%d-%d → %d 状态 %d，%d B，耗时 %d ms",
				mid, end, rg.Status, rg.Status, len(rg.Body), rg.Elapsed.Milliseconds()),
			"HTTP Range 单请求", rangeOK)
		if !rangeOK {
			t.Fatalf("seek 不符合预期：status=%d len=%d（期望 %d）耗时=%s",
				rg.Status, len(rg.Body), end-mid+1, rg.Elapsed)
		}
	}

	// ---- 明文残留检查（AC8.3）----
	spillOK, spillDetail := checkNoPlaintextSpill(t, dir, p.cmd.Process.Pid, []string{".mp4", ".mov", ".webm"})
	record(fmt.Sprintf("AC8.3 播放零明文残留 %s", s.Name),
		"解密只在内存；播放期间不新增磁盘文件、进程不打开明文视频",
		spillDetail, "目录快照对比 + lsof -p <pid>", spillOK)
	if !spillOK {
		t.Fatalf("AC8.3 失败: %s", spillDetail)
	}

	// ---- 锁定 ----
	lock := httpDo(t, "POST", base+"lock", "")
	var exitErr error
	select {
	case exitErr = <-p.done:
	case <-time.After(15 * time.Second):
		t.Fatalf("锁定后进程未退出\n输出:\n%s", p.output())
	}
	closed := portClosed(base)
	lockOK := lock.Status == http.StatusOK && exitErr == nil && closed
	record(fmt.Sprintf("AC8.1 锁定并退出 %s", s.Name),
		"POST /lock → 清零密钥、进程退出、端口关闭",
		fmt.Sprintf("lock 状态 %d；进程退出 err=%v；端口已关闭=%v", lock.Status, exitErr, closed),
		"POST /lock + 进程退出 + 端口探测", lockOK)
	if !lockOK {
		t.Fatalf("锁定不符合预期：status=%d exitErr=%v portClosed=%v", lock.Status, exitErr, closed)
	}
}

// ---------- 恢复 ----------

// TestE2E_RecoveryFromMnemonic 覆盖 US2：丢账户文件后用助记词重建，旧密档仍可播。
func TestE2E_RecoveryFromMnemonic(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	keystore := filepath.Join(dir, "keystore.json")
	const pw1 = "e2e original passphrase"
	const pw2 = "e2e rebuilt passphrase"

	addr, words := createAccount(t, bin, keystore, pw1)
	src, plain := writeSample(t, dir, sampleKind{Name: "recover.mp4", Data: syntheticMP4(200 * 1024)})
	vaultPath := src + ".svid"
	sealVault(t, bin, keystore, pw1, src, vaultPath)

	// 丢账户文件
	if err := os.Remove(keystore); err != nil {
		t.Fatal(err)
	}

	// 助记词重建
	p := startProc(t, bin, nil, "--account", keystore, "account", "recover", "--from-mnemonic")
	p.waitFor("请输入助记词", 20*time.Second)
	p.send(strings.Join(words, " "))
	p.waitFor("设置新的登录口令", 30*time.Second)
	p.send(pw2)
	p.waitFor("请再次输入以确认", 20*time.Second)
	p.send(pw2)
	if err := p.waitExit(90 * time.Second); err != nil {
		t.Fatalf("recover --from-mnemonic 失败: %v\n%s", err, p.output())
	}
	newAddr := addressRe.FindString(p.output())
	addrSame := newAddr == addr
	record("US2 助记词重建（黑盒 recover）",
		"用 12 词助记词重建账户，地址与原账户一致",
		fmt.Sprintf("原地址 %s → 新地址 %s（一致=%v）；keystore 重新生成=%v", addr, newAddr, addrSame, fileExists(keystore)),
		"account recover --from-mnemonic", addrSame && fileExists(keystore))
	if !addrSame {
		t.Fatalf("重建后地址不一致: %s != %s", newAddr, addr)
	}

	// 旧密档仍可播
	pp, base := startPlay(t, bin, nil, keystore, pw2, vaultPath, "--idle", "5m")
	media := mediaPath(t, base)
	full := httpDo(t, "GET", base+strings.TrimPrefix(media, "/"), "")
	playOK := full.Status == http.StatusOK && bytes.Equal(full.Body, plain)
	record("US2 重建后启封旧密档",
		"公钥未变 → 旧密档无需重新封存即可播放",
		fmt.Sprintf("旧密档 %d B 密文 → %d B 明文，逐字节一致=%v", fileSize(vaultPath), len(full.Body), bytes.Equal(full.Body, plain)),
		"play（重建账户 + 旧密档）", playOK)

	httpDo(t, "POST", base+"lock", "")
	if err := pp.waitExit(15 * time.Second); err != nil {
		t.Fatalf("锁定后退出失败: %v", err)
	}
	if !playOK {
		t.Fatal("重建后旧密档不可播")
	}
}

// TestE2E_MnemonicMismatchWritesNothing 覆盖 AC1.1：复述抽查失败时绝不写账户文件。
func TestE2E_MnemonicMismatchWritesNothing(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	keystore := filepath.Join(dir, "keystore.json")

	p := startProc(t, bin, nil, "--account", keystore, "account", "create")
	p.waitFor("设置登录口令", 20*time.Second)
	p.send("e2e mismatch passphrase")
	p.waitFor("请再次输入以确认", 20*time.Second)
	p.send("e2e mismatch passphrase")
	p.waitFor("请按序号复述", 30*time.Second)
	idxs := requestedIndexes(p.output())
	if len(idxs) == 0 {
		t.Fatalf("未解析到抽查序号:\n%s", p.output())
	}
	p.waitFor(fmt.Sprintf("第 %d 个词: ", idxs[0]), 15*time.Second)
	p.send("definitely-wrong-word")
	err := p.waitExit(60 * time.Second)

	noFile := !fileExists(keystore)
	ok := err != nil && noFile
	record("AC1.1 复述失败即中止（黑盒）",
		"抽查答错 → 报错退出，且不写出 keystore",
		fmt.Sprintf("退出 err=%v；keystore 存在=%v", err != nil, fileExists(keystore)),
		"account create（故意答错）", ok)
	if !ok {
		t.Fatalf("复述失败场景不符合预期：err=%v keystore存在=%v", err, fileExists(keystore))
	}
}

// ---------- 篡改 ----------

// TestE2E_TamperDetected 覆盖 AC4.4 / AC4.6：
// 头部损坏报「不是密档」；数据块被改时报完整性错误但服务不崩溃。
func TestE2E_TamperDetected(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	keystore := filepath.Join(dir, "keystore.json")
	const pw = "e2e tamper passphrase"

	createAccount(t, bin, keystore, pw)
	src, _ := writeSample(t, dir, sampleKind{Name: "tamper.mp4", Data: syntheticMP4(300 * 1024)})
	good := src + ".svid"
	sealVault(t, bin, keystore, pw, src, good)

	// (1) 头部损坏：play 必须直接报「不是密档」
	headBroken := src + ".head.svid"
	data, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	broken := append([]byte(nil), data...)
	broken[0] ^= 0xFF
	if err := os.WriteFile(headBroken, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	ph := startProc(t, bin, nil, "--account", keystore, "play", headBroken, "--no-browser")
	ph.waitFor("输入登录口令", 20*time.Second)
	ph.send(pw)
	headErr := ph.waitExit(60 * time.Second)
	headOK := headErr != nil && strings.Contains(ph.output(), "不是密档")
	record("AC4.6 头部损坏 = 不是密档（黑盒）",
		"magic 被改 → 明确报「不是密档文件或头部已损坏」，不启动服务",
		fmt.Sprintf("退出 err=%v；输出含「不是密档」=%v", headErr != nil, strings.Contains(ph.output(), "不是密档")),
		"play（篡改 magic）", headOK)

	// (2) 数据块损坏：媒体请求报错，但进程不崩溃、播放页仍可访问
	bodyBroken := src + ".body.svid"
	broken2 := append([]byte(nil), data...)
	target := int64(vault.HeaderSize) + int64(vault.ChunkNonceSize) + 128
	if target >= int64(len(broken2)) {
		t.Fatal("样本过小，无法构造数据块篡改")
	}
	broken2[target] ^= 0xFF
	if err := os.WriteFile(bodyBroken, broken2, 0o600); err != nil {
		t.Fatal(err)
	}

	p, base := startPlay(t, bin, nil, keystore, pw, bodyBroken, "--idle", "5m")
	media := mediaPath(t, base)
	req, err := http.NewRequest("GET", base+strings.TrimPrefix(media, "/"), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-65535")
	resp, reqErr := http.DefaultClient.Do(req)
	detected := false
	if reqErr != nil {
		detected = true // 流被中断，也算检测到
	} else {
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		detected = resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent
	}
	// 进程必须还活着：播放页仍返回 200
	page := httpDo(t, "GET", base, "")
	alive := page.Status == http.StatusOK

	record("AC4.4 数据块篡改（黑盒）",
		"被改块报完整性失败；进程不崩溃，播放页仍可访问",
		fmt.Sprintf("媒体请求被拒=%v（reqErr=%v，状态=%s）；播放页仍 200=%v",
			detected, reqErr, statusOf(resp), alive),
		"GET 中段 Range（篡改块）", detected && alive)

	httpDo(t, "POST", base+"lock", "")
	if err := p.waitExit(15 * time.Second); err != nil {
		t.Fatalf("锁定后退出失败: %v", err)
	}
	if !headOK || !detected || !alive {
		t.Fatalf("篡改检测不符合预期：头部=%v 数据块=%v 存活=%v", headOK, detected, alive)
	}
}

// ---------- 小工具 ----------

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func fileSize(p string) int64 {
	info, err := os.Stat(p)
	if err != nil {
		return -1
	}
	return info.Size()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// summaryLine 取 CLI 输出里最能说明结果的一行（优先含「容器=」的汇总行）。
func summaryLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "容器=") {
			return strings.TrimSpace(line)
		}
	}
	return firstLine(s)
}

func statusOf(resp *http.Response) string {
	if resp == nil {
		return "无响应"
	}
	return fmt.Sprint(resp.StatusCode)
}
