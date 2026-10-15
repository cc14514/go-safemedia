package integration

import (
	"path/filepath"
	"testing"

	"github.com/cc14514/go-safemedia/internal/desktopcfg"
)

// T8.13（AC8.1/AC8.5）：文档化的多账户流程 —— 第二个账户用 `--account` 建到 `accounts/` 子目录下，
// 该目录会被自动创建；登录页的账户来源（`desktopcfg.ListAccounts`）应能同时列出两个账户，
// 并把 `desktop.json` 指定的那个排在首位。
//
// 这条断言把「README/手册/说明.txt 里写的用法」和「登录页实际列出的账户」钉在一起，
// 避免文档写了一条走不通的路（用户按文档建第二个账户时才发现）。
func TestMultiAccountDiscoveryFromCLICreatedAccounts(t *testing.T) {
	home := t.TempDir()
	dataDir := desktopcfg.DefaultDir(home)
	mainKs := filepath.Join(dataDir, "keystore.json")
	workKs := filepath.Join(dataDir, "accounts", "工作.json") // 父目录此时并不存在

	// 默认账户（默认位置就在 ~/.safemedia 下）
	if out, err := runCLI(t, &script{secrets: []string{"first account passphrase", "first account passphrase"}},
		"--account", mainKs, "account", "create"); err != nil {
		t.Fatalf("建默认账户失败: %v\n%s", err, out)
	}
	// 第二个账户：放到 accounts/ 子目录（目录应由 keystore 保存时自动创建）
	if out, err := runCLI(t, &script{secrets: []string{"second account passphrase", "second account passphrase"}},
		"--account", workKs, "account", "create"); err != nil {
		t.Fatalf("建第二个账户失败: %v\n%s", err, out)
	}
	if !fileExists(workKs) {
		t.Fatalf("accounts/ 子目录未被自动创建：%s 不存在", workKs)
	}

	// 发现：两个账户都在列表里，名称=文件名，地址不同（确实是两个独立账户）
	found := desktopcfg.ListAccounts(home, desktopcfg.Config{})
	if len(found) != 2 {
		t.Fatalf("发现 %d 个账户，期望 2：%+v", len(found), found)
	}
	byPath := map[string]desktopcfg.Account{}
	for _, a := range found {
		byPath[a.Path] = a
	}
	for _, path := range []string{mainKs, workKs} {
		a, ok := byPath[path]
		if !ok {
			t.Fatalf("未发现账户 %s（发现：%+v）", path, found)
		}
		if a.Name != filepath.Base(path) {
			t.Fatalf("%s 的名称 = %q，期望文件名 %q", path, a.Name, filepath.Base(path))
		}
		if a.Address == "" {
			t.Fatalf("%s 未读到公开地址", path)
		}
	}
	if byPath[mainKs].Address == byPath[workKs].Address {
		t.Fatal("两个账户的地址相同，账户建重了")
	}

	// 配置指定的账户置首（登录页的默认选中项）
	cfg := desktopcfg.Config{Account: workKs}
	found = desktopcfg.ListAccounts(home, cfg)
	if len(found) != 2 || found[0].Path != workKs {
		t.Fatalf("配置指定的账户未置首：%+v", found)
	}
}
