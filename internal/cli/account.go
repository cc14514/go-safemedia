package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cc14514/go-safemedia/internal/account"
)

func newAccountCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account",
		Short: "账户管理：新建、恢复、导出恢复文件",
	}
	cmd.AddCommand(newAccountCreateCmd())
	cmd.AddCommand(newAccountExportRecoveryCmd())
	cmd.AddCommand(newAccountRecoverCmd())
	return cmd
}

func newAccountCreateCmd() *cobra.Command {
	var words int
	cmd := &cobra.Command{
		Use:   "create",
		Short: "新建账户（生成助记词 + keystore v3 账户文件）",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, redlineWarning)
			fmt.Fprintln(out)

			path := AccountPath()
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("账户文件已存在：%s（如需重建请先备份并删除）", path)
			}

			passphrase, err := Prompt.ConfirmPassword("设置登录口令: ")
			if err != nil {
				return err
			}
			defer account.Wipe(passphrase)

			acct, err := account.Generate(words)
			if err != nil {
				return err
			}

			printMnemonic(out, acct.Mnemonic())
			if err := Prompt.ConfirmMnemonic(acct.Mnemonic()); err != nil {
				return err
			}

			if err := account.SaveKeystoreWithPassphrase(path, acct, passphrase, ScryptN); err != nil {
				return err
			}
			fmt.Fprintf(out, "\n✓ 账户已创建\n  地址:   %s\n  账户:   %s\n", acct.Address(), path)
			fmt.Fprintln(out, "  助记词是唯一可重建账户的东西，请离线抄写保存，切勿截图或联网存储。")
			return nil
		},
	}
	cmd.Flags().IntVar(&words, "words", 12, "助记词词数：12 或 24")
	return cmd
}

func newAccountExportRecoveryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export-recovery",
		Short: "导出恢复文件（独立恢复口令保护，用于找回登录口令）",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			path := AccountPath()
			loginPw, err := Prompt.ReadSecret("输入当前登录口令: ")
			if err != nil {
				return err
			}
			acct, err := account.LoadKeystoreWithPassphrase(path, loginPw)
			if err != nil {
				return err
			}

			recoveryPath := RecoveryPath()
			if _, err := os.Stat(recoveryPath); err == nil {
				return fmt.Errorf("恢复文件已存在：%s（如需重导请先备份并删除）", recoveryPath)
			}
			recoveryPw, err := Prompt.ConfirmPassword("设置恢复口令: ")
			if err != nil {
				return err
			}
			if err := account.ExportRecoveryKeystore(recoveryPath, acct, recoveryPw, ScryptN); err != nil {
				return err
			}
			fmt.Fprintf(out, "✓ 恢复文件已导出\n  路径: %s\n", recoveryPath)
			fmt.Fprintln(out, "  请把它与助名词分开存放：恢复口令 ≠ 登录口令。")
			return nil
		},
	}
	return cmd
}

func newAccountRecoverCmd() *cobra.Command {
	var fromMnemonic bool
	var fromRecovery string
	cmd := &cobra.Command{
		Use:   "recover",
		Short: "恢复账户：用助记词重建，或用恢复文件重置登录口令",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if fromMnemonic == (fromRecovery != "") {
				return fmt.Errorf("请二选一：--from-mnemonic 或 --from-recovery <path>")
			}

			var acct *account.Account
			var err error
			if fromMnemonic {
				mnemonic, e := Prompt.ReadMnemonic()
				if e != nil {
					return e
				}
				acct, err = account.RecoverFromMnemonic(mnemonic)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "✓ 助记词有效，重建出地址 %s\n", acct.Address())
			} else {
				recoveryPw, e := Prompt.ReadSecret("输入恢复口令: ")
				if e != nil {
					return e
				}
				acct, err = account.LoadKeystoreWithPassphrase(fromRecovery, recoveryPw)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "✓ 恢复文件已解锁，地址 %s\n", acct.Address())
			}

			path := AccountPath()
			if _, err := os.Stat(path); err == nil {
				backup := path + ".bak"
				if err := os.Rename(path, backup); err != nil {
					return fmt.Errorf("备份旧账户文件失败: %w", err)
				}
				fmt.Fprintf(out, "  旧账户文件已备份为 %s\n", backup)
			}
			newPw, err := Prompt.ConfirmPassword("设置新的登录口令: ")
			if err != nil {
				return err
			}
			if err := account.SaveKeystoreWithPassphrase(path, acct, newPw, ScryptN); err != nil {
				return err
			}
			fmt.Fprintf(out, "✓ 账户已恢复并写入 %s\n  地址: %s\n", path, acct.Address())
			fmt.Fprintln(out, "  原有密档无需重新封存：公钥未变，旧密档仍可启封。")
			return nil
		},
	}
	cmd.Flags().BoolVar(&fromMnemonic, "from-mnemonic", false, "用助记词重建账户")
	cmd.Flags().StringVar(&fromRecovery, "from-recovery", "", "用恢复文件重置登录口令")
	return cmd
}

// printMnemonic 以编号网格显示助记词（FR-1.1 强制显示）。
func printMnemonic(out interface{ Write([]byte) (int, error) }, mnemonic string) {
	words := strings.Fields(mnemonic)
	width := 4
	fmt.Fprintln(out, "以下助记词是重建账户的唯一凭证，请立刻用纸笔抄写并离线保存：")
	fmt.Fprintln(out)
	for i, w := range words {
		fmt.Fprintf(out, "%2d.%-14s", i+1, w)
		if (i+1)%width == 0 {
			fmt.Fprintln(out)
		}
	}
	if len(words)%width != 0 {
		fmt.Fprintln(out)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "⚠ 不要截图、不要存网盘、不要发给任何人。抄写完成后按提示复述抽查。")
}
