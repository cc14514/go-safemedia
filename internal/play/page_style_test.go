package play

import (
	"regexp"
	"strings"
	"testing"
)

// styleFixturePages 渲染四个页面用于外观/文案守护测试。
func styleFixturePages() map[string]string {
	tree := treeNode{Dirs: []treeNode{{
		Name:  "a",
		Files: []treeFile{{Label: "a/one.mp4.svid", Name: "one.mp4.svid", Size: "1.0 MiB", Link: "/watch/aaa"}},
	}}}
	return map[string]string{
		"index":  indexPage(tree, "/lib"),
		"player": playerPage("one.mp4.svid", "/media/tok/aaa", "/watch/aaa", tree, "/watch/aaa"),
		"picker": libraryPage(pickerView{Root: "/lib", Path: "/lib", Up: "/library?path=/",
			Vaults: 2, Crumbs: []pickerCrumb{{Name: "lib", Last: true}},
			Dirs: []pickerDir{{Name: "a", Link: "/library?path=/lib/a", Vaults: 1, SubDirs: 0}}}),
		"login": loginPage(loginView{Target: "/lib", Accounts: []AccountOption{
			{ID: "/home/u/.safemedia/keystore.json", Label: "keystore.json", Note: "0x1a2b…9f8e"},
			{ID: "/home/u/.safemedia/accounts/work.json", Label: "work.json", Note: "0x9ed7…da40"},
		}, Selected: "/home/u/.safemedia/accounts/work.json"}),
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
		"绝不导入",      // 只新建账户的红线说明（留在 CLI 输出与文档里）
		"明文不落盘",     // 登录页/索引页的解释句
		"环回",        // 播放页的技术说明句
		"元数据",       // 索引页关于「不显示原始视频名」的解释
		"只用于本机解密",   // 登录页的解释句
		"空闲超时或手动锁定", // 登录页的解释句
	}
	for name, html := range pages {
		for _, b := range banned {
			if strings.Contains(html, b) {
				t.Fatalf("%s 页仍含说明性文案 %q（AC8.6 要求删除）:\n%s", name, b, html)
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
