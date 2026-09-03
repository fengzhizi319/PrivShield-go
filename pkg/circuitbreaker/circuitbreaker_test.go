package circuitbreaker

import (
	"sync"
	"testing"
	"time"
)

func TestBreaker_StateTransitions(t *testing.T) {
	cb := NewBreaker(3, 50*time.Millisecond)

	// Initially closed
	if cb.State() != StateClosed {
		t.Fatalf("expected StateClosed, got %v", cb.State())
	}
	if !cb.Allow() {
		t.Fatal("expected Allow() to be true in StateClosed")
	}

	// 2 failures should not trip
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != StateClosed {
		t.Fatalf("expected StateClosed after 2 failures, got %v", cb.State())
	}

	// 3rd failure trips to StateOpen
	cb.RecordFailure()
	if cb.State() != StateOpen {
		t.Fatalf("expected StateOpen after 3 failures, got %v", cb.State())
	}
	if cb.Allow() {
		t.Fatal("expected Allow() to be false immediately after tripping")
	}

	// Wait for cooldown
	time.Sleep(60 * time.Millisecond)

	// First call after cooldown should transition to HalfOpen and allow first probe
	if !cb.Allow() {
		t.Fatal("expected Allow() to be true after cooldown")
	}
	if cb.State() != StateHalfOpen {
		t.Fatalf("expected StateHalfOpen, got %v", cb.State())
	}

	// Allow should permit up to halfOpenMax concurrent probes
	// In-flight probes: 1 already used by first Allow()
	if !cb.Allow() { // 2nd probe
		t.Fatal("expected Allow() to allow 2nd probe")
	}
	if !cb.Allow() { // 3rd probe
		t.Fatal("expected Allow() to allow 3rd probe")
	}
	// 4th probe should be rejected because halfOpenMax = 3
	if cb.Allow() {
		t.Fatal("expected Allow() to reject 4th concurrent probe in HalfOpen")
	}

	// Record 3 successes
	cb.RecordSuccess()
	cb.RecordSuccess()
	cb.RecordSuccess()

	// Should be recovered to StateClosed
	if cb.State() != StateClosed {
		t.Fatalf("expected StateClosed after 3 successes, got %v", cb.State())
	}
}

func TestBreaker_HalfOpenFailureTripsImmediately(t *testing.T) {
	cb := NewBreaker(2, 20*time.Millisecond)
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != StateOpen {
		t.Fatalf("expected StateOpen, got %v", cb.State())
	}

	time.Sleep(30 * time.Millisecond)
	if !cb.Allow() {
		t.Fatal("expected Allow() to be true")
	}
	if cb.State() != StateHalfOpen {
		t.Fatalf("expected StateHalfOpen, got %v", cb.State())
	}

	// Any failure in HalfOpen immediately trips back to StateOpen
	cb.RecordFailure()
	if cb.State() != StateOpen {
		t.Fatalf("expected StateOpen after failure in HalfOpen, got %v", cb.State())
	}
	if cb.Allow() {
		t.Fatal("expected Allow() to be false in StateOpen")
	}
}

func TestBreaker_ConcurrentProbeBounds(t *testing.T) {
	cb := NewBreaker(1, 10*time.Millisecond)
	cb.RecordFailure()
	time.Sleep(15 * time.Millisecond)

	var allowedCount int
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Launch 50 concurrent requests trying to pass in HalfOpen
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if cb.Allow() {
				mu.Lock()
				allowedCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if allowedCount > 3 {
		t.Fatalf("allowedCount %d exceeded halfOpenMax (3)", allowedCount)
	}
	if allowedCount == 0 {
		t.Fatal("expected at least 1 probe to be allowed")
	}
}
