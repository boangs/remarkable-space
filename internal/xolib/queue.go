package xolib

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// 交给 xochitl 里的「空间」外壳去做的操作 —— 外壳认识的 op 才会做, 不认识的回报失败。
//
//	load   文件已经写进数据目录, 让文件库加载这个 uuid (Library.requestLoadEntry, 3.28 及以前)
//	import 文件放在暂存目录 Path, 让系统导入器复制进文件库 (DocumentImporter.importFromUrls,
//	       3.29 起: 文件库对 QML 只剩只读的 LibraryFacade, requestLoadEntry 没了)
//	folder / notebook  3.29 起建文件夹 / 笔记本: 让 LibraryController.createCollection /
//	       createDocument 建, 回报里的 newId 是系统给的 uuid (盘上写文件再 load 这条路走不通了)
const (
	OpLoad     = "load"
	OpImport   = "import"
	OpFolder   = "folder"
	OpNotebook = "notebook"
)

type Job struct {
	Op     string `json:"op"`
	ID     string `json:"id"`
	Path   string `json:"path,omitempty"`   // import: 暂存文件, 文件名就是文件库里的显示名
	Parent string `json:"parent,omitempty"` // import / folder / notebook: 放进哪个文件夹, 空 = 根目录
	Name   string `json:"name,omitempty"`   // folder / notebook: 显示名
	// NotBefore: 做失败的活要等一阵再给外壳, 否则「取 → 失败 → 立刻排回 → 立刻再取」空转
	NotBefore time.Time `json:"notBefore,omitzero"`
}

// RetryDelay 失败的活多久之后再试。
const RetryDelay = 30 * time.Second

type Result struct {
	Op    string `json:"op"`
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	NewID string `json:"newId,omitempty"` // folder / notebook: 系统建出来的 uuid
}

// ActiveWindow: 外壳最后一次来取活之后这么久内, 算「空间开着」。外壳的长连接 25 秒一轮,
// 取完立刻再连, 所以正常开着时间隔接近 0; 留 35 秒余量兜住网络抖动。
const ActiveWindow = 35 * time.Second

// Queue 是待通知队列 + 外壳在线状态。待通知的条目落盘: 常驻桥在文件写完、通知之前退出,
// 或 space-server 中途重启, 恢复后照样补上, 不会出现「文件在磁盘上却永远看不到」。
type Queue struct {
	mu       sync.Mutex
	file     string
	pending  []Job
	wake     chan struct{}
	waiters  map[string][]chan Result
	lastSeen time.Time
	polling  int            // 正挂着的长连接数
	closed   bool           // 外壳主动说了「我关了」
	importer bool           // 外壳说这台固件要走导入器 (3.29 起)
	inflight map[string]Job // 已交给外壳、还没回报的活 —— 失败时按原样排回
	now      func() time.Time
}

func NewQueue(file string) *Queue {
	q := &Queue{file: file, wake: make(chan struct{}), waiters: map[string][]chan Result{}, inflight: map[string]Job{}, now: time.Now}
	if file != "" {
		if b, err := os.ReadFile(file); err == nil {
			_ = json.Unmarshal(b, &q.pending)
		}
	}
	return q
}

// Enqueue 排一个活, 叫醒正在等的外壳。
func (q *Queue) Enqueue(j Job) {
	q.mu.Lock()
	q.pending = append(q.pending, j)
	q.saveLocked()
	close(q.wake)
	q.wake = make(chan struct{})
	q.mu.Unlock()
}

// Next 给外壳取活: 有能做的就立刻全部拿走, 没有就最多等 wait。拿走即出队 —— 外壳做完回报,
// 失败的由 Done 延后重排。
//
// presence: 这个长连接算不算「空间开着」。外壳的算; 注入库常驻顶层的文件库桥 (hooks/root/library.qml)
// 一直挂着, 不算 —— 空间页面状态只用于界面逻辑, 不影响常驻扫码上传。
func (q *Queue) Next(ctx context.Context, wait time.Duration, presence bool) []Job {
	q.mu.Lock()
	if presence {
		q.closed = false
		q.lastSeen = q.now()
		q.polling++
		defer func() {
			q.mu.Lock()
			q.polling--
			q.lastSeen = q.now()
			q.mu.Unlock()
		}()
	}
	deadline := q.now().Add(wait)
	for {
		now := q.now()
		var ready, later []Job
		var soonest time.Time
		for _, j := range q.pending {
			if j.NotBefore.After(now) {
				later = append(later, j)
				if soonest.IsZero() || j.NotBefore.Before(soonest) {
					soonest = j.NotBefore
				}
			} else {
				ready = append(ready, j)
			}
		}
		if len(ready) > 0 {
			q.pending = later
			q.saveLocked()
			for i := range ready {
				ready[i].NotBefore = time.Time{}
				q.inflight[ready[i].ID] = ready[i]
			}
			q.mu.Unlock()
			return ready
		}
		left := deadline.Sub(now)
		if left <= 0 {
			q.mu.Unlock()
			return nil
		}
		if !soonest.IsZero() && soonest.Sub(now) < left {
			left = soonest.Sub(now)
		}
		wake := q.wake
		q.mu.Unlock()
		t := time.NewTimer(left)
		select {
		case <-wake:
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return nil
		}
		t.Stop()
		q.mu.Lock()
	}
}

// Done 收外壳的回报。失败的重新排回去 (过 RetryDelay 再试), 并通知等结果的人。
// 导入器模式下失败的 load 不再排回: 这台固件做不了 load, 条目已在盘上, xochitl 重启时自己会读到。
func (q *Queue) Done(r Result) {
	q.mu.Lock()
	j, ok := q.inflight[r.ID]
	delete(q.inflight, r.ID)
	if !ok {
		j = Job{Op: r.Op, ID: r.ID}
	}
	if !r.OK && (j.Op == OpImport || (j.Op == OpLoad && !q.importer)) {
		j.NotBefore = q.now().Add(RetryDelay)
		q.pending = append(q.pending, j)
		q.saveLocked()
	}
	ws := q.waiters[r.ID]
	delete(q.waiters, r.ID)
	q.mu.Unlock()
	for _, w := range ws {
		w <- r
	}
}

// Wait 等某个 id 的回报, 超时返回 false (文件已经在磁盘上, 只是还没出现在文件库里)。
func (q *Queue) Wait(id string, timeout time.Duration) (Result, bool) {
	ch := make(chan Result, 1)
	q.mu.Lock()
	q.waiters[id] = append(q.waiters[id], ch)
	q.mu.Unlock()
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case r := <-ch:
		return r, true
	case <-t.C:
		q.mu.Lock()
		ws := q.waiters[id]
		for i, w := range ws {
			if w == ch {
				q.waiters[id] = append(ws[:i], ws[i+1:]...)
				break
			}
		}
		if len(q.waiters[id]) == 0 {
			delete(q.waiters, id)
		}
		q.mu.Unlock()
		return Result{}, false
	}
}

// SetImporter 记下外壳报的模式。从 load 切到 import 时, 用 convert 改写还没做成的 load
// (比如升级到 3.29 之前上传、一直加载不了的文档); convert 返回 false 的 load 直接丢掉。
func (q *Queue) SetImporter(on bool, convert func(*Job) bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if on == q.importer {
		return
	}
	q.importer = on
	if !on {
		return
	}
	kept := q.pending[:0]
	for _, j := range q.pending {
		if j.Op != OpLoad || convert(&j) {
			kept = append(kept, j)
		}
	}
	q.pending = kept
	q.saveLocked()
}

// Drop 撤掉一条还没交出去的活 (等不到外壳时用, 免得空间下次打开又建一遍)。
func (q *Queue) Drop(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	kept := q.pending[:0]
	for _, j := range q.pending {
		if j.ID != id {
			kept = append(kept, j)
		}
	}
	q.pending = kept
	q.saveLocked()
}

// Importer 外壳最近一次报的是不是导入器模式。
func (q *Queue) Importer() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.importer
}

// Close 外壳关闭时调: 立刻算作「空间关了」, 不必等 ActiveWindow 过期。
func (q *Queue) Close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
}

// Active 空间开着没有: 外壳正挂着长连接, 或刚取过活不久, 且没有主动说关了。
func (q *Queue) Active() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	return q.polling > 0 || (!q.lastSeen.IsZero() && q.now().Sub(q.lastSeen) < ActiveWindow)
}

// Pending 待通知的条目数 (状态接口用)。
func (q *Queue) Pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

func (q *Queue) saveLocked() {
	if q.file == "" {
		return
	}
	b, _ := json.Marshal(q.pending)
	_ = os.MkdirAll(filepath.Dir(q.file), 0o755)
	tmp := q.file + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, q.file)
	}
}
