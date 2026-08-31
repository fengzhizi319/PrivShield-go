package flusher

import (
	"fmt"
	"sync"
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

func TestBufferedAuditStore_ConcurrentWrites(t *testing.T) {
	memStore := memory.NewAuditStore()
	cfg := Config{
		BufferSize:    5000,
		MaxBatchSize:  50,
		FlushInterval: 10 * time.Millisecond,
	}

	bufStore := NewBufferedAuditStore(memStore, cfg, nil)

	const goroutines = 20
	const itemsPerGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(gID int) {
			defer wg.Done()
			for i := 0; i < itemsPerGoroutine; i++ {
				_ = bufStore.SaveLogWithSnapshot(&store.AuditLog{
					ID:        fmt.Sprintf("log-%d-%d", gID, i),
					Operation: "k_anon",
					Status:    "success",
					Timestamp: time.Now(),
				}, &store.SnapshotRecord{
					ID:         fmt.Sprintf("snap-%d-%d", gID, i),
					AuditLogID: fmt.Sprintf("log-%d-%d", gID, i),
					Timestamp:  time.Now(),
				})
			}
		}(g)
	}

	wg.Wait()
	bufStore.Close()

	_, totalLogs, err := memStore.ListLogs(store.AuditFilter{})
	if err != nil {
		t.Fatalf("ListLogs error: %v", err)
	}
	expected := goroutines * itemsPerGoroutine
	if totalLogs != expected {
		t.Fatalf("expected %d total logs, got %d", expected, totalLogs)
	}

	_, totalSnaps, err := memStore.ListSnapshots(10000, 0)
	if err != nil {
		t.Fatalf("ListSnapshots error: %v", err)
	}
	if totalSnaps != expected {
		t.Fatalf("expected %d total snapshots, got %d", expected, totalSnaps)
	}
}

// TestBufferedAuditStore_HashChainIntegrity (P0-1 Fix Validation)
func TestBufferedAuditStore_HashChainIntegrity(t *testing.T) {
	memStore := memory.NewAuditStore()
	cfg := Config{
		BufferSize:    2000,
		MaxBatchSize:  20,
		FlushInterval: 15 * time.Millisecond,
	}

	bufStore := NewBufferedAuditStore(memStore, cfg, nil)

	const totalLogs = 100
	for i := 0; i < totalLogs; i++ {
		err := bufStore.SaveLog(&store.AuditLog{
			ID:             fmt.Sprintf("log-chain-%03d", i),
			Operation:      "mask",
			Status:         "success",
			Timestamp:      time.Now().UTC(),
			Algorithm:      "SM3",
			InputHash:      fmt.Sprintf("in-hash-%d", i),
			OutputHash:     fmt.Sprintf("out-hash-%d", i),
			User:           "admin",
			SecurityLevel:  "L2",
			ParametersJSON: "{}",
		})
		if err != nil {
			t.Fatalf("SaveLog failed at %d: %v", i, err)
		}
	}

	// Verify chain after flushing
	res, err := bufStore.VerifyChain(1000)
	if err != nil {
		t.Fatalf("VerifyChain error: %v", err)
	}

	if !res.Valid {
		t.Fatalf("expected hash chain to be valid, got broken at ID %s: %s (expected: %s, actual: %s)",
			res.BrokenAtID, res.Message, res.ExpectedHash, res.ActualHash)
	}

	if res.TotalVerified != totalLogs {
		t.Fatalf("expected %d logs verified, got %d", totalLogs, res.TotalVerified)
	}

	bufStore.Close()
}

// TestBufferedAuditStore_ReadYourOwnWrites (P1-1 Fix Validation)
func TestBufferedAuditStore_ReadYourOwnWrites(t *testing.T) {
	memStore := memory.NewAuditStore()
	cfg := Config{
		BufferSize:    500,
		MaxBatchSize:  50,
		FlushInterval: 500 * time.Millisecond, // Long flush interval
	}

	bufStore := NewBufferedAuditStore(memStore, cfg, nil)
	defer bufStore.Close()

	logID := "log-instant-001"
	err := bufStore.SaveLog(&store.AuditLog{
		ID:        logID,
		Operation: "dp",
		Status:    "success",
		Timestamp: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("SaveLog failed: %v", err)
	}

	// Read immediately while still in memory queue
	log, err := bufStore.GetLog(logID)
	if err != nil {
		t.Fatalf("expected GetLog to succeed for un-flushed log, got: %v", err)
	}
	if log == nil || log.ID != logID {
		t.Fatalf("expected log %s, got %+v", logID, log)
	}

	latest, err := bufStore.GetLatestLog()
	if err != nil {
		t.Fatalf("GetLatestLog error: %v", err)
	}
	if latest == nil || latest.ID != logID {
		t.Fatalf("expected latest log %s, got %+v", logID, latest)
	}
}

// TestBufferedAuditStore_ConcurrentFlush (P0-3 Fix Validation)
func TestBufferedAuditStore_ConcurrentFlush(t *testing.T) {
	memStore := memory.NewAuditStore()
	cfg := Config{
		BufferSize:    1000,
		MaxBatchSize:  25,
		FlushInterval: 10 * time.Millisecond,
	}

	bufStore := NewBufferedAuditStore(memStore, cfg, nil)

	var wg sync.WaitGroup
	// Writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				_ = bufStore.SaveLog(&store.AuditLog{
					ID:        fmt.Sprintf("log-cf-%d-%d", writerID, j),
					Operation: "mask",
					Status:    "success",
					Timestamp: time.Now().UTC(),
				})
			}
		}(i)
	}

	// Flushers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				time.Sleep(5 * time.Millisecond)
				_ = bufStore.Flush()
			}
		}()
	}

	wg.Wait()
	bufStore.Close()

	_, total, err := memStore.ListLogs(store.AuditFilter{})
	if err != nil {
		t.Fatalf("ListLogs error: %v", err)
	}
	if total != 300 {
		t.Fatalf("expected 300 total logs, got %d", total)
	}
}
