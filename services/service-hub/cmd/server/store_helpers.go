// Command server is the entry point for the service-hub module.
// 本文件汇总 service-hub 任务持久化存储初始化、崩溃恢复、失败任务自动重试与
// 数据保留清理等存储/重试辅助函数（由 main.go 装配流程调用）。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/metrics"
	"github.com/fengzhizi319/PrivShield-go/pkg/store"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/memory"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/postgres"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/sqlite"

	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/config"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/retry"
)

// initTaskStore initializes either an in-memory task store or a persistent SQLite database.
// initTaskStore 根据配置的 dbPath 初始化任务存储介质：
// - dbPath 为空：使用轻量内存存储（memory.NewTaskStore()）；
// - dbPath 非空：打开并初始化 SQLite 数据库连接（sqlite.NewTaskStore(db)）。
func initTaskStore(dbPath string, logger *slog.Logger) (store.TaskStore, error) {
	if dbPath == "" {
		logger.Info("using in-memory task store (no persistence)")
		return memory.NewTaskStore(), nil
	}

	db, err := sqlite.Open(dbPath, logger)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	ts, err := sqlite.NewTaskStore(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create task store: %w", err)
	}

	logger.Info("sqlite task store initialized", "path", dbPath)
	return ts, nil
}

// initLeasedTaskStore initializes the task store with PostgreSQL lease support (Phase B).
// initLeasedTaskStore 初始化带 PostgreSQL 租约支持的任务存储（Phase B）。
//
// 优先级：
//  1. PG_DSN 非空 → PostgreSQL LeasedTaskStore（支持多副本 Hub）
//  2. DBPath 非空 → SQLite TaskStore（租约方法返回 ErrLeaseNotSupported）
//  3. 均为空 → 内存 TaskStore（租约方法返回 ErrLeaseNotSupported）
//
// P0-4 禁静音降级：PG_DSN 已配置而探测失败时，StrictStorage（默认 true）直接上抛错误，
// 由 main() log.Fatalf 终止进程——回退到 2/3 档会让多副本 Hub 无声丢失租约语义。
// 仅当显式 SERVICE_HUB_STRICT_STORAGE=false 时才允许回退（保留原 Warn 路径）。
func initLeasedTaskStore(cfg *config.Config, logger *slog.Logger) (store.LeasedTaskStore, error) {
	if cfg.PGDSN != "" {
		logger.Info("probing PostgreSQL leased task store (Phase B multi-replica Hub)")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		pgStore, err := postgres.NewStore(
			ctx,
			postgres.Config{
				DSN:     cfg.PGDSN,
				MaxConn: int32(cfg.PGMaxConn),
				MinConn: int32(cfg.PGMinConn),
			},
			logger,
		)
		if err == nil {
			logger.Info("postgresql leased task store initialized",
				"max_conns", cfg.PGMaxConn,
				"min_conns", cfg.PGMinConn,
				"lease_ttl", cfg.LeaseTTL,
			)
			return pgStore, nil
		}
		if cfg.StrictStorage {
			return nil, fmt.Errorf("strict storage mode (SERVICE_HUB_STRICT_STORAGE=true): PostgreSQL leased task store probe failed, refusing to fall back to a store without lease semantics: %w", err)
		}
		logger.Warn("PostgreSQL connection probe failed, falling back to SQLite / in-memory store", "error", err.Error())
	}

	// Fallback to SQLite / memory (lease operations return ErrLeaseNotSupported)
	// 回退到 SQLite / 内存（租约操作返回 ErrLeaseNotSupported）
	ts, err := initTaskStore(cfg.DBPath, logger)
	if err != nil {
		return nil, err
	}

	// Both sqlite.TaskStore and memory.TaskStore implement LeasedTaskStore
	// (with lease methods returning ErrLeaseNotSupported).
	// sqlite.TaskStore 和 memory.TaskStore 均实现 LeasedTaskStore 接口
	// （租约方法返回 ErrLeaseNotSupported）。
	leased, ok := ts.(store.LeasedTaskStore)
	if !ok {
		return nil, fmt.Errorf("internal: task store does not implement LeasedTaskStore")
	}

	logger.Warn("using non-PostgreSQL store: lease operations will return ErrLeaseNotSupported. " +
		"Set SERVICE_HUB_PG_DSN for multi-replica Hub support.")
	return leased, nil
}

// redactDSN returns a safe-for-logging version of the PostgreSQL DSN.
// redactDSN 返回可安全记录日志的 PostgreSQL DSN（隐藏密码）。
func redactDSN(dsn string) string {
	if dsn == "" {
		return "(not configured)"
	}
	// Simple redaction: show only first 20 chars / 简单脱敏：仅显示前 20 个字符
	if len(dsn) > 20 {
		return dsn[:20] + "...[REDACTED]"
	}
	return "[SET]"
}

// recoverOrphanedTasks scans for tasks stuck in "running" or "pending" state
// after a crash/restart and handles them appropriately:
// - pending tasks: kept in queue (not yet executed, safe to requeue);
// - running tasks: marked as failed (may have partially executed).
// 崩溃恢复：区分处理 running 和 pending 状态的孤立任务。
//
// 当服务突然崩溃（kill -9、OOM Kill、断电）时，优雅停机代码不会执行，
// 导致 running/pending 状态的任务永远卡在数据库中。此函数在启动时自动恢复这些孤立任务。
//
// 改进点（#1）：pending 任务直接保留在队列中（它们尚未执行，无需标记失败）；
// running 任务标记为 failed（可能已部分执行，需要重新提交）。
func recoverOrphanedTasks(taskStore store.TaskStore, mc *metrics.Collector, logger *slog.Logger) error {
	// 1. 扫描所有 "running" 状态的任务 → 标记为 failed（可能已部分执行）
	runningTasks, _, err := taskStore.List(store.TaskFilter{Status: "running", Limit: 10000})
	if err != nil {
		return fmt.Errorf("list running tasks: %w", err)
	}

	for i := range runningTasks {
		runningTasks[i].Status = "failed"
		runningTasks[i].Error = "server crashed or restarted (recovered on startup)"
		runningTasks[i].ErrorClass = retry.ClassRecovered
		now := time.Now()
		runningTasks[i].CompletedAt = &now
		runningTasks[i].DurationMs = now.Sub(runningTasks[i].CreatedAt).Milliseconds()
		if err := taskStore.Update(&runningTasks[i]); err != nil {
			return fmt.Errorf("mark running task %s as failed: %w", runningTasks[i].ID, err)
		}
		if mc != nil {
			mc.RecordOrphanedRecovery("running")
		}
	}

	// 2. 扫描所有 "pending" 状态的任务 → 直接保留在队列中（尚未执行，无需标记失败）
	pendingTasks, _, err := taskStore.List(store.TaskFilter{Status: "pending", Limit: 10000})
	if err != nil {
		return fmt.Errorf("list pending tasks: %w", err)
	}

	// pending 任务无需修改状态，仅记录指标
	for range pendingTasks {
		if mc != nil {
			mc.RecordOrphanedRecovery("pending")
		}
	}

	// 3. 记录恢复日志
	if len(runningTasks) > 0 || len(pendingTasks) > 0 {
		logger.Warn("recovered orphaned tasks after crash/restart",
			"running_marked_failed", len(runningTasks),
			"pending_kept_in_queue", len(pendingTasks),
			"total_recovered", len(runningTasks)+len(pendingTasks))
	} else {
		logger.Info("no orphaned tasks found, all tasks are in terminal state")
	}
	return nil
}

// maxRetryCount is the maximum number of retry attempts for a failed task.
const maxRetryCount = 3

// retryFailedTasks automatically retries failed tasks whose persisted failure class
// is marked retryable.
// 自动重试机制：扫描所有因瞬时故障而失败的任务，重新提交执行。
//
// 改进点（#3）：使用结构化 RetryCount 字段替代脆弱的 strings.Count；
// 改进点（#10）：重试采用指数退避延迟（5s → 10s → 20s），避免下游仍不可用时立即再次失败；
// 改进点（P2-7）：是否重试改判为读取失败点落库的 error_class 枚举，
// 不再对 task.Error 这段自由文案做子串匹配（文案改写即会静默丧失重试能力）。
func retryFailedTasks(taskStore store.TaskStore, mc *metrics.Collector, logger *slog.Logger) {
	// 扫描所有 "failed" 状态的任务
	failedTasks, _, err := taskStore.List(store.TaskFilter{Status: "failed", Limit: 100})
	if err != nil {
		logger.Error("failed to list failed tasks for retry", "error", err.Error())
		return
	}

	retryCount := 0
	for i := range failedTasks {
		// 只重试失败点已判定为瞬时的分类（如 timeout / downstream / shutdown / recovered）
		if !retry.IsRetryableClass(failedTasks[i].ErrorClass) {
			continue
		}

		// 使用结构化 RetryCount 字段检查重试次数（替代脆弱的 strings.Count）
		if failedTasks[i].RetryCount >= maxRetryCount {
			logger.Warn("task exceeded max retry attempts, skipping",
				"task_id", failedTasks[i].ID,
				"retry_count", failedTasks[i].RetryCount,
				"max_retry", maxRetryCount)
			if mc != nil {
				mc.RecordTaskRetry("exhausted")
			}
			continue
		}

		// 检查退避延迟（#10）：如果 RetryAfter 尚未到期，跳过
		if failedTasks[i].RetryAfter != nil && time.Now().Before(*failedTasks[i].RetryAfter) {
			continue
		}

		// 计算指数退避延迟：5s * 2^(retryCount)
		newRetryCount := failedTasks[i].RetryCount + 1
		backoffDuration := 5 * time.Second * time.Duration(1<<uint(failedTasks[i].RetryCount))
		retryAfter := time.Now().Add(backoffDuration)

		// 重置任务状态为 pending
		failedTasks[i].Status = "pending"
		failedTasks[i].Stage = "queued"
		failedTasks[i].Error = fmt.Sprintf("retrying (attempt %d/%d)", newRetryCount, maxRetryCount)
		failedTasks[i].ErrorClass = ""
		failedTasks[i].StartedAt = nil
		failedTasks[i].CompletedAt = nil
		failedTasks[i].DurationMs = 0
		failedTasks[i].RetryCount = newRetryCount
		failedTasks[i].RetryAfter = &retryAfter

		if err := taskStore.Update(&failedTasks[i]); err != nil {
			logger.Error("failed to reset task for retry", "task_id", failedTasks[i].ID, "error", err.Error())
			continue
		}

		retryCount++
		if mc != nil {
			mc.RecordTaskRetry("queued")
		}
		logger.Info("task queued for retry",
			"task_id", failedTasks[i].ID,
			"attempt", newRetryCount,
			"backoff_seconds", backoffDuration.Seconds())
	}

	if retryCount > 0 {
		logger.Info("queued tasks for retry", "count", retryCount)
	} else {
		logger.Debug("no retryable failed tasks found")
	}
}

// periodicRetryLoop runs retryFailedTasks periodically until the context is cancelled.
// 周期性后台重试循环：每隔 interval 扫描一次 failed 任务并自动重试。
// 解决“运行时失败的任务必须等到下次服务重启才能重试”的问题（#2）。
func periodicRetryLoop(ctx context.Context, taskStore store.TaskStore, mc *metrics.Collector, logger *slog.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	logger.Info("periodic background retry started", "interval_seconds", interval.Seconds())

	for {
		select {
		case <-ctx.Done():
			logger.Info("periodic background retry stopped")
			return
		case <-ticker.C:
			retryFailedTasks(taskStore, mc, logger)
		}
	}
}

// dataRetentionLoop periodically deletes terminal tasks older than retentionDays.
// dataRetentionLoop 周期性删除超过保留期的终态任务，防止 SQLite 无限膨胀。
//
// 每 6 小时执行一次清理，仅删除 completed/failed 状态的任务，
// 保留 pending/running 状态的任务不受影响。
func dataRetentionLoop(ctx context.Context, taskStore store.TaskStore, logger *slog.Logger, retentionDays int) {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	logger.Info("data retention cleanup started", "retention_days", retentionDays, "interval_hours", 6)

	// Run once immediately on startup / 启动时立即执行一次
	runRetentionCleanup(taskStore, logger, retentionDays)

	for {
		select {
		case <-ctx.Done():
			logger.Info("data retention cleanup stopped")
			return
		case <-ticker.C:
			runRetentionCleanup(taskStore, logger, retentionDays)
		}
	}
}

// runRetentionCleanup performs a single cleanup pass.
func runRetentionCleanup(taskStore store.TaskStore, logger *slog.Logger, retentionDays int) {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	deleted, err := taskStore.CleanupOld(cutoff)
	if err != nil {
		logger.Error("data retention cleanup failed", "error", err.Error())
		return
	}
	if deleted > 0 {
		logger.Info("data retention cleanup completed",
			"deleted_tasks", deleted,
			"cutoff", cutoff.Format(time.RFC3339),
			"retention_days", retentionDays)
	}
}
