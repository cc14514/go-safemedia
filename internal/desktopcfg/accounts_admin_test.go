package desktopcfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cc14514/go-safemedia/internal/account"
)

// testAdmin 造一个低成本 scrypt 的管理器（生产用 account.DefaultScryptN）。
func testAdmin(t *testing.T, home string, cfg Config) *AccountAdmin {
	t.Helper()
	return NewAccountAdmin(AdminOptions{
		Home:    home,
		Config:  cfg,
		ScryptN: 1 << 10,
		Now:     time.Now,
	})
}

// answersFor 按抽查序号从助记词里取出正确复述（序号 1-based）。
func answersFor(c CreateChallenge) []string {
	words := strings.Fields(c.Mnemonic)
	out := make([]string, len(c.Indexes))
	for i, idx := range c.Indexes {
		out[i] = words[idx-1]
	}
	return out
}

// jsonFilesUnder 列出目录（含一层子目录）下的 *.json，用于断言「有没有写文件」。
func jsonFilesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("遍历 %s 失败: %v", dir, err)
	}
	return out
}

// T9.1（AC9.4）：账户名称校验。
func TestValidateAccountName(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"工作", "工作.json", false},
		{"工作.json", "工作.json", false},
		{"  工作  ", "工作.json", false},
		{"work-2.json", "work-2.json", false},
		{"", "", true},
		{"   ", "", true},
		{"../escape", "", true},
		{"a/b", "", true},
		{`a\b`, "", true},
		{".hidden", "", true},
		{"a\x01b", "", true},
		{strings.Repeat("a", 65), "", true},
	}
	for _, c := range cases {
		got, err := ValidateAccountName(c.in)
		if c.wantErr {
			if err == nil {
				t.Fatalf("名称 %q 应被拒绝，得到 %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("名称 %q 不该被拒绝: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("名称 %q → 期望 %q，得到 %q", c.in, c.want, got)
		}
	}
}

// T9.1（AC9.4）：首个账户写入默认账户路径，其余写入 accounts/ 子目录。
func TestAccountTargetPath(t *testing.T) {
	home := t.TempDir()
	a := testAdmin(t, home, Config{})

	if got := a.DefaultTargetName(); got != "keystore.json" {
		t.Fatalf("无账户时默认账户名应为 keystore.json，得到 %q", got)
	}
	p, err := a.TargetPath("")
	if err != nil {
		t.Fatal(err)
	}
	if p != DefaultAccount(home) {
		t.Fatalf("无账户时应写入默认账户路径，得到 %s", p)
	}

	// 已有一个账户后：新账户写入 accounts/，且默认名不再固定。
	writeKeystore(t, DefaultAccount(home))
	if got := a.DefaultTargetName(); got != "" {
		t.Fatalf("已有账户时不该固定名称，得到 %q", got)
	}
	p, err = a.TargetPath("工作")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(DefaultDir(home), "accounts", "工作.json")
	if p != want {
		t.Fatalf("目标路径应为 %s，得到 %s", want, p)
	}
}

// T9.1/T9.2（AC9.2/AC9.3）：创建分两步 —— Create 只持有待确认态、不写任何文件；
// 复述抽查通过后才写盘，且写入的 keystore 地址与生成的账户一致。
func TestCreateWritesOnlyAfterMnemonicConfirmed(t *testing.T) {
	home := t.TempDir()
	a := testAdmin(t, home, Config{})

	ch, err := a.Create("", "我在周二买了三斤香蕉和一把伞", 12)
	if err != nil {
		t.Fatalf("创建应成功: %v", err)
	}
	if len(ch.Indexes) != 3 {
		t.Fatalf("应抽查 3 个序号，得到 %v", ch.Indexes)
	}
	if len(strings.Fields(ch.Mnemonic)) != 12 {
		t.Fatalf("助记词应为 12 词，得到 %q", ch.Mnemonic)
	}
	if ch.Token == "" {
		t.Fatal("待确认态应有 token")
	}
	if files := jsonFilesUnder(t, DefaultDir(home)); len(files) != 0 {
		t.Fatalf("确认之前不得写任何文件，却出现 %v", files)
	}

	// 抽查答案错误 → 仍然不写盘，且实现明确报错。
	bad := answersFor(ch)
	bad[0] = "definitely-not-a-word"
	if _, err := a.ConfirmCreate(ch.Token, bad); err == nil {
		t.Fatal("抽查答案错误应报错")
	}
	if files := jsonFilesUnder(t, DefaultDir(home)); len(files) != 0 {
		t.Fatalf("抽查失败后仍不得写文件，却出现 %v", files)
	}
	if !strings.Contains(mustErr(t, func() error { _, e := a.ConfirmCreate(ch.Token, bad); return e }), "不正确") {
		t.Fatal("抽查失败应提示哪个词不正确")
	}

	// 抽查答案正确 → 写盘，且可用口令解锁、地址与展示一致。
	got, err := a.ConfirmCreate(ch.Token, answersFor(ch))
	if err != nil {
		t.Fatalf("抽查通过应写入账户: %v", err)
	}
	if got.Path != DefaultAccount(home) {
		t.Fatalf("首账户应写入默认账户，得到 %s", got.Path)
	}
	loaded, err := account.LoadKeystore(got.Path, "我在周二买了三斤香蕉和一把伞")
	if err != nil {
		t.Fatalf("写入的 keystore 应可用口令解锁: %v", err)
	}
	if loaded.Address() != got.Address {
		t.Fatalf("地址不一致: %s vs %s", loaded.Address(), got.Address)
	}
	if got.Name != "keystore.json" || !strings.HasPrefix(got.Short, "0x") {
		t.Fatalf("展示字段不对: %+v", got)
	}

	// 待确认态是一次性的：同一个 token 不能再用。
	if _, err := a.ConfirmCreate(ch.Token, answersFor(ch)); err == nil {
		t.Fatal("已消费的 token 应失效")
	}
}

// T9.2（AC9.3）：取消、口令不合格、尝试超限都不写文件。
func TestCreateAbortPathsWriteNothing(t *testing.T) {
	home := t.TempDir()

	// 口令不合格 → Create 直接拒绝，连待确认态都不建立。
	a := testAdmin(t, home, Config{})
	if _, err := a.Create("", "short", 12); err == nil {
		t.Fatal("弱口令应被拒绝")
	}
	if files := jsonFilesUnder(t, DefaultDir(home)); len(files) != 0 {
		t.Fatalf("弱口令不得写文件，却出现 %v", files)
	}

	// 取消 → 待确认态丢弃，之后确认无效。
	ch, err := a.Create("", "我在周二买了三斤香蕉和一把伞", 12)
	if err != nil {
		t.Fatal(err)
	}
	a.CancelCreate(ch.Token)
	if _, err := a.ConfirmCreate(ch.Token, answersFor(ch)); err == nil {
		t.Fatal("取消后不应还能确认")
	}

	// 尝试超限 → 待确认态失效（即使之后答案正确）。
	ch2, err := a.Create("", "我在周二买了三斤香蕉和一把伞", 12)
	if err != nil {
		t.Fatal(err)
	}
	bad := answersFor(ch2)
	bad[0] = "wrong"
	for i := 0; i < 5; i++ {
		_, _ = a.ConfirmCreate(ch2.Token, bad)
	}
	if _, err := a.ConfirmCreate(ch2.Token, answersFor(ch2)); err == nil {
		t.Fatal("尝试次数超限后应失效")
	}
	if files := jsonFilesUnder(t, DefaultDir(home)); len(files) != 0 {
		t.Fatalf("以上路径都不得写文件，却出现 %v", files)
	}
}

// T9.2（AC9.12）：待确认态会过期，过期后不写盘、不可确认。
func TestCreatePendingExpires(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	a := NewAccountAdmin(AdminOptions{Home: home, ScryptN: 1 << 10, Now: func() time.Time { return now }})

	ch, err := a.Create("", "我在周二买了三斤香蕉和一把伞", 12)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	if _, err := a.ConfirmCreate(ch.Token, answersFor(ch)); err == nil {
		t.Fatal("过期的待确认态应失效")
	}
	if files := jsonFilesUnder(t, DefaultDir(home)); len(files) != 0 {
		t.Fatalf("过期后不得写文件，却出现 %v", files)
	}
}

// T9.1（AC9.4）：目标已存在时拒绝创建，绝不覆盖。
func TestCreateRefusesExistingTarget(t *testing.T) {
	home := t.TempDir()
	a := testAdmin(t, home, Config{})
	addrBefore := writeKeystore(t, DefaultAccount(home))

	if _, err := a.Create("", "我在周二买了三斤香蕉和一把伞", 12); err == nil {
		t.Fatal("默认账户已存在时应拒绝创建")
	}
	ch, err := a.Create("work", "我在周二买了三斤香蕉和一把伞", 12)
	if err != nil {
		t.Fatalf("已有账户时创建新名称应允许: %v", err)
	}
	// 名称被待确认态占用时，第二次同名创建应被拒绝（避免两个待确认态抢同一路径）。
	if _, err := a.Create("work", "我在周二买了三斤香蕉和一把伞", 12); err == nil {
		t.Fatal("待确认态已占用该名称时应拒绝")
	}
	a.CancelCreate(ch.Token)
	loaded, err := account.LoadKeystore(DefaultAccount(home), "pw")
	if err != nil || loaded.Address() != addrBefore {
		t.Fatalf("原有账户不得被改动: %v", err)
	}
}

// T9.3（AC9.8/AC9.9）：删除必须口令授权；成功后移入回收目录；数据目录外的路径拒绝。
func TestDeleteAccountRequiresPasswordAndTrashes(t *testing.T) {
	home := t.TempDir()
	dir := DefaultDir(home)
	work := filepath.Join(dir, "accounts", "work.json")
	if err := os.MkdirAll(filepath.Dir(work), 0o700); err != nil {
		t.Fatal(err)
	}
	writeKeystore(t, work) // writeKeystore 用的口令是 "pw"

	cfg := Config{Account: work, Library: "/tmp/library"}
	a := testAdmin(t, home, cfg)

	// 未出现在发现列表的路径（隐藏文件不被扫描）→ 拒绝。
	hidden := filepath.Join(dir, ".hidden.json")
	writeKeystore(t, hidden)
	if _, err := a.Delete(hidden, "pw"); err == nil {
		t.Fatal("不在发现列表中的路径应被拒绝")
	}

	// 数据目录之外（即使被配置指定）→ 拒绝，绝不删。
	outside := filepath.Join(t.TempDir(), "extra.json")
	writeKeystore(t, outside)
	if _, err := a.Delete(outside, "pw"); err == nil {
		t.Fatal("数据目录外的账户应被拒绝（提示手动处理）")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("被拒绝的删除不得动文件: %v", err)
	}

	// 口令错误 → 拒绝，文件仍在。
	if _, err := a.Delete(work, "wrong-password"); err == nil {
		t.Fatal("口令错误应拒绝删除")
	}
	if _, err := os.Stat(work); err != nil {
		t.Fatalf("口令错误时不得动文件: %v", err)
	}

	// 口令正确 → 移入回收目录，原路径消失，且告知「是否就是配置指定的账户」。
	res, err := a.Delete(work, "pw")
	if err != nil {
		t.Fatalf("口令正确应删除: %v", err)
	}
	if !res.WasConfigured {
		t.Fatal("被删账户正是配置指定的账户，应如实告知以便清理配置")
	}
	if res.Trashed == "" {
		t.Fatal("应返回回收目录中的新路径")
	}
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Fatalf("原账户文件应已移走: %v", err)
	}
	if fi, err := os.Stat(res.Trashed); err != nil {
		t.Fatalf("回收目录里应有文件: %v", err)
	} else if fi.Size() == 0 {
		t.Fatal("回收文件不应为空")
	}
	if !strings.HasSuffix(filepath.Base(res.Trashed), "work.json") {
		t.Fatalf("回收文件名应保留原名，得到 %s", res.Trashed)
	}
	// 回收文件仍可用原口令解锁（证明是移走而不是损坏）。
	if _, err := account.LoadKeystore(res.Trashed, "pw"); err != nil {
		t.Fatalf("回收文件应可还原: %v", err)
	}

	// 删除后列表里不再有它。
	for _, a := range ListAccounts(home, cfg) {
		if a.Path == work {
			t.Fatalf("删除后不应再列出: %+v", a)
		}
	}
}

// T9.3（AC9.8）：回收目录里同名时加序号，不覆盖既有回收文件。
func TestDeleteTrashNameCollision(t *testing.T) {
	home := t.TempDir()
	dir := DefaultDir(home)
	trash := filepath.Join(dir, "trash")
	if err := os.MkdirAll(trash, 0o700); err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	a := NewAccountAdmin(AdminOptions{Home: home, ScryptN: 1 << 10, Now: func() time.Time { return fixed }})

	first := filepath.Join(dir, "one.json")
	writeKeystore(t, first)
	r1, err := a.Delete(first, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if r1.Trashed == "" {
		t.Fatal("应给出回收路径")
	}
	if _, err := os.Stat(r1.Trashed); err != nil {
		t.Fatalf("回收文件应存在: %v", err)
	}
	// 同名账户再来一次（同一时间戳）→ 不得覆盖既有的回收文件。
	writeKeystore(t, first)
	r2, err := a.Delete(first, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if r2.Trashed == r1.Trashed {
		t.Fatalf("同一时间戳下不该覆盖既有回收文件: %s", r2.Trashed)
	}
	if _, err := os.Stat(r1.Trashed); err != nil {
		t.Fatalf("既有回收文件应保留: %v", err)
	}
	if _, err := os.Stat(r2.Trashed); err != nil {
		t.Fatalf("第二次回收文件应存在: %v", err)
	}
}

// T9.4（AC9.6）：助记词恢复 —— 有效则写入（原文件备份为 .bak），无效则什么都不动。
func TestRecoverFromMnemonic(t *testing.T) {
	home := t.TempDir()
	oldAddr := writeKeystore(t, DefaultAccount(home))
	a := testAdmin(t, home, Config{})

	// 无效助记词 → 不动原文件、不留 .bak。
	if _, err := a.RecoverFromMnemonic("", "not a real mnemonic at all", "我在周二买了三斤香蕉和一把伞"); err == nil {
		t.Fatal("无效助记词应报错")
	}
	if _, err := os.Stat(DefaultAccount(home) + ".bak"); err == nil {
		t.Fatal("无效助记词不得备份原文件")
	}
	if loaded, err := account.LoadKeystore(DefaultAccount(home), "pw"); err != nil || loaded.Address() != oldAddr {
		t.Fatalf("无效助记词不得改动原账户: %v", err)
	}

	// 有效助记词 → 写入新账户（默认账户路径），原文件备份为 .bak。
	src, err := account.Generate(12)
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.RecoverFromMnemonic("", src.Mnemonic(), "我在周二买了三斤香蕉和一把伞")
	if err != nil {
		t.Fatalf("有效助记词应恢复: %v", err)
	}
	if got.Address != src.Address() {
		t.Fatalf("地址应为助记词推导的地址: %s vs %s", got.Address, src.Address())
	}
	loaded, err := account.LoadKeystore(DefaultAccount(home), "我在周二买了三斤香蕉和一把伞")
	if err != nil || loaded.Address() != src.Address() {
		t.Fatalf("新 keystore 应可用新口令解锁: %v", err)
	}
	backup, err := account.LoadKeystore(DefaultAccount(home)+".bak", "pw")
	if err != nil || backup.Address() != oldAddr {
		t.Fatalf("旧账户应备份为 .bak: %v", err)
	}

	// 指定名称 → 恢复成 accounts/ 下的另一个账户，不碰当前账户。
	other, err := account.Generate(12)
	if err != nil {
		t.Fatal(err)
	}
	got2, err := a.RecoverFromMnemonic("备用", other.Mnemonic(), "我在周二买了三斤香蕉和一把伞")
	if err != nil {
		t.Fatalf("按名称恢复应成功: %v", err)
	}
	wantPath := filepath.Join(DefaultDir(home), "accounts", "备用.json")
	if got2.Path != wantPath {
		t.Fatalf("应写入 accounts/ 子目录 %s，得到 %s", wantPath, got2.Path)
	}
	if _, err := account.LoadKeystore(DefaultAccount(home), "我在周二买了三斤香蕉和一把伞"); err != nil {
		t.Fatalf("按名称恢复不得动当前账户: %v", err)
	}
}

// T9.4（AC9.7）：用恢复文件重置登录口令；口令错则不写文件。
func TestRecoverFromRecoveryFile(t *testing.T) {
	home := t.TempDir()
	acct, err := account.Generate(12)
	if err != nil {
		t.Fatal(err)
	}
	if err := account.SaveKeystore(DefaultAccount(home), acct, "old-password-123", 1<<10); err != nil {
		t.Fatal(err)
	}
	recovery := filepath.Join(t.TempDir(), "recovery.json")
	if err := account.ExportRecoveryKeystore(recovery, acct, []byte("recovery-password"), 1<<10); err != nil {
		t.Fatal(err)
	}
	a := testAdmin(t, home, Config{})

	if _, err := a.RecoverFromRecoveryFile("", "~/nope.json", "recovery-password", "我在周二买了三斤香蕉和一把伞"); err == nil {
		t.Fatal("恢复文件不存在应报错")
	}
	if _, err := a.RecoverFromRecoveryFile("", recovery, "wrong-recovery", "我在周二买了三斤香蕉和一把伞"); err == nil {
		t.Fatal("恢复口令错误应报错")
	}
	if _, err := os.Stat(DefaultAccount(home) + ".bak"); err == nil {
		t.Fatal("失败路径不得备份原文件")
	}

	got, err := a.RecoverFromRecoveryFile("", recovery, "recovery-password", "我在周二买了三斤香蕉和一把伞")
	if err != nil {
		t.Fatalf("正确恢复口令应重置登录口令: %v", err)
	}
	if got.Address != acct.Address() {
		t.Fatalf("地址应不变: %s vs %s", got.Address, acct.Address())
	}
	if _, err := account.LoadKeystore(DefaultAccount(home), "我在周二买了三斤香蕉和一把伞"); err != nil {
		t.Fatalf("应可用新口令解锁: %v", err)
	}
	if _, err := account.LoadKeystore(DefaultAccount(home), "old-password-123"); err == nil {
		t.Fatal("旧口令应失效")
	}
}

// T9.1/T9.3（AC9.1/AC9.2/AC9.8）：创建 → 列表可见 → 删除 → 列表消失（无需重启）。
func TestCreateThenDeleteReflectedInList(t *testing.T) {
	home := t.TempDir()
	a := testAdmin(t, home, Config{})
	writeKeystore(t, DefaultAccount(home)) // 先有一个默认账户

	ch, err := a.Create("工作", "我在周二买了三斤香蕉和一把伞", 12)
	if err != nil {
		t.Fatal(err)
	}
	created, err := a.ConfirmCreate(ch.Token, answersFor(ch))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range ListAccounts(home, Config{}) {
		if x.Path == created.Path {
			found = true
		}
	}
	if !found {
		t.Fatalf("新建账户应立刻出现在列表中: %+v", created)
	}

	if _, err := a.Delete(created.Path, "我在周二买了三斤香蕉和一把伞"); err != nil {
		t.Fatalf("删除新建账户应成功: %v", err)
	}
	for _, x := range ListAccounts(home, Config{}) {
		if x.Path == created.Path {
			t.Fatalf("删除后不应再出现: %+v", x)
		}
	}
}

func mustErr(t *testing.T, f func() error) string {
	t.Helper()
	err := f()
	if err == nil {
		return ""
	}
	return err.Error()
}
