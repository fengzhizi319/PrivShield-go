// Package postgres_test provides integration tests for PostgreSQL LeasedTaskStore.
// Package postgres_test 为 PostgreSQL LeasedTaskStore 提供完整的集成测试套件。
//
// ==============================================================================
// 【测试运行与前置条件】
// 本集成测试依赖真实可用的 PostgreSQL 实例，通过环境变量触发：
//
//	PRIVSHIELD_PG_TEST_DSN="postgres://user:pass@localhost:5432/privshield_hub_test" \
//	go test -tags=integration -v ./pkg/store/postgres/...
//
// 若未配置 PRIVSHIELD_PG_TEST_DSN 环境变量，测试将自动安全跳过（Skip）。
//
// 【测试场景覆盖】
// 1. ClaimNext：无 pending 任务返回 nil、正常抢占最高优先级任务、自动跳过 running 任务；
// 2. CompleteLease：合法 token 正常完成、错误 token 拒绝覆盖；
// 3. FailLease：终态失败流转、可重试失败回退为 pending 与 retry_count 累加；
// 4. RenewLease：未超期租约正常续期；
// 5. RequeueExpiredLeases：过期租约批量回收重置为 pending。
// ==============================================================================

package postgres_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/store"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/postgres"
)

func getTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("PRIVSHIELD_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("PRIVSHIELD_PG_TEST_DSN not set, skipping PostgreSQL integration tests")
	}
	return dsn
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func setupTestStore(t *testing.T) *postgres.Store {
	t.Helper()
	dsn := getTestDSN(t)
	s, err := postgres.NewStore(context.Background(), postgres.Config{
		DSN:     dsn,
		MaxConn: 3,
		MinConn: 1,
	}, testLogger())
	if err != nil {
		t.Fatalf("failed to create postgres store: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	// 每个测试前清理任务表，保证测试用例环境隔离
	_, err = s.Pool().Exec(context.Background(), "DELETE FROM tasks")
	if err != nil {
		t.Fatalf("failed to clean tasks table: %v", err)
	}
	return s
}

// ─────────────────────────────────────────────────────────────
// 1. ClaimNext 抢占测试
// ─────────────────────────────────────────────────────────────

func TestClaimNext_NoPendingTasks(t *testing.T) {
	s := setupTestStore(t)
	lease, err := s.ClaimNext("hub-1", 60*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lease != nil {
		t.Fatal("expected nil lease when no pending tasks")
	}
}

func TestClaimNext_ClaimsPendingTask(t *testing.T) {
	s := setupTestStore(t)
	now := time.Now()

	// 插入一个待调度的 pending 任务
	err := s.Save(&store.Task{
		ID:         "task-claim-1",
		Status:     "pending",
		Stage:      "queued",
		Source:     "test",
		Operation:  "mask",
		Priority:   5,
		CreatedAt:  now,
		MaxRetries: 3,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	lease, err := s.ClaimNext("hub-1", 60*time.Second)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if lease == nil {
		t.Fatal("expected non-nil lease")
	}
	if lease.Task.ID != "task-claim-1" {
		t.Fatalf("expected task-claim-1, got %s", lease.Task.ID)
	}
	if lease.Owner != "hub-1" {
		t.Fatalf("expected owner hub-1, got %s", lease.Owner)
	}
	if lease.Task.Status != "running" {
		t.Fatalf("expected status running, got %s", lease.Task.Status)
	}
}

func TestClaimNext_SkipsRunningTasks(t *testing.T) {
	s := setupTestStore(t)
	now := time.Now()

	// 插入 running 任务（不应被重复抢占）
	s.Save(&store.Task{
		ID:         "task-running",
		Status:     "running",
		Stage:      "ingest",
		CreatedAt:  now,
		MaxRetries: 3,
	})
	// 插入 pending 任务（应被正常抢占）
	s.Save(&store.Task{
		ID:         "task-pending",
		Status:     "pending",
		Stage:      "queued",
		CreatedAt:  now,
		MaxRetries: 3,
	})

	lease, err := s.ClaimNext("hub-1", 60*time.Second)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if lease == nil || lease.Task.ID != "task-pending" {
		t.Fatalf("expected task-pending, got %v", lease)
	}
}

// ─────────────────────────────────────────────────────────────
// 2. CompleteLease 完成测试
// ─────────────────────────────────────────────────────────────

func TestCompleteLease_Success(t *testing.T) {
	s := setupTestStore(t)
	now := time.Now()
	s.Save(&store.Task{
		ID: "task-complete", Status: "pending", Stage: "queued",
		CreatedAt: now, MaxRetries: 3,
	})

	lease, _ := s.ClaimNext("hub-1", 60*time.Second)
	if lease == nil {
		t.Fatal("expected lease")
	}

	ok, err := s.CompleteLease(lease.Task.ID, lease.Owner, lease.Token, store.TaskResult{
		Stage: "done",
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}

	// 验证数据库中状态已变为 completed
	task, _ := s.Get(lease.Task.ID)
	if task.Status != "completed" {
		t.Fatalf("expected completed, got %s", task.Status)
	}
}

func TestCompleteLease_WrongToken(t *testing.T) {
	s := setupTestStore(t)
	now := time.Now()
	s.Save(&store.Task{
		ID: "task-wrong-token", Status: "pending", Stage: "queued",
		CreatedAt: now, MaxRetries: 3,
	})

	lease, _ := s.ClaimNext("hub-1", 60*time.Second)
	if lease == nil {
		t.Fatal("expected lease")
	}

	ok, err := s.CompleteLease(lease.Task.ID, lease.Owner, "wrong-token", store.TaskResult{})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for wrong token")
	}
}

// ─────────────────────────────────────────────────────────────
// 3. FailLease 失败处理测试
// ─────────────────────────────────────────────────────────────

func TestFailLease_Terminal(t *testing.T) {
	s := setupTestStore(t)
	now := time.Now()
	s.Save(&store.Task{
		ID: "task-fail", Status: "pending", Stage: "queued",
		CreatedAt: now, MaxRetries: 3,
	})

	lease, _ := s.ClaimNext("hub-1", 60*time.Second)
	if lease == nil {
		t.Fatal("expected lease")
	}

	ok, err := s.FailLease(lease.Task.ID, lease.Owner, lease.Token, store.TaskFailure{
		Error:     "permanent failure",
		Retryable: false,
	})
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}

	task, _ := s.Get(lease.Task.ID)
	if task.Status != "failed" {
		t.Fatalf("expected failed, got %s", task.Status)
	}
}

func TestFailLease_Retryable(t *testing.T) {
	s := setupTestStore(t)
	now := time.Now()
	s.Save(&store.Task{
		ID: "task-retry", Status: "pending", Stage: "queued",
		CreatedAt: now, MaxRetries: 3,
	})

	lease, _ := s.ClaimNext("hub-1", 60*time.Second)
	if lease == nil {
		t.Fatal("expected lease")
	}

	ok, err := s.FailLease(lease.Task.ID, lease.Owner, lease.Token, store.TaskFailure{
		Error:     "timeout",
		Retryable: true,
	})
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}

	// 可重试任务应回退为 pending 状态，且 retry_count 递增为 1
	task, _ := s.Get(lease.Task.ID)
	if task.Status != "pending" {
		t.Fatalf("expected pending after retryable fail, got %s", task.Status)
	}
	if task.RetryCount != 1 {
		t.Fatalf("expected retry_count=1, got %d", task.RetryCount)
	}
}

// ─────────────────────────────────────────────────────────────
// 4. RenewLease 续租测试
// ─────────────────────────────────────────────────────────────

func TestRenewLease_Success(t *testing.T) {
	s := setupTestStore(t)
	now := time.Now()
	s.Save(&store.Task{
		ID: "task-renew", Status: "pending", Stage: "queued",
		CreatedAt: now, MaxRetries: 3,
	})

	lease, _ := s.ClaimNext("hub-1", 5*time.Second)
	if lease == nil {
		t.Fatal("expected lease")
	}

	ok, err := s.RenewLease(lease.Task.ID, lease.Owner, lease.Token, 60*time.Second)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
}

// ─────────────────────────────────────────────────────────────
// 5. RequeueExpiredLeases 过期租约回收测试
// ─────────────────────────────────────────────────────────────

func TestRequeueExpiredLeases(t *testing.T) {
	s := setupTestStore(t)
	now := time.Now()
	s.Save(&store.Task{
		ID: "task-expire", Status: "pending", Stage: "queued",
		CreatedAt: now, MaxRetries: 3,
	})

	// 使用极短 TTL 领取任务
	lease, _ := s.ClaimNext("hub-1", 1*time.Millisecond)
	if lease == nil {
		t.Fatal("expected lease")
	}

	// 等待租约自然超时
	time.Sleep(10 * time.Millisecond)

	count, err := s.RequeueExpiredLeases(10)
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 requeued, got %d", count)
	}

	// 任务应成功回退为 pending 状态
	task, _ := s.Get(lease.Task.ID)
	if task.Status != "pending" {
		t.Fatalf("expected pending after expiry, got %s", task.Status)
	}
}
