package main

import (
	"errors"

	"github.com/cc14514/go-safemedia/internal/play"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// sealPicker 把 Wails 的原生选择对话框适配成引擎的 play.FilePicker（US11）。
//
// 为什么要它：Wails 的原生拖放是否放行由 webview 的页面行为决定（见 AGENTS 易错点 10），
// 实测在真实 .app 里拖入会被吞掉。这条路径**不依赖任何拖放能力**，是封存的保底入口
// （FR-6.17），与拖入共用引擎的同一条管线。
//
// 两条约定（与 internal/play.FilePicker 一致）：
//   - 用户取消 → 返回空 + nil error（引擎据此保持状态不变）；
//   - 对话框自身失败 → 返回 error（引擎在面板里显示中文原因）。
//
// 选择器只回**路径**，文件内容不经 webview（AC11.6）。
type sealPicker struct {
	app *App
}

var errNoWindow = errors.New("窗口尚未就绪")

// videoFilters 只列封存白名单容器（mp4/mov/webm，FR-6.1）。
// Wails 会把 "*.mp4" 剥成扩展名交给系统面板（macOS 侧 setAllowedFileTypes /
// UTType typeWithFilenameExtension），所以**只能给具体扩展名**：写通配 "*.\\*"
// 会变成扩展名 "*"，实测会得到一个无意义的 dynamic UTType（不是「所有文件」，
// 也匹配不到任何真实文件）；给空扩展名更糟（UTType 返回 nil）。
func videoFilters() []wruntime.FileFilter {
	return []wruntime.FileFilter{
		{DisplayName: "视频（mp4 / mov / webm）", Pattern: "*.mp4;*.mov;*.webm"},
	}
}

func (p *sealPicker) PickFiles() ([]string, error) {
	ctx := p.app.windowCtx()
	if ctx == nil {
		return nil, errNoWindow
	}
	return wruntime.OpenMultipleFilesDialog(ctx, wruntime.OpenDialogOptions{
		Title:   "选择要封存的视频",
		Filters: videoFilters(),
	})
}

func (p *sealPicker) PickFolder() (string, error) {
	ctx := p.app.windowCtx()
	if ctx == nil {
		return "", errNoWindow
	}
	return wruntime.OpenDirectoryDialog(ctx, wruntime.OpenDialogOptions{
		Title: "选择要封存的文件夹",
	})
}

// 编译期断言：sealPicker 满足引擎的选择器接口。
var _ play.FilePicker = (*sealPicker)(nil)
