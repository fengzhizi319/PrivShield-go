package grpcserver

// ==============================================================================
// 【gRPC 权限映射完整性门禁 + 鉴权拦截器对称性回归】
//
// 与 REST 侧 internal/rest/route_audit_test.go 的 TestAllRoutesHaveExplicitPermission
// 一一对应。此前 privacy-engine 只有 REST 侧有门禁，gRPC 侧的映射表长期无人核对：
// DPVectorMean 就曾在无人察觉的情况下静默落入 fail-closed 的 "admin" 兜底
// （对持有 privacy:dp 的合法调用方返回 403）。本文件把「加了 RPC 忘配权限」变成 CI 失败。
//
// 同时锁定 gRPC 鉴权拦截器与 REST 的行为对称性：
//   - 未映射方法必须落 "admin"（fail-closed，绝不返回空串造成放行）；
//   - AGENT_HEALTH_NO_AUTH=false 时 /Health 不再无条件匿名豁免（此前 gRPC 与 REST 不一致）；
//   - 认证通过后身份必须注入 ctx（供身份级限流与审计使用）。
// ==============================================================================

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "github.com/fengzhizi319/PrivShield-go/engine-go/internal/grpcserver/proto"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/security"
	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
)

const grpcServicePrefix = "/privacy.local.PrivacyService/"

// TestAllGRPCMethodsHaveExplicitPermission 遍历 proto 生成的全部 RPC（Unary + Stream），
// 断言没有任何方法落入 fail-closed 兜底权限 "admin"。新增 RPC 忘记配 scope 时 CI 立即失败。
func TestAllGRPCMethodsHaveExplicitPermission(t *testing.T) {
	// 有意要求最高权限的方法白名单（当前为空：引擎的 RPC 全部具备细粒度 scope）。
	allowFallback := map[string]bool{}

	var methods []string
	for _, m := range pb.PrivacyService_ServiceDesc.Methods {
		methods = append(methods, m.MethodName)
	}
	for _, s := range pb.PrivacyService_ServiceDesc.Streams {
		methods = append(methods, s.StreamName)
	}
	if len(methods) == 0 {
		t.Fatal("PrivacyService_ServiceDesc 未解析到任何方法，门禁失效")
	}

	for _, name := range methods {
		full := grpcServicePrefix + name
		if perm := pkgauth.PermissionForGRPCMethod(full); perm == "admin" && !allowFallback[name] {
			t.Errorf("gRPC method %s has no explicit scope mapping (fell through to fail-closed %q); "+
				"add it to pkg/auth.PermissionForGRPCMethod or登记到 allowFallback 白名单", name, "admin")
		}
	}
}

// TestGRPCPermissionMappingIsFailClosed 锁定兜底语义：未知方法必须要求 admin，
// 绝不允许退化为空串（空串在拦截器里意味着「不校验」，即 fail-open 越权面）。
func TestGRPCPermissionMappingIsFailClosed(t *testing.T) {
	cases := map[string]string{
		"Mask":            "privacy:mask",
		"DPVectorMean":    "privacy:dp", // 曾经漏配，锁死回归
		"DPVectorSum":     "privacy:dp",
		"KAnonymizeTable": "privacy:kano",
		"ObfuscateQuery":  "privacy:qol",
		"DynClassify":     "dynclassification:read",
		"RecommendParams": "privacy:profile",
	}
	for method, want := range cases {
		if got := pkgauth.PermissionForGRPCMethod(grpcServicePrefix + method); got != want {
			t.Errorf("PermissionForGRPCMethod(%q) = %q, want %q", method, got, want)
		}
	}
	if got := pkgauth.PermissionForGRPCMethod(grpcServicePrefix + "SomeBrandNewMethod"); got != "admin" {
		t.Errorf("unknown gRPC method must fall through to %q (fail-closed), got %q", "admin", got)
	}
}

func TestShortMethodName(t *testing.T) {
	if got := shortMethodName(grpcServicePrefix + "Mask"); got != "Mask" {
		t.Errorf("shortMethodName = %q, want Mask", got)
	}
	if got := shortMethodName("Mask"); got != "Mask" {
		t.Errorf("shortMethodName(no slash) = %q, want Mask", got)
	}
	if got := shortMethodName(""); got != "" {
		t.Errorf("shortMethodName(empty) = %q, want empty", got)
	}
}

// ──────────────────────────────────────────────
// 鉴权拦截器与 REST 的行为对称性
// ──────────────────────────────────────────────

// resetAuthSettings 在测试前后重置 security 单例，避免污染其他用例。
func resetAuthSettings(t *testing.T) {
	t.Helper()
	security.ResetSettings()
	t.Cleanup(security.ResetSettings)
}

func passthroughHandler(ctx context.Context, _ any) (any, error) { return ctx, nil }

func ctxWithAuth(token string) context.Context {
	md := metadata.New(map[string]string{})
	if token != "" {
		md.Set("authorization", token)
	}
	return metadata.NewIncomingContext(context.Background(), md)
}

func TestAuthInterceptor_InsufficientScope(t *testing.T) {
	resetAuthSettings(t)
	t.Setenv("AGENT_AUTH_ENABLED", "true")
	t.Setenv("AGENT_AUTH_INTERNAL_API_KEYS", "reader-key-1234567890:reader:privacy:mask")
	security.ResetSettings()

	ctx := ctxWithAuth("Bearer reader-key-1234567890")
	_, err := authUnaryInterceptor(ctx, nil, &grpc.UnaryServerInfo{
		FullMethod: grpcServicePrefix + "DPCount",
	}, passthroughHandler)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for mask-only key calling DPCount, got %v", err)
	}
}

func TestAuthInterceptor_ValidScopeInjectsIdentity(t *testing.T) {
	resetAuthSettings(t)
	t.Setenv("AGENT_AUTH_ENABLED", "true")
	t.Setenv("AGENT_AUTH_INTERNAL_API_KEYS", "mask-key-1234567890:mask-svc:privacy:mask")
	security.ResetSettings()

	ctx := ctxWithAuth("Bearer mask-key-1234567890")
	out, err := authUnaryInterceptor(ctx, nil, &grpc.UnaryServerInfo{
		FullMethod: grpcServicePrefix + "Mask",
	}, passthroughHandler)
	if err != nil {
		t.Fatalf("expected allow, got %v", err)
	}
	handlerCtx := out.(context.Context)
	id := IdentityFromContext(handlerCtx)
	if id == nil || id.Name != "mask-svc" {
		t.Fatalf("identity must be injected into ctx for downstream guards/audit, got %+v", id)
	}
	if got := CallerName(handlerCtx); got != "mask-svc" {
		t.Errorf("CallerName = %q, want mask-svc", got)
	}
}

// TestAuthInterceptor_HealthHonorsNoAuthFlag 对齐 REST：AGENT_HEALTH_NO_AUTH=false 时
// 健康探针也必须通过身份鉴别（历史上 gRPC 无条件豁免 /Health）。
func TestAuthInterceptor_HealthHonorsNoAuthFlag(t *testing.T) {
	resetAuthSettings(t)
	t.Setenv("AGENT_AUTH_ENABLED", "true")
	t.Setenv("AGENT_HEALTH_NO_AUTH", "false")
	t.Setenv("AGENT_AUTH_INTERNAL_API_KEYS", "health-key-1234567890:health-svc:health:read")
	security.ResetSettings()

	if _, err := authUnaryInterceptor(ctxWithAuth(""), nil, &grpc.UnaryServerInfo{
		FullMethod: grpcServicePrefix + "Health",
	}, passthroughHandler); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("Health must require credentials when AGENT_HEALTH_NO_AUTH=false, got %v", err)
	}

	out, err := authUnaryInterceptor(ctxWithAuth("Bearer health-key-1234567890"), nil, &grpc.UnaryServerInfo{
		FullMethod: grpcServicePrefix + "Health",
	}, passthroughHandler)
	if err != nil {
		t.Fatalf("Health with health:read key must pass, got %v", err)
	}
	_ = out
}

func TestAuthInterceptor_AuthDisabledInjectsAnonymous(t *testing.T) {
	resetAuthSettings(t)
	t.Setenv("AGENT_AUTH_ENABLED", "false")
	security.ResetSettings()

	out, err := authUnaryInterceptor(ctxWithAuth(""), nil, &grpc.UnaryServerInfo{
		FullMethod: grpcServicePrefix + "Mask",
	}, passthroughHandler)
	if err != nil {
		t.Fatalf("auth disabled must pass through, got %v", err)
	}
	if got := CallerName(out.(context.Context)); got != "anonymous" {
		t.Errorf("CallerName = %q, want anonymous", got)
	}
}

func TestGRPCRateLimitKeyFunc(t *testing.T) {
	settings := &security.Settings{HealthNoRateLimit: true}
	keyFunc := grpcRateLimitKeyFunc(settings)

	if got := keyFunc(context.Background(), grpcServicePrefix+"Health"); got != "" {
		t.Errorf("health must be exempt when HealthNoRateLimit=true, got %q", got)
	}

	idCtx := ContextWithIdentity(context.Background(), &pkgauth.Identity{
		ServiceType: "internal", Name: "mask-svc", Scopes: []string{"privacy:mask"},
	})
	got := keyFunc(idCtx, grpcServicePrefix+"Mask")
	if got != "internal:mask-svc:Mask" {
		t.Errorf("key = %q, want internal:mask-svc:Mask", got)
	}

	anon := keyFunc(context.Background(), grpcServicePrefix+"Mask")
	if !strings.HasPrefix(anon, "external:anonymous:Mask") {
		t.Errorf("anonymous key must stay identity+method bounded, got %q", anon)
	}
}
