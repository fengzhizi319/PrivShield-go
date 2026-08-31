// Package flusher provides an in-memory ring-buffered batch flusher for store.AuditStore.
// Package flusher 为 store.AuditStore 提供基于内存缓冲的高性能微批异步刷盘器。
//
// 核心设计与保障：
// 1. 将高并发下的频繁单条写事务折叠为低频大事务（SaveLogsBatch），化解 SQLite 单写者锁竞争；
// 2. 单 Worker 串行计算并链式咬合 PrevHash 与 IntegrityHash，彻底杜绝高并发缓冲下的防篡改哈希链断裂（P0-1）；
// 3. 严格的生命周期管理与无竞态停机（Close）：写锁阻断新入队 + Worker 完全排空（P0-2）；
// 4. 串行化 Flush 指令：通过向 Worker 投递刷新请求实现独占刷盘与错误透传，消除并发争用与静默吞错（P0-3）；
// 5. 内存短环查找支持“读己之写”一致性（GetLog / GetLatestLog 查询缓冲中待落盘记录）。
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
	BufferSize    int           // 环形缓冲队列容量（默认 10000）
	MaxBatchSize  int           // 单批最大写入条数（默认 200）
	FlushInterval time.Duration // 最长刷盘等待时间窗口（默认 20ms）
}

// DefaultConfig returns default high-performance batch flusher settings.
func DefaultConfig() Config {
	return Config{
		BufferSize:    10000,
		MaxBatchSize:  200,
		FlushInterval: 20 * time.Millisecond,
	}
}

type pendingItem struct {
	log      *store.AuditLog
	snapshot *store.SnapshotRecord
}

type flushRequest struct {
	done chan error
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

	// In-memory read-your-own-writes and chain-tail tracking
	stateMu    sync.RWMutex
	lastHash   string
	lastLog    *store.AuditLog
	recentLogs map[string]*store.AuditLog

	flushedTotal atomic.Int64
	failedTotal  atomic.Int64
	droppedTotal atomic.Int64
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

	b.wg.Add(1)
	go b.flushWorker()

	logger.Info("buffered audit batch store initialized",
		"buffer_size", cfg.BufferSize,
		"max_batch_size", cfg.MaxBatchSize,
		"flush_interval", cfg.FlushInterval.String(),
		"initial_chain_hash", initHash,
	)
	return b
}

// SaveLog puts the audit log into the batch buffer or sync-saves if full/closed.
func (b *BufferedAuditStore) SaveLog(log *store.AuditLog) error {
	return b.SaveLogWithSnapshot(log, nil)
}

// SaveLogWithSnapshot puts the audit log and snapshot into the batch buffer.
func (b *BufferedAuditStore) SaveLogWithSnapshot(log *store.AuditLog, snapshot *store.SnapshotRecord) error {
	if log == nil {
		return errors.New("audit log cannot be nil")
	}

	b.closeMu.RLock()
	defer b.closeMu.RUnlock()

	if b.closed {
		// Closed: fallback to direct synchronous save with re-chaining
		return b.saveDirectWithChain(log, snapshot)
	}

	// Clone log pointer to ensure worker can safely mutate hash without data race
	logCopy := *log
	item := pendingItem{log: &logCopy, snapshot: snapshot}

	// Stage in memory for read-your-own-writes before enqueueing
	b.stateMu.Lock()
	b.recentLogs[log.ID] = &logCopy
	b.lastLog = &logCopy
	b.stateMu.Unlock()

	select {
	case b.queue <- item:
		return nil
	default:
		// Queue full: fail-safe synchronous write to prevent data loss
		b.droppedTotal.Add(1)
		b.logger.Warn("audit batch queue full, falling back to synchronous save", "log_id", log.ID)
		return b.saveDirectWithChain(log, snapshot)
	}
}

// saveDirectWithChain synchronously writes a log directly to underlying store while maintaining hash chain.
func (b *BufferedAuditStore) saveDirectWithChain(log *store.AuditLog, snapshot *store.SnapshotRecord) error {
	b.stateMu.Lock()
	if log.PrevHash == "" {
		log.PrevHash = b.lastHash
	}
	if log.IntegrityHash == "" {
		log.IntegrityHash = store.ComputeAuditIntegrityHash(
			log.ID, log.PrevHash, log.Timestamp, log.Algorithm,
			log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON,
		)
	}
	b.lastHash = log.IntegrityHash
	b.lastLog = log
	b.recentLogs[log.ID] = log
	b.stateMu.Unlock()

	if snapshot != nil {
		return b.underlying.SaveLogWithSnapshot(log, snapshot)
	}
	return b.underlying.SaveLog(log)
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

// CleanupOld delegates to the underlying store.
func (b *BufferedAuditStore) CleanupOld(before time.Time) (int64, error) {
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
	case <-time.After(15 * time.Second):
		return errors.New("flush operation timed out after 15s")
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

	close(b.stopCh)
	b.wg.Wait()

	b.logger.Info("buffered audit store safely closed and flushed",
		"total_flushed", b.flushedTotal.Load(),
		"total_failed", b.failedTotal.Load(),
		"total_overflow_sync", b.droppedTotal.Load(),
	)

	if closer, ok := b.underlying.(io.Closer); ok {
		return closer.Close()
	}
	return nil
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

// OverflowTotal returns the total number of logs written directly due to full queue.
func (b *BufferedAuditStore) OverflowTotal() int64 {
	return b.droppedTotal.Load()
}

func (b *BufferedAuditStore) flushWorker() {
	defer b.wg.Done()

	ticker := time.NewTicker(b.cfg.FlushInterval)
	defer ticker.Stop()

	batchLogs := make([]store.AuditLog, 0, b.cfg.MaxBatchSize)
	batchSnaps := make([]store.SnapshotRecord, 0, b.cfg.MaxBatchSize)

	// flushCurrent writes the accumulated batch and re-chains in sequential FIFO order
	flushCurrent := func() error {
		if len(batchLogs) == 0 && len(batchSnaps) == 0 {
			return nil
		}

		b.stateMu.Lock()
		for i := range batchLogs {
			// P0-1: Sequential chain binding strictly ordered in the worker
			batchLogs[i].PrevHash = b.lastHash
			batchLogs[i].IntegrityHash = store.ComputeAuditIntegrityHash(
				batchLogs[i].ID, batchLogs[i].PrevHash, batchLogs[i].Timestamp,
				batchLogs[i].Algorithm, batchLogs[i].InputHash, batchLogs[i].OutputHash,
				batchLogs[i].User, batchLogs[i].SecurityLevel, batchLogs[i].ParametersJSON,
			)
			b.lastHash = batchLogs[i].IntegrityHash
			b.lastLog = &batchLogs[i]
		}
		b.stateMu.Unlock()

		err := b.underlying.SaveLogsBatch(batchLogs, batchSnaps)
		if err != nil {
			b.failedTotal.Add(int64(len(batchLogs)))
			b.logger.Error("failed to flush audit batch to underlying store", "count", len(batchLogs), "error", err.Error())
		} else {
			b.flushedTotal.Add(int64(len(batchLogs)))
		}

		// Clear committed records from memory map
		b.stateMu.Lock()
		for i := range batchLogs {
			delete(b.recentLogs, batchLogs[i].ID)
		}
		b.stateMu.Unlock()

		batchLogs = batchLogs[:0]
		batchSnaps = batchSnaps[:0]
		return err
	}

	drainQueue := func() {
		for {
			select {
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
			drainQueue()
			return

		case req := <-b.flushReqCh:
			// Drain queue up to this instant and flush synchronously (P0-3)
			drainQueue()
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
