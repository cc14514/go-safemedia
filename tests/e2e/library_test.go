package e2e

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// watchLinkRe 抓播放页/索引页里的条目链接与标题（标题 = 库内相对路径）。
var watchLinkRe = regexp.MustCompile(`href="(/watch/[0-9a-f]+)" title="([^"]*)"`)

// libMediaRe 抓侧栏模式下播放页的媒体地址。
var libMediaRe = regexp.MustCompile(`src="(/media/[0-9a-f]+/[0-9a-f]+)"`)

// TestE2E_LibraryTreeSidebar 覆盖 AC5.1–AC5.5：
// 目录（递归含子目录）播放、目录树索引页、播放页左侧常驻目录树、从侧栏切换片源。
func TestE2E_LibraryTreeSidebar(t *testing.T) {
	samples := loadSamples(t)
	if len(samples) == 0 {
		t.Skip("没有可用样本")
	}
	bin := buildBinary(t)
	dir := t.TempDir()
	keystore := filepath.Join(dir, "keystore.json")
	const pw = "e2e library passphrase 123"
	createAccount(t, bin, keystore, pw)

	// 造两级密档库：第 1 个样本放库根，其余放 season1/ 子目录。
	libRoot := filepath.Join(dir, "library")
	rels := make([]string, 0, len(samples))
	plain := map[string][]byte{}
	srcByRel := map[string]string{}
	isReal := map[string]bool{}
	for i, s := range samples {
		sub := ""
		if i > 0 {
			sub = "season1"
		}
		srcDir := filepath.Join(dir, "src", sub)
		if err := os.MkdirAll(srcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		src, data := writeSample(t, srcDir, s)
		srcRel := filepath.Join(sub, s.Name)
		rel := srcRel + ".svid" // 库内相对路径（索引页 title 即它）
		rels = append(rels, rel)
		plain[rel] = data
		srcByRel[rel] = src
		isReal[rel] = s.Real
		dst := filepath.Join(libRoot, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		sealVault(t, bin, keystore, pw, src, dst)
	}

	p, base := startPlay(t, bin, nil, keystore, pw, libRoot, "--idle", "5m")

	// ---- AC5.4 索引页：递归列出子目录条目，并按目录树分组 ----
	index := httpDo(t, "GET", base, "")
	byRel := map[string]string{}
	for _, m := range watchLinkRe.FindAllStringSubmatch(string(index.Body), -1) {
		byRel[m[2]] = m[1]
	}
	grouped := bytes.Contains(index.Body, []byte("<details")) && bytes.Contains(index.Body, []byte("season1"))
	treeOK := index.Status == http.StatusOK
	for _, rel := range rels {
		if byRel[rel] == "" {
			treeOK = false
			t.Errorf("索引页缺少条目 %q", rel)
		}
	}
	nested := len(samples) > 1
	if nested && !grouped {
		treeOK = false
	}
	record("AC5.4 目录树索引（递归含子目录）",
		"索引页列出子目录中的密档，并按目录树（<details>）分组",
		fmt.Sprintf("库内 %d 个密档（子目录 %v）；解析到条目链接 %d 个；目录分组=%v",
			len(rels), nested, len(byRel), grouped),
		"GET / 索引页 + 链接解析", treeOK)
	if !treeOK {
		t.Fatalf("目录树索引不符合预期\n页面:\n%s", index.Body)
	}

	// ---- AC5.5 播放页左侧常驻目录树 ----
	target := rels[len(rels)-1] // 最深（season1 内）的条目
	watch := byRel[target]
	if watch == "" {
		t.Fatalf("目标条目 %q 无链接", target)
	}
	page := httpDo(t, "GET", base+strings.TrimPrefix(watch, "/"), "")
	hasAside := bytes.Contains(page.Body, []byte("<aside"))
	hasActive := bytes.Contains(page.Body, []byte("file active"))
	allSwitchable := true
	for _, rel := range rels {
		if !bytes.Contains(page.Body, []byte(byRel[rel])) {
			allSwitchable = false
			t.Errorf("侧栏缺少条目 %q 的链接（无法切换片源）", rel)
		}
	}
	pageOK := page.Status == http.StatusOK && hasAside && hasActive && allSwitchable
	record("AC5.5 播放页左侧常驻目录树",
		"播放页左侧显示整库目录树、当前条目高亮，其它条目可直接点击切换",
		fmt.Sprintf("页面 %d B；<aside> 存在=%v；当前条目高亮=%v；%d 个条目链接齐全=%v",
			len(page.Body), hasAside, hasActive, len(rels), allSwitchable),
		"GET /watch/<token>", pageOK)
	if !pageOK {
		t.Fatalf("播放页目录树不符合预期（aside=%v active=%v switchable=%v）", hasAside, hasActive, allSwitchable)
	}

	// 从侧栏选中的条目：整段取流与原文逐字节一致
	media := libMediaRe.FindStringSubmatch(string(page.Body))
	if media == nil {
		t.Fatalf("播放页没有媒体地址:\n%s", page.Body)
	}
	got := httpDo(t, "GET", base+strings.TrimPrefix(media[1], "/"), "")
	streamOK := got.Status == http.StatusOK && bytes.Equal(got.Body, plain[target])
	if !streamOK {
		t.Fatalf("侧栏选中条目的流不一致：状态 %d，%d B（期望 %d B）",
			got.Status, len(got.Body), len(plain[target]))
	}

	// 切换片源：点另一个条目的页面链接，同样能取到正确的流
	other := rels[0]
	opage := httpDo(t, "GET", base+strings.TrimPrefix(byRel[other], "/"), "")
	omedia := libMediaRe.FindStringSubmatch(string(opage.Body))
	switchOK := omedia != nil
	if switchOK {
		ogot := httpDo(t, "GET", base+strings.TrimPrefix(omedia[1], "/"), "")
		switchOK = ogot.Status == http.StatusOK && bytes.Equal(ogot.Body, plain[other])
	}
	record("AC5.5 侧栏切换片源",
		"两个不同子目录的条目各自取流，内容与原文一致",
		fmt.Sprintf("选中 %s → %d B 一致=%v；切换 %s → 一致=%v",
			target, len(got.Body), streamOK, other, switchOK),
		"GET /watch + /media（两条条目）", switchOK)
	if !switchOK {
		t.Fatalf("切换片源后取流不一致（other=%s）", other)
	}

	// ---- 真实浏览器解码（可选，需 Chrome 且未禁用） ----
	chrome := findChrome()
	if chrome == "" || os.Getenv("SAFEMEDIA_E2E_NO_BROWSER") == "1" {
		t.Log("跳过浏览器校验（无 Chrome 或 SAFEMEDIA_E2E_NO_BROWSER=1）")
	} else {
		watchURL := base + strings.TrimPrefix(watch, "/")
		dom := dumpDOM(t, chrome, watchURL, 6*time.Second)
		state := "未上报"
		if m := decodeStateRe.FindStringSubmatch(dom); m != nil {
			state = m[1]
		}
		hasMpvHint := strings.Contains(dom, "--player mpv")
		playable := state == "playing" || state == "canplay"
		raw := "跳过"
		if isReal[target] {
			raw = rawDecodeState(t, chrome, srcByRel[target], 6*time.Second)
		}
		rawPlayable := raw == "playing" || raw == "canplay"

		ok := playable
		if !isReal[target] {
			// 合成样本无有效码流：期望失败并提示改用 mpv。
			ok = state == "failed" && hasMpvHint
		} else if rawPlayable && !playable {
			ok = false
		}
		record(fmt.Sprintf("AC5.5 侧栏选中条目真实解码（%s，无头 Chrome）", realTag(isReal[target])),
			"经目录树选中的条目在浏览器里真实解码（合成样本则显示 mpv 提示）",
			"data-decode="+state+"（对照原文件="+raw+"）；<video> 存在="+strconv.FormatBool(strings.Contains(dom, "<video"))+
				"；mpv 提示="+strconv.FormatBool(hasMpvHint),
			"chrome --headless --dump-dom（密档流 vs 原文件对照）", ok)
		if !ok {
			t.Fatalf("侧栏选中条目在浏览器中不可播（state=%s，原文件=%s）", state, raw)
		}
	}

	// ---- 锁定（侧栏模式同样生效） ----
	lockStart := time.Now()
	lock := httpDo(t, "POST", base+"lock", "")
	exitErr := p.waitExitOrKill(15 * time.Second)
	closed := portClosed(base)
	lockOK := lock.Status == http.StatusOK && exitErr == nil && closed
	record("AC8.1 目录树会话锁定",
		"侧栏模式 POST /lock → 清零密钥、进程退出、端口关闭",
		fmt.Sprintf("lock 状态 %d；进程退出 err=%v；端口已关闭=%v；耗时 %.2fs",
			lock.Status, exitErr, closed, time.Since(lockStart).Seconds()),
		"POST /lock + 进程退出 + 端口探测", lockOK)
	if !lockOK {
		t.Fatalf("目录树会话锁定不符合预期：exitErr=%v portClosed=%v\n输出:\n%s", exitErr, closed, p.output())
	}
}
