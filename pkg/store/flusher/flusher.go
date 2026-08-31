// Package flusher provides an in-memory buffered micro-batch flusher for store.AuditStore.
// Package flusher 为 store.AuditStore 提供基于内存缓冲的高性能微批异步刷盘器。
//
// 核心设计与保障：
// 1. 将高并发下的频繁单条写事务折叠为低频大事务（SaveLogsBatch），化解 SQLite 单写者锁竞争与 PG 连接争用；
// 2. 单一权威机制（Single Authority）：链尾 prev_hash 与 integrity_hash 只能由本存储在服务端裁定，
//    入队即在锁内确定并同步写回调用方的 AuditLog 与 SnapshotRecord 指针，消除日志行、快照行与
//    HTTP/gRPC 响应体的哈希分叉；调用方传入的 prev_hash 一律视为非法（见 services 层校验）；
// 3. 严格 FIFO 顺序入队与微批提交：链推进与入队成功在临界区内原子完成，队列拥塞时按 EnqueueTimeout
//    有界等待并显式失败，杜绝越序落盘与假阳性断链；
// 4. 持久性优先于吞吐：底层写入失败时整批保留在工作线程暂存区（retry backlog）并在下一轮**按原序**
//    优先重投，绝不丢弃已确认（acked）的记录；退避重试期间健康状态保持降级，直到积压真正落盘才清除；
// 5. 生命周期无竞态：closed 只由 stateMu 保护（单一互斥量、单一线程语义），停机先置位再关信号，
//    生产者持锁入队与 Close 互斥，因此 Close 之后的入队必被排空；排空超时则如实报告搁浅条数，
//    且不再关闭底层存储，避免被抛弃的工作线程写入已关闭句柄；
// 6. Flush 是真正的屏障：返回 nil 当且仅当队列与工作线程暂存区均已清空并成功提交，
//    因此 ListLogs/GetStats/VerifyChain 等读路径不会在数据尚未落盘时给出"完整且校验通过"的结论；
// 7. 内存有界：读己之写暂存映射（recentLogs）受 MaxStaged 约束并按入队序淘汰最旧条目，
//    重投暂存区同样有界，超限后快速拒绝新写入，防止存储长时间不可用导致 OOM。
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

// ErrBacklogSaturated is returned when the un-flushed retry backlog reached its bound,
// meaning the underlying storage has been unavailable long enough that accepting more
// audit records would risk unbounded memory growth.
var ErrBacklogSaturated = errors.New("audit flush backlog saturated, underlying storage unavailable")

// Config holds configuration parameters for BufferedAuditStore.
type Config struct {
	BufferSize     int           // 环形缓冲队列容量（默认 10000）
	MaxBatchSize   int           // 单批最大写入条数（默认 200）
	FlushInterval  time.Duration // 最长刷盘等待时间窗口（默认 20ms）
	EnqueueTimeout time.Duration // 队列满时等待可用槽位的超时时间（默认 500ms，防拥塞越序与丢数据）
	FlushTimeout   time.Duration // 显式 Flush 屏障等待超时时间（默认 5s）
	CloseTimeout   time.Duration // 优雅停机排空等待超时时间（默认 10s）
	MaxRetries     int           // 单批提交失败重试次数（默认 3）
	MaxStaged      int           // 内存暂存/重投积压上限（默认 50000，防存储故障期无界增长）
}

// DefaultConfig returns default high-performance batch flusher settings.
func DefaultConfig() Config {
	return Config{
		BufferSize:     10000,
		MaxBatchSize:   200,
		FlushInterval:  20 * time.Millisecond,
		EnqueueTimeout: 500 * time.Millisecond,
		FlushTimeout:   5 * time.Second,
		CloseTimeout:   10 * time.Second,
		MaxRetries:     3,
		MaxStaged:      50000,
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

	// stateMu is the ONLY guard of closed/lastHash. Holding it across the enqueue attempt
	// makes chain advancement and queue placement atomic, so a concurrent Close can never
	// strand an acknowledged record.
	stateMu  sync.Mutex
	closed   bool
	lastHash string

	// latest is published lock-free so GetLatestLog never blocks behind a congested writer.
	latest atomic.Pointer[store.AuditLog]

	// stageMu guards the read-your-own-writes map; readers take RLock and therefore never
	// wait on a producer that is parked in the EnqueueTimeout back-off.
	stageMu    sync.RWMutex
	recentLogs map[string]*store.AuditLog
	stagedIDs  []string // insertion order, used for bounded eviction

	flushedTotal  atomic.Int64
	failedTotal   atomic.Int64
	overflowTotal atomic.Int64
	evictedTotal  atomic.Int64
	retryPending  atomic.Int64

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
		cfg.FlushTimeout = 5 * time.Second
	}
	if cfg.CloseTimeout <= 0 {
		cfg.CloseTimeout = 10 * time.Second
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 3
	}
	if cfg.MaxStaged <= 0 {
		cfg.MaxStaged = 50000
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
		underlying: underlying,
		cfg:        cfg,
		logger:     logger,
		queue:      make(chan pendingItem, cfg.BufferSize),
		flushReqCh: make(chan flushRequest),
		stopCh:     make(chan struct{}),
		lastHash:   initHash,
		recentLogs: make(map[string]*store.AuditLog, 1024),
	}
	b.lastFlushErr.Store("")
	if initLog != nil {
		b.latest.Store(initLog)
	}

	b.wg.Add(1)
	go b.flushWorker()

	logger.Info("buffered audit batch store initialized",
		"buffer_size", cfg.BufferSize,
		"max_batch_size", cfg.MaxBatchSize,
		"flush_interval", cfg.FlushInterval.String(),
		"enqueue_timeout", cfg.EnqueueTimeout.String(),
		"max_staged", cfg.MaxStaged,
		"initial_chain_hash", initHash,
	)
	return b
}

// SaveLog puts the audit log into the batch buffer.
func (b *BufferedAuditStore) SaveLog(log *store.AuditLog) error {
	return b.SaveLogWithSnapshot(log, nil)
}

// SaveLogWithSnapshot establishes the single-authority hash chain and enqueues the log and snapshot.
func (b *BufferedAuditStore) SaveLogWithSnapshot(log *store.AuditLog, snapshot *store.SnapshotRecord) error {
	if log == nil {
		return errors.New("audit log cannot be nil")
	}
	if b.RetryPending() >= int64(b.cfg.MaxStaged) {
		b.overflowTotal.Add(1)
		return ErrBacklogSaturated
	}

	b.stateMu.Lock()
	if b.closed {
		b.stateMu.Unlock()
		return ErrStoreClosed
	}

	// Single Authority: the chain tail is decided by this store, never by the caller (P0-B).
	log.PrevHash = b.lastHash
	log.IntegrityHash = store.ComputeAuditIntegrityHash(
		log.ID, log.PrevHash, log.Timestamp, log.Algorithm,
		log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON,
	)

	logCopy := *log
	var snap *store.SnapshotRecord
	if snapshot != nil {
		s := *snapshot
		s.PrevHash = log.PrevHash
		s.IntegrityHash = log.IntegrityHash
		snap = &s
		*snapshot = s // Sync back to caller's snapshot pointer
	}

	item := pendingItem{log: &logCopy, snapshot: snap}

	// Try non-blocking enqueue first
	enqueued := false
	select {
	case b.queue <- item:
		enqueued = true
	default:
	}

	if !enqueued {
		// Queue full: wait up to EnqueueTimeout to maintain strict FIFO channel ordering (P0-A).
		timer := time.NewTimer(b.cfg.EnqueueTimeout)
		select {
		case b.queue <- item:
			enqueued = true
		case <-timer.C:
		}
		timer.Stop()
	}

	if !enqueued {
		b.overflowTotal.Add(1)
		b.stateMu.Unlock()
		b.logger.Warn("audit flusher queue congested, write rejected after timeout",
			"log_id", log.ID, "timeout", b.cfg.EnqueueTimeout.String())
		return fmt.Errorf("audit buffer queue congested: timeout waiting for slot after %v", b.cfg.EnqueueTimeout)
	}

	// The chain only advances for records that are safely queued, so a rejected write above
	// leaves lastHash untouched and the on-disk chain stays contiguous.
	b.lastHash = logCopy.IntegrityHash
	b.latest.Store(&logCopy)
	b.stateMu.Unlock()

	b.stageLog(&logCopy)
	*log = logCopy // Sync back to caller's log pointer
	return nil
}

// SaveLogsBatch routes bulk writes through the same single-authority enqueue path so that
// chain order and queue order can never diverge, then blocks until everything is committed.
func (b *BufferedAuditStore) SaveLogsBatch(logs []store.AuditLog, snapshots []store.SnapshotRecord) error {
	if len(logs) == 0 {
		if len(snapshots) == 0 {
			return nil
		}
		return b.underlying.SaveLogsBatch(nil, snapshots)
	}

	var firstErr error
	// Snapshots are only index-paired with logs when the caller passed equal-length slices;
	// otherwise they carry their own chain fields and go straight to the underlying store.
	pairSnapshots := len(snapshots) == len(logs)

	for i := range logs {
		var snap *store.SnapshotRecord
		if pairSnapshots {
			snap = &snapshots[i]
		}
		if err := b.SaveLogWithSnapshot(&logs[i], snap); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
	}

	if !pairSnapshots && len(snapshots) > 0 {
		if err := b.underlying.SaveLogsBatch(nil, snapshots); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// Always attempt the barrier flush so already-accepted records are not left pending.
	flushErr := b.Flush()
	if firstErr != nil {
		return firstErr
	}
	return flushErr
}

// GetLog returns the log from in-memory pending buffer if present, otherwise delegates to underlying store.
func (b *BufferedAuditStore) GetLog(id string) (*store.AuditLog, error) {
	b.stageMu.RLock()
	if log, ok := b.recentLogs[id]; ok {
		logCopy := *log
		b.stageMu.RUnlock()
		return &logCopy, nil
	}
	b.stageMu.RUnlock()

	return b.underlying.GetLog(id)
}

// GetLatestLog returns the newest buffered (possibly still un-flushed) chain tail log.
func (b *BufferedAuditStore) GetLatestLog() (*store.AuditLog, error) {
	if l := b.latest.Load(); l != nil {
		logCopy := *l
		return &logCopy, nil
	}
	return b.underlying.GetLatestLog()
}

// ListLogs delegates to the underlying store after flushing.
func (b *BufferedAuditStore) ListLogs(filter store.AuditFilter) ([]store.AuditLog, int, error) {
	if err := b.Flush(); err != nil {
		b.logger.Warn("flush before list logs incomplete, returning persisted subset", "error", err.Error())
	}
	return b.underlying.ListLogs(filter)
}

// GetStats delegates to the underlying store after flushing.
func (b *BufferedAuditStore) GetStats() (*store.AuditStats, error) {
	if err := b.Flush(); err != nil {
		b.logger.Warn("flush before stats incomplete, stats cover a persisted subset", "error", err.Error())
	}
	return b.underlying.GetStats()
}

// GenerateReport delegates to the underlying store after flushing.
func (b *BufferedAuditStore) GenerateReport(period string) (*store.AuditReport, error) {
	if err := b.Flush(); err != nil {
		b.logger.Warn("flush before report incomplete, report covers a persisted subset", "error", err.Error())
	}
	return b.underlying.GenerateReport(period)
}

// SaveSnapshot delegates to the underlying store.
func (b *BufferedAuditStore) SaveSnapshot(snap *store.SnapshotRecord) error {
	return b.underlying.SaveSnapshot(snap)
}

// ListSnapshots delegates to the underlying store after flushing.
func (b *BufferedAuditStore) ListSnapshots(limit, offset int) ([]store.SnapshotRecord, int, error) {
	if err := b.Flush(); err != nil {
		b.logger.Warn("flush before list snapshots incomplete, returning persisted subset", "error", err.Error())
	}
	return b.underlying.ListSnapshots(limit, offset)
}

// GetSnapshot delegates to the underlying store.
func (b *BufferedAuditStore) GetSnapshot(id string) (*store.SnapshotRecord, error) {
	return b.underlying.GetSnapshot(id)
}

// VerifyChain delegates to the underlying store after synchronously draining the entire buffer.
// A non-nil error means the buffer could not be fully drained, so any verification result would
// describe an incomplete ledger and must not be treated as a clean attestation.
func (b *BufferedAuditStore) VerifyChain(limit int) (*store.ChainVerificationResult, error) {
	if err := b.Flush(); err != nil {
		b.logger.Error("failed to flush buffer before chain verification", "error", err.Error())
		return nil, fmt.Errorf("flush before verify failed: %w", err)
	}
	return b.underlying.VerifyChain(limit)
}

// CleanupOld cleans up records from underlying store and purges corresponding memory entries.
func (b *BufferedAuditStore) CleanupOld(before time.Time) (int64, error) {
	b.stageMu.Lock()
	for id, l := range b.recentLogs {
		if l.Timestamp.Before(before) {
			delete(b.recentLogs, id)
		}
	}
	b.stageMu.Unlock()
	return b.underlying.CleanupOld(before)
}

// Flush is a durability barrier: it returns nil only when the queue and the retry backlog are
// both empty and committed.
func (b *BufferedAuditStore) Flush() error {
	b.stateMu.Lock()
	if b.closed {
		b.stateMu.Unlock()
		return nil
	}
	b.stateMu.Unlock()

	req := flushRequest{done: make(chan error, 1)}
	select {
	case b.flushReqCh <- req:
	case <-b.stopCh:
		return nil
	case <-time.After(b.cfg.FlushTimeout):
		return fmt.Errorf("flush request not served within %v (queue depth %d)", b.cfg.FlushTimeout, b.QueueDepth())
	}

	select {
	case err := <-req.done:
		return err
	case <-b.stopCh:
		return nil
	case <-time.After(b.cfg.FlushTimeout):
		return fmt.Errorf("flush did not complete within %v (queue depth %d)", b.cfg.FlushTimeout, b.QueueDepth())
	}
}

// Close stops the background worker, drains the entire buffer to disk, and closes the underlying store.
func (b *BufferedAuditStore) Close() error {
	b.stateMu.Lock()
	if b.closed {
		b.stateMu.Unlock()
		return nil
	}
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
		b.logger.Info("buffered audit store closed after full drain",
			"total_flushed", b.flushedTotal.Load(),
			"total_failed", b.failedTotal.Load(),
			"total_overflow_rejected", b.overflowTotal.Load(),
			"total_staged_evicted", b.evictedTotal.Load(),
		)
	case <-time.After(b.cfg.CloseTimeout):
		stranded := b.QueueDepth() + int(b.retryPending.Load())
		b.logger.Error("buffered audit store close timed out; acknowledged records may be un-flushed",
			"timeout", b.cfg.CloseTimeout.String(),
			"queue_depth", b.QueueDepth(),
			"retry_backlog", b.retryPending.Load(),
			"stranded", stranded,
			"total_flushed", b.flushedTotal.Load(),
		)
		closeErr = fmt.Errorf("buffered audit store close timed out after %v with %d records un-flushed", b.cfg.CloseTimeout, stranded)
	}

	if closeErr != nil {
		// The worker goroutine was abandoned and may still be committing. Closing the
		// underlying handle now would turn in-flight batches into "database is closed"
		// errors, so the caller owns the leak instead of the store.
		return closeErr
	}

	// Safely close underlying store if supported (P1-2)
	if closer, ok := b.underlying.(auditStoreCloser); ok {
		if err := closer.Close(); err != nil {
			closeErr = err
		}
	} else if closer, ok := b.underlying.(auditStoreSimpleCloser); ok {
		closer.Close()
	} else if closer, ok := b.underlying.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			closeErr = err
		}
	}

	b.stageMu.Lock()
	b.recentLogs = make(map[string]*store.AuditLog, 1024)
	b.stagedIDs = nil
	b.stageMu.Unlock()

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

// FailedTotal returns the total number of logs whose commit attempt exhausted all retries.
// These records are retained in the retry backlog and re-attempted, so this counter reports
// commit failures rather than permanent data loss.
func (b *BufferedAuditStore) FailedTotal() int64 {
	return b.failedTotal.Load()
}

// OverflowTotal returns the total number of logs rejected because the queue stayed full.
func (b *BufferedAuditStore) OverflowTotal() int64 {
	return b.overflowTotal.Load()
}

// EvictedTotal returns the total number of staged records dropped by the bounded read cache.
func (b *BufferedAuditStore) EvictedTotal() int64 {
	return b.evictedTotal.Load()
}

// RetryPending returns the number of records held in the worker's un-committed retry backlog.
func (b *BufferedAuditStore) RetryPending() int64 {
	return b.retryPending.Load()
}

// StagedCount returns the number of records currently served from the in-memory read buffer.
func (b *BufferedAuditStore) StagedCount() int {
	b.stageMu.RLock()
	defer b.stageMu.RUnlock()
	return len(b.recentLogs)
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

// stageLog records a committed-pending log in the bounded read-your-own-writes map.
func (b *BufferedAuditStore) stageLog(l *store.AuditLog) {
	b.stageMu.Lock()
	if _, dup := b.recentLogs[l.ID]; !dup {
		b.stagedIDs = append(b.stagedIDs, l.ID)
	}
	b.recentLogs[l.ID] = l

	// Compact stale ids, then evict oldest entries beyond the bound.
	if len(b.stagedIDs) > 2*b.cfg.MaxStaged || len(b.recentLogs) > b.cfg.MaxStaged {
		kept := b.stagedIDs[:0]
		for _, id := range b.stagedIDs {
			if _, ok := b.recentLogs[id]; ok {
				kept = append(kept, id)
			}
		}
		b.stagedIDs = kept
		for len(b.recentLogs) > b.cfg.MaxStaged && len(b.stagedIDs) > 0 {
			delete(b.recentLogs, b.stagedIDs[0])
			b.stagedIDs = b.stagedIDs[1:]
			b.evictedTotal.Add(1)
		}
	}
	b.stageMu.Unlock()
}

// unstageLogs removes committed records from the read buffer.
func (b *BufferedAuditStore) unstageLogs(logs []store.AuditLog) {
	b.stageMu.Lock()
	for i := range logs {
		delete(b.recentLogs, logs[i].ID)
	}
	b.stageMu.Unlock()
}

func (b *BufferedAuditStore) flushWorker() {
	defer b.wg.Done()

	ticker := time.NewTicker(b.cfg.FlushInterval)
	defer ticker.Stop()

	batchLogs := make([]store.AuditLog, 0, b.cfg.MaxBatchSize)
	batchSnaps := make([]store.SnapshotRecord, 0, b.cfg.MaxBatchSize)

	// backlog holds batches whose commit failed after all retries. It is always flushed
	// ahead of newer records, so persistence order keeps matching chain order (P0-1).
	var backlogLogs []store.AuditLog
	var backlogSnaps []store.SnapshotRecord

	flushCurrent := func() error {
		if len(batchLogs) == 0 && len(batchSnaps) == 0 && len(backlogLogs) == 0 && len(backlogSnaps) == 0 {
			return nil
		}

		// Preserve FIFO: backlog first, then the freshly accumulated batch.
		payloadLogs := batchLogs
		payloadSnaps := batchSnaps
		if len(backlogLogs) > 0 || len(backlogSnaps) > 0 {
			mergedLogs := make([]store.AuditLog, 0, len(backlogLogs)+len(batchLogs))
			mergedLogs = append(mergedLogs, backlogLogs...)
			mergedLogs = append(mergedLogs, batchLogs...)
			mergedSnaps := make([]store.SnapshotRecord, 0, len(backlogSnaps)+len(batchSnaps))
			mergedSnaps = append(mergedSnaps, backlogSnaps...)
			mergedSnaps = append(mergedSnaps, batchSnaps...)
			payloadLogs = mergedLogs
			payloadSnaps = mergedSnaps
		}

		var err error
		for attempt := 0; attempt <= b.cfg.MaxRetries; attempt++ {
			err = b.underlying.SaveLogsBatch(payloadLogs, payloadSnaps)
			if err == nil {
				break
			}
			if attempt < b.cfg.MaxRetries {
				time.Sleep(time.Duration(25*(1<<attempt)) * time.Millisecond)
			}
		}

		if err != nil {
			// Retain everything for replay: acknowledged records are never discarded, so a
			// transient storage failure cannot fork the hash chain any more.
			retainedLogs := make([]store.AuditLog, len(payloadLogs))
			copy(retainedLogs, payloadLogs)
			retainedSnaps := make([]store.SnapshotRecord, len(payloadSnaps))
			copy(retainedSnaps, payloadSnaps)
			backlogLogs, backlogSnaps = retainedLogs, retainedSnaps

			b.retryPending.Store(int64(len(backlogLogs)))
			b.failedTotal.Add(int64(len(payloadLogs)))
			b.hasFlushError.Store(true)
			b.lastFlushErr.Store(err.Error())
			b.logger.Error("audit batch commit failed after retries, retained for replay",
				"count", len(payloadLogs), "backlog", len(backlogLogs), "error", err.Error())
			batchLogs = batchLogs[:0]
			batchSnaps = batchSnaps[:0]
			return err
		}

		b.flushedTotal.Add(int64(len(payloadLogs)))
		b.unstageLogs(payloadLogs)
		backlogLogs, backlogSnaps = nil, nil
		b.retryPending.Store(0)
		batchLogs = batchLogs[:0]
		batchSnaps = batchSnaps[:0]

		if b.hasFlushError.Load() {
			b.hasFlushError.Store(false)
			b.lastFlushErr.Store("")
			b.logger.Info("audit flush backlog recovered and fully persisted", "replayed", len(payloadLogs))
		}
		return nil
	}

	// hasBacklog reports whether replay must be attempted before any newer record is committed.
	hasBacklog := func() bool { return len(backlogLogs) > 0 || len(backlogSnaps) > 0 }

	// drainQueue moves queued items into the current batch. While a backlog is pending it only
	// accumulates: replaying is driven by the ticker / explicit Flush, otherwise every
	// MaxBatchSize new records would re-submit the whole backlog.
	// maxItems <= 0 means "drain everything that is currently buffered".
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
				if len(batchLogs) >= b.cfg.MaxBatchSize && !hasBacklog() {
					_ = flushCurrent()
				}
				if maxItems > 0 && count >= maxItems {
					return
				}
			default:
				return
			}
		}
	}

	for {
		select {
		case <-b.stopCh:
			// Drain everything, then commit the backlog so shutdown loses nothing it accepted.
			drainQueue(0)
			_ = flushCurrent()
			return

		case req := <-b.flushReqCh:
			// Barrier: drain and commit until nothing is left, bounded by FlushTimeout so a
			// continuous producer cannot turn the barrier into an unbounded loop.
			deadline := time.Now().Add(b.cfg.FlushTimeout)
			var err error
			for {
				drainQueue(0)
				err = flushCurrent()
				// A failed commit is reported immediately; the ticker keeps replaying the
				// backlog so the caller never spins for the whole FlushTimeout.
				if err != nil || (b.QueueDepth() == 0 && !hasBacklog()) {
					break
				}
				if !time.Now().Before(deadline) {
					if err == nil {
						err = fmt.Errorf("flush incomplete after %v (queue depth %d, backlog %d)",
							b.cfg.FlushTimeout, b.QueueDepth(), len(backlogLogs))
					} else {
						err = fmt.Errorf("%w (flush still incomplete after %v, backlog %d)",
							err, b.cfg.FlushTimeout, len(backlogLogs))
					}
					break
				}
			}
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
			if len(batchLogs) >= b.cfg.MaxBatchSize && !hasBacklog() {
				_ = flushCurrent()
			}
		}
	}
}
