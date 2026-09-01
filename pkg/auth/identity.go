// Package auth provides shared identity, permission mapping and scope-based authentication.
//
// 从 engine-go/internal/security 下沉到 pkg/auth，供 services、console 及 engine-go 统一使用。
package auth

import "strings"

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
func PermissionForRESTPath(path string) string {
	// 去除尾部斜杠
	if len(path) > 1 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}
	switch {
	case path == "/health" || path == "/livez" || path == "/readyz" || path == "/readyz/llm":
		return "health:read"
	case strings.HasPrefix(path, "/v1/privacy/mask"):
		return "privacy:mask"
	case path == "/v1/privacy/hash":
		return "privacy:hash"
	case strings.HasPrefix(path, "/v1/privacy/dp/") || strings.HasPrefix(path, "/v1/privacy/ldp/"):
		return "privacy:dp"
	case strings.HasPrefix(path, "/v1/privacy/k_anonymize"):
		return "privacy:kano"
	case strings.HasPrefix(path, "/v1/privacy/qol/"):
		return "privacy:qol"
	case path == "/v1/privacy/budget":
		return "privacy:budget"
	case path == "/v1/privacy/profile/recommend":
		return "privacy:profile"
	case path == "/v1/privacy/process_file":
		return "privacy:mask"
	case strings.HasPrefix(path, "/v1/privacy/classify/"):
		return "classification:read"
	case strings.HasPrefix(path, "/v1/dynclassification"):
		if path == "/v1/dynclassification/profiles/reload" || path == "/v1/dynclassification/generate_profile" {
			return "dynclassification:write"
		}
	case strings.HasPrefix(path, "/v1/agent"):
		return "agent:process"
	case strings.HasPrefix(path, "/v1/medical"):
		return "medical:process"
	case strings.HasPrefix(path, "/v1/pipeline"):
		return "pipeline:process"
	case strings.HasPrefix(path, "/v1/ops/"):
		return "ops:diagnostics"
	case strings.HasPrefix(path, "/debug/pprof"):
		return "ops:admin"
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
