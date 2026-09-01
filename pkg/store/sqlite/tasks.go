// Package sqlite provides SQLite-backed TaskStore implementation.
// Package sqlite 提供基于 SQLite 的任务流水线存储接口实现。
//
// ==============================================================================
// 【核心特性与优化】
// 1. 【Save / INSERT OR REPLACE】：通过 SQLite 原生语法实现幂等保存；
// 2. 【List / SQL 级分页】：支持带 status 过滤的 SQL 级 LIMIT / OFFSET 分页；
// 3. 【Counts / GROUP BY 聚合】：在 SQLite 引擎层执行状态聚合；
// 4. 【scanTaskFields 抽象复用 (P45 fix)】：复用 Row 与 Rows 扫描逻辑，减少重复代码；
// 5. 【CleanupOld】：支持定时清理终态过期任务。
// ==============================================================================

package sqlite

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/fengzhizi319/PrivShield/pkg/store"
)

// TaskStore implements store.TaskStore backed by SQLite.
// TaskStore 基于 SQLite 实现 store.TaskStore 接口。
type TaskStore struct {
	db *sql.DB
}

// NewTaskStore creates a new SQLite-backed task store.
//
// NewTaskStore 创建并初始化 SQLite 任务存储实例。
func NewTaskStore(db *sql.DB) (*TaskStore, error) {
	if err := InitTaskTables(db); err != nil {
		return nil, fmt.Errorf("init task tables: %w", err)
	}
	return &TaskStore{db: db}, nil
}

// Save inserts or replaces a task in SQLite.
func (s *TaskStore) Save(task *store.Task) error {
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO tasks (id, status, stage, source, api_code, datasource_id, operation, priority, created_at, payload_json, retry_count, retry_after, trace_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, task.ID, task.Status, task.Stage, task.Source, task.APICode, task.DatasourceID, task.Operation, task.Priority,
		task.CreatedAt.Format(time.RFC3339Nano), task.PayloadJSON, task.RetryCount, nullTime(task.RetryAfter), task.TraceID)
	return err
}

// Get retrieves a task by ID.
func (s *TaskStore) Get(id string) (*store.Task, error) {
	row := s.db.QueryRow(`
		SELECT id, status, stage, source, api_code, datasource_id, operation, priority, created_at, started_at, completed_at, duration_ms, error, error_class, retry_count, retry_after, trace_id
		FROM tasks WHERE id = ?
	`, id)
	return scanTask(row)
}

// List returns filtered and paginated tasks.
func (s *TaskStore) List(filter store.TaskFilter) ([]store.Task, int, error) {
	// 统计总数
	countQuery := "SELECT COUNT(*) FROM tasks"
	var total int
	var args []any
	if filter.Status != "" {
		countQuery += " WHERE status = ?"
		args = append(args, filter.Status)
	}
	if err := s.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// 分页查询行
	query := "SELECT id, status, stage, source, api_code, datasource_id, operation, priority, created_at, started_at, completed_at, duration_ms, error, error_class, retry_count, retry_after, trace_id FROM tasks"
	if filter.Status != "" {
		query += " WHERE status = ?"
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

	var listArgs []any
	if filter.Status != "" {
		listArgs = append(listArgs, filter.Status)
	}

	rows, err := s.db.Query(query, listArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	tasks := make([]store.Task, 0)
	for rows.Next() {
		t, err := scanTaskFields(rows.Scan)
		if err != nil {
			return nil, 0, err
		}
		tasks = append(tasks, *t)
	}
	return tasks, total, rows.Err()
}

// Update modifies an existing task's fields in SQLite.
func (s *TaskStore) Update(task *store.Task) error {
	_, err := s.db.Exec(`
		UPDATE tasks SET status=?, stage=?, api_code=?, datasource_id=?, started_at=?, completed_at=?, duration_ms=?, error=?, error_class=?, retry_count=?, retry_after=?, trace_id=?
		WHERE id=?
	`, task.Status, task.Stage, task.APICode, task.DatasourceID, nullTime(task.StartedAt), nullTime(task.CompletedAt),
		task.DurationMs, task.Error, task.ErrorClass, task.RetryCount, nullTime(task.RetryAfter), task.TraceID, task.ID)
	return err
}

// Counts computes task counts aggregated by status in SQLite.
func (s *TaskStore) Counts() (store.TaskCounts, error) {
	var c store.TaskCounts
	rows, err := s.db.Query("SELECT status, COUNT(*) FROM tasks GROUP BY status")
	if err != nil {
		return c, err
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

// scanTaskFields scans common task fields from any scanner interface.
// P45 fix: 抽象通用扫描逻辑，消除 scanTask 与 scanTaskRow 的重复代码。
func scanTaskFields(scan func(dest ...any) error) (*store.Task, error) {
	var t store.Task
	var createdAt string
	var startedAt, completedAt, errMsg, errClass, retryAfter sql.NullString

	err := scan(&t.ID, &t.Status, &t.Stage, &t.Source, &t.APICode, &t.DatasourceID, &t.Operation, &t.Priority,
		&createdAt, &startedAt, &completedAt, &t.DurationMs, &errMsg, &errClass,
		&t.RetryCount, &retryAfter, &t.TraceID)
	if err != nil {
		return nil, err
	}

	t.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if startedAt.Valid {
		if ts, err := time.Parse(time.RFC3339Nano, startedAt.String); err == nil {
			t.StartedAt = &ts
		}
	}
	if completedAt.Valid {
		if ts, err := time.Parse(time.RFC3339Nano, completedAt.String); err == nil {
			t.CompletedAt = &ts
		}
	}
	t.Error = errMsg.String
	t.ErrorClass = errClass.String
	if retryAfter.Valid {
		if ts, err := time.Parse(time.RFC3339Nano, retryAfter.String); err == nil {
			t.RetryAfter = &ts
		}
	}
	return &t, nil
}

func scanTask(row *sql.Row) (*store.Task, error) {
	return scanTaskFields(row.Scan)
}

// CleanupOld deletes terminal (completed/failed) tasks older than the cutoff time.
func (s *TaskStore) CleanupOld(before time.Time) (int64, error) {
	cutoff := before.Format(time.RFC3339Nano)
	result, err := s.db.Exec(
		`DELETE FROM tasks WHERE status IN ('completed', 'failed') AND created_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// nullTime converts a *time.Time to sql.NullString for storage.
func nullTime(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: t.Format(time.RFC3339Nano), Valid: true}
}
