package account_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cc14514/go-safemedia/internal/account"
)

func TestCheckPasswordTooShort(t *testing.T) {
	for _, pw := range []string{"", "abc", "short", "123456789"} {
		if _, err := account.CheckPassword(pw); !errors.Is(err, account.ErrPasswordTooShort) {
			t.Fatalf("口令 %q 应报 ErrPasswordTooShort, got %v", pw, err)
		}
	}
}

func TestCheckPasswordWeakPatterns(t *testing.T) {
	for _, pw := range []string{"password123", "MyQwerty99!", "admin-12345678"} {
		advice, err := account.CheckPassword(pw)
		if err == nil {
			t.Fatalf("弱口令 %q 应被拒绝", pw)
		}
		if advice == "" {
			t.Fatalf("拒绝 %q 时应给出建议", pw)
		}
	}
}

func TestCheckPasswordAcceptsPassphrase(t *testing.T) {
	good := []string{
		"correct horse battery staple",
		"我家的猫今天又打翻了花盆-2026",
		"Tr0ub4dor&3xtra-Long-Phrase",
	}
	for _, pw := range good {
		advice, err := account.CheckPassword(pw)
		if err != nil {
			t.Fatalf("口令 %q 应通过, got err=%v", pw, err)
		}
		if advice != "" {
			t.Fatalf("强口令不应有建议, got %q", advice)
		}
	}
}

func TestPasswordEntropyMonotonic(t *testing.T) {
	if account.PasswordEntropyBits("") != 0 {
		t.Fatal("空口令熵应为 0")
	}
	short := account.PasswordEntropyBits("abcdefgh")
	long := account.PasswordEntropyBits("abcdefghijklmnop")
	if long <= short {
		t.Fatalf("更长口令熵应更高: %f vs %f", long, short)
	}
	mixed := account.PasswordEntropyBits("aB3!aB3!aB3!aB3!")
	if mixed <= short {
		t.Fatalf("混合字符集熵应更高: %f vs %f", mixed, short)
	}
}

func TestMinPasswordLenConstant(t *testing.T) {
	pw := strings.Repeat("a", account.MinPasswordLen)
	if _, err := account.CheckPassword(pw); err != nil {
		t.Fatalf("恰好 %d 字符的口令不应因长度被拒: %v", account.MinPasswordLen, err)
	}
}
