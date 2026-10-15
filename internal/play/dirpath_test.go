package play

import (
	"os"
	"path/filepath"
	"testing"
)

// T7.4 的单元部分：normalizeDir 展开 ~、把相对路径规范为绝对路径，并拒绝非目录。
// （页面级的错误展示由 picker_test.go 的同名场景覆盖。）
func TestNormalizeDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("无法确定主目录，跳过 ~ 展开检查")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := normalizeDir("~")
	if err != nil {
		t.Fatalf("~ 应可展开: %v", err)
	}
	if filepath.Clean(got) != filepath.Clean(home) {
		t.Fatalf("~ 展开为 %q，期望 %q", got, home)
	}
	if got, err := normalizeDir("~/.safemedia"); err != nil {
		// 目录不一定存在：只要求展开到主目录之下，不存在时报「目录不可用」。
		if filepath.Dir(got) != "" && filepath.Clean(filepath.Dir(got)) != filepath.Clean(home) {
			t.Fatalf("~/... 未展开到主目录之下: %q", got)
		}
	}
	if got, err := normalizeDir(dir); err != nil || got != dir {
		t.Fatalf("合法目录 = %q, %v；期望 %q", got, err, dir)
	}
	if got, err := normalizeDir("."); err != nil || !filepath.IsAbs(got) {
		t.Fatalf("相对路径未规范为绝对路径: %q, %v", got, err)
	}
	if _, err := normalizeDir(file); err == nil {
		t.Fatal("文件不该被当作目录")
	}
	if _, err := normalizeDir("   "); err == nil {
		t.Fatal("空路径应报错")
	}
	if _, err := normalizeDir(filepath.Join(dir, "nope")); err == nil {
		t.Fatal("不存在的目录应报错")
	}
}
