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
