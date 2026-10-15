package desktopcfg

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cc14514/go-safemedia/internal/account"
)

// Account 是登录页可选的本地账户，只含公开信息（绝不含密钥材料）。
type Account struct {
	Path    string // keystore 绝对路径；作为不透明 ID 交给引擎
	Name    string // 文件名（登录页显示）
	Address string // 0x 开头的公开地址
	Short   string // 短形式（0x1a2b…9f8e）
}

// ListAccounts 发现本机可用账户，供登录页选择（FR-6.7 / AC8.1）：
//
//  1. cfg.Account 指定的路径（可在数据目录之外，如 CLI 的 --account）；
//  2. ~/.safemedia/*.json（默认 keystore 就在这里）；
//  3. ~/.safemedia/accounts/*.json（多账户归置用）。
//
// 只有能读出公开地址的 keystore 才算账户（desktop.json、任意 JSON、隐藏文件自动排除）；
// 按绝对路径去重；配置指定的账户排首位，其余按文件名排序（列表稳定）。
//
// 发现只在启动时做一次（Scope「不含」：运行中新增账户需重启），
// 且只读公开地址字段——不需要口令。空列表是合法状态（登录页据此提示先建账户，AC8.5）。
func ListAccounts(home string, cfg Config) []Account {
	var paths []string
	if p := strings.TrimSpace(cfg.Account); p != "" {
		paths = append(paths, expandHome(p, home))
	}
	dir := DefaultDir(home)
	paths = append(paths, scanJSONFiles(dir)...)
	paths = append(paths, scanJSONFiles(filepath.Join(dir, "accounts"))...)

	seen := make(map[string]bool, len(paths))
	out := make([]Account, 0, len(paths))
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		if seen[abs] {
			continue
		}
		addr, err := account.ReadKeystoreAddress(abs)
		if err != nil {
			continue // 不是 keystore → 不是账户
		}
		seen[abs] = true
		out = append(out, Account{
			Path:    abs,
			Name:    filepath.Base(abs),
			Address: addr,
			Short:   account.ShortAddress(addr),
		})
	}
	// 首个是配置指定的账户（若存在）；其余按文件名排序，避免列顺序随目录项变化。
	if len(out) > 1 {
		rest := out[1:]
		sort.SliceStable(rest, func(i, j int) bool { return rest[i].Name < rest[j].Name })
	}
	return out
}

// scanJSONFiles 列出 dir 顶层的 *.json（不含子目录、不含隐藏项）。目录不存在时返回 nil。
func scanJSONFiles(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	sort.Strings(out)
	return out
}
