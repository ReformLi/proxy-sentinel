package logger

import (
	"context"
	"log"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"proxy-sentinel/internal/storage"
)

const (
	flushInterval = 5 * time.Second
	flushBatch    = 100
	subBuffer     = 256
)

// Writer 异步批量写入日志，并向 SSE 订阅者广播新日志
type Writer struct {
	db            *storage.DB
	sampleRate    float64
	maskSensitive bool
	queueCapacity int // 异步日志队列容量上限（满时丢弃最旧；0=不限制）

	mu     sync.Mutex
	buffer []*storage.LogRecord

	droppedCount int64 // 累计丢弃数（mu 锁内操作，flush 时清零并告警）
	seq          int64 // 本地序号，用于 SSE 推送（数据库自增 ID 在批量落库前尚未生成）

	flushCh chan struct{}

	subMu       sync.RWMutex
	subscribers map[chan *storage.LogRecord]struct{}

	done chan struct{}
	wg   sync.WaitGroup
}

// NewWriter 创建日志写入器
func NewWriter(db *storage.DB, sampleRate float64, maskSensitive bool, queueCapacity int) *Writer {
	w := &Writer{
		db:            db,
		sampleRate:    sampleRate,
		maskSensitive: maskSensitive,
		queueCapacity: queueCapacity,
		flushCh:       make(chan struct{}, 1),
		subscribers:   make(map[chan *storage.LogRecord]struct{}),
		done:          make(chan struct{}),
	}
	w.wg.Add(1)
	go w.run()
	return w
}

// Write 提交一条日志记录（非阻塞：缓冲后立即返回）
func (w *Writer) Write(rec *storage.LogRecord) {
	if w.sampleRate < 1.0 {
		if w.sampleRate <= 0 || rand.Float64() > w.sampleRate {
			return
		}
	}
	if w.maskSensitive {
		rec = MaskRecord(rec)
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now()
	}

	w.mu.Lock()
	// 队列容量上限保护：满时丢弃最旧一批（flushBatch 条），保留较新日志；0=不限制
	if w.queueCapacity > 0 && len(w.buffer) >= w.queueCapacity {
		drop := flushBatch
		if drop > len(w.buffer) {
			drop = len(w.buffer)
		}
		// 重新分配切片，避免底层数组前部引用泄漏导致内存不释放
		w.buffer = append([]*storage.LogRecord(nil), w.buffer[drop:]...)
		w.droppedCount += int64(drop)
	}
	w.buffer = append(w.buffer, rec)
	needFlush := len(w.buffer) >= flushBatch
	w.mu.Unlock()

	// 广播副本：ID 使用本地序号（真实自增 ID 在批量落库后才产生）
	broadcast := *rec
	broadcast.ID = atomic.AddInt64(&w.seq, 1)
	w.broadcast(&broadcast)

	if needFlush {
		select {
		case w.flushCh <- struct{}{}:
		default:
		}
	}
}

// flush 将缓冲区批量写入数据库（单事务提交，避免逐条 fsync）
func (w *Writer) flush() {
	w.mu.Lock()
	dropped := w.droppedCount
	w.droppedCount = 0
	if len(w.buffer) == 0 {
		w.mu.Unlock()
		if dropped > 0 {
			log.Printf("日志队列降级：丢弃 %d 条最旧日志（队列上限 %d，0=不限制）", dropped, w.queueCapacity)
		}
		return
	}
	batch := w.buffer
	w.buffer = nil
	w.mu.Unlock()

	if dropped > 0 {
		log.Printf("日志队列降级：丢弃 %d 条最旧日志（队列上限 %d，0=不限制）", dropped, w.queueCapacity)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := w.db.InsertLogs(ctx, batch); err != nil {
		// 写入失败不阻塞代理，整批丢弃并告警
		log.Printf("日志批量写入失败（丢弃 %d 条）: %v", len(batch), err)
	}
}

// run 后台刷新循环
func (w *Writer) run() {
	defer w.wg.Done()
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			w.flush()
		case <-w.flushCh:
			w.flush()
		case <-w.done:
			w.flush()
			return
		}
	}
}

// Close 关闭写入器并刷新剩余日志
func (w *Writer) Close() {
	close(w.done)
	w.wg.Wait()
}

// broadcast 向所有 SSE 订阅者推送日志（非阻塞）
func (w *Writer) broadcast(rec *storage.LogRecord) {
	w.subMu.RLock()
	defer w.subMu.RUnlock()
	for ch := range w.subscribers {
		select {
		case ch <- rec:
		default:
			// 订阅者消费过慢则丢弃，避免阻塞代理
		}
	}
}

// Subscribe 订阅实时日志流，返回通道与取消函数
func (w *Writer) Subscribe() (chan *storage.LogRecord, func()) {
	ch := make(chan *storage.LogRecord, subBuffer)
	w.subMu.Lock()
	w.subscribers[ch] = struct{}{}
	w.subMu.Unlock()
	return ch, func() {
		w.subMu.Lock()
		if _, ok := w.subscribers[ch]; ok {
			delete(w.subscribers, ch)
			close(ch)
		}
		w.subMu.Unlock()
	}
}
