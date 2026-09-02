// Package sqlite provides SQLite-backed AuditStore implementation.
// Package sqlite 提供基于 SQLite 的不可篡改审计日志与快照存储实现。
//
// ==============================================================================
// 【核心设计与互斥保护】
// 1. 【单写者互斥锁保护 (s.mu)】：
//    在 SaveLog/SaveLogWithSnapshot/SaveLogsBatch 等写路径中通过互斥锁保护链尾递进与事务执行，
//    确保单机多协程并发写入时哈希链绝对保序连续；
// 2. 【事务原子落盘】：
//    SaveLogWithSnapshot 与 SaveLogsBatch 均在底层显式使用 BEGIN/COMMIT 事务，
//    避免单条日志或快照发生部分成功的部分写入；
// 3. 【SQL 聚合报表生成】：
//    GetStats 与 GenerateReport 利用 SQLite 原生 datetime 与聚合函数快速计算成功率与 Top 操作；
// 4. 【哈希链规范化序对账 (VerifyChain)】：
//    采用 `ORDER BY seq ASC, timestamp ASC, id ASC` 规范化链序对账核验，与 PostgreSQL、内存实现
//    及重签工具 `repairchain` 复用同一口径（P2-4）：`seq` 为单调的锚点锻造序（历史库由 rowid 回填），
//    决定链的真正回放顺序；`(timestamp, id)` 作为确定性兜底尾序，使同时间戳记录在各后端与
//    离线工具上始终以同一顺序重放，写入序（GetLatestLog 链尾裁定）与核验序严格互逆，
//    杜绝「一处判为断链、另一处判为正常」的伪分叉。
// ==============================================================================

package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/store"
)

// AuditStore implements store.AuditStore backed by SQLite.
// AuditStore 基于 SQLite 实现不可篡改审计日志与快照存储。
type AuditStore struct {
	db *sql.DB
	mu sync.Mutex
}

// NewAuditStore creates a new SQLite-backed audit store.
//
// NewAuditStore 构建 SQLite 审计存储实例并自动初始化表结构。
func NewAuditStore(db *sql.DB) (*AuditStore, error) {
	if err := InitAuditTables(db); err != nil {
		return nil, fmt.Errorf("init audit tables: %w", err)
	}
	return &AuditStore{db: db}, nil
}

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

// SaveLog persists an audit log and computes its hash chain if missing.
func (s *AuditStore) SaveLog(log *store.AuditLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if log.PrevHash == "" {
		if latest, err := s.GetLatestLog(); err == nil && latest != nil {
			log.PrevHash = latest.IntegrityHash
		}
	}
	if log.IntegrityHash == "" {
		log.IntegrityHash = store.ComputeAuditIntegrityHash(log.ID, log.PrevHash, log.Timestamp, log.Algorithm, log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON)
	}
	signAuditLog(log)
	_, err := s.db.Exec(`
		INSERT INTO audit_logs (id, seq, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash,
			algorithm, parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash, sm2_signature)
		VALUES (?, COALESCE((SELECT MAX(seq) FROM audit_logs), 0) + 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, log.ID, log.TaskID, log.APICode, log.DatasourceID, log.Timestamp.Format(time.RFC3339Nano), log.Operation, log.DataSource,
		log.InputHash, log.OutputHash, log.Algorithm, log.ParametersJSON,
		log.InputRows, log.OutputRows, log.DurationMs, log.User, log.Status, log.ErrorMessage, log.SecurityLevel,
		log.PrevHash, log.IntegrityHash, log.SM2Signature)
	return err
}

// SaveLogWithSnapshot persists an audit log and its snapshot as one transaction.
//
// SaveLogWithSnapshot 在同一事务内原子持久化审计日志与其关联的数据快照。
func (s *AuditStore) SaveLogWithSnapshot(log *store.AuditLog, snapshot *store.SnapshotRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if log.PrevHash == "" {
		if latest, err := s.GetLatestLog(); err == nil && latest != nil {
			log.PrevHash = latest.IntegrityHash
		}
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

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO audit_logs (id, seq, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash,
			algorithm, parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash, sm2_signature)
		VALUES (?, COALESCE((SELECT MAX(seq) FROM audit_logs), 0) + 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, log.ID, log.TaskID, log.APICode, log.DatasourceID, log.Timestamp.Format(time.RFC3339Nano), log.Operation, log.DataSource,
		log.InputHash, log.OutputHash, log.Algorithm, log.ParametersJSON,
		log.InputRows, log.OutputRows, log.DurationMs, log.User, log.Status, log.ErrorMessage, log.SecurityLevel,
		log.PrevHash, log.IntegrityHash, log.SM2Signature); err != nil {
		return err
	}
	if snapshot != nil {
		if _, err := tx.Exec(`
			INSERT INTO snapshots (id, audit_log_id, timestamp, input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash, sm2_signature)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, snapshot.ID, snapshot.AuditLogID, snapshot.Timestamp.Format(time.RFC3339Nano),
			snapshot.InputSample, snapshot.OutputSample, snapshot.Algorithm, snapshot.ParametersJSON, snapshot.IntegrityHash, snapshot.PrevHash, snapshot.SM2Signature); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SaveLogsBatch saves multiple logs and optional snapshots in a single atomic transaction.
//
// SaveLogsBatch 在单个原子事务内批量插入多条日志与快照。
func (s *AuditStore) SaveLogsBatch(logs []store.AuditLog, snapshots []store.SnapshotRecord) error {
	if len(logs) == 0 && len(snapshots) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	logStmt, err := tx.Prepare(`
		INSERT INTO audit_logs (id, seq, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash,
			algorithm, parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash, sm2_signature)
		VALUES (?, COALESCE((SELECT MAX(seq) FROM audit_logs), 0) + 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer logStmt.Close()

	for i := range logs {
		log := &logs[i]
		if log.IntegrityHash == "" {
			log.IntegrityHash = store.ComputeAuditIntegrityHash(log.ID, log.PrevHash, log.Timestamp, log.Algorithm, log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON)
		}
		signAuditLog(log)
		if _, err := logStmt.Exec(log.ID, log.TaskID, log.APICode, log.DatasourceID, log.Timestamp.Format(time.RFC3339Nano), log.Operation, log.DataSource,
			log.InputHash, log.OutputHash, log.Algorithm, log.ParametersJSON,
			log.InputRows, log.OutputRows, log.DurationMs, log.User, log.Status, log.ErrorMessage, log.SecurityLevel,
			log.PrevHash, log.IntegrityHash, log.SM2Signature); err != nil {
			return err
		}
	}

	if len(snapshots) > 0 {
		snapStmt, err := tx.Prepare(`
			INSERT INTO snapshots (id, audit_log_id, timestamp, input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash, sm2_signature)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			return err
		}
		defer snapStmt.Close()

		for i := range snapshots {
			snap := &snapshots[i]
			signSnapshot(snap)
			if _, err := snapStmt.Exec(snap.ID, snap.AuditLogID, snap.Timestamp.Format(time.RFC3339Nano),
				snap.InputSample, snap.OutputSample, snap.Algorithm, snap.ParametersJSON, snap.IntegrityHash, snap.PrevHash, snap.SM2Signature); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

// GetLog retrieves an audit log by ID.
func (s *AuditStore) GetLog(id string) (*store.AuditLog, error) {
	row := s.db.QueryRow(`
		SELECT id, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash, algorithm,
			parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash, sm2_signature
		FROM audit_logs WHERE id = ?
	`, id)
	return scanAuditLog(row)
}

// GetLatestLog returns the chain tail, i.e. the last record in the canonical chain order.
//
// GetLatestLog 返回链尾记录：按规范化链序 `(seq DESC, timestamp DESC, id DESC)` 取最后一条
// （P2-4），与 `VerifyChain` 的遍历方向严格互逆，确保「写入侧锚定的上一条」与「核验侧的上一条」
// 在同时间戳场景下仍是同一条记录，避免写入即误判断链。
func (s *AuditStore) GetLatestLog() (*store.AuditLog, error) {
	row := s.db.QueryRow(`
		SELECT id, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash, algorithm,
			parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash, sm2_signature
		FROM audit_logs ORDER BY seq DESC, timestamp DESC, id DESC LIMIT 1
	`)
	log, err := scanAuditLog(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return log, nil
}

// ListLogs returns filtered and paginated audit logs.
func (s *AuditStore) ListLogs(filter store.AuditFilter) ([]store.AuditLog, int, error) {
	where, args := buildAuditWhere(filter)

	// 统计总数
	countQuery := "SELECT COUNT(*) FROM audit_logs" + where
	var total int
	if err := s.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// 查询行
	query := `SELECT id, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash, algorithm,
		parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash, sm2_signature
		FROM audit_logs` + where + " ORDER BY timestamp DESC"
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

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	logs := make([]store.AuditLog, 0)
	for rows.Next() {
		l, err := scanAuditLogRow(rows)
		if err != nil {
			return nil, 0, err
		}
		logs = append(logs, *l)
	}
	return logs, total, rows.Err()
}

// GetStats computes aggregated audit statistics via SQLite engine.
func (s *AuditStore) GetStats() (*store.AuditStats, error) {
	stats := &store.AuditStats{
		ByOperation:     make(map[string]int),
		ByStatus:        make(map[string]int),
		BySecurityLevel: make(map[string]int),
	}

	if err := s.db.QueryRow("SELECT COUNT(*), COALESCE(AVG(duration_ms), 0) FROM audit_logs").Scan(&stats.TotalOperations, &stats.AvgDurationMs); err != nil {
		return nil, err
	}

	rows, err := s.db.Query("SELECT operation, COUNT(*) FROM audit_logs GROUP BY operation")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var op string
		var count int
		if err := rows.Scan(&op, &count); err != nil {
			return nil, err
		}
		stats.ByOperation[op] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows2, err := s.db.Query("SELECT status, COUNT(*) FROM audit_logs GROUP BY status")
	if err != nil {
		return nil, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var status string
		var count int
		if err := rows2.Scan(&status, &count); err != nil {
			return nil, err
		}
		stats.ByStatus[status] = count
	}
	if err := rows2.Err(); err != nil {
		return nil, err
	}

	rows3, err := s.db.Query("SELECT security_level, COUNT(*) FROM audit_logs WHERE security_level != '' GROUP BY security_level")
	if err != nil {
		return nil, err
	}
	defer rows3.Close()
	for rows3.Next() {
		var level string
		var count int
		if err := rows3.Scan(&level, &count); err != nil {
			return nil, err
		}
		stats.BySecurityLevel[level] = count
	}
	return stats, rows3.Err()
}

// GenerateReport generates a compliance audit report with SQL-level filtering and aggregation.
func (s *AuditStore) GenerateReport(period string) (*store.AuditReport, error) {
	var periodDuration string
	switch period {
	case "1h":
		periodDuration = "1 hour"
	case "7d":
		periodDuration = "7 days"
	case "30d":
		periodDuration = "30 days"
	default:
		periodDuration = "24 hours"
	}

	report := &store.AuditReport{
		BySecurityLevel: make(map[string]int),
	}

	whereClause := "WHERE timestamp > datetime('now', ?)"
	periodParam := "-" + periodDuration

	var totalCount, successCount int
	query := fmt.Sprintf("SELECT COUNT(*), COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END), 0) FROM audit_logs %s", whereClause)
	if err := s.db.QueryRow(query, periodParam).Scan(&totalCount, &successCount); err != nil {
		return nil, err
	}
	report.TotalOperations = totalCount
	if totalCount > 0 {
		report.SuccessRate = float64(successCount) / float64(totalCount) * 100
	}

	query2 := fmt.Sprintf("SELECT security_level, COUNT(*) FROM audit_logs %s AND security_level != '' GROUP BY security_level", whereClause)
	rows, err := s.db.Query(query2, periodParam)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var level string
		var count int
		if err := rows.Scan(&level, &count); err != nil {
			return nil, err
		}
		report.BySecurityLevel[level] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	query3 := fmt.Sprintf("SELECT operation, COUNT(*) as cnt FROM audit_logs %s GROUP BY operation ORDER BY cnt DESC LIMIT 5", whereClause)
	rows3, err := s.db.Query(query3, periodParam)
	if err != nil {
		return nil, err
	}
	defer rows3.Close()
	topOps := make([]string, 0, 5)
	for rows3.Next() {
		var op string
		var count int
		if err := rows3.Scan(&op, &count); err != nil {
			return nil, err
		}
		topOps = append(topOps, fmt.Sprintf("%s (%d)", op, count))
	}
	report.TopOperations = topOps
	if err := rows3.Err(); err != nil {
		return nil, err
	}

	report.Recommendations = generateRecommendations(report.BySecurityLevel, report.SuccessRate)
	return report, nil
}

func generateRecommendations(byLevel map[string]int, successRate float64) []string {
	return store.BuildAuditRecommendations(byLevel, successRate)
}

// SaveSnapshot stores a snapshot record in SQLite.
func (s *AuditStore) SaveSnapshot(snap *store.SnapshotRecord) error {
	if snap.IntegrityHash == "" {
		snap.IntegrityHash = store.ComputeSnapshotIntegrityHash(
			snap.ID, snap.AuditLogID, snap.PrevHash, snap.Timestamp, snap.Algorithm,
			snap.InputSample, snap.OutputSample, snap.ParametersJSON,
		)
	}
	signSnapshot(snap)
	_, err := s.db.Exec(`
		INSERT INTO snapshots (id, audit_log_id, timestamp, input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash, sm2_signature)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, snap.ID, snap.AuditLogID, snap.Timestamp.Format(time.RFC3339Nano),
		snap.InputSample, snap.OutputSample, snap.Algorithm, snap.ParametersJSON, snap.IntegrityHash, snap.PrevHash, snap.SM2Signature)
	return err
}

// ListSnapshots returns paginated snapshot records.
func (s *AuditStore) ListSnapshots(limit, offset int) ([]store.SnapshotRecord, int, error) {
	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM snapshots").Scan(&total); err != nil {
		return nil, 0, err
	}

	query := "SELECT id, audit_log_id, timestamp, input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash, sm2_signature FROM snapshots ORDER BY timestamp DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
		if offset > 0 {
			query += fmt.Sprintf(" OFFSET %d", offset)
		}
	}
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	snaps := make([]store.SnapshotRecord, 0)
	for rows.Next() {
		snap, err := scanSnapshotRow(rows)
		if err != nil {
			return nil, 0, err
		}
		snaps = append(snaps, *snap)
	}
	return snaps, total, rows.Err()
}

// GetSnapshot retrieves a snapshot by ID.
func (s *AuditStore) GetSnapshot(id string) (*store.SnapshotRecord, error) {
	row := s.db.QueryRow(`
		SELECT id, audit_log_id, timestamp, input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash, sm2_signature
		FROM snapshots WHERE id = ?
	`, id)
	return scanSnapshotRowScanner(row.Scan)
}

// VerifyChain verifies the unbroken cryptographic hash chain of recent logs in SQLite.
//
// VerifyChain 按**规范化链序** `(seq ASC, timestamp ASC, id ASC)` 对账核验哈希链完整性。
// limit <= 0 表示验证全表记录；否则验证最多 limit 条记录。
//
// P2-4：核验结论附带机器可读 `Reason`（取值见 store.ChainReason* 枚举），遍历顺序与
// PostgreSQL、内存实现及重签工具 `repairchain` 同源：`seq`（锚点锻造序，由 P1 引入的单调列）
// 为首要次序，`(timestamp, id)` 为确定性兜底尾序，使同时间戳记录在所有后端与离线工具上
// 以同一顺序回放，杜绝「一处判为断链、另一处判为正常」的伪分叉。
func (s *AuditStore) VerifyChain(limit int) (*store.ChainVerificationResult, error) {
	const maxLimit = 100000
	if limit < 0 || limit > maxLimit {
		limit = maxLimit
	}

	totalRecords := 0
	if err := s.db.QueryRow("SELECT COUNT(*) FROM audit_logs").Scan(&totalRecords); err != nil {
		return nil, err
	}

	// 规范化链序 (seq, timestamp, id)：seq 为锚点锻造序（等价于落盘序，见 init.go 的 seq 回填），
	// (timestamp, id) 提供与 PostgreSQL 完全一致的确定性兜底尾序（P2-4）。
	query := `SELECT id, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash, algorithm,
		parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash, sm2_signature
		FROM audit_logs ORDER BY seq ASC, timestamp ASC, id ASC`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var previousHash string
	count, legacyCount := 0, 0

	for rows.Next() {
		log, err := scanAuditLogRow(rows)
		if err != nil {
			return nil, err
		}

		if log.IntegrityHash != "" {
			ok, hashLabel := store.VerifyAuditRecord(log)
			if !ok {
				// 优先判定是否为 SM2 签名无效（完整性哈希已通过但签名失败）。
				if hashLabel != "" {
					integrityOk, _ := store.VerifyAuditIntegrityHash(log.IntegrityHash, log.ID, log.PrevHash, log.Timestamp, log.Algorithm, log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON)
					if integrityOk && log.SM2Signature != "" {
						return &store.ChainVerificationResult{
							Reason:        store.ChainReasonInvalidSM2Signature,
							TotalVerified: count,
							TotalRecords:  totalRecords,
							Valid:         false,
							BrokenAtID:    log.ID,
							ActualHash:    log.SM2Signature,
							LegacyHashed:  legacyCount,
							Message:       fmt.Sprintf("SM2 signature invalid at log %s: non-repudiation proof forged or key mismatch", log.ID),
						}, nil
					}
				}
				// 锚点仍与上游衔接 ⇒ 记录被「原位改写业务字段」；否则为一般性哈希分叉。两者均判无效（fail-closed）。
				reason := store.ChainReasonHashMismatch
				if count == 0 || log.PrevHash == previousHash {
					reason = store.ChainReasonTamperedPayload
				}
				return &store.ChainVerificationResult{
					Reason:        reason,
					TotalVerified: count,
					TotalRecords:  totalRecords,
					Valid:         false,
					BrokenAtID:    log.ID,
					ExpectedHash:  store.ComputeAuditIntegrityHash(log.ID, log.PrevHash, log.Timestamp, log.Algorithm, log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON),
					ActualHash:    log.IntegrityHash,
					LegacyHashed:  legacyCount,
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
	// Detect physical deletion: if limit was unset (0 = full scan) and verified count < total records,
	// it means records in the middle of the chain may have been deleted and the returned subset still appears continuous.
	if limit <= 0 && count < totalRecords {
		result.Reason = store.ChainReasonMissingRecords
		result.Valid = false
		result.Message = fmt.Sprintf("possible missing records: verified %d records but table has %d total records", count, totalRecords)
	}
	return result, rows.Err()
}

func buildAuditWhere(filter store.AuditFilter) (string, []any) {
	conditions := make([]string, 0)
	args := make([]any, 0)

	if filter.TaskID != "" {
		conditions = append(conditions, "task_id = ?")
		args = append(args, filter.TaskID)
	}
	if filter.APICode != "" {
		conditions = append(conditions, "api_code = ?")
		args = append(args, filter.APICode)
	}
	if filter.DatasourceID != "" {
		conditions = append(conditions, "(datasource_id = ? OR datasource = ?)")
		args = append(args, filter.DatasourceID, filter.DatasourceID)
	} else if filter.DataSource != "" {
		conditions = append(conditions, "(datasource = ? OR datasource_id = ?)")
		args = append(args, filter.DataSource, filter.DataSource)
	}
	if filter.Operation != "" {
		conditions = append(conditions, "operation = ?")
		args = append(args, filter.Operation)
	}
	if filter.User != "" {
		conditions = append(conditions, "user_name = ?")
		args = append(args, filter.User)
	}
	if filter.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, filter.Status)
	}
	if filter.SecurityLevel != "" {
		conditions = append(conditions, "security_level = ?")
		args = append(args, filter.SecurityLevel)
	}

	if len(conditions) == 0 {
		return "", nil
	}

	where := " WHERE "
	for i, c := range conditions {
		if i > 0 {
			where += " AND "
		}
		where += c
	}
	return where, args
}

// CleanupOld deletes audit logs and their snapshots older than the cutoff time.
func (s *AuditStore) CleanupOld(before time.Time) (int64, error) {
	cutoff := before.Format(time.RFC3339Nano)
	_, _ = s.db.Exec(`DELETE FROM snapshots WHERE audit_log_id IN (SELECT id FROM audit_logs WHERE timestamp < ?)`, cutoff)
	result, err := s.db.Exec(`DELETE FROM audit_logs WHERE timestamp < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// FetchOldestForArchive implements store.AuditArchiveReader for SQLite: it returns the oldest
// expired logs in the canonical chain order (the same `(seq, timestamp, id)` order VerifyChain
// replays, so archive order equals chain order) together with the snapshots attached to them.
//
// FetchOldestForArchive 按规范化链序 `(seq ASC, timestamp ASC, id ASC)` 返回最早到期的存证日志及其快照，
// 与 VerifyChain 的回放序及 PostgreSQL 实现保持一致（P2-4）；
// 时间过滤语义与 CleanupOld 严格一致（timestamp < cutoff），调用方每归档一页即按 ID 删除，
// 因此无需游标也不会「删而未档」。
func (s *AuditStore) FetchOldestForArchive(before time.Time, limit int) ([]store.AuditLog, []store.SnapshotRecord, error) {
	if limit <= 0 {
		limit = store.DefaultArchivePageSize
	}
	cutoff := before.Format(time.RFC3339Nano)

	rows, err := s.db.Query(`
		SELECT rowid, id, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash, algorithm,
			parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash, sm2_signature
		FROM audit_logs WHERE timestamp < ? ORDER BY seq ASC, timestamp ASC, id ASC LIMIT ?
	`, cutoff, limit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	logs := make([]store.AuditLog, 0, limit)
	ids := make([]any, 0, limit)
	for rows.Next() {
		l, err := scanAuditLogRowWithRowID(rows)
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
		chunk := ids[start:end]
		placeholders := strings.Repeat("?,", len(chunk))
		snapQuery := `SELECT id, audit_log_id, timestamp, input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash, sm2_signature
			 FROM snapshots WHERE audit_log_id IN (` + placeholders[:len(placeholders)-1] + `) ORDER BY timestamp ASC, id ASC`
		snapRows, err := s.db.Query(snapQuery, chunk...)
		if err != nil {
			return nil, nil, err
		}
		for snapRows.Next() {
			snap, err := scanSnapshotRow(snapRows)
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
// their snapshots, chunked inside one transaction per chunk so deletion is never partial.
//
// DeleteLogsByIDs 按 ID 精确删除已完成归档的存证日志及其级联快照，
// 每批在单事务内先删快照后删日志，避免部分成功导致存证悬挂。
func (s *AuditStore) DeleteLogsByIDs(ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	var deleted int64
	for start := 0; start < len(ids); start += store.ArchiveIDChunkSize {
		end := min(start+store.ArchiveIDChunkSize, len(ids))
		chunk := ids[start:end]
		placeholders := strings.Repeat("?,", len(chunk))
		in := placeholders[:len(placeholders)-1]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}

		tx, err := s.db.Begin()
		if err != nil {
			return deleted, err
		}
		if _, err := tx.Exec(`DELETE FROM snapshots WHERE audit_log_id IN (`+in+`)`, args...); err != nil {
			_ = tx.Rollback()
			return deleted, err
		}
		tag, err := tx.Exec(`DELETE FROM audit_logs WHERE id IN (`+in+`)`, args...)
		if err != nil {
			_ = tx.Rollback()
			return deleted, err
		}
		if err := tx.Commit(); err != nil {
			return deleted, err
		}
		n, err := tag.RowsAffected()
		if err != nil {
			return deleted, err
		}
		deleted += n
	}
	return deleted, nil
}

func scanAuditLogRowWithRowID(rows *sql.Rows) (*store.AuditLog, error) {
	var rowID int64
	var l store.AuditLog
	var ts string
	var paramsJSON, sm2Signature sql.NullString
	var taskID, apiCode, datasourceID, prevHash, integrityHash sql.NullString

	if err := rows.Scan(&rowID, &l.ID, &taskID, &apiCode, &datasourceID, &ts, &l.Operation, &l.DataSource, &l.InputHash, &l.OutputHash,
		&l.Algorithm, &paramsJSON, &l.InputRows, &l.OutputRows, &l.DurationMs,
		&l.User, &l.Status, &l.ErrorMessage, &l.SecurityLevel, &prevHash, &integrityHash, &sm2Signature); err != nil {
		return nil, err
	}

	l.TaskID = taskID.String
	l.APICode = apiCode.String
	l.DatasourceID = datasourceID.String
	l.PrevHash = prevHash.String
	l.IntegrityHash = integrityHash.String
	l.SM2Signature = sm2Signature.String
	l.ParametersJSON = paramsJSON.String
	l.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
	if paramsJSON.Valid {
		_ = json.Unmarshal([]byte(paramsJSON.String), &l.Parameters)
	}
	return &l, nil
}

func scanAuditFields(scan func(dest ...any) error) (*store.AuditLog, error) {
	var l store.AuditLog
	var ts string
	var paramsJSON, sm2Signature sql.NullString
	var taskID, apiCode, datasourceID, prevHash, integrityHash sql.NullString

	err := scan(&l.ID, &taskID, &apiCode, &datasourceID, &ts, &l.Operation, &l.DataSource, &l.InputHash, &l.OutputHash,
		&l.Algorithm, &paramsJSON, &l.InputRows, &l.OutputRows, &l.DurationMs,
		&l.User, &l.Status, &l.ErrorMessage, &l.SecurityLevel, &prevHash, &integrityHash, &sm2Signature)
	if err != nil {
		return nil, err
	}

	l.TaskID = taskID.String
	l.APICode = apiCode.String
	l.DatasourceID = datasourceID.String
	l.PrevHash = prevHash.String
	l.IntegrityHash = integrityHash.String
	l.SM2Signature = sm2Signature.String
	if l.DatasourceID == "" && l.DataSource != "" {
		l.DatasourceID = l.DataSource
	}
	if l.DataSource == "" && l.DatasourceID != "" {
		l.DataSource = l.DatasourceID
	}

	l.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
	l.ParametersJSON = paramsJSON.String
	if paramsJSON.Valid {
		_ = json.Unmarshal([]byte(paramsJSON.String), &l.Parameters)
	}
	return &l, nil
}

func scanAuditLog(row *sql.Row) (*store.AuditLog, error) {
	return scanAuditFields(row.Scan)
}

func scanAuditLogRow(rows *sql.Rows) (*store.AuditLog, error) {
	return scanAuditFields(rows.Scan)
}

func scanSnapshotRowScanner(scan func(dest ...any) error) (*store.SnapshotRecord, error) {
	var snap store.SnapshotRecord
	var ts string
	var paramsJSON sql.NullString
	var prevHash sql.NullString
	var sm2Signature sql.NullString

	err := scan(&snap.ID, &snap.AuditLogID, &ts, &snap.InputSample, &snap.OutputSample,
		&snap.Algorithm, &paramsJSON, &snap.IntegrityHash, &prevHash, &sm2Signature)
	if err != nil {
		return nil, err
	}

	snap.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
	snap.PrevHash = prevHash.String
	snap.SM2Signature = sm2Signature.String
	snap.ParametersJSON = paramsJSON.String
	if paramsJSON.Valid {
		_ = json.Unmarshal([]byte(paramsJSON.String), &snap.Parameters)
	}
	return &snap, nil
}

func scanSnapshotRow(rows *sql.Rows) (*store.SnapshotRecord, error) {
	return scanSnapshotRowScanner(rows.Scan)
}

// Close closes the underlying SQLite database connection.
func (s *AuditStore) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
