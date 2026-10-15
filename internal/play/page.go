package play

import (
	"html/template"
	"strings"
)

// baseCSS 是所有页面共用的基础样式（一处定义、四处 {{template "baseCSS"}} 复用）。
// 目标是「看起来像原生 macOS 深色应用」而不是网页（用户反馈：页面里冒出超链接就露馅）：
//
//	· 按钮：<button> 与 <a class="btn"> 外观完全相同（导航用 a，不出现任何链接色/下划线），
//	  不可选中、箭头光标、hover / 按下 / 键盘焦点三态；
//	· 文字链接一律以按钮或列表行呈现；列表行整行可点、hover 给底色、不改文字颜色；
//	· 输入框：深色内嵌底 + 焦点环；跟随系统深色外观（color-scheme: dark）。
const baseCSS = `{{define "baseCSS"}}
  :root { color-scheme: dark;
    --bg:#1b1b1d; --bg-side:#151517; --panel:#212123; --field:#101012;
    --line:#333336; --line-soft:#2a2a2c; --text:#e9e9ec; --dim:#9b9ba1;
    --accent:#3f7a34; --accent-hi:#4a8f3e; --accent-text:#e8f7e2;
    --focus:#4d8de0; --radius:7px; --content:840px; }
  * { box-sizing: border-box; }
  body { margin:0; background:var(--bg); color:var(--text);
    font:13.5px/1.55 -apple-system,"SF Pro Text","Helvetica Neue","PingFang SC",sans-serif;
    -webkit-font-smoothing:antialiased; }
  h1 { font-size:15px; font-weight:600; margin:0; }
  code { font-family:ui-monospace,"SF Mono",Menlo,monospace; font-size:12.5px;
    background:#00000055; padding:1px 6px; border-radius:5px; }
  ::selection { background:#3f7a3466; }

  /* 按钮：a.btn 与 button 视觉一致，导航（换页）也走按钮外观 */
  button, a.btn { -webkit-appearance:none; appearance:none; display:inline-flex; align-items:center;
    justify-content:center; gap:6px; font:inherit; font-size:13px; font-weight:500; color:var(--text);
    background:#333336; border:1px solid #48484b; border-radius:var(--radius); padding:5px 12px;
    min-height:25px; text-decoration:none; cursor:default;
    user-select:none; -webkit-user-select:none; white-space:nowrap; }
  button:hover, a.btn:hover { background:#3d3d41; }
  button:active, a.btn:active { background:#2b2b2e; border-color:#3a3a3d; }
  button:disabled { opacity:.45; }
  button:focus-visible, a.btn:focus-visible, input:focus-visible { outline:2px solid var(--focus); outline-offset:2px; }
  button.primary, a.btn.primary { background:var(--accent); border-color:#4f8a44; color:var(--accent-text); }
  button.primary:hover, a.btn.primary:hover { background:var(--accent-hi); }
  button.primary:active, a.btn.primary:active { background:#356529; }

  input[type=password], input[type=text] { -webkit-appearance:none; appearance:none; width:100%;
    background:var(--field); color:var(--text); border:1px solid #3c3c3f; border-radius:var(--radius);
    padding:7px 10px; font:inherit; }
  input:focus { outline:none; border-color:var(--focus); box-shadow:0 0 0 3px #4d8de033; }

  /* 窗口顶部工具栏：像原生窗口的一行控件；内容与正文列左对齐 */
  header.toolbar { display:block; padding:9px 24px; background:var(--panel);
    border-bottom:1px solid var(--line-soft); position:sticky; top:0; z-index:2; }
  header.toolbar .tbwrap { width:min(100%,var(--content)); margin:0 auto; display:flex; align-items:center; gap:10px; }
  header.toolbar .grow { flex:1; min-width:0; }
  header.toolbar, header.toolbar * { user-select:none; -webkit-user-select:none; }

  /* 列表行：整行可点，hover 给底色，不改文字颜色、无下划线 */
  a.row { color:var(--text); text-decoration:none; }
{{end}}`

// treeCSS 是目录树的共享样式（索引页与播放页侧栏共用）。
const treeCSS = `{{define "treeCSS"}}
  ul.tree { list-style:none; margin:0; padding-left:8px; }
  ul.tree li { padding:1px 0; }
  ul.tree details > summary { cursor:pointer; font-weight:500; color:#c9c9ce; padding:3px 6px; border-radius:5px;
                              user-select:none; -webkit-user-select:none; }
  ul.tree details > summary:hover { background:#ffffff0d; }
  ul.tree details[open] > summary { color:var(--text); }
  ul.tree li.file { display:flex; align-items:baseline; gap:8px; }
  ul.tree li.file a { flex:1; min-width:0; display:block; padding:3px 6px; border-radius:5px; cursor:pointer;
                      color:#d2d2d7; text-decoration:none; word-break:break-all; }
  ul.tree li.file a:hover { background:#ffffff0d; }
  ul.tree li.file.active a { background:var(--accent); color:var(--accent-text); }
  ul.tree li.file .sz { opacity:.35; font-size:11.5px; white-space:nowrap; }
{{end}}`

// treeTmpl 是递归渲染目录树的模板。
// 数据上下文为 treeCtx；子节点通过 nodeWith 传递（$ 是本次 {{template}} 调用的数据，
// 故不能用 $ 直接指向页面数据）。
const treeTmpl = `{{define "tree"}}<ul class="tree">
{{range .Node.Dirs}}<li class="dir"><details open><summary>{{.Name}}</summary>{{template "tree" (nodeWith $ .)}}</details></li>
{{end}}{{range .Node.Files}}<li class="file{{if eq .Link $.Active}} active{{end}}"><a href="{{.Link}}" title="{{.Label}}" class="row">{{.Name}}</a><span class="sz">{{.Size}}</span></li>
{{end}}</ul>{{end}}`

const playerSrc = `{{define "player"}}<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>{{.Title}}</title>
<style>
{{template "baseCSS"}}
  body { display:flex; min-height:100vh; }
  main { flex:1; min-width:0; display:flex; flex-direction:column; align-items:center; padding:22px 24px; }
  main h1 { font-size:14px; font-weight:600; color:var(--dim); margin:0 0 14px; word-break:break-all; text-align:center; }
  video { width:min(100%,1100px); max-height:78vh; background:#000; border-radius:10px; box-shadow:0 10px 34px rgba(0,0,0,.55); }
  .row { display:flex; gap:10px; margin-top:14px; align-items:center; flex-wrap:wrap; justify-content:center; }
  .warn { display:none; margin-top:16px; max-width:760px; background:#3a2410; border:1px solid #7a4a12; color:#ffd9a0;
          padding:12px 16px; border-radius:8px; font-size:13.5px; }
  .warn code { background:#00000055; padding:1px 5px; border-radius:4px; }
  .hint { margin-top:10px; font-size:12px; color:var(--dim); text-align:center; }
  aside.tree { flex:0 0 272px; box-sizing:border-box; height:100vh; position:sticky; top:0; overflow:auto;
               background:var(--bg-side); border-right:1px solid var(--line-soft); padding:12px 10px 28px; font-size:13px; }
  aside.tree .sidehead { display:flex; align-items:center; justify-content:space-between; gap:8px; margin:0 2px 12px 4px; }
  aside.tree .libtitle { font-size:12px; color:var(--dim); user-select:none; -webkit-user-select:none; }
  aside.tree .sidehead a.btn { padding:3px 9px; min-height:22px; font-size:12px; }
{{template "treeCSS"}}
  @media (max-width:820px) {
    body { flex-direction:column; }
    aside.tree { width:100%; flex:none; height:auto; max-height:34vh; position:static;
                 border-right:none; border-bottom:1px solid var(--line-soft); }
  }
</style>
</head>
<body>
{{if .HasTree}}<aside class="tree">
  <div class="sidehead">
    <div class="libtitle">密档库 · {{.Count}} 个</div>
    <a class="btn" href="/library">选择密档库…</a>
  </div>
  {{template "tree" .Tree}}
</aside>
{{end}}<main>
<h1>{{.Title}}</h1>
<video id="v" controls autoplay playsinline src="{{.Media}}"></video>

<div class="warn" id="warn">
  浏览器无法解码这个视频流（常见于 <b>HEVC/H.265</b> 且浏览器不支持，例如 Firefox）。
  请关闭此页，改用 <code>safemedia play {{.Title}} --player mpv</code> 播放（本机需已安装 mpv）。
</div>

<div class="row">
  {{if .Back}}<a class="btn" href="{{.Back}}">← 返回列表</a>{{end}}
  <button id="lock">立即锁定并清理密钥</button>
</div>
<div class="hint">视频通过本机 127.0.0.1 环回解密播放，明文不落盘。{{if .HasTree}}左侧为密档目录树，点击即可切换片源；全屏播放时目录树自动隐藏。{{end}}</div>
</main>

<script>
const v = document.getElementById('v');
const warn = document.getElementById('warn');
let failed = false;
// data-decode 是给自动化（L3）与排障用的机器可读播放状态。
document.documentElement.dataset.decode = 'pending';
function showWarn(){
  document.documentElement.dataset.decode = 'failed';
  if(!failed){ failed = true; warn.style.display = 'block'; }
}
v.addEventListener('playing', () => { document.documentElement.dataset.decode = 'playing'; });
v.addEventListener('canplay', () => { if (!failed) document.documentElement.dataset.decode = 'canplay'; });
v.addEventListener('error', showWarn);
v.addEventListener('stalled', () => setTimeout(() => { if (v.readyState < 2) showWarn(); }, 4000));
document.getElementById('lock').addEventListener('click', async () => {
  let alive = false;
  try { const r = await fetch('/lock', { method: 'POST' }); alive = r.ok; } catch (e) {}
  v.pause(); v.removeAttribute('src'); v.load();
  document.documentElement.dataset.decode = 'locked';
  document.getElementById('lock').disabled = true;
  const msg = '已锁定：密钥已从内存清零。';
  document.querySelector('.hint').textContent = alive ? msg + '正在返回登录页…' : msg + '服务已关闭，可关闭此页面。';
  if (alive) location.replace('/');
});
// 长目录树时把当前播放的条目滚进视野。
const act = document.querySelector('aside.tree li.file.active');
if (act) act.scrollIntoView({ block: 'nearest' });
</script>
</body>
</html>{{end}}`

const indexSrc = `{{define "index"}}<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>go-safemedia 密档库</title>
<style>
{{template "baseCSS"}}
  main { width:min(100%,var(--content)); margin:0 auto; padding:22px 24px 44px; }
  p.sub { color:var(--dim); font-size:12.5px; margin:0 0 18px; }
  ul.tree { padding-left:0; }
  .empty { border:1px dashed var(--line); border-radius:10px; padding:30px; text-align:center; color:var(--dim); }
  .empty code { margin:0 4px; }
  .foot { margin-top:26px; font-size:12px; color:var(--dim); opacity:.8; user-select:none; -webkit-user-select:none; }
{{template "treeCSS"}}
</style>
</head>
<body>
<header class="toolbar">
  <div class="tbwrap">
    <h1>密档库</h1>
    <span class="grow"></span>
    <a class="btn primary" href="/library">选择密档库…</a>
  </div>
</header>
<main>
  <p class="sub">库根：<code>{{.Root}}</code> ｜ 共 {{.Count}} 个密档（含子目录）。条目只显示密档文件名，不包含原始视频名等元数据。</p>
  {{if .HasTree}}{{template "tree" .Tree}}
  {{else}}<div class="empty">这个目录里没有 .svid 密档。<br>先运行 <code>safemedia seal &lt;video&gt;</code> 封存视频。</div>{{end}}
  <div class="foot">全部解密只在本机 127.0.0.1 环回进行，明文不落盘。</div>
</main>
</body>
</html>{{end}}`

// loginSrc 是锁定态下的登录页（桌面外壳；桌面 Shell 以锁定态启动服务，AC1.1）。
const loginSrc = `{{define "login"}}<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>go-safemedia 解锁</title>
<style>
{{template "baseCSS"}}
  body { display:flex; align-items:center; justify-content:center; min-height:100vh; padding:28px; }
  .card { width:min(100%,420px); background:var(--panel); border:1px solid #2e2e31; border-radius:12px;
          padding:26px 24px 20px; box-shadow:0 22px 60px rgba(0,0,0,.45); }
  .card h1 { font-size:16px; margin:0 0 6px; }
  p.sub { color:var(--dim); font-size:12.5px; margin:0 0 18px; word-break:break-all; }
  label { display:block; font-size:12.5px; color:var(--dim); margin:0 0 6px;
          user-select:none; -webkit-user-select:none; }
  button.primary { width:100%; margin-top:16px; padding:8px 14px; font-size:13.5px; }
  .err { margin:0 0 16px; background:#3a1a1a; border:1px solid #7a2a2a; color:#ffb0b0;
         padding:10px 12px; border-radius:8px; font-size:13px; }
  .hint { margin-top:14px; font-size:12px; color:var(--dim); }
  .redline { margin-top:12px; font-size:11.5px; color:var(--dim); opacity:.85;
             border-top:1px solid var(--line-soft); padding-top:12px; }
</style>
</head>
<body>
<form class="card" method="post" action="/login">
  <h1>解锁密档</h1>
  <p class="sub">{{if .Target}}目标：{{.Target}}{{else}}输入账户口令以启封播放；口令只用于本机解密。{{end}}</p>
  {{if .Err}}<p class="err">{{.Err}}</p>{{end}}
  <label for="password">账户口令</label>
  <input id="password" name="password" type="password" autofocus autocomplete="current-password" required>
  <button class="primary" type="submit">解锁并进入</button>
  <div class="hint">解密全部在本机 127.0.0.1 环回进行，明文不落盘；空闲超时或手动锁定后会回到本页。</div>
  <div class="redline">本工具只新建账户，绝不导入持有真实资产的钱包私钥/助记词。</div>
</form>
<script>try { document.getElementById('password').focus(); } catch (e) {}</script>
</body>
</html>{{end}}`

// librarySrc 是密档库目录选择页（US7）：像文件浏览器一样浏览目录、选定库根。
// 只列目录名（不列文件），与 ScanLibrary 同口径跳过隐藏项与符号链接。
const librarySrc = `{{define "library"}}<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>选择密档库</title>
<style>
{{template "baseCSS"}}
  main { width:min(100%,var(--content)); margin:0 auto; padding:22px 24px 44px; }
  p.sub { color:var(--dim); font-size:12.5px; margin:0 0 16px; word-break:break-all; }
  .err { margin:0 0 16px; background:#3a1a1a; border:1px solid #7a2a2a; color:#ffb0b0;
         padding:10px 12px; border-radius:8px; font-size:13px; }
  /* 面包屑：分段控件观感（无下划线、无链接色） */
  .crumbs { display:flex; flex-wrap:wrap; align-items:center; gap:1px; margin:0 0 12px; font-size:12.5px;
            user-select:none; -webkit-user-select:none; }
  .crumbs a { padding:2px 7px; border-radius:5px; color:var(--text); text-decoration:none; cursor:pointer; }
  .crumbs a:hover { background:#ffffff14; }
  .crumbs .sep { color:var(--dim); opacity:.55; padding:0 1px; }
  .crumbs .cur { color:var(--dim); padding:2px 7px; }
  .pathbar { display:flex; gap:8px; margin:0 0 14px; }
  .pathbar input { font-family:ui-monospace,"SF Mono",Menlo,monospace; font-size:12.5px; }
  .actions { display:flex; align-items:center; gap:10px; margin:0 0 16px; flex-wrap:wrap; }
  .meta { color:var(--dim); font-size:12.5px; margin:0 0 12px; }
  ul.dirs { list-style:none; margin:0; padding:0; background:var(--panel); border:1px solid var(--line-soft);
            border-radius:10px; overflow:hidden; }
  ul.dirs li { position:relative; display:flex; justify-content:space-between; align-items:baseline;
               gap:12px; padding:8px 14px; border-top:1px solid #26262a; }
  ul.dirs li:first-child { border-top:none; }
  ul.dirs li:hover { background:#ffffff0d; }
  ul.dirs li a { color:var(--text); text-decoration:none; }
  ul.dirs li a::after { content:""; position:absolute; inset:0; } /* 整行可点 */
  ul.dirs .n { color:var(--dim); font-size:12px; white-space:nowrap; user-select:none; -webkit-user-select:none; }
  /* 目录行前置图标（原生文件夹观感），随文字基线对齐 */
  ul.dirs .ico { flex:none; margin-right:8px; vertical-align:-2px; }
  .empty { border:1px dashed var(--line); border-radius:10px; padding:22px; text-align:center;
           color:var(--dim); font-size:13px; }
</style>
</head>
<body>
<header class="toolbar">
  <div class="tbwrap">
    <h1>选择密档库</h1>
    <span class="grow"></span>
    {{if .Up}}<a class="btn" href="{{.Up}}">↑ 上级目录</a>{{end}}
    <a class="btn" href="/">← 返回密档库索引</a>
  </div>
</header>
<main>
  <p class="sub">当前库根：<code>{{.Root}}</code></p>
  {{if .Err}}<p class="err">{{.Err}}</p>{{end}}
  <nav class="crumbs">{{range .Crumbs}}{{if .Last}}<span class="cur">{{.Name}}</span>{{else}}<a href="{{.Link}}">{{.Name}}</a><span class="sep">›</span>{{end}}{{end}}</nav>
  <form class="pathbar" method="get" action="/library">
    <input name="path" value="{{.Path}}" spellcheck="false" autocomplete="off" autocapitalize="off">
    <button type="submit">转到</button>
  </form>
  <div class="actions">
    <form method="post" action="/library">
      <input type="hidden" name="path" value="{{.Path}}">
      <button class="primary" type="submit">使用此目录作为库根</button>
    </form>
  </div>
  <p class="meta">本目录直接包含 {{.Vaults}} 个密档{{if .IsRoot}}；这就是当前库根{{else}}；选定后按子目录结构递归展示全部密档{{end}}。</p>
  {{if .Dirs}}<ul class="dirs">{{range .Dirs}}<li><a href="{{.Link}}" class="row" title="{{.Name}}"><svg class="ico" viewBox="0 0 16 16" width="13" height="13" aria-hidden="true"><path d="M1.5 4.5c0-.8.6-1.4 1.4-1.4h2.8c.4 0 .8.2 1 .5l.9 1.1h5.5c.8 0 1.4.6 1.4 1.4v5.4c0 .8-.6 1.4-1.4 1.4H2.9c-.8 0-1.4-.6-1.4-1.4V4.5z" fill="#5f92d8" fill-opacity=".9"/></svg>{{.Name}}/</a><span class="n">{{.Vaults}} 个密档 · {{.SubDirs}} 个子目录</span></li>{{end}}</ul>
  {{else}}<div class="empty">这个目录下没有可进入的子目录。</div>{{end}}
</main>
</body>
</html>{{end}}`

// treeCtx 是目录树模板的数据上下文：节点 + 当前高亮的条目链接。
type treeCtx struct {
	Node   treeNode
	Active string
}

var pageTmpl = template.Must(template.New("pages").Funcs(template.FuncMap{
	// nodeWith 由父上下文派生一个子节点上下文（保持 Active 不变）。
	"nodeWith": func(ctx treeCtx, n treeNode) treeCtx {
		return treeCtx{Node: n, Active: ctx.Active}
	},
}).Parse(baseCSS + treeCSS + treeTmpl + playerSrc + indexSrc + librarySrc + loginSrc))

// playerPage 渲染播放页。tree 为空则不显示左侧目录树（单密档模式）。
// Title 做 HTML 转义（密档文件名可能含任意字符）；back 为空则不显示返回链接。
func playerPage(name, mediaPath, back string, tree treeNode, active string) string {
	title := strings.TrimSpace(name)
	if title == "" {
		title = "go-safemedia 播放"
	}
	var b strings.Builder
	err := pageTmpl.ExecuteTemplate(&b, "player", struct {
		Title   string
		Media   string
		Back    string
		Tree    treeCtx
		HasTree bool
		Count   int
	}{
		Title:   title,
		Media:   mediaPath,
		Back:    back,
		Tree:    treeCtx{Node: tree, Active: active},
		HasTree: !tree.empty(),
		Count:   tree.count(),
	})
	if err != nil {
		return renderError(err)
	}
	return b.String()
}

// indexPage 渲染目录索引页（按目录树分组，AC5.4）；root 为当前库根（US7 显示用）。
func indexPage(tree treeNode, root string) string {
	var b strings.Builder
	err := pageTmpl.ExecuteTemplate(&b, "index", struct {
		Tree    treeCtx
		HasTree bool
		Count   int
		Root    string
	}{
		Tree:    treeCtx{Node: tree},
		HasTree: !tree.empty(),
		Count:   tree.count(),
		Root:    root,
	})
	if err != nil {
		return renderError(err)
	}
	return b.String()
}

// pickerCrumb 是目录选择页的面包屑片段（Last 为当前目录，不可点）。
type pickerCrumb struct {
	Name string
	Link string
	Last bool
}

// pickerDir 是目录选择页中的一个子目录。
type pickerDir struct {
	Name    string
	Link    string
	Vaults  int // 该子目录「直接」包含的密档数
	SubDirs int // 该子目录「直接」包含的子目录数
}

// pickerView 是目录选择页的渲染数据（US7）。
type pickerView struct {
	Root   string
	Path   string
	Err    string
	Up     string
	IsRoot bool
	Vaults int
	Crumbs []pickerCrumb
	Dirs   []pickerDir
}

// libraryPage 渲染密档库目录选择页。
func libraryPage(v pickerView) string {
	var b strings.Builder
	err := pageTmpl.ExecuteTemplate(&b, "library", v)
	if err != nil {
		return renderError(err)
	}
	return b.String()
}

// loginPage 渲染锁定态的登录页（target 为目标描述，errMsg 为上次失败原因）。
// 与其它页面一致：模板执行失败必须可见（renderError），不得静默截断。
func loginPage(target, errMsg string) string {
	var b strings.Builder
	err := pageTmpl.ExecuteTemplate(&b, "login", struct {
		Target string
		Err    string
	}{Target: target, Err: errMsg})
	if err != nil {
		return renderError(err)
	}
	return b.String()
}

// renderError 把模板渲染失败变成可见页面（避免静默空白页）。
func renderError(err error) string {
	return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>渲染失败</title></head>` +
		`<body style="background:#111;color:#e8e8e8;font:15px -apple-system,sans-serif;padding:32px">` +
		`<h1 style="font-size:16px">页面渲染失败</h1><pre>` + template.HTMLEscapeString(err.Error()) + `</pre></body></html>`
}
