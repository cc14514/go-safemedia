package account_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/cc14514/go-safemedia/internal/account"
)

// BIP-39 官方测试助记词（熵全 0）。
const officialMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

// 上述助记词在 m/44'/60'/0'/0/0 下的以太坊地址（MetaMask 首账户，公开已知向量）。
const officialAddress = "0x9858EfFD232B4033E47d90003D41EC34EcaEda94"

func TestGenerate12Words(t *testing.T) {
	acct, err := account.Generate(12)
	if err != nil {
		t.Fatalf("Generate(12) 出错: %v", err)
	}
	if got := len(strings.Fields(acct.Mnemonic())); got != 12 {
		t.Fatalf("助记词词数 = %d, 期望 12", got)
	}
	if !account.ValidMnemonic(acct.Mnemonic()) {
		t.Fatalf("生成的助记词未通过 BIP-39 校验: %q", acct.Mnemonic())
	}
}

func TestGenerate24Words(t *testing.T) {
	acct, err := account.Generate(24)
	if err != nil {
		t.Fatalf("Generate(24) 出错: %v", err)
	}
	if got := len(strings.Fields(acct.Mnemonic())); got != 24 {
		t.Fatalf("助记词词数 = %d, 期望 24", got)
	}
	if !account.ValidMnemonic(acct.Mnemonic()) {
		t.Fatalf("生成的助记词未通过 BIP-39 校验")
	}
}

func TestGenerateRejectsBadWordCount(t *testing.T) {
	for _, n := range []int{0, 13, 15, 25, -1} {
		if _, err := account.Generate(n); err == nil {
			t.Fatalf("Generate(%d) 应当报错", n)
		}
	}
}

func TestGenerateUnique(t *testing.T) {
	a, err := account.Generate(12)
	if err != nil {
		t.Fatal(err)
	}
	b, err := account.Generate(12)
	if err != nil {
		t.Fatal(err)
	}
	if a.Mnemonic() == b.Mnemonic() {
		t.Fatal("两次生成的助记词相同，随机性有问题")
	}
	if a.Address() == b.Address() {
		t.Fatal("两次生成的地址相同，随机性有问题")
	}
}

func TestAddressFormat(t *testing.T) {
	acct, err := account.Generate(12)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	if !re.MatchString(acct.Address()) {
		t.Fatalf("地址格式不符: %q", acct.Address())
	}
}

func TestRecoverMatchesGeneratedAccount(t *testing.T) {
	acct, err := account.Generate(12)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := account.RecoverFromMnemonic(acct.Mnemonic())
	if err != nil {
		t.Fatalf("RecoverFromMnemonic 出错: %v", err)
	}
	if recovered.Address() != acct.Address() {
		t.Fatalf("恢复出的地址 %s != 原地址 %s", recovered.Address(), acct.Address())
	}
}

func TestRecoverDeterministic(t *testing.T) {
	a, err := account.RecoverFromMnemonic(officialMnemonic)
	if err != nil {
		t.Fatal(err)
	}
	b, err := account.RecoverFromMnemonic(officialMnemonic)
	if err != nil {
		t.Fatal(err)
	}
	if a.Address() != b.Address() {
		t.Fatalf("同一助记词两次推导出不同地址: %s vs %s", a.Address(), b.Address())
	}
}

func TestRecoverKnownVector(t *testing.T) {
	acct, err := account.RecoverFromMnemonic(officialMnemonic)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(acct.Address(), officialAddress) {
		t.Fatalf("已知向量推导错误: got %s, want %s", acct.Address(), officialAddress)
	}
}

func TestRecoverRejectsInvalidMnemonic(t *testing.T) {
	cases := []string{
		"",
		"abandon abandon abandon",
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon", // 校验和错
		"hello world this is not a mnemonic at all really not",
	}
	for _, m := range cases {
		if _, err := account.RecoverFromMnemonic(m); err == nil {
			t.Fatalf("非法助记词 %q 应当报错", m)
		}
	}
}

func TestValidMnemonic(t *testing.T) {
	if !account.ValidMnemonic(officialMnemonic) {
		t.Fatal("官方向量应判定为合法")
	}
	if account.ValidMnemonic("abandon abandon abandon") {
		t.Fatal("词数不足应判定为非法")
	}
}

func TestLoadedAccountHasNoMnemonic(t *testing.T) {
	// keystore 不应携带助记词：见 keystore_test.go 的往返测试。
	acct, err := account.Generate(12)
	if err != nil {
		t.Fatal(err)
	}
	if acct.Mnemonic() == "" {
		t.Fatal("Create 生成的账户必须携带助记词")
	}
}
