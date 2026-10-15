package desktopcfg

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cc14514/go-safemedia/internal/account"
)

// maxCreateTries 是复述抽查的最大尝试次数（超过即作废本次创建）。
const maxCreateTries = 3

// createTTL 是待确认创建的存活时长（口令与助记词只在这段时间里留在内存）。
const createTTL = 5 * time.Minute

// AdminOptions 是账户管理动作的依赖（数据目录、配置、scrypt 强度）。
type AdminOptions struct {
	Home    string // 用户主目录（数据目录 = Home/.safemedia）
	Config  Config // 当前桌面配置（决定「配置指定的账户」与目标路径）
	ScryptN int    // keystore 的 scrypt 强度；0 → account.DefaultScryptN
	Now     func() time.Time
}

// CreateChallenge 是「生成账户、等待复述抽查」的中间态凭据。
type CreateChallenge struct {
	Token    string // 待确认凭据（确认/取消时回传）
	Mnemonic string // 助记词（由调用方在页面上按编号网格展示）
	Indexes  []int  // 抽查序号（1-based，顺序即提问顺序）
	Path     string // 确认后将写入的路径
}

// DeleteResult 是删除账户的结果。
type DeleteResult struct {
	Trashed       string // 回收目录中的新路径
	WasConfigured bool   // 被删的正是配置里指定的账户（调用方应清理配置）
}

// pending 是一次尚未确认的账户创建。
type pending struct {
	path     string
	acct     *account.Account
	mnemonic string
	indexes  []int
	pass     []byte
	tries    int
	expires  time.Time
}

// AccountAdmin 实现账户管理动作（创建 / 恢复 / 删除）。
//
// 与引擎解耦：这里只碰文件系统与 internal/account，不含 HTTP 或模板逻辑，
// 便于在 L1 直接覆盖（见 specs/002/tasks.md T9.1–T9.4）。
type AccountAdmin struct {
	opts AdminOptions

	mu      sync.Mutex
	pending map[string]*pending
}

// NewAccountAdmin 构造账户管理器。
func NewAccountAdmin(opts AdminOptions) *AccountAdmin {
	if opts.ScryptN == 0 {
		opts.ScryptN = account.DefaultScryptN
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &AccountAdmin{opts: opts, pending: map[string]*pending{}}
}

// DefaultTargetName 返回「创建时默认使用的账户名称」。
//
// 还没有任何账户时，第一个账户必须落在默认账户路径（AC9.4），名称固定为 keystore.json；
// 已有账户后名称由用户决定（空串表示交给页面填写）。
func (a *AccountAdmin) DefaultTargetName() string {
	if len(ListAccounts(a.opts.Home, a.opts.Config)) == 0 {
		return filepath.Base(DefaultAccount(a.opts.Home))
	}
	return ""
}

// TargetPath 计算新账户写入路径（AC9.4）：
//
//	无账户    → <数据目录>/keystore.json（默认账户）
//	已有账户  → <数据目录>/accounts/<名称>.json
func (a *AccountAdmin) TargetPath(name string) (string, error) {
	if len(ListAccounts(a.opts.Home, a.opts.Config)) == 0 {
		return DefaultAccount(a.opts.Home), nil
	}
	safe, err := ValidateAccountName(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(DefaultDir(a.opts.Home), "accounts", safe), nil
}

// EffectiveAccount 返回「当前配置的账户文件」：配置里指定了就用它，否则用默认账户。
func (a *AccountAdmin) EffectiveAccount() string {
	if p := strings.TrimSpace(a.opts.Config.Account); p != "" {
		return filepath.Clean(expandHome(p, a.opts.Home))
	}
	return DefaultAccount(a.opts.Home)
}

// RecoverTargetPath 计算「恢复写入路径」（AC9.6/AC9.7）：
//
//	名称为空 → 当前配置的账户（与 CLI `account recover` 写 `--account` 同口径）
//	有名称   → <数据目录>/accounts/<名称>.json（恢复出另一个账户，不覆盖当前账户）
func (a *AccountAdmin) RecoverTargetPath(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return a.EffectiveAccount(), nil
	}
	safe, err := ValidateAccountName(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(DefaultDir(a.opts.Home), "accounts", safe), nil
}

// ValidateAccountName 校验账户名称并规范化（自动补 .json）。
//
// 拒绝：空、路径分隔符、`..`、控制字符、以 `.` 开头（隐藏文件不参与账户发现）、过长。
func ValidateAccountName(name string) (string, error) {
	s := strings.TrimSpace(name)
	if s == "" {
		return "", fmt.Errorf("账户名称不能为空")
	}
	if strings.ContainsAny(s, `/\`) {
		return "", fmt.Errorf("账户名称不能包含路径分隔符")
	}
	if strings.Contains(s, "..") {
		return "", fmt.Errorf("账户名称不能包含 `..`")
	}
	if strings.HasPrefix(s, ".") {
		return "", fmt.Errorf("账户名称不能以 `.` 开头")
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("账户名称不能包含控制字符")
		}
	}
	if !strings.HasSuffix(strings.ToLower(s), ".json") {
		s += ".json"
	}
	if len(s) > 64 {
		return "", fmt.Errorf("账户名称过长（最多 64 字节）")
	}
	return s, nil
}

// Create 生成账户并返回复述抽查，**不写任何文件**（AC9.3）。
//
// 确认由 ConfirmCreate 完成，取消由 CancelCreate 完成；两者之外的状态会在 createTTL 后过期。
func (a *AccountAdmin) Create(targetName, password string, words int) (CreateChallenge, error) {
	if _, err := account.CheckPassword(password); err != nil {
		return CreateChallenge{}, fmt.Errorf("口令不合格：%w", err)
	}
	path, err := a.TargetPath(targetName)
	if err != nil {
		return CreateChallenge{}, err
	}
	if _, err := os.Stat(path); err == nil {
		return CreateChallenge{}, fmt.Errorf("账户文件已存在：%s（如需重建请先备份并删除）", path)
	}
	a.mu.Lock()
	for _, p := range a.pending {
		if p.path == path {
			a.mu.Unlock()
			return CreateChallenge{}, fmt.Errorf("名称 %s 正在创建中，请先完成或取消", filepath.Base(path))
		}
	}
	a.mu.Unlock()

	acct, err := account.Generate(words)
	if err != nil {
		return CreateChallenge{}, err
	}
	token, err := randomToken()
	if err != nil {
		return CreateChallenge{}, err
	}
	indexes, err := pickIndexes(words, 3)
	if err != nil {
		return CreateChallenge{}, err
	}
	a.mu.Lock()
	a.pending[token] = &pending{
		path:     path,
		acct:     acct,
		mnemonic: acct.Mnemonic(),
		indexes:  indexes,
		pass:     []byte(password),
		expires:  a.now().Add(createTTL),
	}
	a.mu.Unlock()

	return CreateChallenge{
		Token:    token,
		Mnemonic: acct.Mnemonic(),
		Indexes:  indexes,
		Path:     path,
	}, nil
}

// ConfirmCreate 校验复述抽查；全部正确才写 keystore（AC9.2/AC9.3）。
func (a *AccountAdmin) ConfirmCreate(token string, answers []string) (Account, error) {
	p := a.take(token)
	if p == nil {
		return Account{}, fmt.Errorf("创建会话已失效，请回到账户管理页重新开始")
	}
	words := strings.Fields(p.mnemonic)
	if len(answers) != len(p.indexes) {
		a.pending[token] = p // 参数个数不对不算一次尝试
		return Account{}, fmt.Errorf("请按提示复述 %d 个词", len(p.indexes))
	}
	for i, idx := range p.indexes {
		if !strings.EqualFold(strings.TrimSpace(answers[i]), words[idx-1]) {
			a.mu.Lock()
			p.tries++
			tooMany := p.tries >= maxCreateTries
			if tooMany {
				delete(a.pending, token)
				account.Wipe(p.pass)
			} else {
				a.pending[token] = p
			}
			a.mu.Unlock()
			if tooMany {
				return Account{}, fmt.Errorf("复述错误次数过多，本次创建已作废（未写入任何文件）")
			}
			return Account{}, fmt.Errorf("第 %d 个词（序号 %d）不正确，请核对后重试", i+1, idx)
		}
	}

	if err := account.SaveKeystoreWithPassphrase(p.path, p.acct, p.pass, a.opts.ScryptN); err != nil {
		a.mu.Lock()
		a.pending[token] = p
		a.mu.Unlock()
		return Account{}, err
	}
	addr := p.acct.Address()
	account.Wipe(p.pass)
	return Account{
		Path:    p.path,
		Name:    filepath.Base(p.path),
		Address: addr,
		Short:   account.ShortAddress(addr),
	}, nil
}

// CancelCreate 丢弃待确认创建（清零内存中的口令与助记词）。
func (a *AccountAdmin) CancelCreate(token string) {
	a.mu.Lock()
	p, ok := a.pending[token]
	delete(a.pending, token)
	a.mu.Unlock()
	if ok {
		account.Wipe(p.pass)
	}
}

// take 取出待确认态（过期即作废）。
func (a *AccountAdmin) take(token string) *pending {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.pending[token]
	if !ok {
		return nil
	}
	delete(a.pending, token)
	if a.now().After(p.expires) {
		account.Wipe(p.pass)
		return nil
	}
	return p
}

// RecoverFromMnemonic 用助记词重建账户（AC9.6）：先验助记词，再备份旧文件，最后写入。
func (a *AccountAdmin) RecoverFromMnemonic(name, mnemonic, password string) (Account, error) {
	if _, err := account.CheckPassword(password); err != nil {
		return Account{}, fmt.Errorf("口令不合格：%w", err)
	}
	acct, err := account.RecoverFromMnemonic(mnemonic)
	if err != nil {
		return Account{}, err
	}
	path, err := a.RecoverTargetPath(name)
	if err != nil {
		return Account{}, err
	}
	if err := backupExisting(path); err != nil {
		return Account{}, err
	}
	return a.saveRecovered(path, acct, password)
}

// RecoverFromRecoveryFile 用恢复文件重置登录口令（AC9.7）。
func (a *AccountAdmin) RecoverFromRecoveryFile(name, srcPath, recoveryPassword, newPassword string) (Account, error) {
	if _, err := account.CheckPassword(newPassword); err != nil {
		return Account{}, fmt.Errorf("口令不合格：%w", err)
	}
	src := expandHome(strings.TrimSpace(srcPath), a.opts.Home)
	if src == "" {
		return Account{}, fmt.Errorf("请填写恢复文件路径")
	}
	if info, err := os.Stat(src); err != nil {
		return Account{}, fmt.Errorf("恢复文件不可用：%w", err)
	} else if info.IsDir() {
		return Account{}, fmt.Errorf("恢复文件路径 %s 是目录", src)
	}
	pw := []byte(recoveryPassword)
	defer account.Wipe(pw)
	acct, err := account.LoadKeystoreWithPassphrase(src, pw)
	if err != nil {
		return Account{}, fmt.Errorf("恢复口令错误或恢复文件无效")
	}
	path, err := a.RecoverTargetPath(name)
	if err != nil {
		return Account{}, err
	}
	if err := backupExisting(path); err != nil {
		return Account{}, err
	}
	return a.saveRecovered(path, acct, newPassword)
}

// saveRecovered 写入恢复出的账户（口令字节用完即清零）。
func (a *AccountAdmin) saveRecovered(path string, acct *account.Account, password string) (Account, error) {
	pw := []byte(password)
	defer account.Wipe(pw)
	if err := account.SaveKeystoreWithPassphrase(path, acct, pw, a.opts.ScryptN); err != nil {
		return Account{}, err
	}
	addr := acct.Address()
	return Account{
		Path:    path,
		Name:    filepath.Base(path),
		Address: addr,
		Short:   account.ShortAddress(addr),
	}, nil
}

// Delete 在口令授权下把账户文件移入回收目录（AC9.8/AC9.9）。
//
// 口令错误、路径不在账户管理页的列表内、或路径落在数据目录之外时，一律不改动任何文件。
func (a *AccountAdmin) Delete(path, password string) (DeleteResult, error) {
	abs := filepath.Clean(expandHome(strings.TrimSpace(path), a.opts.Home))
	if abs == "" || path == "" {
		return DeleteResult{}, fmt.Errorf("缺少账户路径")
	}
	listed := false
	for _, cand := range ListAccounts(a.opts.Home, a.opts.Config) {
		if filepath.Clean(cand.Path) == abs {
			listed = true
			break
		}
	}
	if !listed {
		return DeleteResult{}, fmt.Errorf("只能删除账户管理页里列出的账户")
	}
	dir := DefaultDir(a.opts.Home)
	if abs != filepath.Clean(DefaultAccount(a.opts.Home)) &&
		!strings.HasPrefix(abs, dir+string(os.PathSeparator)) {
		return DeleteResult{}, fmt.Errorf("该账户文件不在 %s 内，请手动处理", dir)
	}

	pw := []byte(password)
	defer account.Wipe(pw)
	if _, err := account.LoadKeystoreWithPassphrase(abs, pw); err != nil {
		return DeleteResult{}, fmt.Errorf("口令错误，未删除任何文件")
	}

	trash := filepath.Join(dir, "trash")
	if err := os.MkdirAll(trash, 0o700); err != nil {
		return DeleteResult{}, fmt.Errorf("创建回收目录失败：%w", err)
	}
	target := filepath.Join(trash, a.now().Format("20060102-150405")+"-"+filepath.Base(abs))
	for i := 2; ; i++ {
		if _, err := os.Stat(target); os.IsNotExist(err) {
			break
		}
		target = filepath.Join(trash, fmt.Sprintf("%s-%s-%d", a.now().Format("20060102-150405"), filepath.Base(abs), i))
	}
	if err := os.Rename(abs, target); err != nil {
		return DeleteResult{}, fmt.Errorf("移入回收目录失败：%w", err)
	}
	wasConfigured := false
	if cfgAcct := strings.TrimSpace(a.opts.Config.Account); cfgAcct != "" {
		wasConfigured = filepath.Clean(expandHome(cfgAcct, a.opts.Home)) == abs
	}
	return DeleteResult{Trashed: target, WasConfigured: wasConfigured}, nil
}

func (a *AccountAdmin) now() time.Time { return a.opts.Now() }

// backupExisting 把已存在的账户文件改名为 <path>.bak（与 CLI 的恢复流程一致）。
func backupExisting(path string) error {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	if err := os.Rename(path, path+".bak"); err != nil {
		return fmt.Errorf("备份旧账户文件失败：%w", err)
	}
	return nil
}

// randomToken 生成待确认态凭据。
func randomToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// pickIndexes 随机挑 k 个互不相同的序号（1-based），用于复述抽查。
//
// 与 CLI 的同名逻辑各自实现：desktopcfg 不应依赖 internal/cli（那会拖进 cobra 与终端）。
func pickIndexes(n, k int) ([]int, error) {
	if n < k {
		k = n
	}
	perm := make([]int, 0, n)
	for i := 1; i <= n; i++ {
		perm = append(perm, i)
	}
	for i := 0; i < k; i++ {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		j := i + int(binary.BigEndian.Uint64(b[:])%uint64(n-i))
		perm[i], perm[j] = perm[j], perm[i]
	}
	out := append([]int(nil), perm[:k]...)
	sort.Ints(out)
	return out, nil
}
