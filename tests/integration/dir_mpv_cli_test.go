package integration

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestM4PlayDirectoryIndex 覆盖 AC5.1–AC5.3：目录索引列出全部密档、点选可播、无额外元数据。
func TestM4PlayDirectoryIndex(t *testing.T) {
	dir := t.TempDir()
	const pw = "library passphrase"
	keystore := newAccountForTest(t, dir, pw)

	// 造两个视频并封存到 vaults/
	srcDir := filepath.Join(dir, "videos")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plain := map[string][]byte{}
	for _, name := range []string{"one.mp4", "two.mp4"} {
		plain[name] = fakeMP4(4000 + len(name)*100)
		if err := os.WriteFile(filepath.Join(srcDir, name), plain[name], 0o644); err != nil {
			t.Fatal(err)
		}
	}
	vaultDir := filepath.Join(dir, "vaults")
	if err := os.MkdirAll(vaultDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.mp4", "two.mp4"} {
		if out, err := runCLI(t, &script{secrets: []string{pw}}, "--account", keystore,
			"seal", filepath.Join(srcDir, name), "-o", filepath.Join(vaultDir, name+".svid")); err != nil {
			t.Fatalf("seal %s 失败: %v\n%s", name, err, out)
		}
	}

	buf, done := startCLI(t, &script{secrets: []string{pw}},
		"--account", keystore, "play", vaultDir, "--no-browser", "--idle", "10m")
	base := waitForURL(t, buf)

	// AC5.1：索引页列出全部密档（以密档文件名为条目）
	resp, err := http.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	html := string(page)
	for _, name := range []string{"one.mp4.svid", "two.mp4.svid"} {
		if !strings.Contains(html, name) {
			t.Fatalf("索引页缺少条目 %s:\n%s", name, html)
		}
	}
	if strings.Contains(html, "<video") {
		t.Fatal("索引页应只列条目，不应内嵌播放器")
	}
	if strings.Contains(html, "duration") || strings.Contains(html, "resolution") {
		t.Fatal("AC5.3：索引页不应出现时长/分辨率等元数据")
	}

	// 点选第一个条目 → 播放页 → 拉流
	watch := regexp.MustCompile(`href="(/watch/[0-9a-f]+)"`).FindStringSubmatch(html)
	if watch == nil {
		t.Fatalf("索引页没有 watch 链接:\n%s", html)
	}
	wresp, err := http.Get(base + strings.TrimPrefix(watch[1], "/"))
	if err != nil {
		t.Fatal(err)
	}
	wpage, _ := io.ReadAll(wresp.Body)
	wresp.Body.Close()
	media := regexp.MustCompile(`src="(/media/[0-9a-f]+/[0-9a-f]+)"`).FindStringSubmatch(string(wpage))
	if media == nil {
		t.Fatalf("播放页没有媒体地址:\n%s", wpage)
	}
	if !strings.Contains(string(wpage), "--player mpv") {
		t.Fatal("播放页缺少 mpv 逃生提示（AC4.5）")
	}

	mresp, err := http.Get(base + strings.TrimPrefix(media[1], "/"))
	if err != nil {
		t.Fatal(err)
	}
	stream, _ := io.ReadAll(mresp.Body)
	mresp.Body.Close()
	if !bytes.Equal(stream, plain["one.mp4"]) {
		t.Fatal("目录模式拉流内容与原文不一致（注意索引排序为 one 在前）")
	}

	// 锁定后命令退出
	lresp, err := http.Post(base+"lock", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	lresp.Body.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("目录模式 play 退出返回错误: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("目录模式锁定后未退出:\n%s", buf.String())
	}
}

// TestM4PlayDirectoryEmpty 覆盖 AC5.2：空目录显示空列表提示。
func TestM4PlayDirectoryEmpty(t *testing.T) {
	dir := t.TempDir()
	const pw = "empty library passphrase"
	keystore := newAccountForTest(t, dir, pw)
	empty := filepath.Join(dir, "empty-vaults")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	buf, done := startCLI(t, &script{secrets: []string{pw}},
		"--account", keystore, "play", empty, "--no-browser", "--idle", "10m")
	base := waitForURL(t, buf)

	resp, err := http.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), "没有 .svid 密档") {
		t.Fatalf("空目录索引页缺提示:\n%s", page)
	}

	if lresp, err := http.Post(base+"lock", "text/plain", nil); err == nil {
		lresp.Body.Close()
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("空目录模式锁定后未退出")
	}
}

// TestM4MpvModePassesLoopbackURL 覆盖 AC6.1：--player mpv 把环回媒体地址交给 mpv。
// 用一个假 mpv 脚本捕获 argv，避免依赖本机是否安装 mpv。
func TestM4MpvModePassesLoopbackURL(t *testing.T) {
	dir := t.TempDir()
	const pw = "mpv passphrase"
	keystore := newAccountForTest(t, dir, pw)

	src := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(src, fakeMP4(3000), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "clip.svid")
	if _, err := runCLI(t, &script{secrets: []string{pw}}, "--account", keystore, "seal", src, "-o", dst); err != nil {
		t.Fatal(err)
	}

	// 假 mpv：把 argv 写进文件后立即退出（模拟播放结束）
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(dir, "mpv-args.txt")
	scriptBody := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$MPV_ARGS_FILE\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "mpv"), []byte(scriptBody), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("MPV_ARGS_FILE", argsFile)

	buf, done := startCLI(t, &script{secrets: []string{pw}},
		"--account", keystore, "play", dst, "--player", "mpv", "--idle", "10m")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("mpv 模式退出错误: %v\n输出:\n%s", err, buf.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("mpv 模式未退出:\n%s", buf.String())
	}

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("假 mpv 未被调用: %v\n输出:\n%s", err, buf.String())
	}
	if !regexp.MustCompile(`http://127\.0\.0\.1:\d+/media/[0-9a-f]+`).MatchString(string(raw)) {
		t.Fatalf("交给 mpv 的参数不是环回媒体地址:\n%s", raw)
	}
}

// TestM4MpvMissingIsReported 覆盖 AC6.2：未安装 mpv 时明确提示且不启动服务。
func TestM4MpvMissingIsReported(t *testing.T) {
	dir := t.TempDir()
	const pw = "no mpv passphrase"
	keystore := newAccountForTest(t, dir, pw)

	src := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(src, fakeMP4(1500), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "clip.svid")
	if _, err := runCLI(t, &script{secrets: []string{pw}}, "--account", keystore, "seal", src, "-o", dst); err != nil {
		t.Fatal(err)
	}

	// 把 PATH 指向空目录：确定性地制造「没有 mpv」
	t.Setenv("PATH", filepath.Join(dir, "nowhere"))

	buf, done := startCLI(t, &script{secrets: []string{pw}},
		"--account", keystore, "play", dst, "--player", "mpv")
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("AC6.2：mpv 缺失时应当报错退出")
		}
		if !strings.Contains(err.Error(), "mpv") {
			t.Fatalf("错误信息未提到 mpv: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("mpv 缺失时 play 未退出")
	}
	if urlRe.MatchString(buf.String()) {
		t.Fatalf("mpv 缺失却启动了播放服务:\n%s", buf.String())
	}
}

// TestM4MpvModeRejectsDirectory 目录模式不支持 mpv（提示改用浏览器模式）。
func TestM4MpvModeRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	const pw = "dir mpv passphrase"
	keystore := newAccountForTest(t, dir, pw)
	vaultDir := filepath.Join(dir, "vaults")
	if err := os.MkdirAll(vaultDir, 0o755); err != nil {
		t.Fatal(err)
	}

	buf, done := startCLI(t, &script{secrets: []string{pw}},
		"--account", keystore, "play", vaultDir, "--player", "mpv")
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("目录模式 + mpv 应当报错")
		}
		if !strings.Contains(err.Error(), "mpv") {
			t.Fatalf("错误信息不明确: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("目录模式 + mpv 未退出:\n%s", buf.String())
	}
	if urlRe.MatchString(buf.String()) {
		t.Fatalf("目录模式 + mpv 不应启动服务:\n%s", buf.String())
	}
}
