// Package play 实现本地播放服务：把密档解密流经 127.0.0.1 端口交给浏览器 <video> 或 mpv。
//
// 安全边界（宪法红线）：
//   - 只监听 127.0.0.1（绝不 0.0.0.0）；
//   - 明文只存在于内存与 loopback 响应中，不落盘、不缓存；
//   - 媒体地址带随机 token，避免其他本机网页盲扫 localhost 端口偷流；
//   - 空闲超时 / 收到锁定指令时清零密钥并立即关闭服务；锁定后一律不 panic。
//
// 生命周期有三态（plan.md §4）：
//
//	Locked   密钥已清零；若为软锁定（SoftLock）服务存活并只服务登录页
//	Unlocked 密钥在内存，内容路由可用
//	Closed   终态，端口已关闭
package play

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultIdleTimeout 是空闲自动锁定时长（FR-5.2）。
const DefaultIdleTimeout = 30 * time.Minute

// sessionCookieName 是解锁成功后下发的会话 cookie 名。
// 只在本机环回上使用，用于阻止本机其它页面盲扫端口访问内容路由。
const sessionCookieName = "sv_session"

// coreConfig 是 core 的构造参数。
type coreConfig struct {
	idle        time.Duration
	now         func() time.Time
	onLock      func(reason string)
	onLockExtra func() // 锁定瞬间清零密钥；不得阻塞
	softLock    bool   // true = 锁定时不关停服务，回到登录页（桌面外壳）
	mediaPrefix string // 媒体路由前缀（锁定态下对该前缀返回 403 而非登录页）
	target      string // 登录页展示的目标描述
	unlockFn    func(password string) error
	session     string // 非空 → 解锁后内容路由要求该会话 cookie
	preUnlocked bool   // 构造时已持有密钥（CLI/测试路径）
}

// core 是单文件模式与目录模式共用的生命周期：监听、门控、空闲监控、锁定。
type core struct {
	idleTimeout time.Duration
	now         func() time.Time
	onLock      func(reason string)
	onLockExtra func()

	softLock    bool
	mediaPrefix string
	target      string
	unlockFn    func(password string) error
	session     string

	targetMu sync.RWMutex // 保护 target（US7：切换库根后登录页文案要跟着变）

	last     atomic.Int64 // 最近一次请求时间（UnixNano）
	locked   atomic.Bool  // 密钥已清零（需要再次解锁）
	unlocked atomic.Bool  // 密钥在内存
	closed   atomic.Bool  // 终态
	idleGen  atomic.Uint64

	httpSrv  *http.Server
	listener net.Listener
	url      string
}

func newCore(cfg coreConfig) *core {
	if cfg.idle <= 0 {
		cfg.idle = DefaultIdleTimeout
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	c := &core{
		idleTimeout: cfg.idle,
		now:         cfg.now,
		onLock:      cfg.onLock,
		onLockExtra: cfg.onLockExtra,
		softLock:    cfg.softLock,
		mediaPrefix: cfg.mediaPrefix,
		target:      cfg.target,
		unlockFn:    cfg.unlockFn,
		session:     cfg.session,
	}
	c.unlocked.Store(cfg.preUnlocked)
	c.locked.Store(!cfg.preUnlocked)
	c.touch()
	return c
}

func (c *core) touch() { c.last.Store(c.now().UnixNano()) }

// setTarget 更新登录页展示的目标描述（US7：切换库根后调用）。
func (c *core) setTarget(t string) {
	c.targetMu.Lock()
	c.target = t
	c.targetMu.Unlock()
}

// loginTarget 返回当前目标描述。
func (c *core) loginTarget() string {
	c.targetMu.RLock()
	defer c.targetMu.RUnlock()
	return c.target
}

// start 在 127.0.0.1 随机端口启动。
func (c *core) start(h http.Handler) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("启动本地播放服务失败: %w", err)
	}
	c.listener = ln
	c.url = fmt.Sprintf("http://%s/", ln.Addr().String())
	c.httpSrv = &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = c.httpSrv.Serve(ln) }()
	c.startIdleWatch()
	return c.url, nil
}

// startIdleWatch 启动（并作废上一轮）空闲监视。
func (c *core) startIdleWatch() {
	gen := c.idleGen.Add(1)
	go c.watchIdle(gen)
}

// watchIdle 周期性检查空闲时间，超过阈值自动锁定。
func (c *core) watchIdle(gen uint64) {
	interval := c.idleTimeout / 10
	if interval < 20*time.Millisecond {
		interval = 20 * time.Millisecond
	}
	if interval > 30*time.Second {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		// 已锁定 / 已关停 / 被新一轮监视取代 → 退出
		if c.idleGen.Load() != gen || c.closed.Load() || !c.unlocked.Load() {
			return
		}
		if c.now().Sub(time.Unix(0, c.last.Load())) >= c.idleTimeout {
			c.lock(fmt.Sprintf("空闲超过 %s，已自动锁定并清零密钥", c.idleTimeout))
			return
		}
	}
}

// markUnlocked 由解锁流程调用：进入解锁态并重启空闲监视。
func (c *core) markUnlocked() {
	c.unlocked.Store(true)
	c.locked.Store(false)
	c.touch()
	c.startIdleWatch()
}

// lock 锁定（幂等）：清零密钥；软锁定保持服务存活以服务登录页，否则关停。
func (c *core) lock(reason string) {
	if !c.locked.CompareAndSwap(false, true) {
		return
	}
	c.unlocked.Store(false)
	c.idleGen.Add(1) // 作废空闲监视
	if c.onLockExtra != nil {
		c.onLockExtra()
	}
	if !c.softLock {
		c.closeService()
	}
	if c.onLock != nil {
		c.onLock(reason)
	}
}

// closeService 关停监听（终态）。
func (c *core) closeService() {
	c.closed.Store(true)
	if c.httpSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = c.httpSrv.Shutdown(ctx)
		return
	}
	if c.listener != nil {
		_ = c.listener.Close()
	}
}

// shutdown 关闭服务并清零密钥。
func (c *core) shutdown() error {
	c.closed.Store(true)
	c.idleGen.Add(1)
	if c.onLockExtra != nil {
		c.onLockExtra()
	}
	if c.httpSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return c.httpSrv.Shutdown(ctx)
	}
	if c.listener != nil {
		return c.listener.Close()
	}
	return nil
}

// gate 是门控中间件：锁定态只服务登录页，解锁后校验会话，终态一律 410。
func (c *core) gate(content http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.touch()
		if c.closed.Load() {
			http.Error(w, "播放服务已关闭", http.StatusGone)
			return
		}
		if r.URL.Path == "/login" {
			c.handleLogin(w, r)
			return
		}
		if !c.unlocked.Load() {
			// 锁定态：内容一律不可见（AC1.6：媒体地址被拒绝且不 panic）
			if c.isMediaPath(r.URL.Path) {
				http.Error(w, "未解锁：请先在登录页输入口令", http.StatusForbidden)
				return
			}
			c.renderLogin(w, "")
			return
		}
		if c.session != "" && !c.sessionOK(r) {
			http.Error(w, "会话无效：请重新解锁", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/lock" {
			handleLockRequest(c)(w, r)
			return
		}
		content.ServeHTTP(w, r)
	})
}

func (c *core) isMediaPath(p string) bool {
	return c.mediaPrefix != "" && strings.HasPrefix(p, c.mediaPrefix)
}

func (c *core) sessionOK(r *http.Request) bool {
	ck, err := r.Cookie(sessionCookieName)
	return err == nil && ck.Value == c.session
}

// renderLogin 输出登录页（不再缓存）。
func (c *core) renderLogin(w http.ResponseWriter, errMsg string) {
	writeNoCacheHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, loginPage(c.loginTarget(), errMsg))
}

// handleLogin 处理登录页：GET 渲染表单，POST 校验口令并解锁。
func (c *core) handleLogin(w http.ResponseWriter, r *http.Request) {
	if c.unlocked.Load() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodPost {
		c.renderLogin(w, "")
		return
	}
	if c.unlockFn == nil {
		c.renderLogin(w, "该服务未配置解锁方式")
		return
	}
	if err := r.ParseForm(); err != nil {
		c.renderLogin(w, "请求解析失败，请重试")
		return
	}
	// 口令字节在解锁回调内部使用后即清零，此处不保留副本。
	if err := c.unlockFn(r.PostFormValue("password")); err != nil {
		c.renderLogin(w, err.Error())
		return
	}
	c.markUnlocked()
	if c.session != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    c.session,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
	}
	writeNoCacheHeaders(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func newToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// handleLockRequest 处理页面上的「立即锁定」按钮。
func handleLockRequest(c *core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "只支持 POST", http.StatusMethodNotAllowed)
			return
		}
		writeNoCacheHeaders(w)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if c.softLock {
			fmt.Fprintln(w, "已锁定：密钥已从内存清零，正在返回登录页。")
		} else {
			fmt.Fprintln(w, "已锁定：密钥已从内存清零，服务关闭。可关闭此页面。")
		}
		go c.lock("手动锁定")
	}
}
