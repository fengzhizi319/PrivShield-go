package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/fengzhizi319/PrivShield/pkg/store"
)

// AuditStore implements store.AuditStore backed by SQLite.
type AuditStore struct {
	db *sql.DB
	mu sync.Mutex
}

// NewAuditStore creates a new SQLite-backed audit store.
func NewAuditStore(db *sql.DB) (*AuditStore, error) {
	if err := InitAuditTables(db); err != nil {
		return nil, fmt.Errorf("init audit tables: %w", err)
	}
	return &AuditStore{db: db}, nil
}

func (s *AuditStore) SaveLog(log *store.AuditLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if log.IntegrityHash == "" {
		log.IntegrityHash = store.ComputeAuditIntegrityHash(log.ID, log.PrevHash, log.Timestamp, log.Algorithm, log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON)
	}
	_, err := s.db.Exec(`
		INSERT INTO audit_logs (id, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash,
			algorithm, parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, log.ID, log.TaskID, log.APICode, log.DatasourceID, log.Timestamp.Format(time.RFC3339Nano), log.Operation, log.DataSource,
		log.InputHash, log.OutputHash, log.Algorithm, log.ParametersJSON,
		log.InputRows, log.OutputRows, log.DurationMs, log.User, log.Status, log.ErrorMessage, log.SecurityLevel,
		log.PrevHash, log.IntegrityHash)
	return err
}

// SaveLogWithSnapshot persists an audit log and its snapshot as one transaction.
func (s *AuditStore) SaveLogWithSnapshot(log *store.AuditLog, snapshot *store.SnapshotRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if log.IntegrityHash == "" {
		log.IntegrityHash = store.ComputeAuditIntegrityHash(log.ID, log.PrevHash, log.Timestamp, log.Algorithm, log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON)
	}
	if snapshot.IntegrityHash == "" {
		snapshot.IntegrityHash = log.IntegrityHash
	}
	if snapshot.PrevHash == "" {
		snapshot.PrevHash = log.PrevHash
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO audit_logs (id, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash,
			algorithm, parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, log.ID, log.TaskID, log.APICode, log.DatasourceID, log.Timestamp.Format(time.RFC3339Nano), log.Operation, log.DataSource,
		log.InputHash, log.OutputHash, log.Algorithm, log.ParametersJSON,
		log.InputRows, log.OutputRows, log.DurationMs, log.User, log.Status, log.ErrorMessage, log.SecurityLevel,
		log.PrevHash, log.IntegrityHash); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO snapshots (id, audit_log_id, timestamp, input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, snapshot.ID, snapshot.AuditLogID, snapshot.Timestamp.Format(time.RFC3339Nano),
		snapshot.InputSample, snapshot.OutputSample, snapshot.Algorithm, snapshot.ParametersJSON, snapshot.IntegrityHash, snapshot.PrevHash); err != nil {
		return err
	}
	return tx.Commit()
}

// SaveLogsBatch saves multiple logs and optional snapshots in a single atomic transaction.
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
		INSERT INTO audit_logs (id, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash,
			algorithm, parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer logStmt.Close()

	for _, log := range logs {
		if log.IntegrityHash == "" {
			log.IntegrityHash = store.ComputeAuditIntegrityHash(log.ID, log.PrevHash, log.Timestamp, log.Algorithm, log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON)
		}
		if _, err := logStmt.Exec(log.ID, log.TaskID, log.APICode, log.DatasourceID, log.Timestamp.Format(time.RFC3339Nano), log.Operation, log.DataSource,
			log.InputHash, log.OutputHash, log.Algorithm, log.ParametersJSON,
			log.InputRows, log.OutputRows, log.DurationMs, log.User, log.Status, log.ErrorMessage, log.SecurityLevel,
			log.PrevHash, log.IntegrityHash); err != nil {
			return err
		}
	}

	if len(snapshots) > 0 {
		snapStmt, err := tx.Prepare(`
			INSERT INTO snapshots (id, audit_log_id, timestamp, input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			return err
		}
		defer snapStmt.Close()

		for _, snap := range snapshots {
			if _, err := snapStmt.Exec(snap.ID, snap.AuditLogID, snap.Timestamp.Format(time.RFC3339Nano),
				snap.InputSample, snap.OutputSample, snap.Algorithm, snap.ParametersJSON, snap.IntegrityHash, snap.PrevHash); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

func (s *AuditStore) GetLog(id string) (*store.AuditLog, error) {
	row := s.db.QueryRow(`
		SELECT id, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash, algorithm,
			parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash
		FROM audit_logs WHERE id = ?
	`, id)
	return scanAuditLog(row)
}

// GetLatestLog returns the most recently written audit log.
func (s *AuditStore) GetLatestLog() (*store.AuditLog, error) {
	row := s.db.QueryRow(`
		SELECT id, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash, algorithm,
			parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash
		FROM audit_logs ORDER BY rowid DESC LIMIT 1
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

func (s *AuditStore) ListLogs(filter store.AuditFilter) ([]store.AuditLog, int, error) {
	where, args := buildAuditWhere(filter)

	// Count total
	countQuery := "SELECT COUNT(*) FROM audit_logs" + where
	var total int
	if err := s.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// Fetch rows
	query := `SELECT id, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash, algorithm,
		parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash
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

func (s *AuditStore) GetStats() (*store.AuditStats, error) {
	stats := &store.AuditStats{
		ByOperation:     make(map[string]int),
		ByStatus:        make(map[string]int),
		BySecurityLevel: make(map[string]int),
	}

	// Total count and average duration
	if err := s.db.QueryRow("SELECT COUNT(*), COALESCE(AVG(duration_ms), 0) FROM audit_logs").Scan(&stats.TotalOperations, &stats.AvgDurationMs); err != nil {
		return nil, err
	}

	// Group by operation
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

	// Group by status
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

	// Group by security_level
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

	// 1. Total count and success count in one query
	var totalCount, successCount int
	query := fmt.Sprintf("SELECT COUNT(*), COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END), 0) FROM audit_logs %s", whereClause)
	if err := s.db.QueryRow(query, periodParam).Scan(&totalCount, &successCount); err != nil {
		return nil, err
	}
	report.TotalOperations = totalCount
	if totalCount > 0 {
		report.SuccessRate = float64(successCount) / float64(totalCount) * 100
	}

	// 2. Group by security_level
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

	// 3. Get top operations (ORDER BY count DESC LIMIT 5)
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
	_, err := s.db.Exec(`
		INSERT INTO snapshots (id, audit_log_id, timestamp, input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, snap.ID, snap.AuditLogID, snap.Timestamp.Format(time.RFC3339Nano),
		snap.InputSample, snap.OutputSample, snap.Algorithm, snap.ParametersJSON, snap.IntegrityHash, snap.PrevHash)
	return err
}

func (s *AuditStore) ListSnapshots(limit, offset int) ([]store.SnapshotRecord, int, error) {
	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM snapshots").Scan(&total); err != nil {
		return nil, 0, err
	}

	query := "SELECT id, audit_log_id, timestamp, input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash FROM snapshots ORDER BY timestamp DESC"
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

func (s *AuditStore) GetSnapshot(id string) (*store.SnapshotRecord, error) {
	row := s.db.QueryRow(`
		SELECT id, audit_log_id, timestamp, input_sample, output_sample, algorithm, parameters_json, integrity_hash, prev_hash
		FROM snapshots WHERE id = ?
	`, id)
	return scanSnapshotRowScanner(row.Scan)
}

// VerifyChain verifies the unbroken cryptographic hash chain of recent logs.
func (s *AuditStore) VerifyChain(limit int) (*store.ChainVerificationResult, error) {
	if limit <= 0 || limit > 10000 {
		limit = 1000
	}

	// The chain follows persistence order, not the timestamp text: timestamps are stored as
	// offset-bearing RFC3339Nano strings, whose lexicographic order is not chronological.
	// 哈希链沿用具名顺序（落盘顺序）而非 timestamp 文本：带时区偏移的时间串按字典序排列不等于按时间排列。
	query := fmt.Sprintf(`SELECT id, task_id, api_code, datasource_id, timestamp, operation, datasource, input_hash, output_hash, algorithm,
		parameters_json, input_rows, output_rows, duration_ms, user_name, status, error_message, security_level, prev_hash, integrity_hash
		FROM audit_logs ORDER BY rowid ASC LIMIT %d`, limit)

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

		// Check internal hash integrity (canonical SM3, legacy SHA-256 accepted)
		if log.IntegrityHash != "" {
			ok, hashLabel := store.VerifyAuditIntegrityHash(log.IntegrityHash, log.ID, log.PrevHash, log.Timestamp, log.Algorithm, log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON)
			if !ok {
				return &store.ChainVerificationResult{
					TotalVerified: count,
					Valid:         false,
					BrokenAtID:    log.ID,
					ExpectedHash:  store.ComputeAuditIntegrityHash(log.ID, log.PrevHash, log.Timestamp, log.Algorithm, log.InputHash, log.OutputHash, log.User, log.SecurityLevel, log.ParametersJSON),
					ActualHash:    log.IntegrityHash,
					Message:       fmt.Sprintf("integrity hash mismatch at log %s: content modified", log.ID),
				}, nil
			}
			if !store.IsCanonicalHashLabel(hashLabel) {
				legacyCount++
			}
		}

		// Check chain continuity with previous record
		if count > 0 && log.PrevHash != previousHash {
			return &store.ChainVerificationResult{
				TotalVerified: count,
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
		TotalVerified: count,
		Valid:         true,
		LegacyHashed:  legacyCount,
		Message:       fmt.Sprintf("hash chain verified successfully (%d records checked)", count),
	}
	if legacyCount > 0 {
		result.Message = fmt.Sprintf("hash chain verified successfully (%d records checked, %d legacy-hashed records pending canonical SM3 re-signing)", count, legacyCount)
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

// CleanupOld deletes audit logs and their associated snapshots older than the cutoff time.
func (s *AuditStore) CleanupOld(before time.Time) (int64, error) {
	cutoff := before.Format(time.RFC3339Nano)
	_, _ = s.db.Exec(`DELETE FROM snapshots WHERE audit_log_id IN (SELECT id FROM audit_logs WHERE timestamp < ?)`, cutoff)
	result, err := s.db.Exec(`DELETE FROM audit_logs WHERE timestamp < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func scanAuditFields(scan func(dest ...any) error) (*store.AuditLog, error) {
	var l store.AuditLog
	var ts string
	var paramsJSON sql.NullString
	var taskID, apiCode, datasourceID, prevHash, integrityHash sql.NullString

	err := scan(&l.ID, &taskID, &apiCode, &datasourceID, &ts, &l.Operation, &l.DataSource, &l.InputHash, &l.OutputHash,
		&l.Algorithm, &paramsJSON, &l.InputRows, &l.OutputRows, &l.DurationMs,
		&l.User, &l.Status, &l.ErrorMessage, &l.SecurityLevel, &prevHash, &integrityHash)
	if err != nil {
		return nil, err
	}

	l.TaskID = taskID.String
	l.APICode = apiCode.String
	l.DatasourceID = datasourceID.String
	l.PrevHash = prevHash.String
	l.IntegrityHash = integrityHash.String
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

	err := scan(&snap.ID, &snap.AuditLogID, &ts, &snap.InputSample, &snap.OutputSample,
		&snap.Algorithm, &paramsJSON, &snap.IntegrityHash, &prevHash)
	if err != nil {
		return nil, err
	}

	snap.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
	snap.PrevHash = prevHash.String
	snap.ParametersJSON = paramsJSON.String
	if paramsJSON.Valid {
		_ = json.Unmarshal([]byte(paramsJSON.String), &snap.Parameters)
	}
	return &snap, nil
}

func scanSnapshotRow(rows *sql.Rows) (*store.SnapshotRecord, error) {
	return scanSnapshotRowScanner(rows.Scan)
}
