package circuitbreaker

import (
	"sync"
	"testing"
	"time"
)

func TestBreaker_StateTransitions(t *testing.T) {
	cb := New(Options{Threshold: 3, Cooldown: 50 * time.Millisecond, HalfOpenMax: 3})

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
	cb := New(Options{Threshold: 2, Cooldown: 20 * time.Millisecond, HalfOpenMax: 3})
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
	cb := New(Options{Threshold: 1, Cooldown: 10 * time.Millisecond, HalfOpenMax: 3})
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

func TestBreaker_CustomHalfOpenMax(t *testing.T) {
	// Test with HalfOpenMax = 1
	cb1 := New(Options{
		Threshold:   1,
		Cooldown:    20 * time.Millisecond,
		HalfOpenMax: 1,
	})
	if cb1.HalfOpenMax() != 1 {
		t.Fatalf("expected HalfOpenMax=1, got %d", cb1.HalfOpenMax())
	}
	cb1.RecordFailure() // Trips to Open
	time.Sleep(30 * time.Millisecond)

	if !cb1.Allow() { // 1st probe in HalfOpen
		t.Fatal("expected 1st probe to be allowed")
	}
	if cb1.Allow() { // 2nd probe should be rejected since HalfOpenMax = 1
		t.Fatal("expected 2nd probe to be rejected with HalfOpenMax=1")
	}
	cb1.RecordSuccess()
	if cb1.State() != StateClosed {
		t.Fatalf("expected StateClosed after 1 success, got %v", cb1.State())
	}

	// Test with HalfOpenMax = 5
	cb5 := New(Options{
		Threshold:   2,
		Cooldown:    20 * time.Millisecond,
		HalfOpenMax: 5,
	})
	cb5.RecordFailure()
	cb5.RecordFailure() // Trips to Open
	time.Sleep(30 * time.Millisecond)

	for i := 0; i < 5; i++ {
		if !cb5.Allow() {
			t.Fatalf("expected probe %d to be allowed", i+1)
		}
	}
	if cb5.Allow() {
		t.Fatal("expected 6th probe to be rejected with HalfOpenMax=5")
	}
	for i := 0; i < 4; i++ {
		cb5.RecordSuccess()
		if cb5.State() != StateHalfOpen {
			t.Fatalf("expected still HalfOpen after %d successes", i+1)
		}
	}
	cb5.RecordSuccess() // 5th success recovers
	if cb5.State() != StateClosed {
		t.Fatalf("expected StateClosed after 5 successes, got %v", cb5.State())
	}
}

func TestBreaker_NoStarvationWhenRecordFailureInStateOpen(t *testing.T) {
	// Cooldown is 50ms.
	cb := New(Options{Threshold: 1, Cooldown: 50 * time.Millisecond, HalfOpenMax: 3})
	cb.RecordFailure() // Trips to StateOpen at t=0
	if cb.State() != StateOpen {
		t.Fatalf("expected StateOpen, got %v", cb.State())
	}
	initialOpenedAt := cb.OpenedAt()

	// Simulate late in-flight responses arriving after 20ms
	time.Sleep(20 * time.Millisecond)
	for i := 0; i < 10; i++ {
		cb.RecordFailure()
	}

	// openedAt must NOT have been updated by failures in StateOpen!
	if !cb.OpenedAt().Equal(initialOpenedAt) {
		t.Fatalf("openedAt was mutated during StateOpen! got %v, want %v", cb.OpenedAt(), initialOpenedAt)
	}

	// Wait another 35ms (total elapsed time 55ms since trip, which is > 50ms cooldown)
	time.Sleep(35 * time.Millisecond)

	// If the starvation bug existed, the 10 RecordFailure() calls at t=20ms would have reset openedAt to 20ms,
	// and at t=55ms, elapsed time from the mutated openedAt would only be 35ms < 50ms, causing Allow() to fail.
	// With the fix, Allow() must succeed and transition to HalfOpen!
	if !cb.Allow() {
		t.Fatal("expected Allow() to succeed after cooldown elapsed from original trip time (starvation prevented)")
	}
	if cb.State() != StateHalfOpen {
		t.Fatalf("expected StateHalfOpen, got %v", cb.State())
	}
}

func TestBreaker_StateClosedFailureDoesNotPolluteOpenedAt(t *testing.T) {
	cb := New(Options{Threshold: 3, Cooldown: 50 * time.Millisecond, HalfOpenMax: 3})
	cb.RecordFailure() // 1 failure (< 3 threshold)
	if !cb.OpenedAt().IsZero() {
		t.Fatalf("expected openedAt to be zero in StateClosed, got %v", cb.OpenedAt())
	}
	if cb.Failures() != 1 {
		t.Fatalf("expected failures=1, got %d", cb.Failures())
	}
	cb.RecordSuccess()
	if cb.Failures() != 0 {
		t.Fatalf("expected failures reset to 0 after success, got %d", cb.Failures())
	}
}

func TestBreaker_AdaptiveBackoff(t *testing.T) {
	// Base cooldown: 25ms, backoff factor 2.0, max cooldown 100ms
	cb := New(Options{
		Threshold:     1,
		Cooldown:      25 * time.Millisecond,
		HalfOpenMax:   1,
		BackoffFactor: 2.0,
		MaxCooldown:   100 * time.Millisecond,
	})

	if cb.Cooldown() != 25*time.Millisecond {
		t.Fatalf("expected initial cooldown 25ms, got %v", cb.Cooldown())
	}

	// 1st trip: Closed -> Open (cooldown = 25ms)
	cb.RecordFailure()
	time.Sleep(30 * time.Millisecond)

	if !cb.Allow() {
		t.Fatal("expected Allow() to succeed after 1st cooldown")
	}
	if cb.State() != StateHalfOpen {
		t.Fatalf("expected HalfOpen, got %v", cb.State())
	}

	// Probe fails -> trips to Open, cooldown backs off: 25ms * 2 = 50ms
	cb.RecordFailure()
	if cb.State() != StateOpen {
		t.Fatalf("expected StateOpen, got %v", cb.State())
	}
	if cb.Cooldown() != 50*time.Millisecond {
		t.Fatalf("expected backed-off cooldown 50ms, got %v", cb.Cooldown())
	}

	// At 30ms (< 50ms), Allow() must be rejected
	time.Sleep(30 * time.Millisecond)
	if cb.Allow() {
		t.Fatal("expected Allow() to be false before 50ms backoff cooldown expires")
	}

	// Wait remaining 25ms (total > 50ms) -> Allow() should now succeed
	time.Sleep(25 * time.Millisecond)
	if !cb.Allow() {
		t.Fatal("expected Allow() to succeed after 50ms backoff cooldown")
	}

	// 2nd probe fails -> trips to Open, cooldown backs off: 50ms * 2 = 100ms
	cb.RecordFailure()
	if cb.Cooldown() != 100*time.Millisecond {
		t.Fatalf("expected backed-off cooldown 100ms, got %v", cb.Cooldown())
	}

	// 3rd probe fails -> max cooldown cap 100ms enforced
	time.Sleep(105 * time.Millisecond)
	if !cb.Allow() {
		t.Fatal("expected Allow() to succeed after 100ms")
	}
	cb.RecordFailure()
	if cb.Cooldown() != 100*time.Millisecond {
		t.Fatalf("expected cooldown capped at maxCooldown 100ms, got %v", cb.Cooldown())
	}

	// Finally, probe succeeds and recovers to Closed -> cooldown resets to base 25ms
	time.Sleep(105 * time.Millisecond)
	if !cb.Allow() {
		t.Fatal("expected Allow() to succeed")
	}
	cb.RecordSuccess()
	if cb.State() != StateClosed {
		t.Fatalf("expected StateClosed, got %v", cb.State())
	}
	if cb.Cooldown() != 25*time.Millisecond {
		t.Fatalf("expected cooldown reset to base 25ms, got %v", cb.Cooldown())
	}
}

func TestBreaker_Reset(t *testing.T) {
	cb := New(Options{Threshold: 1, Cooldown: 100 * time.Millisecond, HalfOpenMax: 3})
	cb.RecordFailure()
	if cb.State() != StateOpen {
		t.Fatalf("expected StateOpen, got %v", cb.State())
	}

	cb.Reset()
	if cb.State() != StateClosed {
		t.Fatalf("expected StateClosed after Reset, got %v", cb.State())
	}
	if !cb.Allow() {
		t.Fatal("expected Allow() to be true after Reset")
	}
	if cb.Failures() != 0 {
		t.Fatalf("expected failures=0 after Reset, got %d", cb.Failures())
	}
}
