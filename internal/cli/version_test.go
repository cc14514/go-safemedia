package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cc14514/go-safemedia/internal/version"
)

// T2.2：version 子命令输出 = internal/version 的注入值。
// 发布构建由 -ldflags "-X .../internal/version.Version=vX.Y.Z" 注入，
// 这里改写包变量验证输出确实取自该变量（而非硬编码）。
func TestVersionCommandReportsInjectedVersion(t *testing.T) {
	old := version.Version
	version.Version = "v9.9.9"
	defer func() { version.Version = old }()

	cmd := newVersionCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("version 命令失败: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "go-safemedia v9.9.9" {
		t.Fatalf("version 输出 = %q，期望 %q", got, "go-safemedia v9.9.9")
	}
}

// T2.3（AC5.3）：`--version` 与 `version` 两种写法输出一致（都会打注入值）。
// 曾只有子命令，`safemedia --version` 报 unknown flag —— 用户第一次想查版本时最可能敲的就是它。
func TestVersionFlagMatchesSubcommand(t *testing.T) {
	old := version.Version
	version.Version = "v9.9.9"
	defer func() { version.Version = old }()

	want := "go-safemedia v9.9.9"

	byFlag := newRootCmd()
	var fbuf bytes.Buffer
	byFlag.SetOut(&fbuf)
	byFlag.SetArgs([]string{"--version"})
	if err := byFlag.Execute(); err != nil {
		t.Fatalf("--version 失败: %v", err)
	}
	if got := strings.TrimSpace(fbuf.String()); got != want {
		t.Fatalf("--version 输出 = %q，期望 %q", got, want)
	}

	byCmd := newRootCmd()
	var cbuf bytes.Buffer
	byCmd.SetOut(&cbuf)
	byCmd.SetArgs([]string{"version"})
	if err := byCmd.Execute(); err != nil {
		t.Fatalf("version 子命令失败: %v", err)
	}
	if got := strings.TrimSpace(cbuf.String()); got != want {
		t.Fatalf("version 子命令输出 = %q，期望 %q", got, want)
	}
}
