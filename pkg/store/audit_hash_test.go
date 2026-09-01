// Package store 测试套件
//
// ==============================================================================
// 【测试套件设计目标与覆盖范围】
// 本测试文件验证审计哈希链的完整性计算与核验（SM3 / HMAC-SM3 / SHA-256 兼容）：
//  1. 【无密钥纯 SM3 模式】：验证未设置存证密钥时使用纯 SM3 计算完整性摘要，
//     与手动计算的 SM3 摘要完全一致，且核验返回 AuditHashSM3 标签；
//  2. 【HMAC-SM3 密钥模式】：验证设置存证密钥后使用 HMAC-SM3 计算带版本前缀的摘要，
//     与手动 HMACSM3Hex 计算结果完全一致，核验返回 AuditHashSM3HMAC 规范标签；
//  3. 【密钥升级兼容】：验证无密钥写入的摘要在设置密钥后仍可核验（legacy 候选），
//     但无密钥重算结果不再被视为规范态（IsCanonicalHashLabel 返回 false）；
//  4. 【错误密钥拒绝】：验证使用错误密钥的伪造摘要与正确密钥摘要不同，且核验被拒绝；
//  5. 【字段篡改检测】：验证修改 output_hash 后核验失败；
//  6. 【历史 SHA-256 兼容】：验证迁移前本机时区 + SHA-256 写入的摘要仍可核验，
//     但历史标签永不被视为规范态；
//  7. 【空摘要拒绝】：验证空 stored hash 不通过核验；
//  8. 【空白密钥裁剪】：验证纯空白密钥被裁剪为空，回退到无密钥 SM3 模式。
// ==============================================================================

package store

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/fengzhizi319/PrivShield-go/pkg/crypto"
)

// 测试用固定要素，避免各用例重复拼参数。
const (
	testLogID        = "log-chain-1"
	testPrevHash     = "0000000000000000000000000000000000000000000000000000000000000000"
	testAlgorithm    = "SM4"
	testInputHash    = "in-hash"
	testOutputHash   = "out-hash"
	testUser         = "auditor"
	testSecLevel     = "L3"
	testParamsJSON   = `{"field":"phone"}`
	testHMACKey      = "局方托管存证密钥-32bytes-minimum-length"
	testWrongHMACKey = "wrong-key-not-the-managed-secret-at-all"
)

var testTimestamp = time.Date(2026, 3, 14, 9, 26, 53, 123456789, time.UTC)

func computeTest() string {
	return ComputeAuditIntegrityHash(testLogID, testPrevHash, testTimestamp, testAlgorithm, testInputHash, testOutputHash, testUser, testSecLevel, testParamsJSON)
}

func verifyTest(stored string) (bool, string) {
	return VerifyAuditIntegrityHash(stored, testLogID, testPrevHash, testTimestamp, testAlgorithm, testInputHash, testOutputHash, testUser, testSecLevel, testParamsJSON)
}

// withChainKey 在用例结束后恢复进程级存证 HMAC 密钥。
func withChainKey(t *testing.T, key string) {
	t.Helper()
	prev := AuditChainKey()
	t.Cleanup(func() { SetAuditChainKey(prev) })
	SetAuditChainKey(key)
}

// TestAuditChainKeyTrimsWhitespace 验证纯空白密钥被裁剪为空，回退到无密钥 SM3 模式。
// 执行逻辑：设置纯空白密钥（"  \t  "），断言 AuditChainKey() 返回空串，
// 且算法回退到 AuditHashSM3。
func TestAuditChainKeyTrimsWhitespace(t *testing.T) {
	withChainKey(t, "")
	SetAuditChainKey("  \t  ")
	if got := AuditChainKey(); got != "" {
		t.Fatalf("whitespace-only key must reset to un-keyed state, got %q", got)
	}
	if algo := ComputeAuditIntegrityHashAlgo(); algo != AuditHashSM3 {
		t.Fatalf("un-keyed algo = %q, want %q", algo, AuditHashSM3)
	}
}

// TestUnkeyedChainUsesPlainSM3 验证无密钥模式下使用纯 SM3 计算完整性摘要。
// 执行逻辑：清除密钥后手动拼接 payload 并计算 SM3 摘要，
// 与 ComputeAuditIntegrityHash 结果比较，断言完全一致；
// 核验返回 AuditHashSM3 标签，且 IsCanonicalHashLabel 返回 true。
func TestUnkeyedChainUsesPlainSM3(t *testing.T) {
	withChainKey(t, "")
	payload := integrityPayload(testLogID, testPrevHash, testTimestamp, testAlgorithm, testInputHash, testOutputHash, testUser, testSecLevel, testParamsJSON, true)
	want := crypto.SumSM3([]byte(payload))
	if got := computeTest(); got != hex.EncodeToString(want[:]) {
		t.Fatalf("un-keyed hash mismatch: got %s", got)
	}
	if ComputeAuditIntegrityHashAlgo() != AuditHashSM3 {
		t.Fatalf("un-keyed algo = %q, want %q", ComputeAuditIntegrityHashAlgo(), AuditHashSM3)
	}
	ok, label := verifyTest(computeTest())
	if !ok || label != AuditHashSM3 {
		t.Fatalf("un-keyed verify = (%v, %q), want (true, %q)", ok, label, AuditHashSM3)
	}
	if !IsCanonicalHashLabel(label) {
		t.Fatalf("un-keyed SM3 must be canonical while un-keyed")
	}
}

// TestKeyedChainIsHMACSM3WithVersionPrefix 验证密钥模式下使用 HMAC-SM3 计算带版本前缀的摘要。
// 执行逻辑：设置存证密钥后手动拼接 "sm3_hmac|<payload>" 并计算 HMAC-SM3，
// 与 ComputeAuditIntegrityHash 结果比较，断言完全一致；
// 核验返回 AuditHashSM3HMAC 规范标签。
func TestKeyedChainIsHMACSM3WithVersionPrefix(t *testing.T) {
	withChainKey(t, testHMACKey)
	payload := integrityPayload(testLogID, testPrevHash, testTimestamp, testAlgorithm, testInputHash, testOutputHash, testUser, testSecLevel, testParamsJSON, true)
	want := crypto.HMACSM3Hex([]byte(testHMACKey), []byte(AuditHashSM3HMAC+"|"+payload))
	if got := computeTest(); got != want {
		t.Fatalf("keyed hash mismatch: got %s want %s", got, want)
	}
	if ComputeAuditIntegrityHashAlgo() != AuditHashSM3HMAC {
		t.Fatalf("keyed algo = %q, want %q", ComputeAuditIntegrityHashAlgo(), AuditHashSM3HMAC)
	}
	ok, label := verifyTest(computeTest())
	if !ok || !IsCanonicalHashLabel(label) {
		t.Fatalf("keyed verify = (%v, %q), want canonical %q", ok, label, AuditHashSM3HMAC)
	}
}

// TestKeyedChainRejectsUnkeyedRecomputation 验证密钥升级后的兼容性。
// 执行逻辑：先在无密钥态计算摘要，然后设置密钥，断言无密钥摘要仍可核验（legacy 候选），
// 但无密钥 SM3 标签不再被视为规范态（IsCanonicalHashLabel 返回 false）。
func TestKeyedChainRejectsUnkeyedRecomputation(t *testing.T) {
	withChainKey(t, "")
	unkeyed := computeTest()

	SetAuditChainKey(testHMACKey)
	if ok, _ := verifyTest(unkeyed); !ok {
		t.Fatalf("pre-keying evidence must still verify after keying (legacy candidate)")
	}
	// 知道口径但不知道密钥者重算出的无密钥摘要不能被接受为规范态。
	if IsCanonicalHashLabel(AuditHashSM3) {
		t.Fatalf("un-keyed SM3 must not be canonical once a chain key is active")
	}
}

// TestKeyedChainRejectsWrongKeyForgery 验证使用错误密钥的伪造摘要被拒绝。
// 执行逻辑：在密钥模式下计算正确摘要，然后用错误密钥伪造摘要，
// 断言伪造摘要与正确摘要不同，且核验被拒绝。
func TestKeyedChainRejectsWrongKeyForgery(t *testing.T) {
	withChainKey(t, testHMACKey)
	stored := computeTest()

	forged := crypto.HMACSM3Hex([]byte(testWrongHMACKey), []byte(AuditHashSM3HMAC+"|"+integrityPayload(testLogID, testPrevHash, testTimestamp, testAlgorithm, testInputHash, testOutputHash, testUser, testSecLevel, testParamsJSON, true)))
	if forged == stored {
		t.Fatalf("forgery with a wrong key must differ from the keyed hash")
	}
	if ok, label := verifyTest(forged); ok {
		t.Fatalf("wrong-key forgery must not verify, got label %q", label)
	}
}

// TestKeyedChainDetectsFieldTampering 验证修改字段后核验失败。
// 执行逻辑：在密钥模式下计算正确摘要并验证通过，
// 然后将 output_hash 篡改为 "attacker-controlled-output"，断言核验失败。
func TestKeyedChainDetectsFieldTampering(t *testing.T) {
	withChainKey(t, testHMACKey)
	stored := computeTest()
	if ok, _ := verifyTest(stored); !ok {
		t.Fatalf("keyed record failed to verify")
	}
	if ok, _ := VerifyAuditIntegrityHash(stored, testLogID, testPrevHash, testTimestamp, testAlgorithm, testInputHash, "attacker-controlled-output", testUser, testSecLevel, testParamsJSON); ok {
		t.Fatalf("tampered output_hash must fail verification")
	}
}

// TestLegacyLocalTimezoneSHA256StillVerifies 验证迁移前本机时区 + SHA-256 写入的摘要仍可核验。
// 执行逻辑：使用 CST 时区 + SHA-256 手动计算历史格式摘要，
// 调用 VerifyAuditIntegrityHash 断言核验成功且标签为 "sha256:legacy"，
// 但历史标签永不被视为规范态（IsCanonicalHashLabel 返回 false）。
func TestLegacyLocalTimezoneSHA256StillVerifies(t *testing.T) {
	withChainKey(t, "")
	// 迁移前写入方使用本机时区 + SHA-256，核验端必须在无密钥态下兼容。
	localTS := time.Date(2026, 3, 14, 17, 26, 53, 123456789, time.FixedZone("CST", 8*3600))
	payload := integrityPayload(testLogID, testPrevHash, localTS, testAlgorithm, testInputHash, testOutputHash, testUser, testSecLevel, testParamsJSON, false)
	sum := sha256.Sum256([]byte(payload))
	stored := hex.EncodeToString(sum[:])

	ok, label := VerifyAuditIntegrityHash(stored, testLogID, testPrevHash, localTS, testAlgorithm, testInputHash, testOutputHash, testUser, testSecLevel, testParamsJSON)
	if !ok || label != AuditHashSHA256+LegacyHashSuffix {
		t.Fatalf("legacy local-tz SHA-256 verify = (%v, %q), want (true, %q)", ok, label, AuditHashSHA256+LegacyHashSuffix)
	}
	if IsCanonicalHashLabel(label) {
		t.Fatalf("legacy labels must never be canonical")
	}
}

// TestVerifyRejectsEmptyStoredHash 验证空 stored hash 不通过核验。
// 执行逻辑：在密钥模式下调用 verifyTest("")，断言 ok=false 且 label 为空。
func TestVerifyRejectsEmptyStoredHash(t *testing.T) {
	withChainKey(t, testHMACKey)
	if ok, label := verifyTest(""); ok || label != "" {
		t.Fatalf("empty stored hash must not verify, got (%v, %q)", ok, label)
	}
}
