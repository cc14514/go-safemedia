package main

import (
	"github.com/cc14514/go-safemedia/internal/desktopcfg"
	"github.com/cc14514/go-safemedia/internal/play"
)

// playAccounts 把发现的本地账户转成引擎的登录页选项（US8）。
// 每次调用重新遍历（AC9.1）：账户管理页新建/删除后，登录页立刻反映。
func playAccounts(home string, cfg desktopcfg.Config) []play.AccountOption {
	found := desktopcfg.ListAccounts(home, cfg)
	out := make([]play.AccountOption, 0, len(found))
	for _, a := range found {
		out = append(out, play.AccountOption{ID: a.Path, Label: a.Name, Note: a.Short})
	}
	return out
}

// accountAdmin 把 desktopcfg 的账户管理动作适配成引擎的 AccountsAdmin 接口（US9）。
//
// 引擎只渲染页面并把动作透传过来；真正的文件操作（生成/校验/落盘/移入回收目录）
// 都在 desktopcfg 里，且删除成功后要顺手清理配置里指向已删账户的 account 字段。
type accountAdmin struct {
	home  string
	cfg   desktopcfg.Config
	inner *desktopcfg.AccountAdmin
}

func (a accountAdmin) DefaultTargetName() string { return a.inner.DefaultTargetName() }

func (a accountAdmin) Create(name, password string, words int) (play.CreateChallenge, error) {
	ch, err := a.inner.Create(name, password, words)
	if err != nil {
		return play.CreateChallenge{}, err
	}
	return play.CreateChallenge{
		Token:    ch.Token,
		Mnemonic: ch.Mnemonic,
		Indexes:  ch.Indexes,
		Path:     ch.Path,
	}, nil
}

func (a accountAdmin) ConfirmCreate(token string, answers []string) (play.AccountOption, error) {
	acct, err := a.inner.ConfirmCreate(token, answers)
	return toPlayAccount(acct), err
}

func (a accountAdmin) CancelCreate(token string) { a.inner.CancelCreate(token) }

func (a accountAdmin) RecoverFromMnemonic(name, mnemonic, password string) (play.AccountOption, error) {
	acct, err := a.inner.RecoverFromMnemonic(name, mnemonic, password)
	return toPlayAccount(acct), err
}

func (a accountAdmin) RecoverFromRecoveryFile(name, src, recoveryPassword, newPassword string) (play.AccountOption, error) {
	acct, err := a.inner.RecoverFromRecoveryFile(name, src, recoveryPassword, newPassword)
	return toPlayAccount(acct), err
}

// Delete 删除账户；若删掉的正是配置指定的账户，则清掉配置里的 account 字段
// （否则下次启动会拿一个不存在的账户当默认）。
func (a accountAdmin) Delete(path, password string) (play.DeleteResult, error) {
	res, err := a.inner.Delete(path, password)
	if err != nil {
		return play.DeleteResult{}, err
	}
	if res.WasConfigured {
		cfg := a.cfg
		cfg.Account = ""
		_ = desktopcfg.Save(a.home, cfg) // 尽力而为：失败也不影响删除结果
	}
	return play.DeleteResult{Trashed: res.Trashed, WasConfigured: res.WasConfigured}, nil
}

// toPlayAccount 转换账户描述（路径为空的失败场景返回零值，由调用方按 err 处理）。
func toPlayAccount(a desktopcfg.Account) play.AccountOption {
	return play.AccountOption{ID: a.Path, Label: a.Name, Note: a.Short}
}
