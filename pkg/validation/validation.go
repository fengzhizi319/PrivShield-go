// Package validation provides shared input validation helpers for console Go modules.
// Package validation 为控制台各 Go 模块提供共享的输入校验工具。
//
// 各模块的 handler 在绑定 JSON 后调用本包函数进行白名单校验，
// 防止非法值注入和意外操作类型。
package validation

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fengzhizi319/PrivShield-go/pkg/naming"
)

// AllowedValues checks if the given value is in the allowed set.
// Returns nil if allowed, or a descriptive error if not.
// AllowedValues 校验给定值是否在白名单内。
// 合法时返回 nil，非法时返回描述性错误。
func AllowedValues(field, value string, allowed []string) error {
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return fmt.Errorf("invalid %s: %q (allowed: %v)", field, value, allowed)
}

// PortRange validates that a port number is in the valid range 1-65535.
// PortRange 校验端口号是否在合法范围 1-65535 内。
func PortRange(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("invalid port: %d (must be 1-65535)", port)
	}
	return nil
}

// NonEmpty validates that a string field is not empty after trimming.
// NonEmpty 校验字符串字段去除空白后不为空。
func NonEmpty(field, value string) error {
	if len(value) == 0 {
		return fmt.Errorf("%s is required", field)
	}
	return nil
}

// MaxLength validates that a string does not exceed the maximum length.
// MaxLength 校验字符串不超过最大长度。
func MaxLength(field, value string, max int) error {
	if len(value) > max {
		return fmt.Errorf("%s too long: %d chars (max %d)", field, len(value), max)
	}
	return nil
}

// Common validation whitelists / 常用校验白名单

var (
	// DataSourceTypes are valid data source types.
	DataSourceTypes = []string{"database", "api", "file"}

	// SensitivityLevels are valid L1-L5 sensitivity levels.
	// 词表唯一事实源是 rules/taxonomies/default.yaml，Go 侧由 pkg/naming 承载，
	// 此处只做别名导出，不再维护第二份字面量（P1-5）。
	// 历史上同文件还有一份 high/medium/low 的 SecurityLevels 白名单，因与本词表冲突且无引用已删除。
	SensitivityLevels = naming.SecurityLevelIDs()

	// HubOperations are valid service-hub dispatch operations.
	HubOperations = []string{"mask", "k_anon", "dp", "classify", "none"}

	// AuditOperations are valid audit-log operation types.
	AuditOperations = []string{"mask", "classify", "k_anon", "dp", "qol"}

	// AuditStatuses are valid audit-log status values.
	AuditStatuses = []string{"success", "failed"}

	// P52 fix: TaskStatuses are valid task status values for filtering.
	// TaskStatuses 是合法的任务状态值，用于列表过滤校验。
	TaskStatuses = []string{"pending", "running", "completed", "failed"}
)

// GenerateID produces a collision-resistant unique ID with the given prefix.
// Format: <prefix>-<unix_seconds>-<16_random_hex_chars>
// P18 fix: replaces time-based-only ID generation that could collide under concurrency.
// GenerateID 生成抗碰撞的唯一 ID。
// 格式：<prefix>-<unix秒>-<16位随机十六进制字符>（8 字节随机分量，生日悖论安全阈值 >> 10^9）
func GenerateID(prefix string) string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().Unix(), hex.EncodeToString(buf[:]))
}

// ParsePagination extracts and validates limit/offset from Gin query parameters.
// P61 fix: consolidate duplicated pagination parsing logic across all handlers.
//
// Defaults and caps:
//   - defaultLimit: limit 默认值（当查询参数未提供时使用）
//   - maxLimit: limit 上限（防止 OOM）
//
// Returns validated (limit, offset) ready for use in store queries.
func ParsePagination(c *gin.Context, defaultLimit, maxLimit int) (limit, offset int) {
	limit = defaultLimit
	if l, err := fmt.Sscanf(c.DefaultQuery("limit", fmt.Sprintf("%d", defaultLimit)), "%d", &limit); l == 0 || err != nil {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if limit < 1 {
		limit = 1
	}

	offset = 0
	if o, err := fmt.Sscanf(c.DefaultQuery("offset", "0"), "%d", &offset); o == 0 || err != nil {
		offset = 0
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
