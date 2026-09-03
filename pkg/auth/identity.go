// Package auth provides shared identity, permission mapping and scope-based authentication.
//
// 从 engine-go/internal/security 下沉到 pkg/auth，供 services、console 及 engine-go 统一使用。
package auth

import (
	"log/slog"
	"os"
	"strings"
	"time"
)

// Identity 表示已认证的调用者身份。
type Identity struct {
	// ServiceType: "internal"（高信任内部服务）或 "external"（外部/公共客户端）。
	ServiceType string
	// Name: 服务或账户名称，用于日志与限流 key。
	Name string
	// Scopes: 已授予的权限列表。["*"] 表示完全访问。
	Scopes []string
}

// AnonymousIdentity 是认证未启用时使用的默认匿名管理员身份。
var AnonymousIdentity = &Identity{ServiceType: "internal", Name: "anonymous", Scopes: []string{"*"}}

// HasPermission 检查该身份是否被允许执行指定权限。
// 通配符 scope "*" 授予所有权限，否则精确匹配。
func (id *Identity) HasPermission(permission string) bool {
	for _, s := range id.Scopes {
		if s == "*" || s == permission {
			return true
		}
	}
	return false
}

// IsHealthPathOrMethod 判断给定 REST 路径或 gRPC 方法是否为健康探针。
func IsHealthPathOrMethod(pathOrMethod string) bool {
	switch pathOrMethod {
	case "/health", "/livez", "/readyz", "/readyz/llm":
		return true
	}
	// gRPC health check
	if len(pathOrMethod) > 7 && pathOrMethod[len(pathOrMethod)-7:] == "/Health" {
		return true
	}
	return false
}

// PermissionForRESTPath 将 REST 路径映射为所需权限字符串。
// 支持 /v1/* 与 /api/v1/* 双前缀（别名路由归一化后统一匹配）。
func PermissionForRESTPath(path string) string {
	// 去除尾部斜杠
	if len(path) > 1 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}
	// 归一化：/api/v1/* → /v1/*，确保别名路由与主路由共享同一权限映射。
	normalized := path
	if strings.HasPrefix(normalized, "/api/v1/") {
		normalized = "/v1/" + normalized[len("/api/v1/"):]
	} else if normalized == "/api/v1" {
		normalized = "/v1"
	}
	switch {
	case normalized == "/health" || normalized == "/livez" || normalized == "/readyz" || normalized == "/readyz/llm":
		return "health:read"
	// 根路径直调别名（/agent/process, /medical/process 等）
	case normalized == "/agent/process":
		return "agent:process"
	case normalized == "/medical/process":
		return "medical:process"
	case normalized == "/ops/diagnostics":
		return "ops:diagnostics"
	case normalized == "/privacy/process_file":
		return "privacy:mask"
	case strings.HasPrefix(normalized, "/v1/privacy/mask"):
		return "privacy:mask"
	case normalized == "/v1/privacy/hash":
		return "privacy:hash"
	case strings.HasPrefix(normalized, "/v1/privacy/dp/") || strings.HasPrefix(normalized, "/v1/privacy/ldp/"):
		return "privacy:dp"
	case strings.HasPrefix(normalized, "/v1/privacy/k_anonymize"):
		return "privacy:kano"
	case strings.HasPrefix(normalized, "/v1/privacy/qol/"):
		return "privacy:qol"
	case normalized == "/v1/privacy/budget":
		return "privacy:budget"
	case normalized == "/v1/privacy/budget/reset":
		return "privacy:budget"
	case normalized == "/v1/privacy/profile/recommend":
		return "privacy:profile"
	case normalized == "/v1/privacy/process_file":
		return "privacy:mask"
	case strings.HasPrefix(normalized, "/v1/privacy/classify/"):
		return "classification:read"
	case strings.HasPrefix(normalized, "/v1/dynclassification"):
		if normalized == "/v1/dynclassification/profiles/reload" || normalized == "/v1/dynclassification/generate_profile" {
			return "dynclassification:write"
		}
		return "dynclassification:read"
	case strings.HasPrefix(normalized, "/v1/agent"):
		return "agent:process"
	case strings.HasPrefix(normalized, "/v1/medical"):
		return "medical:process"
	case strings.HasPrefix(normalized, "/v1/pipeline"):
		return "pipeline:process"
	case strings.HasPrefix(normalized, "/v1/ops/"):
		return "ops:diagnostics"
	case strings.HasPrefix(normalized, "/debug/pprof"):
		return "ops:admin"
	// /api/v1/* 别名路由中 kano/classify/hash/budget 等子路径
	case strings.HasPrefix(normalized, "/v1/mask"):
		return "privacy:mask"
	case strings.HasPrefix(normalized, "/v1/dp/"):
		return "privacy:dp"
	case strings.HasPrefix(normalized, "/v1/kano/"):
		return "privacy:kano"
	case normalized == "/v1/classify" || normalized == "/v1/classify/batch":
		return "classification:read"
	case normalized == "/v1/hash/hmac":
		return "privacy:hash"
	case normalized == "/v1/budget":
		return "privacy:budget"
	case normalized == "/v1/budget/reset":
		return "privacy:budget"
	case strings.HasPrefix(normalized, "/v1/qol/"):
		return "privacy:qol"
	case strings.HasPrefix(normalized, "/v1/ldp/"):
		return "privacy:dp"
	}
	return ""
}

// PermissionForGRPCMethod 将 gRPC 方法名映射为权限字符串。
func PermissionForGRPCMethod(method string) string {
	short := method
	for i := len(method) - 1; i >= 0; i-- {
		if method[i] == '/' {
			short = method[i+1:]
			break
		}
	}
	mapping := map[string]string{
		"Mask": "privacy:mask", "MaskRecord": "privacy:mask",
		"MaskBatch": "privacy:mask", "MaskDataFrame": "privacy:mask",
		"Hash":    "privacy:hash",
		"DPCount": "privacy:dp", "DPSum": "privacy:dp", "DPMean": "privacy:dp",
		"DPHistogram": "privacy:dp", "DPNoisyCount": "privacy:dp",
		"DPNoisySum": "privacy:dp", "DPNoisyMean": "privacy:dp",
		"DPNoisyHistogram": "privacy:dp", "DPChunkedCount": "privacy:dp",
		"DPChunkedSum": "privacy:dp", "DPChunkedMean": "privacy:dp",
		"DPChunkedHistogram": "privacy:dp", "DPAggregate": "privacy:dp",
		"DPVectorSum": "privacy:dp", "DPAdaptiveClip": "privacy:dp",
		"DPGroupBy":          "privacy:dp",
		"PerturbBinaryBatch": "privacy:dp", "PerturbCategoricalBatch": "privacy:dp",
		"EstimateBinaryFrequency": "privacy:dp", "EstimateCategoricalHistogram": "privacy:dp",
		"KAnonymizeRecord": "privacy:kano", "KAnonymizeTable": "privacy:kano",
		"KAnonymizeDataFrame": "privacy:kano",
		"ObfuscateQuery":      "privacy:qol", "ObfuscateQueryBatch": "privacy:qol",
		"ClassifyField": "classification:read", "ClassifyRecord": "classification:read",
		"ClassifyTable":   "classification:read",
		"DynClassify":     "dynclassification:read",
		"Health":          "health:read",
		"RecommendParams": "privacy:profile",
	}
	if p, ok := mapping[short]; ok {
		return p
	}
	return ""
}

// ParseAPIKeysEnv 解析 "token:name:scope1,scope2;token2:name2:scope3:2025-12-31T23:59:59Z" 格式的 API Key 配置。
// 供 engine-go 与各微服务共享统一的密钥解析逻辑。
// 安全增强（三级等保 G-14）：
//  1. 支持末尾追加 ISO 8601 过期时间（以最后一个冒号分隔）
//  2. 对 token、name、scope 做 TrimSpace
//  3. 丢弃空 token 的条目
func ParseAPIKeysEnv(raw string) map[string]*KeyConfig {
	if raw == "" {
		return nil
	}
	keys := make(map[string]*KeyConfig)
	for _, entry := range strings.Split(raw, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		// 先用 SplitN(, 3) 拆出 token、name、rest
		parts := strings.SplitN(entry, ":", 3)
		if len(parts) < 2 {
			continue
		}
		token := strings.TrimSpace(parts[0])
		name := strings.TrimSpace(parts[1])
		if token == "" || name == "" {
			continue
		}

		rest := ""
		if len(parts) == 3 {
			rest = strings.TrimSpace(parts[2])
		}

		// rest 可能是 "scopes" 或 "scopes:expires_at"
		// 检测末尾是否有 RFC3339 时间戳（含 T 和冒号，长度 >= 20）
		var scopesStr string
		var expiresAt *time.Time
		if rest != "" {
			// 尝试从 rest 末尾提取时间戳：找最后一个能解析为 RFC3339 的尾部
			if idx := findExpirySeparator(rest); idx >= 0 {
				scopesStr = rest[:idx]
				if t, err := time.Parse(time.RFC3339, rest[idx+1:]); err == nil {
					expiresAt = &t
				} else {
					// 解析失败，整体当作 scopes
					scopesStr = rest
				}
			} else {
				scopesStr = rest
			}
		}

		var scopes []string
		if scopesStr != "" {
			for _, s := range strings.Split(scopesStr, ",") {
				s = strings.TrimSpace(s)
				if s != "" {
					scopes = append(scopes, s)
				}
			}
		}
		if len(scopes) == 0 {
			slog.Warn("ParseAPIKeysEnv: key has no scopes, defaulting to empty (no permissions)",
				"key_name", name, "token_prefix", tokenPrefix(token))
			scopes = []string{}
		}
		keys[token] = &KeyConfig{Name: name, Scopes: scopes, ExpiresAt: expiresAt}
	}
	return keys
}

// findExpirySeparator 在 s 中查找过期时间的分隔冒号位置。
// 过期时间格式为 RFC3339（如 2025-12-31T23:59:59Z），从末尾回溯查找。
func findExpirySeparator(s string) int {
	// RFC3339 最短格式 "2006-01-02T15:04:05Z" = 20 字符
	// 从末尾往前找，尝试每个冒号位置
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			candidate := s[i+1:]
			if _, err := time.Parse(time.RFC3339, candidate); err == nil {
				return i
			}
		}
	}
	return -1
}

// LoadAPIKeysFromEnv 从环境变量加载 API Key 映射。
func LoadAPIKeysFromEnv(envKey string) map[string]*KeyConfig {
	return ParseAPIKeysEnv(os.Getenv(envKey))
}

// tokenPrefix 返回 token 的前 4 个字符用于日志标识，避免在日志中泄露完整密钥。
func tokenPrefix(token string) string {
	if len(token) <= 4 {
		return "***"
	}
	return token[:4] + "***"
}

// ServiceHubPermissionForPath 将 service-hub 路由映射为所需权限字符串。
// 对路径进行尾部斜杠归一化，防止 "/api/hub/dispatch/" 等带斜杠路径绕过 Scope 校验。
func ServiceHubPermissionForPath(path string) string {
	// 归一化尾部斜杠：Gin 在部分场景下会保留尾部斜杠，统一去除后匹配。
	if len(path) > 1 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}
	switch {
	case path == "/health" || path == "/readyz" || path == "/api/health":
		return ""
	case path == "/metrics":
		return ""
	case path == "/api/hub/status" || path == "/api/hub/tasks" ||
		strings.HasPrefix(path, "/api/hub/tasks/") ||
		path == "/api/hub/pipeline" ||
		path == "/api/hub/topology" ||
		path == "/api/hub/audit/logs" ||
		path == "/api/hub/datasources":
		return "hub:read"
	case path == "/api/hub/dispatch" || path == "/api/hub/classify" ||
		path == "/api/hub/fetch-and-desensitize" ||
		path == "/api/hub/audit/verify":
		return "hub:dispatch"
	default:
		// fail-closed：未显式映射的非豁免路径默认归入最高 admin 权限，防止空 scope 绕过
		return "admin"
	}
}
