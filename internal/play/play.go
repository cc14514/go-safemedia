package play

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cc14514/go-safemedia/internal/vault"
)

// Options 是单密档播放服务的配置。
type Options struct {
	Reader      *vault.Reader
	Name        string        // 展示名（通常为密档文件名）
	IdleTimeout time.Duration // 0 → DefaultIdleTimeout
	Now         func() time.Time
	OnLock      func(reason string)
}

// Server 是单密档播放服务。
type Server struct {
	opts    Options
	core    *core
	token   string
	handler http.Handler
}

// New 构造单密档播放服务（未启动）。
func New(opts Options) (*Server, error) {
	if opts.Reader == nil {
		return nil, fmt.Errorf("播放服务缺少密档读取器")
	}
	token, err := newToken()
	if err != nil {
		return nil, err
	}
	s := &Server{opts: opts, token: token}
	s.core = newCore(opts.IdleTimeout, opts.Now, opts.OnLock, func() {
		if opts.Reader != nil {
			opts.Reader.Wipe()
		}
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/media/"+token, s.handleMedia)
	mux.HandleFunc("/lock", handleLockRequest(s.core))
	s.handler = s.core.wrap(mux)
	return s, nil
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

// Lock 锁定播放：清零文件密钥、关闭服务、通知回调（幂等）。
func (s *Server) Lock(reason string) { s.core.lock(reason) }

// Close 关闭服务并清零密钥。
func (s *Server) Close() error { return s.core.shutdown() }

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "只支持 GET/HEAD", http.StatusMethodNotAllowed)
		return
	}
	if s.core.locked.Load() || s.opts.Reader == nil {
		http.Error(w, "播放已锁定", http.StatusGone)
		return
	}
	writeNoCacheHeaders(w)
	w.Header().Set("Content-Type", s.opts.Reader.ContentType())

	// 预检：在写出响应头之前先验证请求起点所在的块。
	// 这样被篡改/损坏的密档会得到明确的 500，而不是一个被中途掐断的 200/206 流
	// （AC4.4：报完整性错误且不崩溃）。请求范围之外的块仍由 ServeContent 流式读取，
	// 若其后才遇错，连接会中断——浏览器据此触发 error，播放页随即提示改用 mpv。
	if !s.probeBeforeServe(r) {
		http.Error(w, "密档损坏或已被篡改：无法解密该位置的块", http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, "", time.Time{}, &seeker{r: s.opts.Reader})
}

// probeBeforeServe 读取请求起点处的一小段以确认可解密。
func (s *Server) probeBeforeServe(r *http.Request) bool {
	size := s.opts.Reader.Size()
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
	if _, err := s.opts.Reader.ReadAt(buf, at); err != nil && err != io.EOF {
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, playerPage(s.opts.Name, s.MediaPath(), ""))
}

// writeNoCacheHeaders 禁止浏览器缓存明文（威胁模型 b）。
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
