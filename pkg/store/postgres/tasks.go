// Package postgres provides PostgreSQL implementations of task persistence.
// Package postgres 提供基于 PostgreSQL 的基础任务生命周期持久化实现。
//
// ==============================================================================
// 【核心功能】
// 1. 【Save】：通过 PostgreSQL 原生 ON CONFLICT (id) DO UPDATE 实现安全 UPSERT；
// 2. 【Get】：按 ID 精确查询单条任务全部字段；
// 3. 【List】：支持 SQL 级分页（LIMIT / OFFSET），并以 COUNT(*) 获取全量命中数；
// 4. 【Update】：原子更新任务状态、阶段、耗时、重试与租约字段；
// 5. 【Counts】：利用 GROUP BY status 语句在数据库引擎层直接执行聚合统计；
// 6. 【CleanupOld】：物理清理指定时间前的终态（completed/failed）任务。
// ==============================================================================

package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/store"
)

// ── Basic TaskStore implementation / 基础 TaskStore 实现 ──

// Save inserts or replaces a task using PostgreSQL UPSERT.
//
// Save 插入新任务或在发生主键冲突时全量更新该任务。
func (s *Store) Save(task *store.Task) error {
	ctx := context.Background()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO tasks (id, status, stage, source, api_code, datasource_id, operation, priority, created_at, payload_json, retry_count, retry_after, max_retries, trace_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (id) DO UPDATE SET
			status=EXCLUDED.status, stage=EXCLUDED.stage, source=EXCLUDED.source,
			api_code=EXCLUDED.api_code, datasource_id=EXCLUDED.datasource_id,
			operation=EXCLUDED.operation, priority=EXCLUDED.priority,
			payload_json=EXCLUDED.payload_json, retry_count=EXCLUDED.retry_count,
			retry_after=EXCLUDED.retry_after, max_retries=EXCLUDED.max_retries,
			trace_id=EXCLUDED.trace_id
	`, task.ID, task.Status, task.Stage, task.Source, task.APICode, task.DatasourceID, task.Operation, task.Priority,
		task.CreatedAt, task.PayloadJSON, task.RetryCount, task.RetryAfter, task.MaxRetries, task.TraceID)
	return err
}

// Get retrieves a task by ID.
//
// Get 根据 ID 获取单个任务实体的完整信息，未找到时返回错误。
func (s *Store) Get(id string) (*store.Task, error) {
	ctx := context.Background()
	row := s.pool.QueryRow(ctx, `
		SELECT id, status, stage, source, api_code, datasource_id, operation, priority, created_at, started_at,
			completed_at, duration_ms, error, error_class, retry_count, retry_after, trace_id,
			lease_owner, lease_token, lease_expires_at, version, max_retries
		FROM tasks WHERE id = $1
	`, id)
	return scanTask(row)
}

// List returns tasks matching the filter with total count.
//
// List 在数据库端根据过滤条件查询任务列表，支持 SQL 级 LIMIT 与 OFFSET 分页。
func (s *Store) List(filter store.TaskFilter) ([]store.Task, int, error) {
	ctx := context.Background()

	// 1. 统计符合条件的总记录数
	countQuery := "SELECT COUNT(*) FROM tasks"
	var total int
	var args []any
	if filter.Status != "" {
		countQuery += " WHERE status = $1"
		args = append(args, filter.Status)
	}
	if err := s.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("postgres: count tasks: %w", err)
	}

	// 2. 分页拉取任务实体数据
	query := `SELECT id, status, stage, source, api_code, datasource_id, operation, priority, created_at, started_at,
		completed_at, duration_ms, error, error_class, retry_count, retry_after, trace_id,
		lease_owner, lease_token, lease_expires_at, version, max_retries
		FROM tasks`
	var listArgs []any
	if filter.Status != "" {
		query += " WHERE status = $1"
		listArgs = append(listArgs, filter.Status)
	}
	query += " ORDER BY created_at DESC"
	if filter.Limit > 0 {
		limit := filter.Limit
		if limit > 10000 {
			limit = 10000
		}
		offset := filter.Offset
		if offset < 0 {
			offset = 0
		}
		query += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	}

	rows, err := s.pool.Query(ctx, query, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: list tasks: %w", err)
	}
	defer rows.Close()

	tasks := make([]store.Task, 0)
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, 0, err
		}
		tasks = append(tasks, *t)
	}
	return tasks, total, rows.Err()
}

// Update modifies an existing task's mutable fields.
//
// Update 更新已存在任务的可变业务字段、状态与租约信息。
func (s *Store) Update(task *store.Task) error {
	ctx := context.Background()
	_, err := s.pool.Exec(ctx, `
		UPDATE tasks SET
			status=$1, stage=$2, api_code=$3, datasource_id=$4, started_at=$5, completed_at=$6,
			duration_ms=$7, error=$8, retry_count=$9, retry_after=$10,
			trace_id=$11,
			lease_owner=$12, lease_token=$13, lease_expires_at=$14, version=$15, error_class=$16
		WHERE id=$17
	`, task.Status, task.Stage, task.APICode, task.DatasourceID, task.StartedAt, task.CompletedAt,
		task.DurationMs, task.Error, task.RetryCount, task.RetryAfter,
		task.TraceID,
		task.LeaseOwner, task.LeaseToken, task.LeaseExpiresAt, task.Version, task.ErrorClass,
		task.ID)
	return err
}

// Counts returns aggregated task counts by status.
//
// Counts 在数据库层执行 GROUP BY status 聚合统计各状态的任务总数。
func (s *Store) Counts() (store.TaskCounts, error) {
	ctx := context.Background()
	var c store.TaskCounts
	rows, err := s.pool.Query(ctx, "SELECT status, COUNT(*) FROM tasks GROUP BY status")
	if err != nil {
		return c, fmt.Errorf("postgres: count by status: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return c, err
		}
		switch status {
		case "pending":
			c.Pending = count
		case "running":
			c.Running = count
		case "completed":
			c.Completed = count
		case "failed":
			c.Failed = count
		}
	}
	return c, rows.Err()
}

// CleanupOld deletes terminal tasks older than the cutoff time.
//
// CleanupOld 物理删除早于指定时间戳的终态（completed / failed）任务。
func (s *Store) CleanupOld(before time.Time) (int64, error) {
	ctx := context.Background()
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM tasks WHERE status IN ('completed', 'failed') AND created_at < $1", before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ── Scanning helpers / 行扫描辅助函数 ──

// rowScanner is satisfied by both pgxpool.Row and pgxpool.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(row rowScanner) (*store.Task, error) {
	var t store.Task
	if err := row.Scan(
		&t.ID, &t.Status, &t.Stage, &t.Source, &t.APICode, &t.DatasourceID, &t.Operation, &t.Priority,
		&t.CreatedAt, &t.StartedAt, &t.CompletedAt, &t.DurationMs, &t.Error, &t.ErrorClass,
		&t.RetryCount, &t.RetryAfter, &t.TraceID,
		&t.LeaseOwner, &t.LeaseToken, &t.LeaseExpiresAt, &t.Version, &t.MaxRetries,
	); err != nil {
		return nil, fmt.Errorf("postgres: scan task: %w", err)
	}
	return &t, nil
}

// Compile-time interface assertion / 编译时接口断言
var _ store.LeasedTaskStore = (*Store)(nil)
