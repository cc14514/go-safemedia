package e2e

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// itoa 便捷转换退出码为文本。
func itoa(n int) string { return strconv.Itoa(n) }

// runCLI 把 CLI 跑到结束，返回退出码与合并输出（stdin 用于喂口令）。
func runCLI(t *testing.T, bin, stdin string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	if err == nil {
		return 0, buf.String()
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("运行 CLI 失败: %v", err)
	}
	return ee.ExitCode(), buf.String()
}

// TestE2E_ExitCodes 校验 plan.md §7 的进程退出码约定（脚本化调用依赖它）。
func TestE2E_ExitCodes(t *testing.T) {
	bin := buildBinary(t)
	dir := t.TempDir()
	keystore := filepath.Join(dir, "keystore.json")
	const pw = "e2e exit code passphrase"

	createAccount(t, bin, keystore, pw)
	src, _ := writeSample(t, dir, loadSamples(t)[0])
	vaultPath := src + ".svid"
	sealVault(t, bin, keystore, pw, src, vaultPath)

	// 0：成功。
	code, _ := runCLI(t, bin, "", "version")
	record("退出码 0（成功）", "safemedia version 退出码 = 0",
		"退出码="+itoa(code), bin+" version", code == 0)
	if code != 0 {
		t.Errorf("version 退出码=%d，期望 0", code)
	}

	// 1：用户/输入错误（口令错）。
	code, out := runCLI(t, bin, "definitely-wrong-passphrase\n",
		"--account", keystore, "play", vaultPath, "--no-browser")
	ok1 := code == 1 && strings.Contains(out, "口令错误")
	record("退出码 1（用户错误：口令错）", "错误口令时退出码 = 1 且不启动服务",
		"退出码="+itoa(code)+"；输出="+summaryLine(out), bin+" play <vault> --no-browser", ok1)
	if !ok1 {
		t.Errorf("口令错误退出码=%d（期望 1）\n%s", code, out)
	}

	// 2：数据错误（密档头部损坏）。
	bad := filepath.Join(dir, "bad.svid")
	data, err := os.ReadFile(vaultPath)
	if err != nil {
		t.Fatal(err)
	}
	copy(data[0:4], []byte{0, 0, 0, 0}) // 打掉 magic
	if err := os.WriteFile(bad, data, 0o600); err != nil {
		t.Fatal(err)
	}
	code, out = runCLI(t, bin, pw+"\n", "--account", keystore, "play", bad, "--no-browser")
	ok2 := code == 2 && strings.Contains(out, "不是密档")
	record("退出码 2（数据错误：头部损坏）", "magic 被破坏时退出码 = 2",
		"退出码="+itoa(code)+"；输出="+summaryLine(out), bin+" play <损坏密档> --no-browser", ok2)
	if !ok2 {
		t.Errorf("坏头部退出码=%d（期望 2）\n%s", code, out)
	}

	// 3：环境错误（mpv 缺失）。仅在本机确实没有 mpv 时校验。
	if _, err := exec.LookPath("mpv"); err != nil {
		code, out = runCLI(t, bin, pw+"\n",
			"--account", keystore, "play", vaultPath, "--player", "mpv")
		ok3 := code == 3 && strings.Contains(out, "mpv")
		record("退出码 3（环境错误：mpv 缺失）", "指定 --player mpv 而本机无 mpv 时退出码 = 3",
			"退出码="+itoa(code)+"；输出="+summaryLine(out), bin+" play <vault> --player mpv", ok3)
		if !ok3 {
			t.Errorf("mpv 缺失退出码=%d（期望 3）\n%s", code, out)
		}
	} else {
		record("退出码 3（环境错误：mpv 缺失）", "本机装有 mpv，跳过该行",
			"mpv 存在，无法构造缺失场景", "exec.LookPath(mpv)", true)
	}
}
