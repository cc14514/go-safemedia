// Package play 实现本地播放服务：把密档解密流经 127.0.0.1 端口交给浏览器 <video> 或 mpv。
//
// 安全边界（宪法红线）：
//   - 只监听 127.0.0.1（绝不 0.0.0.0）；
//   - 明文只存在于内存与 loopback 响应中，不落盘、不缓存；
//   - 媒体地址带随机 token，避免其他本机网页盲扫 localhost 端口偷流；
//   - 空闲超时 / 收到锁定指令时清零密钥并立即关闭服务；锁定后一律不 panic。
package play

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultIdleTimeout 是空闲自动锁定时长（FR-5.2）。
const DefaultIdleTimeout = 30 * time.Minute

// core 是单文件模式与目录模式共用的生命周期：监听、空闲监控、锁定。
type core struct {
	idleTimeout time.Duration
	now         func() time.Time
	onLock      func(reason string)

	last   atomic.Int64 // 最近一次请求时间（UnixNano）
	locked atomic.Bool
	once   sync.Once
	stopCh chan struct{}

	httpSrv  *http.Server
	listener net.Listener
	url      string

	// onLockExtra 在锁定瞬间执行（清零密钥等）；不得阻塞。
	onLockExtra func()
}

func newCore(idle time.Duration, now func() time.Time, onLock func(string), extra func()) *core {
	if idle <= 0 {
		idle = DefaultIdleTimeout
	}
	if now == nil {
		now = time.Now
	}
	c := &core{idleTimeout: idle, now: now, onLock: onLock, onLockExtra: extra, stopCh: make(chan struct{})}
	c.touch()
	return c
}

func (c *core) touch() { c.last.Store(c.now().UnixNano()) }

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
	go c.watchIdle()
	return c.url, nil
}

// watchIdle 周期性检查空闲时间，超过阈值自动锁定。
func (c *core) watchIdle() {
	interval := c.idleTimeout / 10
	if interval < 20*time.Millisecond {
		interval = 20 * time.Millisecond
	}
	if interval > 30*time.Second {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-t.C:
			idle := c.now().Sub(time.Unix(0, c.last.Load()))
			if idle >= c.idleTimeout {
				c.lock(fmt.Sprintf("空闲超过 %s，已自动锁定并清零密钥", c.idleTimeout))
				return
			}
		}
	}
}

// lock 锁定（幂等）：清零密钥、关闭服务、通知回调。
func (c *core) lock(reason string) {
	if !c.locked.CompareAndSwap(false, true) {
		return
	}
	c.once.Do(func() { close(c.stopCh) })
	if c.onLockExtra != nil {
		c.onLockExtra()
	}
	if c.httpSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = c.httpSrv.Shutdown(ctx)
	} else if c.listener != nil {
		_ = c.listener.Close()
	}
	if c.onLock != nil {
		c.onLock(reason)
	}
}

// shutdown 关闭服务并清零密钥（不触发 onLock 之外的通知语义）。
func (c *core) shutdown() error {
	c.once.Do(func() { close(c.stopCh) })
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

func (c *core) wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.touch()
		h.ServeHTTP(w, r)
	})
}

func newToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func handleLockRequest(c *core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "只支持 POST", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "已锁定：密钥已从内存清零，服务关闭。可关闭此页面。")
		go c.lock("手动锁定")
	}
}
