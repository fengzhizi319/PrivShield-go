// Package sqlite provides SQLite-backed DataSourceStore implementation.
// Package sqlite 提供基于 SQLite 的数据源资产存储与访问审计实现。
//
// ==============================================================================
// 【核心功能与性能优化】
// 1. 【SQL 级分页 (P28 fix)】：
//    ListDS 与 ListAudit 将 LIMIT / OFFSET 推至 SQL 引擎层执行，避免全量拉入内存截断；
// 2. 【扫描逻辑抽象复用 (P58 fix)】：
//    通过 scanDataSourceFields 统一提取 *sql.Row 与 *sql.Rows 的扫描解析逻辑，消除代码冗余；
// 3. 【标签 JSON 自动序列化】：
//    Tags 切片在落盘时自动序列化为 JSON 字符串，读取时反序列化还原；
// 4. 【时间格式标准化】：
//    时间戳统一采用 time.RFC3339Nano 格式存储与解析。
// ==============================================================================

package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/store"
)

// DataSourceStore implements store.DataSourceStore backed by SQLite.
// DataSourceStore 基于 SQLite 实现数据源资产配置与访问审计存储。
type DataSourceStore struct {
	db *sql.DB
}

// NewDataSourceStore creates a new SQLite-backed data source store.
//
// NewDataSourceStore 构建 SQLite 数据源存储实例并自动初始化表结构。
func NewDataSourceStore(db *sql.DB) (*DataSourceStore, error) {
	if err := InitDataSourceTables(db); err != nil {
		return nil, fmt.Errorf("init datasource tables: %w", err)
	}
	return &DataSourceStore{db: db}, nil
}

// SaveDS saves a data source using INSERT OR REPLACE.
//
// SaveDS 插入或全量替换数据源记录。
func (s *DataSourceStore) SaveDS(ds *store.DataSource) error {
	tagsJSON, _ := json.Marshal(ds.Tags)
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO datasources (id, name, type, host, port, database_name, security_level, status, created_at, tags_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, ds.ID, ds.Name, ds.Type, ds.Host, ds.Port, ds.Database, ds.SecurityLevel,
		ds.Status, ds.CreatedAt.Format(time.RFC3339Nano), string(tagsJSON))
	return err
}

// GetDS retrieves a data source by ID.
func (s *DataSourceStore) GetDS(id string) (*store.DataSource, error) {
	row := s.db.QueryRow(`
		SELECT id, name, type, host, port, database_name, security_level, status, created_at, last_check_at, tags_json
		FROM datasources WHERE id = ?
	`, id)
	return scanDataSource(row)
}

// ListDS returns paginated data sources ordered by created_at DESC.
//
// ListDS 在 SQL 层分页查询数据源列表，并返回总命中数。
func (s *DataSourceStore) ListDS(filter store.DataSourceFilter) ([]store.DataSource, int, error) {
	// 统计总数
	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM datasources").Scan(&total); err != nil {
		return nil, 0, err
	}

	// P28 fix: 推分页到 SQL 层执行
	query := "SELECT id, name, type, host, port, database_name, security_level, status, created_at, last_check_at, tags_json FROM datasources ORDER BY created_at DESC"
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

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	result := make([]store.DataSource, 0)
	for rows.Next() {
		ds, err := scanDataSourceRow(rows)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, *ds)
	}
	return result, total, rows.Err()
}

// DeleteDS deletes a data source by ID.
func (s *DataSourceStore) DeleteDS(id string) error {
	res, err := s.db.Exec("DELETE FROM datasources WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("data source %s not found", id)
	}
	return nil
}

// UpdateDS updates mutable fields of a data source.
func (s *DataSourceStore) UpdateDS(ds *store.DataSource) error {
	tagsJSON, _ := json.Marshal(ds.Tags)
	_, err := s.db.Exec(`
		UPDATE datasources SET name=?, type=?, host=?, port=?, database_name=?, security_level=?,
		status=?, last_check_at=?, tags_json=? WHERE id=?
	`, ds.Name, ds.Type, ds.Host, ds.Port, ds.Database, ds.SecurityLevel,
		ds.Status, nullTime(ds.LastCheckAt), string(tagsJSON), ds.ID)
	return err
}

// SaveAudit records an access audit log for a data source.
func (s *DataSourceStore) SaveAudit(rec *store.AccessAuditRecord) error {
	_, err := s.db.Exec(`
		INSERT INTO access_audit (id, datasource_id, datasource_name, operation, user_name, timestamp, records_count, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, rec.ID, rec.DataSourceID, rec.DataSourceName, rec.Operation, rec.User,
		rec.Timestamp.Format(time.RFC3339Nano), rec.RecordsCount, rec.Status)
	return err
}

// ListAudit returns paginated access audit records.
func (s *DataSourceStore) ListAudit(dsID string, limit, offset int) ([]store.AccessAuditRecord, int, error) {
	where := ""
	var args []any
	if dsID != "" {
		where = " WHERE datasource_id = ?"
		args = append(args, dsID)
	}

	// 统计总数
	countQuery := "SELECT COUNT(*) FROM access_audit" + where
	var total int
	if err := s.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// P28 fix: 推分页到 SQL 层
	query := "SELECT id, datasource_id, datasource_name, operation, user_name, timestamp, records_count, status FROM access_audit" + where + " ORDER BY timestamp DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
		if offset > 0 {
			query += fmt.Sprintf(" OFFSET %d", offset)
		}
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	records := make([]store.AccessAuditRecord, 0)
	for rows.Next() {
		var r store.AccessAuditRecord
		var ts string
		if err := rows.Scan(&r.ID, &r.DataSourceID, &r.DataSourceName, &r.Operation, &r.User,
			&ts, &r.RecordsCount, &r.Status); err != nil {
			return nil, 0, err
		}
		r.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		records = append(records, r)
	}
	return records, total, rows.Err()
}

// scanDataSourceFields scans common DataSource fields from any scanner interface.
// P58 fix: 抽象通用扫描逻辑，复用 QueryRow 与 Rows 的映射过程。
func scanDataSourceFields(scan func(dest ...any) error) (*store.DataSource, error) {
	var ds store.DataSource
	var createdAt string
	var lastCheckAt sql.NullString
	var tagsJSON sql.NullString

	err := scan(&ds.ID, &ds.Name, &ds.Type, &ds.Host, &ds.Port, &ds.Database,
		&ds.SecurityLevel, &ds.Status, &createdAt, &lastCheckAt, &tagsJSON)
	if err != nil {
		return nil, err
	}

	ds.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if lastCheckAt.Valid {
		if ts, err := time.Parse(time.RFC3339Nano, lastCheckAt.String); err == nil {
			ds.LastCheckAt = &ts
		}
	}
	if tagsJSON.Valid {
		_ = json.Unmarshal([]byte(tagsJSON.String), &ds.Tags)
	}
	return &ds, nil
}

func scanDataSource(row *sql.Row) (*store.DataSource, error) {
	return scanDataSourceFields(row.Scan)
}

func scanDataSourceRow(rows *sql.Rows) (*store.DataSource, error) {
	return scanDataSourceFields(rows.Scan)
}
