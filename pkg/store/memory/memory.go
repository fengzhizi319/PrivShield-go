// Package memory provides in-memory fallback implementations of the store interfaces.
// Package memory 为 store 接口提供内存实现，用于开发/测试场景。
//
// 当 DB_PATH 环境变量为空时，各模块自动回退到内存实现。
// 进程重启后数据会丢失，生产环境应使用 SQLite 实现。
package memory

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/fengzhizi319/PrivShield/pkg/store"
)

// ─────────────────────────────────────────────────────────────
// TaskStore / 任务内存存储
// ─────────────────────────────────────────────────────────────

// TaskStore implements store.TaskStore backed by an in-memory map.
type TaskStore struct {
	mu    sync.RWMutex
	tasks map[string]*store.Task
}

// NewTaskStore creates a new in-memory task store.
func NewTaskStore() *TaskStore {
	return &TaskStore{tasks: make(map[string]*store.Task)}
}

func (s *TaskStore) Save(task *store.Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *task
	s.tasks[task.ID] = &cp
	return nil
}

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

	// P46 fix: use sort.Slice (O(n log n)) instead of bubble sort (O(n²)) for production workloads.
	sort.Slice(all, func(i, j int) bool {
		return all[j].CreatedAt.Before(all[i].CreatedAt)
	})

	// Apply pagination / 应用分页
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

// ── Phase B: LeasedTaskStore stubs (not supported) / 租约桩实现（不支持） ──

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
// DataSourceStore / 数据源内存存储
// ─────────────────────────────────────────────────────────────

// DataSourceStore implements store.DataSourceStore backed by an in-memory map.
type DataSourceStore struct {
	mu           sync.RWMutex
	dsMap        map[string]*store.DataSource
	auditRecords []store.AccessAuditRecord
}

// maxAuditRecords is the maximum number of access audit records retained in memory.
// P60 fix: cap in-memory audit records to prevent unbounded memory growth.
const maxAuditRecords = 10_000

// NewDataSourceStore creates a new in-memory data source store.
func NewDataSourceStore() *DataSourceStore {
	return &DataSourceStore{
		dsMap:        make(map[string]*store.DataSource),
		auditRecords: make([]store.AccessAuditRecord, 0),
	}
}

func (s *DataSourceStore) SaveDS(ds *store.DataSource) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *ds
	s.dsMap[ds.ID] = &cp
	return nil
}

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

func (s *DataSourceStore) ListDS(filter store.DataSourceFilter) ([]store.DataSource, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]store.DataSource, 0, len(s.dsMap))
	for _, ds := range s.dsMap {
		result = append(result, *ds)
	}
	total := len(result)

	// P46 fix: use sort.Slice instead of bubble sort.
	sort.Slice(result, func(i, j int) bool {
		return result[j].CreatedAt.Before(result[i].CreatedAt)
	})

	// Apply pagination
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

func (s *DataSourceStore) DeleteDS(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.dsMap[id]; !ok {
		return fmt.Errorf("data source %s not found", id)
	}
	delete(s.dsMap, id)
	return nil
}

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

func (s *DataSourceStore) SaveAudit(rec *store.AccessAuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *rec
	s.auditRecords = append(s.auditRecords, cp)
	// P60 fix: drop oldest records when capacity is exceeded to prevent unbounded memory growth.
	if len(s.auditRecords) > maxAuditRecords {
		s.auditRecords = s.auditRecords[len(s.auditRecords)-maxAuditRecords:]
	}
	return nil
}

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

	// Apply pagination
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
// AuditStore / 审计日志内存存储
// ─────────────────────────────────────────────────────────────

// AuditStore implements store.AuditStore backed by in-memory slices.
type AuditStore struct {
	mu        sync.RWMutex
	logs      []store.AuditLog
	snapshots []store.SnapshotRecord
}

// maxAuditLogs is the maximum number of audit logs retained in memory.
// P60 fix: cap in-memory audit logs to prevent unbounded memory growth.
const maxAuditLogs = 50_000

// maxSnapshots is the maximum number of snapshots retained in memory.
// P60 fix: cap in-memory snapshots to prevent unbounded memory growth.
const maxSnapshots = 50_000

// NewAuditStore creates a new in-memory audit store.
func NewAuditStore() *AuditStore {
	return &AuditStore{
		logs:      make([]store.AuditLog, 0),
		snapshots: make([]store.SnapshotRecord, 0),
	}
}

func (s *AuditStore) SaveLog(log *store.AuditLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if log.PrevHash == "" && len(s.logs) > 0 {
		log.PrevHash = s.logs[len(s.logs)-1].IntegrityHash
	}
	if log.IntegrityHash == "" {
		log.IntegrityHash = store.ComputeAuditIntegrityHash(log.ID, log.PrevHash, log.Timestamp, log.Algorithm, log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON)
	}

	cp := *log
	s.logs = append(s.logs, cp)
	// P60 fix: drop oldest logs when capacity is exceeded to prevent unbounded memory growth.
	if len(s.logs) > maxAuditLogs {
		s.logs = s.logs[len(s.logs)-maxAuditLogs:]
	}
	return nil
}

// SaveLogWithSnapshot stores an audit log and its snapshot while holding one lock.
func (s *AuditStore) SaveLogWithSnapshot(log *store.AuditLog, snapshot *store.SnapshotRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if log.PrevHash == "" && len(s.logs) > 0 {
		log.PrevHash = s.logs[len(s.logs)-1].IntegrityHash
	}
	if log.IntegrityHash == "" {
		log.IntegrityHash = store.ComputeAuditIntegrityHash(log.ID, log.PrevHash, log.Timestamp, log.Algorithm, log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON)
	}
	if snapshot != nil {
		if snapshot.PrevHash == "" {
			snapshot.PrevHash = log.PrevHash
		}
		if snapshot.IntegrityHash == "" {
			snapshot.IntegrityHash = log.IntegrityHash
		}
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

// SaveLogsBatch saves multiple logs and snapshots in memory atomically.
func (s *AuditStore) SaveLogsBatch(logs []store.AuditLog, snapshots []store.SnapshotRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, l := range logs {
		s.logs = append(s.logs, l)
	}
	for _, snap := range snapshots {
		s.snapshots = append(s.snapshots, snap)
	}

	if len(s.logs) > maxAuditLogs {
		s.logs = s.logs[len(s.logs)-maxAuditLogs:]
	}
	if len(s.snapshots) > maxSnapshots {
		s.snapshots = s.snapshots[len(s.snapshots)-maxSnapshots:]
	}
	return nil
}

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

	// P46 fix: use sort.Slice instead of bubble sort.
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[j].Timestamp.Before(filtered[i].Timestamp)
	})

	// Apply pagination / 应用分页
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

func (s *AuditStore) GenerateReport(period string) (*store.AuditReport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Parse period to duration
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

	// Filter by period and compute statistics
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

	// Get top operations
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

	// Generate recommendations
	report.Recommendations = generateRecommendations(report.BySecurityLevel, report.SuccessRate)

	return report, nil
}

// generateRecommendations generates audit recommendations based on statistics.
func generateRecommendations(byLevel map[string]int, successRate float64) []string {
	recs := make([]string, 0)

	if l4 := byLevel["L4"]; l4 > 100 {
		recs = append(recs, "L4 级别操作频繁，建议审查差分隐私预算消耗")
	}
	if l5 := byLevel["L5"]; l5 > 50 {
		recs = append(recs, "L5 绝密数据操作较多，建议加强访问控制审计")
	}
	if successRate < 95 {
		recs = append(recs, fmt.Sprintf("成功率 %.1f%% 低于 95%%，建议排查失败原因", successRate))
	}
	if len(recs) == 0 {
		recs = append(recs, "审计指标正常，无需特别关注")
	}

	return recs
}

func (s *AuditStore) SaveSnapshot(snap *store.SnapshotRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *snap
	s.snapshots = append(s.snapshots, cp)
	// P60 fix: drop oldest snapshots when capacity is exceeded to prevent unbounded memory growth.
	if len(s.snapshots) > maxSnapshots {
		s.snapshots = s.snapshots[len(s.snapshots)-maxSnapshots:]
	}
	return nil
}

// ListSnapshots returns paginated snapshots with total count.
// P35 fix: return total count for proper pagination instead of len(snaps).
func (s *AuditStore) ListSnapshots(limit, offset int) ([]store.SnapshotRecord, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sorted := make([]store.SnapshotRecord, len(s.snapshots))
	copy(sorted, s.snapshots)
	// P46 fix: use sort.Slice instead of bubble sort.
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[j].Timestamp.Before(sorted[i].Timestamp)
	})
	total := len(sorted)

	// P30 fix: apply offset + limit for proper pagination
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

// CleanupOld deletes audit logs and their associated snapshots older than the cutoff time.
// CleanupOld 删除早于截止时间的审计日志及其关联快照，防止内存无限增长。
func (s *AuditStore) CleanupOld(before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Collect IDs of old audit logs for snapshot cleanup
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
	// Remove associated snapshots
	keptSnaps := make([]store.SnapshotRecord, 0, len(s.snapshots))
	for _, snap := range s.snapshots {
		if _, ok := oldIDs[snap.AuditLogID]; !ok {
			keptSnaps = append(keptSnaps, snap)
		}
	}
	s.snapshots = keptSnaps
	return count, nil
}

// VerifyChain verifies the unbroken cryptographic hash chain of recent in-memory logs.
func (s *AuditStore) VerifyChain(limit int) (*store.ChainVerificationResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 || limit > len(s.logs) {
		limit = len(s.logs)
	}

	var previousHash string
	count := 0
	legacyCount := 0

	for i := 0; i < limit; i++ {
		l := s.logs[i]
		if l.IntegrityHash != "" {
			ok, hashLabel := store.VerifyAuditIntegrityHash(l.IntegrityHash, l.ID, l.PrevHash, l.Timestamp, l.Algorithm, l.InputHash, l.OutputHash, l.User, l.SecurityLevel, l.ParametersJSON)
			if !ok {
				expectedHash := store.ComputeAuditIntegrityHash(l.ID, l.PrevHash, l.Timestamp, l.Algorithm, l.InputHash, l.OutputHash, l.User, l.SecurityLevel, l.ParametersJSON)
				return &store.ChainVerificationResult{
					TotalVerified: count,
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
			return &store.ChainVerificationResult{
				TotalVerified: count,
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
		TotalVerified: count,
		Valid:         true,
		LegacyHashed:  legacyCount,
		Message:       fmt.Sprintf("hash chain verified successfully (%d records checked)", count),
	}
	if legacyCount > 0 {
		result.Message = fmt.Sprintf("hash chain verified successfully (%d records checked, %d legacy-hashed records pending canonical SM3 re-signing)", count, legacyCount)
	}
	return result, nil
}

// Ensure interface compliance at compile time.
var (
	_ store.TaskStore       = (*TaskStore)(nil)
	_ store.LeasedTaskStore = (*TaskStore)(nil) // Methods return ErrLeaseNotSupported at runtime.
	_ store.DataSourceStore = (*DataSourceStore)(nil)
	_ store.AuditStore      = (*AuditStore)(nil)
)

// Unused import guard.
var _ = time.Now
