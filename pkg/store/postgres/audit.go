// Package postgres provides PostgreSQL implementations of the AuditStore interface.
// Package postgres 提供基于 PostgreSQL 的审计存证存储接口实现。
//
// ==============================================================================
// 【核心特性与架构设计】
// 1. 【audit_logs 与 snapshots 级联存证】：
//    支持主审计日志与脱敏前后的加密样本快照落盘；
// 2. 【pgx.Batch 管道微批优化】：
//    SaveLogsBatch 采用 pgx.Batch 一次性将批次中的所有日志与快照排队发送至数据库，
//    极大减少网络往返耗时（RTT）；
// 3. 【SQL 级聚合统计与报告生成】：
//    GetStats 与 GenerateReport 均在 PostgreSQL 引擎内部利用原生聚合函数完成，
//    无需将全表数据加载至应用层内存；
// 4. 【哈希链防篡改对账核验 (VerifyChain)】：
//    支持基于国密 SM3 与历史兼容算法自首至尾逐条核验证据完整性；
// 5. 【参数化查询防注入】：
//    所有动态过滤条件均通过严格的占位符（$1, $2, ...）安全拼接。
// ==============================================================================

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fengzhizi319/PrivShield/pkg/store"
)

// AuditStore implements store.AuditStore backed by PostgreSQL.
// AuditStore 基于 PostgreSQL 连接池实现不可篡改审计日志存储。
type AuditStore struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// NewAuditStore creates a new PostgreSQL-backed audit store with connection pooling.
//
// NewAuditStore 根据配置构建带自适应连接池的 PostgreSQL 审计存储实例。
func NewAuditStore(ctx context.Context, cfg Config, logger *slog.Logger) (*AuditStore, error) {
	if cfg.DSN == "" {
		return nil, fmt.Errorf("postgres: DSN must not be empty")
	}
	if logger == nil {
		logger = slog.Default()
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("postgres parse DSN: %w", err)
	}

	numCPU := effectiveNumCPU()
	if cfg.MaxConn > 0 {
		poolCfg.MaxConns = cfg.MaxConn
	} else {
		// 自适应 MaxConns: 限制在 [10, 100]
		adaptiveMax := numCPU * 4
		if adaptiveMax < 10 {
			adaptiveMax = 10
		} else if adaptiveMax > 100 {
			adaptiveMax = 100
		}
		poolCfg.MaxConns = adaptiveMax
	}

	if cfg.MinConn > 0 {
		poolCfg.MinConns = cfg.MinConn
	} else {
		// 自适应 MinConns: 限制在 [2, 20]
		adaptiveMin := numCPU
		if adaptiveMin < 2 {
			adaptiveMin = 2
		} else if adaptiveMin > 20 {
			adaptiveMin = 20
		}
		poolCfg.MinConns = adaptiveMin
	}

	// 不变式约束: minConns 不得超过 maxConns
	if poolCfg.MinConns > poolCfg.MaxConns {
		poolCfg.MinConns = poolCfg.MaxConns
	}

	poolCfg.HealthCheckPeriod = 30 * time.Second
	poolCfg.MaxConnLifetime = 30 * time.Minute
	poolCfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("postgres create pool: %w", err)
	}

	// 使用 3 秒超时探测连接连通性
	pingCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping probe failed (3s): %w", err)
	}

	s := &AuditStore{pool: pool, logger: logger}
	schemaCtx, schemaCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer schemaCancel()
	if err := s.initAuditSchema(schemaCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres init audit schema: %w", err)
	}

	logger.Info("postgresql audit store initialized", "max_conns", poolCfg.MaxConns, "min_conns", poolCfg.MinConns)
	return s, nil
}

// NewAuditStoreWithPool creates an AuditStore using an existing pool.
// NewAuditStoreWithPool 复用外部已创建好的 pgxpool.Pool 实例构建 AuditStore。
func NewAuditStoreWithPool(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) (*AuditStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("postgres pool must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	s := &AuditStore{pool: pool, logger: logger}
	if err := s.initAuditSchema(ctx); err != nil {
		return nil, fmt.Errorf("postgres init audit schema: %w", err)
	}
	return s, nil
}

// initAuditSchema 初始化 audit_logs 与 snapshots 表结构、外键与索引。
func (s *AuditStore) initAuditSchema(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS audit_logs (
			id              TEXT PRIMARY KEY,
			seq             BIGSERIAL NOT NULL UNIQUE,
			task_id         TEXT DEFAULT '',
			api_code        TEXT DEFAULT '',
			datasource_id   TEXT DEFAULT '',
			timestamp       TIMESTAMPTZ NOT NULL,
			operation       TEXT,
			datasource      TEXT,
			input_hash      TEXT,
			output_hash     TEXT,
			algorithm       TEXT,
			parameters_json TEXT,
			input_rows      INTEGER DEFAULT 0,
			output_rows     INTEGER DEFAULT 0,
			duration_ms     BIGINT DEFAULT 0,
			user_name       TEXT,
			status          TEXT,
			error_message   TEXT,
			security_level  TEXT,
			prev_hash       TEXT DEFAULT '',
			integrity_hash  TEXT DEFAULT ''
		);

		CREATE TABLE IF NOT EXISTS snapshots (
			id              TEXT PRIMARY KEY,
			audit_log_id    TEXT REFERENCES audit_logs(id) ON DELETE CASCADE,
			timestamp       TIMESTAMPTZ NOT NULL,
			input_sample    TEXT,
			output_sample   TEXT,
			algorithm       TEXT,
			parameters_json TEXT,
			integrity_hash  TEXT,
			prev_hash       TEXT DEFAULT ''
		);

		CREATE INDEX IF NOT EXISTS idx_audit_logs_timestamp ON audit_logs (timestamp DESC);
		CREATE INDEX IF NOT EXISTS idx_audit_logs_operation ON audit_logs (operation);
		CREATE INDEX IF NOT EXISTS idx_audit_logs_datasource_id ON audit_logs (datasource_id);
		CREATE INDEX IF NOT EXISTS idx_audit_logs_task_id ON audit_logs (task_id);
		CREATE INDEX IF NOT EXISTS idx_snapshots_audit_log_id ON snapshots (audit_log_id);

		ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS task_id TEXT DEFAULT '';
		ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS api_code TEXT DEFAULT '';
		ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS datasource_id TEXT DEFAULT '';
		ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS prev_hash TEXT DEFAULT '';
		ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS integrity_hash TEXT DEFAULT '';

		ALTER TABLE snapshots ADD COLUMN IF NOT EXISTS prev_hash TEXT DEFAULT '';
		ALTER TABLE snapshots ADD COLUMN IF NOT EXISTS integrity_hash TEXT DEFAULT '';
	`)
	if err != nil {
		return fmt.Errorf("init audit schema base: %w", err)
	}

	// P1 fix: add monotonic sequence column for deterministic chain verification order.
	// Existing deployments get the column added and backfilled; new deployments already have it.
	if _, err := s.pool.Exec(ctx, `ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS seq BIGINT`); err != nil {
		return fmt.Errorf("add seq column: %w", err)
	}
	// Backfill seq for pre-existing rows using stable temporal order, then set a sequence default.
	if _, err := s.pool.Exec(ctx, `
		WITH numbered AS (
			SELECT id, row_number() OVER (ORDER BY timestamp ASC, id ASC) AS rnum
			FROM audit_logs
			WHERE seq IS NULL
		)
		UPDATE audit_logs AS a
		SET seq = numbered.rnum
		FROM numbered
		WHERE a.id = numbered.id AND a.seq IS NULL
	`); err != nil {
		return fmt.Errorf("backfill seq values: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `
		CREATE SEQUENCE IF NOT EXISTS audit_logs_seq_seq;
		DO $$
		DECLARE max_seq BIGINT;
		BEGIN
			SELECT COALESCE(MAX(seq), 0) INTO max_seq FROM audit_logs;
			IF max_seq > 0 THEN
				PERFORM setval('audit_logs_seq_seq', max_seq + 1);
			END IF;
		END $$;
		ALTER TABLE audit_logs ALTER COLUMN seq SET DEFAULT nextval('audit_logs_seq_seq');
		ALTER TABLE audit_logs ALTER COLUMN seq SET NOT NULL;
	`); err != nil {
		return fmt.Errorf("configure seq default: %w", err)
	}
	return nil
}

// Close closes the underlying connection pool.
// Close 关闭底层连接池。
func (s *AuditStore) Close() error {
	if s.pool != nil {
		s.pool.Close()
	}
	return nil
}

// SaveLog persists an audit log and calculates its hash chain if missing.
//
// SaveLog 持久化单条审计日志：若 prev_hash 或 integrity_hash 为空，自动计算链式哈希并落库。
func (s *AuditStore) SaveLog(log *store.AuditLog) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if log.PrevHash == "" {
		if latest, err := s.GetLatestLog(); err == nil && latest != nil {
			log.PrevHash = latest.IntegrityHash
		}
	}
	if log.IntegrityHash == "" {
		log.IntegrityHash = store.ComputeAuditIntegrityHash(log.ID, log.PrevHash, log.Timestamp, log.Algorithm, log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON)
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_logs (id, seq, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash,
			algorithm, parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash)
		VALUES ($1, nextval('audit_logs_seq_seq'), $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
	`, log.ID, log.TaskID, log.APICode, log.DatasourceID, log.Timestamp, log.Operation, log.DataSource,
		log.InputHash, log.OutputHash, log.Algorithm, log.ParametersJSON,
		log.InputRows, log.OutputRows, log.DurationMs, log.User, log.Status, log.ErrorMessage, log.SecurityLevel,
		log.PrevHash, log.IntegrityHash)
	return err
}

// SaveLogWithSnapshot atomically stores an audit log and its snapshot inside a transaction.
//
// SaveLogWithSnapshot 在同一数据库事务内原子持久化审计日志与其关联的数据快照。
func (s *AuditStore) SaveLogWithSnapshot(log *store.AuditLog, snapshot *store.SnapshotRecord) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if log.PrevHash == "" {
		if latest, err := s.GetLatestLog(); err == nil && latest != nil {
			log.PrevHash = latest.IntegrityHash
		}
	}
	if log.IntegrityHash == "" {
		log.IntegrityHash = store.ComputeAuditIntegrityHash(log.ID, log.PrevHash, log.Timestamp, log.Algorithm, log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON)
	}
	if snapshot != nil {
		// P0 fix: snapshot prev_hash binds to the parent log's integrity hash
		// and its integrity hash covers the snapshot's own sample fields.
		if snapshot.PrevHash == "" {
			snapshot.PrevHash = log.IntegrityHash
		}
		if snapshot.IntegrityHash == "" {
			snapshot.IntegrityHash = store.ComputeSnapshotIntegrityHash(
				snapshot.ID, snapshot.AuditLogID, snapshot.PrevHash, snapshot.Timestamp, snapshot.Algorithm,
				snapshot.InputSample, snapshot.OutputSample, snapshot.ParametersJSON,
			)
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (id, seq, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash,
			algorithm, parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash)
		VALUES ($1, nextval('audit_logs_seq_seq'), $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
	`, log.ID, log.TaskID, log.APICode, log.DatasourceID, log.Timestamp, log.Operation, log.DataSource,
		log.InputHash, log.OutputHash, log.Algorithm, log.ParametersJSON,
		log.InputRows, log.OutputRows, log.DurationMs, log.User, log.Status, log.ErrorMessage, log.SecurityLevel,
		log.PrevHash, log.IntegrityHash); err != nil {
		return err
	}

	if snapshot != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO snapshots (id, audit_log_id, timestamp, input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, snapshot.ID, snapshot.AuditLogID, snapshot.Timestamp,
			snapshot.InputSample, snapshot.OutputSample, snapshot.Algorithm, snapshot.ParametersJSON, snapshot.IntegrityHash, snapshot.PrevHash); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// SaveLogsBatch saves multiple logs and snapshots in a single pipeline round-trip using pgx.Batch.
//
// SaveLogsBatch 利用 pgx.Batch 管道技术批量原子提交多条日志与快照。
func (s *AuditStore) SaveLogsBatch(logs []store.AuditLog, snapshots []store.SnapshotRecord) error {
	if len(logs) == 0 && len(snapshots) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	batch := &pgx.Batch{}
	for _, log := range logs {
		if log.IntegrityHash == "" {
			log.IntegrityHash = store.ComputeAuditIntegrityHash(log.ID, log.PrevHash, log.Timestamp, log.Algorithm, log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON)
		}
		batch.Queue(`
			INSERT INTO audit_logs (id, seq, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash,
				algorithm, parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash)
			VALUES ($1, nextval('audit_logs_seq_seq'), $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		`, log.ID, log.TaskID, log.APICode, log.DatasourceID, log.Timestamp, log.Operation, log.DataSource,
			log.InputHash, log.OutputHash, log.Algorithm, log.ParametersJSON,
			log.InputRows, log.OutputRows, log.DurationMs, log.User, log.Status, log.ErrorMessage, log.SecurityLevel,
			log.PrevHash, log.IntegrityHash)
	}

	for _, snap := range snapshots {
		batch.Queue(`
			INSERT INTO snapshots (id, audit_log_id, timestamp, input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		`, snap.ID, snap.AuditLogID, snap.Timestamp,
			snap.InputSample, snap.OutputSample, snap.Algorithm, snap.ParametersJSON, snap.IntegrityHash, snap.PrevHash)
	}

	br := tx.SendBatch(ctx, batch)
	totalQueued := len(logs) + len(snapshots)
	for i := 0; i < totalQueued; i++ {
		if _, err := br.Exec(); err != nil {
			br.Close()
			return err
		}
	}
	if err := br.Close(); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// GetLog retrieves an audit log by ID.
func (s *AuditStore) GetLog(id string) (*store.AuditLog, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	row := s.pool.QueryRow(ctx, `
		SELECT id, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash, algorithm,
			parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash
		FROM audit_logs WHERE id = $1
	`, id)
	return scanPGAuditRow(row)
}

// GetLatestLog returns the most recently written audit log from PostgreSQL.
//
// GetLatestLog 返回链尾记录：按规范化链序 `(seq DESC, timestamp DESC, id DESC)` 取最后一条，
// 与 VerifyChain 的回放序严格互逆（P2-4），保证「写入侧锚定的上一条」与「核验侧的上一条」
// 在同时间戳场景下仍是同一条记录，避免写入即误判断链。
func (s *AuditStore) GetLatestLog() (*store.AuditLog, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	row := s.pool.QueryRow(ctx, `
		SELECT id, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash, algorithm,
			parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash
		FROM audit_logs ORDER BY seq DESC, timestamp DESC, id DESC LIMIT 1
	`)
	log, err := scanPGAuditRow(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return log, err
}

// ListLogs returns filtered and paginated audit logs.
func (s *AuditStore) ListLogs(filter store.AuditFilter) ([]store.AuditLog, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	whereClause, args := buildPGAuditWhere(filter)

	countQuery := "SELECT COUNT(*) FROM audit_logs" + whereClause
	var total int
	if err := s.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}

	query := fmt.Sprintf(`
		SELECT id, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash, algorithm,
			parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash
		FROM audit_logs%s ORDER BY timestamp DESC LIMIT $%d OFFSET $%d
	`, whereClause, len(args)+1, len(args)+2)

	queryArgs := append(args, limit, filter.Offset)
	rows, err := s.pool.Query(ctx, query, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	logs := make([]store.AuditLog, 0, limit)
	for rows.Next() {
		log, err := scanPGAuditRow(rows)
		if err != nil {
			return nil, 0, err
		}
		logs = append(logs, *log)
	}
	return logs, total, rows.Err()
}

// GetStats computes aggregated audit metrics via SQL engine.
func (s *AuditStore) GetStats() (*store.AuditStats, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stats := &store.AuditStats{
		ByOperation:     make(map[string]int),
		ByStatus:        make(map[string]int),
		BySecurityLevel: make(map[string]int),
	}

	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*), COALESCE(AVG(duration_ms), 0) FROM audit_logs`).Scan(&stats.TotalOperations, &stats.AvgDurationMs); err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(ctx, `SELECT operation, COUNT(*) FROM audit_logs GROUP BY operation`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var op string
			var count int
			if err := rows.Scan(&op, &count); err == nil {
				stats.ByOperation[op] = count
			}
		}
	}

	rows2, err := s.pool.Query(ctx, `SELECT status, COUNT(*) FROM audit_logs GROUP BY status`)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var st string
			var count int
			if err := rows2.Scan(&st, &count); err == nil {
				stats.ByStatus[st] = count
			}
		}
	}

	rows3, err := s.pool.Query(ctx, `SELECT security_level, COUNT(*) FROM audit_logs WHERE security_level != '' GROUP BY security_level`)
	if err == nil {
		defer rows3.Close()
		for rows3.Next() {
			var lvl string
			var count int
			if err := rows3.Scan(&lvl, &count); err == nil {
				stats.BySecurityLevel[lvl] = count
			}
		}
	}

	return stats, nil
}

// GenerateReport generates an audit compliance report with recommendations.
func (s *AuditStore) GenerateReport(period string) (*store.AuditReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var periodInterval string
	switch period {
	case "1h":
		periodInterval = "1 hour"
	case "7d":
		periodInterval = "7 days"
	case "30d":
		periodInterval = "30 days"
	default:
		periodInterval = "24 hours"
	}

	report := &store.AuditReport{
		BySecurityLevel: make(map[string]int),
		TopOperations:   make([]string, 0),
		Recommendations: make([]string, 0),
	}

	query := fmt.Sprintf(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END), 0) FROM audit_logs WHERE timestamp > NOW() - INTERVAL '%s'`, periodInterval)
	var totalCount, successCount int
	if err := s.pool.QueryRow(ctx, query).Scan(&totalCount, &successCount); err != nil {
		return nil, err
	}
	report.TotalOperations = totalCount
	if totalCount > 0 {
		report.SuccessRate = float64(successCount) / float64(totalCount) * 100.0
	}

	query2 := fmt.Sprintf(`SELECT security_level, COUNT(*) FROM audit_logs WHERE timestamp > NOW() - INTERVAL '%s' AND security_level != '' GROUP BY security_level`, periodInterval)
	rows2, err := s.pool.Query(ctx, query2)
	if err == nil {
		defer rows2.Close()
		for rows2.Next() {
			var lvl string
			var count int
			if err := rows2.Scan(&lvl, &count); err == nil {
				report.BySecurityLevel[lvl] = count
			}
		}
	}

	query3 := fmt.Sprintf(`SELECT operation, COUNT(*) as cnt FROM audit_logs WHERE timestamp > NOW() - INTERVAL '%s' GROUP BY operation ORDER BY cnt DESC LIMIT 5`, periodInterval)
	rows3, err := s.pool.Query(ctx, query3)
	if err == nil {
		defer rows3.Close()
		for rows3.Next() {
			var op string
			var count int
			if err := rows3.Scan(&op, &count); err == nil {
				report.TopOperations = append(report.TopOperations, fmt.Sprintf("%s (%d)", op, count))
			}
		}
	}

	report.Recommendations = store.BuildAuditRecommendations(report.BySecurityLevel, report.SuccessRate)

	return report, nil
}

// SaveSnapshot stores a single snapshot in PostgreSQL.
func (s *AuditStore) SaveSnapshot(snap *store.SnapshotRecord) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := s.pool.Exec(ctx, `
		INSERT INTO snapshots (id, audit_log_id, timestamp, input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, snap.ID, snap.AuditLogID, snap.Timestamp,
		snap.InputSample, snap.OutputSample, snap.Algorithm, snap.ParametersJSON, snap.IntegrityHash, snap.PrevHash)
	return err
}

// ListSnapshots returns paginated snapshot records.
func (s *AuditStore) ListSnapshots(limit, offset int) ([]store.SnapshotRecord, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if limit <= 0 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM snapshots`).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, audit_log_id, timestamp, input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash
		FROM snapshots ORDER BY timestamp DESC LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	snaps := make([]store.SnapshotRecord, 0, limit)
	for rows.Next() {
		snap, err := scanPGSnapshotRow(rows)
		if err != nil {
			return nil, 0, err
		}
		snaps = append(snaps, *snap)
	}
	return snaps, total, rows.Err()
}

// GetSnapshot retrieves a snapshot record by ID.
func (s *AuditStore) GetSnapshot(id string) (*store.SnapshotRecord, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	row := s.pool.QueryRow(ctx, `
		SELECT id, audit_log_id, timestamp, input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash
		FROM snapshots WHERE id = $1
	`, id)
	return scanPGSnapshotRow(row)
}

// VerifyChain verifies the unbroken cryptographic hash chain of recent logs in PostgreSQL.
//
// VerifyChain 按**规范化链序** `(seq ASC, timestamp ASC, id ASC)` 逐条对账核验哈希链（P2-4）。
// 存证链的权威回放顺序是「锚点被锻造的顺序」：单一权威写入者按 `seq` 递进 `prev_hash`，
// 因此 `seq` 必须为首要次序；`(timestamp, id)` 作为确定性的兜底尾序，使**同时间戳的记录
// 在 SQLite、PostgreSQL 与重签工具上永远以同一顺序回放**，不再出现「一处判为断链、
// 另一处判为正常」的伪分叉。若反过来以 timestamp 为首要次序，客户端时间在并发写入下
// 与入队顺序交错时会把合法构建的链误判为断链，故此处保持 seq 优先。
func (s *AuditStore) VerifyChain(limit int) (*store.ChainVerificationResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const maxLimit = 100000
	if limit < 0 || limit > maxLimit {
		limit = maxLimit
	}

	var totalRecords int
	if err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM audit_logs").Scan(&totalRecords); err != nil {
		return nil, err
	}

	query := `
		SELECT id, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash, algorithm,
			parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash
		FROM audit_logs ORDER BY seq ASC, timestamp ASC, id ASC`
	var args []any
	if limit > 0 {
		query += " LIMIT $1"
		args = append(args, limit)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var previousHash string
	count := 0
	legacyCount := 0

	for rows.Next() {
		log, err := scanPGAuditRow(rows)
		if err != nil {
			return nil, err
		}

		// 规范化链序 (seq ASC, timestamp ASC, id ASC)：seq 为锚点锻造序，(timestamp, id)
		// 为确定性兜底尾序，与写入侧链尾裁定、SQLite 及重签工具保持同一口径（P2-4）。

		if log.IntegrityHash != "" {
			ok, hashLabel := store.VerifyAuditIntegrityHash(log.IntegrityHash, log.ID, log.PrevHash, log.Timestamp, log.Algorithm, log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON)
			if !ok {
				// 锚点仍与上游衔接 ⇒ 记录被「原位改写业务字段」；否则为一般性哈希分叉。两者均判无效（fail-closed）。
				reason := store.ChainReasonHashMismatch
				if count == 0 || log.PrevHash == previousHash {
					reason = store.ChainReasonTamperedPayload
				}
				expectedHash := store.ComputeAuditIntegrityHash(log.ID, log.PrevHash, log.Timestamp, log.Algorithm, log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON)
				return &store.ChainVerificationResult{
					Reason:        reason,
					TotalVerified: count,
					TotalRecords:  totalRecords,
					Valid:         false,
					LegacyHashed:  legacyCount,
					BrokenAtID:    log.ID,
					ExpectedHash:  expectedHash,
					ActualHash:    log.IntegrityHash,
					Message:       fmt.Sprintf("integrity hash mismatch at log %s: content modified", log.ID),
				}, nil
			}
			if !store.IsCanonicalHashLabel(hashLabel) {
				legacyCount++
			}
		}

		if count > 0 && log.PrevHash != previousHash {
			// 空锚点单独归因为 missing_prev，便于看板区分「链起点被抹除」与「锚点被替换」。
			reason := store.ChainReasonBrokenChain
			if log.PrevHash == "" {
				reason = store.ChainReasonMissingPrev
			}
			return &store.ChainVerificationResult{
				Reason:        reason,
				TotalVerified: count,
				TotalRecords:  totalRecords,
				Valid:         false,
				LegacyHashed:  legacyCount,
				BrokenAtID:    log.ID,
				ExpectedHash:  previousHash,
				ActualHash:    log.PrevHash,
				Message:       fmt.Sprintf("hash chain broken at log %s: expected prev_hash %s, got %s", log.ID, previousHash, log.PrevHash),
			}, nil
		}

		previousHash = log.IntegrityHash
		if previousHash == "" {
			previousHash = store.ComputeAuditIntegrityHash(log.ID, log.PrevHash, log.Timestamp, log.Algorithm, log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON)
		}
		count++
	}

	result := &store.ChainVerificationResult{
		Reason:        store.ChainReasonOK,
		TotalVerified: count,
		TotalRecords:  totalRecords,
		Valid:         true,
		LegacyHashed:  legacyCount,
		Message:       fmt.Sprintf("hash chain verified successfully (%d records checked, %d total records)", count, totalRecords),
	}
	if legacyCount > 0 {
		// 证据真实但写入于密钥化口径之前：链有效，仅需重签（P2-4 缺口 b）。
		result.Reason = store.ChainReasonLegacyHashed
		result.Message = fmt.Sprintf("hash chain verified successfully (%d records checked, %d total records, %d legacy-hashed records pending canonical SM3 re-signing)", count, totalRecords, legacyCount)
	}
	if limit <= 0 && count < totalRecords {
		result.Reason = store.ChainReasonMissingRecords
		result.Valid = false
		result.Message = fmt.Sprintf("possible missing records: verified %d records but table has %d total records", count, totalRecords)
	}
	return result, rows.Err()
}

// CleanupOld deletes audit logs older than the cutoff time.
func (s *AuditStore) CleanupOld(before time.Time) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tag, err := s.pool.Exec(ctx, `DELETE FROM audit_logs WHERE timestamp < $1`, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// FetchOldestForArchive implements store.AuditArchiveReader for PostgreSQL: it returns the oldest
// expired records ascending in canonical chain order `(seq, timestamp, id)` so the retention guard
// can archive a page, delete that page by ID, and then re-read from the oldest expired record again
// without a cursor — and so an archived segment replays in the same order VerifyChain verifies it.
//
// FetchOldestForArchive 按规范化链序 `(seq ASC, timestamp ASC, id ASC)` 正序返回最早到期的存证日志及其快照；
// 时间过滤语义与 CleanupOld 一致（timestamp < before），调用方每归档一页即按 ID 删除，
// 因此无需游标也不会「删而未档」。
func (s *AuditStore) FetchOldestForArchive(before time.Time, limit int) ([]store.AuditLog, []store.SnapshotRecord, error) {
	if limit <= 0 {
		limit = store.DefaultArchivePageSize
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rows, err := s.pool.Query(ctx, `
		SELECT id, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash, algorithm,
			parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash
		FROM audit_logs
		WHERE timestamp < $1
		ORDER BY seq ASC, timestamp ASC, id ASC LIMIT $2
	`, before, limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	logs := make([]store.AuditLog, 0, limit)
	ids := make([]string, 0, limit)
	for rows.Next() {
		l, err := scanPGAuditRow(rows)
		if err != nil {
			return nil, nil, err
		}
		logs = append(logs, *l)
		ids = append(ids, l.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(ids) == 0 {
		return nil, nil, nil
	}

	snaps := make([]store.SnapshotRecord, 0, len(ids))
	for start := 0; start < len(ids); start += store.ArchiveIDChunkSize {
		end := min(start+store.ArchiveIDChunkSize, len(ids))
		args := make([]any, end-start)
		placeholders := make([]string, end-start)
		for i, id := range ids[start:end] {
			args[i] = id
			placeholders[i] = fmt.Sprintf("$%d", i+1)
		}
		query := `SELECT id, audit_log_id, timestamp, input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash
			 FROM snapshots WHERE audit_log_id IN (` + strings.Join(placeholders, ",") + `) ORDER BY timestamp ASC, id ASC`
		snapRows, err := s.pool.Query(ctx, query, args...)
		if err != nil {
			return nil, nil, err
		}
		for snapRows.Next() {
			snap, err := scanPGSnapshotRow(snapRows)
			if err != nil {
				snapRows.Close()
				return nil, nil, err
			}
			snaps = append(snaps, *snap)
		}
		err = snapRows.Err()
		snapRows.Close()
		if err != nil {
			return nil, nil, err
		}
	}
	return logs, snaps, nil
}

// DeleteLogsByIDs implements store.AuditArchiveReader: it removes exactly the archived logs and
// their snapshots. Snapshot rows are deleted explicitly so archive-consistency does not depend on
// the FK ON DELETE CASCADE setting of the installed schema version.
//
// DeleteLogsByIDs 按 ID 精确删除已完成归档的存证日志及其快照；显式删除快照行，
// 使归档一致性不依赖底层 schema 版本是否开启 FK ON DELETE CASCADE。
func (s *AuditStore) DeleteLogsByIDs(ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var deleted int64
	for start := 0; start < len(ids); start += store.ArchiveIDChunkSize {
		end := min(start+store.ArchiveIDChunkSize, len(ids))
		args := make([]any, end-start)
		placeholders := make([]string, end-start)
		for i, id := range ids[start:end] {
			args[i] = id
			placeholders[i] = fmt.Sprintf("$%d", i+1)
		}
		in := strings.Join(placeholders, ",")

		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return deleted, err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM snapshots WHERE audit_log_id IN (`+in+`)`, args...); err != nil {
			_ = tx.Rollback(ctx)
			return deleted, err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM audit_logs WHERE id IN (`+in+`)`, args...)
		if err != nil {
			_ = tx.Rollback(ctx)
			return deleted, err
		}
		if err := tx.Commit(ctx); err != nil {
			return deleted, err
		}
		deleted += tag.RowsAffected()
	}
	return deleted, nil
}

// HasTablePrivilege reports whether the connected database role holds the given privilege on a
// table, as evaluated server-side by has_table_privilege(current_user, ...).
//
// HasTablePrivilege 由数据库服务端判定当前连接角色是否具备指定表的权限，
// 用于存证「只写账号」自检（P1-6）：审计表必须拒绝 UPDATE/DELETE。
func (s *AuditStore) HasTablePrivilege(ctx context.Context, table, privilege string) (bool, error) {
	var allowed bool
	err := s.pool.QueryRow(ctx, `SELECT has_table_privilege(current_user, $1, $2)`, table, privilege).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("postgres: has_table_privilege(%s, %s): %w", table, privilege, err)
	}
	return allowed, nil
}

func buildPGAuditWhere(filter store.AuditFilter) (string, []any) {
	conditions := make([]string, 0)
	args := make([]any, 0)

	if filter.TaskID != "" {
		args = append(args, filter.TaskID)
		conditions = append(conditions, fmt.Sprintf("task_id = $%d", len(args)))
	}
	if filter.APICode != "" {
		args = append(args, filter.APICode)
		conditions = append(conditions, fmt.Sprintf("api_code = $%d", len(args)))
	}
	if filter.DatasourceID != "" {
		args = append(args, filter.DatasourceID)
		conditions = append(conditions, fmt.Sprintf("(datasource_id = $%d OR datasource = $%d)", len(args), len(args)))
	} else if filter.DataSource != "" {
		args = append(args, filter.DataSource)
		conditions = append(conditions, fmt.Sprintf("(datasource = $%d OR datasource_id = $%d)", len(args), len(args)))
	}
	if filter.Operation != "" {
		args = append(args, filter.Operation)
		conditions = append(conditions, fmt.Sprintf("operation = $%d", len(args)))
	}
	if filter.User != "" {
		args = append(args, filter.User)
		conditions = append(conditions, fmt.Sprintf("user_name = $%d", len(args)))
	}
	if filter.Status != "" {
		args = append(args, filter.Status)
		conditions = append(conditions, fmt.Sprintf("status = $%d", len(args)))
	}
	if filter.SecurityLevel != "" {
		args = append(args, filter.SecurityLevel)
		conditions = append(conditions, fmt.Sprintf("security_level = $%d", len(args)))
	}

	if len(conditions) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

type pgRowScanner interface {
	Scan(dest ...any) error
}

func scanPGAuditRow(row pgRowScanner) (*store.AuditLog, error) {
	var l store.AuditLog
	var paramsJSON *string
	var taskID, apiCode, datasourceID, prevHash, integrityHash *string

	err := row.Scan(&l.ID, &taskID, &apiCode, &datasourceID, &l.Timestamp, &l.Operation, &l.DataSource, &l.InputHash, &l.OutputHash,
		&l.Algorithm, &paramsJSON, &l.InputRows, &l.OutputRows, &l.DurationMs,
		&l.User, &l.Status, &l.ErrorMessage, &l.SecurityLevel, &prevHash, &integrityHash)
	if err != nil {
		return nil, err
	}

	if taskID != nil {
		l.TaskID = *taskID
	}
	if apiCode != nil {
		l.APICode = *apiCode
	}
	if datasourceID != nil {
		l.DatasourceID = *datasourceID
	}
	if prevHash != nil {
		l.PrevHash = *prevHash
	}
	if integrityHash != nil {
		l.IntegrityHash = *integrityHash
	}
	if l.DatasourceID == "" && l.DataSource != "" {
		l.DatasourceID = l.DataSource
	}
	if l.DataSource == "" && l.DatasourceID != "" {
		l.DataSource = l.DatasourceID
	}

	if paramsJSON != nil {
		l.ParametersJSON = *paramsJSON
		_ = json.Unmarshal([]byte(*paramsJSON), &l.Parameters)
	}
	return &l, nil
}

func scanPGSnapshotRow(row pgRowScanner) (*store.SnapshotRecord, error) {
	var snap store.SnapshotRecord
	var paramsJSON, prevHash *string

	err := row.Scan(&snap.ID, &snap.AuditLogID, &snap.Timestamp, &snap.InputSample, &snap.OutputSample,
		&snap.Algorithm, &paramsJSON, &snap.IntegrityHash, &prevHash)
	if err != nil {
		return nil, err
	}

	if prevHash != nil {
		snap.PrevHash = *prevHash
	}
	if paramsJSON != nil {
		snap.ParametersJSON = *paramsJSON
		_ = json.Unmarshal([]byte(*paramsJSON), &snap.Parameters)
	}
	return &snap, nil
}

var _ store.AuditStore = (*AuditStore)(nil)
