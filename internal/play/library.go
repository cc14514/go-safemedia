package play

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
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
	Target      string // 登录页展示的库路径
	IdleTimeout time.Duration
	Now         func() time.Time
	OnLock      func(reason string)

	// SoftLock=true 时锁定时不关停服务，改为回到登录页（桌面外壳）；
	// 默认 false：锁定即关停（CLI 既有行为）。
	SoftLock bool
}

// Library 是目录（多密档）播放服务。
type Library struct {
	opts    LibraryOptions
	core    *core
	token   string
	byID    map[string]Entry
	order   []Entry
	tree    treeNode
	handler http.Handler

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
	token, err := newToken()
	if err != nil {
		return nil, err
	}
	l := &Library{
		opts:  opts,
		priv:  opts.Priv,
		token: token,
		byID:  make(map[string]Entry, len(opts.Entries)),
		order: make([]Entry, 0, len(opts.Entries)),
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
	for _, e := range opts.Entries {
		raw := make([]byte, 8)
		if _, err := rand.Read(raw); err != nil {
			return nil, err
		}
		e.id = hex.EncodeToString(raw)
		l.byID[e.id] = e
		l.order = append(l.order, e)
	}
	l.tree = buildTree(l.order)

	mux := http.NewServeMux()
	mux.HandleFunc("/", l.handleIndex)
	mux.HandleFunc("/watch/", l.handleWatch)
	mux.HandleFunc("/media/"+l.token+"/", l.handleMedia)
	mux.HandleFunc("/lock", handleLockRequest(l.core))
	l.handler = l.core.gate(mux)
	return l, nil
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
func (l *Library) Count() int { return len(l.order) }

func (l *Library) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}
	writeNoCacheHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, indexPage(l.tree))
}

func (l *Library) handleWatch(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/watch/")
	e, ok := l.byID[id]
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeNoCacheHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 播放页左侧常驻目录树（AC5.5），当前条目高亮。
	fmt.Fprint(w, playerPage(e.Name, "/media/"+l.token+"/"+e.id, "/", l.tree, "/watch/"+e.id))
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
	e, ok := l.byID[id]
	if !ok {
		http.NotFound(w, r)
		return
	}

	// 按需启封：只在本次响应期间持有该密档的文件密钥。
	// 持读锁取私钥并启封，避免与 wipe（写锁）并发访问同一把私钥。
	l.mu.RLock()
	priv := l.priv
	if priv == nil {
		l.mu.RUnlock()
		http.Error(w, "未解锁：请先在登录页输入口令", http.StatusForbidden)
		return
	}
	vr, err := vault.Open(e.Path, priv)
	l.mu.RUnlock()
	if err != nil {
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
