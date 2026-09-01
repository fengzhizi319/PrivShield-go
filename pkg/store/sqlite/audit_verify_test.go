// P2-4 验真读路径回归测试 / Regression tests for the tamper-evidence verification read path.
//
// 【验证目标 / what these tests pin down】
//  1. 【reason 枚举】：验真结论必须以机器可读的 `ChainVerificationResult.Reason` 表达
//     （ok / legacy_hashed / tampered_payload / hash_mismatch / missing_prev），
//     看板无需再解析英文 `message` 字符串；
//  2. 【legacy_hashed 透出】：写入于密钥化口径之前的无密钥 SM3 存证，注入 `AUDIT_LOG_HASH_KEY`
//     后必须判为「已验真、仅待重签」（valid=true），**绝不可误判为篡改**；
//  3. 【fail-closed 不弱化】：真实改写（业务字段原位篡改 / 锚点被替换）仍必须 valid=false；
//  4. 【规范化链序确定性】：同时间戳记录在 `(seq ASC, timestamp ASC, id ASC)` 链序下必须
//     以唯一且可复现的顺序回放，且与写入侧链尾裁定互逆，杜绝误报断链。

package sqlite_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/store"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/sqlite"
)

// verifyTestChainKey 是模拟「局方托管存证 HMAC 密钥」的测试密钥。
const verifyTestChainKey = "局方托管存证密钥-P2-4-32bytes-min-length"

// bogusHash64 是一条长度合法但由攻击者伪造的前序哈希，用于模拟锚点被替换。
const bogusHash64 = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"

// setupVerifyStore 打开临时 SQLite 库，返回审计存储与底层连接（后者用于模拟库内篡改）。
func setupVerifyStore(t *testing.T) (*sqlite.AuditStore, *sql.DB) {
	t.Helper()
	dbPath := openTestDB(t)
	db, err := sqlite.Open(dbPath, testLogger())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	as, err := sqlite.NewAuditStore(db)
	if err != nil {
		t.Fatalf("new audit store: %v", err)
	}
	return as, db
}

// withChainKey 临时注入进程级存证 HMAC 密钥并在用例结束后还原（密钥为全局状态，必须还原）。
func withChainKey(t *testing.T, key string) {
	t.Helper()
	prev := store.AuditChainKey()
	t.Cleanup(func() { store.SetAuditChainKey(prev) })
	store.SetAuditChainKey(key)
}

// putChainLog 写入一条审计日志，其 prev_hash 与 integrity_hash 均由存储层链尾裁定自动赋予。
func putChainLog(t *testing.T, as *sqlite.AuditStore, id string, ts time.Time, inputHash, outputHash string) *store.AuditLog {
	t.Helper()
	l := &store.AuditLog{
		ID:            id,
		Timestamp:     ts,
		Operation:     "mask",
		Algorithm:     "field_mask",
		DataSource:    "ds_yibao",
		DatasourceID:  "ds_yibao",
		InputHash:     inputHash,
		OutputHash:    outputHash,
		User:          "auditor",
		Status:        "success",
		SecurityLevel: "L3",
	}
	if err := as.SaveLog(l); err != nil {
		t.Fatalf("save log %s: %v", id, err)
	}
	return l
}

// mustVerify 执行全量链核验并在出错时立即失败。
func mustVerify(t *testing.T, as *sqlite.AuditStore) *store.ChainVerificationResult {
	t.Helper()
	res, err := as.VerifyChain(0)
	if err != nil {
		t.Fatalf("verify chain: %v", err)
	}
	return res
}

// mustExec 直接在底层库上执行篡改语句，模拟「绕过服务端的物理改库」攻击。
func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// TestAuditStore_VerifyChain_ReportsLegacyHashedForUnkeyedRecord 覆盖 P2-4 缺口 (a)(b)：
// 无密钥 SM3 时代写入的存证，在注入存证密钥后必须被报为 `legacy_hashed`——
// 链仍然有效（验真通过），但这些记录以历史候选口径验真、仍需重签，不得与真实失配混为一谈。
func TestAuditStore_VerifyChain_ReportsLegacyHashedForUnkeyedRecord(t *testing.T) {
	withChainKey(t, "") // 迁移前：未配置密钥，按无密钥 SM3 口径写入
	as, _ := setupVerifyStore(t)
	now := time.Now().UTC()
	putChainLog(t, as, "legacy-1", now, "in-1", "out-1")
	putChainLog(t, as, "legacy-2", now.Add(time.Second), "in-2", "out-2")

	// 基线：无密钥态下全部记录均属当前规范口径。
	if res := mustVerify(t, as); res.Reason != store.ChainReasonOK || res.LegacyHashed != 0 {
		t.Fatalf("un-keyed baseline: reason=%q legacy_hashed=%d, want %q and 0", res.Reason, res.LegacyHashed, store.ChainReasonOK)
	}

	withChainKey(t, verifyTestChainKey) // 上线密钥化口径后回验历史证据

	res := mustVerify(t, as)
	if !res.Valid {
		t.Fatalf("legacy evidence must stay verified, got valid=false reason=%q msg=%s", res.Reason, res.Message)
	}
	if res.Reason != store.ChainReasonLegacyHashed {
		t.Fatalf("reason = %q, want %q", res.Reason, store.ChainReasonLegacyHashed)
	}
	if res.LegacyHashed != 2 {
		t.Fatalf("legacy_hashed = %d, want 2", res.LegacyHashed)
	}
	if res.TotalVerified != 2 || res.TotalRecords != 2 {
		t.Fatalf("verified/records = %d/%d, want 2/2", res.TotalVerified, res.TotalRecords)
	}
	if res.BrokenAtID != "" {
		t.Fatalf("pending re-signing must not report a break point, got broken_at_id=%q", res.BrokenAtID)
	}
}

// TestAuditStore_VerifyChain_ReportsTamperedPayload 覆盖缺口 (a)：记录被「原位改写业务字段」
// （锚点仍与上游衔接）时必须报 `tampered_payload`，并保持 fail-closed 判定无效。
func TestAuditStore_VerifyChain_ReportsTamperedPayload(t *testing.T) {
	as, db := setupVerifyStore(t)
	now := time.Now().UTC()
	putChainLog(t, as, "intact-1", now, "in-1", "out-1")
	putChainLog(t, as, "victim-2", now.Add(time.Second), "in-2", "out-2")

	mustExec(t, db, `UPDATE audit_logs SET input_hash = ? WHERE id = ?`, "in-2-TAMPERED", "victim-2")

	res := mustVerify(t, as)
	if res.Valid {
		t.Fatalf("tampered payload must stay invalid, got valid=true msg=%s", res.Message)
	}
	if res.Reason != store.ChainReasonTamperedPayload {
		t.Fatalf("reason = %q, want %q", res.Reason, store.ChainReasonTamperedPayload)
	}
	if res.BrokenAtID != "victim-2" {
		t.Fatalf("broken_at_id = %q, want victim-2", res.BrokenAtID)
	}
	if res.ExpectedHash == "" || res.ActualHash == "" || res.ExpectedHash == res.ActualHash {
		t.Fatalf("expected/actual must both be reported and differ, got %q vs %q", res.ExpectedHash, res.ActualHash)
	}
	if res.TotalRecords != 2 {
		t.Fatalf("total_records = %d, want 2", res.TotalRecords)
	}
}

// TestAuditStore_VerifyChain_ReportsHashMismatchOnRewrittenAnchor 区分缺口 (a) 的另一种失配：
// `prev_hash` 被整体替换（锚点与内容同时分叉）时报 `hash_mismatch`，而非原位篡改。
func TestAuditStore_VerifyChain_ReportsHashMismatchOnRewrittenAnchor(t *testing.T) {
	as, db := setupVerifyStore(t)
	now := time.Now().UTC()
	putChainLog(t, as, "anchor-1", now, "in-1", "out-1")
	putChainLog(t, as, "victim-2", now.Add(time.Second), "in-2", "out-2")

	mustExec(t, db, `UPDATE audit_logs SET prev_hash = ? WHERE id = ?`, bogusHash64, "victim-2")

	res := mustVerify(t, as)
	if res.Valid {
		t.Fatal("a rewritten anchor must stay invalid")
	}
	if res.Reason != store.ChainReasonHashMismatch {
		t.Fatalf("reason = %q, want %q", res.Reason, store.ChainReasonHashMismatch)
	}
	if res.BrokenAtID != "victim-2" {
		t.Fatalf("broken_at_id = %q, want victim-2", res.BrokenAtID)
	}
}

// TestAuditStore_VerifyChain_ReportsMissingPrev 覆盖缺口 (a)：非链首记录的锚点与哈希被抹除
// （物理删改的常见形态）时报 `missing_prev`，与「锚点被替换为其它值」区分开。
func TestAuditStore_VerifyChain_ReportsMissingPrev(t *testing.T) {
	as, db := setupVerifyStore(t)
	now := time.Now().UTC()
	putChainLog(t, as, "head-1", now, "in-1", "out-1")
	putChainLog(t, as, "stripped-2", now.Add(time.Second), "in-2", "out-2")

	mustExec(t, db, `UPDATE audit_logs SET prev_hash = '', integrity_hash = '' WHERE id = ?`, "stripped-2")

	res := mustVerify(t, as)
	if res.Valid {
		t.Fatal("a record with its anchor stripped must stay invalid")
	}
	if res.Reason != store.ChainReasonMissingPrev {
		t.Fatalf("reason = %q, want %q", res.Reason, store.ChainReasonMissingPrev)
	}
	if res.BrokenAtID != "stripped-2" {
		t.Fatalf("broken_at_id = %q, want stripped-2", res.BrokenAtID)
	}
}

// TestAuditStore_VerifyChain_IdenticalTimestampsReplayStably 覆盖缺口 (c)：
// 三条**时间戳完全相同**的记录按 ID 字典序「逆序」写入。规范化链序 `(seq, timestamp, id)` 下
// 回放顺序与锚点锻造顺序一致，验真必须通过且可复现；若退化为以 timestamp 为首要次序，
// 回放顺序会被倒置成 a→m→z，从而对合法链误报断链。
func TestAuditStore_VerifyChain_IdenticalTimestampsReplayStably(t *testing.T) {
	as, _ := setupVerifyStore(t)
	same := time.Now().UTC()
	for _, id := range []string{"chain-z", "chain-m", "chain-a"} {
		putChainLog(t, as, id, same, "in-"+id, "out-"+id)
	}

	first := mustVerify(t, as)
	if !first.Valid {
		t.Fatalf("identical-timestamp chain must verify cleanly, got reason=%q msg=%s broken_at=%q",
			first.Reason, first.Message, first.BrokenAtID)
	}
	if first.Reason != store.ChainReasonOK || first.TotalVerified != 3 || first.LegacyHashed != 0 {
		t.Fatalf("unexpected verdict: reason=%q verified=%d legacy=%d",
			first.Reason, first.TotalVerified, first.LegacyHashed)
	}

	// 顺序稳定性：重复核验必须得出同一结论（不存在随机回放序）。
	for i := 0; i < 5; i++ {
		again := mustVerify(t, as)
		if again.Reason != first.Reason || again.Valid != first.Valid || again.TotalVerified != first.TotalVerified {
			t.Fatalf("run %d replayed the chain differently: reason=%q valid=%v verified=%d",
				i, again.Reason, again.Valid, again.TotalVerified)
		}
	}

	// 写入侧链尾裁定必须与核验回放序互逆：链尾 = 最后锚定的记录。
	latest, err := as.GetLatestLog()
	if err != nil || latest == nil {
		t.Fatalf("get latest log: %v", err)
	}
	if latest.ID != "chain-a" {
		t.Fatalf("chain tail = %q, want chain-a (last anchored record in canonical order)", latest.ID)
	}
}
