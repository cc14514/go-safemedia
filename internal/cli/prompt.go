package cli

import (
	"bufio"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/cc14514/go-safemedia/internal/account"
)

// Prompter 抽象交互输入，便于 L2 集成测试注入脚本化输入。
type Prompter struct {
	In          io.Reader
	Out         io.Writer
	ReadSecret  func(prompt string) ([]byte, error)
	ReadLine    func(prompt string) (string, error)
	TerminalFd  int
	PickIndexes func(total, count int) []int

	// reader 复用同一条输入流上的行读取器（多个 bufio.Reader 会互相吞掉缓冲数据）。
	reader *bufio.Reader
}

// lineReader 返回复用的行读取器。
func (p *Prompter) lineReader() *bufio.Reader {
	if p.reader == nil {
		in := p.In
		if in == nil {
			in = os.Stdin
		}
		p.reader = bufio.NewReader(in)
	}
	return p.reader
}

// NewTermPrompter 返回绑定真实终端的 Prompter（口令无回显）。
func NewTermPrompter() *Prompter {
	p := &Prompter{
		In:         os.Stdin,
		Out:        os.Stdout,
		TerminalFd: int(os.Stdin.Fd()),
		PickIndexes: func(total, count int) []int {
			return RandomIndexes(total, count, rand.Intn)
		},
	}
	p.ReadSecret = func(prompt string) ([]byte, error) {
		fmt.Fprint(p.Out, prompt)
		if p.TerminalFd >= 0 && term.IsTerminal(p.TerminalFd) {
			b, err := term.ReadPassword(p.TerminalFd)
			fmt.Fprintln(p.Out)
			return b, err
		}
		// 非终端（管道/重定向）：退回逐行读取，便于脚本化与黑盒 E2E。
		// 输入本来就不经过用户终端，不存在回显泄密问题。
		line, err := p.lineReader().ReadString('\n')
		if err != nil && line == "" {
			return nil, err
		}
		return []byte(strings.TrimRight(line, "\r\n")), nil
	}
	p.ReadLine = func(prompt string) (string, error) {
		fmt.Fprint(p.Out, prompt)
		line, err := p.lineReader().ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimSpace(line), nil
	}
	return p
}

// Password 读取并校验一个口令（单次输入）。
func (p *Prompter) Password(prompt string) ([]byte, error) {
	for {
		pw, err := p.ReadSecret(prompt)
		if err != nil {
			return nil, err
		}
		advice, err := account.CheckPassword(string(pw))
		if err != nil {
			account.Wipe(pw)
			fmt.Fprintf(p.Out, "✗ %v\n%s\n", err, advice)
			continue
		}
		if advice != "" {
			fmt.Fprintf(p.Out, "⚠ %s\n", advice)
		}
		return pw, nil
	}
}

// ConfirmPassword 读取口令并要求二次确认（用于新建/重置口令）。
func (p *Prompter) ConfirmPassword(prompt string) ([]byte, error) {
	for {
		first, err := p.Password(prompt)
		if err != nil {
			return nil, err
		}
		second, err := p.ReadSecret("请再次输入以确认: ")
		if err != nil {
			account.Wipe(first)
			return nil, err
		}
		if string(first) != string(second) {
			account.Wipe(second)
			account.Wipe(first)
			fmt.Fprintln(p.Out, "✗ 两次输入不一致，请重来")
			continue
		}
		account.Wipe(second)
		return first, nil
	}
}

// ReadMnemonic 读取用户输入的助记词（空格分隔）。
func (p *Prompter) ReadMnemonic() (string, error) {
	line, err := p.ReadLine("请输入助记词（空格分隔）: ")
	if err != nil {
		return "", err
	}
	return strings.Join(strings.Fields(line), " "), nil
}

// ConfirmMnemonic 强制抄写确认：随机抽查 count 个词的序号，要求用户按序复述。
func (p *Prompter) ConfirmMnemonic(mnemonic string) error {
	words := strings.Fields(mnemonic)
	if len(words) == 0 {
		return fmt.Errorf("助记词为空")
	}
	count := 3
	if len(words) < count {
		count = len(words)
	}
	idxs := p.PickIndexes(len(words), count)
	fmt.Fprintln(p.Out, "请按序号复述下列助记词，以确认已经抄写：")
	for _, i := range idxs {
		fmt.Fprintf(p.Out, "  #%d ", i+1)
	}
	fmt.Fprintln(p.Out)
	for _, i := range idxs {
		got, err := p.ReadLine(fmt.Sprintf("第 %d 个词: ", i+1))
		if err != nil {
			return err
		}
		if strings.TrimSpace(got) != words[i] {
			return fmt.Errorf("第 %d 个词不正确，助记词确认失败（未保存任何文件）", i+1)
		}
	}
	return nil
}

// RandomIndexes 从 total 个位置里随机挑 count 个不重复的下标（升序返回）。
// randInt 注入随机源以便测试可重现。
func RandomIndexes(total, count int, randInt func(n int) int) []int {
	if count > total {
		count = total
	}
	if count <= 0 || total <= 0 {
		return nil
	}
	seen := make(map[int]bool, count)
	for len(seen) < count {
		seen[randInt(total)] = true
	}
	idxs := make([]int, 0, len(seen))
	for i := range seen {
		idxs = append(idxs, i)
	}
	for i := 1; i < len(idxs); i++ {
		for j := i; j > 0 && idxs[j-1] > idxs[j]; j-- {
			idxs[j-1], idxs[j] = idxs[j], idxs[j-1]
		}
	}
	return idxs
}
