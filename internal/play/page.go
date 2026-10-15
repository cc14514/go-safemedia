package play

import (
	"html/template"
	"strings"
)

// treeCSS 是目录树的共享样式（索引页与播放页侧栏共用）。
const treeCSS = `{{define "treeCSS"}}
  ul.tree { list-style:none; margin:0; padding-left:10px; }
  ul.tree li { padding:1px 0; }
  ul.tree details > summary { cursor:pointer; font-weight:600; opacity:.85; padding:2px 4px; border-radius:6px; }
  ul.tree details > summary:hover { background:#202020; }
  ul.tree li.file { display:flex; align-items:baseline; gap:8px; }
  ul.tree li.file a { flex:1; min-width:0; display:block; padding:3px 6px; border-radius:6px;
                      color:#cfcfcf; text-decoration:none; word-break:break-all; }
  ul.tree li.file a:hover { background:#222; }
  ul.tree li.file.active a { background:#23331f; color:#b6f0b6; }
  ul.tree li.file .sz { opacity:.4; font-size:12px; white-space:nowrap; }
{{end}}`

// treeTmpl 是递归渲染目录树的模板。
// 数据上下文为 treeCtx；子节点通过 nodeWith 传递（$ 是本次 {{template}} 调用的数据，
// 故不能用 $ 直接指向页面数据）。
const treeTmpl = `{{define "tree"}}<ul class="tree">
{{range .Node.Dirs}}<li class="dir"><details open><summary>{{.Name}}</summary>{{template "tree" (nodeWith $ .)}}</details></li>
{{end}}{{range .Node.Files}}<li class="file{{if eq .Link $.Active}} active{{end}}"><a href="{{.Link}}" title="{{.Label}}">{{.Name}}</a><span class="sz">{{.Size}}</span></li>
{{end}}</ul>{{end}}`

const playerSrc = `{{define "player"}}<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>{{.Title}}</title>
<style>
  :root { color-scheme: dark; }
  body { margin:0; background:#111; color:#e8e8e8; font:15px/1.6 -apple-system,"PingFang SC","Microsoft YaHei",sans-serif;
         display:flex; min-height:100vh; }
  main { flex:1; min-width:0; display:flex; flex-direction:column; align-items:center; padding:24px; box-sizing:border-box; }
  h1 { font-size:16px; font-weight:600; margin:0 0 16px; opacity:.85; word-break:break-all; text-align:center; }
  video { width:min(100%,1100px); max-height:78vh; background:#000; border-radius:10px; box-shadow:0 8px 30px rgba(0,0,0,.6); }
  .row { display:flex; gap:12px; margin-top:14px; align-items:center; flex-wrap:wrap; justify-content:center; }
  button, a.btn { background:#2a2a2a; color:#e8e8e8; border:1px solid #444; border-radius:8px; padding:8px 16px;
         cursor:pointer; font-size:14px; text-decoration:none; }
  button:hover, a.btn:hover { background:#333; }
  .warn { display:none; margin-top:16px; max-width:760px; background:#3a2410; border:1px solid #7a4a12; color:#ffd9a0;
          padding:12px 16px; border-radius:8px; font-size:14px; }
  .warn code { background:#00000055; padding:1px 5px; border-radius:4px; }
  .hint { margin-top:10px; font-size:12.5px; opacity:.55; text-align:center; }
  aside.tree { flex:0 0 264px; box-sizing:border-box; height:100vh; position:sticky; top:0; overflow:auto;
               background:#151515; border-right:1px solid #262626; padding:14px 10px 28px; font-size:13.5px; }
  aside.tree .libtitle { font-size:12.5px; opacity:.5; margin:0 0 10px 6px; }
  aside.tree .libtitle a { color:#9ecb7a; text-decoration:none; }
  aside.tree .libtitle a:hover { text-decoration:underline; }
{{template "treeCSS"}}
  @media (max-width:820px) {
    body { flex-direction:column; }
    aside.tree { width:100%; flex:none; height:auto; max-height:34vh; position:static;
                 border-right:none; border-bottom:1px solid #262626; }
  }
</style>
</head>
<body>
{{if .HasTree}}<aside class="tree">
  <div class="libtitle"><a href="/library">密档库 · {{.Count}} 个 · 换目录…</a></div>
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
<div class="hint">视频通过本机 127.0.0.1 环回解密播放；关闭命令行窗口即结束会话。{{if .HasTree}}左侧为密档目录树，点击即可切换片源；全屏播放时目录树自动隐藏。{{end}}</div>
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
  alert(msg);
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
  :root { color-scheme: dark; }
  body { margin:0; background:#111; color:#e8e8e8; font:15px/1.6 -apple-system,"PingFang SC","Microsoft YaHei",sans-serif;
         display:flex; justify-content:center; padding:40px 24px; box-sizing:border-box; min-height:100vh; }
  main { width:min(100%,820px); }
  h1 { font-size:18px; margin:0 0 6px; }
  p.sub { opacity:.55; font-size:13px; margin:0 0 22px; }
  ul.tree { padding-left:0; }
  .empty { border:1px dashed #3a3a3a; border-radius:10px; padding:26px; text-align:center; opacity:.6; }
  .tools { margin:-8px 0 20px; font-size:13px; }
  .tools a { color:#9ecb7a; text-decoration:none; }
  .tools a:hover { text-decoration:underline; }
  .foot { margin-top:22px; font-size:12.5px; opacity:.45; }
{{template "treeCSS"}}
</style>
</head>
<body>
<main>
  <h1>密档库</h1>
  <p class="sub">库根：<code>{{.Root}}</code> ｜ 共 {{.Count}} 个密档（含子目录）。条目只显示密档文件名，不包含原始视频名等元数据。</p>
  <p class="tools"><a href="/library">选择密档库（换扫描目录）…</a></p>
  {{if .HasTree}}{{template "tree" .Tree}}
  {{else}}<div class="empty">这个目录里没有 .svid 密档。<br>先运行 <code>safemedia seal &lt;video&gt;</code> 封存视频。</div>{{end}}
  <div class="foot">全部解密只在本机 127.0.0.1 环回进行；关闭命令行窗口即结束会话。</div>
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
  :root { color-scheme: dark; }
  body { margin:0; min-height:100vh; background:#111; color:#e8e8e8;
         font:15px/1.6 -apple-system,"PingFang SC","Microsoft YaHei",sans-serif;
         display:flex; align-items:center; justify-content:center; padding:24px; box-sizing:border-box; }
  .card { width:min(100%,430px); background:#181818; border:1px solid #2a2a2a; border-radius:14px; padding:28px 26px 22px; }
  h1 { font-size:18px; margin:0 0 8px; }
  p.sub { opacity:.6; font-size:13px; margin:0 0 18px; word-break:break-all; }
  label { display:block; font-size:13px; opacity:.7; margin-bottom:6px; }
  input[type=password] { width:100%; box-sizing:border-box; background:#0e0e0e; color:#e8e8e8;
        border:1px solid #3a3a3a; border-radius:8px; padding:10px 12px; font-size:15px; }
  input[type=password]:focus { outline:none; border-color:#4a6b3a; }
  button { margin-top:16px; width:100%; background:#2f4a25; color:#dff5d8; border:1px solid #4a6b3a;
        border-radius:8px; padding:11px 16px; font-size:15px; cursor:pointer; }
  button:hover { background:#3a5a2d; }
  .err { margin:0 0 16px; background:#3a1a1a; border:1px solid #7a2a2a; color:#ffb0b0;
        padding:10px 12px; border-radius:8px; font-size:13.5px; }
  .hint { margin-top:16px; font-size:12.5px; opacity:.5; }
  .redline { margin-top:12px; font-size:12px; opacity:.45; border-top:1px solid #262626; padding-top:12px; }
</style>
</head>
<body>
<form class="card" method="post" action="/login">
  <h1>解锁密档</h1>
  <p class="sub">{{if .Target}}目标：{{.Target}}{{else}}输入账户口令以启封播放；口令只用于本机解密。{{end}}</p>
  {{if .Err}}<p class="err">{{.Err}}</p>{{end}}
  <label for="password">账户口令</label>
  <input id="password" name="password" type="password" autofocus autocomplete="current-password" required>
  <button type="submit">解锁并进入</button>
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
  :root { color-scheme: dark; }
  body { margin:0; background:#111; color:#e8e8e8; font:15px/1.6 -apple-system,"PingFang SC","Microsoft YaHei",sans-serif;
         display:flex; justify-content:center; padding:36px 24px; box-sizing:border-box; min-height:100vh; }
  main { width:min(100%,780px); }
  h1 { font-size:18px; margin:0 0 8px; }
  p.sub { opacity:.6; font-size:13px; margin:0 0 16px; word-break:break-all; }
  p.sub code { background:#00000055; padding:1px 6px; border-radius:5px; }
  .err { margin:0 0 16px; background:#3a1a1a; border:1px solid #7a2a2a; color:#ffb0b0;
         padding:10px 12px; border-radius:8px; font-size:13.5px; }
  .crumbs { margin:0 0 12px; font-size:13px; opacity:.8; word-break:break-all; }
  .crumbs a { color:#9ecb7a; text-decoration:none; }
  .crumbs a:hover { text-decoration:underline; }
  .crumbs .cur { opacity:.6; }
  .pathbar { display:flex; gap:8px; margin:0 0 14px; }
  .pathbar input { flex:1; min-width:0; box-sizing:border-box; background:#0e0e0e; color:#e8e8e8; border:1px solid #3a3a3a;
         border-radius:8px; padding:9px 11px; font-size:13px; font-family:ui-monospace,Menlo,monospace; }
  .pathbar input:focus { outline:none; border-color:#4a6b3a; }
  .pathbar button { background:#242424; color:#e8e8e8; border:1px solid #3a3a3a; border-radius:8px;
         padding:9px 14px; font-size:13.5px; cursor:pointer; }
  .pathbar button:hover { background:#2e2e2e; }
  .actions { display:flex; align-items:center; gap:14px; margin:0 0 16px; flex-wrap:wrap; }
  .primary { background:#2f4a25; color:#dff5d8; border:1px solid #4a6b3a; border-radius:8px;
         padding:10px 16px; font-size:14px; cursor:pointer; }
  .primary:hover { background:#3a5a2d; }
  .up { color:#9ecb7a; text-decoration:none; font-size:13.5px; }
  .meta { opacity:.55; font-size:12.5px; margin:0 0 14px; }
  ul.dirs { list-style:none; margin:0; padding:0; border:1px solid #242424; border-radius:10px; overflow:hidden; }
  ul.dirs li { display:flex; justify-content:space-between; gap:12px; padding:10px 14px; border-top:1px solid #1e1e1e; }
  ul.dirs li:first-child { border-top:none; }
  ul.dirs a { color:#e8e8e8; text-decoration:none; }
  ul.dirs a:hover { color:#9ecb7a; }
  ul.dirs .n { opacity:.45; font-size:12.5px; white-space:nowrap; }
  .empty { border:1px dashed #3a3a3a; border-radius:10px; padding:18px; text-align:center; opacity:.6; font-size:13.5px; }
  .foot { margin-top:20px; font-size:12.5px; opacity:.55; }
  .foot a { color:#9ecb7a; text-decoration:none; }
</style>
</head>
<body>
<main>
  <h1>选择密档库</h1>
  <p class="sub">当前库根：<code>{{.Root}}</code></p>
  {{if .Err}}<p class="err">{{.Err}}</p>{{end}}
  <nav class="crumbs">{{range .Crumbs}}{{if .Last}}<span class="cur">{{.Name}}</span>{{else}}<a href="{{.Link}}">{{.Name}}</a> / {{end}}{{end}}</nav>
  <form class="pathbar" method="get" action="/library">
    <input name="path" value="{{.Path}}" spellcheck="false" autocomplete="off" autocapitalize="off">
    <button type="submit">转到</button>
  </form>
  <div class="actions">
    <form method="post" action="/library">
      <input type="hidden" name="path" value="{{.Path}}">
      <button class="primary" type="submit">使用此目录作为库根</button>
    </form>
    {{if .Up}}<a class="up" href="{{.Up}}">↑ 上级目录</a>{{end}}
  </div>
  <p class="meta">本目录直接包含 {{.Vaults}} 个密档{{if .IsRoot}}；这就是当前库根{{else}}；选定后按子目录结构递归展示全部密档{{end}}。</p>
  {{if .Dirs}}<ul class="dirs">{{range .Dirs}}<li><a href="{{.Link}}">{{.Name}}/</a><span class="n">{{.Vaults}} 个密档 · {{.SubDirs}} 个子目录</span></li>{{end}}</ul>
  {{else}}<div class="empty">这个目录下没有可进入的子目录。</div>{{end}}
  <p class="foot"><a href="/">← 返回密档库索引</a></p>
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
}).Parse(treeCSS + treeTmpl + playerSrc + indexSrc + librarySrc + loginSrc))

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
