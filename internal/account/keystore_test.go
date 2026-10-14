package account_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cc14514/go-safemedia/internal/account"
)

// testScryptN 是测试专用低成本参数（生产用 account.DefaultScryptN = 2^17）。
const testScryptN = 1 << 10

func newTestAccount(t *testing.T) *account.Account {
	t.Helper()
	acct, err := account.Generate(12)
	if err != nil {
		t.Fatalf("Generate 出错: %v", err)
	}
	return acct
}

func TestKeystoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keystore.json")
	acct := newTestAccount(t)

	if err := account.SaveKeystore(path, acct, "correct horse battery", testScryptN); err != nil {
		t.Fatalf("SaveKeystore 出错: %v", err)
	}
	loaded, err := account.LoadKeystore(path, "correct horse battery")
	if err != nil {
		t.Fatalf("LoadKeystore 出错: %v", err)
	}
	if loaded.Address() != acct.Address() {
		t.Fatalf("载入地址 %s != 原地址 %s", loaded.Address(), acct.Address())
	}
	if loaded.Mnemonic() != "" {
		t.Fatal("keystore 载入的账户不应携带助记词")
	}
	if loaded.PrivateKey() == nil {
		t.Fatal("载入的账户私钥为空")
	}
}

func TestKeystoreWrongPassword(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keystore.json")
	acct := newTestAccount(t)
	if err := account.SaveKeystore(path, acct, "right-password", testScryptN); err != nil {
		t.Fatal(err)
	}
	_, err := account.LoadKeystore(path, "wrong-password")
	if err != account.ErrWrongPassword {
		t.Fatalf("错误口令应返回 ErrWrongPassword, got %v", err)
	}
}

func TestKeystoreTamperDetected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keystore.json")
	acct := newTestAccount(t)
	if err := account.SaveKeystore(path, acct, "right-password", testScryptN); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// 篡改 ciphertext 的十六进制首字符，MAC 校验应当失败。
	marker := `"ciphertext": "`
	idx := strings.Index(string(raw), marker)
	if idx < 0 {
		t.Fatal("keystore 中找不到 ciphertext 字段")
	}
	pos := idx + len(marker)
	b := []byte(raw)
	switch b[pos] {
	case '0':
		b[pos] = '1'
	default:
		b[pos] = '0'
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = account.LoadKeystore(path, "right-password")
	if err != account.ErrWrongPassword {
		t.Fatalf("被篡改的 keystore 应返回 ErrWrongPassword, got %v", err)
	}
}

func TestKeystoreFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "keystore.json")
	acct := newTestAccount(t)
	if err := account.SaveKeystore(path, acct, "some-password", testScryptN); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("keystore 权限 = %o, 期望 600", perm)
	}
}

func TestSaveKeystoreRejectsEmptyPassword(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keystore.json")
	acct := newTestAccount(t)
	if err := account.SaveKeystore(path, acct, "", testScryptN); err == nil {
		t.Fatal("空口令应当报错")
	}
}

func TestKeystoreTwoFilesSameAccount(t *testing.T) {
	// 恢复文件与主文件是同一账户的两份封装，可用不同口令打开。
	dir := t.TempDir()
	main := filepath.Join(dir, "keystore.json")
	recovery := filepath.Join(dir, "recovery.json")
	acct := newTestAccount(t)

	if err := account.SaveKeystore(main, acct, "login-pass", testScryptN); err != nil {
		t.Fatal(err)
	}
	if err := account.SaveKeystore(recovery, acct, "recovery-pass", testScryptN); err != nil {
		t.Fatal(err)
	}

	a, err := account.LoadKeystore(main, "login-pass")
	if err != nil {
		t.Fatal(err)
	}
	b, err := account.LoadKeystore(recovery, "recovery-pass")
	if err != nil {
		t.Fatal(err)
	}
	if a.Address() != b.Address() {
		t.Fatalf("两份 keystore 地址不一致: %s vs %s", a.Address(), b.Address())
	}
	if _, err := account.LoadKeystore(recovery, "login-pass"); err != account.ErrWrongPassword {
		t.Fatalf("用登录口令打恢复文件应失败, got %v", err)
	}
}

func TestExportRecoveryKeystore(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "keystore.json")
	recovery := filepath.Join(dir, "recovery.json")
	acct := newTestAccount(t)

	if err := account.SaveKeystore(main, acct, "login-pass", testScryptN); err != nil {
		t.Fatal(err)
	}
	if err := account.ExportRecoveryKeystore(recovery, acct, []byte("recovery-pass"), testScryptN); err != nil {
		t.Fatal(err)
	}

	// 用恢复口令打开恢复文件 → 得到同一账户 → 用新登录口令重置主 keystore。
	recovered, err := account.LoadKeystoreWithPassphrase(recovery, []byte("recovery-pass"))
	if err != nil {
		t.Fatalf("恢复口令打不开恢复文件: %v", err)
	}
	if recovered.Address() != acct.Address() {
		t.Fatalf("恢复文件账户地址不一致: %s vs %s", recovered.Address(), acct.Address())
	}
	if err := account.SaveKeystore(main, recovered, "new-login-pass", testScryptN); err != nil {
		t.Fatal(err)
	}
	again, err := account.LoadKeystore(main, "new-login-pass")
	if err != nil {
		t.Fatalf("重置后的主 keystore 打不开: %v", err)
	}
	if again.Address() != acct.Address() {
		t.Fatal("重置登录口令后账户地址发生了变化")
	}
}

func TestPassphraseZeroedAfterUse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keystore.json")
	acct := newTestAccount(t)

	save := []byte("secret-login-pass")
	if err := account.SaveKeystoreWithPassphrase(path, acct, save, testScryptN); err != nil {
		t.Fatal(err)
	}
	if !allZero(save) {
		t.Fatal("SaveKeystoreWithPassphrase 用后未清零口令字节")
	}

	open := []byte("secret-login-pass")
	if _, err := account.LoadKeystoreWithPassphrase(path, open); err != nil {
		t.Fatal(err)
	}
	if !allZero(open) {
		t.Fatal("LoadKeystoreWithPassphrase 用后未清零口令字节")
	}
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}
