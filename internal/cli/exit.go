package cli

import (
	"errors"

	"github.com/cc14514/go-safemedia/internal/vault"
)

// 进程退出码约定（plan.md §7）。
const (
	ExitOK   = 0 // 成功
	ExitUser = 1 // 用户/输入错误：口令错、白名单外、文件不存在、不属于本账户
	ExitData = 2 // 数据错误：非密档/头部损坏、密档被篡改
	ExitEnv  = 3 // 环境错误：mpv 缺失等
)

// codedError 让错误携带明确的退出码。
type codedError struct {
	code int
	err  error
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }
func (e *codedError) ExitCode() int { return e.code }

// withExitCode 给错误标注退出码（nil 原样返回）。
func withExitCode(code int, err error) error {
	if err == nil {
		return nil
	}
	return &codedError{code: code, err: err}
}

// ExitCodeOf 把错误映射为进程退出码。
func ExitCodeOf(err error) int {
	if err == nil {
		return ExitOK
	}
	// 显式标注优先。
	var coder interface{ ExitCode() int }
	if errors.As(err, &coder) {
		return coder.ExitCode()
	}
	switch {
	case errors.Is(err, vault.ErrTampered),
		errors.Is(err, vault.ErrNotAVault),
		errors.Is(err, vault.ErrUnsupportedVersion):
		return ExitData
	}
	// 其余（口令错、不属于本账户、白名单外、文件不存在…）按用户错误。
	return ExitUser
}
