// Command safemedia-desktop 是 go-safemedia 的桌面外壳（首版仅 macOS）。
//
// 外壳刻意保持「薄」——它不做 UI，也不自己实现 HTTP：
//
//	① 解析播放目标（启动参数 > ~/.safemedia/desktop.json > 默认库）；
//	② 以「锁定态」启动引擎（internal/play），此时只对外提供登录页；
//	③ 用系统 WKWebView 装一个窗口，把窗口指向引擎的 127.0.0.1 地址，
//	   之后目录树、播放页、空闲锁定全部复用引擎自己的页面；
//	④ 窗口关闭/退出时清零密钥并关停服务。
//
// 设计取舍与被否方案见 docs/adr/0001-desktop-shell-wails-loopback.md 与 specs/002/plan.md。
package main

import (
	"context"
	"crypto/ecdsa"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/cc14514/go-safemedia/internal/account"
	"github.com/cc14514/go-safemedia/internal/desktopcfg"
	"github.com/cc14514/go-safemedia/internal/play"
	"github.com/cc14514/go-safemedia/internal/vault"
	"github.com/cc14514/go-safemedia/internal/version"
)

//go:embed all:frontend/dist
var assets embed.FS

// bootstrapPlaceholder 是引导页里注入运行时信息（地址或错误）的占位符。
const bootstrapPlaceholder = "__BOOT__"

// bootInfo 是注入引导页的运行时信息。
type bootInfo struct {
	URL     string `json:"url,omitempty"`
	Error   string `json:"error,omitempty"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

// engine 是外壳对引擎的最小抽象：单档与目录两种形态共用同一套生命周期语义
// （锁定即清零密钥、关闭即关停服务）。
type engine interface {
	Start() (string, error)
	Locked() bool
	Close() error
}

// App 是暴露给 Wails 的最小宿主对象；窗口关闭时负责清零密钥。
type App struct {
	ctx    context.Context
	engine engine
}

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// shutdown 由 Wails 在窗口关闭/退出时调用（AC3.3）。
func (a *App) shutdown(context.Context) {
	if a.engine != nil {
		_ = a.engine.Close()
		a.engine = nil
	}
}

func main() {
	app := &App{}
	title := "go-safemedia"
	boot := bootInfo{Version: version.String(), Title: title}

	home, err := os.UserHomeDir()
	switch {
	case err != nil:
		boot.Error = "无法确定用户主目录：" + err.Error()
	default:
		url, label, err := prepare(app, home)
		if err != nil {
			boot.Error = err.Error()
			break
		}
		boot.URL = url
		// 诊断输出（GUI 的 stderr）：确认引擎以「锁定态」起步（AC1.1）。
		fmt.Fprintf(os.Stderr, "[safemedia] 引擎地址=%s 已锁定=%v\n", url, app.engine.Locked())
		if label != "" {
			title = "go-safemedia — " + label
			boot.Title = title
		}
	}

	if err := wails.Run(&options.App{
		Title:     title,
		Width:     1180,
		Height:    760,
		MinWidth:  900,
		MinHeight: 560,
		AssetServer: &assetserver.Options{
			Assets:     assets,
			Middleware: bootstrapMiddleware(boot),
		},
		BackgroundColour: &options.RGBA{R: 17, G: 17, B: 17, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind:             []interface{}{app},
	}); err != nil {
		app.shutdown(context.Background())
		fmt.Fprintln(os.Stderr, "桌面外壳启动失败:", err)
		os.Exit(1)
	}
}

// prepare 解析目标并以锁定态启动引擎，返回引擎地址与窗口标题用的标签。
func prepare(app *App, home string) (url, label string, err error) {
	cfg, err := desktopcfg.Load(home)
	if err != nil {
		return "", "", err
	}
	target, err := desktopcfg.Resolve(appArgs(), cfg, home)
	if err != nil {
		return "", "", err
	}
	eng, label, err := buildEngine(target)
	if err != nil {
		return "", "", err
	}
	url, err = eng.Start()
	if err != nil {
		_ = eng.Close()
		return "", "", err
	}
	app.engine = eng
	return url, label, nil
}

// appArgsOverride 仅供测试注入启动参数（与 internal/cli 的 Prompt 注入同一路数）。
var appArgsOverride []string

// appArgs 返回启动参数，忽略以 - 开头的框架/系统参数。
func appArgs() []string {
	src := os.Args[1:]
	if appArgsOverride != nil {
		src = appArgsOverride
	}
	var out []string
	for _, a := range src {
		if strings.HasPrefix(a, "-") {
			continue
		}
		out = append(out, a)
	}
	return out
}

// buildEngine 按目标形态构造引擎（锁定态启动：先出登录页，解锁后才解密）。
func buildEngine(target desktopcfg.Target) (engine, string, error) {
	load := passphraseLoader(target.Account)

	switch target.Kind {
	case desktopcfg.KindFile:
		name := filepath.Base(target.Path)
		srv, err := play.New(play.Options{
			Name:        name,
			IdleTimeout: play.DefaultIdleTimeout,
			SoftLock:    true, // 锁定后回到登录页，而不是关停（AC3.1/AC3.2）
			Unlock: func(password string) (string, *vault.Reader, error) {
				acct, err := load(password)
				if err != nil {
					return "", nil, err
				}
				r, err := vault.Open(target.Path, acct.PrivateKey())
				if err != nil {
					return "", nil, err
				}
				return name, r, nil
			},
		})
		if err != nil {
			return nil, "", err
		}
		return srv, name, nil

	case desktopcfg.KindLibrary:
		entries, err := play.ScanLibrary(target.Path)
		if err != nil {
			return nil, "", err
		}
		lib, err := play.NewLibrary(play.LibraryOptions{
			Entries:     entries,
			Target:      target.Path,
			IdleTimeout: play.DefaultIdleTimeout,
			SoftLock:    true,
			Unlock: func(password string) (*ecdsa.PrivateKey, error) {
				acct, err := load(password)
				if err != nil {
					return nil, err
				}
				return acct.PrivateKey(), nil
			},
		})
		if err != nil {
			return nil, "", err
		}
		return lib, filepath.Base(target.Path), nil
	}
	return nil, "", fmt.Errorf("不支持的目标类型 %q", target.Kind)
}

// passphraseLoader 返回「口令 → 账户」的加载器。
//
// 口令字节用完立即清零；字符串本身（来自 HTTP 表单）无法清零，这是 Go 字符串的固有限制，
// 与 CLI 路径一致（见 internal/cli/prompt.go）。
func passphraseLoader(accountPath string) func(string) (*account.Account, error) {
	return func(password string) (*account.Account, error) {
		pass := []byte(password)
		defer account.Wipe(pass)

		if _, err := os.Stat(accountPath); err != nil {
			return nil, fmt.Errorf("找不到账户文件 %s：请先用命令行执行 `safemedia account create` 新建账户", accountPath)
		}
		acct, err := account.LoadKeystoreWithPassphrase(accountPath, pass)
		if err != nil {
			return nil, err
		}
		return acct, nil
	}
}

// bootstrapMiddleware 把引导页交给 webview，并注入运行时信息。
//
// 引导页只做一件事：把窗口 `location.replace` 到引擎的环回地址。
// 窗口内容始终来自我们自己的 127.0.0.1 服务，因此不需要前端框架或 Node 构建。
func bootstrapMiddleware(boot bootInfo) assetserver.Middleware {
	page, err := renderBootstrap(boot)
	if err != nil {
		// 引导页缺失属打包错误：给出一段自带错误提示的兜底页面，避免白屏。
		page = []byte("<!doctype html><meta charset=\"utf-8\"><body style=\"background:#111;color:#e8e8e8;font-family:-apple-system,sans-serif\">" +
			"<h1>go-safemedia 桌面外壳</h1><p>引导页缺失：" + htmlEscape(err.Error()) + "</p>")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" || r.URL.Path == "/index.html" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				_, _ = w.Write(page)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// renderBootstrap 把运行时信息注入引导页。
//
// 用 encoding/json 生成 JS 字面量：它会转义 < > &，因此错误文本不会提前闭合 <script>。
func renderBootstrap(boot bootInfo) ([]byte, error) {
	raw, err := assets.ReadFile("frontend/dist/index.html")
	if err != nil {
		return nil, fmt.Errorf("读取引导页失败: %w", err)
	}
	payload, err := json.Marshal(boot)
	if err != nil {
		return nil, err
	}
	return []byte(strings.ReplaceAll(string(raw), bootstrapPlaceholder, string(payload))), nil
}

// htmlEscape 做最小 HTML 转义（仅用于兜底页面）。
func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
