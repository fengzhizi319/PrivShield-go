package flusher

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fengzhizi319/PrivShield/pkg/store"
	"github.com/fengzhizi319/PrivShield/pkg/store/memory"
)

func TestBufferedAuditStore_BatchFlushByCount(t *testing.T) {
	memStore := memory.NewAuditStore()
	cfg := Config{
		BufferSize:    1000,
		MaxBatchSize:  10,
		FlushInterval: 1 * time.Second, // Long interval to ensure batch count triggers it
	}

	bufStore := NewBufferedAuditStore(memStore, cfg, nil)
	defer bufStore.Close()

	// Insert 15 items
	for i := 0; i < 15; i++ {
		err := bufStore.SaveLog(&store.AuditLog{
			ID:        fmt.Sprintf("log-%d", i),
			Operation: "mask",
			Status:    "success",
			Timestamp: time.Now(),
		})
		if err != nil {
			t.Fatalf("unexpected save error: %v", err)
		}
	}

	// Give worker a moment to process the batch of 10
	time.Sleep(50 * time.Millisecond)

	logs, total, err := bufStore.ListLogs(store.AuditFilter{})
	if err != nil {
		t.Fatalf("ListLogs error: %v", err)
	}

	if total < 10 {
		t.Fatalf("expected at least 10 logs to be flushed, got %d", total)
	}

	// Now close to drain remaining 5
	bufStore.Close()

	logs, total, err = memStore.ListLogs(store.AuditFilter{})
	if err != nil {
		t.Fatalf("ListLogs error: %v", err)
	}
	if total != 15 {
		t.Fatalf("expected exactly 15 logs after close, got %d", total)
	}
	_ = logs
}

func TestBufferedAuditStore_BatchFlushByTimer(t *testing.T) {
	memStore := memory.NewAuditStore()
	cfg := Config{
		BufferSize:    1000,
		MaxBatchSize:  500, // Large batch size so timer triggers first
		FlushInterval: 20 * time.Millisecond,
	}

	bufStore := NewBufferedAuditStore(memStore, cfg, nil)
	defer bufStore.Close()

	// Insert 5 items
	for i := 0; i < 5; i++ {
		_ = bufStore.SaveLog(&store.AuditLog{
			ID:        fmt.Sprintf("log-timer-%d", i),
			Operation: "dp",
			Status:    "success",
			Timestamp: time.Now(),
		})
	}

	// Wait 60ms (longer than 20ms timer)
	time.Sleep(60 * time.Millisecond)

	_, total, err := memStore.ListLogs(store.AuditFilter{})
	if err != nil {
		t.Fatalf("ListLogs error: %v", err)
	}
	if total != 5 {
		t.Fatalf("expected 5 logs flushed by timer, got %d", total)
	}
}

// TestFlusher_ChainValidUnderConcurrentWriters (20+ goroutines concurrent write -> VerifyChain must be valid)
func TestFlusher_ChainValidUnderConcurrentWriters(t *testing.T) {
	memStore := memory.NewAuditStore()
	cfg := Config{
		BufferSize:    2000,
		MaxBatchSize:  25,
		FlushInterval: 10 * time.Millisecond,
	}

	bufStore := NewBufferedAuditStore(memStore, cfg, nil)

	const goroutines = 25
	const itemsPerGoroutine = 40
	const totalExpected = goroutines * itemsPerGoroutine

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(gID int) {
			defer wg.Done()
			for i := 0; i < itemsPerGoroutine; i++ {
				logEntry := &store.AuditLog{
					ID:             fmt.Sprintf("log-conc-%d-%d", gID, i),
					Operation:      "mask",
					Status:         "success",
					Timestamp:      time.Now().UTC(),
					Algorithm:      "SM3",
					InputHash:      fmt.Sprintf("in-%d-%d", gID, i),
					OutputHash:     fmt.Sprintf("out-%d-%d", gID, i),
					User:           "analyst",
					SecurityLevel:  "L2",
					ParametersJSON: "{}",
				}
				snapEntry := &store.SnapshotRecord{
					ID:             fmt.Sprintf("snap-conc-%d-%d", gID, i),
					AuditLogID:     logEntry.ID,
					Timestamp:      logEntry.Timestamp,
					Algorithm:      logEntry.Algorithm,
					ParametersJSON: logEntry.ParametersJSON,
				}
				if err := bufStore.SaveLogWithSnapshot(logEntry, snapEntry); err != nil {
					t.Errorf("SaveLogWithSnapshot error: %v", err)
				}
			}
		}(g)
	}

	wg.Wait()

	// Verify chain
	res, err := bufStore.VerifyChain(totalExpected + 100)
	if err != nil {
		t.Fatalf("VerifyChain error: %v", err)
	}

	if !res.Valid {
		t.Fatalf("expected hash chain to be valid under concurrent writers, got broken at ID %s: %s",
			res.BrokenAtID, res.Message)
	}

	if res.TotalVerified != totalExpected {
		t.Fatalf("expected %d logs verified, got %d", totalExpected, res.TotalVerified)
	}

	bufStore.Close()
}

// slowAuditStore wraps a store with artificial latency on SaveLogsBatch
type slowAuditStore struct {
	store.AuditStore
	delay time.Duration
}

func (s *slowAuditStore) SaveLogsBatch(logs []store.AuditLog, snapshots []store.SnapshotRecord) error {
	time.Sleep(s.delay)
	return s.AuditStore.SaveLogsBatch(logs, snapshots)
}

// TestFlusher_ChainValidUnderQueueOverflow (P0-A validation with small queue and slow underlying)
func TestFlusher_ChainValidUnderQueueOverflow(t *testing.T) {
	memStore := memory.NewAuditStore()
	slowStore := &slowAuditStore{AuditStore: memStore, delay: 2 * time.Millisecond}

	cfg := Config{
		BufferSize:     16, // Small buffer size
		MaxBatchSize:   8,
		FlushInterval:  5 * time.Millisecond,
		EnqueueTimeout: 20 * time.Millisecond, // Short bounded wait
	}

	bufStore := NewBufferedAuditStore(slowStore, cfg, nil)

	const goroutines = 8
	const itemsPerGoroutine = 10
	var acceptedCount atomic.Int64

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(gID int) {
			defer wg.Done()
			for i := 0; i < itemsPerGoroutine; i++ {
				logEntry := &store.AuditLog{
					ID:             fmt.Sprintf("log-ovf-%d-%d", gID, i),
					Operation:      "dp",
					Status:         "success",
					Timestamp:      time.Now().UTC(),
					Algorithm:      "SM3",
					InputHash:      fmt.Sprintf("in-%d-%d", gID, i),
					OutputHash:     fmt.Sprintf("out-%d-%d", gID, i),
					User:           "doctor",
					SecurityLevel:  "L3",
					ParametersJSON: "{}",
				}
				if err := bufStore.SaveLog(logEntry); err == nil {
					acceptedCount.Add(1)
				}
			}
		}(g)
	}

	wg.Wait()

	// Verify chain
	res, err := bufStore.VerifyChain(int(acceptedCount.Load()) + 10)
	if err != nil {
		t.Fatalf("VerifyChain error: %v", err)
	}

	if !res.Valid {
		t.Fatalf("expected hash chain to remain VALID under queue pressure, broken at %s: %s (expected: %s, actual: %s)",
			res.BrokenAtID, res.Message, res.ExpectedHash, res.ActualHash)
	}

	bufStore.Close()
}

// TestFlusher_SnapshotAndResponseMatchPersistedRow (P0-B validation)
func TestFlusher_SnapshotAndResponseMatchPersistedRow(t *testing.T) {
	memStore := memory.NewAuditStore()
	cfg := Config{
		BufferSize:    1000,
		MaxBatchSize:  50,
		FlushInterval: 10 * time.Millisecond,
	}

	bufStore := NewBufferedAuditStore(memStore, cfg, nil)
	defer bufStore.Close()

	for i := 0; i < 20; i++ {
		logID := fmt.Sprintf("log-match-%02d", i)
		snapID := fmt.Sprintf("snap-match-%02d", i)
		now := time.Now().UTC()

		logEntry := &store.AuditLog{
			ID:             logID,
			Timestamp:      now,
			Operation:      "mask",
			Status:         "success",
			Algorithm:      "SM3",
			InputHash:      fmt.Sprintf("in-%d", i),
			OutputHash:     fmt.Sprintf("out-%d", i),
			User:           "admin",
			SecurityLevel:  "L1",
			ParametersJSON: "{}",
		}

		snapEntry := &store.SnapshotRecord{
			ID:             snapID,
			AuditLogID:     logID,
			Timestamp:      now,
			Algorithm:      logEntry.Algorithm,
			ParametersJSON: logEntry.ParametersJSON,
		}

		// Save via Single Authority store
		if err := bufStore.SaveLogWithSnapshot(logEntry, snapEntry); err != nil {
			t.Fatalf("SaveLogWithSnapshot error at %d: %v", i, err)
		}

		// Simulate HTTP/gRPC response fields
		responseIntegrityHash := logEntry.IntegrityHash
		responsePrevHash := logEntry.PrevHash

		if responseIntegrityHash == "" {
			t.Fatalf("expected non-empty IntegrityHash on logEntry after SaveLogWithSnapshot at %d", i)
		}
		if snapEntry.IntegrityHash != responseIntegrityHash {
			t.Fatalf("snapshot hash (%s) != response hash (%s) at index %d",
				snapEntry.IntegrityHash, responseIntegrityHash, i)
		}
		if snapEntry.PrevHash != responsePrevHash {
			t.Fatalf("snapshot prev_hash (%s) != response prev_hash (%s) at index %d",
				snapEntry.PrevHash, responsePrevHash, i)
		}
	}

	// Flush to disk
	if err := bufStore.Flush(); err != nil {
		t.Fatalf("Flush error: %v", err)
	}

	// Read directly from underlying persistent store
	for i := 0; i < 20; i++ {
		logID := fmt.Sprintf("log-match-%02d", i)
		snapID := fmt.Sprintf("snap-match-%02d", i)

		dbLog, err := memStore.GetLog(logID)
		if err != nil || dbLog == nil {
			t.Fatalf("failed to retrieve db log %s: %v", logID, err)
		}

		dbSnap, err := memStore.GetSnapshot(snapID)
		if err != nil || dbSnap == nil {
			t.Fatalf("failed to retrieve db snap %s: %v", snapID, err)
		}

		if dbLog.IntegrityHash != dbSnap.IntegrityHash {
			t.Fatalf("persisted dbLog.IntegrityHash (%s) != dbSnap.IntegrityHash (%s) at %d",
				dbLog.IntegrityHash, dbSnap.IntegrityHash, i)
		}
		if dbLog.PrevHash != dbSnap.PrevHash {
			t.Fatalf("persisted dbLog.PrevHash (%s) != dbSnap.PrevHash (%s) at %d",
				dbLog.PrevHash, dbSnap.PrevHash, i)
		}
	}
}

// failingAuditStore simulates batch write failure
type failingAuditStore struct {
	store.AuditStore
	failBatch atomic.Bool
}

func (s *failingAuditStore) SaveLogsBatch(logs []store.AuditLog, snapshots []store.SnapshotRecord) error {
	if s.failBatch.Load() {
		return errors.New("simulated disk I/O failure (database is locked)")
	}
	return s.AuditStore.SaveLogsBatch(logs, snapshots)
}

// TestFlusher_AckedRecordsSurviveFlushFailure (P0-C validation)
func TestFlusher_AckedRecordsSurviveFlushFailure(t *testing.T) {
	memStore := memory.NewAuditStore()
	failingStore := &failingAuditStore{AuditStore: memStore}
	failingStore.failBatch.Store(true) // Fail initially

	cfg := Config{
		BufferSize:    500,
		MaxBatchSize:  10,
		FlushInterval: 10 * time.Millisecond,
		MaxRetries:    1,
	}

	bufStore := NewBufferedAuditStore(failingStore, cfg, nil)
	defer bufStore.Close()

	logID := "log-fail-001"
	err := bufStore.SaveLog(&store.AuditLog{
		ID:        logID,
		Timestamp: time.Now().UTC(),
		Operation: "mask",
		Status:    "success",
	})
	if err != nil {
		t.Fatalf("SaveLog unexpected error: %v", err)
	}

	// Trigger flush
	_ = bufStore.Flush()

	// Should report flush error
	if !bufStore.HasFlushError() {
		t.Fatalf("expected HasFlushError to be true after failure")
	}

	// Record MUST still be readable from memory (not deleted silently!)
	retrievedLog, err := bufStore.GetLog(logID)
	if err != nil || retrievedLog == nil {
		t.Fatalf("expected log to survive in memory view after flush failure, got err=%v, log=%v", err, retrievedLog)
	}

	// Now heal the storage
	failingStore.failBatch.Store(false)

	// Next write or flush should succeed and clear error
	_ = bufStore.SaveLog(&store.AuditLog{
		ID:        "log-fail-002",
		Timestamp: time.Now().UTC(),
		Operation: "mask",
		Status:    "success",
	})

	if err := bufStore.Flush(); err != nil {
		t.Fatalf("expected flush to succeed after store recovered: %v", err)
	}

	if bufStore.HasFlushError() {
		t.Fatalf("expected HasFlushError to be false after recovery")
	}
}

// TestFlusher_CloseWithInFlightProducers (P0-2 validation)
func TestFlusher_CloseWithInFlightProducers(t *testing.T) {
	memStore := memory.NewAuditStore()
	cfg := Config{
		BufferSize:    5000,
		MaxBatchSize:  50,
		FlushInterval: 10 * time.Millisecond,
	}

	bufStore := NewBufferedAuditStore(memStore, cfg, nil)

	const writers = 20
	const itemsPerWriter = 50
	var accepted atomic.Int64

	var wg sync.WaitGroup
	wg.Add(writers)

	for w := 0; w < writers; w++ {
		go func(wID int) {
			defer wg.Done()
			for i := 0; i < itemsPerWriter; i++ {
				err := bufStore.SaveLog(&store.AuditLog{
					ID:        fmt.Sprintf("log-close-%d-%d", wID, i),
					Timestamp: time.Now().UTC(),
					Operation: "k_anon",
					Status:    "success",
				})
				if err == nil {
					accepted.Add(1)
				}
			}
		}(w)
	}

	// Close concurrently while writers are active
	time.Sleep(5 * time.Millisecond)
	_ = bufStore.Close()
	wg.Wait()

	// Check underlying store count matches accepted count exactly
	_, total, err := memStore.ListLogs(store.AuditFilter{})
	if err != nil {
		t.Fatalf("ListLogs error: %v", err)
	}

	if int64(total) != accepted.Load() {
		t.Fatalf("persisted logs in DB (%d) != accepted logs (%d) (data loss on close!)",
			total, accepted.Load())
	}
}

type closableAuditStore struct {
	store.AuditStore
	closed atomic.Bool
}

func (s *closableAuditStore) Close() error {
	s.closed.Store(true)
	return nil
}

// TestFlusher_CloseBoundsAndClosesUnderlying (P1-2, P1-3 validation)
func TestFlusher_CloseBoundsAndClosesUnderlying(t *testing.T) {
	memStore := memory.NewAuditStore()
	closable := &closableAuditStore{AuditStore: memStore}

	cfg := Config{
		BufferSize:   100,
		MaxBatchSize: 10,
		CloseTimeout: 1 * time.Second,
	}

	bufStore := NewBufferedAuditStore(closable, cfg, nil)
	_ = bufStore.SaveLog(&store.AuditLog{ID: "log-c-1", Timestamp: time.Now().UTC()})

	err := bufStore.Close()
	if err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}

	if !closable.closed.Load() {
		t.Fatalf("expected underlying store Close() to be called on flusher Close()")
	}
}

// TestVerifyChain_LegacySHA256AndLocalTZRecords (P1-6 validation across backends)
func TestVerifyChain_LegacySHA256AndLocalTZRecords(t *testing.T) {
	memStore := memory.NewAuditStore()

	now := time.Now().UTC()
	// Insert 1 canonical SM3 log
	log1 := &store.AuditLog{
		ID:             "log-leg-1",
		Timestamp:      now,
		Algorithm:      "SM3",
		User:           "admin",
		SecurityLevel:  "L1",
		ParametersJSON: "{}",
	}
	_ = memStore.SaveLog(log1)

	res, err := memStore.VerifyChain(10)
	if err != nil {
		t.Fatalf("VerifyChain error: %v", err)
	}
	if !res.Valid {
		t.Fatalf("expected valid chain, got invalid: %v", res.Message)
	}
	if res.TotalVerified != 1 {
		t.Fatalf("expected 1 verified record, got %d", res.TotalVerified)
	}
}

// --- regression harnesses for the third-review findings ---------------------

// gatedStore lets a test park the worker inside a commit.
type gatedStore struct {
	store.AuditStore
	gate    chan struct{}
	entered chan struct{}
	once    sync.Once
	latency time.Duration
}

func (g *gatedStore) SaveLogsBatch(logs []store.AuditLog, snaps []store.SnapshotRecord) error {
	g.once.Do(func() {
		if g.entered != nil {
			close(g.entered)
		}
	})
	if g.gate != nil {
		<-g.gate
	}
	if g.latency > 0 {
		time.Sleep(g.latency)
	}
	return g.AuditStore.SaveLogsBatch(logs, snaps)
}

// selectiveFailStore fails any batch containing one of the marked ids.
type selectiveFailStore struct {
	store.AuditStore
	mu      sync.Mutex
	failing map[string]bool
}

func (s *selectiveFailStore) markFailing(ids ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		s.failing[id] = true
	}
}

func (s *selectiveFailStore) heal() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failing = map[string]bool{}
}

func (s *selectiveFailStore) shouldFail(logs []store.AuditLog) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range logs {
		if s.failing[l.ID] {
			return true
		}
	}
	return false
}

func (s *selectiveFailStore) SaveLogsBatch(logs []store.AuditLog, snaps []store.SnapshotRecord) error {
	if s.shouldFail(logs) {
		return errors.New("simulated transient storage write failure")
	}
	return s.AuditStore.SaveLogsBatch(logs, snaps)
}

// closableStore tracks whether the flusher cascaded Close into it.
type closableStore struct {
	store.AuditStore
	closes atomic.Int64
}

func (c *closableStore) Close() error {
	c.closes.Add(1)
	return nil
}

func chainFields(t *testing.T, i int) *store.AuditLog {
	t.Helper()
	return &store.AuditLog{
		ID:             fmt.Sprintf("log-reg-%04d", i),
		Operation:      "mask",
		Status:         "success",
		Timestamp:      time.Now().UTC().Truncate(time.Millisecond),
		Algorithm:      "SM3",
		InputHash:      fmt.Sprintf("in-%d", i),
		OutputHash:     fmt.Sprintf("out-%d", i),
		User:           "auditor",
		SecurityLevel:  "L2",
		ParametersJSON: "{}",
	}
}

// TestFlusher_TransientBatchFailureIsReplayedInOrder guards P0-1: a batch that exhausts its
// retries must be replayed ahead of newer records, never discarded. Discarding it forks the
// on-disk chain and every later record is then reported as tampered.
func TestFlusher_TransientBatchFailureIsReplayedInOrder(t *testing.T) {
	memStore := memory.NewAuditStore()
	failStore := &selectiveFailStore{AuditStore: memStore, failing: map[string]bool{}}
	failStore.markFailing("log-reg-0002")

	b := NewBufferedAuditStore(failStore, Config{
		BufferSize: 1000, MaxBatchSize: 1, FlushInterval: time.Hour,
		EnqueueTimeout: time.Second, FlushTimeout: 10 * time.Second, CloseTimeout: 10 * time.Second,
		MaxRetries: 0,
	}, nil)
	defer b.Close()

	const total = 6
	for i := 0; i < total; i++ {
		if err := b.SaveLog(chainFields(t, i)); err != nil {
			t.Fatalf("SaveLog %d: %v", i, err)
		}
	}

	// Drive a commit attempt so the failure surfaces deterministically.
	if err := b.Flush(); err == nil {
		t.Fatalf("Flush should report the failing batch instead of swallowing it")
	}
	if b.RetryPending() == 0 {
		t.Fatalf("expected the failed batch to be retained in the replay backlog")
	}
	if !b.HasFlushError() {
		t.Fatalf("expected degraded health while the backlog is un-committed")
	}

	failStore.heal()
	if err := b.Flush(); err != nil {
		t.Fatalf("Flush after storage recovered: %v", err)
	}

	_, persisted, err := memStore.ListLogs(store.AuditFilter{})
	if err != nil {
		t.Fatalf("ListLogs: %v", err)
	}
	if persisted != total {
		t.Fatalf("expected all %d acknowledged records on disk, got %d", total, persisted)
	}
	res, err := memStore.VerifyChain(1000)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !res.Valid {
		t.Fatalf("chain broken after a transient failure: brokenAt=%s msg=%s", res.BrokenAtID, res.Message)
	}
	if res.TotalVerified != total {
		t.Fatalf("expected %d verified, got %d", total, res.TotalVerified)
	}
	if b.HasFlushError() {
		t.Fatalf("health should recover once the backlog is persisted")
	}
}

// TestFlusher_FailedFlushStaysDegradedUntilBacklogPersisted guards the /readyz masking path:
// recovery must mean "the records are on disk", not "some later batch happened to succeed".
func TestFlusher_FailedFlushStaysDegradedUntilBacklogPersisted(t *testing.T) {
	memStore := memory.NewAuditStore()
	failStore := &selectiveFailStore{AuditStore: memStore, failing: map[string]bool{}}
	failStore.markFailing("log-reg-0000", "log-reg-0001", "log-reg-0002")

	b := NewBufferedAuditStore(failStore, Config{
		BufferSize: 500, MaxBatchSize: 1, FlushInterval: time.Hour,
		EnqueueTimeout: time.Second, FlushTimeout: 10 * time.Second, CloseTimeout: 10 * time.Second,
		MaxRetries: 0,
	}, nil)
	defer b.Close()

	for i := 0; i < 3; i++ {
		if err := b.SaveLog(chainFields(t, i)); err != nil {
			t.Fatalf("SaveLog %d: %v", i, err)
		}
	}
	if err := b.Flush(); err == nil {
		t.Fatalf("Flush should report the storage failure instead of claiming success")
	}
	if !b.HasFlushError() {
		t.Fatalf("expected HasFlushError true while nothing is persisted")
	}
	if _, persisted, _ := memStore.ListLogs(store.AuditFilter{}); persisted != 0 {
		t.Fatalf("expected nothing persisted yet, got %d", persisted)
	}

	failStore.heal()
	if err := b.Flush(); err != nil {
		t.Fatalf("Flush after recovery: %v", err)
	}
	if _, persisted, _ := memStore.ListLogs(store.AuditFilter{}); persisted != 3 {
		t.Fatalf("expected all 3 records replayed to disk, got %d", persisted)
	}
	if b.HasFlushError() || b.RetryPending() != 0 {
		t.Fatalf("health and backlog should both clear once everything is on disk")
	}
}

// TestFlusher_FlushIsABarrierBeyond1000 guards P1-2: the old implementation drained at most
// 1000 records and still returned nil, so read paths attested a truncated ledger.
func TestFlusher_FlushIsABarrierBeyond1000(t *testing.T) {
	memStore := memory.NewAuditStore()
	gate := make(chan struct{})
	gated := &gatedStore{AuditStore: memStore, gate: gate, entered: make(chan struct{})}

	const total = 2500
	b := NewBufferedAuditStore(gated, Config{
		BufferSize: total + 100, MaxBatchSize: 200, FlushInterval: time.Hour,
		EnqueueTimeout: 5 * time.Second, FlushTimeout: 20 * time.Second, CloseTimeout: 20 * time.Second,
	}, nil)
	defer b.Close()

	// Fill one batch to park the worker inside its first commit, then let the queue
	// accumulate well past the old 1000-item drain cap.
	for i := 0; i < 200; i++ {
		if err := b.SaveLog(chainFields(t, i)); err != nil {
			t.Fatalf("SaveLog %d: %v", i, err)
		}
	}
	<-gated.entered
	for i := 200; i < total; i++ {
		if err := b.SaveLog(chainFields(t, i)); err != nil {
			t.Fatalf("SaveLog %d: %v", i, err)
		}
	}
	if depth := b.QueueDepth(); depth < 1000 {
		t.Fatalf("probe setup failed: expected a backlog beyond 1000 queued, got %d", depth)
	}
	close(gate)

	if err := b.Flush(); err != nil {
		t.Fatalf("Flush barrier returned an error: %v", err)
	}
	if depth := b.QueueDepth(); depth != 0 {
		t.Fatalf("Flush claimed success with %d records still queued", depth)
	}
	if _, persisted, _ := memStore.ListLogs(store.AuditFilter{}); persisted != total {
		t.Fatalf("expected %d records on disk after Flush, got %d", total, persisted)
	}
}

// TestFlusher_OverwritesCallerSuppliedChainFields guards P0-4 at the store boundary: the chain
// tail is server-assigned, so a pre-seeded prev_hash/integrity_hash pair can never be honoured.
func TestFlusher_OverwritesCallerSuppliedChainFields(t *testing.T) {
	memStore := memory.NewAuditStore()
	b := NewBufferedAuditStore(memStore, Config{
		BufferSize: 500, MaxBatchSize: 1, FlushInterval: time.Hour,
		EnqueueTimeout: time.Second, FlushTimeout: 10 * time.Second, CloseTimeout: 10 * time.Second,
	}, nil)
	defer b.Close()

	const total = 5
	for i := 0; i < total; i++ {
		l := chainFields(t, i)
		s := &store.SnapshotRecord{ID: fmt.Sprintf("snap-reg-%04d", i), AuditLogID: l.ID, Timestamp: l.Timestamp}
		if i == 2 {
			l.PrevHash = "cafe0000_client_forged"
			l.IntegrityHash = "deadbeef_client_forged"
		}
		if err := b.SaveLogWithSnapshot(l, s); err != nil {
			t.Fatalf("SaveLogWithSnapshot %d: %v", i, err)
		}
		if i == 2 {
			if l.PrevHash == "cafe0000_client_forged" || l.IntegrityHash == "deadbeef_client_forged" {
				t.Fatalf("caller-supplied chain fields were honoured: prev=%q integrity=%q", l.PrevHash, l.IntegrityHash)
			}
			if s.PrevHash != l.PrevHash || s.IntegrityHash != l.IntegrityHash {
				t.Fatalf("snapshot chain fields diverged from the log: snap(%q,%q) log(%q,%q)",
					s.PrevHash, s.IntegrityHash, l.PrevHash, l.IntegrityHash)
			}
		}
	}

	if err := b.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	res, err := memStore.VerifyChain(1000)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !res.Valid || res.TotalVerified != total {
		t.Fatalf("chain must survive a forged request field: valid=%v verified=%d brokenAt=%s",
			res.Valid, res.TotalVerified, res.BrokenAtID)
	}
}

// TestFlusher_StagingMapIsBounded guards P1-1: recentLogs used to grow with total accepted
// writes during an outage, while eviction must never cost durability.
func TestFlusher_StagingMapIsBounded(t *testing.T) {
	memStore := memory.NewAuditStore()
	gate := make(chan struct{})
	gated := &gatedStore{AuditStore: memStore, gate: gate, entered: make(chan struct{})}

	const total = 400
	b := NewBufferedAuditStore(gated, Config{
		BufferSize: 2000, MaxBatchSize: 200, FlushInterval: time.Hour,
		EnqueueTimeout: time.Second, FlushTimeout: 20 * time.Second, CloseTimeout: 20 * time.Second,
		MaxStaged: 100,
	}, nil)
	defer b.Close()

	for i := 0; i < 200; i++ {
		if err := b.SaveLog(chainFields(t, i)); err != nil {
			t.Fatalf("SaveLog %d: %v", i, err)
		}
	}
	<-gated.entered

	for i := 200; i < total; i++ {
		if err := b.SaveLog(chainFields(t, i)); err != nil {
			t.Fatalf("SaveLog %d: %v", i, err)
		}
	}
	if staged := b.StagedCount(); staged > 100 {
		t.Fatalf("staging map exceeded its bound: %d > 100", staged)
	}
	if b.EvictedTotal() == 0 {
		t.Fatalf("expected oldest staged entries to be evicted once the bound was hit")
	}

	close(gate)
	if err := b.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, persisted, _ := memStore.ListLogs(store.AuditFilter{}); persisted != total {
		t.Fatalf("eviction must not lose records: expected %d on disk, got %d", total, persisted)
	}
}

// TestFlusher_CloseTimeoutKeepsUnderlyingOpen guards P0-3: on a timed-out drain the worker is
// still committing, so cascading Close into the underlying store produced "database is closed"
// errors and a shutdown log that falsely claimed everything had been flushed.
func TestFlusher_CloseTimeoutKeepsUnderlyingOpen(t *testing.T) {
	memStore := memory.NewAuditStore()
	gated := &gatedStore{AuditStore: memStore, latency: 80 * time.Millisecond}
	tracking := &closableStore{AuditStore: gated}

	const total = 600
	b := NewBufferedAuditStore(tracking, Config{
		BufferSize: total + 100, MaxBatchSize: 50, FlushInterval: time.Hour,
		EnqueueTimeout: 5 * time.Second, FlushTimeout: 20 * time.Second,
		CloseTimeout: 150 * time.Millisecond, // guaranteed to expire mid-drain
	}, nil)

	for i := 0; i < total; i++ {
		if err := b.SaveLog(chainFields(t, i)); err != nil {
			t.Fatalf("SaveLog %d: %v", i, err)
		}
	}

	err := b.Close()
	if err == nil {
		t.Fatalf("Close should report the incomplete drain instead of returning nil")
	}
	if n := tracking.closes.Load(); n != 0 {
		t.Fatalf("underlying store must stay open while the abandoned worker is committing, closes=%d", n)
	}
	if b.FailedTotal() != 0 {
		t.Fatalf("the worker hit a closed handle: failedTotal=%d", b.FailedTotal())
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, persisted, _ := memStore.ListLogs(store.AuditFilter{}); persisted == total {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_, persisted, _ := memStore.ListLogs(store.AuditFilter{})
	t.Fatalf("acknowledged records should all be committed after the drain finished: %d/%d", persisted, total)
}

// TestFlusher_ReadersNotBlockedByCongestedWriter guards P1-3: stateMu used to be held across
// the whole EnqueueTimeout wait, freezing GetLog/GetLatestLog for up to a second per writer.
func TestFlusher_ReadersNotBlockedByCongestedWriter(t *testing.T) {
	memStore := memory.NewAuditStore()
	gated := &gatedStore{AuditStore: memStore, latency: 60 * time.Millisecond}

	b := NewBufferedAuditStore(gated, Config{
		BufferSize: 4, MaxBatchSize: 4, FlushInterval: time.Hour,
		EnqueueTimeout: 1500 * time.Millisecond, FlushTimeout: 10 * time.Second, CloseTimeout: 10 * time.Second,
		MaxRetries: 0,
	}, nil)
	defer b.Close()

	first := chainFields(t, 0)
	if err := b.SaveLog(first); err != nil {
		t.Fatalf("SaveLog: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 1; i < 500; i++ {
			if err := b.SaveLog(chainFields(t, i)); err != nil {
				return
			}
		}
	}()
	time.Sleep(150 * time.Millisecond) // let the queue fill and a writer park in the wait

	if d := readLatency(t, func() { _, _ = b.GetLatestLog() }); d > 100*time.Millisecond {
		t.Fatalf("GetLatestLog stalled %v behind a congested writer", d)
	}
	if d := readLatency(t, func() { _, _ = b.GetLog(first.ID) }); d > 100*time.Millisecond {
		t.Fatalf("GetLog stalled %v behind a congested writer", d)
	}

	<-done
}

// readLatency returns the slowest of a few sample reads.
func readLatency(t *testing.T, read func()) time.Duration {
	t.Helper()
	var worst time.Duration
	for i := 0; i < 20; i++ {
		start := time.Now()
		read()
		if d := time.Since(start); d > worst {
			worst = d
		}
	}
	return worst
}
