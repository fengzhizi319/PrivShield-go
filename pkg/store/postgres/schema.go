// Package postgres manages PostgreSQL schema definitions and migrations for tasks.
// Package postgres 管理 PostgreSQL 任务与租约表结构定义及平滑迁移。
//
// ==============================================================================
// 【表结构与索引设计】
// 1. 【tasks 表】：
//    支持 Phase B 流水线任务生命周期、重试退避调度与多副本独占租约；
// 2. 【部分索引 (Partial Index)】：
//    idx_tasks_claim: `ON tasks (priority DESC, created_at ASC) WHERE status = 'pending'`
//    专为 ClaimNext 打造，仅索引待调度任务，极大降低并发抢占时的 B-Tree 查找深度与写开销；
// 3. 【Canonical 标识迁移与回填】：
//    自动补全 api_code、datasource_id 与 trace_id，并为历史存量数据执行回填。
// ==============================================================================

package postgres

import "context"

// initSchema creates the tasks table and indexes if they don't exist.
//
// initSchema 在不存在时创建 tasks 表及索引，并自动执行存量迁移。
//
// 执行逻辑：
// 1. CREATE TABLE IF NOT EXISTS tasks：创建完整的任务与租约数据表；
// 2. ALTER TABLE ADD COLUMN IF NOT EXISTS：幂等补充可能缺失的字段；
// 3. UPDATE tasks 回填历史存量记录的 datasource_id 与 api_code；
// 4. CREATE INDEX IF NOT EXISTS：创建状态、重试时间、租约过期时间及部分索引 idx_tasks_claim。
func (s *Store) initSchema(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS tasks (
			id              TEXT PRIMARY KEY,
			status          TEXT NOT NULL DEFAULT 'pending',
			stage           TEXT NOT NULL DEFAULT 'queued',
			source          TEXT,
			api_code        TEXT DEFAULT '',
			datasource_id   TEXT DEFAULT '',
			operation       TEXT,
			priority        INTEGER DEFAULT 0,
			created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			started_at      TIMESTAMPTZ,
			completed_at    TIMESTAMPTZ,
			duration_ms     BIGINT DEFAULT 0,
			error           TEXT,
			error_class     TEXT DEFAULT '',
			payload_json    TEXT,
			retry_count     INTEGER DEFAULT 0,
			retry_after     TIMESTAMPTZ,
			trace_id        TEXT DEFAULT '',
			lease_owner     TEXT DEFAULT '',
			lease_token     TEXT DEFAULT '',
			lease_expires_at TIMESTAMPTZ,
			version         INTEGER DEFAULT 0,
			max_retries     INTEGER DEFAULT 3
		);

		-- Migration for existing databases / 为已有数据库执行平滑升级
		ALTER TABLE tasks ADD COLUMN IF NOT EXISTS api_code TEXT DEFAULT '';
		ALTER TABLE tasks ADD COLUMN IF NOT EXISTS datasource_id TEXT DEFAULT '';
		ALTER TABLE tasks ADD COLUMN IF NOT EXISTS trace_id TEXT DEFAULT '';
		ALTER TABLE tasks ADD COLUMN IF NOT EXISTS error_class TEXT DEFAULT '';

		-- Backfill canonical identifiers / 回填规范化标识
		UPDATE tasks SET datasource_id = source
		WHERE (datasource_id IS NULL OR datasource_id = '') AND SUBSTRING(source, 1, 3) = 'ds_';
		UPDATE tasks SET api_code = 'api1_yibao'
		WHERE datasource_id = 'ds_yibao' AND (api_code IS NULL OR api_code = '');
		UPDATE tasks SET api_code = 'api2_kangyang'
		WHERE datasource_id = 'ds_kangyang' AND (api_code IS NULL OR api_code = '');

		CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks (status);
		CREATE INDEX IF NOT EXISTS idx_tasks_created ON tasks (created_at);
		CREATE INDEX IF NOT EXISTS idx_tasks_retry_after ON tasks (retry_after);
		CREATE INDEX IF NOT EXISTS idx_tasks_lease_expires ON tasks (lease_expires_at);
		CREATE INDEX IF NOT EXISTS idx_tasks_datasource_id ON tasks (datasource_id);

		-- Partial index for ClaimNext: only pending tasks, ordered by priority DESC, created_at ASC.
		-- 部分索引用于 ClaimNext：仅针对 pending 状态的任务建立优先级与创建时间复合索引。
		CREATE INDEX IF NOT EXISTS idx_tasks_claim
			ON tasks (priority DESC, created_at ASC)
			WHERE status = 'pending';
	`)
	return err
}
