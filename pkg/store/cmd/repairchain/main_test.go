package main

import (
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/store"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/sqlite"
)

const testKey = "managed-evidence-chain-key-0123456789"

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// seedChain 用真实存储层写入 n 条带快照的审计记录，保证 schema 与哈希口径与生产一致。
func seedChain(t *testing.T, n int) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "audit-log.db")
	db, err := sqlite.Open(dbPath, quietLogger())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	as, err := sqlite.NewAuditStore(db)
	if err != nil {
		t.Fatalf("new audit store: %v", err)
	}
	base := time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		id := "log-" + string(rune('a'+i))
		l := &store.AuditLog{
			ID: id, TaskID: "task-1", APICode: "yibao", DatasourceID: "ds-1",
			Timestamp: base.Add(time.Duration(i) * time.Minute),
			Algorithm: "SM4-GCM", InputHash: "in-" + id, OutputHash: "out-" + id,
			User: "auditor", SecurityLevel: "L3", Status: "success",
			ParametersJSON: `{"fields":["phone"]}`,
		}
		snap := &store.SnapshotRecord{
			ID: "snap-" + id, AuditLogID: id, Timestamp: l.Timestamp,
			InputSample: "raw-" + id, OutputSample: "masked-" + id,
			Algorithm: "SM4-GCM", ParametersJSON: `{"fields":["phone"]}`,
		}
		if err := as.SaveLogWithSnapshot(l, snap); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}
	return dbPath
}

func withKey(t *testing.T, key string) {
	t.Helper()
	prev := store.AuditChainKey()
	t.Cleanup(func() { store.SetAuditChainKey(prev) })
	store.SetAuditChainKey(key)
}

func readChain(t *testing.T, dbPath string) []auditRow {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	chain, err := scanChain(db)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return chain
}

func TestParseConfigRejectsBadFlags(t *testing.T) {
	t.Setenv("AUDIT_LOG_DB_PATH", "")
	t.Setenv("AUDIT_LOG_HASH_KEY", "")
	withKey(t, "")
	if _, err := parseConfig([]string{"-audit-log-db", "x.db", "-mode", "wipe"}); err == nil {
		t.Fatal("unknown mode must be rejected")
	}
	if _, err := parseConfig([]string{"-mode", "resign", "-backup=false", "-audit-log-db", "x.db"}); err == nil {
		t.Fatal("write mode without backup must be rejected")
	}
	if _, err := parseConfig([]string{}); err == nil {
		t.Fatal("missing database path must be rejected")
	}
	cfg, err := parseConfig([]string{"-audit-log-db", "x.db", "-mode", "resign", "-hash-key", testKey})
	if err != nil {
		t.Fatalf("valid flags rejected: %v", err)
	}
	if cfg.mode != modeResign || store.AuditChainKey() != testKey {
		t.Fatalf("cfg=%+v key=%q", cfg, store.AuditChainKey())
	}
}

func TestVerifyAcceptsCanonicalUnkeyedChain(t *testing.T) {
	withKey(t, "")
	dbPath := seedChain(t, 3)
	if err := run([]string{"-audit-log-db", dbPath}, quietLogger()); err != nil {
		t.Fatalf("verify on a canonical un-keyed chain: %v", err)
	}
	diag, err := classify(readChain(t, dbPath))
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if diag.total != 3 || diag.canonical != 3 || !diag.clean() {
		t.Fatalf("diag=%+v", diag)
	}
}

func TestResignUpgradesLegacyChainToKeyedConvention(t *testing.T) {
	withKey(t, "")
	dbPath := seedChain(t, 3)
	unkeyed := readChain(t, dbPath)

	withKey(t, "")
	err := run([]string{"-audit-log-db", dbPath, "-mode", "verify", "-hash-key", testKey}, quietLogger())
	if err == nil || !strings.Contains(err.Error(), "legacy") {
		t.Fatalf("verify after keying must report pending re-signing, got %v", err)
	}

	if err := run([]string{"-audit-log-db", dbPath, "-mode", "resign", "-hash-key", testKey}, quietLogger()); err != nil {
		t.Fatalf("resign: %v", err)
	}
	resigned := readChain(t, dbPath)
	if len(resigned) != len(unkeyed) {
		t.Fatalf("row count changed: %d → %d", len(unkeyed), len(resigned))
	}
	diag, err := classify(resigned)
	if err != nil {
		t.Fatalf("classify after resign: %v", err)
	}
	if !diag.clean() || diag.canonical != len(unkeyed) {
		t.Fatalf("post-resign diag=%+v", diag)
	}
	for i, r := range resigned {
		if r.IntegrityHash == unkeyed[i].IntegrityHash {
			t.Fatalf("record %s hash unchanged by resign", r.ID)
		}
		if i > 0 && r.PrevHash != resigned[i-1].IntegrityHash {
			t.Fatalf("record %s prev_hash not re-anchored to the new upstream hash", r.ID)
		}
	}

	// 重签必须同步刷新 snapshots 镜像哈希，否则快照核验会因锚点漂移失败。
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT s.audit_log_id, s.prev_hash, s.integrity_hash, a.prev_hash, a.integrity_hash FROM snapshots s JOIN audit_logs a ON a.id = s.audit_log_id`)
	if err != nil {
		t.Fatalf("query snapshots: %v", err)
	}
	defer rows.Close()
	pairs := 0
	for rows.Next() {
		var id, sp, si, ap, ai string
		if err := rows.Scan(&id, &sp, &si, &ap, &ai); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if sp != ap || si != ai {
			t.Fatalf("snapshot of %s not re-signed: snap=(%s,%s) log=(%s,%s)", id, sp, si, ap, ai)
		}
		pairs++
	}
	if pairs != len(unkeyed) {
		t.Fatalf("expected %d snapshot pairs, got %d", len(unkeyed), pairs)
	}

	// 已重签的链在密钥下可再次验真，且保留无密钥历史口径候选仍可核验的能力。
	if _, label := store.VerifyAuditIntegrityHash(resigned[0].IntegrityHash, resigned[0].ID, resigned[0].PrevHash, resigned[0].mustTime(t), resigned[0].Algorithm, resigned[0].InputHash, resigned[0].OutputHash, resigned[0].User, resigned[0].SecurityLevel, resigned[0].ParametersJSON); label != store.AuditHashSM3HMAC {
		t.Fatalf("resigned row label = %q, want %q", label, store.AuditHashSM3HMAC)
	}
}

func TestTamperedRecordIsRefusedNotResigned(t *testing.T) {
	withKey(t, "")
	dbPath := seedChain(t, 3)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`UPDATE audit_logs SET output_hash = 'attacker-value' WHERE id = 'log-b'`); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	db.Close()

	err = run([]string{"-audit-log-db", dbPath}, quietLogger())
	if err == nil || !strings.Contains(err.Error(), "forensics") {
		t.Fatalf("tampered chain must abort with a forensics hint, got %v", err)
	}
	before := readChain(t, dbPath)
	if err := run([]string{"-audit-log-db", dbPath, "-mode", "resign", "-hash-key", testKey}, quietLogger()); err == nil {
		t.Fatal("resign must refuse to launder a tampered record")
	}
	after := readChain(t, dbPath)
	for i := range before {
		if before[i].IntegrityHash != after[i].IntegrityHash {
			t.Fatalf("tampered run rewrote record %s hashes", before[i].ID)
		}
	}
}

func TestRepairReanchorsBrokenChain(t *testing.T) {
	withKey(t, "")
	dbPath := seedChain(t, 4)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// 模拟上游记录被物理删除：把 log-c 的锚点改成孤立值，内容本身保持真实。
	if _, err := db.Exec(`UPDATE audit_logs SET prev_hash = ? WHERE id = 'log-c'`, strings.Repeat("f", 64)); err != nil {
		t.Fatalf("break anchor: %v", err)
	}
	db.Close()

	if err := run([]string{"-audit-log-db", dbPath}, quietLogger()); err == nil {
		t.Fatal("verify must report the broken anchor")
	}
	if err := run([]string{"-audit-log-db", dbPath, "-mode", "repair"}, quietLogger()); err != nil {
		t.Fatalf("repair: %v", err)
	}
	chain := readChain(t, dbPath)
	diag, err := classify(chain)
	if err != nil {
		t.Fatalf("classify after repair: %v", err)
	}
	if !diag.clean() {
		t.Fatalf("post-repair diag=%+v", diag)
	}
	for i := range chain {
		if i == 0 {
			continue
		}
		if chain[i].PrevHash != chain[i-1].IntegrityHash {
			t.Fatalf("record %s still mis-anchored after repair", chain[i].ID)
		}
	}
}

func TestNonRoundTripTimestampIsRefused(t *testing.T) {
	withKey(t, "")
	// 驱动会在写入时归一化时间戳，因此只能在内存中构造异常口径，验证重算前置守卫。
	padded := auditRow{ID: "log-a", Timestamp: "2026-05-01T08:00:00.000000000Z"}
	if _, err := padded.parsedTime(); err == nil {
		t.Fatal("zero-padded nanoseconds must be refused: the write side never formats them")
	}
	malformed := auditRow{ID: "log-b", Timestamp: "2026-05-01 08:00:00"}
	if _, err := malformed.parsedTime(); err == nil {
		t.Fatal("non-RFC3339 timestamp must be refused")
	}
	if _, err := classify([]auditRow{padded}); err == nil {
		t.Fatal("classify must abort instead of re-hashing an uninterpretable timestamp")
	}
}

func (r auditRow) mustTime(t *testing.T) time.Time {
	t.Helper()
	ts, err := r.parsedTime()
	if err != nil {
		t.Fatalf("parsedTime: %v", err)
	}
	return ts
}
