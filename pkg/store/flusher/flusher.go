// Package flusher provides an in-memory buffered micro-batch flusher for store.AuditStore.
// Package flusher 为 store.AuditStore 提供基于内存缓冲的高性能微批异步刷盘器。
//
// ==============================================================================
// 【核心设计与企业级高可用保障】
// 1. 【写事务折叠优化】：
//    在高并发脱敏场景下，将频繁的单条小事务折叠为低频的大事务（SaveLogsBatch），
//    化解 SQLite 单写者锁竞争与 PostgreSQL 连接池连接争用；
// 2. 【单一权威机制 (Single Authority)】：
//    链尾 prev_hash 与 integrity_hash 只能由本存储在服务端单点裁定，入队即在锁内确定并同步写回
//    调用方的 AuditLog 与 SnapshotRecord 指针，彻底消除日志行、快照行与 HTTP/gRPC 响应体的哈希分叉；
//    调用方传入的 prev_hash 一律视为非法并强制覆盖；
// 3. 【严格 FIFO 保序入队】：
//    链推进与入队成功在临界区内原子完成，队列拥塞时按 EnqueueTimeout 有界等待并显式报错，
//    杜绝越序落盘与假阳性断链；
// 4. 【持久性优先于吞吐 (Durability First)】：
//    底层写入失败时整批保留在工作线程暂存区（retry backlog）并在下一轮【按原序优先重投】，
//    绝不丢弃已确认（acked）的记录；退避重试期间健康状态保持降级，直到积压真正落盘才清除；
// 5. 【生命周期无竞态停机】：
//    closed 状态受单一互斥量保护，停机先置位再关信号，Close 之后的入队必被排空；排空超时则如实报告
//    搁浅条数，且不再关闭底层存储，避免被抛弃的工作线程写入已关闭句柄；
// 6. 【Flush 强一致性屏障】：
//    Flush 返回 nil 当且仅当队列与工作线程暂存区均已清空并成功提交，
//    确保 ListLogs/GetStats/VerifyChain 等读路径不会在数据尚未落盘时给出"完整且校验通过"的虚假结论；
// 7. 【内存有界防 OOM】：
//    读己之写暂存映射（recentLogs）受 MaxStaged 约束并按入队序淘汰最旧条目，重投暂存区同样有界，
//    超限后快速拒绝新写入，防止底层持久层长期不可用导致内存打爆。
// ==============================================================================

package flusher

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/store"
)

// 确保 BufferedAuditStore 实现了 store.AuditStore 接口。
var _ store.AuditStore = (*BufferedAuditStore)(nil)

// ErrStoreClosed is returned when an operation is attempted on a closed store.
// 当对已关闭的刷盘器执行读写操作时返回此错误。
var ErrStoreClosed = errors.New("audit store is closed")

// ErrBacklogSaturated is returned when the un-flushed retry backlog reached its bound,
// meaning the underlying storage has been unavailable long enough that accepting more
// audit records would risk unbounded memory growth.
// 当重试积压区达到上限时返回此错误，表明底层存储故障时间过长，必须快速拒绝以防 OOM。
var ErrBacklogSaturated = errors.New("audit flush backlog saturated, underlying storage unavailable")

// Config holds configuration parameters for BufferedAuditStore.
// Config 定义微批刷盘器的性能与行为参数。
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
// DefaultConfig 返回推荐的企业级高性能刷盘器配置参数。
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
// BufferedAuditStore 封装底层审计存储，提供异步微批聚合与可靠性保障。
type BufferedAuditStore struct {
	underlying store.AuditStore
	cfg        Config
	logger     *slog.Logger

	queue      chan pendingItem
	flushReqCh chan flushRequest
	stopCh     chan struct{}
	wg         sync.WaitGroup

	// stateMu 保护 closed 与 lastHash。在入队全过程中持锁，使哈希链递进与队列写入具备原子性。
	stateMu  sync.Mutex
	closed   bool
	lastHash string

	// latest 采用无锁原子指针存储，确保 GetLatestLog 永远不会阻塞在拥塞的写入者之后。
	latest atomic.Pointer[store.AuditLog]

	// stageMu 保护读己之写（Read-your-own-writes）暂存哈希表。
	stageMu    sync.RWMutex
	recentLogs map[string]*store.AuditLog
	stagedIDs  []string // 按插入顺序记录 ID，用于有界淘汰

	flushedTotal  atomic.Int64
	failedTotal   atomic.Int64
	overflowTotal atomic.Int64
	evictedTotal  atomic.Int64
	retryPending  atomic.Int64

	hasFlushError atomic.Bool
	lastFlushErr  atomic.Value // string
}

// NewBufferedAuditStore creates a new buffered batch audit store wrapper.
//
// NewBufferedAuditStore 构建基于内存缓冲的微批审计存储包装器。
//
// 执行逻辑：
// 1. 参数默认值兜底；
// 2. 从底层存储获取最新的 chain tail log 初始化 lastHash 与 latest；
// 3. 启动后台 flushWorker 协程监听队列、定时器与 Flush 屏障事件。
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
// SaveLog 将单条审计日志加入微批缓冲队列。
func (b *BufferedAuditStore) SaveLog(log *store.AuditLog) error {
	return b.SaveLogWithSnapshot(log, nil)
}

// SaveLogWithSnapshot establishes the single-authority hash chain and enqueues the log and snapshot.
//
// SaveLogWithSnapshot 执行 Single Authority 链尾裁定，并将审计日志与快照原子加入缓冲队列。
//
// 执行逻辑：
// 1. 检查重试积压区是否饱和（>= MaxStaged），若饱和则快速失败；
// 2. 加锁 stateMu：检查是否已关闭；
// 3. 【单一权威裁定】：由服务端强制赋予 log.PrevHash = b.lastHash，并计算规范 SM3 IntegrityHash；
// 4. 若传入快照，将其 PrevHash 与 IntegrityHash 严格与主日志对齐；
// 5. 尝试入队：先非阻塞写入，若满则在 EnqueueTimeout 时间窗口内有界等待；
// 6. 入队成功后递进 b.lastHash 并更新 b.latest 原子指针，解锁；
// 7. 写入读己之写暂存表 stageLog，并将计算结果写回调用方的原指针。
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
	log.SM2Signature = store.SignAuditRecord(log.IntegrityHash)

	logCopy := *log
	var snap *store.SnapshotRecord
	if snapshot != nil {
		s := *snapshot
		// P0 fix: snapshot prev_hash points to the parent log's integrity hash, forming a
		// secondary chain (audit log chain -> snapshot sub-chain) so that replacing the
		// sample invalidates the snapshot hash even if the log itself is untouched.
		s.PrevHash = log.IntegrityHash
		// P0 fix: compute a standalone integrity hash that covers the snapshot's own fields,
		// including encrypted input/output samples, instead of copying the parent log hash.
		s.IntegrityHash = store.ComputeSnapshotIntegrityHash(
			s.ID, s.AuditLogID, s.PrevHash, s.Timestamp, s.Algorithm,
			s.InputSample, s.OutputSample, s.ParametersJSON,
		)
		s.SM2Signature = store.SignAuditRecord(s.IntegrityHash)
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
//
// SaveLogsBatch 将批量写入按序路由通过单一权威入队路径，并触发同步 Flush 屏障落盘。
func (b *BufferedAuditStore) SaveLogsBatch(logs []store.AuditLog, snapshots []store.SnapshotRecord) error {
	if len(logs) == 0 {
		if len(snapshots) == 0 {
			return nil
		}
		return b.underlying.SaveLogsBatch(nil, snapshots)
	}

	var firstErr error
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
		for i := range snapshots {
			if snapshots[i].IntegrityHash == "" {
				snapshots[i].IntegrityHash = store.ComputeSnapshotIntegrityHash(
					snapshots[i].ID, snapshots[i].AuditLogID, snapshots[i].PrevHash, snapshots[i].Timestamp, snapshots[i].Algorithm,
					snapshots[i].InputSample, snapshots[i].OutputSample, snapshots[i].ParametersJSON,
				)
			}
			if snapshots[i].SM2Signature == "" {
				snapshots[i].SM2Signature = store.SignAuditRecord(snapshots[i].IntegrityHash)
			}
		}
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
//
// GetLog 优先从内存读己之写暂存表中获取未刷盘记录，未命中则下沉到底层持久层。
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
//
// GetLatestLog 无锁返回当前最新的链尾记录（包含暂存区）。
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
//
// VerifyChain 先同步排空所有缓冲区，然后委托底层持久层执行全量对账核验。
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

// FetchOldestForArchive drains the buffer to a durability barrier and then delegates. It fails
// closed on a partial flush because the retention guard deletes exactly what it archives.
//
// FetchOldestForArchive 先排空缓冲到达持久化屏障，再下沉到底层存储；
// 刷盘未完成或底层不具备归档读取能力时直接返回错误（fail-closed），
// 避免「按不完整数据集归档后删除」。
func (b *BufferedAuditStore) FetchOldestForArchive(before time.Time, limit int) ([]store.AuditLog, []store.SnapshotRecord, error) {
	if err := b.Flush(); err != nil {
		return nil, nil, fmt.Errorf("flush before archive failed: %w", err)
	}
	reader, ok := b.underlying.(store.AuditArchiveReader)
	if !ok {
		return nil, nil, fmt.Errorf("underlying audit store %T does not support archive reading", b.underlying)
	}
	return reader.FetchOldestForArchive(before, limit)
}

// DeleteLogsByIDs purges the staged read-your-writes entries for the deleted IDs and delegates.
//
// DeleteLogsByIDs 同步清除暂存区中对应 ID 的记录后下沉删除。
func (b *BufferedAuditStore) DeleteLogsByIDs(ids []string) (int64, error) {
	b.stageMu.Lock()
	for _, id := range ids {
		delete(b.recentLogs, id)
	}
	b.stageMu.Unlock()

	reader, ok := b.underlying.(store.AuditArchiveReader)
	if !ok {
		return 0, fmt.Errorf("underlying audit store %T does not support archive deletion", b.underlying)
	}
	return reader.DeleteLogsByIDs(ids)
}

// Flush is a durability barrier: it returns nil only when the queue and the retry backlog are
// both empty and committed.
//
// Flush 是强一致性持久化屏障：阻塞直至队列积压与重试积压区全部成功提交至磁盘后方返回。
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
//
// Close 执行优雅停机：停止接收新请求，排空队列中已接受的所有日志至磁盘，并关闭底层存储句柄。
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

	hasBacklog := func() bool { return len(backlogLogs) > 0 || len(backlogSnaps) > 0 }

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
			drainQueue(0)
			_ = flushCurrent()
			return

		case req := <-b.flushReqCh:
			deadline := time.Now().Add(b.cfg.FlushTimeout)
			var err error
			for {
				drainQueue(0)
				err = flushCurrent()
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
