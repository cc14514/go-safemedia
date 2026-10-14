package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/cc14514/go-safemedia/internal/account"
)

var flagAccountPath string

// Prompt 是全局交互器，L2 测试可替换注入脚本化输入。
var Prompt = NewTermPrompter()

// ScryptN 是 keystore 的 scrypt 成本参数。生产值满足项目宪法（N >= 2^17）；
// L2 集成测试可临时调低以加速（只影响测试进程）。
var ScryptN = account.DefaultScryptN

// DefaultAccountDir 是账户文件默认目录（可用 --account 覆盖）。
func DefaultAccountDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".safemedia"
	}
	return filepath.Join(home, ".safemedia")
}

// AccountPath 返回主 keystore 路径。
func AccountPath() string {
	if flagAccountPath != "" {
		return flagAccountPath
	}
	return filepath.Join(DefaultAccountDir(), "keystore.json")
}

// RecoveryPath 返回恢复文件路径（与主 keystore 同目录）。
func RecoveryPath() string {
	return filepath.Join(filepath.Dir(AccountPath()), "recovery.json")
}

// Execute 运行 CLI，返回进程退出码（约定见 plan.md §7）。
func Execute() int {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		return ExitCodeOf(err)
	}
	return ExitOK
}

// NewRootCmd 返回根命令（供 L2 集成测试在进程内执行）。
func NewRootCmd() *cobra.Command { return newRootCmd() }

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "safemedia",
		Short:         "go-safemedia — 离线加密视频：封存、启封、播放",
		Long:          "go-safemedia 把视频封存为 .svid 密档（AES-256-GCM 分块加密），只有持有账户私钥的人能启封播放。完全离线运行。",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&flagAccountPath, "account", "",
		"账户 keystore 路径（默认 ~/.safemedia/keystore.json）")
	root.AddCommand(newAccountCmd())
	root.AddCommand(newSealCmd())
	root.AddCommand(newPlayCmd())
	root.AddCommand(newVersionCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "打印版本信息",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "go-safemedia v0.1.0-dev")
			return nil
		},
	}
}

// redlineWarning 是账户创建时必现的红线提示（项目宪法：只新建，不导入）。
const redlineWarning = `⚠ 红线提示：本账户仅供 go-safemedia 使用。
  绝不要把持有真实资产的钱包私钥/助记词导入本工具，也不要复用同一助记词。
  一处失窃，两处全完。请始终使用本命令新生成的账户。`
