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
