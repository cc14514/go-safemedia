package version

import (
	"strings"
	"testing"
)

// 默认版本必须显式带 dev 标记：有人拿着自建包排查时，一眼能看出不是发布包。
func TestDefaultVersionIsDevMarked(t *testing.T) {
	if Version == "" {
		t.Fatal("默认版本不得为空")
	}
	if !strings.Contains(Version, "dev") {
		t.Fatalf("默认版本 %q 未带 dev 标记", Version)
	}
}

// String 必须取自变量（发布构建靠 -ldflags 覆盖它）。
func TestStringUsesInjectedVersion(t *testing.T) {
	old := Version
	Version = "v9.9.9"
	defer func() { Version = old }()
	if got := String(); got != "go-safemedia v9.9.9" {
		t.Fatalf("String() = %q", got)
	}
}
