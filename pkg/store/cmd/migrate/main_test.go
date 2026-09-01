// Package main provides integration tests for the database migration tool.
// Package main 为 Phase A (SQLite) 到 Phase B (PostgreSQL) 的数据迁移工具提供集成测试。
//
// ==============================================================================
// 【测试运行前置条件】
// 测试依赖真实 PostgreSQL 实例：
//
//	PRIVSHIELD_PG_TEST_DSN="postgres://user:pass@localhost:5432/privshield_migrate_test" \
//	go test -tags=integration -v ./pkg/store/cmd/migrate/...
//
// 若未配置 PRIVSHIELD_PG_TEST_DSN 环境变量，测试将自动安全跳过（Skip）。
//
// 【测试场景覆盖】
// 1. TestMigrateSQLiteToPostgres：验证任务、审计日志与快照的完整迁移及哈希链对账核验；
// 2. TestMigrateSQLiteToPostgres_VerifySnapshots：验证迁移后 SM4-GCM 密文快照解密验真模式；
// 3. TestMigrateSQLiteToPostgres_VerifySnapshotsWrongKey：验证使用错误密钥时解密验真必须报错拦截；
// 4. TestVerifySnapshotsOnlyMode：验证 -snapshot-verify-mode=only 单独验真模式。
// ==============================================================================

package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	pkcrypto "github.com/fengzhizi319/PrivShield/pkg/crypto"
	"github.com/fengzhizi319/PrivShield/pkg/store/postgres"
	"github.com/fengzhizi319/PrivShield/pkg/store/sqlite"
)

func getTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("PRIVSHIELD_PG_TEST_DSN")
	if dsn == "" {
		t.Skip("PRIVSHIELD_PG_TEST_DSN not set, skipping migration integration test")
	}
	return dsn
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func computeIntegrityHash(logID, prevHash string, timestamp time.Time, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON string) string {
	data := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%s|%v",
		prevHash, logID, timestamp.UTC().Format(time.RFC3339Nano), algorithm,
		inputHash, outputHash, user, securityLevel, paramsJSON)
	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", hash)
}

func encryptSnapshotSample(t *testing.T, plaintext, key string) string {
	t.Helper()
	ciphertext, err := pkcrypto.EncryptString(plaintext, key)
	if err != nil {
		t.Fatalf("encrypt snapshot sample: %v", err)
	}
	return ciphertext
}

func insertTask(t *testing.T, db *sql.DB, id string, createdAt time.Time) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO tasks (id, status, stage, source, api_code, datasource_id, operation, priority, created_at, started_at, completed_at, duration_ms, "error", payload_json, retry_count, retry_after, lease_owner, lease_token, lease_expires_at, version, max_retries)
		VALUES (?, 'completed', 'done', 'ds_yibao', 'api1_yibao', 'ds_yibao', 'mask', 50, ?, NULL, NULL, 120, '', '{}', 0, NULL, '', '', NULL, 0, 0)
	`, id, createdAt); err != nil {
		t.Fatalf("insert task %s: %v", id, err)
	}
}

func cleanPostgresTables(t *testing.T, ctx context.Context, dsn string) *postgres.Store {
	t.Helper()
	logger := testLogger()

	auditStore, err := postgres.NewAuditStore(ctx, postgres.Config{DSN: dsn}, logger)
	if err != nil {
		t.Fatalf("create postgres audit store: %v", err)
	}
	defer auditStore.Close()

	taskStore, err := postgres.NewStore(ctx, postgres.Config{DSN: dsn, MaxConn: 3, MinConn: 1}, logger)
	if err != nil {
		t.Fatalf("create postgres task store: %v", err)
	}
	pool := taskStore.Pool()
	if _, err := pool.Exec(ctx, "DELETE FROM snapshots"); err != nil {
		t.Fatalf("clean snapshots: %v", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM audit_logs"); err != nil {
		t.Fatalf("clean audit_logs: %v", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM tasks"); err != nil {
		t.Fatalf("clean tasks: %v", err)
	}
	return taskStore
}

// ─────────────────────────────────────────────────────────────
// 1. SQLite 到 PostgreSQL 迁移与哈希链核验端到端测试
// ─────────────────────────────────────────────────────────────

func TestMigrateSQLiteToPostgres(t *testing.T) {
	ctx := context.Background()
	dsn := getTestDSN(t)
	logger := testLogger()

	taskStore := cleanPostgresTables(t, ctx, dsn)
	defer taskStore.Close()

	auditStore, err := postgres.NewAuditStore(ctx, postgres.Config{DSN: dsn}, logger)
	if err != nil {
		t.Fatalf("create postgres audit store: %v", err)
	}
	defer auditStore.Close()

	pool := taskStore.Pool()

	tmpDir := t.TempDir()
	hubDBPath := filepath.Join(tmpDir, "service-hub.db")
	auditDBPath := filepath.Join(tmpDir, "audit-log.db")

	hubDB, err := sql.Open("sqlite", hubDBPath)
	if err != nil {
		t.Fatalf("open hub sqlite: %v", err)
	}
	if err := sqlite.InitTaskTables(hubDB); err != nil {
		t.Fatalf("init hub task tables: %v", err)
	}
	insertTask(t, hubDB, "task-1", time.Now())
	_ = hubDB.Close()

	auditDB, err := sql.Open("sqlite", auditDBPath)
	if err != nil {
		t.Fatalf("open audit sqlite: %v", err)
	}
	if err := sqlite.InitAuditTables(auditDB); err != nil {
		t.Fatalf("init audit tables: %v", err)
	}

	ts := time.Now().UTC().Truncate(time.Microsecond)
	prev1 := ""
	hash1 := computeIntegrityHash("log-1", prev1, ts, "mask", "input1", "output1", "admin", "L3", "{}")
	if _, err := auditDB.Exec(`
		INSERT INTO audit_logs (id, task_id, api_code, datasource_id, timestamp, operation, datasource,
			input_hash, output_hash, algorithm, parameters_json, input_rows, output_rows, duration_ms,
			user_name, status, error_message, security_level, prev_hash, integrity_hash)
		VALUES ('log-1', 'task-1', 'api1_yibao', 'ds_yibao', ?, 'mask', 'ds_yibao',
			'input1', 'output1', 'mask', '{}', 1, 1, 100,
			'admin', 'success', '', 'L3', ?, ?)
	`, ts, prev1, hash1); err != nil {
		t.Fatalf("insert audit log 1: %v", err)
	}

	ts2 := ts.Add(time.Second)
	hash2 := computeIntegrityHash("log-2", hash1, ts2, "mask", "input2", "output2", "admin", "L3", "{}")
	if _, err := auditDB.Exec(`
		INSERT INTO audit_logs (id, task_id, api_code, datasource_id, timestamp, operation, datasource,
			input_hash, output_hash, algorithm, parameters_json, input_rows, output_rows, duration_ms,
			user_name, status, error_message, security_level, prev_hash, integrity_hash)
		VALUES ('log-2', 'task-1', 'api1_yibao', 'ds_yibao', ?, 'mask', 'ds_yibao',
			'input2', 'output2', 'mask', '{}', 1, 1, 100,
			'admin', 'success', '', 'L3', ?, ?)
	`, ts2, hash1, hash2); err != nil {
		t.Fatalf("insert audit log 2: %v", err)
	}

	if _, err := auditDB.Exec(`
		INSERT INTO snapshots (id, audit_log_id, timestamp, input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash)
		VALUES ('snap-1', 'log-1', ?, 'in', 'out', 'mask', '{}', ?, ?)
	`, ts, hash1, prev1); err != nil {
		t.Fatalf("insert snapshot: %v", err)
	}
	_ = auditDB.Close()

	// 执行迁移
	cfg := runConfig{
		hubDBPath:          hubDBPath,
		auditDBPath:        auditDBPath,
		pgDSN:              dsn,
		batchSize:          100,
		dryRun:             false,
		verify:             false,
		snapshotVerifyMode: "skip",
	}
	if err := run(ctx, logger, cfg); err != nil {
		t.Fatalf("run migration: %v", err)
	}

	// 验证迁移条数
	var taskCount int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM tasks").Scan(&taskCount); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if taskCount != 1 {
		t.Errorf("expected 1 task, got %d", taskCount)
	}

	var logCount, snapCount int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM audit_logs").Scan(&logCount); err != nil {
		t.Fatalf("count audit_logs: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM snapshots").Scan(&snapCount); err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	if logCount != 2 {
		t.Errorf("expected 2 audit logs, got %d", logCount)
	}
	if snapCount != 1 {
		t.Errorf("expected 1 snapshot, got %d", snapCount)
	}

	// 验证哈希链在 PostgreSQL 中依然连续且完全有效
	res, err := auditStore.VerifyChain(0)
	if err != nil {
		t.Fatalf("verify chain: %v", err)
	}
	if !res.Valid {
		t.Fatalf("hash chain invalid: %s (broken_at_id=%s)", res.Message, res.BrokenAtID)
	}
	if res.TotalVerified != 2 {
		t.Errorf("expected 2 verified logs, got %d", res.TotalVerified)
	}
}

// ─────────────────────────────────────────────────────────────
// 2. 密文快照解密验真测试
// ─────────────────────────────────────────────────────────────

func TestMigrateSQLiteToPostgres_VerifySnapshots(t *testing.T) {
	ctx := context.Background()
	dsn := getTestDSN(t)
	logger := testLogger()

	taskStore := cleanPostgresTables(t, ctx, dsn)
	defer taskStore.Close()

	auditStore, err := postgres.NewAuditStore(ctx, postgres.Config{DSN: dsn}, logger)
	if err != nil {
		t.Fatalf("create postgres audit store: %v", err)
	}
	defer auditStore.Close()

	pool := taskStore.Pool()

	tmpDir := t.TempDir()
	hubDBPath := filepath.Join(tmpDir, "service-hub.db")
	auditDBPath := filepath.Join(tmpDir, "audit-log.db")

	hubDB, err := sql.Open("sqlite", hubDBPath)
	if err != nil {
		t.Fatalf("open hub sqlite: %v", err)
	}
	if err := sqlite.InitTaskTables(hubDB); err != nil {
		t.Fatalf("init hub task tables: %v", err)
	}
	insertTask(t, hubDB, "task-1", time.Now())
	_ = hubDB.Close()

	auditDB, err := sql.Open("sqlite", auditDBPath)
	if err != nil {
		t.Fatalf("open audit sqlite: %v", err)
	}
	if err := sqlite.InitAuditTables(auditDB); err != nil {
		t.Fatalf("init audit tables: %v", err)
	}

	const key = "test-secret-key-12345"
	ts := time.Now().UTC().Truncate(time.Microsecond)
	prev1 := ""
	hash1 := computeIntegrityHash("log-1", prev1, ts, "mask", "input1", "output1", "admin", "L3", "{}")
	if _, err := auditDB.Exec(`
		INSERT INTO audit_logs (id, task_id, api_code, datasource_id, timestamp, operation, datasource,
			input_hash, output_hash, algorithm, parameters_json, input_rows, output_rows, duration_ms,
			user_name, status, error_message, security_level, prev_hash, integrity_hash)
		VALUES ('log-1', 'task-1', 'api1_yibao', 'ds_yibao', ?, 'mask', 'ds_yibao',
			'input1', 'output1', 'mask', '{}', 1, 1, 100,
			'admin', 'success', '', 'L3', ?, ?)
	`, ts, prev1, hash1); err != nil {
		t.Fatalf("insert audit log 1: %v", err)
	}

	plainIn := "plain-input"
	plainOut := "plain-output"
	encIn := encryptSnapshotSample(t, "sensitive input", key)
	encOut := encryptSnapshotSample(t, "sensitive output", key)

	if _, err := auditDB.Exec(`
		INSERT INTO snapshots (id, audit_log_id, timestamp, input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash)
		VALUES ('snap-plain', 'log-1', ?, ?, ?, 'mask', '{}', ?, ?)
	`, ts, plainIn, plainOut, hash1, prev1); err != nil {
		t.Fatalf("insert plaintext snapshot: %v", err)
	}
	if _, err := auditDB.Exec(`
		INSERT INTO snapshots (id, audit_log_id, timestamp, input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash)
		VALUES ('snap-enc', 'log-1', ?, ?, ?, 'mask', '{}', ?, ?)
	`, ts, encIn, encOut, hash1, prev1); err != nil {
		t.Fatalf("insert encrypted snapshot: %v", err)
	}
	_ = auditDB.Close()

	cfg := runConfig{
		hubDBPath:          hubDBPath,
		auditDBPath:        auditDBPath,
		pgDSN:              dsn,
		batchSize:          100,
		dryRun:             false,
		verify:             false,
		snapshotVerifyMode: "after-migrate",
		auditEncryptionKey: key,
	}
	if err := run(ctx, logger, cfg); err != nil {
		t.Fatalf("run migration with snapshot verification: %v", err)
	}

	var snapCount int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM snapshots").Scan(&snapCount); err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	if snapCount != 2 {
		t.Errorf("expected 2 snapshots, got %d", snapCount)
	}
}

// ─────────────────────────────────────────────────────────────
// 3. 错误解密密钥拦截测试
// ─────────────────────────────────────────────────────────────

func TestMigrateSQLiteToPostgres_VerifySnapshotsWrongKey(t *testing.T) {
	ctx := context.Background()
	dsn := getTestDSN(t)
	logger := testLogger()

	taskStore := cleanPostgresTables(t, ctx, dsn)
	defer taskStore.Close()

	auditStore, err := postgres.NewAuditStore(ctx, postgres.Config{DSN: dsn}, logger)
	if err != nil {
		t.Fatalf("create postgres audit store: %v", err)
	}
	defer auditStore.Close()

	tmpDir := t.TempDir()
	hubDBPath := filepath.Join(tmpDir, "service-hub.db")
	auditDBPath := filepath.Join(tmpDir, "audit-log.db")

	hubDB, err := sql.Open("sqlite", hubDBPath)
	if err != nil {
		t.Fatalf("open hub sqlite: %v", err)
	}
	if err := sqlite.InitTaskTables(hubDB); err != nil {
		t.Fatalf("init hub task tables: %v", err)
	}
	insertTask(t, hubDB, "task-1", time.Now())
	_ = hubDB.Close()

	auditDB, err := sql.Open("sqlite", auditDBPath)
	if err != nil {
		t.Fatalf("open audit sqlite: %v", err)
	}
	if err := sqlite.InitAuditTables(auditDB); err != nil {
		t.Fatalf("init audit tables: %v", err)
	}

	const key = "test-secret-key-12345"
	const wrongKey = "wrong-secret-key"
	ts := time.Now().UTC().Truncate(time.Microsecond)
	prev1 := ""
	hash1 := computeIntegrityHash("log-1", prev1, ts, "mask", "input1", "output1", "admin", "L3", "{}")
	if _, err := auditDB.Exec(`
		INSERT INTO audit_logs (id, task_id, api_code, datasource_id, timestamp, operation, datasource,
			input_hash, output_hash, algorithm, parameters_json, input_rows, output_rows, duration_ms,
			user_name, status, error_message, security_level, prev_hash, integrity_hash)
		VALUES ('log-1', 'task-1', 'api1_yibao', 'ds_yibao', ?, 'mask', 'ds_yibao',
			'input1', 'output1', 'mask', '{}', 1, 1, 100,
			'admin', 'success', '', 'L3', ?, ?)
	`, ts, prev1, hash1); err != nil {
		t.Fatalf("insert audit log 1: %v", err)
	}

	encIn := encryptSnapshotSample(t, "sensitive input", key)
	encOut := encryptSnapshotSample(t, "sensitive output", key)
	if _, err := auditDB.Exec(`
		INSERT INTO snapshots (id, audit_log_id, timestamp, input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash)
		VALUES ('snap-enc', 'log-1', ?, ?, ?, 'mask', '{}', ?, ?)
	`, ts, encIn, encOut, hash1, prev1); err != nil {
		t.Fatalf("insert encrypted snapshot: %v", err)
	}
	_ = auditDB.Close()

	cfg := runConfig{
		hubDBPath:          hubDBPath,
		auditDBPath:        auditDBPath,
		pgDSN:              dsn,
		batchSize:          100,
		dryRun:             false,
		verify:             false,
		snapshotVerifyMode: "after-migrate",
		auditEncryptionKey: wrongKey,
	}
	if err := run(ctx, logger, cfg); err == nil {
		t.Fatalf("expected migration/verification to fail with wrong key, got nil")
	}
}

// ─────────────────────────────────────────────────────────────
// 4. 单独验真模式测试 (snapshot-verify-mode=only)
// ─────────────────────────────────────────────────────────────

func TestVerifySnapshotsOnlyMode(t *testing.T) {
	ctx := context.Background()
	dsn := getTestDSN(t)
	logger := testLogger()

	taskStore := cleanPostgresTables(t, ctx, dsn)
	defer taskStore.Close()

	pool := taskStore.Pool()

	const key = "test-only-mode-key"
	ts := time.Now().UTC().Truncate(time.Microsecond)
	encIn := encryptSnapshotSample(t, "only input", key)
	encOut := encryptSnapshotSample(t, "only output", key)

	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_logs (id, task_id, api_code, datasource_id, "timestamp", operation, datasource,
			input_hash, output_hash, algorithm, parameters_json, input_rows, output_rows, duration_ms,
			user_name, status, error_message, security_level, prev_hash, integrity_hash)
		VALUES ('log-1', '', '', '', $1, 'mask', '', '', '', 'mask', '{}', 0, 0, 0, '', 'success', '', 'L3', '', '')
	`, ts); err != nil {
		t.Fatalf("insert audit log directly: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO snapshots (id, audit_log_id, "timestamp", input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash)
		VALUES ('snap-only', 'log-1', $1, $2, $3, 'mask', '{}', '', '')
	`, ts, encIn, encOut); err != nil {
		t.Fatalf("insert snapshot directly: %v", err)
	}

	cfg := runConfig{
		hubDBPath:          "",
		auditDBPath:        "",
		pgDSN:              dsn,
		batchSize:          100,
		dryRun:             false,
		verify:             false,
		snapshotVerifyMode: "only",
		auditEncryptionKey: key,
	}
	if err := run(ctx, logger, cfg); err != nil {
		t.Fatalf("run snapshot-only verification: %v", err)
	}
}
