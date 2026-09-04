package grpcserver

// ==============================================================================
// 【gRPC 方法权限映射完整性门禁 · H-3】
// 与 REST 侧 route_audit_test.go 的 TestAllRoutesHaveExplicitPermission 对应：
// 遍历 proto 生成的 ServiceHubService 全部方法，断言每个非健康探针方法都映射到
// 显式的、非兜底（非 "admin"）scope，防止「加了 RPC 忘配权限」导致方法落入
// fail-closed 的 "admin" 兜底而实际拒绝合法调用，或因历史 fail-open 默认而越权。
//
// ServiceHubPermissionForGRPCMethod 未命中即返回 "admin"（fail-closed），
// 因此任何遗漏映射的新增业务方法都会在本测试中失败，强制开发者补显式 case。
// ==============================================================================

import (
	"context"
	"strings"
	"testing"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
	pb "github.com/fengzhizi319/PrivShield-go/services/service-hub/proto"
)

// TestAllGRPCMethodsHaveExplicitPermission 断言 ServiceHubService 的每个业务方法
// 都拥有显式的 scope 映射（既非空串也非 fail-closed 兜底 "admin"）。健康探针豁免。
func TestAllGRPCMethodsHaveExplicitPermission(t *testing.T) {
	serviceName := pb.ServiceHubService_ServiceDesc.ServiceName
	methods := pb.ServiceHubService_ServiceDesc.Methods

	if len(methods) == 0 {
		t.Fatal("ServiceHubService_ServiceDesc.Methods is empty; proto may not be generated")
	}

	for _, m := range methods {
		fullMethod := serviceName + "/" + m.MethodName

		// 健康探针显式豁免（返回空串是设计意图）。
		if pkgauth.IsHealthPathOrMethod(fullMethod) {
			continue
		}

		perm := ServiceHubPermissionForGRPCMethod(fullMethod)
		if perm == "" {
			t.Errorf("gRPC method %q maps to empty scope (fail-open): add an explicit case in ServiceHubPermissionForGRPCMethod", fullMethod)
			continue
		}
		if strings.EqualFold(perm, "admin") {
			t.Errorf("gRPC method %q falls into fail-closed 'admin' fallback (unmapped): add an explicit scope mapping", fullMethod)
			continue
		}
	}
}

// TestServiceHubPermissionForGRPCMethodFailClosed 验证未映射方法确实落入 "admin"（而非空串），
// 锁死 H-1 修复：防止 default 分支被误改回 fail-open。
func TestServiceHubPermissionForGRPCMethodFailClosed(t *testing.T) {
	full := pb.ServiceHubService_ServiceDesc.ServiceName + "/SomeFutureMethod"
	if got := ServiceHubPermissionForGRPCMethod(full); got != "admin" {
		t.Errorf("unmapped gRPC method default = %q, want \"admin\" (fail-closed)", got)
	}
	// FetchAndDesensitize 必须显式映射到 hub:dispatch（最敏感 PII 拉取操作）。
	fad := pb.ServiceHubService_ServiceDesc.ServiceName + "/FetchAndDesensitize"
	if got := ServiceHubPermissionForGRPCMethod(fad); got != "hub:dispatch" {
		t.Errorf("FetchAndDesensitize scope = %q, want \"hub:dispatch\"", got)
	}
}

// TestCheckDatasourceAccessGRPCParity 验证 gRPC 侧 ABAC 与 REST 使用同一 pkg 判定，
// 覆盖无身份放行、超级权限放行、细粒度限定越权拒绝三类核心场景。
func TestCheckDatasourceAccessGRPCParity(t *testing.T) {
	s := &GRPCServer{}

	// 无身份（开发/免密）→ 放行。
	if !s.checkDatasourceAccess(context.Background(), "ds_yibao", "yibao") {
		t.Error("nil identity should be allowed (dev/no-auth mode)")
	}

	// 超级权限放行。
	adminCtx := ContextWithIdentity(t.Context(), &pkgauth.Identity{Scopes: []string{"*"}})
	if !s.checkDatasourceAccess(adminCtx, "ds_yibao", "yibao") {
		t.Error(`"*" scope should be allowed`)
	}

	// 声明了细粒度数据源限定但未命中当前数据源 → 拒绝（越权）。
	restrictedCtx := ContextWithIdentity(t.Context(), &pkgauth.Identity{Scopes: []string{"hub:dispatch:ds_kangyang"}})
	if s.checkDatasourceAccess(restrictedCtx, "ds_yibao", "yibao") {
		t.Error("caller restricted to ds_kangyang must NOT access ds_yibao")
	}
	if !s.checkDatasourceAccess(restrictedCtx, "ds_kangyang", "kangyang") {
		t.Error("caller with hub:dispatch:ds_kangyang must access ds_kangyang")
	}
}
