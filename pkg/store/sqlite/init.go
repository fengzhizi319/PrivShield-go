// Package sqlite provides SQLite-backed implementations of the store interfaces.
// Package sqlite 提供基于 SQLite 的存储接口实现。
//
// ==============================================================================
// 【架构与技术选型】
// 1. 【modernc.org/sqlite 纯 Go 驱动】：
//    无 CGO 依赖，容器构建无需 gcc 或 libsqlite3-dev，轻量且跨平台兼容；
// 2. 【WAL 预写日志模式与 NORMAL 同步】：
//    设置 `PRAGMA journal_mode=WAL` 与 `PRAGMA synchronous=NORMAL`，
//    在保证崩溃一致性的前提下大幅提升并发读写吞吐；
// 3. 【连接池与锁冲突规避 (P24 fix)】：
//    SQLite 仅支持单写者；限制 MaxOpenConns=4, MaxIdleConns=2, busy_timeout=5000ms，
//    防止过多并发连接引发 SQLite busy 锁竞争；
// 4. 【数据库启动完整性探针 (ValidateIntegrity)】：
//    通过 `PRAGMA integrity_check` 早期探测突发断电导致的数据库文件损坏，防止带病运行；
// 5. 【平滑 Schema 迁移与 Canonical 标识回填】：
//    InitTaskTables/InitAuditTables 自动检测并增量补齐缺失列，
//    并通过 pkg/naming 注册表作为单一事实源执行历史标识回填。
// ==============================================================================

package sqlite

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/naming"

	_ "modernc.org/sqlite" // SQLite 纯 Go 驱动注册
)

// Open opens a SQLite database at the given path and initializes tables.
//
// Open 打开指定路径的 SQLite 数据库，配置高性能连接池与 WAL 参数。
//
// 参数说明：
// - path: 数据库文件路径；为空字符串时返回 nil（指示上层降级使用内存存储）；
// - logger: 结构化日志记录器，为 nil 时默认使用 slog.Default()。
//
// 执行逻辑：
// 1. 校验 path，追加 busy_timeout、journal_mode=WAL 与 synchronous=NORMAL 参数；
// 2. 调用 sql.Open 打开数据库；
// 3. 执行 PRAGMA journal_mode=WAL、busy_timeout=5000、synchronous=NORMAL 与 foreign_keys=ON；
// 4. 配置最大连接数 4、空闲连接数 2、连接最大生命周期 5 分钟；
// 5. 记录就绪日志并返回 *sql.DB 实例。
func Open(path string, logger *slog.Logger) (*sql.DB, error) {
	if path == "" {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}

	dsn := path
	if !strings.Contains(path, "?") {
		dsn = path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}

	// WAL 模式提升并发读性能
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	// 5 秒忙等待超时，处理瞬时写锁竞争
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set busy_timeout: %w", err)
	}

	// P24 fix: SQLite 同一时间仅支持一个写入者；限制连接数防止过度锁争用
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * 60 * time.Second)

	// P26 fix: 设置 synchronous=NORMAL 提升 WAL 模式写入性能（仍具备崩溃安全性）
	if _, err := db.Exec("PRAGMA synchronous=NORMAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set synchronous: %w", err)
	}

	// 启用外键约束强制执行
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set foreign_keys: %w", err)
	}

	logger.Info("sqlite database opened", "path", path)
	return db, nil
}

// ValidateIntegrity performs a SQLite integrity check on the database file at dbPath.
//
// ValidateIntegrity 对指定路径的 SQLite 数据库文件执行完整性校验。
//
// 返回值：
// - nil: 校验通过或 dbPath 为空（内存模式无需校验）；
// - error: 数据库损坏或检测语句失败。
//
// 业务价值：
// 异常断电或机器宕机可能导致 SQLite 数据库损坏，此函数在服务启动早期拦截损坏，防止脏读脏写。
func ValidateIntegrity(dbPath string) error {
	if dbPath == "" {
		return nil // 内存模式无需校验
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("open database for integrity check: %w", err)
	}
	defer db.Close()

	var result string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("integrity check query failed: %w", err)
	}

	if result != "ok" {
		return fmt.Errorf("database corruption detected: %s", result)
	}

	return nil
}

// InitTaskTables creates the tasks table if it doesn't exist.
//
// InitTaskTables 初始化 tasks 表结构并执行向后兼容增量迁移。
//
// 执行逻辑：
// 1. 创建 tasks 主表及基础索引；
// 2. 查询 PRAGMA table_info(tasks) 分析现有列；
// 3. 补全 retry_count、retry_after、trace_id、lease_*、api_code、datasource_id 等列；
// 4. 调用 backfillTaskCanonicalIDs 回填规范化标识；
// 5. 创建部分索引与辅助索引。
func InitTaskTables(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL DEFAULT 'pending',
			stage TEXT NOT NULL DEFAULT 'queued',
			source TEXT,
			api_code TEXT DEFAULT '',
			datasource_id TEXT DEFAULT '',
			operation TEXT,
			priority INTEGER DEFAULT 0,
			created_at DATETIME NOT NULL,
			started_at DATETIME,
			completed_at DATETIME,
			duration_ms INTEGER DEFAULT 0,
			error TEXT,
			error_class TEXT DEFAULT '',
			payload_json TEXT,
			retry_count INTEGER DEFAULT 0,
			retry_after DATETIME,
			trace_id TEXT DEFAULT '',
			lease_owner TEXT DEFAULT '',
			lease_token TEXT DEFAULT '',
			lease_expires_at DATETIME,
			version INTEGER DEFAULT 0,
			max_retries INTEGER DEFAULT 3
		);
		CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
		CREATE INDEX IF NOT EXISTS idx_tasks_created ON tasks(created_at);
	`)
	if err != nil {
		return err
	}

	// 向后兼容迁移：检查并补充缺失列
	cursor, err := db.Query("PRAGMA table_info(tasks)")
	if err != nil {
		return err
	}
	defer cursor.Close()

	columns := make(map[string]bool)
	for cursor.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := cursor.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			continue
		}
		columns[name] = true
	}

	if !columns["retry_count"] {
		if _, err := db.Exec("ALTER TABLE tasks ADD COLUMN retry_count INTEGER DEFAULT 0"); err != nil {
			return err
		}
	}
	if !columns["retry_after"] {
		if _, err := db.Exec("ALTER TABLE tasks ADD COLUMN retry_after DATETIME"); err != nil {
			return err
		}
	}
	if !columns["trace_id"] {
		if _, err := db.Exec("ALTER TABLE tasks ADD COLUMN trace_id TEXT DEFAULT ''"); err != nil {
			return err
		}
	}
	if !columns["error_class"] {
		if _, err := db.Exec("ALTER TABLE tasks ADD COLUMN error_class TEXT DEFAULT ''"); err != nil {
			return err
		}
	}

	// ── Phase B: 租约字段 ──
	if !columns["lease_owner"] {
		if _, err := db.Exec("ALTER TABLE tasks ADD COLUMN lease_owner TEXT DEFAULT ''"); err != nil {
			return err
		}
	}
	if !columns["lease_token"] {
		if _, err := db.Exec("ALTER TABLE tasks ADD COLUMN lease_token TEXT DEFAULT ''"); err != nil {
			return err
		}
	}
	if !columns["lease_expires_at"] {
		if _, err := db.Exec("ALTER TABLE tasks ADD COLUMN lease_expires_at DATETIME"); err != nil {
			return err
		}
	}
	if !columns["version"] {
		if _, err := db.Exec("ALTER TABLE tasks ADD COLUMN version INTEGER DEFAULT 0"); err != nil {
			return err
		}
	}
	if !columns["max_retries"] {
		if _, err := db.Exec("ALTER TABLE tasks ADD COLUMN max_retries INTEGER DEFAULT 3"); err != nil {
			return err
		}
	}

	// ── Canonical 标识列 ──
	if !columns["api_code"] {
		if _, err := db.Exec("ALTER TABLE tasks ADD COLUMN api_code TEXT DEFAULT ''"); err != nil {
			return err
		}
	}
	if !columns["datasource_id"] {
		if _, err := db.Exec("ALTER TABLE tasks ADD COLUMN datasource_id TEXT DEFAULT ''"); err != nil {
			return err
		}
	}
	if err := backfillTaskCanonicalIDs(db); err != nil {
		return err
	}

	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_tasks_retry_after ON tasks(retry_after)"); err != nil {
		return err
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_tasks_lease_expires ON tasks(lease_expires_at)"); err != nil {
		return err
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_tasks_claim ON tasks(status, priority DESC, created_at) WHERE status='pending'"); err != nil {
		return err
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_tasks_datasource_id ON tasks(datasource_id)"); err != nil {
		return err
	}

	return nil
}

// backfillTaskCanonicalIDs 把历史任务行回填为 canonical 标识：
//  1. datasource_id 从已规范化的 source（ds_* 形式）复制；
//  2. api_code 由 pkg/naming 注册表按 datasource_id 反查（唯一事实源，不硬编码映射）。
//
// 幂等设计：仅填充空值，重复执行不改变已有数据。
func backfillTaskCanonicalIDs(db *sql.DB) error {
	if _, err := db.Exec(
		`UPDATE tasks SET datasource_id = source
		 WHERE (datasource_id IS NULL OR datasource_id = '') AND substr(source, 1, 3) = 'ds_'`); err != nil {
		return err
	}
	for _, entry := range naming.Registry {
		if entry.APICode == "" || entry.DataSourceID == "" {
			continue
		}
		if _, err := db.Exec(
			`UPDATE tasks SET api_code = ?
			 WHERE datasource_id = ? AND (api_code IS NULL OR api_code = '')`,
			entry.APICode, entry.DataSourceID); err != nil {
			return err
		}
	}
	return nil
}

// InitDataSourceTables creates the datasources and access_audit tables.
// InitDataSourceTables 初始化 datasources 与 access_audit 表结构及索引。
func InitDataSourceTables(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS datasources (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			type TEXT,
			host TEXT,
			port INTEGER,
			database_name TEXT,
			security_level TEXT,
			status TEXT NOT NULL DEFAULT 'disconnected',
			created_at DATETIME NOT NULL,
			last_check_at DATETIME,
			tags_json TEXT
		);
		CREATE TABLE IF NOT EXISTS access_audit (
			id TEXT PRIMARY KEY,
			datasource_id TEXT,
			datasource_name TEXT,
			operation TEXT,
			user_name TEXT,
			timestamp DATETIME NOT NULL,
			records_count INTEGER DEFAULT 0,
			status TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_access_audit_ds ON access_audit(datasource_id);
	`)
	return err
}

// InitAuditTables creates the audit_logs and snapshots tables.
//
// InitAuditTables 初始化 audit_logs 与 snapshots 表结构，并安全执行增量列迁移。
//
// 关键顺序约束：
// 必须先 ALTER TABLE 补充 task_id、api_code、datasource_id 列，
// 随后才能 CREATE INDEX，否则旧库会因 `no such column` 报错导致服务启动失败。
func InitAuditTables(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS audit_logs (
			id TEXT PRIMARY KEY,
			seq INTEGER,
			task_id TEXT DEFAULT '',
			api_code TEXT DEFAULT '',
			datasource_id TEXT DEFAULT '',
			timestamp DATETIME NOT NULL,
			operation TEXT,
			datasource TEXT,
			input_hash TEXT,
			output_hash TEXT,
			algorithm TEXT,
			parameters_json TEXT,
			input_rows INTEGER DEFAULT 0,
			output_rows INTEGER DEFAULT 0,
			duration_ms INTEGER DEFAULT 0,
			user_name TEXT,
			status TEXT,
			error_message TEXT,
			security_level TEXT,
			prev_hash TEXT DEFAULT '',
			integrity_hash TEXT DEFAULT '',
			sm2_signature TEXT DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS snapshots (
			id TEXT PRIMARY KEY,
			audit_log_id TEXT,
			timestamp DATETIME NOT NULL,
			input_sample TEXT,
			output_sample TEXT,
			algorithm TEXT,
			parameters_json TEXT,
			integrity_hash TEXT,
			prev_hash TEXT DEFAULT '',
			sm2_signature TEXT DEFAULT ''
			FOREIGN KEY(audit_log_id) REFERENCES audit_logs(id)
		);
		CREATE INDEX IF NOT EXISTS idx_audit_logs_ts ON audit_logs(timestamp);
		CREATE INDEX IF NOT EXISTS idx_audit_logs_op ON audit_logs(operation);
		CREATE INDEX IF NOT EXISTS idx_snapshots_audit ON snapshots(audit_log_id);
	`)
	if err != nil {
		return err
	}

	// 检查并迁移 audit_logs
	cursor, err := db.Query("PRAGMA table_info(audit_logs)")
	if err != nil {
		return err
	}
	defer cursor.Close()

	columns := make(map[string]bool)
	for cursor.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := cursor.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			continue
		}
		columns[name] = true
	}

	if !columns["task_id"] {
		if _, err := db.Exec("ALTER TABLE audit_logs ADD COLUMN task_id TEXT DEFAULT ''"); err != nil {
			return err
		}
	}
	if !columns["api_code"] {
		if _, err := db.Exec("ALTER TABLE audit_logs ADD COLUMN api_code TEXT DEFAULT ''"); err != nil {
			return err
		}
	}
	if !columns["datasource_id"] {
		if _, err := db.Exec("ALTER TABLE audit_logs ADD COLUMN datasource_id TEXT DEFAULT ''"); err != nil {
			return err
		}
	}
	if !columns["prev_hash"] {
		if _, err := db.Exec("ALTER TABLE audit_logs ADD COLUMN prev_hash TEXT DEFAULT ''"); err != nil {
			return err
		}
	}
	if !columns["integrity_hash"] {
		if _, err := db.Exec("ALTER TABLE audit_logs ADD COLUMN integrity_hash TEXT DEFAULT ''"); err != nil {
			return err
		}
	}
	if !columns["sm2_signature"] {
		if _, err := db.Exec("ALTER TABLE audit_logs ADD COLUMN sm2_signature TEXT DEFAULT ''"); err != nil {
			return err
		}
	}
	// P1 fix: monotonic sequence column for deterministic chain verification order.
	if !columns["seq"] {
		if _, err := db.Exec("ALTER TABLE audit_logs ADD COLUMN seq INTEGER"); err != nil {
			return err
		}
		// Backfill existing rows with their rowid order so VerifyChain stays stable.
		if _, err := db.Exec("UPDATE audit_logs SET seq = rowid WHERE seq IS NULL"); err != nil {
			return err
		}
	}

	// 索引在列迁移后创建
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_audit_logs_ds ON audit_logs(datasource_id)"); err != nil {
		return err
	}
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_audit_logs_task ON audit_logs(task_id)"); err != nil {
		return err
	}

	// 检查并迁移 snapshots
	snapCursor, err := db.Query("PRAGMA table_info(snapshots)")
	if err != nil {
		return err
	}
	defer snapCursor.Close()

	snapColumns := make(map[string]bool)
	for snapCursor.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := snapCursor.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			continue
		}
		snapColumns[name] = true
	}

	if !snapColumns["prev_hash"] {
		if _, err := db.Exec("ALTER TABLE snapshots ADD COLUMN prev_hash TEXT DEFAULT ''"); err != nil {
			return err
		}
	}
	if !snapColumns["integrity_hash"] {
		if _, err := db.Exec("ALTER TABLE snapshots ADD COLUMN integrity_hash TEXT DEFAULT ''"); err != nil {
			return err
		}
	}
	if !snapColumns["sm2_signature"] {
		if _, err := db.Exec("ALTER TABLE snapshots ADD COLUMN sm2_signature TEXT DEFAULT ''"); err != nil {
			return err
		}
	}

	return nil
}
