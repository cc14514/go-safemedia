package desktopcfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cc14514/go-safemedia/internal/account"
)

// writeKeystore 造一个真实 keystore（测试用低成本 scrypt），返回其公开地址。
func writeKeystore(t *testing.T, path string) string {
	t.Helper()
	acct, err := account.Generate(12)
	if err != nil {
		t.Fatalf("生成账户失败: %v", err)
	}
	if err := account.SaveKeystore(path, acct, "pw", 1<<10); err != nil {
		t.Fatalf("写 keystore 失败: %v", err)
	}
	return acct.Address()
}

// T8.2（AC8.1）：账户发现规则 = 数据目录顶层 + accounts/ 子目录 + 配置指定路径；
// 只有 keystore 算账户；按绝对路径去重；配置指定的账户排首位。
func TestListAccountsDiscovery(t *testing.T) {
	home := t.TempDir()
	dir := DefaultDir(home)
	if err := os.MkdirAll(filepath.Join(dir, "accounts"), 0o700); err != nil {
		t.Fatal(err)
	}

	addrMain := writeKeystore(t, filepath.Join(dir, "keystore.json"))
	addrSub := writeKeystore(t, filepath.Join(dir, "accounts", "work.json"))

	// 非账户文件：桌面配置、随机 JSON、隐藏文件都必须被排除。
	for name, body := range map[string]string{
		"desktop.json": `{"library":"/tmp/x"}`,
		"random.json":  `{"hello":1}`,
		".hidden.json": `{"address":"0x` + strings.Repeat("a", 40) + `"}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// 数据目录之外的账户，由 cfg.Account 指定。
	outside := filepath.Join(t.TempDir(), "extra.json")
	addrOut := writeKeystore(t, outside)

	got := ListAccounts(home, Config{Account: outside})
	if len(got) != 3 {
		t.Fatalf("应发现 3 个账户，得到 %d 个: %+v", len(got), got)
	}
	if got[0].Path != outside {
		t.Fatalf("配置指定的账户应排首位，得到 %+v", got[0])
	}
	byAddr := make(map[string]Account, len(got))
	for _, a := range got {
		byAddr[a.Address] = a
	}
	for _, want := range []string{addrMain, addrSub, addrOut} {
		if _, ok := byAddr[want]; !ok {
			t.Fatalf("漏了账户 %s: %+v", want, got)
		}
	}
	if got[1].Name != "keystore.json" {
		t.Fatalf("名称应为文件名，得到 %+v", got[1])
	}
	if !strings.Contains(got[1].Short, "…") || !strings.HasPrefix(got[1].Short, "0x") {
		t.Fatalf("短地址形态不对: %+v", got[1])
	}
	if got[1].Short == got[1].Address {
		t.Fatalf("短地址不应等于完整地址: %+v", got[1])
	}

	// 去重：配置指向数据目录内已发现的账户时不得重复列出。
	if dup := ListAccounts(home, Config{Account: filepath.Join(dir, "keystore.json")}); len(dup) != 2 {
		t.Fatalf("应按绝对路径去重，得到 %d 个: %+v", len(dup), dup)
	}

	// 空数据目录 → 空列表（登录页据此提示先建账户，AC8.5）。
	if empty := ListAccounts(t.TempDir(), Config{}); len(empty) != 0 {
		t.Fatalf("空目录应返回空列表: %+v", empty)
	}
}
