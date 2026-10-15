package cli

import (
	"fmt"
	"io"
	"io/fs"
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

// sealJob 是一次封存：源视频路径与目标密档路径。
type sealJob struct {
	src string
	dst string
}

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

			jobs, skipped, err := collectSealJobs(args, out)
			if err != nil {
				return err
			}
			if len(jobs) == 0 {
				return fmt.Errorf("没有找到可封存的文件（支持的容器：mp4 / mov / webm）")
			}
			if skipped > 0 {
				fmt.Fprintf(w, "跳过 %d 个非视频文件\n", skipped)
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
				if dir := filepath.Dir(j.dst); dir != "" && dir != "." {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						failed++
						fmt.Fprintf(w, "✗ %s: %v\n", j.dst, err)
						continue
					}
				}
				if !force {
					if _, err := os.Stat(j.dst); err == nil {
						failed++
						fmt.Fprintf(w, "✗ %s 已存在（用 --force 覆盖）\n", j.dst)
						continue
					}
				}

				fmt.Fprintf(w, "封存 %s\n   → %s\n", j.src, j.dst)
				res, err := vault.SealFileWithProgress(j.src, j.dst, pub, prog.update)
				prog.clear()
				if err != nil {
					failed++
					fmt.Fprintf(w, "✗ 失败：%v\n", err)
					fmt.Fprintln(w, "  若已产生未完成的 .svid，可安全删除后重试（其中不含明文）。")
					continue
				}
				ratio := ""
				if info, statErr := os.Stat(j.dst); statErr == nil && res.PlaintextSize > 0 {
					ratio = fmt.Sprintf("  +%.1f%%", float64(info.Size()-res.PlaintextSize)*100/float64(res.PlaintextSize))
				}
				fmt.Fprintf(w, "✓ %s 容器=%s 明文=%s 块数=%d%s\n",
					filepath.Base(j.dst), res.Container, humanSize(res.PlaintextSize), res.Chunks, ratio)
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

// collectSealJobs 把位置参数（文件或目录）展开为封存任务列表。
//
// -o 的含义：单个文件输入且 -o 不是已存在的目录时，视为密档文件路径；
// 其余情况（目录输入、多个输入、-o 本身是目录）一律视为输出目录。
// 目录输入递归遍历，非白名单/隐藏/符号链接/已有 .svid 跳过并计入 skipped。
func collectSealJobs(args []string, out string) ([]sealJob, int, error) {
	outIsDir := out != ""
	if outIsDir {
		if len(args) == 1 {
			if info, err := os.Stat(args[0]); err == nil && info.IsDir() {
				outIsDir = true
			} else if info, err := os.Stat(out); err != nil || !info.IsDir() {
				outIsDir = false // 单个文件 + -o 指向不存在的路径 → 密档文件
			}
		}
	}

	var jobs []sealJob
	seen := make(map[string]bool)
	skipped := 0

	add := func(j sealJob) {
		abs, err := filepath.Abs(j.src)
		if err != nil {
			abs = j.src
		}
		if seen[abs] {
			return
		}
		seen[abs] = true
		jobs = append(jobs, j)
	}

	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, 0, withExitCode(ExitUser,
				fmt.Errorf("%s: %w（若要指定输出位置，请用 -o）", arg, err))
		}

		if info.IsDir() {
			js, sk, err := walkSealDir(arg, out, outIsDir)
			if err != nil {
				return nil, 0, err
			}
			skipped += sk
			for _, j := range js {
				add(j)
			}
			continue
		}

		// 显式给出的文件：白名单不通过就直接报错（而不是静默跳过）。
		if _, _, err := vault.DetectFileContainer(arg); err != nil {
			return nil, 0, withExitCode(ExitUser, fmt.Errorf("%s: %w", arg, err))
		}
		add(sealJob{src: arg, dst: sealFileOutput(arg, out, outIsDir)})
	}

	sort.Slice(jobs, func(i, j int) bool { return jobs[i].src < jobs[j].src })
	return jobs, skipped, nil
}

// walkSealDir 递归遍历目录，收集可封存的视频文件。
func walkSealDir(root, out string, outIsDir bool) ([]sealJob, int, error) {
	var jobs []sealJob
	skipped := 0

	// 输出目录若位于输入目录之内，遍历时跳过它本身（避免把上一轮的密档再封一遍）。
	skipDirs := make(map[string]bool)
	if outIsDir && out != "" {
		if abs, err := filepath.Abs(out); err == nil {
			if info, err := os.Stat(abs); err == nil && info.IsDir() {
				skipDirs[abs] = true
			}
		}
	}

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != root && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil // 不跟随符号链接
		}
		if d.IsDir() {
			if abs, aerr := filepath.Abs(path); aerr == nil && skipDirs[abs] {
				return fs.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), VaultExt) {
			return nil
		}
		if _, _, err := vault.DetectFileContainer(path); err != nil {
			skipped++
			return nil
		}

		var dst string
		if outIsDir && out != "" {
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				rel = d.Name()
			}
			dst = filepath.Join(out, rel) + VaultExt
		} else {
			dst = path + VaultExt
		}
		jobs = append(jobs, sealJob{src: path, dst: dst})
		return nil
	})
	if walkErr != nil {
		return nil, skipped, walkErr
	}
	return jobs, skipped, nil
}

// sealFileOutput 计算单个文件的输出密档路径。
func sealFileOutput(src, out string, outIsDir bool) string {
	switch {
	case out == "":
		return src + VaultExt
	case outIsDir:
		return filepath.Join(out, filepath.Base(src)+VaultExt)
	default:
		return out
	}
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
