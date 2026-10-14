package account

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
)

// MinPasswordLen 是登录/恢复口令的最短长度（FR-1.2）。
const MinPasswordLen = 10

// ErrPasswordTooShort 表示口令过短。
var ErrPasswordTooShort = errors.New("口令至少需要 10 个字符")

// commonWeak 是必须拒绝的常见弱口令片段（小写比较）。
var commonWeak = []string{
	"password", "passwd", "123456", "qwerty", "abc123", "letmein",
	"iloveyou", "admin", "safemedia", "111111", "000000",
}

// CheckPassword 校验口令强度：过短或含常见弱口令片段时返回错误。
// advice 始终给出可读建议（可能为空串）。
func CheckPassword(password string) (advice string, err error) {
	if len([]rune(password)) < MinPasswordLen {
		return fmt.Sprintf("建议使用一句好记的长口令（至少 %d 字符，≥16 字符更佳）", MinPasswordLen), ErrPasswordTooShort
	}
	lower := strings.ToLower(password)
	for _, w := range commonWeak {
		if strings.Contains(lower, w) {
			return "口令包含常见弱口令片段，请更换", errors.New("口令包含常见弱口令片段")
		}
	}
	if PasswordEntropyBits(password) < 50 {
		return "口令熵偏低：长度比复杂度更有效，建议加长口语句子", nil
	}
	return "", nil
}

// PasswordEntropyBits 给出粗略的口令熵估计（字符集大小 ^ 长度），用于 CLI 展示。
func PasswordEntropyBits(password string) float64 {
	if password == "" {
		return 0
	}
	var hasLower, hasUpper, hasDigit, hasSymbol bool
	for _, r := range password {
		switch {
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r) || unicode.IsSpace(r):
			hasSymbol = true
		}
	}
	alphabet := 0.0
	for _, pair := range []struct {
		has  bool
		size float64
	}{
		{hasLower, 26}, {hasUpper, 26}, {hasDigit, 10}, {hasSymbol, 33},
	} {
		if pair.has {
			alphabet += pair.size
		}
	}
	if alphabet <= 1 {
		return 0
	}
	return float64(len([]rune(password))) * math.Log2(alphabet)
}
