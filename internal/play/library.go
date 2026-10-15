package play

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cc14514/go-safemedia/internal/vault"
)

// VaultSuffix 是密档扩展名（与 cli.VaultExt 一致；此处不引 cli 以免环依赖）。
const VaultSuffix = ".svid"

// Entry 是索引页中的一个密档条目。
type Entry struct {
	Name string // 展示名（密档文件名，不回显原始视频名）
	Rel  string // 相对库根的路径（以 / 分隔），用于目录树分组
	Path string // 磁盘路径
	Size int64  // 密档体积（字节）
	id   string // 内部随机 id，仅用于 URL
}

// treeNode 是渲染用的目录节点（AC5.4）。
type treeNode struct {
	Name  string // 目录名（库根为空）
	Files []treeFile
	Dirs  []treeNode
}

// treeFile 是目录树中的一个密档叶子。
type treeFile struct {
	Label string // 相对库根的路径（悬停提示，便于同名文件区分）
	Name  string // 展示名
	Size  string
	Link  string // /watch/<id>
}

func (n *treeNode) empty() bool { return len(n.Files) == 0 && len(n.Dirs) == 0 }

func (n *treeNode) count() int {
	c := len(n.Files)
	for i := range n.Dirs {
		c += n.Dirs[i].count()
	}
	return c
}

// buildTree 把条目按相对路径组织成目录树。
func buildTree(entries []Entry) treeNode {
	var root treeNode
	for _, e := range entries {
		segs := strings.Split(e.Rel, "/")
		insertInto(&root, segs[:len(segs)-1], treeFile{
			Label: e.Rel,
			Name:  e.Name,
			Size:  humanBytes(e.Size),
			Link:  "/watch/" + e.id,
		})
	}
	sortNode(&root)
	return root
}

// insertInto 把叶子插入到 dirs 指定的层级（按需创建中间目录）。
func insertInto(n *treeNode, dirs []string, f treeFile) {
	if len(dirs) == 0 {
		n.Files = append(n.Files, f)
		return
	}
	for i := range n.Dirs {
		if n.Dirs[i].Name == dirs[0] {
			insertInto(&n.Dirs[i], dirs[1:], f)
			return
		}
	}
	n.Dirs = append(n.Dirs, treeNode{Name: dirs[0]})
	insertInto(&n.Dirs[len(n.Dirs)-1], dirs[1:], f)
}

// sortNode 递归按名称排序（目录在前，密档在后）。
func sortNode(n *treeNode) {
	sort.Slice(n.Dirs, func(i, j int) bool { return n.Dirs[i].Name < n.Dirs[j].Name })
	sort.Slice(n.Files, func(i, j int) bool { return n.Files[i].Name < n.Files[j].Name })
	for i := range n.Dirs {
		sortNode(&n.Dirs[i])
	}
}

// LibraryOptions 是目录模式播放服务的配置。
//
// 目录模式按需启封：每次媒体请求才用私钥解出该密档的文件密钥，
// 响应结束立即 Close 清零。锁定/关闭时私钥也会被尽力清零。
type LibraryOptions struct {
	// Priv 是预解锁路径（CLI 与测试）：提供后服务以解锁态启动，不需要登录页。
	Priv *ecdsa.PrivateKey
	// Unlock 是延迟解锁路径（桌面外壳）：服务以锁定态启动，登录页提交口令后
	// 调用本回调取得私钥。与 Priv 二选一；回调实现方须在使用后清零口令字节。
	Unlock func(password string) (*ecdsa.PrivateKey, error)

	Entries     []Entry
	Target      string // 登录页展示的目标描述（通常为库根路径）
	Root        string // 当前库根；为空则回落到 Target。US7 目录选择页以此为起点
	IdleTimeout time.Duration
	Now         func() time.Time
	OnLock      func(reason string)

	// OnLibraryChange 在页面切换库根成功后调用（参数为新的绝对路径），用于持久化
	// 选择（桌面外壳写 desktop.json）。为 nil 表示只在本会话生效（CLI）。
	// 返回错误时页面会显示出来，但库根**已经**切换（不静默丢选择）。
	OnLibraryChange func(root string) error

	// SoftLock=true 时锁定时不关停服务，改为回到登录页（桌面外壳）；
	// 默认 false：锁定即关停（CLI 既有行为）。
	SoftLock bool
}

// Library 是目录（多密档）播放服务。
type Library struct {
	opts    LibraryOptions
	core    *core
	token   string
	root    string
	byID    map[string]Entry
	order   []Entry
	tree    treeNode
	handler http.Handler

	// mu 保护下列可变状态：priv（解锁/清密钥）与 root/byID/order/tree
	// （US7 切换库根会整体替换）。清密钥必须与解密读互斥，见 AGENTS.md 易错点。
	mu   sync.RWMutex
	priv *ecdsa.PrivateKey
}

// ScanLibrary 递归扫描目录下的 .svid（AC5.4），返回按相对路径排序的条目。
//
// 跳过：隐藏项（`.` 开头）、符号链接（避免越出库根）、非 .svid 文件。
func ScanLibrary(dir string) ([]Entry, error) {
	root := filepath.Clean(dir)
	var entries []Entry
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil // 子目录不可读：跳过该分支，不让整次扫描失败
		}
		if p == root {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(name), VaultSuffix) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		entries = append(entries, Entry{
			Name: name,
			Rel:  filepath.ToSlash(rel),
			Path: p,
			Size: info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Rel < entries[j].Rel })
	return entries, nil
}

// NewLibrary 构造目录播放服务（未启动）。允许空列表（AC5.2 显示空列表提示）。
func NewLibrary(opts LibraryOptions) (*Library, error) {
	if opts.Priv == nil && opts.Unlock == nil {
		return nil, errors.New("目录播放需要账户私钥用于按需启封")
	}
	root := opts.Root
	if root == "" {
		root = opts.Target
	}
	if opts.Target == "" {
		opts.Target = root
	}
	token, err := newToken()
	if err != nil {
		return nil, err
	}
	byID, order, err := assignIDs(opts.Entries)
	if err != nil {
		return nil, err
	}
	l := &Library{
		opts:  opts,
		priv:  opts.Priv,
		token: token,
		root:  root,
		byID:  byID,
		order: order,
	}
	session := ""
	if opts.Priv == nil {
		if session, err = newToken(); err != nil {
			return nil, err
		}
	}
	l.core = newCore(coreConfig{
		idle:        opts.IdleTimeout,
		now:         opts.Now,
		onLock:      opts.OnLock,
		onLockExtra: l.wipe,
		softLock:    opts.SoftLock,
		mediaPrefix: "/media/" + token + "/",
		target:      opts.Target,
		unlockFn:    l.doUnlock,
		session:     session,
		preUnlocked: opts.Priv != nil,
	})
	l.tree = buildTree(l.order)

	mux := http.NewServeMux()
	mux.HandleFunc("/", l.handleIndex)
	mux.HandleFunc("/watch/", l.handleWatch)
	mux.HandleFunc("/library", l.handleLibrary)
	mux.HandleFunc("/library/", l.handleLibrary)
	mux.HandleFunc("/media/"+l.token+"/", l.handleMedia)
	mux.HandleFunc("/lock", handleLockRequest(l.core))
	l.handler = l.core.gate(mux)
	return l, nil
}

// assignIDs 给每个条目分配内部随机 id（URL 用）。
func assignIDs(entries []Entry) (map[string]Entry, []Entry, error) {
	byID := make(map[string]Entry, len(entries))
	order := make([]Entry, 0, len(entries))
	for _, e := range entries {
		raw := make([]byte, 8)
		if _, err := rand.Read(raw); err != nil {
			return nil, nil, err
		}
		e.id = hex.EncodeToString(raw)
		byID[e.id] = e
		order = append(order, e)
	}
	return byID, order, nil
}

// SetRoot 切换库根并立即重扫（US7）。返回新条目数。
//
// 失败时不改动任何现有状态（AC7.5）；成功后在 l.mu 下整体替换 root/byID/order/tree，
// 并刷新登录页的目标文案。持久化由调用方通过 LibraryOptions.OnLibraryChange 决定。
func (l *Library) SetRoot(root string) (int, error) {
	abs, err := normalizeDir(root)
	if err != nil {
		return 0, err
	}
	entries, err := ScanLibrary(abs)
	if err != nil {
		return 0, err
	}
	byID, order, err := assignIDs(entries)
	if err != nil {
		return 0, err
	}
	tree := buildTree(order)

	l.mu.Lock()
	l.root = abs
	l.byID = byID
	l.order = order
	l.tree = tree
	l.mu.Unlock()

	l.core.setTarget(abs)
	return len(order), nil
}

// Root 返回当前库根。
func (l *Library) Root() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.root
}

// normalizeDir 把用户输入的目录规范成绝对路径：支持 ~ 与相对路径；
// 必须是存在的目录，否则返回中文错误（供页面直接展示）。
func normalizeDir(dir string) (string, error) {
	s := strings.TrimSpace(dir)
	if s == "" {
		return "", errors.New("请填写目录路径")
	}
	if s == "~" || strings.HasPrefix(s, "~/") || s == "~\\" || strings.HasPrefix(s, "~\\") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("无法确定用户主目录: %w", err)
		}
		s = filepath.Join(home, strings.TrimLeft(strings.TrimPrefix(s, "~"), `/\`))
	}
	abs, err := filepath.Abs(s)
	if err != nil {
		return "", fmt.Errorf("路径无效: %w", err)
	}
	abs = filepath.Clean(abs)
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("目录不可用：%v", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("不是目录：%s（请填目录，密档库按目录扫描）", abs)
	}
	return abs, nil
}

// doUnlock 是门控的解锁回调：调用 Options.Unlock 并接管新私钥。
func (l *Library) doUnlock(password string) error {
	if l.opts.Unlock == nil {
		return errors.New("该服务未配置解锁方式")
	}
	priv, err := l.opts.Unlock(password)
	if err != nil {
		return err
	}
	if priv == nil {
		return errors.New("解锁失败：未取得账户私钥")
	}
	l.mu.Lock()
	l.priv = priv
	l.mu.Unlock()
	return nil
}

// wipe 尽力清零当前私钥（锁定/关闭时必须调用）。
func (l *Library) wipe() {
	l.mu.Lock()
	defer l.mu.Unlock()
	wipePrivateKey(l.priv)
}

// Handler 返回 HTTP 处理器。
func (l *Library) Handler() http.Handler { return l.handler }

// Start 启动服务并返回索引页 URL。
func (l *Library) Start() (string, error) { return l.core.start(l.handler) }

// URL 返回索引页 URL。
func (l *Library) URL() string { return l.core.url }

// Addr 返回监听地址。
func (l *Library) Addr() string {
	if l.core.listener == nil {
		return ""
	}
	return l.core.listener.Addr().String()
}

// Locked 表示服务是否已锁定。
func (l *Library) Locked() bool { return l.core.locked.Load() }

// Lock 锁定（幂等）。
func (l *Library) Lock(reason string) { l.core.lock(reason) }

// Close 关闭服务并清零密钥。
func (l *Library) Close() error { return l.core.shutdown() }

// Count 返回条目数。
func (l *Library) Count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.order)
}

// handleLibrary 是密档库目录选择页（US7）：GET 浏览目录，POST 切换库根。
//
// 两个动作都挂在门控之内 → 锁定态自动回落登录页（AC7.6 / NFR-8）。
func (l *Library) handleLibrary(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		l.renderPicker(w, r.URL.Query().Get("path"), "")

	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			l.renderPicker(w, l.Root(), "请求解析失败，请重试")
			return
		}
		want := r.PostFormValue("path")
		n, err := l.SetRoot(want)
		if err != nil {
			l.renderPicker(w, want, err.Error())
			return
		}
		if cb := l.opts.OnLibraryChange; cb != nil {
			if err := cb(l.Root()); err != nil {
				// 库根已切换生效，但持久化失败必须可见（不静默丢选择）。
				l.renderPicker(w, l.Root(), fmt.Sprintf("已切换到 %s（%d 个密档），但保存配置失败：%v", l.Root(), n, err))
				return
			}
		}
		writeNoCacheHeaders(w)
		http.Redirect(w, r, "/", http.StatusSeeOther)

	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "只支持 GET/POST", http.StatusMethodNotAllowed)
	}
}

// renderPicker 渲染目录选择页：浏览 wanted（空 = 当前库根）并展示 errMsg。
// 非法路径不让页面失败：回退浏览当前库根，同时把原因显示出来（AC7.5）。
func (l *Library) renderPicker(w http.ResponseWriter, wanted, errMsg string) {
	dir, err := normalizeDir(wanted)
	if err != nil {
		errMsg = combineMsg(errMsg, err.Error())
		dir, _ = normalizeDir(l.Root())
	}
	v := l.pickerView(dir)
	v.Err = combineMsg(errMsg, v.Err)
	renderHTML(w, libraryPage(v))
}

// pickerView 组装目录选择页数据：面包屑、上级、子目录列表（含直含密档数）。
func (l *Library) pickerView(dir string) pickerView {
	root := l.Root()
	v := pickerView{Root: root, Path: dir, IsRoot: samePath(dir, root), Crumbs: buildCrumbs(dir)}
	if up := filepath.Dir(dir); up != dir {
		v.Up = libraryLink(up)
	}
	des, err := os.ReadDir(dir)
	if err != nil {
		v.Err = "无法读取目录：" + err.Error()
		return v
	}
	var vaults int
	dirs := make([]pickerDir, 0, len(des))
	for _, de := range des {
		name := de.Name()
		if strings.HasPrefix(name, ".") || de.Type()&fs.ModeSymlink != 0 {
			continue
		}
		full := filepath.Join(dir, name)
		if !de.IsDir() {
			if strings.EqualFold(filepath.Ext(name), VaultSuffix) {
				vaults++
			}
			continue
		}
		nv, nd := countDirect(full)
		dirs = append(dirs, pickerDir{Name: name, Link: libraryLink(full), Vaults: nv, SubDirs: nd})
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	v.Vaults = vaults
	v.Dirs = dirs
	return v
}

// countDirect 统计目录「直接」包含的密档数与子目录数（不递归；跳过隐藏项与符号链接）。
func countDirect(dir string) (vaults, subdirs int) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	for _, de := range des {
		name := de.Name()
		if strings.HasPrefix(name, ".") || de.Type()&fs.ModeSymlink != 0 {
			continue
		}
		if de.IsDir() {
			subdirs++
			continue
		}
		if strings.EqualFold(filepath.Ext(name), VaultSuffix) {
			vaults++
		}
	}
	return vaults, subdirs
}

// buildCrumbs 生成面包屑（根 → … → 当前目录；最后一段不可点）。
func buildCrumbs(dir string) []pickerCrumb {
	crumbs := []pickerCrumb{{Name: "/", Link: libraryLink("/")}}
	acc := ""
	for _, seg := range strings.Split(filepath.Clean(dir), "/") {
		if seg == "" {
			continue
		}
		acc += "/" + seg
		crumbs = append(crumbs, pickerCrumb{Name: seg, Link: libraryLink(acc)})
	}
	crumbs[len(crumbs)-1].Last = true
	return crumbs
}

// libraryLink 生成目录选择页的链接。
func libraryLink(dir string) string { return "/library?path=" + url.QueryEscape(dir) }

// samePath 判断两个路径是否指向同一目录（忽略尾部斜杠与冗余分隔符）。
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// combineMsg 拼接两条提示（跳过空串）。
func combineMsg(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "；" + b
	}
}

// renderHTML 输出 HTML 页面（统一 no-store：页面与明文都不进任何缓存）。
func renderHTML(w http.ResponseWriter, html string) {
	writeNoCacheHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

func (l *Library) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}
	l.mu.RLock()
	tree, root := l.tree, l.root
	l.mu.RUnlock()
	writeNoCacheHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, indexPage(tree, root))
}

func (l *Library) handleWatch(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/watch/")
	l.mu.RLock()
	e, ok := l.byID[id]
	tree := l.tree
	l.mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeNoCacheHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 播放页左侧常驻目录树（AC5.5），当前条目高亮。
	fmt.Fprint(w, playerPage(e.Name, "/media/"+l.token+"/"+e.id, "/", tree, "/watch/"+e.id))
}

func (l *Library) handleMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "只支持 GET/HEAD", http.StatusMethodNotAllowed)
		return
	}
	if l.core.locked.Load() {
		http.Error(w, "播放已锁定", http.StatusGone)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/media/"+l.token+"/")

	// 按需启封：持读锁取私钥与条目并启封（与 wipe / SetRoot 串行化，
	// 避免「边流边清零」或切换库根时读到半更新状态）。
	l.mu.RLock()
	e, ok := l.byID[id]
	priv := l.priv
	var (
		vr  *vault.Reader
		err error
	)
	if ok && priv != nil {
		vr, err = vault.Open(e.Path, priv)
	}
	l.mu.RUnlock()

	switch {
	case !ok:
		http.NotFound(w, r)
		return
	case priv == nil:
		http.Error(w, "未解锁：请先在登录页输入口令", http.StatusForbidden)
		return
	case err != nil:
		switch {
		case errors.Is(err, vault.ErrNotThisAccount):
			http.Error(w, "该密档不属于当前账户", http.StatusForbidden)
		case errors.Is(err, vault.ErrNotAVault), errors.Is(err, vault.ErrTampered):
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		default:
			http.Error(w, fmt.Sprintf("启封失败: %v", err), http.StatusInternalServerError)
		}
		return
	}
	defer vr.Close()

	writeNoCacheHeaders(w)
	w.Header().Set("Content-Type", vr.ContentType())
	http.ServeContent(w, r, "", time.Time{}, &seeker{r: vr})
}

// wipePrivateKey 尽力清零私钥标量（big.Int 内部表示无法保证完全擦除，仅降低驻留）。
func wipePrivateKey(priv *ecdsa.PrivateKey) {
	if priv == nil {
		return
	}
	if priv.D != nil {
		priv.D.SetInt64(0)
	}
	if priv.X != nil {
		priv.X.SetInt64(0)
	}
	if priv.Y != nil {
		priv.Y.SetInt64(0)
	}
}

// humanBytes 格式化体积。
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
