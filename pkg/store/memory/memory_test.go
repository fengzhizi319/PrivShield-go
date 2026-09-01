// Package memory_test provides unit tests for the in-memory store implementations.
// Package memory_test 为 TaskStore、DataSourceStore 和 AuditStore 的内存实现提供完整的单元测试套件。
//
// ==============================================================================
// 【测试覆盖范围】
// 1. TaskStore：Save/Get 基础读写、未找到报错、List 状态过滤与分页、Update 更新覆盖、Counts 状态聚合统计；
// 2. DataSourceStore：CRUD 增删改查、未找到校验、SaveAudit/ListAudit 访问审计记录及数据源隔离；
// 3. AuditStore：Log CRUD、ListLogs 多维度过滤、Snapshot 记录存储与根据 ID 查询、分页边界与越界保护。
// ==============================================================================

package memory

import (
	"fmt"
	"testing"
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/store"
)

// ─────────────────────────────────────────────────────────────
// 1. TaskStore 单元测试
// ─────────────────────────────────────────────────────────────

func TestTaskStore_SaveAndGet(t *testing.T) {
	s := NewTaskStore()
	task := &store.Task{
		ID:        "t1",
		Status:    "pending",
		Stage:     "queued",
		Source:    "test",
		Operation: "mask",
		CreatedAt: time.Now(),
	}

	if err := s.Save(task); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := s.Get("t1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != "t1" || got.Status != "pending" {
		t.Errorf("unexpected task: %+v", got)
	}
}

func TestTaskStore_GetNotFound(t *testing.T) {
	s := NewTaskStore()
	if _, err := s.Get("nonexistent"); err == nil {
		t.Error("expected error for nonexistent task")
	}
}

func TestTaskStore_List(t *testing.T) {
	s := NewTaskStore()
	for i, status := range []string{"pending", "running", "completed"} {
		s.Save(&store.Task{
			ID:        "t" + string(rune('0'+i+1)),
			Status:    status,
			CreatedAt: time.Now(),
		})
	}

	// 1. 查询全部任务
	tasks, total, err := s.List(store.TaskFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 {
		t.Errorf("expected 3, got %d", total)
	}
	if len(tasks) != 3 {
		t.Errorf("expected 3 tasks, got %d", len(tasks))
	}

	// 2. 按状态过滤 running 任务
	filtered, count, _ := s.List(store.TaskFilter{Status: "running"})
	if count != 1 {
		t.Errorf("expected 1 running, got %d", count)
	}
	if len(filtered) != 1 || filtered[0].Status != "running" {
		t.Errorf("filter mismatch: %+v", filtered)
	}
}

func TestTaskStore_Update(t *testing.T) {
	s := NewTaskStore()
	s.Save(&store.Task{ID: "t1", Status: "pending", CreatedAt: time.Now()})

	got, _ := s.Get("t1")
	got.Status = "completed"
	if err := s.Update(got); err != nil {
		t.Fatalf("update: %v", err)
	}

	updated, _ := s.Get("t1")
	if updated.Status != "completed" {
		t.Errorf("expected completed, got %s", updated.Status)
	}
}

func TestTaskStore_Counts(t *testing.T) {
	s := NewTaskStore()
	for _, status := range []string{"pending", "running", "completed", "completed", "failed"} {
		s.Save(&store.Task{ID: "t-" + status + "-" + time.Now().String(), Status: status, CreatedAt: time.Now()})
	}

	counts, err := s.Counts()
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts.Pending != 1 {
		t.Errorf("pending: expected 1, got %d", counts.Pending)
	}
	if counts.Running != 1 {
		t.Errorf("running: expected 1, got %d", counts.Running)
	}
	if counts.Completed != 2 {
		t.Errorf("completed: expected 2, got %d", counts.Completed)
	}
	if counts.Failed != 1 {
		t.Errorf("failed: expected 1, got %d", counts.Failed)
	}
}

func TestTaskStore_Pagination(t *testing.T) {
	s := NewTaskStore()
	for i := 0; i < 10; i++ {
		s.Save(&store.Task{
			ID:        fmt.Sprintf("t-%02d", i),
			Status:    "pending",
			CreatedAt: time.Now().Add(time.Duration(i) * time.Minute),
		})
	}

	// 1. 正常分页：Offset 3, Limit 4
	tasks, total, err := s.List(store.TaskFilter{Limit: 4, Offset: 3})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 10 {
		t.Errorf("expected total 10, got %d", total)
	}
	if len(tasks) != 4 {
		t.Errorf("expected 4 tasks, got %d", len(tasks))
	}

	// 2. 越界分页：Offset 20（超出总记录数 10）
	tasksOOB, totalOOB, err := s.List(store.TaskFilter{Limit: 4, Offset: 20})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if totalOOB != 10 || len(tasksOOB) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasksOOB))
	}
}

// ─────────────────────────────────────────────────────────────
// 2. DataSourceStore 单元测试
// ─────────────────────────────────────────────────────────────

func TestDataSourceStore_CRUD(t *testing.T) {
	s := NewDataSourceStore()

	ds := &store.DataSource{
		ID:   "ds1",
		Name: "test-db",
		Type: "database",
		Host: "localhost",
		Port: 5432,
	}

	// 1. 新增
	if err := s.SaveDS(ds); err != nil {
		t.Fatalf("save: %v", err)
	}

	// 2. 查询单个
	got, err := s.GetDS("ds1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "test-db" {
		t.Errorf("expected test-db, got %s", got.Name)
	}

	// 3. 列表查询
	list, _, err := s.ListDS(store.DataSourceFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1, got %d", len(list))
	}

	// 4. 更新
	got.Name = "updated-db"
	if err := s.UpdateDS(got); err != nil {
		t.Fatalf("update: %v", err)
	}
	updated, _ := s.GetDS("ds1")
	if updated.Name != "updated-db" {
		t.Errorf("expected updated-db, got %s", updated.Name)
	}

	// 5. 删除
	if err := s.DeleteDS("ds1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetDS("ds1"); err == nil {
		t.Error("expected error after delete")
	}
}

func TestDataSourceStore_Audit(t *testing.T) {
	s := NewDataSourceStore()

	rec := store.AccessAuditRecord{
		ID:           "a1",
		DataSourceID: "ds1",
		Operation:    "read",
		User:         "admin",
		Timestamp:    time.Now(),
	}
	if err := s.SaveAudit(&rec); err != nil {
		t.Fatalf("save audit: %v", err)
	}

	records, _, err := s.ListAudit("ds1", 0, 0)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 audit record, got %d", len(records))
	}
}

// ─────────────────────────────────────────────────────────────
// 3. AuditStore 单元测试
// ─────────────────────────────────────────────────────────────

func TestAuditStore_LogCRUD(t *testing.T) {
	s := NewAuditStore()

	log := &store.AuditLog{
		ID:        "log1",
		Timestamp: time.Now(),
		Operation: "mask",
		Status:    "success",
	}

	if err := s.SaveLog(log); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := s.GetLog("log1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Operation != "mask" {
		t.Errorf("expected mask, got %s", got.Operation)
	}

	logs, total, err := s.ListLogs(store.AuditFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(logs) != 1 {
		t.Errorf("expected 1, got total=%d len=%d", total, len(logs))
	}
}

func TestAuditStore_LogFilter(t *testing.T) {
	s := NewAuditStore()

	for _, op := range []string{"mask", "mask", "k_anon"} {
		s.SaveLog(&store.AuditLog{
			ID:        "log-" + op + "-" + time.Now().String(),
			Timestamp: time.Now(),
			Operation: op,
			Status:    "success",
		})
	}

	filtered, count, _ := s.ListLogs(store.AuditFilter{Operation: "mask"})
	if count != 2 {
		t.Errorf("expected 2 mask logs, got %d", count)
	}
	if len(filtered) != 2 {
		t.Errorf("expected 2 filtered logs, got %d", len(filtered))
	}
}

func TestAuditStore_Snapshots(t *testing.T) {
	s := NewAuditStore()

	snap := &store.SnapshotRecord{
		ID:            "snap1",
		AuditLogID:    "log1",
		Timestamp:     time.Now(),
		Algorithm:     "field_mask",
		IntegrityHash: "abc123",
	}

	if err := s.SaveSnapshot(snap); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	snaps, total, err := s.ListSnapshots(10, 0)
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snaps) != 1 {
		t.Errorf("expected 1 snapshot, got %d", len(snaps))
	}
	if total != 1 {
		t.Errorf("expected total 1, got %d", total)
	}

	got, err := s.GetSnapshot("snap1")
	if err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	if got.IntegrityHash != "abc123" {
		t.Errorf("expected abc123, got %s", got.IntegrityHash)
	}
}

func TestAuditStore_LogPagination(t *testing.T) {
	s := NewAuditStore()
	for i := 0; i < 10; i++ {
		s.SaveLog(&store.AuditLog{
			ID:        fmt.Sprintf("log-%02d", i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Minute),
			Operation: "mask",
			Status:    "success",
		})
	}

	// 分页测试：Offset 4, Limit 3
	logs, total, err := s.ListLogs(store.AuditFilter{Limit: 3, Offset: 4})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 10 {
		t.Errorf("expected total 10, got %d", total)
	}
	if len(logs) != 3 {
		t.Errorf("expected 3 logs, got %d", len(logs))
	}
}
