package play

import (
	"regexp"
	"strings"
	"testing"
)

// styleFixturePages 渲染各页面用于外观/文案守护测试（覆盖引擎的全部页面）。
func styleFixturePages() map[string]string {
	tree := treeNode{Dirs: []treeNode{{
		Name:  "a",
		Files: []treeFile{{Label: "a/one.mp4.svid", Name: "one.mp4.svid", Size: "1.0 MiB", Link: "/watch/aaa"}},
	}}}
	return map[string]string{
		"index":  indexPage(tree, "/lib", false),
		"player": playerPage("one.mp4.svid", "/media/tok/aaa", "/watch/aaa", tree, "/watch/aaa"),
		"picker": libraryPage(pickerView{Root: "/lib", Path: "/lib", Up: "/library?path=/",
			Vaults: 2, Crumbs: []pickerCrumb{{Name: "lib", Last: true}},
			Dirs: []pickerDir{{Name: "a", Link: "/library?path=/lib/a", Vaults: 1, SubDirs: 0}}}),
		// US10：封存面板与「选择输出目录」模式的选择页也必须是桌面应用外观
		"picker-seal": libraryPage(pickerView{Root: "/lib", RootLabel: "当前输出目录", Path: "/lib",
			Purpose: "seal", Up: "/library?path=/&purpose=seal", IsRoot: true, Vaults: 2,
			Crumbs: []pickerCrumb{{Name: "lib", Last: true}},
			Dirs:   []pickerDir{{Name: "a", Link: "/library?path=/lib/a&purpose=seal", Vaults: 1, SubDirs: 0}}}),
		"seal": sealPanelPage(sealPanelView{
			Status: sealStatus{
				Active: true,
				Output: "/lib",
				Items: []sealItemJSON{{
					Name: "clip.mp4", State: "封存中", Percent: 42,
					Done: "4.2 MiB", Total: "10.0 MiB",
				}},
				Rejects: []sealReject{{Path: "notes.txt", Reason: "不支持的容器格式（仅支持 mp4 / webm / mov）"}},
				Summary: "1 个文件",
			},
		}),
		// US11：注入选择器后的封存面板（多两个按钮，外观同样必须是桌面应用）。
		"seal-pick": sealPanelPage(sealPanelView{
			HasPicker: true,
			Status: sealStatus{
				Active: true,
				Output: "/lib",
				Items: []sealItemJSON{{
					Name: "clip.mp4", State: "封存中", Percent: 42,
					Done: "4.2 MiB", Total: "10.0 MiB",
				}},
				Rejects: []sealReject{{Path: "notes.txt", Reason: "不支持的容器格式（仅支持 mp4 / webm / mov）"}},
				Summary: "1 个文件",
			},
		}),
		"login": loginPage(loginView{Target: "/lib", Accounts: []AccountOption{
			{ID: "/home/u/.safemedia/keystore.json", Label: "keystore.json", Note: "0x1a2b…9f8e"},
			{ID: "/home/u/.safemedia/accounts/work.json", Label: "work.json", Note: "0x9ed7…da40"},
		}, Selected: "/home/u/.safemedia/accounts/work.json"}),
		"accounts": accountsPage(accountsView{Accounts: []AccountOption{
			{ID: "/home/u/.safemedia/keystore.json", Label: "keystore.json", Note: "0x1a2b…9f8e"},
			{ID: "/home/u/.safemedia/accounts/work.json", Label: "work.json", Note: "0x9ed7…da40"},
		}}),
		"create":   createPage(createView{Name: "keystore.json", NameLocked: true, Words: 12}),
		"mnemonic": mnemonicPage(mnemonicView{Token: "tok", Words: []numberedWord{{Index: 1, Word: "abandon"}}, Prompts: []promptItem{{Index: 1}}, Path: "/home/u/.safemedia/accounts/work.json"}),
		"recover":  recoverPage(recoverView{Mode: "mnemonic"}),
		"delete":   deletePage(deleteView{Path: "/home/u/.safemedia/accounts/work.json"}),
	}
}

// T7.10（AC7.9）：外壳/浏览器里的页面必须呈现为「桌面应用」而不是网页 ——
// 不出现浏览器默认样式的文本链接（下划线/链接色），可点元素一律是按钮或列表行。
// 用户反馈：「模仿桌面应用的，放个超链接就露馅了，一眼就看出来是网页了」。
func TestPagesLookLikeDesktopAppNotWebLinks(t *testing.T) {
	pages := styleFixturePages()

	// ① 任何页面都不得出现下划线文本（正文里的链接式下划线是「网页」最明显的破绽）。
	for name, html := range pages {
		if strings.Contains(html, "text-decoration:underline") {
			t.Fatalf("%s 页出现下划线文本样式:\n%s", name, html)
		}
	}

	// ② 每个 <a> 都必须带 class（= 按钮或列表行外观），不得有裸链接走浏览器默认样式。
	// （Go 的 regexp 是 RE2，不支持负向前瞻，改为逐标签检查。）
	anchor := regexp.MustCompile(`<a [^>]*>`)
	for name, html := range pages {
		for _, tag := range anchor.FindAllString(html, -1) {
			if !strings.Contains(tag, "class=") {
				t.Fatalf("%s 页存在无 class 的裸链接 %s（会显示成浏览器默认链接）:\n%s", name, tag, html)
			}
		}
	}

	// ③ 导航入口是按钮外观；按钮具备 hover / 按下 / 键盘焦点三态。
	idx := pages["index"]
	for _, want := range []string{
		`<a class="btn primary" href="/library">选择密档库…</a>`,
		"a.btn:hover", "a.btn:active", "a.btn:focus-visible",
		"button:hover", "button:active", "button:focus-visible",
	} {
		if !strings.Contains(idx, want) {
			t.Fatalf("索引页缺少 %q:\n%s", want, idx)
		}
	}
	// 列表行整行可点、且不改文字颜色。
	if !strings.Contains(pages["picker"], `ul.dirs li a::after { content:""; position:absolute; inset:0; }`) {
		t.Fatalf("目录行不是整行可点:\n%s", pages["picker"])
	}
	// 按钮文字不可被选中（原生控件的行为），也不该出现箭头光标以外的「手型链接」暗示。
	if !strings.Contains(idx, "user-select:none") {
		t.Fatalf("按钮缺少 user-select:none:\n%s", idx)
	}

	// ④ 播放页侧栏的「换目录」也必须是按钮，不是绿色文字链接。
	if !strings.Contains(pages["player"], `<a class="btn" href="/library">选择密档库…</a>`) {
		t.Fatalf("播放页侧栏缺少按钮式换目录入口:\n%s", pages["player"])
	}
}

// T8.4（AC8.6）：页面上不留说明性「废话」文本（用户要求把解释段落全部去掉），
// 只保留功能性信息：标题、目标/库根、数量、文件名与大小、错误原因、失败提示。
func TestPagesHaveNoFillerCopy(t *testing.T) {
	pages := styleFixturePages()
	banned := []string{
		"明文不落盘",     // 登录页/索引页的解释句
		"环回",        // 播放页的技术说明句
		"元数据",       // 索引页关于「不显示原始视频名」的解释
		"只用于本机解密",   // 登录页的解释句
		"空闲超时或手动锁定", // 登录页的解释句
	}
	// 红线提示是唯一允许的「说明句」，且只出现在「用户交出/拿到助记词」的两页（AC9.5）。
	redlinePages := map[string]bool{"create": true, "recover": true}
	for name, html := range pages {
		for _, b := range banned {
			if strings.Contains(html, b) {
				t.Fatalf("%s 页仍含说明性文案 %q（AC8.6 要求删除）:\n%s", name, b, html)
			}
		}
		if !redlinePages[name] {
			for _, b := range []string{"绝不导入", "持有真实资产"} {
				if strings.Contains(html, b) {
					t.Fatalf("%s 页出现了红线说明 %q（只允许在创建页与恢复页，AC9.5）:\n%s", name, b, html)
				}
			}
		}
	}
	if strings.Contains(pages["login"], `class="hint"`) || strings.Contains(pages["login"], `class="redline"`) {
		t.Fatalf("登录页仍有说明段落容器:\n%s", pages["login"])
	}
	if strings.Contains(pages["index"], `class="foot"`) {
		t.Fatalf("索引页仍有脚注段落:\n%s", pages["index"])
	}

	// 登录页保留必需的功能性元素：目标、账户选择器、口令、解锁按钮。
	for _, want := range []string{
		`<select id="account" name="account">`,
		`value="/home/u/.safemedia/accounts/work.json" selected`, // 上次选中项保持
		`label for="password"`, `<input id="password" name="password" type="password"`,
		`<button class="primary" type="submit">解锁</button>`,
	} {
		if !strings.Contains(pages["login"], want) {
			t.Fatalf("登录页缺少功能元素 %q:\n%s", want, pages["login"])
		}
	}
}

// pageSources 返回全部页面模板源码（含账户管理页），用于整站级外观守护。
func pageSources() map[string]string {
	return map[string]string{
		"player":   playerSrc,
		"index":    indexSrc,
		"login":    loginSrc,
		"picker":   librarySrc,
		"accounts": accountsSrc,
		"create":   createSrc,
		"mnemonic": mnemonicSrc,
		"recover":  recoverSrc,
		"delete":   deleteSrc,
		"seal":     sealSrc,
	}
}

// T11.8（AC11.8）：引擎每个页面都声明 Wails 的拖放目标标记 `--wails-drop-target: drop`。
//
// 背景：Wails 的原生拖放是否放行由 webview 的页面行为决定（未关 DisableWebViewDrop 时），
// 实测表现为「拖到窗口上什么也不发生」。外壳侧关掉 webview 拖放 + 页面声明该标记是双保险；
// 标记来自共享的 baseCSS，任何页面都不许丢（否则拖放可能只在一部分页面可用）。
func TestPagesDeclareWailsDropTarget(t *testing.T) {
	if !strings.Contains(baseCSS, "--wails-drop-target: drop") {
		t.Fatalf("baseCSS 未声明 Wails 拖放目标标记（US11）:\n%s", baseCSS)
	}
	for name, html := range styleFixturePages() {
		if !strings.Contains(html, "--wails-drop-target: drop") {
			t.Fatalf("%s 页缺少拖放目标标记（拖放会依赖 webview 的页面行为而不稳定）", name)
		}
	}
	// 声明必须落在 :root（自定义属性靠继承生效；放在某个具体元素上会漏掉其它页面结构）。
	if !regexp.MustCompile(`:root\s*\{[^}]*--wails-drop-target`).MatchString(baseCSS) {
		t.Fatalf("拖放目标标记应在 :root 上声明:\n%s", baseCSS)
	}
}

// T9.7（AC9.6）：工具栏必须与正文列对齐 —— 正文列宽只允许写 var(--content)，
// 窄页面通过重定义 --content 收窄（共享的 .tbwrap 规则读同一个变量）。
// 曾出现：表单页把 main 硬编码成 520px，工具栏标题仍贴在 840px 列的左边，两者错位，
// 看起来就像「网页里放了个居中的卡片」。硬编码 = 测试判红。
func TestToolbarAlignsWithContentColumn(t *testing.T) {
	if !strings.Contains(baseCSS, ".tbwrap { width:min(100%,var(--content));") {
		t.Fatalf("工具栏容器 .tbwrap 未按 --content 取宽:\n%s", baseCSS)
	}
	headered := 0
	for name, src := range pageSources() {
		if !strings.Contains(src, `<header class="toolbar">`) {
			continue
		}
		headered++
		got := regexp.MustCompile(`main\s*\{[^}]*width:min\(100%,([^;]*)\)`).FindAllStringSubmatch(src, -1)
		if len(got) == 0 {
			t.Fatalf("%s 页有工具栏却没有正文列宽声明", name)
		}
		for _, g := range got {
			if strings.TrimSpace(g[1]) != "var(--content)" {
				t.Fatalf("%s 页把正文列宽硬编码为 %q（应写 var(--content) 并重定义 --content，否则工具栏与正文错位）", name, g[1])
			}
		}
	}
	if headered < 6 {
		t.Fatalf("带工具栏的页面只找到 %d 个，守护覆盖面不足", headered)
	}
}

// T9.8（AC9.5）：红线提示只允许出现在「用户交出或拿到助记词」的两页（创建页、恢复页），
// 其他页面保持无说明性文案（AC8.6）。
func TestRedlineNoticeOnlyWhereSecretsEnter(t *testing.T) {
	allowed := map[string]bool{"create": true, "recover": true}
	for name, src := range pageSources() {
		if strings.Contains(src, "绝不导入") || strings.Contains(src, "持有真实资产") {
			if !allowed[name] {
				t.Fatalf("%s 页出现了红线提示（只允许出现在创建页与恢复页）", name)
			}
		}
	}
	for _, name := range []string{"create", "recover"} {
		if !strings.Contains(pageSources()[name], "持有真实资产") {
			t.Fatalf("%s 页缺少红线提示（生成/重建密钥前必须让用户看到）", name)
		}
	}
	// AC9.12：助记词页不提供「复制」入口（必须手抄）。
	for _, bad := range []string{"复制", "clipboard", "navigator.clipboard"} {
		if strings.Contains(pageSources()["mnemonic"], bad) {
			t.Fatalf("助记词页出现了 %q（AC9.12 要求必须手抄，不提供复制）", bad)
		}
	}
}
