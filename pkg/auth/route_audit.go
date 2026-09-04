// Package auth — 路由权限审计（route permission audit）。
//
// 背景：各微服务采用「集中式 path→permission 映射」（见 PermissionForRESTPath /
// ServiceHubPermissionForPath / AuditLogPermissionForPath），路由注册与权限映射分离。
// 新增接口若遗漏映射，会静默落入 fail-closed 兜底权限（如 "admin"），既有被误锁死、
// 又缺少细粒度隔离的风险。本文件提供启动审计与测试门禁两种发现手段（推荐方案二 + 方案三）。
package auth

import (
	"log/slog"
	"sort"

	"github.com/gin-gonic/gin"
)

// RoutePermissionIssue 描述一条落入 fail-closed 兜底权限（未显式配置 scope）的路由。
type RoutePermissionIssue struct {
	Method       string
	Path         string
	FallbackPerm string
}

// AuditRoutePermissions 遍历已注册的 Gin 路由，识别落入 fail-closed 兜底权限（未显式映射
// scope）的接口，用于发现「新增路由遗漏权限配置」的问题。
//
//   - permFunc: 将 (method, path) 映射为所需权限字符串（各服务 PermissionForPath 的统一签名适配）。
//   - fallbackPerms: 该服务的 fail-closed 兜底权限集合（如 {"admin"} / {"audit:admin"}），
//     命中即表示该路由未显式映射。
//   - allowFallback: 有意使用兜底权限的基础设施路由 path 白名单（如 "/metrics"、pprof），
//     命中的 path 不计入告警；新增的、未列入白名单的兜底路由仍会被报告。可为 nil。
//
// 返回未显式配置权限的路由列表（按 path、method 稳定排序）；调用方可据策略选择仅告警、
// 或在测试中断言列表为空作为 CI 门禁。
func AuditRoutePermissions(routes []gin.RouteInfo, permFunc func(method, path string) string, fallbackPerms map[string]bool, allowFallback map[string]bool) []RoutePermissionIssue {
	var issues []RoutePermissionIssue
	for _, rt := range routes {
		if allowFallback[rt.Path] {
			continue
		}
		if perm := permFunc(rt.Method, rt.Path); fallbackPerms[perm] {
			issues = append(issues, RoutePermissionIssue{Method: rt.Method, Path: rt.Path, FallbackPerm: perm})
		}
	}
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Path != issues[j].Path {
			return issues[i].Path < issues[j].Path
		}
		return issues[i].Method < issues[j].Method
	})
	return issues
}

// LogRoutePermissionAudit 执行路由权限审计并通过日志输出结果。发现落入兜底权限的路由时打
// WARN（列出具体 method+path），全部覆盖时打 DEBUG。该方法仅提醒，不阻断启动。
// allowFallback 语义同 AuditRoutePermissions：有意使用兜底权限的基础设施路由 path 白名单。
func LogRoutePermissionAudit(logger *slog.Logger, moduleName string, routes []gin.RouteInfo, permFunc func(method, path string) string, fallbackPerms map[string]bool, allowFallback map[string]bool) {
	if logger == nil {
		logger = slog.Default()
	}
	issues := AuditRoutePermissions(routes, permFunc, fallbackPerms, allowFallback)
	if len(issues) == 0 {
		logger.Debug("route permission audit: all routes have explicit scope mapping",
			"module", moduleName, "route_count", len(routes))
		return
	}
	unmapped := make([]string, 0, len(issues))
	for _, is := range issues {
		unmapped = append(unmapped, is.Method+" "+is.Path)
	}
	logger.Warn("route permission audit: routes fell through to fail-closed default scope (missing explicit permission mapping)",
		"module", moduleName,
		"count", len(issues),
		"unmapped_routes", unmapped,
		"hint", "register these paths in the service PermissionForPath mapping, or grant callers the fallback admin scope")
}
