package play

import (
	"regexp"
	"strings"
	"testing"
)

// T7.10（AC7.9）：外壳/浏览器里的页面必须呈现为「桌面应用」而不是网页 ——
// 不出现浏览器默认样式的文本链接（下划线/链接色），可点元素一律是按钮或列表行。
// 用户反馈：「模仿桌面应用的，放个超链接就露馅了，一眼就看出来是网页了」。
func TestPagesLookLikeDesktopAppNotWebLinks(t *testing.T) {
	tree := treeNode{Dirs: []treeNode{{
		Name:  "a",
		Files: []treeFile{{Label: "a/one.mp4.svid", Name: "one.mp4.svid", Size: "1.0 MiB", Link: "/watch/aaa"}},
	}}}
	pages := map[string]string{
		"index":  indexPage(tree, "/lib"),
		"player": playerPage("one.mp4.svid", "/media/tok/aaa", "/watch/aaa", tree, "/watch/aaa"),
		"picker": libraryPage(pickerView{Root: "/lib", Path: "/lib", Up: "/library?path=/",
			Vaults: 2, Crumbs: []pickerCrumb{{Name: "lib", Last: true}},
			Dirs: []pickerDir{{Name: "a", Link: "/library?path=/lib/a", Vaults: 1, SubDirs: 0}}}),
		"login": loginPage("/lib", ""),
	}

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
