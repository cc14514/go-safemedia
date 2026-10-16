package play

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cc14514/go-safemedia/internal/vault"
)

// Options 是单密档播放服务的配置。
type Options struct {
	// Reader 是预解锁路径（CLI 与测试）：提供后服务以解锁态启动，不需要登录页。
	Reader *vault.Reader
	// Name 是展示名（通常为密档文件名），同时作为登录页的目标描述。
	Name string
	// Unlock 是延迟解锁路径（桌面外壳）：服务以锁定态启动，
	// 登录页提交「账户 + 口令」后调用本回调取得 Reader。与 Reader 二选一。
	// 回调实现方负责在内部使用后清零口令字节，且不得依赖 play 包；
	// accountID 来自 Accounts 列表，调用前引擎已校验它在列表内。
	Unlock func(accountID, password string) (name string, r *vault.Reader, err error)

	// Accounts 返回登录页可选的账户（US8）。每次渲染都会调用一次，
	// 因此运行中新增/删除账户后无需重启即可在登录页看到（AC9.1）。
	// 返回空列表 = 不渲染选择器，只输入口令。
	Accounts func() []AccountOption

	// AccountsAdmin 启用账户管理页（US9）。为 nil → `/accounts*` 返回 404
	// （CLI 形态：不提供账户管理界面）。
	AccountsAdmin AccountsAdmin

	IdleTimeout time.Duration // 0 → DefaultIdleTimeout
	Now         func() time.Time
	OnLock      func(reason string)

	// SoftLock=true 时锁定时不关停服务，改为回到登录页（桌面外壳）；
	// 默认 false：锁定即关停（CLI 既有行为，AC8.2）。
	SoftLock bool

	// Picker 提供系统原生的「选择视频文件…/选择文件夹…」对话框（US11）。
	// 单密档模式没有封存面板，保留此注入面只为测试与调用方一致。
	Picker FilePicker
}

// Server 是单密档播放服务。
type Server struct {
	opts    Options
	core    *core
	token   string
	handler http.Handler

	mu     sync.RWMutex
	reader *vault.Reader
	name   string
}

// New 构造单密档播放服务（未启动）。
func New(opts Options) (*Server, error) {
	if opts.Reader == nil && opts.Unlock == nil {
		return nil, fmt.Errorf("播放服务缺少密档读取器")
	}
	token, err := newToken()
	if err != nil {
		return nil, err
	}
	s := &Server{opts: opts, token: token, reader: opts.Reader, name: opts.Name}

	// 锁定态启动（需登录）时启用会话校验；预解锁路径无 cookie 可发，不校验。
	session := ""
	if opts.Reader == nil {
		if session, err = newToken(); err != nil {
			return nil, err
		}
	}

	s.core = newCore(coreConfig{
		idle:        opts.IdleTimeout,
		now:         opts.Now,
		onLock:      opts.OnLock,
		onLockExtra: s.wipe,
		softLock:    opts.SoftLock,
		mediaPrefix: "/media/" + token,
		target:      opts.Name,
		accountsFn:  opts.Accounts,
		admin:       opts.AccountsAdmin,
		unlockFn:    s.doUnlock,
		session:     session,
		preUnlocked: opts.Reader != nil,
		dropToken:   randomToken(), // 单密档模式不注入 Sealer → /drop 返回 409
		picker:      opts.Picker,   // 单密档模式无封存面板；保留注入面以便测试
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/drop/", s.core.handleDrop)
	mux.HandleFunc("/media/"+token, s.handleMedia)
	s.handler = s.core.gate(mux)
	return s, nil
}

// doUnlock 是门控的解锁回调：调用 Options.Unlock 并接管新 Reader。
func (s *Server) doUnlock(accountID, password string) error {
	if s.opts.Unlock == nil {
		return fmt.Errorf("该服务未配置解锁方式")
	}
	name, r, err := s.opts.Unlock(accountID, password)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.reader != nil {
		_ = s.reader.Close()
	}
	s.reader = r
	s.name = name
	s.mu.Unlock()
	return nil
}

// wipe 清零当前 Reader 的文件密钥（锁定/关闭时必须调用）。
func (s *Server) wipe() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reader != nil {
		s.reader.Wipe()
	}
}

func (s *Server) currentReader() *vault.Reader {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.reader
}

// displayName 返回展示名（解锁后以解锁回调给出的名字为准）。
func (s *Server) displayName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.name != "" {
		return s.name
	}
	return s.opts.Name
}

// Handler 返回 HTTP 处理器（L1 测试可直接挂到 httptest）。
func (s *Server) Handler() http.Handler { return s.handler }

// MediaPath 返回受 token 保护的媒体路径。
func (s *Server) MediaPath() string { return "/media/" + s.token }

// Start 在 127.0.0.1 的随机端口启动服务，返回播放页 URL。
func (s *Server) Start() (string, error) { return s.core.start(s.handler) }

// URL 返回播放页 URL（Start 之后有效）。
func (s *Server) URL() string { return s.core.url }

// Addr 返回监听地址。
func (s *Server) Addr() string {
	if s.core.listener == nil {
		return ""
	}
	return s.core.listener.Addr().String()
}

// Locked 表示服务是否已锁定（密钥已清零）。
func (s *Server) Locked() bool { return s.core.locked.Load() }

// Lock 锁定播放：清零文件密钥（幂等）。SoftLock=false 时同时关停服务。
func (s *Server) Lock(reason string) { s.core.lock(reason) }

// Close 关闭服务并清零密钥。
func (s *Server) Close() error { return s.core.shutdown() }

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "只支持 GET/HEAD", http.StatusMethodNotAllowed)
		return
	}
	vr := s.currentReader()
	if s.core.locked.Load() || vr == nil {
		http.Error(w, "播放已锁定", http.StatusGone)
		return
	}
	writeNoCacheHeaders(w)
	w.Header().Set("Content-Type", vr.ContentType())

	// 预检：在写出响应头之前先验证请求起点所在的块。
	// 这样被篡改/损坏的密档会得到明确的 500，而不是一个被中途掐断的 200/206 流
	// （AC4.4：报完整性错误且不崩溃）。请求范围之外的块仍由 ServeContent 流式读取，
	// 若其后才遇错，连接会中断——浏览器据此触发 error，播放页随即提示改用 mpv。
	if !probeBeforeServe(vr, r) {
		http.Error(w, "密档损坏或已被篡改：无法解密该位置的块", http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, "", time.Time{}, &seeker{r: vr})
}

// probeBeforeServe 读取请求起点处的一小段以确认可解密。
func probeBeforeServe(vr *vault.Reader, r *http.Request) bool {
	size := vr.Size()
	if size <= 0 {
		return true
	}
	at := int64(0)
	if start, ok := firstRangeStart(r.Header.Get("Range"), size); ok {
		at = start
	}
	if at >= size {
		return true
	}
	n := int64(probeWindow)
	if size-at < n {
		n = size - at
	}
	buf := make([]byte, n)
	if _, err := vr.ReadAt(buf, at); err != nil && err != io.EOF {
		return false
	}
	return true
}

// probeWindow 是响应头前的预检窗口大小。
const probeWindow = 32 << 10

// firstRangeStart 解析单段 Range 的起始偏移；多段或无法解析时退回 0。
func firstRangeStart(header string, size int64) (int64, bool) {
	const p = "bytes="
	if !strings.HasPrefix(header, p) {
		return 0, false
	}
	spec := strings.TrimSpace(header[len(p):])
	if i := strings.IndexByte(spec, ','); i >= 0 {
		spec = spec[:i] // 只取第一段
	}
	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		return 0, false
	}
	startStr, endStr := strings.TrimSpace(spec[:dash]), strings.TrimSpace(spec[dash+1:])
	if startStr == "" { // bytes=-N：末尾 N 字节
		n, err := strconv.ParseInt(endStr, 10, 64)
		if err != nil || n <= 0 {
			return 0, false
		}
		if n > size {
			n = size
		}
		return size - n, true
	}
	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 {
		return 0, false
	}
	return start, true
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}
	writeNoCacheHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, playerPage(s.displayName(), s.MediaPath(), "", treeNode{}, ""))
}

// writeNoCacheHeaders 禁止浏览器缓存明文（威胁模型 b）。
// 门控与媒体响应一律使用（plan.md §5）。
func writeNoCacheHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

// seeker 把 vault.Reader 适配为 http.ServeContent 需要的 io.ReadSeeker。
type seeker struct {
	r   *vault.Reader
	off int64
}

func (s *seeker) Read(p []byte) (int, error) {
	n, err := s.r.ReadAt(p, s.off)
	s.off += int64(n)
	return n, err
}

func (s *seeker) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = s.off + offset
	case io.SeekEnd:
		abs = s.r.Size() + offset
	default:
		return 0, fmt.Errorf("非法 whence: %d", whence)
	}
	if abs < 0 {
		return 0, fmt.Errorf("非法偏移: %d", abs)
	}
	s.off = abs
	return abs, nil
}
