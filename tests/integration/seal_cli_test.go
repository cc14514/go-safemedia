package integration

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cc14514/go-safemedia/internal/account"
	"github.com/cc14514/go-safemedia/internal/vault"
)

// fakeMP4 生成带合法 ISO BMFF 头的最小 mp4 数据（封存只 sniff 头，不解析内容）。
func fakeMP4(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31 + 7)
	}
	copy(b[0:4], []byte{0x00, 0x00, 0x00, 0x20})
	copy(b[4:8], "ftyp")
	copy(b[8:12], "isom")
	return b
}

// fakeMOV 生成 brand 为 `qt  ` 的最小 mov 数据。
func fakeMOV(n int) []byte {
	b := fakeMP4(n)
	copy(b[8:12], "qt  ")
	return b
}

// newAccountForTest 创建一个测试账户并返回 keystore 路径与口令。
func newAccountForTest(t *testing.T, dir, passphrase string) string {
	t.Helper()
	keystore := filepath.Join(dir, "keystore.json")
	s := &script{secrets: []string{passphrase, passphrase}}
	if _, err := runCLI(t, s, "--account", keystore, "account", "create"); err != nil {
		t.Fatalf("account create 失败: %v", err)
	}
	return keystore
}

func TestM2SealAndOpenVaultRoundTrip(t *testing.T) {
	dir := t.TempDir()
	const pw = "seal passphrase"
	keystore := newAccountForTest(t, dir, pw)

	src := filepath.Join(dir, "clip.mp4")
	plain := fakeMP4(3000)
	if err := os.WriteFile(src, plain, 0o644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "clip.svid")
	s := &script{secrets: []string{pw}}
	out, err := runCLI(t, s, "--account", keystore, "seal", src, "-o", dst)
	if err != nil {
		t.Fatalf("seal 失败: %v\n输出:\n%s", err, out)
	}
	if !fileExists(dst) {
		t.Fatalf("未生成密档:\n%s", out)
	}

	// AC3.1：原文件不被修改
	if got, _ := os.ReadFile(src); !bytes.Equal(got, plain) {
		t.Fatal("封存修改了原文件")
	}

	// 用账户私钥启封并逐字节校验
	acct, err := account.LoadKeystoreWithPassphrase(keystore, []byte(pw))
	if err != nil {
		t.Fatalf("加载账户失败: %v", err)
	}
	r, err := vault.Open(dst, acct.PrivateKey())
	if err != nil {
		t.Fatalf("Open 密档失败: %v", err)
	}
	defer r.Close()
	got := make([]byte, r.Size())
	if _, err := r.ReadAt(got, 0); err != nil {
		t.Fatalf("ReadAt 失败: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("启封内容与原文不一致")
	}

	// AC3.5 / AC3.7：密档里没有原始文件名，也看不到明文片段
	raw, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("clip.mp4")) {
		t.Fatal("密档中出现了原始文件名明文")
	}
	if bytes.Contains(raw, plain[:64]) {
		t.Fatal("密档中出现了连续明文片段")
	}
}

func TestM2WrongAccountCannotOpen(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	const pw = "shared passphrase"
	keystoreA := newAccountForTest(t, dirA, pw)
	keystoreB := newAccountForTest(t, dirB, pw)

	src := filepath.Join(dirA, "clip.mp4")
	if err := os.WriteFile(src, fakeMP4(2000), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dirA, "clip.svid")
	s := &script{secrets: []string{pw}}
	if out, err := runCLI(t, s, "--account", keystoreA, "seal", src, "-o", dst); err != nil {
		t.Fatalf("seal 失败: %v\n%s", err, out)
	}

	other, err := account.LoadKeystoreWithPassphrase(keystoreB, []byte(pw))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vault.Open(dst, other.PrivateKey()); err == nil {
		t.Fatal("别的账户不该能打开本账户的密档")
	}
}

func TestM2SealRejectsUnsupportedContainer(t *testing.T) {
	dir := t.TempDir()
	const pw = "seal passphrase"
	keystore := newAccountForTest(t, dir, pw)

	// 随机字节（既非 ftyp 也非 EBML）
	src := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(src, []byte("this-is-not-a-video"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "movie.svid")
	s := &script{secrets: []string{pw}}
	out, err := runCLI(t, s, "--account", keystore, "seal", src, "-o", dst)
	if err == nil {
		t.Fatalf("白名单外容器应当拒绝封存:\n%s", out)
	}
	if fileExists(dst) {
		t.Fatal("拒绝封存时不应留下密档")
	}
}

func TestM2SealDirBatchAndForce(t *testing.T) {
	dir := t.TempDir()
	const pw = "seal passphrase"
	keystore := newAccountForTest(t, dir, pw)

	in := filepath.Join(dir, "in")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.mp4", "b.mp4"} {
		if err := os.WriteFile(filepath.Join(in, name), fakeMP4(1500), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 非白名单文件在目录遍历时被跳过，不影响同目录的视频
	if err := os.WriteFile(filepath.Join(in, "c.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &script{secrets: []string{pw}}
	out, err := runCLI(t, s, "--account", keystore, "seal", in)
	if err != nil {
		t.Fatalf("目录封存失败: %v\n%s", err, out)
	}
	if !strings.Contains(out, "跳过 1 个非视频文件") {
		t.Fatalf("未报告跳过的非视频文件:\n%s", out)
	}
	for _, name := range []string{"a", "b"} {
		if !fileExists(filepath.Join(in, name+".mp4.svid")) {
			t.Fatalf("%s 未被封存:\n%s", name, out)
		}
	}
	if fileExists(filepath.Join(in, "c.txt.svid")) {
		t.Fatalf("非视频文件不该被封存:\n%s", out)
	}

	// 已存在且未加 --force：拒绝
	s2 := &script{secrets: []string{pw}}
	out2, err := runCLI(t, s2, "--account", keystore, "seal", filepath.Join(in, "a.mp4"))
	if err == nil {
		t.Fatalf("已存在密档且未指定 --force 时应当失败:\n%s", out2)
	}

	// --force 覆盖成功
	s3 := &script{secrets: []string{pw}}
	out3, err := runCLI(t, s3, "--account", keystore, "seal", filepath.Join(in, "a.mp4"), "--force")
	if err != nil {
		t.Fatalf("--force 覆盖失败: %v\n%s", err, out3)
	}
}

// AC3.8：目录输入 + 目录输出，相对子路径在输出目录中重建。
func TestM2SealDirMirrorsTreeToOutDir(t *testing.T) {
	dir := t.TempDir()
	const pw = "seal passphrase"
	keystore := newAccountForTest(t, dir, pw)

	in := filepath.Join(dir, "in")
	for _, sub := range []string{"", "sub", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(in, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(in, rel), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("top.mp4", fakeMP4(1200))
	write("sub/nested.mov", fakeMOV(1200))
	write(".hidden/skip.mp4", fakeMP4(1200))
	write("notes.txt", []byte("not a video"))

	outDir := filepath.Join(dir, "vaults")
	s := &script{secrets: []string{pw}}
	out, err := runCLI(t, s, "--account", keystore, "seal", in, "-o", outDir)
	if err != nil {
		t.Fatalf("目录 → 目录封存失败: %v\n%s", err, out)
	}

	for _, rel := range []string{"top.mp4.svid", filepath.Join("sub", "nested.mov.svid")} {
		if !fileExists(filepath.Join(outDir, rel)) {
			t.Fatalf("输出目录缺少 %s:\n%s", rel, out)
		}
	}
	// 隐藏项与非视频不封存
	if fileExists(filepath.Join(outDir, ".hidden", "skip.mp4.svid")) {
		t.Fatalf("隐藏目录中的文件不该被封存:\n%s", out)
	}
	if fileExists(filepath.Join(outDir, "notes.txt.svid")) {
		t.Fatalf("非视频文件不该被封存:\n%s", out)
	}
	// 指定了输出目录时，源目录里不留密档
	if fileExists(filepath.Join(in, "top.mp4.svid")) {
		t.Fatalf("指定 -o 后不该再把密档写到源目录:\n%s", out)
	}

	// 封存结果确实可用该账户启封
	acct, err := account.LoadKeystoreWithPassphrase(keystore, []byte(pw))
	if err != nil {
		t.Fatal(err)
	}
	r, err := vault.Open(filepath.Join(outDir, "sub", "nested.mov.svid"), acct.PrivateKey())
	if err != nil {
		t.Fatalf("启封镜像路径下的密档失败: %v", err)
	}
	defer r.Close()
	if r.Size() != 1200 {
		t.Fatalf("启封大小不符: %d", r.Size())
	}
}
