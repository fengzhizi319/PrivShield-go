// Package store implements canonical cryptographic hash chain calculation and verification.
// Package store 实现审计日志不可篡改哈希链的规范化计算与跨版本对账核验。
//
// ==============================================================================
// 【防篡改密码学设计标准】
// 1. 【国家商用密码标准】：
//    当前权威算法采用 GB/T 32918 / GM/T 0004-2012《SM3 密码杂凑算法》（输出 256 位摘要）；
// 2. 【密钥化完整性防篡改（P1-2）】：
//    配置 `AUDIT_LOG_HASH_KEY`（局方托管密钥）后，新写入存证一律采用
//    `HMAC-SM3(key, "SM3-HMAC:v1|" + 9 要素前映像)`，未持有密钥者无法伪造或改写记录，
//    核验方仍可独立验真；未配置密钥时退回无密钥 SM3（仅可证明「内容未被修改」，
//    不能阻止知道口径者重算，故生产环境必须注入密钥）；
// 3. 【UTC 纳秒级前映像归一化】：
//    时间戳统一归一化为 UTC RFC3339Nano 格式进行拼接，彻底杜绝因 PostgreSQL
//    TIMESTAMPTZ 类型丢失写入方本地时区偏移而导致核验端算出的前映像不一致与伪分叉；
// 4. 【9 要素完整性前映像结构】：
//    `prev_hash|log_id|timestamp_utc|algorithm|input_hash|output_hash|user|security_level|params_json`
// 5. 【向下兼容多轨核验】：
//    VerifyAuditIntegrityHash 依次尝试「密钥化 HMAC-SM3 → 无密钥 SM3-UTC → SHA-256-UTC →
//    SM3-LocalTZ → SHA-256-LocalTZ」，确保加密产品认证前写入的历史证据依然合法可验。
// ==============================================================================

package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fengzhizi319/PrivShield/pkg/crypto"
)

// 存证哈希链支持的算法与版本标识常量。
const (
	// AuditHashSM3 是无密钥态下的权威国密 SM3 哈希算法标识（GM/T 0004-2012）。
	AuditHashSM3 = "SM3"

	// AuditHashSM3HMAC 是密钥化 HMAC-SM3 存证哈希的版本标签（P1-2 上线口径）。
	AuditHashSM3HMAC = "SM3-HMAC:v1"

	// AuditHashSHA256 是升级国密前使用的旧版 SHA-256 算法标识，核验历史记录时向下兼容。
	AuditHashSHA256 = "SHA256"

	// LegacyHashSuffix 是历史旧版哈希核验标签后缀（"-LEGACY"），标记需要后续重签的存量记录。
	LegacyHashSuffix = "-LEGACY"
)

// chainKey 是进程级存证哈希 HMAC 密钥（启动时由 SetAuditChainKey 注入一次）。
var chainKey atomic.Pointer[string]

// SetAuditChainKey 注入密钥化存证哈希的 HMAC-SM3 密钥；传空串退回无密钥 SM3 口径。
// 该函数只在进程启动阶段调用一次，运行期改钥会导致既有记录核验失败。
func SetAuditChainKey(key string) {
	trimmed := strings.TrimSpace(key)
	chainKey.Store(&trimmed)
}

// AuditChainKey 返回当前生效的存证哈希 HMAC 密钥（空串表示无密钥态）。
func AuditChainKey() string {
	if p := chainKey.Load(); p != nil {
		return *p
	}
	return ""
}

// hashCandidate 封装一条待核验候选：前映像字符串、计算函数与对应的算法标签。
type hashCandidate struct {
	payload string
	digest  func(payload string) string
	label   string
}

// integrityPayload 构建审计日志完整性哈希的原始前映像（Pre-image）字符串。
//
// 执行逻辑：
// 1. 根据 inUTC 参数决定是否将时间戳强制转换为 UTC 时区；
// 2. 将 9 大审计要素以 "|" 字符连接拼接为紧凑字符串。
func integrityPayload(logID, prevHash string, timestamp time.Time, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON string, inUTC bool) string {
	ts := timestamp.Format(time.RFC3339Nano)
	if inUTC {
		ts = timestamp.UTC().Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%s|%v",
		prevHash, logID, ts, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON)
}

// ComputeAuditIntegrityHash computes the canonical chain hash of an audit record.
//
// ComputeAuditIntegrityHash 计算审计记录的规范化防篡改链式哈希（全仓库唯一权威实现）。
//
// 使用方法：
// 审计日志写入（SaveLog / SaveLogWithSnapshot / SaveLogsBatch）时由存储层统一调用生成。
//
// 执行逻辑：
// 1. 构建 UTC 归一化的 9 要素前映像 payload；
// 2. 配置了存证 HMAC 密钥（SetAuditChainKey）时，对 `"SM3-HMAC:v1|" + payload` 计算 HMAC-SM3；
// 3. 未配置密钥时退回无密钥 SM3 摘要；两种口径均编码为 64 位小写十六进制。
func ComputeAuditIntegrityHash(logID, prevHash string, timestamp time.Time, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON string) string {
	payload := integrityPayload(logID, prevHash, timestamp, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON, true)
	if key := AuditChainKey(); key != "" {
		return crypto.HMACSM3Hex([]byte(key), []byte(AuditHashSM3HMAC+"|"+payload))
	}
	sum := crypto.SumSM3([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// ComputeAuditIntegrityHashAlgo returns the label of the hash convention currently produced by
// ComputeAuditIntegrityHash, so diagnostics and the re-signing tool can state the active口径.
//
// ComputeAuditIntegrityHashAlgo 返回当前写入口径对应的算法标签（密钥化或无密钥 SM3）。
func ComputeAuditIntegrityHashAlgo() string {
	if AuditChainKey() != "" {
		return AuditHashSM3HMAC
	}
	return AuditHashSM3
}

// VerifyAuditIntegrityHash reports whether stored authenticates the record. When a chain key is
// configured the keyed HMAC-SM3 candidate is tried first; the four un-keyed pre-migration
// conventions (plain SM3, SHA-256, and their local-timezone variants) stay accepted so evidence
// written before keying still verifies.
//
// VerifyAuditIntegrityHash 核验单条记录的存储哈希是否与其实际业务内容完全匹配。
//
// 兼容性保障：
// 优先尝试密钥化 HMAC-SM3（配置密钥时），随后兼容无密钥 SM3-UTC、SHA-256-UTC、
// SM3-LocalTZ 与 SHA-256-LocalTZ 四种历史候选前映像，确保存量证据永不失验。
//
// 返回值：
// - bool: 是否通过完整性校验（若为 false，说明数据内容或前序哈希曾遭到恶意篡改）；
// - string: 成功匹配的算法标签（若为空串表示数据已损坏/被篡改）。
func VerifyAuditIntegrityHash(stored, logID, prevHash string, timestamp time.Time, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON string) (bool, string) {
	if stored == "" {
		return false, ""
	}
	utc := integrityPayload(logID, prevHash, timestamp, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON, true)

	candidates := make([]hashCandidate, 0, 5)
	if key := AuditChainKey(); key != "" {
		keyed := key
		candidates = append(candidates, hashCandidate{
			label: AuditHashSM3HMAC,
			digest: func(payload string) string {
				return crypto.HMACSM3Hex([]byte(keyed), []byte(AuditHashSM3HMAC+"|"+payload))
			},
			payload: utc,
		})
	}
	sm3 := func(payload string) string {
		sum := crypto.SumSM3([]byte(payload))
		return hex.EncodeToString(sum[:])
	}
	sha := func(payload string) string {
		sum := sha256.Sum256([]byte(payload))
		return hex.EncodeToString(sum[:])
	}
	candidates = append(candidates,
		hashCandidate{payload: utc, label: AuditHashSM3, digest: sm3},
		hashCandidate{payload: utc, label: AuditHashSHA256 + LegacyHashSuffix, digest: sha},
	)
	if local := integrityPayload(logID, prevHash, timestamp, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON, false); local != utc {
		candidates = append(candidates,
			hashCandidate{payload: local, label: AuditHashSM3 + LegacyHashSuffix, digest: sm3},
			hashCandidate{payload: local, label: AuditHashSHA256 + LegacyHashSuffix, digest: sha},
		)
	}

	storedBytes := []byte(stored)
	for _, c := range candidates {
		if hmac.Equal(storedBytes, []byte(c.digest(c.payload))) {
			return true, c.label
		}
	}
	return false, ""
}

// IsCanonicalHashLabel reports whether a verification label matches the write convention that is
// active right now. Once a chain key is configured, un-keyed SM3 rows stop being canonical and are
// counted as pending re-signing, which is what the re-signing tool consumes.
//
// IsCanonicalHashLabel 判断核验命中的标签是否与「当前写入口径」一致：
// 注入存证 HMAC 密钥后，无密钥 SM3 记录即不再视为规范态，而被计入「待重签」，
// 重签工具据此把存量记录升级为密钥化 HMAC-SM3 口径。
func IsCanonicalHashLabel(label string) bool {
	return label != "" && label == ComputeAuditIntegrityHashAlgo()
}

// snapshotIntegrityPayload builds the pre-image for a snapshot integrity hash.
// The pre-image covers the snapshot's own identity, its parent audit log, the chain
// linkage, the algorithm, the encrypted input/output samples and the parameters,
// so that swapping or re-encrypting a sample is detected during verification.
//
// snapshotIntegrityPayload 构建快照完整性哈希的前映像，包含快照自身 ID、关联日志 ID、
// 前序哈希、时间戳、算法、输入/输出样本密文与参数，确保样本替换可被检测。
func snapshotIntegrityPayload(snapshotID, auditLogID, prevHash string, timestamp time.Time, algorithm, inputSample, outputSample, parametersJSON string, inUTC bool) string {
	ts := timestamp.Format(time.RFC3339Nano)
	if inUTC {
		ts = timestamp.UTC().Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%s",
		prevHash, snapshotID, auditLogID, ts, algorithm, inputSample, outputSample, parametersJSON)
}

// ComputeSnapshotIntegrityHash computes a standalone integrity hash for a snapshot record.
// Unlike the previous implementation that simply copied the parent log's integrity hash,
// this hash binds the snapshot's own fields (input/output samples) to the tamper-evidence chain.
//
// ComputeSnapshotIntegrityHash 为快照记录计算独立的完整性哈希。
// 与旧实现（直接复制主日志 integrity_hash）不同，此哈希将快照自身字段绑定到防篡改链。
func ComputeSnapshotIntegrityHash(snapshotID, auditLogID, prevHash string, timestamp time.Time, algorithm, inputSample, outputSample, parametersJSON string) string {
	payload := snapshotIntegrityPayload(snapshotID, auditLogID, prevHash, timestamp, algorithm, inputSample, outputSample, parametersJSON, true)
	if key := AuditChainKey(); key != "" {
		return crypto.HMACSM3Hex([]byte(key), []byte("SM3-HMAC:v1-SNAPSHOT|"+payload))
	}
	sum := crypto.SumSM3([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// VerifySnapshotIntegrityHash verifies that a stored snapshot integrity hash matches the
// recomputed hash over the snapshot's own fields. It tries the same keyed/un-keyed candidates
// as the audit log verifier, plus a legacy fallback that accepts hashes copied from the parent
// log for backward compatibility with already-persisted snapshots.
//
// VerifySnapshotIntegrityHash 验证快照存储的完整性哈希是否与其自身字段重算结果一致。
// 优先尝试新的快照独立哈希，并保留旧版「继承自主日志哈希」的兼容性回退。
func VerifySnapshotIntegrityHash(stored, snapshotID, auditLogID, prevHash string, timestamp time.Time, algorithm, inputSample, outputSample, parametersJSON string) (bool, string) {
	if stored == "" {
		return false, ""
	}
	utc := snapshotIntegrityPayload(snapshotID, auditLogID, prevHash, timestamp, algorithm, inputSample, outputSample, parametersJSON, true)

	candidates := make([]hashCandidate, 0, 7)
	if key := AuditChainKey(); key != "" {
		keyed := key
		candidates = append(candidates, hashCandidate{
			label: AuditHashSM3HMAC + "-SNAPSHOT",
			digest: func(payload string) string {
				return crypto.HMACSM3Hex([]byte(keyed), []byte("SM3-HMAC:v1-SNAPSHOT|"+payload))
			},
			payload: utc,
		})
	}
	sm3 := func(payload string) string {
		sum := crypto.SumSM3([]byte(payload))
		return hex.EncodeToString(sum[:])
	}
	sha := func(payload string) string {
		sum := sha256.Sum256([]byte(payload))
		return hex.EncodeToString(sum[:])
	}
	candidates = append(candidates,
		hashCandidate{payload: utc, label: AuditHashSM3 + "-SNAPSHOT", digest: sm3},
		hashCandidate{payload: utc, label: AuditHashSHA256 + LegacyHashSuffix + "-SNAPSHOT", digest: sha},
	)
	if local := snapshotIntegrityPayload(snapshotID, auditLogID, prevHash, timestamp, algorithm, inputSample, outputSample, parametersJSON, false); local != utc {
		candidates = append(candidates,
			hashCandidate{payload: local, label: AuditHashSM3 + LegacyHashSuffix + "-SNAPSHOT", digest: sm3},
			hashCandidate{payload: local, label: AuditHashSHA256 + LegacyHashSuffix + "-SNAPSHOT", digest: sha},
		)
	}

	storedBytes := []byte(stored)
	for _, c := range candidates {
		if hmac.Equal(storedBytes, []byte(c.digest(c.payload))) {
			return true, c.label
		}
	}
	return false, ""
}
