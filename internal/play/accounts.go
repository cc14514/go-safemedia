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
