package seal

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/cc14514/go-safemedia/internal/vault"
)

// State 是一个封存条目的状态。
type State int

const (
	StateWaiting  State = iota // 等待
	StateRunning               // 封存中
	StateDone                  // 完成
	StateFailed                // 失败
	StateSkipped               // 跳过（已存在 / 非白名单）
	StateCanceled              // 已取消（用户锁定 / 中止）
)

// Label 返回状态的中文名（页面与终端共用）。
func (s State) Label() string {
	switch s {
	case StateWaiting:
		return "等待"
	case StateRunning:
		return "封存中"
	case StateDone:
		return "完成"
	case StateFailed:
		return "失败"
	case StateSkipped:
		return "跳过"
	case StateCanceled:
		return "已取消"
	}
	return "未知"
}

// Finished 表示该条目已终态（不会再变化）。
func (s State) Finished() bool {
	switch s {
	case StateDone, StateFailed, StateSkipped, StateCanceled:
		return true
	}
	return false
}

// Item 是队列中的一个封存条目（含实时进度）。
type Item struct {
	Src string
	Dst string

	State  State
	Done   int64 // 已封存的明文字节数
	Total  int64 // 明文总字节数
	Reason string

	Container     string
	Chunks        uint64
	PlaintextSize int64
}

// Percent 返回 0-100 的整数百分比（总长未知时按可读性取 0 或 100）。
func (i Item) Percent() int {
	if i.State == StateDone {
		return 100
	}
	if i.Total <= 0 {
		return 0
	}
	p := int(i.Done * 100 / i.Total)
	if p > 100 {
		p = 100
	}
	return p
}

// Options 控制队列执行方式。
type Options struct {
	// Overwrite 为真时覆盖已存在的密档；为假时跳过（AC10.5）。
	Overwrite bool
	// RemovePartialOnFailure 为真时清理未完成的密档（页面用；CLI 保留今天的语义）。
	RemovePartialOnFailure bool
	// OnChange 在条目状态/进度变化时回调（调用方自行节流，本包不保证频率）。
	OnChange func()
}

// Queue 顺序执行封存任务：不并发多个文件，进度可读且不互相干扰（AC10.5）。
type Queue struct {
	mu    sync.Mutex
	items []Item
	opts  Options

	ctx    context.Context
	cancel context.CancelFunc

	active bool
	once   sync.Once
	done   chan struct{}
}

// NewQueue 用任务列表构造队列（初始全部为「等待」）。
func NewQueue(jobs []Job, opts Options) *Queue {
	items := make([]Item, 0, len(jobs))
	for _, j := range jobs {
		items = append(items, Item{Src: j.Src, Dst: j.Dst, State: StateWaiting})
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Queue{items: items, opts: opts, ctx: ctx, cancel: cancel, done: make(chan struct{})}
}

// Items 返回条目快照（页面轮询用）。
func (q *Queue) Items() []Item {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Item, len(q.items))
	copy(out, q.items)
	return out
}

// Active 表示队列正在运行（空闲锁定据此跳过判定，AC10.9）。
func (q *Queue) Active() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.active
}

// Cancel 中止队列：进行中的条目立即停止并清理半成品，未开始的标记为已取消。
func (q *Queue) Cancel() { q.cancel() }

// Done 在队列结束后关闭（Wait 用）。
func (q *Queue) Done() <-chan struct{} { return q.done }

// Wait 阻塞到队列结束。
func (q *Queue) Wait() { <-q.done }

// Start 异步执行队列。pub 是账户公钥。
func (q *Queue) Start(pub *ecdsa.PublicKey) {
	q.once.Do(func() {
		q.mu.Lock()
		q.active = true
		q.mu.Unlock()
		go q.run(pub)
	})
}

// Summary 返回终态计数（完成 / 失败 / 跳过 / 取消）。
func (q *Queue) Summary() (done, failed, skipped, canceled int) {
	for _, it := range q.Items() {
		switch it.State {
		case StateDone:
			done++
		case StateFailed:
			failed++
		case StateSkipped:
			skipped++
		case StateCanceled:
			canceled++
		}
	}
	return
}

func (q *Queue) run(pub *ecdsa.PublicKey) {
	defer func() {
		q.mu.Lock()
		q.active = false
		q.mu.Unlock()
		close(q.done)
		q.notify()
	}()

	for idx := range q.items {
		if q.ctx.Err() != nil {
			q.markCanceledFrom(idx)
			return
		}
		if !q.step(idx, pub) {
			q.markCanceledFrom(idx + 1)
			return
		}
	}
}

// step 执行单个条目，返回 false 表示队列被取消（后续条目应标记为已取消）。
func (q *Queue) step(idx int, pub *ecdsa.PublicKey) bool {
	src, dst, total, err := q.begin(idx)
	if err != nil {
		q.fail(idx, err, false)
		return true // 单个条目失败不影响其它条目（AC10.4）
	}
	res, err := vault.SealFileContext(q.ctx, src, dst, pub, vault.SealOptions{Overwrite: q.opts.Overwrite},
		func(done, total int64) { q.progress(idx, done, total) })
	if err != nil {
		canceled := errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
		// 只清理「我们自己写出来的」半成品。
		// 目标已存在（ErrTargetExists，AC10.5 的判定发生在写第一个字节之前）时 dst 是
		// **别人的密档**：删它等于毁掉用户既有的密档 —— 曾因无条件 os.Remove(dst) 丢数据。
		if !errors.Is(err, vault.ErrTargetExists) && (q.opts.RemovePartialOnFailure || canceled) {
			_ = os.Remove(dst) // 半成品不含明文，删除安全
		}
		q.fail(idx, err, canceled)
		return !canceled
	}
	q.finish(idx, res, total)
	return true
}

func (q *Queue) begin(idx int) (src, dst string, total int64, err error) {
	q.mu.Lock()
	it := &q.items[idx]
	it.State = StateRunning
	it.Reason = ""
	src, dst = it.Src, it.Dst
	q.mu.Unlock()

	// 输出目录可能尚未创建（源目录结构在输出目录下重建）。
	if dir := filepath.Dir(dst); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return src, dst, 0, err
		}
	}
	if info, serr := os.Stat(src); serr == nil {
		total = info.Size()
	}
	q.mu.Lock()
	q.items[idx].Total = total
	q.mu.Unlock()
	q.notify()
	return src, dst, total, nil
}

func (q *Queue) progress(idx int, done, total int64) {
	q.mu.Lock()
	it := &q.items[idx]
	it.Done = done
	if total > 0 {
		it.Total = total
	}
	q.mu.Unlock()
	q.notify()
}

func (q *Queue) finish(idx int, res *vault.SealResult, total int64) {
	q.mu.Lock()
	it := &q.items[idx]
	it.State = StateDone
	it.Done = total
	it.Total = total
	it.Container = res.Container
	it.Chunks = res.Chunks
	it.PlaintextSize = res.PlaintextSize
	q.mu.Unlock()
	q.notify()
}

func (q *Queue) fail(idx int, err error, canceled bool) {
	q.mu.Lock()
	it := &q.items[idx]
	if canceled {
		it.State = StateCanceled
		it.Reason = "已取消"
	} else if errors.Is(err, vault.ErrTargetExists) {
		it.State = StateSkipped
		it.Reason = "已存在（跳过）"
	} else {
		it.State = StateFailed
		it.Reason = err.Error()
	}
	q.mu.Unlock()
	q.notify()
}

// markCanceledFrom 把 idx 起的条目标记为已取消（未开始）。
func (q *Queue) markCanceledFrom(idx int) {
	q.mu.Lock()
	for i := idx; i < len(q.items); i++ {
		if q.items[i].State.Finished() {
			continue
		}
		q.items[i].State = StateCanceled
		q.items[i].Reason = "已取消"
	}
	q.mu.Unlock()
	q.notify()
}

func (q *Queue) notify() {
	if q.opts.OnChange != nil {
		q.opts.OnChange()
	}
}
