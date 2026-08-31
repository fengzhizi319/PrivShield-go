package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/fengzhizi319/PrivShield/pkg/crypto"
)

// Audit chain hash algorithms / 存证哈希链算法标识。
const (
	// AuditHashSM3 is the canonical algorithm (GM/T 0004-2012).
	AuditHashSM3 = "SM3"
	// AuditHashSHA256 is the pre-SM3 algorithm, still accepted when verifying old records.
	AuditHashSHA256 = "SHA256"
	// LegacyHashSuffix marks a record that only verifies under a pre-migration hash convention.
	LegacyHashSuffix = "-LEGACY"
)

type hashCandidate struct {
	payload string
	label   string
}

// integrityPayload builds the pre-image of the chain integrity hash.
//
// The canonical form normalises the timestamp to UTC because PostgreSQL stores TIMESTAMPTZ, which
// loses the writer's zone offset; without normalisation one record hashes to two different
// pre-images depending on where it is verified.
func integrityPayload(logID, prevHash string, timestamp time.Time, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON string, inUTC bool) string {
	ts := timestamp.Format(time.RFC3339Nano)
	if inUTC {
		ts = timestamp.UTC().Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%s|%v",
		prevHash, logID, ts, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON)
}

// ComputeAuditIntegrityHash computes the canonical SM3 chain hash of an audit record.
// ComputeAuditIntegrityHash 计算审计记录的规范化 SM3 防篡改链式哈希（全仓库唯一权威实现）。
func ComputeAuditIntegrityHash(logID, prevHash string, timestamp time.Time, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON string) string {
	payload := integrityPayload(logID, prevHash, timestamp, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON, true)
	sum := crypto.SumSM3([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// VerifyAuditIntegrityHash reports whether stored authenticates the record, accepting the two
// pre-migration conventions (SHA-256, and local-zone timestamps) as legacy so historical evidence
// written before the SM3 migration still verifies.
//
// The second return value is the matched algorithm label; it is empty when nothing matches, which
// means the record was tampered with.
func VerifyAuditIntegrityHash(stored, logID, prevHash string, timestamp time.Time, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON string) (bool, string) {
	if stored == "" {
		return false, ""
	}
	utc := integrityPayload(logID, prevHash, timestamp, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON, true)
	candidates := []hashCandidate{{utc, AuditHashSM3}, {utc, AuditHashSHA256 + LegacyHashSuffix}}
	if local := integrityPayload(logID, prevHash, timestamp, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON, false); local != utc {
		candidates = append(candidates,
			hashCandidate{local, AuditHashSM3 + LegacyHashSuffix},
			hashCandidate{local, AuditHashSHA256 + LegacyHashSuffix},
		)
	}
	for _, c := range candidates {
		var got string
		if c.label == AuditHashSM3 || c.label == AuditHashSM3+LegacyHashSuffix {
			sum := crypto.SumSM3([]byte(c.payload))
			got = hex.EncodeToString(sum[:])
		} else {
			sum := sha256.Sum256([]byte(c.payload))
			got = hex.EncodeToString(sum[:])
		}
		if stored == got {
			return true, c.label
		}
	}
	return false, ""
}

// IsCanonicalHashLabel reports whether a verification label denotes the current convention.
func IsCanonicalHashLabel(label string) bool { return label == AuditHashSM3 }
