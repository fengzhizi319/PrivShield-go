// Package tlsutil provides unit tests for gRPC mTLS interceptors.
// Package tlsutil 为 gRPC mTLS CN 鉴权拦截器提供全量单元测试与异常模拟测试套件。
//
// ==============================================================================
// 【测试场景与断言依据】
// 1. 【extractClientCN】：
//    - 正常携带有效 TLS peer 的 context 成功解析 CN；
//    - 缺失 peer 上下文时返回 codes.Unauthenticated；
//    - 缺失 TLSInfo 认证凭据时返回 codes.Unauthenticated；
// 2. 【authorizeClient】：
//    - 通配符 `*` 允许任意 RPC 调用；
//    - 精确方法匹配与前缀模式匹配放行；
//    - 越权方法调用拦截并返回 codes.PermissionDenied；
//    - 未登记的未知客户端 CN 拦截并返回 codes.PermissionDenied；
// 3. 【UnaryServerInterceptor】：
//    - 授权通过时透明调用下游 handler；
//    - 未授权 CN 拦截并不调用 handler；
//    - 越权方法拦截；
//    - 非 TLS 明文请求拦截；
// 4. 【NewWhitelistInterceptor】：
//    - 空路径静默回退；
//    - 有效路径装配并拦截未授权请求。
// ==============================================================================

package tlsutil

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// tlsPeerContext 构造一个携带模拟 TLS peer（含指定 CN）的 context。
func tlsPeerContext(cn string) context.Context {
	return peer.NewContext(context.Background(), &peer.Peer{
		AuthInfo: credentials.TLSInfo{
			State: tls.ConnectionState{
				VerifiedChains: [][]*x509.Certificate{
					{{Subject: pkix.Name{CommonName: cn}}},
				},
			},
		},
	})
}

// ─────────────────────────────────────────────────────────────
// 1. extractClientCN 证书 CommonName 提取测试
// ─────────────────────────────────────────────────────────────

func TestExtractClientCN_Valid(t *testing.T) {
	ctx := tlsPeerContext("test-client.internal")
	cn, err := extractClientCN(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cn != "test-client.internal" {
		t.Errorf("CN = %q, want %q", cn, "test-client.internal")
	}
}

func TestExtractClientCN_NoPeer(t *testing.T) {
	_, err := extractClientCN(context.Background())
	if err == nil {
		t.Fatal("expected error for missing peer")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated, got %v", err)
	}
}

func TestExtractClientCN_NoTLS(t *testing.T) {
	ctx := peer.NewContext(context.Background(), &peer.Peer{})
	_, err := extractClientCN(ctx)
	if err == nil {
		t.Fatal("expected error for missing TLS info")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated, got %v", st.Code())
	}
}

// ─────────────────────────────────────────────────────────────
// 2. authorizeClient 权限匹配逻辑测试
// ─────────────────────────────────────────────────────────────

func TestAuthorizeClient_WildcardScope(t *testing.T) {
	path := createTempWhitelistForInterceptor(t)
	dw, err := NewDynamicWhitelist(path)
	if err != nil {
		t.Fatalf("NewDynamicWhitelist failed: %v", err)
	}
	defer dw.Close()

	// bff-go 拥有通配符 scope: 允许调用任意方法
	if err := dw.authorizeClient("bff-go.privshield.internal", "/AnyService/AnyMethod"); err != nil {
		t.Errorf("expected wildcard scope to allow any method, got: %v", err)
	}
}

func TestAuthorizeClient_SpecificScope(t *testing.T) {
	path := createTempWhitelistForInterceptor(t)
	dw, err := NewDynamicWhitelist(path)
	if err != nil {
		t.Fatalf("NewDynamicWhitelist failed: %v", err)
	}
	defer dw.Close()

	// service-hub 拥有 /PrivacyService/Process 权限
	if err := dw.authorizeClient("service-hub.privshield.internal", "/PrivacyService/Process"); err != nil {
		t.Errorf("expected allowed for /PrivacyService/Process, got: %v", err)
	}

	// service-hub 拥有 /AuditLog/* 权限，匹配 /AuditLog/RecordAudit
	if err := dw.authorizeClient("service-hub.privshield.internal", "/AuditLog/RecordAudit"); err != nil {
		t.Errorf("expected allowed for /AuditLog/RecordAudit via wildcard, got: %v", err)
	}

	// service-hub 未被授予 /DatasourceMgr/FetchSlice
	err = dw.authorizeClient("service-hub.privshield.internal", "/DatasourceMgr/FetchSlice")
	if err == nil {
		t.Fatal("expected PermissionDenied for unauthorized method")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

func TestAuthorizeClient_UnknownCN(t *testing.T) {
	path := createTempWhitelistForInterceptor(t)
	dw, err := NewDynamicWhitelist(path)
	if err != nil {
		t.Fatalf("NewDynamicWhitelist failed: %v", err)
	}
	defer dw.Close()

	err = dw.authorizeClient("unknown-client", "/AnyMethod")
	if err == nil {
		t.Fatal("expected PermissionDenied for unknown CN")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

// ─────────────────────────────────────────────────────────────
// 3. UnaryServerInterceptor 一元拦截器行为测试
// ─────────────────────────────────────────────────────────────

func TestUnaryServerInterceptor_Authorized(t *testing.T) {
	path := createTempWhitelistForInterceptor(t)
	dw, err := NewDynamicWhitelist(path)
	if err != nil {
		t.Fatalf("NewDynamicWhitelist failed: %v", err)
	}
	defer dw.Close()

	interceptor := dw.UnaryServerInterceptor()
	ctx := tlsPeerContext("bff-go.privshield.internal")
	handlerCalled := false

	resp, err := interceptor(ctx, "test-request", &grpc.UnaryServerInfo{FullMethod: "/TestService/TestMethod"}, func(ctx context.Context, req any) (any, error) {
		handlerCalled = true
		return "handler-response", nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handlerCalled {
		t.Error("expected handler to be called")
	}
	if resp != "handler-response" {
		t.Errorf("unexpected response: %v", resp)
	}
}

func TestUnaryServerInterceptor_Unauthorized(t *testing.T) {
	path := createTempWhitelistForInterceptor(t)
	dw, err := NewDynamicWhitelist(path)
	if err != nil {
		t.Fatalf("NewDynamicWhitelist failed: %v", err)
	}
	defer dw.Close()

	interceptor := dw.UnaryServerInterceptor()
	ctx := tlsPeerContext("unknown-client")

	_, err = interceptor(ctx, "test-request", &grpc.UnaryServerInfo{FullMethod: "/TestService/TestMethod"}, func(ctx context.Context, req any) (any, error) {
		t.Error("handler should NOT be called for unauthorized CN")
		return nil, nil
	})

	if err == nil {
		t.Fatal("expected error for unauthorized CN")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

func TestUnaryServerInterceptor_ScopeViolation(t *testing.T) {
	path := createTempWhitelistForInterceptor(t)
	dw, err := NewDynamicWhitelist(path)
	if err != nil {
		t.Fatalf("NewDynamicWhitelist failed: %v", err)
	}
	defer dw.Close()

	interceptor := dw.UnaryServerInterceptor()
	ctx := tlsPeerContext("service-hub.privshield.internal")

	_, err = interceptor(ctx, "test-request", &grpc.UnaryServerInfo{FullMethod: "/DatasourceMgr/FetchSlice"}, func(ctx context.Context, req any) (any, error) {
		t.Error("handler should NOT be called for out-of-scope method")
		return nil, nil
	})

	if err == nil {
		t.Fatal("expected error for out-of-scope method")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}

func TestUnaryServerInterceptor_NoTLS(t *testing.T) {
	path := createTempWhitelistForInterceptor(t)
	dw, err := NewDynamicWhitelist(path)
	if err != nil {
		t.Fatalf("NewDynamicWhitelist failed: %v", err)
	}
	defer dw.Close()

	interceptor := dw.UnaryServerInterceptor()
	ctx := context.Background()

	_, err = interceptor(ctx, "test-request", &grpc.UnaryServerInfo{FullMethod: "/TestService/TestMethod"}, func(ctx context.Context, req any) (any, error) {
		t.Error("handler should NOT be called without TLS")
		return nil, nil
	})

	if err == nil {
		t.Fatal("expected error for non-TLS connection")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated, got %v", st.Code())
	}
}

func createTempWhitelistForInterceptor(t *testing.T) string {
	t.Helper()
	return createTempWhitelist(t, testWhitelistYAML)
}

// ─────────────────────────────────────────────────────────────
// 4. NewWhitelistInterceptor 工厂方法测试
// ─────────────────────────────────────────────────────────────

func TestNewWhitelistInterceptor_EmptyPath(t *testing.T) {
	unary, stream, dw, err := NewWhitelistInterceptor("")
	if err != nil {
		t.Fatalf("unexpected error for empty path: %v", err)
	}
	if unary != nil {
		t.Error("expected nil unary interceptor for empty path")
	}
	if stream != nil {
		t.Error("expected nil stream interceptor for empty path")
	}
	if dw != nil {
		t.Error("expected nil DynamicWhitelist for empty path")
	}
}

func TestNewWhitelistInterceptor_LoadAndAuthorize(t *testing.T) {
	path := createTempWhitelistForInterceptor(t)
	unary, stream, dw, err := NewWhitelistInterceptor(path)
	if err != nil {
		t.Fatalf("NewWhitelistInterceptor failed: %v", err)
	}
	defer dw.Close()
	if unary == nil {
		t.Fatal("expected non-nil unary interceptor")
	}
	if stream == nil {
		t.Fatal("expected non-nil stream interceptor")
	}

	ctx := tlsPeerContext("unknown-client")
	_, err = unary(ctx, "test-request", &grpc.UnaryServerInfo{FullMethod: "/TestService/TestMethod"}, func(ctx context.Context, req any) (any, error) {
		t.Error("handler should NOT be called for unauthorized CN")
		return nil, nil
	})
	if err == nil {
		t.Fatal("expected error for unauthorized CN")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.PermissionDenied {
		t.Errorf("expected PermissionDenied, got %v", st.Code())
	}
}
