package cli

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/cc14514/go-safemedia/internal/account"
	"github.com/cc14514/go-safemedia/internal/vault"
)

// TestExitCodeOf 覆盖 plan.md §7 的退出码约定。
func TestExitCodeOf(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"成功", nil, ExitOK},
		{"篡改", vault.ErrTampered, ExitData},
		{"非密档", vault.ErrNotAVault, ExitData},
		{"版本不支持", fmt.Errorf("读头部: %w", vault.ErrUnsupportedVersion), ExitData},
		{"口令错", account.ErrWrongPassword, ExitUser},
		{"口令过短", account.ErrPasswordTooShort, ExitUser},
		{"不属于本账户", vault.ErrNotThisAccount, ExitUser},
		{"白名单外容器", vault.ErrUnsupportedContainer, ExitUser},
		{"文件不存在", os.ErrNotExist, ExitUser},
		{"环境错误：mpv 缺失", withExitCode(ExitEnv, errors.New("未检测到 mpv")), ExitEnv},
		{"包裹后的环境错误", fmt.Errorf("播放失败: %w", withExitCode(ExitEnv, errors.New("未检测到 mpv"))), ExitEnv},
		{"篡改优先于默认", fmt.Errorf("启封: %w", vault.ErrTampered), ExitData},
	}
	for _, c := range cases {
		if got := ExitCodeOf(c.err); got != c.want {
			t.Errorf("%s: ExitCodeOf=%d 期望 %d", c.name, got, c.want)
		}
	}
}
