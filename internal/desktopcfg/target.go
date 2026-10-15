// Package desktopcfg 解析桌面外壳的播放目标与本地配置。
//
// 与引擎（internal/play）解耦：这里只做路径决策，不含任何解密或 HTTP 逻辑，
// 便于在 L1 直接覆盖（见 specs/002/tasks.md T3.2/T3.3）。
package desktopcfg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Kind 是播放目标的类型。
type Kind string

const (
	// KindFile 表示单个 .svid 密档。
	KindFile Kind = "file"
	// KindLibrary 表示密档库目录（递归扫描子目录）。
	KindLibrary Kind = "library"
)

// Config 是 ~/.safemedia/desktop.json 的内容。
type Config struct {
	// Library 是默认密档库目录；空 = $HOME/.safemedia/library。
	Library string `json:"library,omitempty"`
	// Account 是账户 keystore 路径；空 = $HOME/.safemedia/keystore.json。
	Account string `json:"account,omitempty"`
}

// Target 是本次启动要播放的目标。
type Target struct {
	Kind    Kind
	Path    string
	Account string
}

// DefaultDir 返回 go-safemedia 的默认数据目录（~/.safemedia）。
func DefaultDir(home string) string { return filepath.Join(home, ".safemedia") }

// DefaultAccount 返回默认 keystore 路径。
func DefaultAccount(home string) string { return filepath.Join(DefaultDir(home), "keystore.json") }

// DefaultLibrary 返回默认密档库目录。
func DefaultLibrary(home string) string { return filepath.Join(DefaultDir(home), "library") }

// configPath 返回配置文件路径。
func configPath(home string) string { return filepath.Join(DefaultDir(home), "desktop.json") }

// Load 读取桌面外壳配置。
//
// 文件不存在时返回零值配置且不报错（首次运行属正常情况）；
// 文件存在但无法解析时返回错误——静默忽略配置会让人以为「改了配置没生效」。
func Load(home string) (Config, error) {
	var cfg Config
	b, err := os.ReadFile(configPath(home))
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("读取桌面配置失败: %w", err)
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("桌面配置 %s 不是合法 JSON: %w", configPath(home), err)
	}
	return cfg, nil
}

// Save 写入桌面外壳配置（供将来在 UI 里改库路径使用）。
func Save(home string, cfg Config) error {
	if err := os.MkdirAll(DefaultDir(home), 0o700); err != nil {
		return fmt.Errorf("创建数据目录失败: %w", err)
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(home), append(b, '\n'), 0o600)
}

// Resolve 计算本次要播放的目标。优先级：
//
//	启动参数（文件或目录） > 配置里的 library > 默认库 $HOME/.safemedia/library
//
// 解析出的库目录不存在时会创建（空库是合法状态，索引页会给出提示）。
func Resolve(args []string, cfg Config, home string) (Target, error) {
	acct := strings.TrimSpace(cfg.Account)
	if acct == "" {
		acct = DefaultAccount(home)
	}
	acct = expandHome(acct, home)

	// ① 启动参数：macOS 下由 `open -a ... --args <path>` 或直接运行可执行文件传入。
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		p := expandHome(strings.TrimSpace(args[0]), home)
		info, err := os.Stat(p)
		if err != nil {
			return Target{}, fmt.Errorf("启动参数指定的路径不可用: %w", err)
		}
		if info.IsDir() {
			return Target{Kind: KindLibrary, Path: p, Account: acct}, nil
		}
		return Target{Kind: KindFile, Path: p, Account: acct}, nil
	}

	// ② 配置里的库目录
	if lib := strings.TrimSpace(cfg.Library); lib != "" {
		p := expandHome(lib, home)
		if err := ensureLibraryDir(p); err != nil {
			return Target{}, err
		}
		return Target{Kind: KindLibrary, Path: p, Account: acct}, nil
	}

	// ③ 默认库
	p := DefaultLibrary(home)
	if err := ensureLibraryDir(p); err != nil {
		return Target{}, err
	}
	return Target{Kind: KindLibrary, Path: p, Account: acct}, nil
}

// ensureLibraryDir 确保库目录存在（空库是合法的：索引页会提示「没有密档」）。
func ensureLibraryDir(p string) error {
	if p == "" {
		return fmt.Errorf("密档库路径为空")
	}
	if info, err := os.Stat(p); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("密档库路径 %s 不是目录", p)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("密档库路径不可用: %w", err)
	}
	if err := os.MkdirAll(p, 0o700); err != nil {
		return fmt.Errorf("创建密档库 %s 失败: %w", p, err)
	}
	return nil
}

// expandHome 展开开头的 ~（macOS 拖放/手写路径常见）。
func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}
