package main

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/store"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/memory"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/retry"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// seedFailed 写入一条终态 failed 任务。
func seedFailed(t *testing.T, s store.TaskStore, id, errText, class string, attempts int) {
	t.Helper()
	task := &store.Task{
		ID:         id,
		Status:     "failed",
		Stage:      "classify",
		Operation:  "mask",
		CreatedAt:  time.Now().Add(-time.Hour),
		Error:      errText,
		ErrorClass: class,
		RetryCount: attempts,
		MaxRetries: maxRetryCount,
	}
	if err := s.Save(task); err != nil {
		t.Fatalf("seed task %s: %v", id, err)
	}
}

// TestRetryFailedTasksDecidesFromPersistedClass 验证 P2-7 的口径迁移：
// 是否重投只看 error_class，不再看 Error 文案。
func TestRetryFailedTasksDecidesFromPersistedClass(t *testing.T) {
	s := memory.NewTaskStore()
	seedFailed(t, s, "task-retryable", "engine call blew up in a totally new way", retry.ClassTimeout, 0)
	seedFailed(t, s, "task-contract", "classification failed: engine returned no security level", retry.ClassContract, 0)

	retryFailedTasks(s, nil, discardLogger())

	got, err := s.Get("task-retryable")
	if err != nil {
		t.Fatalf("get retryable task: %v", err)
	}
	if got.Status != "pending" || got.Stage != "queued" || got.RetryCount != 1 {
		t.Fatalf("retryable-class task was not requeued: status=%q stage=%q retries=%d", got.Status, got.Stage, got.RetryCount)
	}
	if got.RetryAfter == nil || !got.RetryAfter.After(time.Now()) {
		t.Errorf("expected a future exponential-backoff retry_after, got %v", got.RetryAfter)
	}
	if got.ErrorClass != "" {
		t.Errorf("requeued task must drop the superseded failure class, got %q", got.ErrorClass)
	}

	got, err = s.Get("task-contract")
	if err != nil {
		t.Fatalf("get contract-failed task: %v", err)
	}
	if got.Status != "failed" {
		t.Fatalf("contract-class failure must stay terminal, got status %q", got.Status)
	}
}

// TestRetryFailedTasksIgnoresRetryableLookingText 是旧字符串匹配的直接回归用例：
// 文案里带满 "timeout" / "connection refused" 也不该换来重试资格。
func TestRetryFailedTasksIgnoresRetryableLookingText(t *testing.T) {
	s := memory.NewTaskStore()
	seedFailed(t, s, "task-text-trap",
		"timeout while dialing connection refused: temporary failure, context deadline exceeded", "", 0)

	retryFailedTasks(s, nil, discardLogger())

	got, err := s.Get("task-text-trap")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != "failed" {
		t.Fatalf("unclassified text must not buy a retry, got status %q", got.Status)
	}
}

// TestRetryFailedTasksRespectsRetryBudget 保证重投仍受重试预算约束，
// 不因分类判定而绕开退避与上限。
func TestRetryFailedTasksRespectsRetryBudget(t *testing.T) {
	s := memory.NewTaskStore()
	seedFailed(t, s, "task-exhausted", "engine unavailable", retry.ClassDownstream, maxRetryCount)
	seedFailed(t, s, "task-waiting-backoff", "engine unavailable", retry.ClassDownstream, 1)
	now := time.Now().Add(time.Minute)
	waiting, err := s.Get("task-waiting-backoff")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	waiting.RetryAfter = &now
	if err := s.Update(waiting); err != nil {
		t.Fatalf("set backoff: %v", err)
	}

	retryFailedTasks(s, nil, discardLogger())

	for _, id := range []string{"task-exhausted", "task-waiting-backoff"} {
		got, err := s.Get(id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if got.Status != "failed" {
			t.Errorf("%s must stay failed (budget exhausted or backoff pending), got %q", id, got.Status)
		}
	}
}
