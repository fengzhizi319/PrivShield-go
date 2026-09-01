// Command repairchain audits, repairs and re-signs the PrivShield evidence hash chain in SQLite.
// Command repairchain 核验、修复并重签 SQLite 存证哈希链。
//
// ==============================================================================
// 【三种模式 / -mode】
//
//	verify（默认，只读）
//		按链序扫描全量存证，逐条判定四种状态并输出诊断报告，绝不写库：
//		  canonical  内容与链锚点均匹配当前写入口径；
//		  legacy     内容真实但哈希属于历史口径（无密钥 SM3 / SHA-256 / 本机时区），需要重签；
//		  re-anchor  内容真实但 prev_hash 与上一条 integrity_hash 不衔接（上游被删除或重排）；
//		  tampered   任何候选口径都无法复原该记录的哈希 —— 内容已被改写。
//
//	repair
//		从第一个锚点断裂处起重新锚定 prev_hash 并级联重算 integrity_hash，用于恢复被误删/重排的链。
//
//	resign
//		注入存证 HMAC 密钥（-hash-key / AUDIT_LOG_HASH_KEY，局方托管）后，
//		把存量记录升级为 `SM3-HMAC:v1` 密钥化口径：从第 0 条起级联重签，
//		并同步刷新 snapshots 表中镜像该记录的 prev_hash / integrity_hash。
//
// 【安全红线】
//  1. tampered 记录一律拒绝写入修复：重签会把篡改痕迹洗成「合法」，必须交由取证流程处理；
//  2. 写入模式默认先复制 .bak 备份，再在单事务内提交，任一失败整体回滚；
//  3. 时间戳必须能与写入侧 RFC3339Nano 口径逐字节往返，否则拒绝重算（防止静默改哈希）。
//
// 【用法 / Usage】
//
//	go run ./pkg/store/cmd/repairchain -audit-log-db /var/lib/privshield/audit-log.db
//	go run ./pkg/store/cmd/repairchain -audit-log-db ... -mode=resign -hash-key "$AUDIT_LOG_HASH_KEY"
//
// 环境变量（优先级低于同名命令行参数）：
//
//	AUDIT_LOG_DB_PATH → -audit-log-db
//	AUDIT_LOG_HASH_KEY → -hash-key
//
// ==============================================================================
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // Pure-Go SQLite driver / 纯 Go SQLite 驱动

	"github.com/fengzhizi319/PrivShield-go/pkg/store"
)

// 存证链扫描支持的模式。
const (
	modeVerify = "verify"
	modeRepair = "repair"
	modeResign = "resign"
)

// 单条记录的诊断状态。
const (
	stateCanonical = "canonical"
	stateLegacy    = "legacy"
	stateReanchor  = "re-anchor"
	stateTampered  = "tampered"
)

// config 是命令行解析出的运行参数。
type config struct {
	auditDBPath string
	hashKey     string
	mode        string
	backup      bool
}

// parseConfig 解析命令行参数并注入进程级存证 HMAC 密钥。
func parseConfig(args []string) (*config, error) {
	var cfg config
	fs := flag.NewFlagSet("repairchain", flag.ContinueOnError)
	fs.StringVar(&cfg.auditDBPath, "audit-log-db", os.Getenv("AUDIT_LOG_DB_PATH"), "Path to audit-log SQLite database (or AUDIT_LOG_DB_PATH env)")
	fs.StringVar(&cfg.hashKey, "hash-key", os.Getenv("AUDIT_LOG_HASH_KEY"), "Evidence chain HMAC key (or AUDIT_LOG_HASH_KEY env); empty keeps the un-keyed SM3 convention")
	fs.StringVar(&cfg.mode, "mode", modeVerify, "Chain operation: verify (read-only), repair (fix broken anchors), resign (upgrade to the active hash convention)")
	fs.BoolVar(&cfg.backup, "backup", true, "Create a .bak copy of the database before any write mode")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	switch cfg.mode {
	case modeVerify, modeRepair, modeResign:
	default:
		return nil, fmt.Errorf("invalid -mode %q (want verify, repair or resign)", cfg.mode)
	}
	if cfg.mode != modeVerify && !cfg.backup {
		return nil, fmt.Errorf("write mode %q requires a backup: remove -backup=false", cfg.mode)
	}
	if strings.TrimSpace(cfg.auditDBPath) == "" {
		return nil, fmt.Errorf("-audit-log-db (or AUDIT_LOG_DB_PATH) is required")
	}
	store.SetAuditChainKey(cfg.hashKey)
	return &cfg, nil
}

// auditRow 是审计日志链上单条记录的可重算要素。
type auditRow struct {
	ID             string
	PrevHash       string
	IntegrityHash  string
	Timestamp      string // RFC3339Nano 原文，重算前需逐字节往返
	Algorithm      string
	InputHash      string
	OutputHash     string
	User           string
	SecurityLevel  string
	ParametersJSON string
}

// parsedTime 解析时间戳并强制其与写入侧口径逐字节往返。
func (r auditRow) parsedTime() (time.Time, error) {
	ts, err := time.Parse(time.RFC3339Nano, r.Timestamp)
	if err != nil {
		return time.Time{}, fmt.Errorf("record %s timestamp %q is not RFC3339Nano: %w", r.ID, r.Timestamp, err)
	}
	if got := ts.Format(time.RFC3339Nano); got != r.Timestamp {
		return time.Time{}, fmt.Errorf("record %s timestamp %q does not round-trip (parsed as %q); refusing to recompute its hash", r.ID, r.Timestamp, got)
	}
	return ts, nil
}

func (r auditRow) compute(prevHash string, ts time.Time) string {
	return store.ComputeAuditIntegrityHash(r.ID, prevHash, ts, r.Algorithm, r.InputHash, r.OutputHash, r.User, r.SecurityLevel, r.ParametersJSON)
}

func (r auditRow) verify(stored, prevHash string, ts time.Time) (bool, string) {
	return store.VerifyAuditIntegrityHash(stored, r.ID, prevHash, ts, r.Algorithm, r.InputHash, r.OutputHash, r.User, r.SecurityLevel, r.ParametersJSON)
}

// diagnosis 是链扫描的汇总结果。
type diagnosis struct {
	total     int
	canonical int
	legacy    []string
	reanchor  []string
	tampered  []string
	states    []string
}

func (d *diagnosis) clean() bool {
	return len(d.legacy) == 0 && len(d.reanchor) == 0 && len(d.tampered) == 0
}

func (d *diagnosis) needsWrite() bool {
	return len(d.reanchor) > 0 || len(d.legacy) > 0
}

// scanChain 按规范化链序 `(seq ASC, timestamp ASC, id ASC)` 读取全量审计日志。
//
// 该排序与存储层 SQLite/PostgreSQL 的 VerifyChain、GetLatestLog 链尾裁定完全同源（P2-4）：
// 早期实现以 `timestamp ASC, rowid ASC` 扫描，首要次序与存储层的单调锚点序 `seq` 不一致，
// 会使重签工具与在线验真对同一批存证得出不同链序，进而误判断链或按错误顺序级联重签。
func scanChain(db *sql.DB) ([]auditRow, error) {
	rows, err := db.Query(`
		SELECT id, prev_hash, integrity_hash, timestamp, algorithm,
		       input_hash, output_hash, user_name, security_level,
		       COALESCE(parameters_json, '')
		FROM audit_logs
		ORDER BY seq ASC, timestamp ASC, id ASC
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

// classify 逐条判定记录状态：先用自身 prev_hash 核验内容真伪，再校验与上一条 integrity_hash 的衔接。
//
// 内容核验对历史口径保持兼容（无密钥 SM3 / SHA-256 / 本机时区变体），
// 因此 legacy 表示「证据真实但需升级到当前写入口径」，而非失效。
func classify(chain []auditRow) (*diagnosis, error) {
	d := &diagnosis{total: len(chain), states: make([]string, len(chain))}
	var prevStored string
	for i, r := range chain {
		ts, err := r.parsedTime()
		if err != nil {
			return nil, err
		}

		ok, label := r.verify(r.IntegrityHash, r.PrevHash, ts)
		switch {
		case ok && (i == 0 || r.PrevHash == prevStored):
			if store.IsCanonicalHashLabel(label) {
				d.states[i] = stateCanonical
				d.canonical++
			} else {
				d.states[i] = stateLegacy
				d.legacy = append(d.legacy, r.ID)
			}
		case ok:
			// 哈希与自身 prev_hash 相符，但与上游 integrity_hash 不衔接：上游被删除或重排过。
			d.states[i] = stateReanchor
			d.reanchor = append(d.reanchor, r.ID)
		default:
			// 换用期望锚点再验一次：命中说明仅锚点被改写，内容仍然真实。
			expectedPrev := r.PrevHash
			if i > 0 {
				expectedPrev = prevStored
			}
			if ok2, _ := r.verify(r.IntegrityHash, expectedPrev, ts); ok2 && expectedPrev != r.PrevHash {
				d.states[i] = stateReanchor
				d.reanchor = append(d.reanchor, r.ID)
			} else {
				d.states[i] = stateTampered
				d.tampered = append(d.tampered, r.ID)
			}
		}

		prevStored = r.IntegrityHash
		if prevStored == "" {
			prevStored = r.compute(r.PrevHash, ts)
		}
	}
	return d, nil
}

// reSign 从 firstIdx 起重新锚定并重算 integrity_hash（以及镜像该记录的 snapshots），单事务提交。
//
// 创世记录（下标 0）保留其自身 prev_hash，避免重签改变链起点锚定口径。
func reSign(db *sql.DB, chain []auditRow, firstIdx int, logger *slog.Logger) (int, error) {
	for i := firstIdx; i < len(chain); i++ {
		if _, err := chain[i].parsedTime(); err != nil {
			return 0, err
		}
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	logStmt, err := tx.Prepare(`UPDATE audit_logs SET prev_hash = ?, integrity_hash = ? WHERE id = ?`)
	if err != nil {
		return 0, fmt.Errorf("prepare log update: %w", err)
	}
	defer logStmt.Close()
	snapStmt, err := tx.Prepare(`UPDATE snapshots SET prev_hash = ?, integrity_hash = ? WHERE audit_log_id = ?`)
	if err != nil {
		return 0, fmt.Errorf("prepare snapshot update: %w", err)
	}
	defer snapStmt.Close()

	prevIntegrity := ""
	if firstIdx > 0 {
		prev := chain[firstIdx-1]
		prevIntegrity = prev.IntegrityHash
		if prevIntegrity == "" {
			ts, err := prev.parsedTime()
			if err != nil {
				return 0, err
			}
			prevIntegrity = prev.compute(prev.PrevHash, ts)
		}
	}

	repaired := 0
	for i := firstIdx; i < len(chain); i++ {
		r := &chain[i]
		newPrev := prevIntegrity
		if i == 0 {
			newPrev = r.PrevHash
		}
		ts, err := r.parsedTime()
		if err != nil {
			return 0, err
		}
		newHash := r.compute(newPrev, ts)
		if _, err := logStmt.Exec(newPrev, newHash, r.ID); err != nil {
			return 0, fmt.Errorf("update record %s: %w", r.ID, err)
		}
		// snapshots 的 prev_hash / integrity_hash 镜像所属审计日志记录，必须同步刷新，
		// 否则重签后快照核验会因锚点漂移而失败。
		if _, err := snapStmt.Exec(newPrev, newHash, r.ID); err != nil {
			return 0, fmt.Errorf("update snapshot of %s: %w", r.ID, err)
		}
		r.PrevHash, r.IntegrityHash = newPrev, newHash
		prevIntegrity = newHash
		repaired++
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	logger.Info("chain rewritten", "records", repaired, "from_index", firstIdx, "hash_algo", store.ComputeAuditIntegrityHashAlgo())
	return repaired, nil
}

// backupDB 在源文件同目录创建数据库副本，路径经清理以防构造式穿越。
func backupDB(dbPath string) (string, error) {
	cleanPath := filepath.Clean(dbPath)
	bakPath := filepath.Join(filepath.Dir(cleanPath), filepath.Base(cleanPath)+".bak."+time.Now().Format("20060102_150405"))
	src, err := os.Open(cleanPath)
	if err != nil {
		return "", fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	dst, err := os.OpenFile(bakPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("create backup: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return "", fmt.Errorf("copy data: %w", err)
	}
	if err := dst.Sync(); err != nil {
		return "", fmt.Errorf("sync backup: %w", err)
	}
	return bakPath, nil
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(os.Args[1:], logger); err != nil {
		logger.Error("repairchain failed", "error", err.Error())
		os.Exit(1)
	}
}

func run(args []string, logger *slog.Logger) error {
	cfg, err := parseConfig(args)
	if err != nil {
		return err
	}
	absPath, err := filepath.Abs(filepath.Clean(cfg.auditDBPath))
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	if _, err := os.Stat(absPath); err != nil {
		return fmt.Errorf("audit-log database not readable: %w", err)
	}

	logger.Info("PrivShield evidence hash chain tool",
		"database", absPath, "mode", cfg.mode,
		"hash_algo", store.ComputeAuditIntegrityHashAlgo(),
		"keyed", store.AuditChainKey() != "")

	db, err := sql.Open("sqlite", absPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	chain, err := scanChain(db)
	if err != nil {
		return err
	}
	if len(chain) == 0 {
		logger.Info("chain is empty, nothing to do")
		return nil
	}

	diag, err := classify(chain)
	if err != nil {
		return err
	}
	report(logger, diag)

	if len(diag.tampered) > 0 {
		return fmt.Errorf("%d record(s) failed every accepted hash convention (content modified): %s — re-signing would erase the tampering evidence, hand this to the forensics process instead",
			len(diag.tampered), strings.Join(diag.tampered, ","))
	}
	if cfg.mode == modeVerify {
		if diag.needsWrite() {
			return fmt.Errorf("chain needs attention: %d re-anchor and %d legacy record(s); run with -mode=repair or -mode=resign", len(diag.reanchor), len(diag.legacy))
		}
		return nil
	}
	if !diag.needsWrite() {
		logger.Info("chain already canonical, no rewrite needed")
		return nil
	}

	// WAL 模式下尾部记录可能仍停留在 -wal 侧文件中，先强制 checkpoint 才能保证备份是可还原的全量副本。
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("wal checkpoint before backup: %w", err)
	}
	bakPath, err := backupDB(absPath)
	if err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}
	logger.Info("backup created", "path", bakPath)

	firstIdx := 0
	if cfg.mode == modeRepair && len(diag.reanchor) > 0 {
		firstIdx = indexOf(chain, diag.reanchor[0])
	}
	if _, err := reSign(db, chain, firstIdx, logger); err != nil {
		return err
	}

	chain, err = scanChain(db)
	if err != nil {
		return err
	}
	post, err := classify(chain)
	if err != nil {
		return fmt.Errorf("post-write verification failed: %w", err)
	}
	if !post.clean() {
		report(logger, post)
		return fmt.Errorf("chain still not canonical after %s: %d re-anchor, %d legacy, %d tampered",
			cfg.mode, len(post.reanchor), len(post.legacy), len(post.tampered))
	}
	logger.Info("chain is valid and canonical", "records", post.total, "hash_algo", store.ComputeAuditIntegrityHashAlgo())
	return nil
}

func report(logger *slog.Logger, d *diagnosis) {
	logger.Info("scan summary",
		"records", d.total, "canonical", d.canonical,
		"legacy", len(d.legacy), "re_anchor", len(d.reanchor), "tampered", len(d.tampered),
		"active_hash_algo", store.ComputeAuditIntegrityHashAlgo())
	if len(d.legacy) > 0 {
		logger.Warn("records authentic but hashed under a superseded convention (re-sign to upgrade)",
			"count", len(d.legacy), "sample", sample(d.legacy))
	}
	if len(d.reanchor) > 0 {
		logger.Error("chain anchoring broken at these records", "count", len(d.reanchor), "sample", sample(d.reanchor))
	}
	if len(d.tampered) > 0 {
		logger.Error("record content does not match its stored hash under any convention",
			"count", len(d.tampered), "sample", sample(d.tampered))
	}
}

func sample(ids []string) string {
	if len(ids) > 5 {
		return strings.Join(ids[:5], ",") + ",…"
	}
	return strings.Join(ids, ",")
}

func indexOf(chain []auditRow, id string) int {
	for i := range chain {
		if chain[i].ID == id {
			return i
		}
	}
	return 0
}
