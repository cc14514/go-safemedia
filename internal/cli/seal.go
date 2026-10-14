package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/cc14514/go-safemedia/internal/account"
	"github.com/cc14514/go-safemedia/internal/vault"
)

// VaultExt 是密档扩展名。
const VaultExt = ".svid"

func newSealCmd() *cobra.Command {
	var outPath, dir, outDir string
	var force bool

	cmd := &cobra.Command{
		Use:   "seal <video>...",
		Short: "把视频封存为 .svid 密档（分块 AES-256-GCM，可拖进度）",
		Long: `把 mp4 / mov / webm 封存为 .svid 密档。

封存不转码：密档体积 ≈ 原文件 + 头部与每块认证标签开销。
文件的解密密钥用你的账户公钥封装——只有持有该账户私钥的人能启封播放。
因此封存需要解锁账户；明文全程只在内存中，不落盘。`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && dir == "" {
				return fmt.Errorf("请至少给出一个视频文件，或用 --dir 指定目录")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			inputs, err := collectSealInputs(args, dir)
			if err != nil {
				return err
			}
			if len(inputs) == 0 {
				return fmt.Errorf("没有找到可封存的文件")
			}
			if outPath != "" && len(inputs) != 1 {
				return fmt.Errorf("-o 只适用于单个输入文件（当前 %d 个）", len(inputs))
			}

			acct, err := unlockAccount()
			if err != nil {
				return err
			}
			pub := &acct.PrivateKey().PublicKey
			fmt.Fprintf(out, "使用账户 %s 的公钥封存\n", acct.Address())

			prog := newProgressPrinter(cmd.ErrOrStderr())
			var failed int
			for _, in := range inputs {
				dst, err := sealOutputPath(in, outPath, outDir)
				if err != nil {
					failed++
					fmt.Fprintf(out, "✗ %s: %v\n", in, err)
					continue
				}
				if !force {
					if _, err := os.Stat(dst); err == nil {
						failed++
						fmt.Fprintf(out, "✗ %s 已存在（用 --force 覆盖）\n", dst)
						continue
					}
				}

				fmt.Fprintf(out, "封存 %s\n   → %s\n", in, dst)
				res, err := vault.SealFileWithProgress(in, dst, pub, prog.update)
				prog.clear()
				if err != nil {
					failed++
					fmt.Fprintf(out, "✗ 失败：%v\n", err)
					fmt.Fprintln(out, "  若已产生未完成的 .svid，可安全删除后重试（其中不含明文）。")
					continue
				}
				ratio := ""
				if info, statErr := os.Stat(dst); statErr == nil && res.PlaintextSize > 0 {
					ratio = fmt.Sprintf("  +%.1f%%", float64(info.Size()-res.PlaintextSize)*100/float64(res.PlaintextSize))
				}
				fmt.Fprintf(out, "✓ %s 容器=%s 明文=%s 块数=%d%s\n",
					filepath.Base(dst), res.Container, humanSize(res.PlaintextSize), res.Chunks, ratio)
			}
			if failed > 0 {
				return fmt.Errorf("%d/%d 个文件封存失败", failed, len(inputs))
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&outPath, "out", "o", "", "输出密档路径（仅单个输入）")
	cmd.Flags().StringVar(&dir, "dir", "", "批量封存该目录下所有 mp4/mov/webm")
	cmd.Flags().StringVar(&outDir, "out-dir", "", "批量输出目录（默认与源文件同目录）")
	cmd.Flags().BoolVar(&force, "force", false, "覆盖已存在的密档")
	return cmd
}

// unlockAccount 解锁账户（封存需要公钥；公钥只能由私钥派生）。
func unlockAccount() (*account.Account, error) {
	path := AccountPath()
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("账户不存在：%s（先运行 safemedia account create）", path)
	}
	passphrase, err := Prompt.ReadSecret("输入登录口令: ")
	if err != nil {
		return nil, err
	}
	defer account.Wipe(passphrase)
	return account.LoadKeystoreWithPassphrase(path, passphrase)
}

// collectSealInputs 合并位置参数与 --dir 的文件列表（去重、排序）。
func collectSealInputs(args []string, dir string) ([]string, error) {
	seen := make(map[string]bool)
	var inputs []string
	add := func(p string) {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if !seen[abs] {
			seen[abs] = true
			inputs = append(inputs, p)
		}
	}
	for _, a := range args {
		info, err := os.Stat(a)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", a, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("%s 是目录（用 --dir 批量封存）", a)
		}
		add(a)
	}
	if dir != "" {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if strings.EqualFold(filepath.Ext(e.Name()), VaultExt) {
				continue
			}
			add(filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(inputs)
	return inputs, nil
}

// sealOutputPath 计算输出密档路径。
func sealOutputPath(in, outPath, outDir string) (string, error) {
	if outPath != "" {
		return outPath, nil
	}
	dir := outDir
	if dir == "" {
		dir = filepath.Dir(in)
	}
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		return "", fmt.Errorf("--out-dir %s 不是目录", dir)
	}
	return filepath.Join(dir, filepath.Base(in)+VaultExt), nil
}

// progressPrinter 在终端上显示封存进度（非终端时静默，避免污染脚本输出）。
type progressPrinter struct {
	w       io.Writer
	enabled bool
	last    int
}

func newProgressPrinter(w io.Writer) *progressPrinter {
	f, ok := w.(*os.File)
	return &progressPrinter{w: w, enabled: ok && term.IsTerminal(int(f.Fd())), last: -1}
}

func (p *progressPrinter) update(done, total int64) {
	if !p.enabled || total <= 0 {
		return
	}
	pct := int(done * 100 / total)
	if pct == p.last || (pct-p.last < 5 && pct != 100) {
		return
	}
	p.last = pct
	fmt.Fprintf(p.w, "\r  进度 %3d%%  %s / %s", pct, humanSize(done), humanSize(total))
}

func (p *progressPrinter) clear() {
	if p.enabled {
		fmt.Fprint(p.w, "\r\033[K")
	}
}

// humanSize 格式化字节数。
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
