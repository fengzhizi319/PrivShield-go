package auth

import (
	"fmt"
	"strings"
)

// SecurityLevel 表示客体数据或主体许可的安全密级（GB/T 22239-2019 三级等保 2.4.2 强制访问控制要求）。
type SecurityLevel int

const (
	// LevelPublic 公开数据（S1 / L1）
	LevelPublic SecurityLevel = 1
	// LevelInternal 内部业务受控数据（S2 / L2）
	LevelInternal SecurityLevel = 2
	// LevelConfidential 敏感隐私数据（S3 / L3）
	LevelConfidential SecurityLevel = 3
	// LevelRestricted 核心/极密数据（S4 / L4）
	LevelRestricted SecurityLevel = 4
)

// String 返回密级的标准标识。
func (l SecurityLevel) String() string {
	switch l {
	case LevelPublic:
		return "S1"
	case LevelInternal:
		return "S2"
	case LevelConfidential:
		return "S3"
	case LevelRestricted:
		return "S4"
	default:
		return "UNKNOWN"
	}
}

// ParseSecurityLevel 将分类分级标识（如 "S1", "S2", "S3", "S4" 或 "L1", "L2", "L3", "L4"）解析为 SecurityLevel。
// 无法识别时默认采用受控级别 LevelInternal。
func ParseSecurityLevel(s string) SecurityLevel {
	s = strings.ToUpper(strings.TrimSpace(s))
	switch s {
	case "S1", "L1", "PUBLIC", "LEVEL1":
		return LevelPublic
	case "S2", "L2", "INTERNAL", "LEVEL2":
		return LevelInternal
	case "S3", "L3", "CONFIDENTIAL", "LEVEL3":
		return LevelConfidential
	case "S4", "L4", "RESTRICTED", "TOP_SECRET", "LEVEL4":
		return LevelRestricted
	default:
		// fail-closed：未识别密级默认归入最高受限密级 LevelRestricted (S4)，防止降级越权
		return LevelRestricted
	}
}

// EvaluateMAC 执行强制访问控制（MAC）策略判定（GB/T 22239-2019 §2.4.2）。
// 严格遵循多级安全（MLS）Bell-LaPadula 模型的不下读限制（No Read Up）：
// 主体许可密级（Clearance）必须大于或等于客体安全密级（Object Level），否则强制拒绝访问。
func EvaluateMAC(subjectClearance, objectLevel SecurityLevel) error {
	if subjectClearance < objectLevel {
		return fmt.Errorf("MAC access forbidden: subject clearance %s is insufficient for object security level %s", subjectClearance, objectLevel)
	}
	return nil
}
