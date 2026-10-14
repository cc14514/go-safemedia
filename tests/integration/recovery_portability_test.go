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

// httpGetAll 发起 GET 并读回状态码与完整响应体。
func httpGetAll(t *testing.T, url string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s 失败: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取 %s 响应失败: %v", url, err)
	}
	return resp.StatusCode, body
}

// fetchSingleVaultMedia 从单文件播放页解析媒体地址并全量拉流。
func fetchSingleVaultMedia(t *testing.T, base string) []byte {
	t.Helper()
	status, page := httpGetAll(t, base)
	if status != http.StatusOK {
		t.Fatalf("播放页状态码 = %d", status)
	}
	m := regexp.MustCompile(`src="(/media/[0-9a-f]+)"`).FindStringSubmatch(string(page))
	if m == nil {
		t.Fatalf("播放页没有媒体地址:\n%s", page)
	}
	status, body := httpGetAll(t, base+strings.TrimPrefix(m[1], "/"))
	if status != http.StatusOK {
		t.Fatalf("媒体状态码 = %d", status)
	}
	return body
}

// lockAndWait 调 /lock 并等待命令退出。
func lockAndWait(t *testing.T, base string, done <-chan error) {
	t.Helper()
	if resp, err := http.Post(base+"lock", "text/plain", nil); err == nil {
		resp.Body.Close()
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("锁定后命令返回错误: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("锁定后命令未退出")
	}
}

// TestM5RecoveredAccountPlaysOldVault 覆盖 T5.1：丢账户文件后用助记词重建，
// 重建后公钥不变，旧密档无需重新封存即可播放。
func TestM5RecoveredAccountPlaysOldVault(t *testing.T) {
	dir := t.TempDir()
	keystore := filepath.Join(dir, "keystore.json")
	const pw1 = "original login 12345"
	const pw2 = "rebuilt login 67890"

	s := &script{secrets: []string{pw1, pw1}}
	out, err := runCLI(t, s, "--account", keystore, "account", "create")
	if err != nil {
		t.Fatalf("create 失败: %v\n%s", err, out)
	}
	words := parsePrintedMnemonic(out)
	addr := extractAddress(out)
	if len(words) != 12 || addr == "" {
		t.Fatalf("未取到助记词/地址:\n%s", out)
	}

	plain := fakeMP4(5000)
	src := filepath.Join(dir, "old.mp4")
	if err := os.WriteFile(src, plain, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "old.svid")
	if out, err := runCLI(t, &script{secrets: []string{pw1}}, "--account", keystore, "seal", src, "-o", dst); err != nil {
		t.Fatalf("seal 失败: %v\n%s", err, out)
	}

	// 模拟丢失账户文件
	if err := os.Remove(keystore); err != nil {
		t.Fatal(err)
	}

	// 助记词重建（设新登录口令）
	s2 := &script{lines: []string{strings.Join(words, " ")}, secrets: []string{pw2, pw2}}
	out2, err := runCLI(t, s2, "--account", keystore, "account", "recover", "--from-mnemonic")
	if err != nil {
		t.Fatalf("recover --from-mnemonic 失败: %v\n%s", err, out2)
	}
	if got := extractAddress(out2); got != addr {
		t.Fatalf("重建后地址变化: %s != %s", got, addr)
	}

	// 旧密档必须仍可播（公钥未变）
	buf, done := startCLI(t, &script{secrets: []string{pw2}},
		"--account", keystore, "play", dst, "--no-browser", "--idle", "5m")
	base := waitForURL(t, buf)
	if got := fetchSingleVaultMedia(t, base); !bytes.Equal(got, plain) {
		t.Fatal("恢复后旧密档拉流内容与原文不一致")
	}
	lockAndWait(t, base, done)

	// 旧口令必须失效
	_, doneOld := startCLI(t, &script{secrets: []string{pw1}},
		"--account", keystore, "play", dst, "--no-browser")
	select {
	case err := <-doneOld:
		if err == nil {
			t.Fatal("旧登录口令应当已失效")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("旧口令场景未退出")
	}
}

// TestM5VaultIsPortableAcrossHomes 覆盖 AC7.1：账户文件与密档拷到"另一台机器"
// （全新的 HOME，无 --account，走默认 ~/.safemedia/keystore.json）即可离线播放。
func TestM5VaultIsPortableAcrossHomes(t *testing.T) {
	homeA := t.TempDir()
	homeB := t.TempDir()
	const pw = "portable passphrase 123"

	keystoreA := filepath.Join(homeA, "keystore.json")
	if _, err := runCLI(t, &script{secrets: []string{pw, pw}}, "--account", keystoreA, "account", "create"); err != nil {
		t.Fatalf("create 失败: %v", err)
	}

	plain := fakeMP4(6000)
	src := filepath.Join(homeA, "trip.mp4")
	if err := os.WriteFile(src, plain, 0o644); err != nil {
		t.Fatal(err)
	}
	vaultA := filepath.Join(homeA, "trip.svid")
	if _, err := runCLI(t, &script{secrets: []string{pw}}, "--account", keystoreA, "seal", src, "-o", vaultA); err != nil {
		t.Fatalf("seal 失败: %v", err)
	}

	// 搬运：只拷账户文件与密档到 B 机（模拟 U 盘/网盘搬运，不拷任何明文）
	ksB := filepath.Join(homeB, ".safemedia", "keystore.json")
	if err := os.MkdirAll(filepath.Dir(ksB), 0o700); err != nil {
		t.Fatal(err)
	}
	copyFile(t, keystoreA, ksB, 0o600)
	vaultB := filepath.Join(homeB, "trip.svid")
	copyFile(t, vaultA, vaultB, 0o600)

	// 切到 B 机 HOME，且不带 --account（走默认路径）
	t.Setenv("HOME", homeB)
	buf, done := startCLI(t, &script{secrets: []string{pw}},
		"--account=", "play", vaultB, "--no-browser", "--idle", "5m")
	base := waitForURL(t, buf)
	if got := fetchSingleVaultMedia(t, base); !bytes.Equal(got, plain) {
		t.Fatal("搬运后拉流内容与原文不一致")
	}
	lockAndWait(t, base, done)
}

// TestM5VaultWithoutAccountCannotPlay 覆盖 AC7.2：只有密档、没有账户文件时无法播放。
func TestM5VaultWithoutAccountCannotPlay(t *testing.T) {
	homeA := t.TempDir()
	homeB := t.TempDir()
	const pw = "no account passphrase"

	keystoreA := filepath.Join(homeA, "keystore.json")
	if _, err := runCLI(t, &script{secrets: []string{pw, pw}}, "--account", keystoreA, "account", "create"); err != nil {
		t.Fatalf("create 失败: %v", err)
	}
	src := filepath.Join(homeA, "secret.mp4")
	if err := os.WriteFile(src, fakeMP4(3000), 0o644); err != nil {
		t.Fatal(err)
	}
	vaultA := filepath.Join(homeA, "secret.svid")
	if _, err := runCLI(t, &script{secrets: []string{pw}}, "--account", keystoreA, "seal", src, "-o", vaultA); err != nil {
		t.Fatalf("seal 失败: %v", err)
	}
	// 只搬密档，不搬账户文件
	vaultB := filepath.Join(homeB, "secret.svid")
	copyFile(t, vaultA, vaultB, 0o600)

	t.Setenv("HOME", homeB)
	buf, done := startCLI(t, &script{secrets: []string{pw}},
		"--account=", "play", vaultB, "--no-browser")
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("AC7.2：没有账户文件时必须无法播放")
		}
		if !strings.Contains(err.Error(), "账户不存在") {
			t.Fatalf("错误信息不明确: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("缺少账户文件时命令未退出")
	}
	if urlRe.MatchString(buf.String()) {
		t.Fatalf("缺少账户文件却启动了播放服务:\n%s", buf.String())
	}
}

func copyFile(t *testing.T, src, dst string, mode os.FileMode) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, mode); err != nil {
		t.Fatal(err)
	}
}
