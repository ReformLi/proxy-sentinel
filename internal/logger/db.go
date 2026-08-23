package logger

import (
	"context"
	"math/rand"
	"sync"
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

	mu     sync.Mutex
	buffer []*storage.LogRecord

	flushCh chan struct{}

	subMu       sync.RWMutex
	subscribers map[chan *storage.LogRecord]struct{}

	done chan struct{}
	wg   sync.WaitGroup
}

// NewWriter 创建日志写入器
func NewWriter(db *storage.DB, sampleRate float64, maskSensitive bool) *Writer {
	w := &Writer{
		db:            db,
		sampleRate:    sampleRate,
		maskSensitive: maskSensitive,
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
	w.buffer = append(w.buffer, rec)
	needFlush := len(w.buffer) >= flushBatch
	w.mu.Unlock()

	// 实时广播给 SSE 订阅者
	w.broadcast(rec)

	if needFlush {
		select {
		case w.flushCh <- struct{}{}:
		default:
		}
	}
}

// flush 将缓冲区批量写入数据库
func (w *Writer) flush() {
	w.mu.Lock()
	if len(w.buffer) == 0 {
		w.mu.Unlock()
		return
	}
	batch := w.buffer
	w.buffer = nil
	w.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, rec := range batch {
		if err := w.db.InsertLog(ctx, rec); err != nil {
			// 写入失败不阻塞代理；丢弃该批次的后续错误继续尝试
			continue
		}
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
