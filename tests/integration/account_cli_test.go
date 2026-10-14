// Package integration 是 go-safemedia 的 L2 集成测试（Feature 完成门禁）。
//
// 本文件覆盖 M1/T1.4：account create → export-recovery → recover 三条命令的
// 正向路径（进程内执行，交互脚本化注入）。
package integration

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cc14514/go-safemedia/internal/cli"
)

// script 是脚本化交互器：口令按队列返回，助记词复述从已打印输出里回读。
type script struct {
	out     *bytes.Buffer
	secrets []string
	lines   []string
}

func (s *script) prompter() *cli.Prompter {
	p := &cli.Prompter{Out: s.out}
	p.PickIndexes = func(total, count int) []int {
		idxs := make([]int, 0, count)
		for i := 0; i < count && i < total; i++ {
			idxs = append(idxs, i)
		}
		return idxs
	}
	p.ReadSecret = func(prompt string) ([]byte, error) {
		if len(s.secrets) == 0 {
			return nil, fmt.Errorf("测试脚本缺少口令（prompt=%q）", prompt)
		}
		v := s.secrets[0]
		s.secrets = s.secrets[1:]
		return []byte(v), nil
	}
	p.ReadLine = func(prompt string) (string, error) {
		if m := regexp.MustCompile(`第 (\d+) 个词`).FindStringSubmatch(prompt); m != nil {
			n, _ := strconv.Atoi(m[1])
			words := parsePrintedMnemonic(s.out.String())
			if n-1 >= len(words) {
				return "", fmt.Errorf("输出中没有第 %d 个助记词", n)
			}
			return words[n-1], nil
		}
		if len(s.lines) == 0 {
			return "", fmt.Errorf("测试脚本缺少输入行（prompt=%q）", prompt)
		}
		v := s.lines[0]
		s.lines = s.lines[1:]
		return v, nil
	}
	return p
}

var mnemonicLineRe = regexp.MustCompile(`(\d+)\.([a-z]+)`)

// parsePrintedMnemonic 从 create 的输出里还原助记词（按序号顺序）。
func parsePrintedMnemonic(out string) []string {
	byIndex := map[int]string{}
	max := 0
	for _, m := range mnemonicLineRe.FindAllStringSubmatch(out, -1) {
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

func runCLI(t *testing.T, s *script, args ...string) (string, error) {
	t.Helper()
	old := cli.Prompt
	oldN := cli.ScryptN
	buf := &bytes.Buffer{}
	s.out = buf
	cli.Prompt = s.prompter()
	cli.ScryptN = 1 << 10 // 测试加速：生产由 account.DefaultScryptN 保证 N>=2^17
	t.Cleanup(func() {
		cli.Prompt = old
		cli.ScryptN = oldN
	})

	cmd := cli.NewRootCmd()
	cmd.SetArgs(args)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	err := cmd.Execute()
	return buf.String(), err
}

func extractAddress(out string) string {
	re := regexp.MustCompile(`0x[0-9a-fA-F]{40}`)
	return re.FindString(out)
}

func TestM1AccountCLIFullFlow(t *testing.T) {
	dir := t.TempDir()
	keystore := dir + "/keystore.json"
	recovery := dir + "/recovery.json"

	// 1) create：设置登录口令（两次）→ 抄写确认助记词
	s := &script{secrets: []string{"correct horse battery", "correct horse battery"}}
	out, err := runCLI(t, s, "--account", keystore, "account", "create")
	if err != nil {
		t.Fatalf("account create 失败: %v\n输出:\n%s", err, out)
	}
	if len(parsePrintedMnemonic(out)) != 12 {
		t.Fatalf("输出未包含 12 词助记词:\n%s", out)
	}
	addr := extractAddress(out)
	if addr == "" {
		t.Fatalf("create 输出中没有账户地址:\n%s", out)
	}
	if !strings.Contains(out, "红线提示") {
		t.Fatal("create 输出缺少红线提示")
	}

	// 2) export-recovery：登录口令 → 设置恢复口令（两次）
	s2 := &script{secrets: []string{"correct horse battery", "recovery passphrase", "recovery passphrase"}}
	out2, err := runCLI(t, s2, "--account", keystore, "account", "export-recovery")
	if err != nil {
		t.Fatalf("export-recovery 失败: %v\n输出:\n%s", err, out2)
	}
	if !strings.Contains(out2, recovery) {
		t.Fatalf("export-recovery 输出未包含恢复文件路径:\n%s", out2)
	}

	// 3) recover --from-recovery：恢复口令 → 新登录口令（两次）
	s3 := &script{secrets: []string{"recovery passphrase", "new login passphrase", "new login passphrase"}}
	out3, err := runCLI(t, s3, "--account", keystore, "account", "recover", "--from-recovery", recovery)
	if err != nil {
		t.Fatalf("recover --from-recovery 失败: %v\n输出:\n%s", err, out3)
	}
	if got := extractAddress(out3); got != addr {
		t.Fatalf("恢复后地址变了: %s != %s", got, addr)
	}
}

func TestM1RecoverFromMnemonic(t *testing.T) {
	dir := t.TempDir()
	keystore := dir + "/keystore.json"

	s := &script{secrets: []string{"original passphrase", "original passphrase"}}
	out, err := runCLI(t, s, "--account", keystore, "account", "create")
	if err != nil {
		t.Fatalf("create 失败: %v", err)
	}
	words := parsePrintedMnemonic(out)
	if len(words) != 12 {
		t.Fatalf("未取到助记词: %q", out)
	}
	addr := extractAddress(out)

	// 删掉账户文件，模拟"丢了账户文件，只剩抄写的助记词"
	if err := os.Remove(keystore); err != nil {
		t.Fatal(err)
	}

	s2 := &script{
		lines:   []string{strings.Join(words, " ")},
		secrets: []string{"rebuilt passphrase", "rebuilt passphrase"},
	}
	out2, err := runCLI(t, s2, "--account", keystore, "account", "recover", "--from-mnemonic")
	if err != nil {
		t.Fatalf("recover --from-mnemonic 失败: %v\n输出:\n%s", err, out2)
	}
	if got := extractAddress(out2); got != addr {
		t.Fatalf("助记词重建地址不符: %s != %s", got, addr)
	}
}

func TestM1WrongLoginPasswordIsRejected(t *testing.T) {
	dir := t.TempDir()
	keystore := dir + "/keystore.json"

	s := &script{secrets: []string{"right passphrase", "right passphrase"}}
	if _, err := runCLI(t, s, "--account", keystore, "account", "create"); err != nil {
		t.Fatalf("create 失败: %v", err)
	}

	s2 := &script{secrets: []string{"wrong passphrase"}}
	_, err := runCLI(t, s2, "--account", keystore, "account", "export-recovery")
	if err == nil {
		t.Fatal("错误登录口令应当导致 export-recovery 失败")
	}
}

func TestM1MnemonicConfirmationMismatch(t *testing.T) {
	dir := t.TempDir()
	keystore := dir + "/keystore.json"

	s := &script{out: &bytes.Buffer{}, secrets: []string{"some passphrase", "some passphrase"}}
	// 复述时不从输出回读，故意给错词：通过覆盖 ReadLine 实现
	p := s.prompter()
	p.ReadLine = func(prompt string) (string, error) {
		if strings.Contains(prompt, "个词") {
			return "definitely-wrong", nil
		}
		return "", nil
	}
	old := cli.Prompt
	oldN := cli.ScryptN
	cli.Prompt = p
	cli.ScryptN = 1 << 10
	defer func() { cli.Prompt = old; cli.ScryptN = oldN }()

	buf := &bytes.Buffer{}
	cmd := cli.NewRootCmd()
	cmd.SetArgs([]string{"--account", keystore, "account", "create"})
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("助记词复述错误时 create 应当失败")
	}
	if fileExists(keystore) {
		t.Fatal("复述失败时不应写出 keystore 文件")
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
