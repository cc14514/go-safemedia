package play

import (
	"html/template"
	"strings"
)

var playerTmpl = template.Must(template.New("player").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>{{.Title}}</title>
<style>
  :root { color-scheme: dark; }
  body { margin:0; background:#111; color:#e8e8e8; font:15px/1.6 -apple-system,"PingFang SC","Microsoft YaHei",sans-serif;
         display:flex; flex-direction:column; align-items:center; min-height:100vh; padding:24px; box-sizing:border-box; }
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
</style>
</head>
<body>
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
<div class="hint">视频通过本机 127.0.0.1 环回解密播放；关闭命令行窗口即结束会话。</div>

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
  try { await fetch('/lock', { method: 'POST' }); } catch (e) {}
  v.pause(); v.removeAttribute('src'); v.load();
  document.documentElement.dataset.decode = 'locked';
  document.getElementById('lock').disabled = true;
  document.querySelector('.hint').textContent = '已锁定：密钥已从内存清零，服务已关闭。';
  alert('已锁定：密钥已从内存清零，服务已关闭。');
});
</script>
</body>
</html>`))

var indexTmpl = template.Must(template.New("index").Parse(`<!doctype html>
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
  ul { list-style:none; padding:0; margin:0; }
  li { border:1px solid #2c2c2c; border-radius:10px; margin-bottom:10px; background:#181818; }
  li a { display:flex; justify-content:space-between; gap:16px; align-items:center; padding:14px 18px;
         color:#e8e8e8; text-decoration:none; }
  li a:hover { background:#202020; }
  .name { word-break:break-all; }
  .size { opacity:.5; font-size:12.5px; white-space:nowrap; }
  .empty { border:1px dashed #3a3a3a; border-radius:10px; padding:26px; text-align:center; opacity:.6; }
  .foot { margin-top:22px; font-size:12.5px; opacity:.45; }
</style>
</head>
<body>
<main>
  <h1>密档库</h1>
  <p class="sub">共 {{len .Rows}} 个密档。条目只显示密档文件名，不包含原始视频名等元数据。</p>
  {{if .Rows}}
  <ul>
    {{range .Rows}}<li><a href="{{.Link}}"><span class="name">{{.Name}}</span><span class="size">{{.Size}}</span></a></li>
    {{end}}
  </ul>
  {{else}}
  <div class="empty">这个目录里没有 .svid 密档。<br>先运行 <code>safemedia seal &lt;video&gt;</code> 封存视频。</div>
  {{end}}
  <div class="foot">全部解密只在本机 127.0.0.1 环回进行；关闭命令行窗口即结束会话。</div>
</main>
</body>
</html>`))

// indexRow 是索引页一行。
type indexRow struct {
	Name string
	Size string
	Link string
}

// playerPage 渲染播放页。Title 做 HTML 转义（密档文件名可能含任意字符）。
// back 为空则不显示返回链接。
func playerPage(name, mediaPath, back string) string {
	title := strings.TrimSpace(name)
	if title == "" {
		title = "go-safemedia 播放"
	}
	var b strings.Builder
	_ = playerTmpl.Execute(&b, struct {
		Title string
		Media string
		Back  string
	}{Title: title, Media: mediaPath, Back: back})
	return b.String()
}

// indexPage 渲染目录索引页。
func indexPage(rows []indexRow) string {
	var b strings.Builder
	_ = indexTmpl.Execute(&b, struct{ Rows []indexRow }{Rows: rows})
	return b.String()
}
