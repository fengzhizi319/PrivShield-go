// Package main implements the audit hash chain repair tool.
//
// scripts/prod/repair_hash_chain.go
//
// PrivShield 审计哈希链修复工具 (Hash Chain Repair Tool)
//
// 功能：
//  1. 扫描 SQLite 审计日志数据库，定位 9 要素哈希链断裂点
//  2. 从断点开始重新锚定 prev_hash 并重算 integrity_hash
//  3. 级联修复后续所有记录，恢复区块链式连续性
//  4. 修复后自动执行全量验真，确认链完整性恢复
//  5. 修复前自动创建数据库备份（.bak 文件）
//
// 用法：
//
//	go run scripts/prod/repair_hash_chain.go \
//	  --audit-log-db /var/lib/privshield/audit-log.db \
//	  [--dry-run] [--backup]
//
// 环境变量（替代命令行参数）：
//
//	AUDIT_LOG_DB_PATH → --audit-log-db
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // Pure-Go SQLite driver / 纯 Go SQLite 驱动

	"github.com/fengzhizi319/PrivShield/pkg/store"
)

// ─────────────────────────────────────────────────────────────
// 配置 / Configuration
// ─────────────────────────────────────────────────────────────

type config struct {
	auditDBPath string
	dryRun      bool
	backup      bool
}

func parseConfig() config {
	var cfg config
	flag.StringVar(&cfg.auditDBPath, "audit-log-db", os.Getenv("AUDIT_LOG_DB_PATH"), "Path to audit-log SQLite database (or AUDIT_LOG_DB_PATH env)")
	flag.BoolVar(&cfg.dryRun, "dry-run", false, "Scan and report broken chain without modifying data")
	flag.BoolVar(&cfg.backup, "backup", true, "Create .bak backup before repair (default: true)")
	flag.Parse()

	if cfg.auditDBPath == "" {
		log.Fatal("ERROR: --audit-log-db or AUDIT_LOG_DB_PATH is required")
	}
	return cfg
}

// ─────────────────────────────────────────────────────────────
// 审计记录结构 / Audit log row structure
// ─────────────────────────────────────────────────────────────

type auditRow struct {
	ID             string
	PrevHash       string
	IntegrityHash  string
	Timestamp      string // RFC3339Nano string from DB
	Algorithm      string
	InputHash      string
	OutputHash     string
	User           string
	SecurityLevel  string
	ParametersJSON string
}

// ─────────────────────────────────────────────────────────────
// 9 要素完整性哈希计算 / 9-element integrity hash computation
// ─────────────────────────────────────────────────────────────

// computeIntegrityHash computes the canonical SM3 integrity hash from the 9 chain elements.
// computeIntegrityHash 从 9 个链要素计算权威国密 SM3 完整性哈希。
func computeIntegrityHash(logID, prevHash, timestampStr, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON string) string {
	var ts time.Time
	var err error
	if ts, err = time.Parse(time.RFC3339Nano, timestampStr); err != nil {
		if ts, err = time.Parse(time.RFC3339, timestampStr); err != nil {
			ts = time.Now().UTC()
		}
	}
	return store.ComputeAuditIntegrityHash(logID, prevHash, ts, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON)
}

// ─────────────────────────────────────────────────────────────
// 数据库备份 / Database backup
// ─────────────────────────────────────────────────────────────

// backupDB creates a copy of the SQLite database file.
// backupDB 创建 SQLite 数据库文件的副本。
//
// The backup path is cleaned and placed in the same directory as the source
// to prevent path traversal via crafted filenames.
// 备份路径经过清理并放置在源文件同目录中，防止通过构造文件名进行路径穿越。
func backupDB(dbPath string) (string, error) {
	cleanPath := filepath.Clean(dbPath)
	dir := filepath.Dir(cleanPath)
	base := filepath.Base(cleanPath)
	bakPath := filepath.Join(dir, base+".bak."+time.Now().Format("20060102_150405"))
	src, err := os.Open(dbPath)
	if err != nil {
		return "", fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(bakPath)
	if err != nil {
		return "", fmt.Errorf("create backup: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return "", fmt.Errorf("copy data: %w", err)
	}
	return bakPath, nil
}

// ─────────────────────────────────────────────────────────────
// 链扫描与修复 / Chain scan and repair
// ─────────────────────────────────────────────────────────────

// scanChain reads all audit logs in chain order and returns them.
// scanChain 按链顺序读取所有审计日志记录并返回。
func scanChain(db *sql.DB) ([]auditRow, error) {
	rows, err := db.Query(`
		SELECT id, prev_hash, integrity_hash, timestamp, algorithm,
		       input_hash, output_hash, user_name, security_level,
		       COALESCE(parameters_json, '')
		FROM audit_logs
		ORDER BY timestamp ASC, rowid ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query audit_logs: %w", err)
	}
	defer rows.Close()

	var chain []auditRow
	for rows.Next() {
		var r auditRow
		if err := rows.Scan(&r.ID, &r.PrevHash, &r.IntegrityHash, &r.Timestamp,
			&r.Algorithm, &r.InputHash, &r.OutputHash, &r.User,
			&r.SecurityLevel, &r.ParametersJSON); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		chain = append(chain, r)
	}
	return chain, rows.Err()
}

// findBreaks scans the chain and returns indices where the chain is broken.
// findBreaks 扫描链并返回断裂处的索引。
//
// A break occurs when:
// 断裂发生在以下情况：
//  1. A record's prev_hash doesn't match the previous record's integrity_hash
//  2. A record's integrity_hash doesn't match the recomputed hash from its 9 elements
func findBreaks(chain []auditRow) []int {
	var breaks []int
	var prevIntegrityHash string

	for i, r := range chain {
		if i > 0 {
			// Check chain continuity: prev_hash must match previous integrity_hash
			// 检查链连续性：prev_hash 必须匹配前一条记录的 integrity_hash
			if r.PrevHash != prevIntegrityHash {
				breaks = append(breaks, i)
				// Recompute what the integrity_hash should be with the corrected prev_hash
				// 重算使用修正后 prev_hash 的 integrity_hash
				prevIntegrityHash = computeIntegrityHash(r.ID, prevIntegrityHash, r.Timestamp, r.Algorithm, r.InputHash, r.OutputHash, r.User, r.SecurityLevel, r.ParametersJSON)
				continue
			}
		}
		// Recompute this record's integrity_hash to verify
		// 重算本条记录的 integrity_hash 以验证
		expected := computeIntegrityHash(r.ID, r.PrevHash, r.Timestamp, r.Algorithm, r.InputHash, r.OutputHash, r.User, r.SecurityLevel, r.ParametersJSON)
		if r.IntegrityHash != "" && r.IntegrityHash != expected {
			breaks = append(breaks, i)
			prevIntegrityHash = expected
			continue
		}
		prevIntegrityHash = r.IntegrityHash
		if prevIntegrityHash == "" {
			prevIntegrityHash = expected
		}
	}
	return breaks
}

// repairChain fixes the hash chain starting from the first break point.
// repairChain 从第一个断裂点开始修复哈希链。
//
// It updates prev_hash and integrity_hash for the broken record and all
// subsequent records, then writes changes back to the database in a single
// transaction.
// 它更新断裂记录及所有后续记录的 prev_hash 和 integrity_hash，
// 然后在单个事务中将变更写回数据库。
func repairChain(db *sql.DB, chain []auditRow, breakIdx int) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`UPDATE audit_logs SET prev_hash = ?, integrity_hash = ? WHERE id = ?`)
	if err != nil {
		return 0, fmt.Errorf("prepare update: %w", err)
	}
	defer stmt.Close()

	repaired := 0
	var prevIntegrityHash string

	// Build the correct chain from the break point onward
	// 从断裂点开始构建正确的链
	// First, get the integrity hash of the record before the break
	// 首先获取断裂前一条记录的 integrity hash
	if breakIdx > 0 {
		prev := chain[breakIdx-1]
		prevIntegrityHash = prev.IntegrityHash
		if prevIntegrityHash == "" {
			prevIntegrityHash = computeIntegrityHash(prev.ID, prev.PrevHash, prev.Timestamp, prev.Algorithm, prev.InputHash, prev.OutputHash, prev.User, prev.SecurityLevel, prev.ParametersJSON)
		}
	}

	for i := breakIdx; i < len(chain); i++ {
		r := &chain[i]
		newPrevHash := prevIntegrityHash
		newIntegrityHash := computeIntegrityHash(r.ID, newPrevHash, r.Timestamp, r.Algorithm, r.InputHash, r.OutputHash, r.User, r.SecurityLevel, r.ParametersJSON)

		if _, err := stmt.Exec(newPrevHash, newIntegrityHash, r.ID); err != nil {
			return 0, fmt.Errorf("update record %s: %w", r.ID, err)
		}
		prevIntegrityHash = newIntegrityHash
		repaired++
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return repaired, nil
}

// verifyChain performs a full chain verification after repair.
// verifyChain 在修复后执行全量链验证。
func verifyChain(db *sql.DB) error {
	chain, err := scanChain(db)
	if err != nil {
		return err
	}
	breaks := findBreaks(chain)
	if len(breaks) > 0 {
		return fmt.Errorf("chain still broken at %d locations after repair", len(breaks))
	}
	return nil
}

// ─────────────────────────────────────────────────────────────
// 主流程 / Main
// ─────────────────────────────────────────────────────────────

func main() {
	cfg := parseConfig()

	// Validate DB path / 校验数据库路径
	if _, err := os.Stat(cfg.auditDBPath); os.IsNotExist(err) {
		log.Fatalf("ERROR: audit-log database not found: %s", cfg.auditDBPath)
	}

	// Clean and resolve to absolute path to prevent path traversal
	// 清理并解析为绝对路径，防止路径穿越
	absPath, err := filepath.Abs(filepath.Clean(cfg.auditDBPath))
	if err != nil {
		log.Fatalf("ERROR: resolve path: %v", err)
	}
	log.Printf("=== PrivShield Audit Hash Chain Repair Tool ===")
	log.Printf("Database: %s", absPath)
	if cfg.dryRun {
		log.Printf("Mode: DRY RUN (no modifications)")
	}

	// Open database / 打开数据库
	db, err := sql.Open("sqlite", absPath)
	if err != nil {
		log.Fatalf("ERROR: open database: %v", err)
	}
	defer db.Close()

	// Step 1: Scan chain / 扫描链
	chain, err := scanChain(db)
	if err != nil {
		log.Fatalf("ERROR: scan chain: %v", err)
	}
	log.Printf("Total audit records: %d", len(chain))

	if len(chain) == 0 {
		log.Printf("No records to verify. Chain is empty.")
		return
	}

	// Step 2: Find breaks / 定位断裂点
	breaks := findBreaks(chain)
	if len(breaks) == 0 {
		log.Printf("✅ Chain is VALID: all %d records verified, no breaks detected.", len(chain))
		return
	}

	log.Printf("❌ Chain is BROKEN: %d break(s) detected", len(breaks))
	for i, idx := range breaks {
		r := chain[idx]
		log.Printf("  Break #%d at record index %d (ID: %s)", i+1, idx, r.ID)
		if idx > 0 {
			prev := chain[idx-1]
			log.Printf("    Expected prev_hash: %s", prev.IntegrityHash)
			log.Printf("    Actual   prev_hash: %s", r.PrevHash)
		}
	}

	if cfg.dryRun {
		log.Printf("DRY RUN: would repair %d records starting from index %d", len(chain)-breaks[0], breaks[0])
		return
	}

	// Step 3: Backup before repair / 修复前备份
	if cfg.backup {
		bakPath, err := backupDB(absPath)
		if err != nil {
			log.Fatalf("ERROR: backup failed: %v", err)
		}
		log.Printf("📦 Backup created: %s", bakPath)
	}

	// Step 4: Repair / 执行修复
	startIdx := breaks[0]
	log.Printf("🔧 Repairing chain from index %d (%d records)...", startIdx, len(chain)-startIdx)
	repaired, err := repairChain(db, chain, startIdx)
	if err != nil {
		log.Fatalf("ERROR: repair failed: %v", err)
	}
	log.Printf("✅ Repaired %d records", repaired)

	// Step 5: Verify after repair / 修复后验真
	log.Printf("🔍 Verifying chain after repair...")
	if err := verifyChain(db); err != nil {
		log.Fatalf("❌ Post-repair verification FAILED: %v", err)
	}
	log.Printf("✅ Post-repair verification PASSED: all %d records form a valid chain", len(chain))
	log.Printf("=== Repair complete ===")
}
