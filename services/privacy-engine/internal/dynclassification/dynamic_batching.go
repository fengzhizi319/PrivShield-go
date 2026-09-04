// Package dynclassification 提供动态合批队列。
//
// 在高并发 NER 推理场景下，单条请求的 GPU/CPU 推理效率极低。
// DynamicBatcher 将一段时间窗口内到达的多个推理请求聚合为一个批次，
// 统一送入推理引擎处理，显著提升吞吐量。
//
// 设计要点：
//   - Channel 缓冲接收请求，无锁化
//   - Ticker 超时触发，避免低流量时延迟过高
//   - 可配置最大批大小与等待超时
//   - 每个请求通过独立 resultChan 获取结果
package dynclassification

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// ──────────────────────────────────────────────
// 批处理任务
// ──────────────────────────────────────────────

// BatchItem 批处理中的单个请求项
type BatchItem struct {
	ID         string           // 请求唯一标识
	Input      string           // 输入文本
	ResultChan chan BatchResult // 结果回传通道
}

// BatchResult 批处理结果
type BatchResult struct {
	ItemID string
	Output interface{} // 推理输出（如 []NerEntity）
	Err    error
}

// BatchFunc 批次处理函数类型。
// 接收一批请求，返回对应的结果列表（顺序与输入一致）。
type BatchFunc func(ctx context.Context, items []BatchItem) []BatchResult

// ──────────────────────────────────────────────
// 动态合批器
// ──────────────────────────────────────────────

// DynamicBatcherConfig 动态合批器配置
type DynamicBatcherConfig struct {
	MaxBatchSize    int           // 单批最大请求数
	MaxWaitTime     time.Duration // 最大等待时间（超时强制刷盘）
	QueueBufferSize int           // 请求队列缓冲大小
}

// DefaultBatcherConfig 默认配置
func DefaultBatcherConfig() DynamicBatcherConfig {
	return DynamicBatcherConfig{
		MaxBatchSize:    32,
		MaxWaitTime:     10 * time.Millisecond,
		QueueBufferSize: 1024,
	}
}

// DynamicBatcher 动态合批队列
type DynamicBatcher struct {
	cfg       DynamicBatcherConfig
	batchFn   BatchFunc
	queue     chan BatchItem
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	dropped   int64 // 丢弃的请求计数
	processed int64 // 已处理的请求计数
	batches   int64 // 已处理的批次数
	mu        sync.Mutex
	running   bool
}

// NewDynamicBatcher 创建动态合批器
func NewDynamicBatcher(cfg DynamicBatcherConfig, fn BatchFunc) *DynamicBatcher {
	ctx, cancel := context.WithCancel(context.Background())
	return &DynamicBatcher{
		cfg:     cfg,
		batchFn: fn,
		queue:   make(chan BatchItem, cfg.QueueBufferSize),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Start 启动合批循环
func (b *DynamicBatcher) Start() {
	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		return
	}
	b.running = true
	b.mu.Unlock()

	b.wg.Add(1)
	go b.batchLoop()
	slog.Info("DynamicBatcher started",
		"max_batch_size", b.cfg.MaxBatchSize,
		"max_wait_time", b.cfg.MaxWaitTime.String(),
	)
}

// Stop 优雅停止合批器
func (b *DynamicBatcher) Stop() {
	b.mu.Lock()
	if !b.running {
		b.mu.Unlock()
		return
	}
	b.running = false
	b.mu.Unlock()

	b.cancel()
	b.wg.Wait()
	slog.Info("DynamicBatcher stopped",
		"processed", b.processed,
		"batches", b.batches,
		"dropped", b.dropped,
	)
}

// Submit 提交一个推理请求
// 返回 resultChan 用于接收结果，timeout 控制等待结果的超时。
func (b *DynamicBatcher) Submit(ctx context.Context, id string, input string, timeout time.Duration) (BatchResult, error) {
	item := BatchItem{
		ID:         id,
		Input:      input,
		ResultChan: make(chan BatchResult, 1),
	}

	select {
	case b.queue <- item:
		// 成功入队
	case <-ctx.Done():
		return BatchResult{ItemID: id}, ctx.Err()
	default:
		// 队列已满，直接降级
		b.mu.Lock()
		b.dropped++
		b.mu.Unlock()
		return BatchResult{ItemID: id}, ErrBatcherQueueFull
	}

	// 等待结果
	select {
	case result := <-item.ResultChan:
		return result, nil
	case <-time.After(timeout):
		return BatchResult{ItemID: id}, ErrBatcherTimeout
	case <-ctx.Done():
		return BatchResult{ItemID: id}, ctx.Err()
	}
}

// batchLoop 核心合批循环
// 策略：收集最多 MaxBatchSize 个请求，或等待超过 MaxWaitTime 后刷盘。
func (b *DynamicBatcher) batchLoop() {
	defer b.wg.Done()

	for {
		select {
		case <-b.ctx.Done():
			// 排空队列中的剩余请求
			b.drainQueue()
			return
		case first := <-b.queue:
			batch := []BatchItem{first}
			deadline := time.NewTimer(b.cfg.MaxWaitTime)

		collectLoop:
			for len(batch) < b.cfg.MaxBatchSize {
				select {
				case <-deadline.C:
					break collectLoop
				case item := <-b.queue:
					batch = append(batch, item)
				case <-b.ctx.Done():
					break collectLoop
				}
			}
			deadline.Stop()

			// 执行批次推理
			b.executeBatch(batch)
		}
	}
}

// executeBatch 执行一批推理请求
func (b *DynamicBatcher) executeBatch(items []BatchItem) {
	b.mu.Lock()
	b.batches++
	b.processed += int64(len(items))
	b.mu.Unlock()

	results := b.batchFn(b.ctx, items)

	// 分发结果
	for i, item := range items {
		if i < len(results) {
			results[i].ItemID = item.ID
			select {
			case item.ResultChan <- results[i]:
			default:
				// 结果通道已满，丢弃
			}
		} else {
			// 结果数量不足，返回空结果
			select {
			case item.ResultChan <- BatchResult{ItemID: item.ID}:
			default:
			}
		}
	}
}

// drainQueue 排空队列（停机时调用）
func (b *DynamicBatcher) drainQueue() {
	for {
		select {
		case item := <-b.queue:
			b.executeBatch([]BatchItem{item})
		default:
			return
		}
	}
}

// Stats 返回合批器统计信息
func (b *DynamicBatcher) Stats() (processed, batches, dropped int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.processed, b.batches, b.dropped
}

// ──────────────────────────────────────────────
// 错误定义
// ──────────────────────────────────────────────

// BatchError 合批器错误
type BatchError struct {
	Message string
}

func (e *BatchError) Error() string { return e.Message }

var (
	// ErrBatcherQueueFull 队列已满
	ErrBatcherQueueFull = &BatchError{Message: "batcher queue is full"}
	// ErrBatcherTimeout 等待结果超时
	ErrBatcherTimeout = &BatchError{Message: "batcher result timeout"}
)
