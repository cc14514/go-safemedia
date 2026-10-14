package cli

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/cc14514/go-safemedia/internal/play"
	"github.com/cc14514/go-safemedia/internal/vault"
)

func newPlayCmd() *cobra.Command {
	var player string
	var noBrowser bool
	var idle time.Duration

	cmd := &cobra.Command{
		Use:   "play <vault|dir>",
		Short: "启封播放密档或密档目录（本机环回解密）",
		Long: `启封 .svid 密档并播放。

  safemedia play clip.svid              浏览器播放单个密档
  safemedia play ~/vaults/              浏览器打开目录索引，点选播放
  safemedia play clip.svid --player mpv 交给 mpv 播放（HEVC 等全格式逃生通道）

解密流只经 127.0.0.1 环回端口送出，明文不落盘；
空闲超过 --idle 会自动锁定并清零内存中的密钥，Ctrl-C 亦立即锁定。`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			target := args[0]

			info, err := os.Stat(target)
			if err != nil {
				return err
			}
			switch player {
			case "browser", "mpv":
			default:
				return fmt.Errorf("未知播放器 %q：只支持 browser 或 mpv", player)
			}

			// AC4.3：口令错误 → 拒绝，且不启动任何服务。
			acct, err := unlockAccount()
			if err != nil {
				return err
			}

			if info.IsDir() {
				return playDirectory(out, target, acct.PrivateKey(), player, noBrowser, idle)
			}
			return playSingleFile(out, target, acct.PrivateKey(), player, noBrowser, idle)
		},
	}

	cmd.Flags().StringVar(&player, "player", "browser", "播放器：browser 或 mpv")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "不自动打开浏览器（仅打印地址）")
	cmd.Flags().DurationVar(&idle, "idle", play.DefaultIdleTimeout, "空闲自动锁定时长")
	return cmd
}

// playSingleFile 播放单个密档。
func playSingleFile(out io.Writer, path string, priv *ecdsa.PrivateKey, player string, noBrowser bool, idle time.Duration) error {
	// AC6.2：mpv 缺失必须在使用前就明确提示，不启动任何服务/浏览器流程。
	if player == "mpv" {
		if _, err := mpvPath(); err != nil {
			return withExitCode(ExitEnv, err)
		}
	}
	// AC4.6：非密档/头部损坏 → 报「不是密档文件」；AC4.4：篡改 → 明确报错。
	r, err := vault.Open(path, priv)
	if err != nil {
		return err
	}
	defer r.Close()

	srv, err := play.New(play.Options{
		Reader:      r,
		Name:        filepath.Base(path),
		IdleTimeout: idle,
		OnLock:      lockReporter(out),
	})
	if err != nil {
		return err
	}
	url, err := srv.Start()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "▶ 播放服务已就绪（仅本机）：%s\n", url)
	fmt.Fprintf(out, "  容器=%s 明文=%s\n", r.ContainerHintName(), humanSize(r.Size()))

	if player == "mpv" {
		media := url + strings.TrimPrefix(srv.MediaPath(), "/")
		fmt.Fprintf(out, "  交给 mpv 播放：%s\n", media)
		if err := runMpv(media); err != nil {
			_ = srv.Close()
			return err
		}
		fmt.Fprintln(out, "mpv 已退出，正在清零密钥并关闭服务…")
		return srv.Close()
	}

	if !noBrowser {
		if err := openBrowser(url); err != nil {
			fmt.Fprintf(out, "  ⚠ 无法自动打开浏览器（%v），请手动访问上面的地址。\n", err)
		}
	} else {
		fmt.Fprintf(out, "  播放页：%s\n", url)
	}
	fmt.Fprintln(out, "  播放中按 Ctrl-C 结束；密钥会立即从内存清零。")
	return waitUntilLocked(out, srv.Locked, srv.Close)
}

// playDirectory 以目录索引方式播放。
func playDirectory(out io.Writer, dir string, priv *ecdsa.PrivateKey, player string, noBrowser bool, idle time.Duration) error {
	if player == "mpv" {
		return fmt.Errorf("mpv 模式请指定单个密档文件；目录索引请用浏览器模式")
	}
	entries, err := play.ScanLibrary(dir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Fprintf(out, "⚠ %s 下没有 .svid 密档，索引页将显示空列表提示。\n", dir)
	}

	lib, err := play.NewLibrary(play.LibraryOptions{
		Priv:        priv,
		Entries:     entries,
		IdleTimeout: idle,
		OnLock:      lockReporter(out),
	})
	if err != nil {
		return err
	}
	url, err := lib.Start()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "▶ 密档库索引已就绪（仅本机）：%s\n  共 %d 个密档\n", url, lib.Count())

	if !noBrowser {
		if err := openBrowser(url); err != nil {
			fmt.Fprintf(out, "  ⚠ 无法自动打开浏览器（%v），请手动访问上面的地址。\n", err)
		}
	} else {
		fmt.Fprintf(out, "  索引页：%s\n", url)
	}
	fmt.Fprintln(out, "  点选条目播放；按 Ctrl-C 结束并清零密钥。")
	return waitUntilLocked(out, lib.Locked, lib.Close)
}

// waitUntilLocked 阻塞至锁定（空闲/手动/POST /lock）或用户中断。
func waitUntilLocked(out io.Writer, locked func() bool, closeFn func() error) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(out, "\n收到中断，正在清零密钥并关闭服务…")
			return closeFn()
		case <-ticker.C:
			if locked() {
				return nil
			}
		}
	}
}

func lockReporter(out io.Writer) func(string) {
	return func(reason string) {
		fmt.Fprintf(out, "\n🔒 %s\n", reason)
	}
}

// mpvPath 查找本机 mpv（AC6.2：未安装则明确提示）。
func mpvPath() (string, error) {
	p, err := exec.LookPath("mpv")
	if err != nil {
		return "", fmt.Errorf("未检测到 mpv：请先安装 mpv，或改用浏览器模式（去掉 --player mpv）")
	}
	return p, nil
}

// runMpv 用 mpv 播放环回解密流（HEVC 等浏览器不支持格式的逃生通道）。
func runMpv(mediaURL string) error {
	bin, err := mpvPath()
	if err != nil {
		return withExitCode(ExitEnv, err)
	}
	cmd := exec.Command(bin, "--force-window=yes", mediaURL)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mpv 播放失败: %w", err)
	}
	return nil
}

// openBrowser 用系统默认浏览器打开本地播放页。
func openBrowser(url string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	case "linux", "freebsd", "openbsd", "netbsd":
		name = "xdg-open"
	default:
		return fmt.Errorf("不支持的平台 %s", runtime.GOOS)
	}
	args = append(args, url)
	return exec.Command(name, args...).Start()
}
