package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/cc14514/go-safemedia/internal/account"
	"github.com/cc14514/go-safemedia/internal/seal"
	"github.com/cc14514/go-safemedia/internal/vault"
)

// VaultExt 是密档扩展名（与 internal/seal 一致）。
const VaultExt = seal.VaultExt

func newSealCmd() *cobra.Command {
	var out string
	var force bool

	cmd := &cobra.Command{
		Use:   "seal <视频文件|目录>...",
		Short: "把视频封存为 .svid 密档（分块 AES-256-GCM，可拖进度）",
		Long: `把 mp4 / mov / webm 封存为 .svid 密档。

输入可以是视频文件，也可以是一个目录——目录会被递归遍历，
其中的白名单视频逐个封存（隐藏项、符号链接、已有 .svid 自动跳过）。

封存不转码：密档体积 ≈ 原文件 + 头部与每块认证标签开销。
文件的解密密钥用你的账户公钥封装——只有持有该账户私钥的人能启封播放。
因此封存需要解锁账户；明文全程只在内存中，不落盘。

输出规则：
  不给 -o           密档写在源文件旁边（clip.mp4 → clip.mp4.svid）
  单个视频 + -o 文件  写成该密档文件
  目录输入 + -o 目录  输出到该目录，源目录的相对子路径在此重建
  多个输入 + -o 目录  同上（此时 -o 一律当作输出目录）`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("请至少给出一个视频文件或目录")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()

			// 输入展开与桌面外壳共用一份实现（internal/seal），此处只负责终端呈现。
			collected, cerr := seal.Collect(args, out)
			if cerr != nil {
				if collected.UserError {
					return withExitCode(ExitUser, cerr)
				}
				return cerr
			}
			jobs := collected.Jobs
			if len(jobs) == 0 {
				return fmt.Errorf("没有找到可封存的文件（支持的容器：mp4 / mov / webm）")
			}
			if collected.Skipped > 0 {
				fmt.Fprintf(w, "跳过 %d 个非视频文件\n", collected.Skipped)
			}

			acct, err := unlockAccount()
			if err != nil {
				return err
			}
			pub := &acct.PrivateKey().PublicKey
			fmt.Fprintf(w, "使用账户 %s 的公钥封存 %d 个文件\n", acct.Address(), len(jobs))

			prog := newProgressPrinter(cmd.ErrOrStderr())
			var failed int
			for _, j := range jobs {
				if dir := filepath.Dir(j.Dst); dir != "" && dir != "." {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						failed++
						fmt.Fprintf(w, "✗ %s: %v\n", j.Dst, err)
						continue
					}
				}
				if !force {
					if _, err := os.Stat(j.Dst); err == nil {
						failed++
						fmt.Fprintf(w, "✗ %s 已存在（用 --force 覆盖）\n", j.Dst)
						continue
					}
				}

				fmt.Fprintf(w, "封存 %s\n   → %s\n", j.Src, j.Dst)
				// 不覆盖时用 O_EXCL 兜底（Stat 与创建之间若被抢占，也不得覆盖）。
				res, err := vault.SealFileContext(cmd.Context(), j.Src, j.Dst, pub,
					vault.SealOptions{Overwrite: force}, prog.update)
				prog.clear()
				if err != nil {
					failed++
					if errors.Is(err, vault.ErrTargetExists) {
						fmt.Fprintf(w, "✗ %s 已存在（用 --force 覆盖）\n", j.Dst)
						continue
					}
					fmt.Fprintf(w, "✗ 失败：%v\n", err)
					fmt.Fprintln(w, "  若已产生未完成的 .svid，可安全删除后重试（其中不含明文）。")
					continue
				}
				ratio := ""
				if info, statErr := os.Stat(j.Dst); statErr == nil && res.PlaintextSize > 0 {
					ratio = fmt.Sprintf("  +%.1f%%", float64(info.Size()-res.PlaintextSize)*100/float64(res.PlaintextSize))
				}
				fmt.Fprintf(w, "✓ %s 容器=%s 明文=%s 块数=%d%s\n",
					filepath.Base(j.Dst), res.Container, humanSize(res.PlaintextSize), res.Chunks, ratio)
			}
			if failed > 0 {
				return fmt.Errorf("%d/%d 个文件封存失败", failed, len(jobs))
			}
			if len(jobs) > 1 {
				fmt.Fprintf(w, "✓ 共 %d 个文件封存完成\n", len(jobs))
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&out, "out", "o", "", "输出密档文件；输入为目录（或多个输入）时视为输出目录")
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
