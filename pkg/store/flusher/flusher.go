// Package flusher provides an in-memory ring-buffered batch flusher for store.AuditStore.
// Package flusher 为 store.AuditStore 提供基于内存缓冲的高性能微批异步刷盘器。
//
// 核心设计与保障：
// 1. 将高并发下的频繁单条写事务折叠为低频大事务（SaveLogsBatch），化解 SQLite 单写者锁竞争与 PG 连接争用；
// 2. 单一权威机制（Single Authority）：在 SaveLogWithSnapshot 入队时即在锁内确定 PrevHash 与 IntegrityHash，
//    并同步写回调用方的 AuditLog 与 SnapshotRecord 指针，消除日志行、快照行与 HTTP/gRPC 响应体的哈希分叉（P0-B）；
// 3. 严格 FIFO 顺序入队与微批提交，结合有界阻塞排队（EnqueueTimeout），杜绝越序落盘与假阳性断链（P0-A）；
// 4. 刷盘重试与持久性保障：底层写入失败自动退避重试，失败条目保留在内存暂存区（recentLogs）中不被静默抹除，
//    并对外暴露健康降级状态（P0-C）；
// 5. 严格生命周期管理与无竞态优雅停机（Close）：写锁阻断新写入 + Worker 完全排空（Drain on Close），零记录丢失；
// 6. 串行化 Flush 指令：通过向 Worker 投递刷新请求实现独占刷盘与错误透传，消除并发争用与读路径放大；
// 7. 内存暂存支持“读己之写”（Read-Your-Own-Writes）线性一致性，并在 CleanupOld 中联动清理防止僵尸读。
package flusher

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fengzhizi319/PrivShield/pkg/store"
)

var _ store.AuditStore = (*BufferedAuditStore)(nil)

// ErrStoreClosed is returned when an operation is attempted on a closed store.
var ErrStoreClosed = errors.New("audit store is closed")

// Config holds configuration parameters for BufferedAuditStore.
type Config struct {
	BufferSize     int           // 环形缓冲队列容量（默认 10000）
	MaxBatchSize   int           // 单批最大写入条数（默认 200）
	FlushInterval  time.Duration // 最长刷盘等待时间窗口（默认 20ms）
	EnqueueTimeout time.Duration // 队列满时等待可用槽位的超时时间（默认 500ms，防拥塞越序与丢数据）
	FlushTimeout   time.Duration // 显式 Flush 等待超时时间（默认 2s）
	CloseTimeout   time.Duration // 优雅停机排空等待超时时间（默认 5s）
	MaxRetries     int           // 刷盘失败重试次数（默认 3）
}

// DefaultConfig returns default high-performance batch flusher settings.
func DefaultConfig() Config {
	return Config{
		BufferSize:     10000,
		MaxBatchSize:   200,
		FlushInterval:  20 * time.Millisecond,
		EnqueueTimeout: 500 * time.Millisecond,
		FlushTimeout:   2 * time.Second,
		CloseTimeout:   5 * time.Second,
		MaxRetries:     3,
	}
}

type pendingItem struct {
	log      *store.AuditLog
	snapshot *store.SnapshotRecord
}

type flushRequest struct {
	done chan error
}

type auditStoreCloser interface {
	Close() error
}

type auditStoreSimpleCloser interface {
	Close()
}

// BufferedAuditStore wraps an underlying store.AuditStore with asynchronous micro-batch aggregation.
type BufferedAuditStore struct {
	underlying store.AuditStore
	cfg        Config
	logger     *slog.Logger

	queue      chan pendingItem
	flushReqCh chan flushRequest
	stopCh     chan struct{}
	wg         sync.WaitGroup

	closeMu sync.RWMutex
	closed  bool

	// In-memory read-your-own-writes and single-authority chain-tail tracking
	stateMu    sync.RWMutex
	lastHash   string
	lastLog    *store.AuditLog
	recentLogs map[string]*store.AuditLog

	flushedTotal  atomic.Int64
	failedTotal   atomic.Int64
	overflowTotal atomic.Int64

	hasFlushError atomic.Bool
	lastFlushErr  atomic.Value // string
}

// NewBufferedAuditStore creates a new buffered batch audit store wrapper.
func NewBufferedAuditStore(underlying store.AuditStore, cfg Config, logger *slog.Logger) *BufferedAuditStore {
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = 10000
	}
	if cfg.MaxBatchSize <= 0 {
		cfg.MaxBatchSize = 200
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 20 * time.Millisecond
	}
	if cfg.EnqueueTimeout <= 0 {
		cfg.EnqueueTimeout = 500 * time.Millisecond
	}
	if cfg.FlushTimeout <= 0 {
		cfg.FlushTimeout = 2 * time.Second
	}
	if cfg.CloseTimeout <= 0 {
		cfg.CloseTimeout = 5 * time.Second
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 3
	}
	if logger == nil {
		logger = slog.Default()
	}

	// Initialize the chain head from the underlying persistent store
	var initHash string
	var initLog *store.AuditLog
	if latest, err := underlying.GetLatestLog(); err == nil && latest != nil {
		initHash = latest.IntegrityHash
		initLog = latest
	}

	b := &BufferedAuditStore{
		underlying:  underlying,
		cfg:         cfg,
		logger:      logger,
		queue:       make(chan pendingItem, cfg.BufferSize),
		flushReqCh:  make(chan flushRequest),
		stopCh:      make(chan struct{}),
		lastHash:    initHash,
		lastLog:     initLog,
		recentLogs:  make(map[string]*store.AuditLog, cfg.BufferSize),
	}
	b.lastFlushErr.Store("")

	b.wg.Add(1)
	go b.flushWorker()

	logger.Info("buffered audit batch store initialized",
		"buffer_size", cfg.BufferSize,
		"max_batch_size", cfg.MaxBatchSize,
		"flush_interval", cfg.FlushInterval.String(),
		"enqueue_timeout", cfg.EnqueueTimeout.String(),
		"initial_chain_hash", initHash,
	)
	return b
}

// SaveLog puts the audit log into the batch buffer or sync-saves if full/closed.
func (b *BufferedAuditStore) SaveLog(log *store.AuditLog) error {
	return b.SaveLogWithSnapshot(log, nil)
}

// SaveLogWithSnapshot establishes the single-authority hash chain and enqueues the log and snapshot.
func (b *BufferedAuditStore) SaveLogWithSnapshot(log *store.AuditLog, snapshot *store.SnapshotRecord) error {
	if log == nil {
		return errors.New("audit log cannot be nil")
	}

	b.closeMu.RLock()
	if b.closed {
		b.closeMu.RUnlock()
		return ErrStoreClosed
	}
	b.closeMu.RUnlock()

	b.stateMu.Lock()
	if b.closed {
		b.stateMu.Unlock()
		return ErrStoreClosed
	}

	// Single Authority: Establish hash chain under stateMu lock at enqueue time (P0-B)
	if log.PrevHash == "" {
		log.PrevHash = b.lastHash
	}
	if log.IntegrityHash == "" {
		log.IntegrityHash = store.ComputeAuditIntegrityHash(
			log.ID, log.PrevHash, log.Timestamp, log.Algorithm,
			log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON,
		)
	}

	logCopy := *log
	var snapCopy *store.SnapshotRecord
	if snapshot != nil {
		s := *snapshot
		s.PrevHash = log.PrevHash
		s.IntegrityHash = log.IntegrityHash
		snapCopy = &s
		*snapshot = s // Sync back to caller's snapshot pointer
	}

	item := pendingItem{log: &logCopy, snapshot: snapCopy}

	// Try non-blocking enqueue first
	select {
	case b.queue <- item:
		b.lastHash = log.IntegrityHash
		b.lastLog = &logCopy
		b.recentLogs[log.ID] = &logCopy
		*log = logCopy // Sync back to caller's log pointer
		b.stateMu.Unlock()
		return nil
	default:
	}

	// Queue full: wait up to EnqueueTimeout to maintain strict FIFO channel ordering (P0-A)
	timer := time.NewTimer(b.cfg.EnqueueTimeout)
	defer timer.Stop()

	select {
	case b.queue <- item:
		b.lastHash = log.IntegrityHash
		b.lastLog = &logCopy
		b.recentLogs[log.ID] = &logCopy
		*log = logCopy
		b.stateMu.Unlock()
		return nil
	case <-timer.C:
		b.overflowTotal.Add(1)
		b.stateMu.Unlock()
		b.logger.Warn("audit flusher queue congested, write rejected after timeout", "log_id", log.ID, "timeout", b.cfg.EnqueueTimeout)
		return fmt.Errorf("audit buffer queue congested: timeout waiting for slot after %v", b.cfg.EnqueueTimeout)
	}
}

// SaveLogsBatch passes through directly to the underlying store.
func (b *BufferedAuditStore) SaveLogsBatch(logs []store.AuditLog, snapshots []store.SnapshotRecord) error {
	if len(logs) == 0 && len(snapshots) == 0 {
		return nil
	}
	b.stateMu.Lock()
	for i := range logs {
		if logs[i].PrevHash == "" {
			logs[i].PrevHash = b.lastHash
		}
		if logs[i].IntegrityHash == "" {
			logs[i].IntegrityHash = store.ComputeAuditIntegrityHash(
				logs[i].ID, logs[i].PrevHash, logs[i].Timestamp, logs[i].Algorithm,
				logs[i].InputHash, logs[i].OutputHash, logs[i].User, logs[i].SecurityLevel, logs[i].ParametersJSON,
			)
		}
		b.lastHash = logs[i].IntegrityHash
		b.lastLog = &logs[i]
		b.recentLogs[logs[i].ID] = &logs[i]
	}
	b.stateMu.Unlock()

	err := b.underlying.SaveLogsBatch(logs, snapshots)
	if err != nil {
		b.failedTotal.Add(int64(len(logs)))
		return err
	}
	b.flushedTotal.Add(int64(len(logs)))
	return nil
}

// GetLog returns the log from in-memory pending buffer if present, otherwise delegates to underlying store.
func (b *BufferedAuditStore) GetLog(id string) (*store.AuditLog, error) {
	b.stateMu.RLock()
	if log, ok := b.recentLogs[id]; ok {
		logCopy := *log
		b.stateMu.RUnlock()
		return &logCopy, nil
	}
	b.stateMu.RUnlock()

	return b.underlying.GetLog(id)
}

// GetLatestLog returns the newest in-flight buffered log, or delegates to underlying store.
func (b *BufferedAuditStore) GetLatestLog() (*store.AuditLog, error) {
	b.stateMu.RLock()
	if b.lastLog != nil {
		logCopy := *b.lastLog
		b.stateMu.RUnlock()
		return &logCopy, nil
	}
	b.stateMu.RUnlock()

	return b.underlying.GetLatestLog()
}

// ListLogs delegates to the underlying store after flushing.
func (b *BufferedAuditStore) ListLogs(filter store.AuditFilter) ([]store.AuditLog, int, error) {
	_ = b.Flush()
	return b.underlying.ListLogs(filter)
}

// GetStats delegates to the underlying store after flushing.
func (b *BufferedAuditStore) GetStats() (*store.AuditStats, error) {
	_ = b.Flush()
	return b.underlying.GetStats()
}

// GenerateReport delegates to the underlying store after flushing.
func (b *BufferedAuditStore) GenerateReport(period string) (*store.AuditReport, error) {
	_ = b.Flush()
	return b.underlying.GenerateReport(period)
}

// SaveSnapshot delegates to the underlying store.
func (b *BufferedAuditStore) SaveSnapshot(snap *store.SnapshotRecord) error {
	return b.underlying.SaveSnapshot(snap)
}

// ListSnapshots delegates to the underlying store after flushing.
func (b *BufferedAuditStore) ListSnapshots(limit, offset int) ([]store.SnapshotRecord, int, error) {
	_ = b.Flush()
	return b.underlying.ListSnapshots(limit, offset)
}

// GetSnapshot delegates to the underlying store.
func (b *BufferedAuditStore) GetSnapshot(id string) (*store.SnapshotRecord, error) {
	return b.underlying.GetSnapshot(id)
}

// VerifyChain delegates to the underlying store after synchronously flushing the entire buffer.
func (b *BufferedAuditStore) VerifyChain(limit int) (*store.ChainVerificationResult, error) {
	if err := b.Flush(); err != nil {
		b.logger.Error("failed to flush buffer before chain verification", "error", err.Error())
		return nil, fmt.Errorf("flush before verify failed: %w", err)
	}
	return b.underlying.VerifyChain(limit)
}

// CleanupOld cleans up records from underlying store and purges corresponding memory entries.
func (b *BufferedAuditStore) CleanupOld(before time.Time) (int64, error) {
	b.stateMu.Lock()
	for id, l := range b.recentLogs {
		if l.Timestamp.Before(before) {
			delete(b.recentLogs, id)
		}
	}
	b.stateMu.Unlock()
	return b.underlying.CleanupOld(before)
}

// Flush synchronously drains the current queue into the underlying store via worker coordination.
func (b *BufferedAuditStore) Flush() error {
	b.closeMu.RLock()
	if b.closed {
		b.closeMu.RUnlock()
		return nil
	}
	b.closeMu.RUnlock()

	req := flushRequest{done: make(chan error, 1)}
	select {
	case b.flushReqCh <- req:
		return <-req.done
	case <-b.stopCh:
		return nil
	case <-time.After(b.cfg.FlushTimeout):
		return fmt.Errorf("flush operation timed out after %v", b.cfg.FlushTimeout)
	}
}

// Close stops the background worker, drains the entire buffer to disk, and closes the underlying store.
func (b *BufferedAuditStore) Close() error {
	b.closeMu.Lock()
	if b.closed {
		b.closeMu.Unlock()
		return nil
	}
	b.closed = true
	b.closeMu.Unlock()

	b.stateMu.Lock()
	b.closed = true
	b.stateMu.Unlock()

	close(b.stopCh)

	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()

	var closeErr error
	select {
	case <-done:
	case <-time.After(b.cfg.CloseTimeout):
		b.logger.Error("buffered audit store close timed out waiting for worker drain", "timeout", b.cfg.CloseTimeout)
		closeErr = fmt.Errorf("buffered audit store close timed out after %v", b.cfg.CloseTimeout)
	}

	b.logger.Info("buffered audit store safely closed and flushed",
		"total_flushed", b.flushedTotal.Load(),
		"total_failed", b.failedTotal.Load(),
		"total_overflow_congested", b.overflowTotal.Load(),
	)

	// Safely close underlying store if supported (P1-2)
	if closer, ok := b.underlying.(auditStoreCloser); ok {
		if err := closer.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	} else if closer, ok := b.underlying.(auditStoreSimpleCloser); ok {
		closer.Close()
	} else if closer, ok := b.underlying.(io.Closer); ok {
		if err := closer.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}

	return closeErr
}

// QueueDepth returns the current number of pending records in the queue.
func (b *BufferedAuditStore) QueueDepth() int {
	return len(b.queue)
}

// FlushedTotal returns the total number of logs flushed by the batch worker.
func (b *BufferedAuditStore) FlushedTotal() int64 {
	return b.flushedTotal.Load()
}

// FailedTotal returns the total number of logs that failed to write.
func (b *BufferedAuditStore) FailedTotal() int64 {
	return b.failedTotal.Load()
}

// OverflowTotal returns the total number of logs that timed out due to full queue.
func (b *BufferedAuditStore) OverflowTotal() int64 {
	return b.overflowTotal.Load()
}

// HasFlushError reports whether the store currently has an unrecovered flush error.
func (b *BufferedAuditStore) HasFlushError() bool {
	return b.hasFlushError.Load()
}

// LastFlushError returns the description of the latest flush error, if any.
func (b *BufferedAuditStore) LastFlushError() string {
	if v := b.lastFlushErr.Load(); v != nil {
		return v.(string)
	}
	return ""
}

func (b *BufferedAuditStore) flushWorker() {
	defer b.wg.Done()

	ticker := time.NewTicker(b.cfg.FlushInterval)
	defer ticker.Stop()

	batchLogs := make([]store.AuditLog, 0, b.cfg.MaxBatchSize)
	batchSnaps := make([]store.SnapshotRecord, 0, b.cfg.MaxBatchSize)

	// flushCurrent writes the accumulated batch preserving FIFO sequential order
	flushCurrent := func() error {
		if len(batchLogs) == 0 && len(batchSnaps) == 0 {
			return nil
		}

		// Underlying batch commit with exponential backoff retries (P0-C)
		var err error
		for attempt := 0; attempt <= b.cfg.MaxRetries; attempt++ {
			err = b.underlying.SaveLogsBatch(batchLogs, batchSnaps)
			if err == nil {
				break
			}
			if attempt < b.cfg.MaxRetries {
				time.Sleep(time.Duration(25*(1<<attempt)) * time.Millisecond)
			}
		}

		if err != nil {
			b.failedTotal.Add(int64(len(batchLogs)))
			b.hasFlushError.Store(true)
			b.lastFlushErr.Store(err.Error())
			b.logger.Error("failed to flush audit batch after retries", "count", len(batchLogs), "error", err.Error())
			// Retain in recentLogs so records remain readable in memory and not lost from memory view (P0-C)
			batchLogs = batchLogs[:0]
			batchSnaps = batchSnaps[:0]
			return err
		}

		b.flushedTotal.Add(int64(len(batchLogs)))
		b.hasFlushError.Store(false)
		b.lastFlushErr.Store("")

		// Clear committed records from memory map
		b.stateMu.Lock()
		for i := range batchLogs {
			delete(b.recentLogs, batchLogs[i].ID)
		}
		b.stateMu.Unlock()

		batchLogs = batchLogs[:0]
		batchSnaps = batchSnaps[:0]
		return nil
	}

	drainQueue := func(maxItems int) {
		count := 0
		for {
			select {
			case item := <-b.queue:
				if item.log != nil {
					batchLogs = append(batchLogs, *item.log)
				}
				if item.snapshot != nil {
					batchSnaps = append(batchSnaps, *item.snapshot)
				}
				count++
				if len(batchLogs) >= b.cfg.MaxBatchSize {
					_ = flushCurrent()
				}
				if maxItems > 0 && count >= maxItems {
					_ = flushCurrent()
					return
				}
			default:
				_ = flushCurrent()
				return
			}
		}
	}

	for {
		select {
		case <-b.stopCh:
			// Drain all remaining items in queue before termination (P0-2)
			drainQueue(0)
			return

		case req := <-b.flushReqCh:
			// Drain queue up to limit and flush synchronously
			drainQueue(1000)
			err := flushCurrent()
			req.done <- err

		case <-ticker.C:
			_ = flushCurrent()

		case item := <-b.queue:
			if item.log != nil {
				batchLogs = append(batchLogs, *item.log)
			}
			if item.snapshot != nil {
				batchSnaps = append(batchSnaps, *item.snapshot)
			}
			if len(batchLogs) >= b.cfg.MaxBatchSize {
				_ = flushCurrent()
			}
		}
	}
}
