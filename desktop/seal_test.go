package main

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cc14514/go-safemedia/internal/account"
	"github.com/cc14514/go-safemedia/internal/desktopcfg"
	"github.com/cc14514/go-safemedia/internal/seal"
	"github.com/cc14514/go-safemedia/internal/vault"
)

// 外壳的封存接线（US10）：原生拖放 → 引擎投递地址 → 队列 → 面板。

// T10.11（AC10.1）：newSealer() 返回的工厂必须「返回时队列已在运行」，
// 并真的能把文件封存成可用账户启封的密档。
func TestDesktopSealerFactorySealsFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "clip.mp4")
	plain := fakeMP4ForShell(200_000)
	if err := os.WriteFile(src, plain, 0o644); err != nil {
		t.Fatal(err)
	}

	acct, err := account.Generate(12)
	if err != nil {
		t.Fatal(err)
	}
	factory := newSealer()
	q := factory([]seal.Job{{Src: src, Dst: filepath.Join(dir, "clip.mp4.svid")}}, &acct.PrivateKey().PublicKey)
	if q == nil {
		t.Fatal("工厂应返回队列")
	}
	if !q.Active() {
		t.Fatal("工厂返回时队列应已在运行")
	}
	select {
	case <-q.Done():
	case <-time.After(30 * time.Second):
		t.Fatal("封存未在 30s 内结束")
	}

	dst := filepath.Join(dir, "clip.mp4.svid")
	r, err := vault.Open(dst, acct.PrivateKey())
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer r.Close()
	got := make([]byte, r.Size())
	if _, err := r.ReadAt(got, 0); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(plain) {
		t.Fatal("启封内容与原文不一致")
	}
}

// T10.12（AC10.6/AC10.7/AC10.10）：dropPaths 把路径投递到引擎；
// 解锁状态下封存完成并能从索引页看到新密档；锁定状态下不写任何文件。
func TestDropPathsWiresToEngine(t *testing.T) {
	home := t.TempDir()
	const pw = "shell drop passphrase"
	keystore := filepath.Join(home, "keystore.json")
	writeTestKeystore(t, keystore, pw)

	libDir := filepath.Join(home, "library")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := desktopcfg.Save(home, desktopcfg.Config{Library: libDir, Account: keystore}); err != nil {
		t.Fatal(err)
	}

	appArgsOverride = nil
	app := &App{}
	base, _, err := prepare(app, home)
	if err != nil {
		t.Fatalf("prepare 失败: %v", err)
	}
	defer app.shutdown(t.Context())

	// ① 锁定态投递：被拒，且库根里不产生任何文件（AC10.7）
	src := filepath.Join(home, "clip.mp4")
	if err := os.WriteFile(src, fakeMP4ForShell(300_000), 0o644); err != nil {
		t.Fatal(err)
	}
	app.dropPaths([]string{src})
	if des, err := os.ReadDir(libDir); err != nil || len(des) != 0 {
		t.Fatalf("锁定态投递不得写文件: %v %v", des, err)
	}

	// ② 登录解锁（页面路径：POST /login）
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.PostForm(base+"login", url.Values{"account": {keystore}, "password": {pw}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("登录应 303，得到 %d", resp.StatusCode)
	}

	// ③ 解锁态投递：封存完成，密档出现在库根与索引页（AC10.1/AC10.3/AC10.10）
	app.dropPaths([]string{src})
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if des, err := os.ReadDir(libDir); err == nil && len(des) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	dst := filepath.Join(libDir, "clip.mp4.svid")
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("库根下未生成密档: %v", err)
	}

	// 索引页（解锁态）应能看到新密档（重扫由队列结束时触发，稍等片刻）
	deadline = time.Now().Add(10 * time.Second)
	var body []byte
	for time.Now().Before(deadline) {
		idx, err := client.Get(base)
		if err != nil {
			t.Fatal(err)
		}
		body, _ = io.ReadAll(idx.Body)
		idx.Body.Close()
		if strings.Contains(string(body), "clip.mp4.svid") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("索引页应看到新密档:\n%s", truncate(string(body), 400))
}

// fakeMP4ForShell 造带合法 ftyp 头的最小 mp4（外壳测试用）。
func fakeMP4ForShell(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*17 + 3)
	}
	copy(b[0:4], []byte{0x00, 0x00, 0x00, 0x20})
	copy(b[4:8], "ftyp")
	copy(b[8:12], "isom")
	return b
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

var _ = json.Marshal
var _ = fmt.Sprint
var _ ecdsa.PublicKey
