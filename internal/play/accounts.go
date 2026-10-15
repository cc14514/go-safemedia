package play

// AccountOption 是登录页可选的账户（FR-6.7 / US8）。
//
// 引擎**不发现**账户：它不遍历文件系统，只渲染调用方注入的列表，并校验
// 「提交的标识是否在列表内」（校验发生在解锁回调之前）。ID 对引擎是不透明的 ——
// 桌面外壳传的是 keystore 绝对路径，CLI/测试路径可以传空串。
type AccountOption struct {
	ID    string // 不透明标识；提交时原样回传给解锁回调
	Label string // 显示名（通常为 keystore 文件名）
	Note  string // 附加说明（通常为地址短形式）；可空
}

// findAccount 在列表中查找 id（空 id 永不匹配：列表非空时必须显式选择）。
func findAccount(list []AccountOption, id string) (AccountOption, bool) {
	if id == "" {
		return AccountOption{}, false
	}
	for _, a := range list {
		if a.ID == id {
			return a, true
		}
	}
	return AccountOption{}, false
}

// AccountsAdmin 是账户管理动作（US9）。
//
// 引擎**不实现**账户管理：它只渲染页面、把动作透传给调用方注入的实现
// （桌面外壳用 internal/desktopcfg；CLI 不注入 → 管理路由 404）。
// 实现方负责：生成/校验/落盘/移入回收目录，以及清零用过的口令字节。
// 为 nil 表示本服务不提供账户管理。
type AccountsAdmin interface {
	// DefaultTargetName 返回创建时默认的账户名称（无账户时为 keystore.json；已有账户时为空）。
	DefaultTargetName() string
	// Create 生成账户并返回复述抽查，**不得写任何文件**。
	Create(targetName, password string, words int) (CreateChallenge, error)
	// ConfirmCreate 校验复述抽查，全部正确才写 keystore。
	ConfirmCreate(token string, answers []string) (AccountOption, error)
	// CancelCreate 丢弃待确认创建。
	CancelCreate(token string)
	// RecoverFromMnemonic 用助记词重建账户。
	RecoverFromMnemonic(name, mnemonic, password string) (AccountOption, error)
	// RecoverFromRecoveryFile 用恢复文件重置登录口令。
	RecoverFromRecoveryFile(name, srcPath, recoveryPassword, newPassword string) (AccountOption, error)
	// Delete 在口令授权下把账户文件移入回收目录（不删除密档）。
	Delete(path, password string) (DeleteResult, error)
}

// CreateChallenge 是「已生成账户、等待复述抽查」的中间态（见 AccountsAdmin.Create）。
type CreateChallenge struct {
	Token    string // 待确认凭据
	Mnemonic string // 助记词（页面上按编号网格展示）
	Indexes  []int  // 抽查序号（1-based）
	Path     string // 确认后将写入的路径
}

// DeleteResult 是删除账户的结果。
type DeleteResult struct {
	Trashed       string // 回收目录中的新路径
	WasConfigured bool   // 被删的正是配置里指定的账户
}
