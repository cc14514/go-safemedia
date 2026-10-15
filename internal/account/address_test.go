package account

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T8.1（AC8.1）：识别一个「账户」只需要 keystore 的公开地址字段 ——
// 读取不做 KDF、不需要口令；非 keystore（desktop.json、任意 JSON）必须被排除，
// 否则桌面登录页会把配置文件列成账户。
func TestReadKeystoreAddressAndShortAddress(t *testing.T) {
	dir := t.TempDir()

	acct, err := Generate(12)
	if err != nil {
		t.Fatalf("生成账户失败: %v", err)
	}
	ks := filepath.Join(dir, "keystore.json")
	if err := SaveKeystore(ks, acct, "pw", 1<<10); err != nil {
		t.Fatalf("写 keystore 失败: %v", err)
	}

	got, err := ReadKeystoreAddress(ks)
	if err != nil {
		t.Fatalf("读取地址失败: %v", err)
	}
	if !strings.EqualFold(got, acct.Address()) {
		t.Fatalf("地址 = %q，期望 %q", got, acct.Address())
	}
	if len(got) != 42 || !strings.HasPrefix(got, "0x") {
		t.Fatalf("地址应为 0x + 40 位十六进制，得到 %q", got)
	}
	if short := ShortAddress(got); short != got[:6]+"…"+got[38:] {
		t.Fatalf("短地址 = %q，期望 %q", short, got[:6]+"…"+got[38:])
	}
	if short := ShortAddress("不是地址"); short != "不是地址" {
		t.Fatalf("非法输入应原样返回，得到 %q", short)
	}

	// 桌面配置与「长得像 JSON 但不是 keystore」的文件都不是账户。
	cfg := filepath.Join(dir, "desktop.json")
	if err := os.WriteFile(cfg, []byte(`{"library":"/tmp/x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if addr, err := ReadKeystoreAddress(cfg); err == nil {
		t.Fatalf("desktop.json 被当成了账户: %q", addr)
	}
	junk := filepath.Join(dir, "junk.json")
	if err := os.WriteFile(junk, []byte(`{"address":"0xdeadbeef"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if addr, err := ReadKeystoreAddress(junk); err == nil {
		t.Fatalf("非法 address 被接受: %q", addr)
	}
	if _, err := ReadKeystoreAddress(filepath.Join(dir, "nope.json")); err == nil {
		t.Fatal("不存在的文件应当报错")
	}
}
