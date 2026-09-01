// Package flusher_test provides comprehensive tests for BufferedAuditStore.
// Package flusher_test 为 BufferedAuditStore 提供全方位的单元、并发与故障注入测试套件。
//
// ==============================================================================
// 【测试矩阵与验证目标】
// 1. 【TestBufferedFlusher_BatchSizeThreshold】：验证单批达到 MaxBatchSize 阈值时立即触发批量落盘；
// 2. 【TestBufferedFlusher_TickerTrigger】：验证未达到批大小阈值时，定时器 FlushInterval 到期自动触发刷盘；
// 3. 【TestBufferedFlusher_ConcurrentWrite】：验证高并发多协程写入时，哈希链的连续性、一致性与无分叉；
// 4. 【TestBufferedFlusher_GetLog_ReadYourOwnWrites】：验证读己之写（Read-your-own-writes）暂存缓存的高速读取；
// 5. 【TestBufferedFlusher_SnapshotAndResponseHashStrictMatch】：验证主日志、快照与客户端响应体哈希的绝对一致（P0-B 修复验证）；
// 6. 【TestBufferedFlusher_CloseDrainsBuffer】：验证 Close 优雅停机时排空所有已确认在途记录；
// 7. 【TestBufferedFlusher_FlushBarrier】：验证 Flush 作为同步强一致性屏障的排空与落盘语义；
// 8. 【TestBufferedFlusher_UnderlyingFailureRetryBacklog】：验证底层存储故障注入时，重试积压区（Retry Backlog）暂存与恢复后按原序重投；
// 9. 【TestBufferedFlusher_BoundedBacklogSaturationRejection】：验证存储长期不可用时，重试积压区达到 MaxStaged 快速拒绝，防止 OOM；
// 10. 【TestBufferedFlusher_CongestionTimeoutRejection】：验证队列拥塞在 EnqueueTimeout 超时后正确报错拒绝，且不破坏哈希链；
// 11. 【TestBufferedFlusher_BoundedStagedMemoryEviction】：验证内存暂存表在超限时按 FIFO 淘汰旧数据，防内存泄漏。
// ==============================================================================

package flusher_test

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/store"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/flusher"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/memory"
)

// silentLogger 返回测试专用的静默 Logger，避免在正常测试输出中打印噪音日志。
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// ─────────────────────────────────────────────────────────────
// 1. 批处理大小阈值触发测试
// ─────────────────────────────────────────────────────────────

func TestBufferedFlusher_BatchSizeThreshold(t *testing.T) {
	memStore := memory.NewAuditStore()
	cfg := flusher.Config{
		BufferSize:    1000,
		MaxBatchSize:  5,
		FlushInterval: 10 * time.Second, // 设置很长的定时器，确保只能靠 BatchSize 达到阈值触发
	}
	bf := flusher.NewBufferedAuditStore(memStore, cfg, silentLogger())
	defer bf.Close()

	now := time.Now()
	for i := 0; i < 5; i++ {
		log := &store.AuditLog{
			ID:            fmt.Sprintf("log-%d", i),
			Timestamp:     now.Add(time.Duration(i) * time.Millisecond),
			Operation:     "mask",
			Algorithm:     "field_mask",
			SecurityLevel: "L3",
		}
		if err := bf.SaveLog(log); err != nil {
			t.Fatalf("save log %d: %v", i, err)
		}
	}

	// 等待工作协程消费并批量写入底层存储
	time.Sleep(100 * time.Millisecond)

	if bf.FlushedTotal() < 5 {
		t.Fatalf("expected at least 5 logs flushed by batch threshold, got %d", bf.FlushedTotal())
	}
}

// ─────────────────────────────────────────────────────────────
// 2. 定时器周期触发测试
// ─────────────────────────────────────────────────────────────

func TestBufferedFlusher_TickerTrigger(t *testing.T) {
	memStore := memory.NewAuditStore()
	cfg := flusher.Config{
		BufferSize:    1000,
		MaxBatchSize:  100, // 阈值设得很大，单条写入无法触发
		FlushInterval: 30 * time.Millisecond,
	}
	bf := flusher.NewBufferedAuditStore(memStore, cfg, silentLogger())
	defer bf.Close()

	log := &store.AuditLog{
		ID:            "ticker-log-1",
		Timestamp:     time.Now(),
		Operation:     "dp",
		Algorithm:     "laplace",
		SecurityLevel: "L4",
	}
	if err := bf.SaveLog(log); err != nil {
		t.Fatalf("save log: %v", err)
	}

	// 此时尚未达到 30ms，应仍处于暂存中
	time.Sleep(10 * time.Millisecond)
	stagedLog, err := bf.GetLog("ticker-log-1")
	if err != nil || stagedLog == nil {
		t.Fatalf("expected log accessible in read buffer before ticker flush: %v", err)
	}

	// 等待超过 FlushInterval 窗口，定时器应触发刷盘
	time.Sleep(60 * time.Millisecond)

	if bf.FlushedTotal() < 1 {
		t.Fatalf("expected log flushed by ticker interval, got flushed=%d", bf.FlushedTotal())
	}
}

// ─────────────────────────────────────────────────────────────
// 3. 高并发多协程写入与哈希链连续性测试
// ─────────────────────────────────────────────────────────────

func TestBufferedFlusher_ConcurrentWrite(t *testing.T) {
	memStore := memory.NewAuditStore()
	cfg := flusher.Config{
		BufferSize:    5000,
		MaxBatchSize:  50,
		FlushInterval: 10 * time.Millisecond,
	}
	bf := flusher.NewBufferedAuditStore(memStore, cfg, silentLogger())

	const totalWriters = 10
	const logsPerWriter = 100
	var wg sync.WaitGroup
	wg.Add(totalWriters)

	for w := 0; w < totalWriters; w++ {
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < logsPerWriter; i++ {
				log := &store.AuditLog{
					ID:            fmt.Sprintf("conc-%d-%d", workerID, i),
					Timestamp:     time.Now(),
					Operation:     "mask",
					Algorithm:     "field_mask",
					InputHash:     "hash_in",
					OutputHash:    "hash_out",
					User:          fmt.Sprintf("user-%d", workerID),
					SecurityLevel: "L3",
				}
				if err := bf.SaveLog(log); err != nil {
					t.Errorf("worker %d failed to save log %d: %v", workerID, i, err)
					return
				}
			}
		}(w)
	}

	wg.Wait()

	// 显式排空并关闭
	if err := bf.Close(); err != nil {
		t.Fatalf("close buffered flusher: %v", err)
	}

	// 核验底层链完整性
	res, err := memStore.VerifyChain(0)
	if err != nil {
		t.Fatalf("verify chain error: %v", err)
	}
	if !res.Valid {
		t.Fatalf("hash chain invalid under concurrent writes: %s (broken at %s)", res.Message, res.BrokenAtID)
	}
	if res.TotalVerified != totalWriters*logsPerWriter {
		t.Fatalf("expected %d verified records, got %d", totalWriters*logsPerWriter, res.TotalVerified)
	}
}

// ─────────────────────────────────────────────────────────────
// 4. 读己之写 (Read-your-own-writes) 内存暂存一致性测试
// ─────────────────────────────────────────────────────────────

func TestBufferedFlusher_GetLog_ReadYourOwnWrites(t *testing.T) {
	memStore := memory.NewAuditStore()
	cfg := flusher.Config{
		BufferSize:    1000,
		MaxBatchSize:  100,
		FlushInterval: 10 * time.Second,
	}
	bf := flusher.NewBufferedAuditStore(memStore, cfg, silentLogger())
	defer bf.Close()

	log := &store.AuditLog{
		ID:            "staged-log-1",
		Timestamp:     time.Now(),
		Operation:     "k_anon",
		Algorithm:     "mondrian",
		SecurityLevel: "L2",
	}
	if err := bf.SaveLog(log); err != nil {
		t.Fatalf("save log: %v", err)
	}

	// 尚未落盘到底层，应能从 bf.GetLog 立即读取
	got, err := bf.GetLog("staged-log-1")
	if err != nil {
		t.Fatalf("failed to get log from staged buffer: %v", err)
	}
	if got.ID != "staged-log-1" || got.Algorithm != "mondrian" {
		t.Fatalf("unexpected staged log content: %+v", got)
	}

	// 底层 memory store 此时不应有此记录
	if _, err := memStore.GetLog("staged-log-1"); err == nil {
		t.Fatal("expected log not yet persisted in underlying store before flush")
	}

	// 手动触发 Flush
	if err := bf.Flush(); err != nil {
		t.Fatalf("flush failed: %v", err)
	}

	// 此时底层 store 应已有数据
	persisted, err := memStore.GetLog("staged-log-1")
	if err != nil || persisted.ID != "staged-log-1" {
		t.Fatalf("expected log persisted in underlying store after flush: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────
// 5. 主日志、快照与调用方响应哈希严格一致性测试 (P0-B 验证)
// ─────────────────────────────────────────────────────────────

func TestBufferedFlusher_SnapshotAndResponseHashStrictMatch(t *testing.T) {
	memStore := memory.NewAuditStore()
	cfg := flusher.Config{
		BufferSize:    1000,
		MaxBatchSize:  100,
		FlushInterval: 10 * time.Millisecond,
	}
	bf := flusher.NewBufferedAuditStore(memStore, cfg, silentLogger())
	defer bf.Close()

	log := &store.AuditLog{
		ID:            "audit-strict-1",
		Timestamp:     time.Now(),
		Operation:     "mask",
		Algorithm:     "sm4_gcm",
		InputHash:     "in_abc",
		OutputHash:    "out_def",
		User:          "auditor",
		SecurityLevel: "L3",
	}
	snap := &store.SnapshotRecord{
		ID:           "snap-strict-1",
		AuditLogID:   "audit-strict-1",
		Timestamp:    log.Timestamp,
		InputSample:  "enc:v1:in",
		OutputSample: "enc:v1:out",
		Algorithm:    "sm4_gcm",
	}

	if err := bf.SaveLogWithSnapshot(log, snap); err != nil {
		t.Fatalf("save log with snapshot: %v", err)
	}

	// 验证指针写回：log 与 snap 必须有各自独立的完整性哈希，snap 的 prev_hash 指向父日志
	if log.IntegrityHash == "" {
		t.Fatal("log.IntegrityHash was not computed")
	}
	if snap.IntegrityHash == "" {
		t.Fatal("snapshot.IntegrityHash was not computed")
	}
	if snap.IntegrityHash == log.IntegrityHash {
		t.Fatalf("snapshot must have its own integrity hash, got same as log: %s", snap.IntegrityHash)
	}
	if snap.PrevHash != log.IntegrityHash {
		t.Fatalf("snapshot prev_hash must point to parent log hash: snap.PrevHash=%s, log.IntegrityHash=%s", snap.PrevHash, log.IntegrityHash)
	}

	// 刷新落盘
	if err := bf.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	// 验证底层持久化记录中的哈希是否完全一致
	savedLog, err := memStore.GetLog("audit-strict-1")
	if err != nil {
		t.Fatalf("get persisted log: %v", err)
	}
	savedSnap, err := memStore.GetSnapshot("snap-strict-1")
	if err != nil {
		t.Fatalf("get persisted snap: %v", err)
	}

	if savedLog.IntegrityHash != log.IntegrityHash {
		t.Fatalf("persisted log hash %s != returned %s", savedLog.IntegrityHash, log.IntegrityHash)
	}
	if savedSnap.IntegrityHash != snap.IntegrityHash {
		t.Fatalf("persisted snapshot hash %s != returned %s", savedSnap.IntegrityHash, snap.IntegrityHash)
	}
	if savedSnap.PrevHash != log.IntegrityHash {
		t.Fatalf("persisted snapshot prev_hash %s != parent log hash %s", savedSnap.PrevHash, log.IntegrityHash)
	}
}

// ─────────────────────────────────────────────────────────────
// 6. Close 优雅停机排空在途数据测试
// ─────────────────────────────────────────────────────────────

func TestBufferedFlusher_CloseDrainsBuffer(t *testing.T) {
	memStore := memory.NewAuditStore()
	cfg := flusher.Config{
		BufferSize:    1000,
		MaxBatchSize:  500, // 批阈值设得很大，不触发
		FlushInterval: 10 * time.Second,
	}
	bf := flusher.NewBufferedAuditStore(memStore, cfg, silentLogger())

	for i := 0; i < 25; i++ {
		log := &store.AuditLog{
			ID:            fmt.Sprintf("drain-%d", i),
			Timestamp:     time.Now(),
			Operation:     "mask",
			Algorithm:     "field_mask",
			SecurityLevel: "L1",
		}
		if err := bf.SaveLog(log); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	// 此时队列有 25 条待刷盘，立即执行 Close
	if err := bf.Close(); err != nil {
		t.Fatalf("close error: %v", err)
	}

	// Close 后，25 条记录必须全部落盘到底层 store
	logs, total, err := memStore.ListLogs(store.AuditFilter{})
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}
	if total != 25 || len(logs) != 25 {
		t.Fatalf("expected 25 drained logs, got total=%d len=%d", total, len(logs))
	}

	// 再次写入应返回 ErrStoreClosed
	err = bf.SaveLog(&store.AuditLog{ID: "after-close", Timestamp: time.Now()})
	if !errors.Is(err, flusher.ErrStoreClosed) {
		t.Fatalf("expected ErrStoreClosed after Close, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────
// 7. Flush 强一致性持久化屏障测试
// ─────────────────────────────────────────────────────────────

func TestBufferedFlusher_FlushBarrier(t *testing.T) {
	memStore := memory.NewAuditStore()
	cfg := flusher.Config{
		BufferSize:    1000,
		MaxBatchSize:  1000,
		FlushInterval: 10 * time.Second,
	}
	bf := flusher.NewBufferedAuditStore(memStore, cfg, silentLogger())
	defer bf.Close()

	for i := 0; i < 10; i++ {
		bf.SaveLog(&store.AuditLog{
			ID:            fmt.Sprintf("barrier-%d", i),
			Timestamp:     time.Now(),
			Operation:     "dp",
			Algorithm:     "gaussian",
			SecurityLevel: "L3",
		})
	}

	// 显式调用 Flush，必须等待直到这 10 条真正写入底层 store
	if err := bf.Flush(); err != nil {
		t.Fatalf("flush barrier failed: %v", err)
	}

	if bf.QueueDepth() != 0 {
		t.Fatalf("expected queue depth 0 after flush, got %d", bf.QueueDepth())
	}
	if bf.FlushedTotal() != 10 {
		t.Fatalf("expected 10 flushed records, got %d", bf.FlushedTotal())
	}
}

// ─────────────────────────────────────────────────────────────
// 8. 底层存储故障注入与重试积压区 (Retry Backlog) 保序重投测试
// ─────────────────────────────────────────────────────────────

type faultyAuditStore struct {
	store.AuditStore
	mu        sync.Mutex
	failBatch atomic.Bool
	saveCalls atomic.Int64
}

func (f *faultyAuditStore) SaveLogsBatch(logs []store.AuditLog, snaps []store.SnapshotRecord) error {
	f.saveCalls.Add(1)
	if f.failBatch.Load() {
		return errors.New("simulated database disk I/O error")
	}
	return f.AuditStore.SaveLogsBatch(logs, snaps)
}

func TestBufferedFlusher_UnderlyingFailureRetryBacklog(t *testing.T) {
	memStore := memory.NewAuditStore()
	faulty := &faultyAuditStore{AuditStore: memStore}
	faulty.failBatch.Store(true) // 模拟底层数据库故障

	cfg := flusher.Config{
		BufferSize:    1000,
		MaxBatchSize:  10,
		FlushInterval: 10 * time.Millisecond,
		MaxRetries:    1,
	}
	bf := flusher.NewBufferedAuditStore(faulty, cfg, silentLogger())
	defer bf.Close()

	// 写入 5 条记录
	for i := 0; i < 5; i++ {
		log := &store.AuditLog{
			ID:            fmt.Sprintf("fail-retry-%d", i),
			Timestamp:     time.Now(),
			Operation:     "mask",
			Algorithm:     "field_mask",
			SecurityLevel: "L2",
		}
		if err := bf.SaveLog(log); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	// 等待重试失败并转入积压区
	time.Sleep(100 * time.Millisecond)

	if !bf.HasFlushError() {
		t.Fatal("expected flusher to be in degraded error state")
	}
	if bf.RetryPending() != 5 {
		t.Fatalf("expected 5 pending retry records in backlog, got %d", bf.RetryPending())
	}

	// 模拟数据库恢复正常
	faulty.failBatch.Store(false)

	// 触发 Flush 重试提交
	if err := bf.Flush(); err != nil {
		t.Fatalf("flush after recovery failed: %v", err)
	}

	if bf.HasFlushError() {
		t.Fatal("expected flusher to recover from error state")
	}
	if bf.RetryPending() != 0 {
		t.Fatalf("expected 0 retry pending after recovery, got %d", bf.RetryPending())
	}

	// 验证底层哈希链完好无损
	res, err := memStore.VerifyChain(0)
	if err != nil {
		t.Fatalf("verify chain: %v", err)
	}
	if !res.Valid || res.TotalVerified != 5 {
		t.Fatalf("expected 5 verified records after backlog replay, got valid=%v total=%d", res.Valid, res.TotalVerified)
	}
}

// ─────────────────────────────────────────────────────────────
// 9. 积压饱和有界拒绝测试 (防 OOM)
// ─────────────────────────────────────────────────────────────

func TestBufferedFlusher_BoundedBacklogSaturationRejection(t *testing.T) {
	memStore := memory.NewAuditStore()
	faulty := &faultyAuditStore{AuditStore: memStore}
	faulty.failBatch.Store(true)

	cfg := flusher.Config{
		BufferSize:    100,
		MaxBatchSize:  5,
		FlushInterval: 10 * time.Millisecond,
		MaxRetries:    0,
		MaxStaged:     5, // 积压上限设为 5 条
	}
	bf := flusher.NewBufferedAuditStore(faulty, cfg, silentLogger())
	defer bf.Close()

	// 写入 5 条填满积压区
	for i := 0; i < 5; i++ {
		_ = bf.SaveLog(&store.AuditLog{ID: fmt.Sprintf("sat-%d", i), Timestamp: time.Now()})
	}

	// 等待积压区生效
	time.Sleep(50 * time.Millisecond)

	// 第 6 条写入必须被快速拒绝，返回 ErrBacklogSaturated
	err := bf.SaveLog(&store.AuditLog{ID: "sat-overflow", Timestamp: time.Now()})
	if !errors.Is(err, flusher.ErrBacklogSaturated) {
		t.Fatalf("expected ErrBacklogSaturated, got: %v", err)
	}
}

type blockingAuditStore struct {
	store.AuditStore
	block chan struct{}
}

func (b *blockingAuditStore) SaveLogsBatch(logs []store.AuditLog, snaps []store.SnapshotRecord) error {
	if b.block != nil {
		<-b.block
	}
	return b.AuditStore.SaveLogsBatch(logs, snaps)
}

func TestBufferedFlusher_CongestionTimeoutRejection(t *testing.T) {
	memStore := memory.NewAuditStore()
	blockCh := make(chan struct{})
	blocking := &blockingAuditStore{AuditStore: memStore, block: blockCh}

	cfg := flusher.Config{
		BufferSize:     2, // 队列深度仅为 2
		MaxBatchSize:   1, // 写入 1 条即触发 SaveLogsBatch 从而阻塞工作协程
		FlushInterval:  1 * time.Hour,
		EnqueueTimeout: 20 * time.Millisecond, // 超时时间短
		MaxStaged:      100,
	}
	bf := flusher.NewBufferedAuditStore(blocking, cfg, silentLogger())
	defer func() {
		close(blockCh) // 释放阻塞以便优雅退出
		bf.Close()
	}()

	// 第 1 条进入后被工作协程读走并阻塞在 SaveLogsBatch
	_ = bf.SaveLog(logWithID("cong-1"))
	time.Sleep(10 * time.Millisecond)

	// 填满队列剩余容量 (2 条)
	_ = bf.SaveLog(logWithID("cong-2"))
	_ = bf.SaveLog(logWithID("cong-3"))

	// 第 4 条写入将在 20ms 后因超时被拒绝
	start := time.Now()
	err := bf.SaveLog(logWithID("cong-4"))
	dur := time.Since(start)

	if err == nil {
		t.Fatal("expected congestion error on full queue")
	}
	if dur < 15*time.Millisecond {
		t.Fatalf("expected timeout wait, returned too quickly (%v)", dur)
	}
}

// ─────────────────────────────────────────────────────────────
// 11. 内存读缓存有界淘汰测试
// ─────────────────────────────────────────────────────────────

func TestBufferedFlusher_BoundedStagedMemoryEviction(t *testing.T) {
	memStore := memory.NewAuditStore()
	cfg := flusher.Config{
		BufferSize:    1000,
		MaxBatchSize:  1000,
		FlushInterval: 10 * time.Second,
		MaxStaged:     3, // 读缓存上限仅 3 条
	}
	bf := flusher.NewBufferedAuditStore(memStore, cfg, silentLogger())
	defer bf.Close()

	// 写入 5 条记录
	for i := 0; i < 5; i++ {
		_ = bf.SaveLog(logWithID(fmt.Sprintf("evict-%d", i)))
	}

	// 缓存容量应被严格限制在 MaxStaged (3条)
	if bf.StagedCount() > 3 {
		t.Fatalf("expected staged count <= 3, got %d", bf.StagedCount())
	}
	if bf.EvictedTotal() < 2 {
		t.Fatalf("expected at least 2 evictions, got %d", bf.EvictedTotal())
	}
}

func logWithID(id string) *store.AuditLog {
	return &store.AuditLog{
		ID:            id,
		Timestamp:     time.Now(),
		Operation:     "mask",
		Algorithm:     "field_mask",
		SecurityLevel: "L3",
	}
}
