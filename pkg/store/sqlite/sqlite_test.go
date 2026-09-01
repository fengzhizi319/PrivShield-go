// Package sqlite_test provides comprehensive unit and regression tests for SQLite store implementations.
// Package sqlite_test 为 SQLite 后端的 TaskStore、DataSourceStore 和 AuditStore 提供全量单元与回归测试套件。
//
// ==============================================================================
// 【测试模块与验证目标】
// 1. 【Open & ValidateIntegrity】：测试空路径、有效路径、静默 Logger、PRAGMA integrity_check 探活与数据库损坏检测；
// 2. 【TaskStore】：测试基础 CRUD、未找到报错、List 分页与 Status 过滤、Update 覆盖、Counts 状态聚合；
// 3. 【DataSourceStore】：测试数据源 CRUD、Delete 级联、Tags 序列化、AccessAudit 访问审计记录分页；
// 4. 【AuditStore】：测试审计日志 CRUD、多维度过滤、Snapshot 外键关联与查询、SaveLogsBatch 批量插入；
// 5. 【Hash Chain & Verify】：测试创世日志、前序链推进与 VerifyChain 防篡改对账核验；
// 6. 【LeasedTaskStore 桩实现】：验证 SQLite 模式下所有租约方法均严格返回 ErrLeaseNotSupported；
// 7. 【Legacy Schema Migration】：验证旧版 15 列旧表结构无缝热迁移、补充 canonical 列（api_code, datasource_id）并自动回填数据。
// ==============================================================================

package sqlite_test

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/store"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/sqlite"
)

// openTestDB 在临时目录创建临时 SQLite 数据库文件供单测使用。
func openTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "test.db")
}

// testLogger 返回测试专用的静默 Logger。
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// ─────────────────────────────────────────────────────────────
// 1. Open 数据库连接初始化测试
// ─────────────────────────────────────────────────────────────

func TestOpen_EmptyPath(t *testing.T) {
	db, err := sqlite.Open("", testLogger())
	if err != nil {
		t.Fatalf("expected nil error for empty path, got %v", err)
	}
	if db != nil {
		t.Fatal("expected nil db for empty path")
	}
}

func TestOpen_ValidPath(t *testing.T) {
	dbPath := openTestDB(t)
	db, err := sqlite.Open(dbPath, testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer db.Close()
	if db == nil {
		t.Fatal("expected non-nil db")
	}
}

func TestOpen_NilLogger(t *testing.T) {
	dbPath := openTestDB(t)
	db, err := sqlite.Open(dbPath, nil) // nil logger 不应 panic
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer db.Close()
}

// ─────────────────────────────────────────────────────────────
// 2. TaskStore 测试
// ─────────────────────────────────────────────────────────────

func setupTaskStore(t *testing.T) *sqlite.TaskStore {
	t.Helper()
	dbPath := openTestDB(t)
	db, err := sqlite.Open(dbPath, testLogger())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ts, err := sqlite.NewTaskStore(db)
	if err != nil {
		t.Fatalf("new task store: %v", err)
	}
	return ts
}

func TestTaskStore_SaveAndGet(t *testing.T) {
	ts := setupTaskStore(t)
	now := time.Now().Truncate(time.Millisecond)
	task := &store.Task{
		ID:        "task-1",
		Status:    "pending",
		Stage:     "queued",
		Source:    "卫健数据",
		Operation: "mask",
		Priority:  5,
		CreatedAt: now,
	}
	if err := ts.Save(task); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := ts.Get("task-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != "task-1" || got.Status != "pending" || got.Operation != "mask" {
		t.Fatalf("unexpected task: %+v", got)
	}
}

func TestTaskStore_GetNotFound(t *testing.T) {
	ts := setupTaskStore(t)
	_, err := ts.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestTaskStore_ListAndFilter(t *testing.T) {
	ts := setupTaskStore(t)
	now := time.Now()
	for i, status := range []string{"pending", "pending", "completed"} {
		ts.Save(&store.Task{
			ID:        fmt_id("task-%d", i),
			Status:    status,
			Stage:     "queued",
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		})
	}
	// 查询全部
	all, total, err := ts.List(store.TaskFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 || len(all) != 3 {
		t.Fatalf("expected 3 tasks, got %d (total=%d)", len(all), total)
	}
	// 状态过滤
	pending, pTotal, err := ts.List(store.TaskFilter{Status: "pending"})
	if err != nil {
		t.Fatalf("list filter: %v", err)
	}
	if pTotal != 2 || len(pending) != 2 {
		t.Fatalf("expected 2 pending tasks, got %d (total=%d)", len(pending), pTotal)
	}
}

func TestTaskStore_ListWithLimit(t *testing.T) {
	ts := setupTaskStore(t)
	now := time.Now()
	for i := 0; i < 5; i++ {
		ts.Save(&store.Task{
			ID:        fmt_id("task-%d", i),
			Status:    "pending",
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		})
	}
	tasks, total, err := ts.List(store.TaskFilter{Limit: 2})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 5 {
		t.Fatalf("expected total=5, got %d", total)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks with limit, got %d", len(tasks))
	}
}

func TestTaskStore_Update(t *testing.T) {
	ts := setupTaskStore(t)
	now := time.Now()
	ts.Save(&store.Task{ID: "task-u", Status: "pending", Stage: "queued", CreatedAt: now})
	started := now.Add(time.Second)
	task := &store.Task{
		ID:         "task-u",
		Status:     "running",
		Stage:      "processing",
		StartedAt:  &started,
		DurationMs: 0,
	}
	if err := ts.Update(task); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := ts.Get("task-u")
	if got.Status != "running" || got.Stage != "processing" || got.StartedAt == nil {
		t.Fatalf("update not applied: %+v", got)
	}
}

func TestTaskStore_Counts(t *testing.T) {
	ts := setupTaskStore(t)
	now := time.Now()
	statuses := []string{"pending", "pending", "running", "completed", "failed"}
	for i, s := range statuses {
		ts.Save(&store.Task{ID: fmt_id("c-%d-%s", i, s), Status: s, CreatedAt: now})
	}
	counts, err := ts.Counts()
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts.Pending != 2 || counts.Running != 1 || counts.Completed != 1 || counts.Failed != 1 {
		t.Fatalf("unexpected counts: %+v", counts)
	}
}

// ─────────────────────────────────────────────────────────────
// 3. DataSourceStore 测试
// ─────────────────────────────────────────────────────────────

func setupDSStore(t *testing.T) *sqlite.DataSourceStore {
	t.Helper()
	dbPath := openTestDB(t)
	db, err := sqlite.Open(dbPath, testLogger())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ds, err := sqlite.NewDataSourceStore(db)
	if err != nil {
		t.Fatalf("new datasource store: %v", err)
	}
	return ds
}

func TestDataSourceStore_SaveAndGet(t *testing.T) {
	ds := setupDSStore(t)
	now := time.Now()
	src := &store.DataSource{
		ID:            "ds-1",
		Name:          "卫健数据库",
		Type:          "database",
		Host:          "10.0.0.1",
		Port:          3306,
		Database:      "health",
		SecurityLevel: "high",
		Status:        "connected",
		CreatedAt:     now,
		Tags:          []string{"卫健", "高密"},
	}
	if err := ds.SaveDS(src); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := ds.GetDS("ds-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "卫健数据库" || got.SecurityLevel != "high" {
		t.Fatalf("unexpected ds: %+v", got)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "卫健" {
		t.Fatalf("unexpected tags: %v", got.Tags)
	}
}

func TestDataSourceStore_ListAndDelete(t *testing.T) {
	ds := setupDSStore(t)
	now := time.Now()
	ds.SaveDS(&store.DataSource{ID: "ds-a", Name: "A", CreatedAt: now})
	ds.SaveDS(&store.DataSource{ID: "ds-b", Name: "B", CreatedAt: now})
	list, _, err := ds.ListDS(store.DataSourceFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}
	if err := ds.DeleteDS("ds-a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, _, _ = ds.ListDS(store.DataSourceFilter{})
	if len(list) != 1 {
		t.Fatalf("expected 1 after delete, got %d", len(list))
	}
}

func TestDataSourceStore_DeleteNotFound(t *testing.T) {
	ds := setupDSStore(t)
	err := ds.DeleteDS("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent datasource")
	}
}

func TestDataSourceStore_Update(t *testing.T) {
	ds := setupDSStore(t)
	now := time.Now()
	ds.SaveDS(&store.DataSource{ID: "ds-u", Name: "Old", Status: "disconnected", CreatedAt: now})
	checkAt := now.Add(time.Minute)
	updated := &store.DataSource{
		ID:          "ds-u",
		Name:        "New",
		Status:      "connected",
		LastCheckAt: &checkAt,
		Tags:        []string{"updated"},
	}
	if err := ds.UpdateDS(updated); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := ds.GetDS("ds-u")
	if got.Name != "New" || got.Status != "connected" || got.LastCheckAt == nil {
		t.Fatalf("update not applied: %+v", got)
	}
}

func TestDataSourceStore_Audit(t *testing.T) {
	ds := setupDSStore(t)
	now := time.Now()
	rec := &store.AccessAuditRecord{
		ID:             "audit-1",
		DataSourceID:   "ds-1",
		DataSourceName: "卫健",
		Operation:      "query",
		User:           "admin",
		Timestamp:      now,
		RecordsCount:   100,
		Status:         "success",
	}
	if err := ds.SaveAudit(rec); err != nil {
		t.Fatalf("save audit: %v", err)
	}
	records, _, err := ds.ListAudit("ds-1", 0, 0)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(records) != 1 || records[0].RecordsCount != 100 {
		t.Fatalf("unexpected audit records: %+v", records)
	}
	// 查询其他数据源应返回空
	empty, _, err := ds.ListAudit("ds-other", 0, 0)
	if err != nil {
		t.Fatalf("list audit filter: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected 0 records, got %d", len(empty))
	}
}

// ─────────────────────────────────────────────────────────────
// 4. AuditStore 测试
// ─────────────────────────────────────────────────────────────

func setupAuditStore(t *testing.T) *sqlite.AuditStore {
	t.Helper()
	dbPath := openTestDB(t)
	db, err := sqlite.Open(dbPath, testLogger())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	as, err := sqlite.NewAuditStore(db)
	if err != nil {
		t.Fatalf("new audit store: %v", err)
	}
	return as
}

func TestAuditStore_SaveAndGetLog(t *testing.T) {
	as := setupAuditStore(t)
	now := time.Now()
	log := &store.AuditLog{
		ID:            "log-1",
		Timestamp:     now,
		Operation:     "mask",
		DataSource:    "卫健",
		InputHash:     "abc123",
		OutputHash:    "def456",
		Algorithm:     "field_mask",
		InputRows:     10,
		OutputRows:    10,
		DurationMs:    50,
		User:          "admin",
		Status:        "success",
		SecurityLevel: "L3",
	}
	if err := as.SaveLog(log); err != nil {
		t.Fatalf("save log: %v", err)
	}
	got, err := as.GetLog("log-1")
	if err != nil {
		t.Fatalf("get log: %v", err)
	}
	if got.Operation != "mask" || got.Algorithm != "field_mask" || got.SecurityLevel != "L3" {
		t.Fatalf("unexpected log: %+v", got)
	}
}

func TestAuditStore_ListLogsFilter(t *testing.T) {
	as := setupAuditStore(t)
	now := time.Now()
	for i, op := range []string{"mask", "mask", "dp"} {
		as.SaveLog(&store.AuditLog{
			ID:        fmt_id("log-%d", i),
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Operation: op,
			User:      "admin",
			Status:    "success",
		})
	}
	// 按操作过滤
	logs, total, err := as.ListLogs(store.AuditFilter{Operation: "mask"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 || len(logs) != 2 {
		t.Fatalf("expected 2 mask logs, got %d (total=%d)", len(logs), total)
	}
	// 按用户过滤
	logs2, total2, err := as.ListLogs(store.AuditFilter{User: "admin"})
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if total2 != 3 || len(logs2) != 3 {
		t.Fatalf("expected 3 admin logs, got %d (total=%d)", len(logs2), total2)
	}
	// 无过滤条件
	all, allTotal, _ := as.ListLogs(store.AuditFilter{})
	if allTotal != 3 || len(all) != 3 {
		t.Fatalf("expected 3 total, got %d", len(all))
	}
}

func TestAuditStore_ListLogsWithLimit(t *testing.T) {
	as := setupAuditStore(t)
	now := time.Now()
	for i := 0; i < 5; i++ {
		as.SaveLog(&store.AuditLog{
			ID:        fmt_id("log-l-%d", i),
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Operation: "mask",
		})
	}
	logs, total, err := as.ListLogs(store.AuditFilter{Limit: 2})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 5 {
		t.Fatalf("expected total=5, got %d", total)
	}
	if len(logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(logs))
	}
}

func TestAuditStore_Snapshots(t *testing.T) {
	as := setupAuditStore(t)
	now := time.Now()

	// P26 fix: 先创建父级审计日志记录以满足外键约束
	parentLog := &store.AuditLog{
		ID:            "log-1",
		Timestamp:     now,
		Operation:     "mask",
		DataSource:    "test",
		InputHash:     "input-hash",
		OutputHash:    "output-hash",
		Algorithm:     "field_mask",
		InputRows:     100,
		OutputRows:    100,
		DurationMs:    50,
		User:          "tester",
		Status:        "success",
		SecurityLevel: "L3",
	}
	if err := as.SaveLog(parentLog); err != nil {
		t.Fatalf("save parent log: %v", err)
	}

	snap := &store.SnapshotRecord{
		ID:            "snap-1",
		AuditLogID:    "log-1",
		Timestamp:     now,
		InputSample:   `{"name":"张三"}`,
		OutputSample:  `{"name":"张*"}`,
		Algorithm:     "field_mask",
		IntegrityHash: "sha256:abc",
	}
	if err := as.SaveSnapshot(snap); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	// 按 ID 查询
	got, err := as.GetSnapshot("snap-1")
	if err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	if got.Algorithm != "field_mask" || got.IntegrityHash != "sha256:abc" {
		t.Fatalf("unexpected snapshot: %+v", got)
	}
	// 列表查询
	snaps, total, err := as.ListSnapshots(10, 0)
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
	if total != 1 {
		t.Fatalf("expected total 1, got %d", total)
	}
}

func TestAuditStore_GetLogNotFound(t *testing.T) {
	as := setupAuditStore(t)
	_, err := as.GetLog("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent log")
	}
}

func fmt_id(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

// ─────────────────────────────────────────────────────────────
// 5. ValidateIntegrity 完整性探针测试
// ─────────────────────────────────────────────────────────────

func TestValidateIntegrity_EmptyPath(t *testing.T) {
	err := sqlite.ValidateIntegrity("")
	if err != nil {
		t.Fatalf("expected nil error for empty path, got %v", err)
	}
}

func TestValidateIntegrity_ValidDatabase(t *testing.T) {
	dbPath := openTestDB(t)
	db, err := sqlite.Open(dbPath, testLogger())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := sqlite.InitTaskTables(db); err != nil {
		t.Fatalf("init tables: %v", err)
	}
	db.Close()

	err = sqlite.ValidateIntegrity(dbPath)
	if err != nil {
		t.Fatalf("expected nil error for valid database, got %v", err)
	}
}

func TestValidateIntegrity_NonexistentPath(t *testing.T) {
	err := sqlite.ValidateIntegrity("/nonexistent/path/to/database.db")
	if err == nil {
		t.Fatal("expected error for nonexistent database path")
	}
}

func TestValidateIntegrity_CorruptedDatabase(t *testing.T) {
	dbPath := openTestDB(t)
	db, err := sqlite.Open(dbPath, testLogger())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := sqlite.InitTaskTables(db); err != nil {
		t.Fatalf("init tables: %v", err)
	}
	db.Close()

	// 写入乱码人工制造损坏
	f, err := os.OpenFile(dbPath, os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open for corruption: %v", err)
	}
	_, _ = f.WriteAt([]byte("CORRUPTED_DATA_GARBHERE"), 100)
	f.Close()

	err = sqlite.ValidateIntegrity(dbPath)
	if err == nil {
		t.Fatal("expected error for corrupted database")
	}
}

// ─────────────────────────────────────────────────────────────
// 6. Phase B: LeasedTaskStore 租约拒绝测试
// ─────────────────────────────────────────────────────────────

func TestLeasedTaskStore_ClaimNext_ReturnsNotSupported(t *testing.T) {
	ts := setupTaskStore(t)
	lease, err := ts.ClaimNext("hub-1", 60*time.Second)
	if err != store.ErrLeaseNotSupported {
		t.Fatalf("expected ErrLeaseNotSupported, got lease=%v err=%v", lease, err)
	}
}

func TestLeasedTaskStore_RenewLease_ReturnsNotSupported(t *testing.T) {
	ts := setupTaskStore(t)
	ok, err := ts.RenewLease("task-1", "hub-1", "token", 60*time.Second)
	if err != store.ErrLeaseNotSupported {
		t.Fatalf("expected ErrLeaseNotSupported, got ok=%v err=%v", ok, err)
	}
}

func TestLeasedTaskStore_CompleteLease_ReturnsNotSupported(t *testing.T) {
	ts := setupTaskStore(t)
	ok, err := ts.CompleteLease("task-1", "hub-1", "token", store.TaskResult{})
	if err != store.ErrLeaseNotSupported {
		t.Fatalf("expected ErrLeaseNotSupported, got ok=%v err=%v", ok, err)
	}
}

func TestLeasedTaskStore_FailLease_ReturnsNotSupported(t *testing.T) {
	ts := setupTaskStore(t)
	ok, err := ts.FailLease("task-1", "hub-1", "token", store.TaskFailure{Error: "test"})
	if err != store.ErrLeaseNotSupported {
		t.Fatalf("expected ErrLeaseNotSupported, got ok=%v err=%v", ok, err)
	}
}

func TestLeasedTaskStore_RequeueExpiredLeases_ReturnsNotSupported(t *testing.T) {
	ts := setupTaskStore(t)
	count, err := ts.RequeueExpiredLeases(10)
	if err != store.ErrLeaseNotSupported {
		t.Fatalf("expected ErrLeaseNotSupported, got count=%v err=%v", count, err)
	}
}

func TestLeasedTaskStore_InterfaceCompliance(t *testing.T) {
	ts := setupTaskStore(t)
	var _ store.LeasedTaskStore = ts
}

// ─────────────────────────────────────────────────────────────
// 7. Schema 迁移与 Canonical 标识回填回归测试
// ─────────────────────────────────────────────────────────────

func TestInitAuditTables_LegacyMigration(t *testing.T) {
	dbPath := openTestDB(t)
	db, err := sqlite.Open(dbPath, testLogger())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// 1. 人工创建 15 列的旧表
	_, err = db.Exec(`
		CREATE TABLE audit_logs (
			id TEXT PRIMARY KEY,
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
			security_level TEXT
		);
		CREATE TABLE snapshots (
			id TEXT PRIMARY KEY,
			audit_log_id TEXT,
			timestamp DATETIME NOT NULL,
			input_sample TEXT,
			output_sample TEXT,
			algorithm TEXT,
			parameters_json TEXT,
			integrity_hash TEXT,
			FOREIGN KEY(audit_log_id) REFERENCES audit_logs(id)
		);
	`)
	if err != nil {
		t.Fatalf("create legacy audit tables: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO audit_logs (id, timestamp, operation, datasource, user_name, status)
		VALUES ('legacy-1', '2026-08-20T10:00:00Z', 'mask', 'ds_yibao', 'tester', 'success')
	`)
	if err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	// 2. 调用生产 InitAuditTables 触发热升级迁移
	if err := sqlite.InitAuditTables(db); err != nil {
		t.Fatalf("InitAuditTables failed on legacy db: %v", err)
	}

	// 3. 验证新列正常读写
	as, err := sqlite.NewAuditStore(db)
	if err != nil {
		t.Fatalf("new audit store: %v", err)
	}

	now := time.Now()
	newLog := &store.AuditLog{
		ID:           "migrated-2",
		TaskID:       "task-123",
		APICode:      "api1_yibao",
		DatasourceID: "ds_yibao",
		Timestamp:    now,
		Operation:    "mask",
		DataSource:   "ds_yibao",
		Status:       "success",
	}
	if err := as.SaveLog(newLog); err != nil {
		t.Fatalf("save new log on migrated db: %v", err)
	}

	got, err := as.GetLog("migrated-2")
	if err != nil {
		t.Fatalf("get migrated log: %v", err)
	}
	if got.TaskID != "task-123" || got.APICode != "api1_yibao" || got.DatasourceID != "ds_yibao" {
		t.Fatalf("canonical fields not stored: %+v", got)
	}

	filtered, total, err := as.ListLogs(store.AuditFilter{TaskID: "task-123", DatasourceID: "ds_yibao"})
	if err != nil {
		t.Fatalf("filter logs: %v", err)
	}
	if total != 1 || len(filtered) != 1 {
		t.Fatalf("expected 1 filtered log, got %d (total=%d)", len(filtered), total)
	}
}

func TestInitTaskTables_LegacyMigration(t *testing.T) {
	dbPath := openTestDB(t)
	db, err := sqlite.Open(dbPath, testLogger())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// 1. 人工创建旧版 tasks 表
	_, err = db.Exec(`
		CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL DEFAULT 'pending',
			stage TEXT NOT NULL DEFAULT 'queued',
			source TEXT,
			operation TEXT,
			priority INTEGER DEFAULT 0,
			created_at DATETIME NOT NULL,
			started_at DATETIME,
			completed_at DATETIME,
			duration_ms INTEGER DEFAULT 0,
			error TEXT,
			payload_json TEXT
		);
	`)
	if err != nil {
		t.Fatalf("create legacy tasks table: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO tasks (id, status, stage, source, operation, created_at)
		VALUES ('legacy-task-1', 'pending', 'queued', 'ds_yibao', 'mask', '2026-08-20T10:00:00Z')
	`)
	if err != nil {
		t.Fatalf("insert legacy task: %v", err)
	}

	// 2. 调用生产 InitTaskTables 触发迁移与回填
	if err := sqlite.InitTaskTables(db); err != nil {
		t.Fatalf("InitTaskTables failed on legacy db: %v", err)
	}

	ts, err := sqlite.NewTaskStore(db)
	if err != nil {
		t.Fatalf("new task store: %v", err)
	}

	// 验证回填字段
	task, err := ts.Get("legacy-task-1")
	if err != nil {
		t.Fatalf("get legacy task: %v", err)
	}
	if task.DatasourceID != "ds_yibao" {
		t.Fatalf("expected backfilled DatasourceID 'ds_yibao', got %q", task.DatasourceID)
	}
	if task.APICode != "api1_yibao" {
		t.Fatalf("expected backfilled APICode 'api1_yibao', got %q", task.APICode)
	}

	// 插入带规范字段的新任务
	now := time.Now()
	newTask := &store.Task{
		ID:           "new-task-2",
		Status:       "running",
		Stage:        "processing",
		Source:       "ds_kangyang",
		APICode:      "api2_kangyang",
		DatasourceID: "ds_kangyang",
		Operation:    "k_anon",
		CreatedAt:    now,
	}
	if err := ts.Save(newTask); err != nil {
		t.Fatalf("save new task: %v", err)
	}
	gotNew, err := ts.Get("new-task-2")
	if err != nil {
		t.Fatalf("get new task: %v", err)
	}
	if gotNew.DatasourceID != "ds_kangyang" || gotNew.APICode != "api2_kangyang" {
		t.Fatalf("canonical fields not preserved on get: %+v", gotNew)
	}
}

// ─────────────────────────────────────────────────────────────
// 8. 哈希链与批量写入测试
// ─────────────────────────────────────────────────────────────

func TestAuditStore_HashChainAndVerify(t *testing.T) {
	as := setupAuditStore(t)

	// 1. 创世日志
	t1 := time.Now().Add(-2 * time.Minute)
	log1 := &store.AuditLog{
		ID:            "chain-1",
		Timestamp:     t1,
		Operation:     "mask",
		DataSource:    "ds_yibao",
		DatasourceID:  "ds_yibao",
		InputHash:     "hash-in-1",
		OutputHash:    "hash-out-1",
		Algorithm:     "mask",
		User:          "alice",
		Status:        "success",
		SecurityLevel: "L3",
		PrevHash:      "",
	}
	if err := as.SaveLog(log1); err != nil {
		t.Fatalf("save log 1: %v", err)
	}

	latest, err := as.GetLatestLog()
	if err != nil || latest == nil {
		t.Fatalf("get latest log: %v", err)
	}
	if latest.IntegrityHash == "" {
		t.Fatal("expected non-empty integrity_hash for genesis log")
	}

	// 2. 连续第二条日志
	t2 := time.Now().Add(-1 * time.Minute)
	log2 := &store.AuditLog{
		ID:            "chain-2",
		Timestamp:     t2,
		Operation:     "dp",
		DataSource:    "ds_yibao",
		DatasourceID:  "ds_yibao",
		InputHash:     "hash-in-2",
		OutputHash:    "hash-out-2",
		Algorithm:     "dp_laplace",
		User:          "bob",
		Status:        "success",
		SecurityLevel: "L4",
		PrevHash:      latest.IntegrityHash,
	}
	if err := as.SaveLog(log2); err != nil {
		t.Fatalf("save log 2: %v", err)
	}

	// 3. 对账核验
	res, err := as.VerifyChain(10)
	if err != nil {
		t.Fatalf("verify chain error: %v", err)
	}
	if !res.Valid || res.TotalVerified != 2 {
		t.Fatalf("expected valid chain with 2 logs, got valid=%v, count=%d, msg=%s", res.Valid, res.TotalVerified, res.Message)
	}
}

func TestAuditStore_BatchSave(t *testing.T) {
	as := setupAuditStore(t)

	now := time.Now()
	logs := []store.AuditLog{
		{ID: "batch-1", Timestamp: now, Operation: "mask", DataSource: "ds_kangyang", Status: "success"},
		{ID: "batch-2", Timestamp: now.Add(time.Second), Operation: "k_anon", DataSource: "ds_kangyang", Status: "success"},
	}
	snaps := []store.SnapshotRecord{
		{ID: "snap-b-1", AuditLogID: "batch-1", Timestamp: now, InputSample: "s1", OutputSample: "s2"},
	}

	if err := as.SaveLogsBatch(logs, snaps); err != nil {
		t.Fatalf("batch save: %v", err)
	}

	l1, err := as.GetLog("batch-1")
	if err != nil || l1 == nil {
		t.Fatalf("get batch-1: %v", err)
	}
	snap, err := as.GetSnapshot("snap-b-1")
	if err != nil || snap == nil {
		t.Fatalf("get snapshot snap-b-1: %v", err)
	}
}

// TestAuditStore_FetchOldestForArchiveAndDeleteByIDs 验证「先归档后删除」所需的存储能力：
// 到期日志按链序（旧→新）返回、带齐关联快照，且按 ID 删除会级联清掉快照。
func TestAuditStore_FetchOldestForArchiveAndDeleteByIDs(t *testing.T) {
	as := setupAuditStore(t)
	base := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 4; i++ {
		id := string(rune('a' + i))
		l := &store.AuditLog{
			ID: "log-" + id, Timestamp: base.Add(time.Duration(i) * time.Hour),
			Operation: "mask", DataSource: "ds_yibao", DatasourceID: "ds_yibao",
			InputHash: "in-" + id, OutputHash: "out-" + id, Algorithm: "SM4-GCM",
			ParametersJSON: `{"fields":["phone"]}`, User: "tester", Status: "success", SecurityLevel: "L4",
		}
		if err := as.SaveLogWithSnapshot(l, &store.SnapshotRecord{
			ID: "snap-" + id, AuditLogID: l.ID, Timestamp: l.Timestamp,
			InputSample: "enc:v2:raw", OutputSample: "enc:v2:masked", Algorithm: "SM4-GCM",
			ParametersJSON: `{"fields":["phone"]}`,
		}); err != nil {
			t.Fatalf("save log %d: %v", i, err)
		}
	}

	var reader store.AuditArchiveReader = as
	logs, snaps, err := reader.FetchOldestForArchive(base.Add(2*time.Hour), 2)
	if err != nil {
		t.Fatalf("fetch oldest: %v", err)
	}
	if len(logs) != 2 || len(snaps) != 2 {
		t.Fatalf("expected a page of 2 logs + 2 snapshots, got %d/%d", len(logs), len(snaps))
	}
	if logs[0].ID != "log-a" || logs[1].ID != "log-b" {
		t.Fatalf("expected oldest-first chain order, got %s,%s", logs[0].ID, logs[1].ID)
	}
	if logs[0].ParametersJSON == "" {
		t.Fatal("archived log must carry parameters_json for independent hash re-computation")
	}

	deleted, err := reader.DeleteLogsByIDs([]string{logs[0].ID, logs[1].ID})
	if err != nil {
		t.Fatalf("delete by ids: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 logs deleted, got %d", deleted)
	}
	remaining, total, err := as.ListLogs(store.AuditFilter{Limit: 100})
	if err != nil || total != 2 || len(remaining) != 2 {
		t.Fatalf("expected 2 surviving logs, got %d/%d err=%v", len(remaining), total, err)
	}
	if _, sn, err := reader.FetchOldestForArchive(base.Add(100*time.Hour), 100); err != nil || len(sn) != 2 {
		t.Fatalf("snapshots of deleted logs must be gone, got %d err=%v", len(sn), err)
	}
	if got, err := reader.DeleteLogsByIDs(nil); err != nil || got != 0 {
		t.Fatalf("empty id list must be a no-op, got %d err=%v", got, err)
	}
}
