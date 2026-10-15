package desktopcfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T3.2：目标解析的三级优先级（启动参数 > 配置 > 默认库）。
func TestResolvePriority(t *testing.T) {
	home := t.TempDir()
	needDir(t, filepath.Join(home, ".safemedia"))

	// 预置：一个库目录、一个密档文件、一个配置指定的库
	libCfg := filepath.Join(home, "lib-from-config")
	libArg := filepath.Join(home, "lib-from-args")
	fileArg := filepath.Join(home, "clip.svid")
	for _, d := range []string{libCfg, libArg} {
		needDir(t, d)
	}
	if err := os.WriteFile(fileArg, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	// ① 启动参数优先于配置
	got, err := Resolve([]string{fileArg}, Config{Library: libCfg}, home)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindFile || got.Path != fileArg {
		t.Fatalf("启动参数为文件时 = %+v，期望 file/%s", got, fileArg)
	}
	got, err = Resolve([]string{libArg}, Config{Library: libCfg}, home)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindLibrary || got.Path != libArg {
		t.Fatalf("启动参数为目录时 = %+v，期望 library/%s", got, libArg)
	}

	// ② 无参数时用配置里的库
	got, err = Resolve(nil, Config{Library: libCfg}, home)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindLibrary || got.Path != libCfg {
		t.Fatalf("配置库 = %+v，期望 library/%s", got, libCfg)
	}

	// ③ 无参数无配置时用默认库，且默认库会被创建
	got, err = Resolve(nil, Config{}, home)
	if err != nil {
		t.Fatal(err)
	}
	wantDefault := DefaultLibrary(home)
	if got.Kind != KindLibrary || got.Path != wantDefault {
		t.Fatalf("默认库 = %+v，期望 library/%s", got, wantDefault)
	}
	if info, err := os.Stat(wantDefault); err != nil || !info.IsDir() {
		t.Fatalf("默认库未被创建: err=%v", err)
	}

	// 账户默认路径
	if got.Account != DefaultAccount(home) {
		t.Fatalf("默认账户 = %q，期望 %q", got.Account, DefaultAccount(home))
	}
}

// T3.2 续：启动参数指向不存在的路径必须报错（不能静默退回默认库，否则用户以为拖进去的文件在放）。
func TestResolveRejectsMissingArgPath(t *testing.T) {
	home := t.TempDir()
	_, err := Resolve([]string{filepath.Join(home, "nope")}, Config{}, home)
	if err == nil {
		t.Fatal("不存在的启动参数应当报错")
	}
}

// T3.2 续：配置里的库路径支持 ~ 展开，账户路径独立于库路径。
func TestResolveExpandsTildeAndUsesConfigAccount(t *testing.T) {
	home := t.TempDir()
	sub := filepath.Join(home, "videos")
	needDir(t, sub)

	acct := filepath.Join(home, "keys", "my.json")
	if err := os.MkdirAll(filepath.Dir(acct), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Library: "~/videos", Account: acct}
	got, err := Resolve(nil, cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != sub {
		t.Fatalf("~ 未展开: %q，期望 %q", got.Path, sub)
	}
	if got.Account != acct {
		t.Fatalf("账户路径 = %q，期望 %q", got.Account, acct)
	}

	// 启动参数里的 ~ 同样展开
	f := filepath.Join(sub, "a.svid")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = Resolve([]string{"~/videos/a.svid"}, Config{}, home)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindFile || got.Path != f {
		t.Fatalf("参数 ~ 未展开: %+v", got)
	}
}

// T3.3：配置读写的三态（缺省 / 合法 / 损坏）。
func TestConfigLoadThreeStates(t *testing.T) {
	home := t.TempDir()

	// 缺省：不报错，返回零值
	cfg, err := Load(home)
	if err != nil {
		t.Fatalf("无配置文件不应报错: %v", err)
	}
	if cfg.Library != "" || cfg.Account != "" {
		t.Fatalf("无配置文件应为零值: %+v", cfg)
	}

	// 合法：往返一致
	want := Config{Library: "/tmp/lib", Account: "/tmp/acct.json"}
	if err := Save(home, want); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg != want {
		t.Fatalf("往返 = %+v，期望 %+v", cfg, want)
	}
	if info, err := os.Stat(configPath(home)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("配置权限应为 0600: err=%v mode=%v", err, info.Mode().Perm())
	}

	// 损坏：必须报错（不得静默退回默认值）
	if err := os.WriteFile(configPath(home), []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(home); err == nil {
		t.Fatal("损坏的配置应当报错")
	}
}

// 已知字段名稳定（配置文件是用户可手写的契约）。
func TestConfigJSONFieldNames(t *testing.T) {
	b, err := json.Marshal(Config{Library: "/l", Account: "/a"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{`"library":"/l"`, `"account":"/a"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("配置字段名变化: %s（缺少 %s）", got, want)
		}
	}
}

func needDir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
}
