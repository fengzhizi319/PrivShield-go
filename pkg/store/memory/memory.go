// Package memory provides in-memory fallback implementations of the store interfaces.
// Package memory 为 store 接口提供基于纯内存的存储实现，用于本地快速开发与集成测试场景。
//
// ==============================================================================
// 【适用场景与安全设计】
// 1. 【降级回退】：当环境变量中未配置 DB_PATH 或数据库连接串时，各微服务自动降级回退至此内存实现；
// 2. 【进程隔离与生命周期】：进程重启后数据即刻丢失，生产环境严禁使用，应使用 SQLite 或 PostgreSQL；
// 3. 【并发安全】：所有内存读写均通过 sync.RWMutex 读写互斥锁保护，保证多协程并发安全；
// 4. 【排序性能 (P46 fix)】：全量 List 查询统一采用 Go 标准库 sort.Slice（O(N log N) 快速排序），避免 O(N^2) 冒泡排序；
// 5. 【内存有界保护 (P60 fix)】：通过硬性上限常量（maxAuditRecords = 10,000, maxAuditLogs = 50,000, maxSnapshots = 50,000）
//    在超限时自动丢弃最旧数据，彻底防止测试与长时间运行过程中的内存无界膨胀与 OOM；
// 6. 【租约桩实现】：LeasedTaskStore 接口各租约方法均返回 ErrLeaseNotSupported，明确标识内存模式不支持跨副本原子租约。
// ==============================================================================

package memory

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/store"
)

// ─────────────────────────────────────────────────────────────
// 1. TaskStore / 任务流水线内存存储
// ─────────────────────────────────────────────────────────────

// TaskStore implements store.TaskStore backed by an in-memory map.
// TaskStore 基于 Go 原生 map + sync.RWMutex 实现任务持久化接口。
type TaskStore struct {
	mu    sync.RWMutex
	tasks map[string]*store.Task
}

// NewTaskStore creates a new in-memory task store.
// NewTaskStore 初始化并返回一个空的任务内存存储实例。
func NewTaskStore() *TaskStore {
	return &TaskStore{tasks: make(map[string]*store.Task)}
}

// Save inserts or updates a task in memory.
//
// 执行逻辑：
// 1. 获取写锁 mu.Lock()；
// 2. 执行浅拷贝副本存储，避免外部在并发中修改入参指针导致数据竞态；
// 3. 存入 tasks 映射表。
func (s *TaskStore) Save(task *store.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *task
	s.tasks[task.ID] = &cp
	return nil
}

// Get retrieves a task by ID from memory.
//
// 执行逻辑：
// 1. 获取读锁 mu.RLock()；
// 2. 查找对应 ID，若不存在返回未找到错误；
// 3. 返回任务实体的浅拷贝副本。
func (s *TaskStore) Get(id string) (*store.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	if !ok {
		return nil, fmt.Errorf("task %s not found", id)
	}
	cp := *t
	return &cp, nil
}

// List returns filtered and paginated tasks ordered by CreatedAt descending.
//
// 执行逻辑：
// 1. 获取读锁 mu.RLock()；
// 2. 遍历所有任务，匹配 Status 过滤条件并追加到临时切片；
// 3. 使用 sort.Slice 按 CreatedAt 降序排列；
// 4. 应用 Offset 与 Limit 进行内存切片截断分页并返回。
func (s *TaskStore) List(filter store.TaskFilter) ([]store.Task, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	all := make([]store.Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		if filter.Status != "" && t.Status != filter.Status {
			continue
		}
		all = append(all, *t)
	}
	total := len(all)

	// P46 fix: use sort.Slice (O(n log n)) instead of bubble sort (O(n²))
	sort.Slice(all, func(i, j int) bool {
		return all[j].CreatedAt.Before(all[i].CreatedAt)
	})

	// 应用内存分页
	start := filter.Offset
	if start > len(all) {
		start = len(all)
	}
	if filter.Limit > 0 {
		end := start + filter.Limit
		if end > len(all) {
			end = len(all)
		}
		all = all[start:end]
	} else if start > 0 {
		all = all[start:]
	}
	return all, total, nil
}

// Update modifies an existing task in memory.
//
// 执行逻辑：
// 1. 获取写锁 mu.Lock()；
// 2. 检查任务是否存在，若不存在返回错误；
// 3. 存入新副本覆盖原任务。
func (s *TaskStore) Update(task *store.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[task.ID]; !ok {
		return fmt.Errorf("task %s not found", task.ID)
	}
	cp := *task
	s.tasks[task.ID] = &cp
	return nil
}

// Counts returns aggregated task counts by status.
//
// 执行逻辑：
// 1. 获取读锁 mu.RLock()；
// 2. 遍历所有任务累加 pending/running/completed/failed 计数。
func (s *TaskStore) Counts() (store.TaskCounts, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var c store.TaskCounts
	for _, t := range s.tasks {
		switch t.Status {
		case "pending":
			c.Pending++
		case "running":
			c.Running++
		case "completed":
			c.Completed++
		case "failed":
			c.Failed++
		}
	}
	return c, nil
}

// CleanupOld deletes terminal (completed/failed) tasks older than the cutoff time.
// CleanupOld 删除早于截止时间的终态任务，防止内存无限增长。
func (s *TaskStore) CleanupOld(before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int64
	for id, t := range s.tasks {
		if t.CreatedAt.Before(before) && (t.Status == "completed" || t.Status == "failed") {
			delete(s.tasks, id)
			count++
		}
	}
	return count, nil
}

// ── Phase B: LeasedTaskStore 桩实现（内存模式不支持租约） ──

// ClaimNext is not supported on in-memory store; returns ErrLeaseNotSupported.
func (s *TaskStore) ClaimNext(owner string, leaseTTL time.Duration) (*store.TaskLease, error) {
	return nil, store.ErrLeaseNotSupported
}

// RenewLease is not supported on in-memory store; returns ErrLeaseNotSupported.
func (s *TaskStore) RenewLease(id, owner, token string, leaseTTL time.Duration) (bool, error) {
	return false, store.ErrLeaseNotSupported
}

// CompleteLease is not supported on in-memory store; returns ErrLeaseNotSupported.
func (s *TaskStore) CompleteLease(id, owner, token string, result store.TaskResult) (bool, error) {
	return false, store.ErrLeaseNotSupported
}

// FailLease is not supported on in-memory store; returns ErrLeaseNotSupported.
func (s *TaskStore) FailLease(id, owner, token string, failure store.TaskFailure) (bool, error) {
	return false, store.ErrLeaseNotSupported
}

// RequeueExpiredLeases is not supported on in-memory store; returns ErrLeaseNotSupported.
func (s *TaskStore) RequeueExpiredLeases(limit int) (int, error) {
	return 0, store.ErrLeaseNotSupported
}

// ─────────────────────────────────────────────────────────────
// 2. DataSourceStore / 数据源内存存储
// ─────────────────────────────────────────────────────────────

// DataSourceStore implements store.DataSourceStore backed by an in-memory map.
type DataSourceStore struct {
	mu           sync.RWMutex
	dsMap        map[string]*store.DataSource
	auditRecords []store.AccessAuditRecord
}

// maxAuditRecords is the maximum number of access audit records retained in memory.
// P60 fix: 内存访问审计记录上限设为 10,000 条，超出自动淘汰最旧记录。
const maxAuditRecords = 10_000

// signAuditLog 为审计记录生成 SM2 签名（若已配置签名器）。
func signAuditLog(log *store.AuditLog) {
	if log.IntegrityHash != "" && log.SM2Signature == "" {
		log.SM2Signature = store.SignAuditRecord(log.IntegrityHash)
	}
}

// signSnapshot 为快照记录生成 SM2 签名（若已配置签名器）。
func signSnapshot(snap *store.SnapshotRecord) {
	if snap != nil && snap.IntegrityHash != "" && snap.SM2Signature == "" {
		snap.SM2Signature = store.SignAuditRecord(snap.IntegrityHash)
	}
}

// NewDataSourceStore creates a new in-memory data source store.
func NewDataSourceStore() *DataSourceStore {
	return &DataSourceStore{
		dsMap:        make(map[string]*store.DataSource),
		auditRecords: make([]store.AccessAuditRecord, 0),
	}
}

// SaveDS stores a data source in memory.
func (s *DataSourceStore) SaveDS(ds *store.DataSource) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *ds
	s.dsMap[ds.ID] = &cp
	return nil
}

// GetDS retrieves a data source by ID from memory.
func (s *DataSourceStore) GetDS(id string) (*store.DataSource, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ds, ok := s.dsMap[id]
	if !ok {
		return nil, fmt.Errorf("data source %s not found", id)
	}
	cp := *ds
	return &cp, nil
}

// ListDS returns data sources ordered by CreatedAt descending.
func (s *DataSourceStore) ListDS(filter store.DataSourceFilter) ([]store.DataSource, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]store.DataSource, 0, len(s.dsMap))
	for _, ds := range s.dsMap {
		result = append(result, *ds)
	}
	total := len(result)

	sort.Slice(result, func(i, j int) bool {
		return result[j].CreatedAt.Before(result[i].CreatedAt)
	})

	if filter.Limit > 0 {
		start := filter.Offset
		if start > len(result) {
			start = len(result)
		}
		end := start + filter.Limit
		if end > len(result) {
			end = len(result)
		}
		result = result[start:end]
	}
	return result, total, nil
}

// DeleteDS removes a data source from memory.
func (s *DataSourceStore) DeleteDS(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.dsMap[id]; !ok {
		return fmt.Errorf("data source %s not found", id)
	}
	delete(s.dsMap, id)
	return nil
}

// UpdateDS updates a data source in memory.
func (s *DataSourceStore) UpdateDS(ds *store.DataSource) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.dsMap[ds.ID]; !ok {
		return fmt.Errorf("data source %s not found", ds.ID)
	}
	cp := *ds
	s.dsMap[ds.ID] = &cp
	return nil
}

// SaveAudit records an access audit log with capacity bounds.
func (s *DataSourceStore) SaveAudit(rec *store.AccessAuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *rec
	s.auditRecords = append(s.auditRecords, cp)
	if len(s.auditRecords) > maxAuditRecords {
		s.auditRecords = s.auditRecords[len(s.auditRecords)-maxAuditRecords:]
	}
	return nil
}

// ListAudit returns filtered audit records by data source ID.
func (s *DataSourceStore) ListAudit(dsID string, limit, offset int) ([]store.AccessAuditRecord, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	filtered := make([]store.AccessAuditRecord, 0)
	for _, r := range s.auditRecords {
		if dsID != "" && r.DataSourceID != dsID {
			continue
		}
		filtered = append(filtered, r)
	}
	total := len(filtered)

	if limit > 0 {
		start := offset
		if start > len(filtered) {
			start = len(filtered)
		}
		end := start + limit
		if end > len(filtered) {
			end = len(filtered)
		}
		filtered = filtered[start:end]
	}
	return filtered, total, nil
}

// ─────────────────────────────────────────────────────────────
// 3. AuditStore / 审计日志内存存储
// ─────────────────────────────────────────────────────────────

// AuditStore implements store.AuditStore backed by in-memory slices.
type AuditStore struct {
	mu        sync.RWMutex
	logs      []store.AuditLog
	snapshots []store.SnapshotRecord
}

// maxAuditLogs is the maximum number of audit logs retained in memory.
// P60 fix: 内存审计日志上限设为 50,000 条。
const maxAuditLogs = 50_000

// maxSnapshots is the maximum number of snapshots retained in memory.
// P60 fix: 内存快照上限设为 50,000 条。
const maxSnapshots = 50_000

// NewAuditStore creates a new in-memory audit store.
func NewAuditStore() *AuditStore {
	return &AuditStore{
		logs:      make([]store.AuditLog, 0),
		snapshots: make([]store.SnapshotRecord, 0),
	}
}

// SaveLog saves an audit log and calculates its hash chain in memory.
func (s *AuditStore) SaveLog(log *store.AuditLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if log.PrevHash == "" && len(s.logs) > 0 {
		log.PrevHash = s.logs[len(s.logs)-1].IntegrityHash
	}
	if log.IntegrityHash == "" {
		log.IntegrityHash = store.ComputeAuditIntegrityHash(log.ID, log.PrevHash, log.Timestamp, log.Algorithm, log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON)
	}
	signAuditLog(log)

	cp := *log
	s.logs = append(s.logs, cp)
	if len(s.logs) > maxAuditLogs {
		s.logs = s.logs[len(s.logs)-maxAuditLogs:]
	}
	return nil
}

// SaveLogWithSnapshot stores an audit log and its snapshot atomically.
func (s *AuditStore) SaveLogWithSnapshot(log *store.AuditLog, snapshot *store.SnapshotRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if log.PrevHash == "" && len(s.logs) > 0 {
		log.PrevHash = s.logs[len(s.logs)-1].IntegrityHash
	}
	if log.IntegrityHash == "" {
		log.IntegrityHash = store.ComputeAuditIntegrityHash(log.ID, log.PrevHash, log.Timestamp, log.Algorithm, log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON)
	}
	signAuditLog(log)
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
		signSnapshot(snapshot)
	}

	logCopy := *log
	s.logs = append(s.logs, logCopy)
	if snapshot != nil {
		snapshotCopy := *snapshot
		s.snapshots = append(s.snapshots, snapshotCopy)
	}
	if len(s.logs) > maxAuditLogs {
		s.logs = s.logs[len(s.logs)-maxAuditLogs:]
	}
	if len(s.snapshots) > maxSnapshots {
		s.snapshots = s.snapshots[len(s.snapshots)-maxSnapshots:]
	}
	return nil
}

// SaveLogsBatch saves multiple logs and snapshots in memory.
func (s *AuditStore) SaveLogsBatch(logs []store.AuditLog, snapshots []store.SnapshotRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range logs {
		signAuditLog(&logs[i])
		s.logs = append(s.logs, logs[i])
	}
	for i := range snapshots {
		signSnapshot(&snapshots[i])
		s.snapshots = append(s.snapshots, snapshots[i])
	}

	if len(s.logs) > maxAuditLogs {
		s.logs = s.logs[len(s.logs)-maxAuditLogs:]
	}
	if len(s.snapshots) > maxSnapshots {
		s.snapshots = s.snapshots[len(s.snapshots)-maxSnapshots:]
	}
	return nil
}

// GetLog retrieves an audit log by ID.
func (s *AuditStore) GetLog(id string) (*store.AuditLog, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, l := range s.logs {
		if l.ID == id {
			cp := l
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("audit log %s not found", id)
}

// GetLatestLog returns the most recently written audit log.
func (s *AuditStore) GetLatestLog() (*store.AuditLog, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.logs) == 0 {
		return nil, nil
	}
	cp := s.logs[len(s.logs)-1]
	return &cp, nil
}

// ListLogs returns filtered and paginated audit logs.
func (s *AuditStore) ListLogs(filter store.AuditFilter) ([]store.AuditLog, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filtered := make([]store.AuditLog, 0)
	for _, l := range s.logs {
		if filter.TaskID != "" && l.TaskID != filter.TaskID {
			continue
		}
		if filter.APICode != "" && l.APICode != filter.APICode {
			continue
		}
		if filter.DatasourceID != "" && l.DatasourceID != filter.DatasourceID && l.DataSource != filter.DatasourceID {
			continue
		}
		if filter.DataSource != "" && l.DataSource != filter.DataSource && l.DatasourceID != filter.DataSource {
			continue
		}
		if filter.Operation != "" && l.Operation != filter.Operation {
			continue
		}
		if filter.User != "" && l.User != filter.User {
			continue
		}
		if filter.Status != "" && l.Status != filter.Status {
			continue
		}
		if filter.SecurityLevel != "" && l.SecurityLevel != filter.SecurityLevel {
			continue
		}
		filtered = append(filtered, l)
	}

	total := len(filtered)

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[j].Timestamp.Before(filtered[i].Timestamp)
	})

	start := filter.Offset
	if start > len(filtered) {
		start = len(filtered)
	}
	if filter.Limit > 0 {
		end := start + filter.Limit
		if end > len(filtered) {
			end = len(filtered)
		}
		filtered = filtered[start:end]
	} else if start > 0 {
		filtered = filtered[start:]
	}
	return filtered, total, nil
}

// GetStats computes aggregated audit statistics in memory.
func (s *AuditStore) GetStats() (*store.AuditStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := &store.AuditStats{
		ByOperation:     make(map[string]int),
		ByStatus:        make(map[string]int),
		BySecurityLevel: make(map[string]int),
	}

	var totalDuration int64
	for _, l := range s.logs {
		stats.ByOperation[l.Operation]++
		stats.ByStatus[l.Status]++
		if l.SecurityLevel != "" {
			stats.BySecurityLevel[l.SecurityLevel]++
		}
		totalDuration += l.DurationMs
	}

	stats.TotalOperations = len(s.logs)
	if len(s.logs) > 0 {
		stats.AvgDurationMs = float64(totalDuration) / float64(len(s.logs))
	}
	return stats, nil
}

// GenerateReport generates an audit compliance report based on memory records.
func (s *AuditStore) GenerateReport(period string) (*store.AuditReport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var periodDuration time.Duration
	switch period {
	case "1h":
		periodDuration = time.Hour
	case "7d":
		periodDuration = 7 * 24 * time.Hour
	case "30d":
		periodDuration = 30 * 24 * time.Hour
	default:
		periodDuration = 24 * time.Hour
	}

	cutoff := time.Now().Add(-periodDuration)
	report := &store.AuditReport{
		BySecurityLevel: make(map[string]int),
	}

	byOp := make(map[string]int)
	successCount := 0
	totalCount := 0

	for _, l := range s.logs {
		if !l.Timestamp.After(cutoff) {
			continue
		}
		totalCount++
		if l.SecurityLevel != "" {
			report.BySecurityLevel[l.SecurityLevel]++
		}
		byOp[l.Operation]++
		if l.Status == "success" {
			successCount++
		}
	}

	report.TotalOperations = totalCount
	if totalCount > 0 {
		report.SuccessRate = float64(successCount) / float64(totalCount) * 100
	}

	type kv struct {
		Key   string
		Value int
	}
	sorted := make([]kv, 0, len(byOp))
	for k, v := range byOp {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Value > sorted[j].Value
	})

	topOps := make([]string, 0, 5)
	for i, kv := range sorted {
		if i >= 5 {
			break
		}
		topOps = append(topOps, fmt.Sprintf("%s (%d)", kv.Key, kv.Value))
	}
	report.TopOperations = topOps
	report.Recommendations = generateRecommendations(report.BySecurityLevel, report.SuccessRate)

	return report, nil
}

func generateRecommendations(byLevel map[string]int, successRate float64) []string {
	return store.BuildAuditRecommendations(byLevel, successRate)
}

// SaveSnapshot saves an evidence snapshot record in memory.
func (s *AuditStore) SaveSnapshot(snap *store.SnapshotRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if snap.IntegrityHash == "" {
		snap.IntegrityHash = store.ComputeSnapshotIntegrityHash(
			snap.ID, snap.AuditLogID, snap.PrevHash, snap.Timestamp, snap.Algorithm,
			snap.InputSample, snap.OutputSample, snap.ParametersJSON,
		)
	}
	signSnapshot(snap)
	cp := *snap
	s.snapshots = append(s.snapshots, cp)
	if len(s.snapshots) > maxSnapshots {
		s.snapshots = s.snapshots[len(s.snapshots)-maxSnapshots:]
	}
	return nil
}

// ListSnapshots returns paginated snapshots with total count.
func (s *AuditStore) ListSnapshots(limit, offset int) ([]store.SnapshotRecord, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sorted := make([]store.SnapshotRecord, len(s.snapshots))
	copy(sorted, s.snapshots)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[j].Timestamp.Before(sorted[i].Timestamp)
	})
	total := len(sorted)

	start := offset
	if start > len(sorted) {
		start = len(sorted)
	}
	if limit > 0 {
		end := start + limit
		if end > len(sorted) {
			end = len(sorted)
		}
		sorted = sorted[start:end]
	} else if start > 0 {
		sorted = sorted[start:]
	}
	return sorted, total, nil
}

// GetSnapshot retrieves a snapshot by ID.
func (s *AuditStore) GetSnapshot(id string) (*store.SnapshotRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, snap := range s.snapshots {
		if snap.ID == id {
			cp := snap
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("snapshot %s not found", id)
}

// CleanupOld deletes audit logs and their snapshots older than the cutoff time.
func (s *AuditStore) CleanupOld(before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	oldIDs := make(map[string]struct{})
	kept := make([]store.AuditLog, 0, len(s.logs))
	var count int64
	for _, l := range s.logs {
		if l.Timestamp.Before(before) {
			oldIDs[l.ID] = struct{}{}
			count++
		} else {
			kept = append(kept, l)
		}
	}
	s.logs = kept
	keptSnaps := make([]store.SnapshotRecord, 0, len(s.snapshots))
	for _, snap := range s.snapshots {
		if _, ok := oldIDs[snap.AuditLogID]; !ok {
			keptSnaps = append(keptSnaps, snap)
		}
	}
	s.snapshots = keptSnaps
	return count, nil
}

// FetchOldestForArchive implements store.AuditArchiveReader for the in-memory store: expired logs
// are returned oldest-first (insertion order equals chain order) together with their snapshots.
//
// FetchOldestForArchive 返回最早到期的内存存证日志（写入序即链序）及其关联快照。
func (s *AuditStore) FetchOldestForArchive(before time.Time, limit int) ([]store.AuditLog, []store.SnapshotRecord, error) {
	if limit <= 0 {
		limit = store.DefaultArchivePageSize
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	logs := make([]store.AuditLog, 0, min(limit, len(s.logs)))
	ids := make(map[string]struct{}, limit)
	for _, l := range s.logs {
		if len(logs) >= limit {
			break
		}
		if l.Timestamp.Before(before) {
			logs = append(logs, l)
			ids[l.ID] = struct{}{}
		}
	}
	if len(logs) == 0 {
		return nil, nil, nil
	}

	snaps := make([]store.SnapshotRecord, 0, len(logs))
	for _, snap := range s.snapshots {
		if _, ok := ids[snap.AuditLogID]; ok {
			snaps = append(snaps, snap)
		}
	}
	return logs, snaps, nil
}

// DeleteLogsByIDs implements store.AuditArchiveReader: it removes exactly the archived logs and
// their cascading snapshots from memory.
//
// DeleteLogsByIDs 精确删除已归档的内存存证日志及其级联快照。
func (s *AuditStore) DeleteLogsByIDs(ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	keptLogs := make([]store.AuditLog, 0, len(s.logs))
	removed := make(map[string]struct{}, len(ids))
	var deleted int64
	for _, l := range s.logs {
		if _, ok := idSet[l.ID]; ok {
			removed[l.ID] = struct{}{}
			deleted++
			continue
		}
		keptLogs = append(keptLogs, l)
	}
	s.logs = keptLogs

	keptSnaps := make([]store.SnapshotRecord, 0, len(s.snapshots))
	for _, snap := range s.snapshots {
		if _, ok := removed[snap.AuditLogID]; ok {
			continue
		}
		keptSnaps = append(keptSnaps, snap)
	}
	s.snapshots = keptSnaps
	return deleted, nil
}

// VerifyChain verifies the unbroken cryptographic hash chain of recent in-memory logs.
//
// VerifyChain 逐条核验内存中日志记录的密码学防篡改哈希链完整性（P2-4），结论附带机器可读
// `Reason`。本内存实现中切片的**入队顺序即 `seq`（锚点锻造序）**，因此必须按入队顺序回放，
// 不得按 timestamp 重排——客户端时间戳与入队顺序交错时会把合法链误判为断链；
// `(timestamp, id)` 仅是各后端共享的确定性兜底尾序。
func (s *AuditStore) VerifyChain(limit int) (*store.ChainVerificationResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 || limit > len(s.logs) {
		limit = len(s.logs)
	}
	totalRecords := len(s.logs)

	var previousHash string
	count := 0
	legacyCount := 0

	for i := 0; i < limit; i++ {
		l := s.logs[i]
		if l.IntegrityHash != "" {
			ok, hashLabel := store.VerifyAuditRecord(&l)
			if !ok {
				// 优先判定是否为 SM2 签名无效（完整性哈希已通过但签名失败）。
				if hashLabel != "" {
					integrityOk, _ := store.VerifyAuditIntegrityHash(l.IntegrityHash, l.ID, l.PrevHash, l.Timestamp, l.Algorithm, l.InputHash, l.OutputHash, l.User, l.SecurityLevel, l.ParametersJSON)
					if integrityOk && l.SM2Signature != "" {
						return &store.ChainVerificationResult{
							Reason:        store.ChainReasonInvalidSM2Signature,
							TotalVerified: count,
							TotalRecords:  totalRecords,
							Valid:         false,
							BrokenAtID:    l.ID,
							ActualHash:    l.SM2Signature,
							LegacyHashed:  legacyCount,
							Message:       fmt.Sprintf("SM2 signature invalid at log %s: non-repudiation proof forged or key mismatch", l.ID),
						}, nil
					}
				}
				// 锚点仍与上游衔接 ⇒ 记录被「原位改写业务字段」；否则为一般性哈希分叉。两者均判无效（fail-closed）。
				reason := store.ChainReasonHashMismatch
				if count == 0 || l.PrevHash == previousHash {
					reason = store.ChainReasonTamperedPayload
				}
				expectedHash := store.ComputeAuditIntegrityHash(l.ID, l.PrevHash, l.Timestamp, l.Algorithm, l.InputHash, l.OutputHash, l.User, l.SecurityLevel, l.ParametersJSON)
				return &store.ChainVerificationResult{
					Reason:        reason,
					TotalVerified: count,
					TotalRecords:  totalRecords,
					Valid:         false,
					LegacyHashed:  legacyCount,
					BrokenAtID:    l.ID,
					ExpectedHash:  expectedHash,
					ActualHash:    l.IntegrityHash,
					Message:       fmt.Sprintf("integrity hash mismatch at log %s: content modified", l.ID),
				}, nil
			}
			if !store.IsCanonicalHashLabel(hashLabel) {
				legacyCount++
			}
		}

		if count > 0 && l.PrevHash != previousHash {
			// 空锚点单独归因为 missing_prev，便于看板区分「链起点被抹除」与「锚点被替换」。
			reason := store.ChainReasonBrokenChain
			if l.PrevHash == "" {
				reason = store.ChainReasonMissingPrev
			}
			return &store.ChainVerificationResult{
				Reason:        reason,
				TotalVerified: count,
				TotalRecords:  totalRecords,
				Valid:         false,
				LegacyHashed:  legacyCount,
				BrokenAtID:    l.ID,
				ExpectedHash:  previousHash,
				ActualHash:    l.PrevHash,
				Message:       fmt.Sprintf("hash chain broken at log %s: expected prev_hash %s, got %s", l.ID, previousHash, l.PrevHash),
			}, nil
		}

		previousHash = l.IntegrityHash
		if previousHash == "" {
			previousHash = store.ComputeAuditIntegrityHash(l.ID, l.PrevHash, l.Timestamp, l.Algorithm, l.InputHash, l.OutputHash, l.User, l.SecurityLevel, l.ParametersJSON)
		}
		count++
	}

	result := &store.ChainVerificationResult{
		Reason:        store.ChainReasonOK,
		TotalVerified: count,
		TotalRecords:  totalRecords,
		Valid:         true,
		LegacyHashed:  legacyCount,
		Message:       fmt.Sprintf("hash chain verified successfully (%d records checked)", count),
	}
	if legacyCount > 0 {
		// 证据真实但写入于密钥化口径之前：链有效，仅需重签（P2-4 缺口 b）。
		result.Reason = store.ChainReasonLegacyHashed
		result.Message = fmt.Sprintf("hash chain verified successfully (%d records checked, %d legacy-hashed records pending canonical SM3 re-signing)", count, legacyCount)
	}
	return result, nil
}

// 编译期接口一致性检查。
var (
	_ store.TaskStore       = (*TaskStore)(nil)
	_ store.LeasedTaskStore = (*TaskStore)(nil) // 运行时方法返回 ErrLeaseNotSupported
	_ store.DataSourceStore = (*DataSourceStore)(nil)
	_ store.AuditStore      = (*AuditStore)(nil)
)
