package store

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/fengzhizi319/PrivShield/pkg/crypto"
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

func TestVerifyRejectsEmptyStoredHash(t *testing.T) {
	withChainKey(t, testHMACKey)
	if ok, label := verifyTest(""); ok || label != "" {
		t.Fatalf("empty stored hash must not verify, got (%v, %q)", ok, label)
	}
}
