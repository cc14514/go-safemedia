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

  input[type=password], input[type=text], select { -webkit-appearance:none; appearance:none; width:100%;
    background:var(--field); color:var(--text); border:1px solid #3c3c3f; border-radius:var(--radius);
    padding:7px 10px; font:inherit; }
  /* 下拉框保持系统控件观感：深色底 + 右侧自绘指示符（不加装饰） */
  select { padding-right:26px; background-image:linear-gradient(45deg,transparent 50%,#9b9ba1 50%),
    linear-gradient(135deg,#9b9ba1 50%,transparent 50%);
    background-position:calc(100% - 15px) 54%, calc(100% - 10px) 54%; background-size:5px 5px, 5px 5px;
    background-repeat:no-repeat; }
  input:focus, select:focus { outline:none; border-color:var(--focus); box-shadow:0 0 0 3px #4d8de033; }
  select:focus-visible { outline:2px solid var(--focus); outline-offset:2px; }

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
<div class="hint" id="status"></div>
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
{{template "treeCSS"}}
</style>
</head>
<body>
<header class="toolbar">
  <div class="tbwrap">
    <h1>密档库</h1>
    <span class="grow"></span>
    {{if .Sealing}}<a class="btn" href="/seal">封存视频…</a>{{end}}
    <a class="btn primary" href="/library">选择密档库…</a>
  </div>
</header>
<main>
  <p class="sub">库根：<code>{{.Root}}</code> ｜ 共 {{.Count}} 个密档（含子目录）</p>
  {{if .HasTree}}{{template "tree" .Tree}}
  {{else}}<div class="empty">这个目录里没有 .svid 密档。<br>{{if .Sealing}}把视频拖到窗口上，或点右上角「封存视频…」。{{else}}先运行 <code>safemedia seal &lt;video&gt;</code> 封存视频。{{end}}</div>{{end}}
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
  button.primary { width:100%; margin-top:18px; padding:8px 14px; font-size:13.5px; }
  a.btn.alt { width:100%; margin-top:10px; }
  .err { margin:0 0 16px; background:#3a1a1a; border:1px solid #7a2a2a; color:#ffb0b0;
         padding:10px 12px; border-radius:8px; font-size:13px; }
  .fld + .fld { margin-top:14px; }
</style>
</head>
<body>
<form class="card" method="post" action="/login">
  <h1>解锁密档</h1>
  <p class="sub">{{if .Target}}目标：{{.Target}}{{end}}</p>
  {{if .Err}}<p class="err">{{.Err}}</p>{{end}}
  {{if .Accounts}}<div class="fld">
    <label for="account">账户</label>
    <select id="account" name="account">{{range .Accounts}}<option value="{{.ID}}"{{if eq .ID $.Selected}} selected{{end}}>{{.Label}}{{if .Note}} — {{.Note}}{{end}}</option>{{end}}</select>
  </div>{{end}}
  <div class="fld">
    <label for="password">账户口令</label>
    <input id="password" name="password" type="password" autofocus autocomplete="current-password" required>
  </div>
  <button class="primary" type="submit">解锁</button>
  {{if .ManageItems}}<a class="btn alt" href="/accounts">账户管理（创建 / 恢复 / 删除）</a>{{end}}
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
<title>{{if eq .Purpose "seal"}}选择输出目录{{else}}选择密档库{{end}}</title>
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
    <h1>{{if eq .Purpose "seal"}}选择输出目录{{else}}选择密档库{{end}}</h1>
    <span class="grow"></span>
    {{if .Up}}<a class="btn" href="{{.Up}}">↑ 上级目录</a>{{end}}
    <a class="btn" href="{{if eq .Purpose "seal"}}/seal{{else}}/{{end}}">{{if eq .Purpose "seal"}}← 返回封存{{else}}← 返回密档库索引{{end}}</a>
  </div>
</header>
<main>
  <p class="sub">{{.RootLabel}}：<code>{{.Root}}</code></p>
  {{if .Err}}<p class="err">{{.Err}}</p>{{end}}
  <nav class="crumbs">{{range .Crumbs}}{{if .Last}}<span class="cur">{{.Name}}</span>{{else}}<a href="{{.Link}}">{{.Name}}</a><span class="sep">›</span>{{end}}{{end}}</nav>
  <form class="pathbar" method="get" action="/library">
    {{if eq .Purpose "seal"}}<input type="hidden" name="purpose" value="seal">{{end}}
    <input name="path" value="{{.Path}}" spellcheck="false" autocomplete="off" autocapitalize="off">
    <button type="submit">转到</button>
  </form>
  <div class="actions">
    <form method="post" action="{{if eq .Purpose "seal"}}/seal/output{{else}}/library{{end}}">
      <input type="hidden" name="path" value="{{.Path}}">
      <button class="primary" type="submit">{{if eq .Purpose "seal"}}使用此目录作为输出目录{{else}}使用此目录作为库根{{end}}</button>
    </form>
  </div>
  <p class="meta">本目录直接包含 {{.Vaults}} 个密档{{if .IsRoot}}；{{if eq .Purpose "seal"}}这就是当前输出目录{{else}}这就是当前库根{{end}}{{end}}</p>
  {{if .Dirs}}<ul class="dirs">{{range .Dirs}}<li><a href="{{.Link}}" class="row" title="{{.Name}}"><svg class="ico" viewBox="0 0 16 16" width="13" height="13" aria-hidden="true"><path d="M1.5 4.5c0-.8.6-1.4 1.4-1.4h2.8c.4 0 .8.2 1 .5l.9 1.1h5.5c.8 0 1.4.6 1.4 1.4v5.4c0 .8-.6 1.4-1.4 1.4H2.9c-.8 0-1.4-.6-1.4-1.4V4.5z" fill="#5f92d8" fill-opacity=".9"/></svg>{{.Name}}/</a><span class="n">{{.Vaults}} 个密档 · {{.SubDirs}} 个子目录</span></li>{{end}}</ul>
  {{else}}<div class="empty">这个目录下没有可进入的子目录。</div>{{end}}
</main>
</body>
</html>{{end}}`

// accountsCSS 是账户管理页（US9）的共享样式：只加不删，避免影响既有页面。
const accountsCSS = `{{define "accountsCSS"}}
  textarea { -webkit-appearance:none; appearance:none; width:100%; background:var(--field); color:var(--text);
    border:1px solid #3c3c3f; border-radius:var(--radius); padding:8px 10px; font:inherit;
    font-family:ui-monospace,"SF Mono",Menlo,monospace; font-size:12.5px; resize:vertical; }
  textarea:focus { outline:none; border-color:var(--focus); box-shadow:0 0 0 3px #4d8de033; }
  .notice { margin:0 0 16px; background:#16301a; border:1px solid #2f6b34; color:#bfe9c2;
    padding:10px 12px; border-radius:8px; font-size:13px; }
  .warnbox { margin:14px 0 0; background:#33260f; border:1px solid #6d5420; color:#f0d9a8;
    padding:10px 12px; border-radius:8px; font-size:12.5px; line-height:1.6; }
  button.danger, a.btn.danger { background:#4a2325; border-color:#7a3336; color:#ffc9c9; }
  button.danger:hover, a.btn.danger:hover { background:#5a2a2c; }
  button.danger:active, a.btn.danger:active { background:#3d1c1e; }
  ul.list { list-style:none; margin:0; padding:0; background:var(--panel); border:1px solid var(--line-soft);
    border-radius:10px; overflow:hidden; }
  ul.list li { display:flex; align-items:center; gap:12px; padding:9px 14px; border-top:1px solid #26262a; }
  ul.list li:first-child { border-top:none; }
  ul.list .grow { flex:1; min-width:0; }
  ul.list .nm { font-weight:500; }
  ul.list .ad { color:var(--dim); font-size:12px; margin-left:8px; }
  .empty { border:1px dashed var(--line); border-radius:10px; padding:22px; text-align:center;
    color:var(--dim); font-size:13px; }
  .card { background:var(--panel); border:1px solid #2e2e31; border-radius:12px;
    padding:20px 20px 18px; margin:0 0 18px; box-shadow:0 14px 40px rgba(0,0,0,.28); }
  .card h2 { font-size:13px; font-weight:600; margin:0 0 12px; }
  .card .sub { color:var(--dim); font-size:12.5px; margin:0 0 14px; word-break:break-all; }
  .card .note { color:var(--dim); font-size:12px; margin:6px 0 0; line-height:1.5; }
  .fld + .fld { margin-top:14px; }
  .actions { display:flex; align-items:center; gap:10px; margin:0; flex-wrap:wrap; }
  ol.mn { list-style:none; margin:0 0 14px; padding:0; display:grid; grid-template-columns:repeat(4,1fr); gap:6px 10px; }
  ol.mn li { display:flex; align-items:baseline; gap:8px; background:var(--field); border:1px solid #2c2c2f;
    border-radius:6px; padding:6px 9px; font-family:ui-monospace,"SF Mono",Menlo,monospace; font-size:12.5px; }
  ol.mn .i { color:var(--dim); font-size:11px; min-width:14px; text-align:right;
    user-select:none; -webkit-user-select:none; }
  ol.mn .w { color:var(--text); user-select:none; -webkit-user-select:none; }
  ul.pick { list-style:none; margin:0; padding:0; }
  ul.pick li { display:flex; align-items:center; gap:10px; margin-bottom:9px; }
  ul.pick .q { color:var(--dim); font-size:12.5px; white-space:nowrap;
    user-select:none; -webkit-user-select:none; }
  ul.pick input { width:200px; }
  p.err, .err { margin:0 0 16px; background:#3a1a1a; border:1px solid #7a2a2a; color:#ffb0b0;
    padding:10px 12px; border-radius:8px; font-size:13px; }
{{end}}`

// accountsSrc 是账户管理页（US9）：列出账户并提供创建 / 恢复 / 删除入口。
const accountsSrc = `{{define "accounts"}}<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>账户管理</title>
<style>
{{template "baseCSS"}}
{{template "accountsCSS"}}
  main { width:min(100%,var(--content)); margin:0 auto; padding:22px 24px 44px; }
  h2.sec { font-size:12px; font-weight:600; color:var(--dim); margin:22px 0 10px;
           user-select:none; -webkit-user-select:none; }
</style>
</head>
<body>
<header class="toolbar">
  <div class="tbwrap">
    <h1>账户管理</h1>
    <span class="grow"></span>
    <a class="btn" href="/">← 返回登录页</a>
  </div>
</header>
<main>
  {{if .Notice}}<p class="notice">{{.Notice}}</p>{{end}}
  {{if .Err}}<p class="err">{{.Err}}</p>{{end}}
  {{if .Accounts}}<ul class="list">{{range .Accounts}}<li>
    <span class="grow"><span class="nm">{{.Label}}</span>{{if .Note}}<span class="ad">{{.Note}}</span>{{end}}</span>
    <a class="btn danger" href="/accounts/delete?path={{.ID | urlquery}}">删除</a>
  </li>{{end}}</ul>
  {{else}}<div class="empty">还没有账户。先创建一个账户，或从助记词（恢复文件）恢复一个。</div>{{end}}
  <h2 class="sec">可用动作</h2>
  <div class="actions">
    <a class="btn primary" href="/accounts/new">创建账户</a>
    <a class="btn" href="/accounts/recover">恢复账户</a>
  </div>
</main>
</body>
</html>{{end}}`

// createSrc 是创建账户的表单页（US9）：口令 + 词数；红线提示只出现在这里（AC9.5）。
const createSrc = `{{define "create"}}<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>创建账户</title>
<style>
{{template "baseCSS"}}
{{template "accountsCSS"}}
  :root { --content:520px; }
  main { width:min(100%,var(--content)); margin:0 auto; padding:22px 24px 44px; }
  label { display:block; font-size:12.5px; color:var(--dim); margin:0 0 6px;
          user-select:none; -webkit-user-select:none; }
  button.primary { width:100%; margin-top:18px; padding:8px 14px; font-size:13.5px; }
</style>
</head>
<body>
<header class="toolbar">
  <div class="tbwrap">
    <h1>创建账户</h1>
    <span class="grow"></span>
    <a class="btn" href="/accounts">← 返回账户管理</a>
  </div>
</header>
<main>
  {{if .Err}}<p class="err">{{.Err}}</p>{{end}}
  <form class="card" method="post" action="/accounts/new">
    {{if .NameLocked}}<div class="fld">
      <label>账户文件</label>
      <p class="sub"><code>{{.Name}}</code></p>
      <p class="note">这是本机第一个账户，固定写入默认账户路径；以后新建的账户再自选名称。</p>
      <input type="hidden" name="name" value="{{.Name}}">
    </div>{{else}}<div class="fld">
      <label for="name">账户名称</label>
      <input id="name" name="name" type="text" value="{{.Name}}" autocomplete="off" spellcheck="false"
             placeholder="例如 工作（留空则用默认名称）">
      <p class="note">会自动补 .json，并写入账户管理页列出的目录；名称以 . 开头或含路径分隔符会被拒绝。</p>
    </div>{{end}}
    <div class="fld">
      <label for="words">助记词词数</label>
      <select id="words" name="words">
        <option value="12"{{if eq .Words 12}} selected{{end}}>12 词（推荐）</option>
        <option value="24"{{if eq .Words 24}} selected{{end}}>24 词</option>
      </select>
    </div>
    <div class="fld">
      <label for="password">登录口令</label>
      <input id="password" name="password" type="password" required autocomplete="new-password" autofocus>
    </div>
    <div class="fld">
      <label for="password2">确认口令</label>
      <input id="password2" name="password2" type="password" required autocomplete="new-password">
    </div>
    <button class="primary" type="submit">生成助记词</button>
    <div class="warnbox">本工具只新建账户，绝不导入持有真实资产的钱包私钥或助记词。助记词只用于重建本工具自己创建的账户，请离线抄写保存。</div>
  </form>
</main>
</body>
</html>{{end}}`

// mnemonicSrc 是助记词抄写 + 复述抽查页（US9）。
// 不放「复制」按钮、词表不可选中、响应 no-store（AC9.2/AC9.12）。
const mnemonicSrc = `{{define "mnemonic"}}<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>抄写助记词</title>
<style>
{{template "baseCSS"}}
{{template "accountsCSS"}}
  :root { --content:640px; }
  main { width:min(100%,var(--content)); margin:0 auto; padding:22px 24px 44px; }
  button.primary { width:100%; margin-top:16px; padding:8px 14px; font-size:13.5px; }
  button.danger { width:100%; margin-top:10px; padding:7px 14px; font-size:13px; }
</style>
</head>
<body>
<header class="toolbar">
  <div class="tbwrap"><h1>抄写助记词</h1><span class="grow"></span></div>
</header>
<main>
  {{if .Err}}<p class="err">{{.Err}}</p>{{end}}
  <div class="card">
    <h2>助记词（{{len .Words}} 词）</h2>
    <p class="sub">请立刻用纸笔按序号抄写。这是重建账户的唯一凭证，页面关闭后不会再显示。</p>
    <ol class="mn">{{range .Words}}<li><span class="i">{{.Index}}</span><span class="w">{{.Word}}</span></li>{{end}}</ol>
    <p class="sub">确认后将写入：<code>{{.Path}}</code></p>
  </div>
  <form class="card" method="post" action="/accounts/new/confirm">
    <h2>复述抽查</h2>
    <p class="sub">按下面提示复述对应的词，全部正确才会写入账户文件。</p>
    <ul class="pick">{{range .Prompts}}<li>
      <span class="q">第 {{.Index}} 个词</span>
      <input name="{{.Field}}" type="text" autocomplete="off" spellcheck="false" autofocus required>
    </li>{{end}}</ul>
    <input type="hidden" name="token" value="{{.Token}}">
    <button class="primary" type="submit">确认并写入账户</button>
  </form>
  <form method="post" action="/accounts/new/cancel">
    <input type="hidden" name="token" value="{{.Token}}">
    <button class="danger" type="submit">取消（不写入任何文件）</button>
  </form>
</main>
</body>
</html>{{end}}`

// recoverSrc 是恢复账户页（US9）：用助记词重建，或用恢复文件重置登录口令。
const recoverSrc = `{{define "recover"}}<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>恢复账户</title>
<style>
{{template "baseCSS"}}
{{template "accountsCSS"}}
  :root { --content:560px; }
  main { width:min(100%,var(--content)); margin:0 auto; padding:22px 24px 44px; }
  label { display:block; font-size:12.5px; color:var(--dim); margin:0 0 6px;
          user-select:none; -webkit-user-select:none; }
  button.primary { width:100%; margin-top:16px; padding:8px 14px; font-size:13.5px; }
</style>
</head>
<body>
<header class="toolbar">
  <div class="tbwrap">
    <h1>恢复账户</h1>
    <span class="grow"></span>
    <a class="btn" href="/accounts">← 返回账户管理</a>
  </div>
</header>
<main>
  {{if .Err}}<p class="err">{{.Err}}</p>{{end}}
  <div class="warnbox">只恢复你自己的 go-safemedia 账户：不要把持有真实资产的钱包助记词输入本工具。</div>
  <form class="card" method="post" action="/accounts/recover">
    <h2>用助记词重建</h2>
    <input type="hidden" name="mode" value="mnemonic">
    <div class="fld">
      <label for="mnemonic">助记词（按顺序、空格分隔）</label>
      <textarea id="mnemonic" name="mnemonic" rows="3" spellcheck="false" autocapitalize="off" autocomplete="off"></textarea>
    </div>
    <div class="fld">
      <label for="rc-name">写入账户名称</label>
      <input id="rc-name" name="name" type="text" autocomplete="off" spellcheck="false" placeholder="留空 = 覆盖当前配置的账户">
      <p class="note">留空时会覆盖当前配置的账户文件（原文件备份为 .bak）；填名称则新建到账户管理页列出的目录里。</p>
    </div>
    <div class="fld">
      <label for="rc-pw">新的登录口令</label>
      <input id="rc-pw" name="password" type="password" autocomplete="new-password">
    </div>
    <div class="fld">
      <label for="rc-pw2">确认口令</label>
      <input id="rc-pw2" name="password2" type="password" autocomplete="new-password">
    </div>
    <button class="primary" type="submit">重建并写入</button>
  </form>
  <form class="card" method="post" action="/accounts/recover">
    <h2>用恢复文件重置登录口令</h2>
    <input type="hidden" name="mode" value="recovery">
    <div class="fld">
      <label for="recovery_path">恢复文件路径</label>
      <input id="recovery_path" name="recovery_path" type="text" autocomplete="off" spellcheck="false"
             placeholder="例如 ~/.safemedia/recovery.json">
      <p class="note">支持 ~ 展开；需要该文件自己的恢复口令（与登录口令不同）。</p>
    </div>
    <div class="fld">
      <label for="recovery_password">恢复口令</label>
      <input id="recovery_password" name="recovery_password" type="password" autocomplete="off">
    </div>
    <div class="fld">
      <label for="rf-name">写入账户名称</label>
      <input id="rf-name" name="name" type="text" autocomplete="off" spellcheck="false" placeholder="留空 = 覆盖当前配置的账户">
    </div>
    <div class="fld">
      <label for="rf-pw">新的登录口令</label>
      <input id="rf-pw" name="password" type="password" autocomplete="new-password">
    </div>
    <div class="fld">
      <label for="rf-pw2">确认口令</label>
      <input id="rf-pw2" name="password2" type="password" autocomplete="new-password">
    </div>
    <button class="primary" type="submit">重置登录口令</button>
  </form>
</main>
</body>
</html>{{end}}`

// deleteSrc 是删除账户的确认页（US9）：必须说明「密档将永久无法启封」并要口令授权。
const deleteSrc = `{{define "delete"}}<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>删除账户</title>
<style>
{{template "baseCSS"}}
{{template "accountsCSS"}}
  :root { --content:520px; }
  main { width:min(100%,var(--content)); margin:0 auto; padding:22px 24px 44px; }
  label { display:block; font-size:12.5px; color:var(--dim); margin:0 0 6px;
          user-select:none; -webkit-user-select:none; }
  .warnbox { margin-bottom:16px; }   /* 与下方「口令」标签拉开距离 */
  button.danger { width:100%; margin-top:18px; padding:8px 14px; font-size:13.5px; }
</style>
</head>
<body>
<header class="toolbar">
  <div class="tbwrap">
    <h1>删除账户</h1>
    <span class="grow"></span>
    <a class="btn" href="/accounts">← 返回账户管理</a>
  </div>
</header>
<main>
  {{if .Err}}<p class="err">{{.Err}}</p>{{end}}
  {{if .Path}}<form class="card" method="post" action="/accounts/delete">
    <h2>要删除的账户</h2>
    <p class="sub"><code>{{.Path}}</code></p>
    <div class="warnbox">删除后这个账户无法再解锁它所封存的密档：<b>该账户封的密档将永久无法启封</b>（密档文件本身保留、不会删除）。若你仍持有它的助记词或恢复文件，之后可以重建账户再启封。账户文件会被移入回收目录，可手动还原。</div>
    <div class="fld">
      <label for="password">该账户的登录口令（确认是你本人）</label>
      <input id="password" name="password" type="password" required autofocus autocomplete="current-password">
    </div>
    <input type="hidden" name="path" value="{{.Path}}">
    <button class="danger" type="submit">确认删除</button>
  </form>
  {{else}}<div class="empty">请从账户管理页选择要删除的账户。</div>{{end}}
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
}).Parse(baseCSS + accountsCSS + sealCSS + treeCSS + treeTmpl + playerSrc + indexSrc + librarySrc + loginSrc +
	accountsSrc + createSrc + mnemonicSrc + recoverSrc + deleteSrc + sealSrc))

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
func indexPage(tree treeNode, root string, sealing bool) string {
	var b strings.Builder
	err := pageTmpl.ExecuteTemplate(&b, "index", struct {
		Tree    treeCtx
		HasTree bool
		Count   int
		Root    string
		Sealing bool
	}{
		Tree:    treeCtx{Node: tree},
		HasTree: !tree.empty(),
		Count:   tree.count(),
		Root:    root,
		Sealing: sealing,
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

	// RootLabel 是「当前库根 / 当前输出目录」这行的标题（US10.3 复用同一个选择器）。
	RootLabel string
	// Purpose 为空表示选库根（POST /library）；"seal" 表示选封存输出目录（POST /seal/output）。
	Purpose string
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

// loginView 是登录页模板的数据。
type loginView struct {
	Target      string          // 目标描述（库根/密档名）
	Err         string          // 上次失败原因
	Accounts    []AccountOption // 可选账户（空 = 不渲染选择器）
	Selected    string          // 上次提交的账户标识（重绘时保持选中）
	ManageItems bool            // 是否显示「账户管理」入口（US9）
}

// accountsView 是账户管理列表页的数据。
type accountsView struct {
	Accounts []AccountOption
	Notice   string
	Err      string
}

// createView 是创建账户表单页的数据（不含任何口令）。
type createView struct {
	Name       string // 目标名称（无账户时为固定默认名）
	NameLocked bool   // 目标名称不可改（本机第一个账户）
	Words      int    // 助记词词数
	Err        string
}

// mnemonicView 是助记词抄写 + 复述抽查页的数据。
type mnemonicView struct {
	Token   string
	Words   []numberedWord
	Prompts []promptItem
	Path    string
	Err     string
}

// recoverView 是恢复账户页的数据（绝不回显口令与助记词）。
type recoverView struct {
	Mode         string // mnemonic / recovery
	Name         string
	RecoveryPath string
	Err          string
}

// deleteView 是删除账户确认页的数据。
type deleteView struct {
	Path string
	Err  string
}

// accountsPage / createPage / mnemonicPage / recoverPage / deletePage 渲染 US9 的页面。
// 与其它页面一致：模板执行失败必须可见（renderError），不得静默截断。
func accountsPage(v accountsView) string { return renderTemplate("accounts", v) }
func createPage(v createView) string     { return renderTemplate("create", v) }
func mnemonicPage(v mnemonicView) string { return renderTemplate("mnemonic", v) }
func recoverPage(v recoverView) string   { return renderTemplate("recover", v) }
func deletePage(v deleteView) string     { return renderTemplate("delete", v) }

// renderTemplate 执行指定模板；失败时返回可见的错误页。
func renderTemplate(name string, data any) string {
	var b strings.Builder
	if err := pageTmpl.ExecuteTemplate(&b, name, data); err != nil {
		return renderError(err)
	}
	return b.String()
}

// loginPage 渲染锁定态的登录页。
// 与其它页面一致：模板执行失败必须可见（renderError），不得静默截断。
func loginPage(v loginView) string {
	var b strings.Builder
	if err := pageTmpl.ExecuteTemplate(&b, "login", v); err != nil {
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

// sealCSS 是封存面板（US10）的样式：只加不删，避免影响既有页面。
const sealCSS = `{{define "sealCSS"}}
  ul.jobs { list-style:none; margin:0; padding:0; background:var(--panel); border:1px solid var(--line-soft);
            border-radius:10px; overflow:hidden; }
  ul.jobs li { padding:9px 14px; border-top:1px solid #26262a; }
  ul.jobs li:first-child { border-top:none; }
  .jrow { display:flex; align-items:baseline; gap:10px; }
  .jrow .nm { flex:1; min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  .jrow .st { color:var(--dim); font-size:12px; white-space:nowrap; }
  .jrow .st.bad { color:#ffb0b0; }
  .jrow .st.ok { color:#8fd18f; }
  .bar { display:block; height:5px; margin-top:7px; border-radius:3px; background:#ffffff14; overflow:hidden; }
  .bar i { display:block; height:100%; width:0; background:#3f9f5f; border-radius:3px; transition:width .35s ease; }
  .bar.wait i { background:#6a6a72; }
  .reason { color:#ffb0b0; font-size:12px; margin-top:5px; word-break:break-all; }
  .warn { margin:0 0 14px; background:#3a2f16; border:1px solid #7a6224; color:#ffd894;
          padding:10px 12px; border-radius:8px; font-size:13px; }
  .spin { display:inline-block; width:9px; height:9px; border-radius:50%; background:#3f9f5f; margin-right:6px; }
{{end}}`

// sealSrc 是封存面板页（US10.2）：实时进度 + 输出目录 + 取消。
// 进度用 1s 轮询 /seal/status（同源 fetch），页面本身不做任何解密/文件访问。
const sealSrc = `{{define "seal"}}<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>封存视频</title>
<style>
{{template "baseCSS"}}
{{template "sealCSS"}}
  main { width:min(100%,var(--content)); margin:0 auto; padding:22px 24px 44px; }
  p.sub { color:var(--dim); font-size:12.5px; margin:0 0 16px; word-break:break-all; }
  .empty { border:1px dashed var(--line); border-radius:10px; padding:26px; text-align:center;
           color:var(--dim); font-size:13px; }
  .actions { display:flex; align-items:center; gap:10px; margin:16px 0 0; }
  p.summary { color:var(--dim); font-size:12.5px; margin:14px 0 0; }
</style>
</head>
<body>
<header class="toolbar">
  <div class="tbwrap">
    <h1>封存视频</h1>
    <span class="grow"></span>
    <a class="btn" href="/library?purpose=seal">更改输出目录…</a>
    <a class="btn" href="/">← 返回密档库</a>
  </div>
</header>
<main>
  <p class="sub">输出目录：<code id="out">{{.Status.Output}}</code></p>
  <div id="rejects"></div>
  <ul class="jobs" id="jobs"></ul>
  <div class="empty" id="empty">把视频文件或文件夹拖到窗口上即可开始封存。</div>
  <p class="summary" id="summary"></p>
  <div class="actions" id="actions">
    <form method="post" action="/seal/cancel"><button type="submit">取消封存</button></form>
  </div>
</main>
<script>
// 首屏状态直接以「JS 对象字面量」注入（html/template 在 script 上下文里把 Go 结构体
// 编码成合法 JS 字面量）。**不要**把它塞进 <script type="application/json"> 再
// JSON.parse(textContent)——那样模板会把字符串再包一层转义，parse 出来是个字符串、
// 页面首屏会静默渲染成空（曾发生；无头 Chrome 的首屏用例钉住它）。
const jobs = document.getElementById('jobs');
const empty = document.getElementById('empty');
const summary = document.getElementById('summary');
const rejects = document.getElementById('rejects');
const outEl = document.getElementById('out');
const actions = document.getElementById('actions');

function esc(s) {
  return String(s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}

function render(d) {
  if (d.output) outEl.textContent = d.output;
  if (d.rejects && d.rejects.length) {
    rejects.innerHTML = '<div class="warn">' + d.rejects.map(r =>
      esc(r.path) + '：' + esc(r.reason)).join('<br>') + '</div>';
  } else {
    rejects.innerHTML = '';
  }
  const items = d.items || [];
  empty.style.display = items.length ? 'none' : 'block';
  jobs.innerHTML = items.map(it => {
    const cls = it.state === '失败' ? 'st bad' : (it.state === '完成' ? 'st ok' : 'st');
    const bar = it.state === '待封存' ? 'bar wait' : 'bar';
    const reason = it.reason ? '<div class="reason">' + esc(it.reason) + '</div>' : '';
    const size = it.total ? (it.done + ' / ' + it.total) : '';
    return '<li><div class="jrow"><span class="nm">' + esc(it.name) + '</span>' +
      '<span class="' + cls + '">' + esc(it.state) + ' ' + (it.percent || 0) + '%</span></div>' +
      '<span class="' + bar + '"><i style="width:' + (it.percent || 0) + '%"></i></span>' +
      '<div class="reason" style="color:var(--dim)">' + esc(size) + '</div>' + reason + '</li>';
  }).join('');
  summary.textContent = d.active ? ('封存中… ' + (d.summary || '')) : (d.summary || '');
  actions.style.display = d.active ? 'flex' : 'none';
}

const boot = {{.Status}};
render(boot);
var timer = setInterval(tick, 1000);

async function tick() {
  try {
    const r = await fetch('/seal/status', {cache: 'no-store'});
    if (r.status === 403 || r.status === 410) { location.reload(); return; }
    if (r.ok) render(await r.json());
  } catch (e) { /* 服务已关闭：停止轮询 */ clearInterval(timer); }
}
</script>
</body>
</html>{{end}}`

// sealPanelView 是封存面板的数据。
type sealPanelView struct {
	Target string
	Status sealStatus
}

// sealPanelPage 渲染封存面板（模板执行失败必须可见，不得静默截断）。
func sealPanelPage(v sealPanelView) string {
	return renderTemplate("seal", v)
}
