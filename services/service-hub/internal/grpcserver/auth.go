package grpcserver

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
)

// identityCtxKey 是 gRPC context 中存储已认证身份的私有键（区别于 gin.Context 的 IdentityContextKey）。
type identityCtxKey struct{}

// ContextWithIdentity 将已认证身份注入 gRPC context，供下游业务方法做数据源级 ABAC 校验。
func ContextWithIdentity(ctx context.Context, id *pkgauth.Identity) context.Context {
	return context.WithValue(ctx, identityCtxKey{}, id)
}

// IdentityFromContext 从 gRPC context 提取认证身份（未注入时返回 nil）。
func IdentityFromContext(ctx context.Context) *pkgauth.Identity {
	if id, ok := ctx.Value(identityCtxKey{}).(*pkgauth.Identity); ok {
		return id
	}
	return nil
}

// identityServerStream 包装 grpc.ServerStream，使流式 handler 能通过 Context() 读到注入的身份。
type identityServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *identityServerStream) Context() context.Context { return s.ctx }

var (
	authOnce      sync.Once
	authAPIKey    string
	authScopeKeys map[string]*pkgauth.KeyConfig
	authKeyStore  *pkgauth.KeyStore
)

// InitAuthSettings 存储 gRPC 鉴权配置，在 main.go 中调用一次。
// ks 可为 nil（未配置文件热轮转时回退到静态 scopeKeys）。
func InitAuthSettings(apiKey string, scopeKeys map[string]*pkgauth.KeyConfig, ks *pkgauth.KeyStore) {
	authOnce.Do(func() {
		authAPIKey = apiKey
		authScopeKeys = scopeKeys
		authKeyStore = ks
		if apiKey != "" {
			slog.Warn("gRPC auth: legacy single API key configured; it will be treated as having no scopes for gRPC. Migrate to scope-based keys (SERVICE_HUB_API_KEYS / SERVICE_HUB_API_KEYS_FILE) for service-to-service gRPC access",
				"component", "service-hub-grpc")
		}
	})
}

func currentScopeKeys() map[string]*pkgauth.KeyConfig {
	if authKeyStore != nil {
		return authKeyStore.Keys()
	}
	return authScopeKeys
}

// AuthUnaryInterceptor 返回 gRPC 一元鉴权拦截器。
// 未配置任何 key 时默认拒绝（fail-closed），不再透传未认证请求。
func AuthUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if pkgauth.IsHealthPathOrMethod(info.FullMethod) {
			return handler(ctx, req)
		}
		scopeKeys := currentScopeKeys()
		if authAPIKey == "" && len(scopeKeys) == 0 {
			pkgauth.AuthFailuresTotal.WithLabelValues("missing_token").Inc()
			return nil, status.Error(codes.Unauthenticated, "authentication required: no API key configured")
		}
		identity, err := authenticateGRPCRequest(ctx, authAPIKey, scopeKeys)
		if err != nil {
			return nil, err
		}
		if requiredPerm := ServiceHubPermissionForGRPCMethod(info.FullMethod); requiredPerm != "" && !identity.HasPermission(requiredPerm) {
			pkgauth.AuthForbiddenTotal.Inc()
			return nil, status.Errorf(codes.PermissionDenied, "insufficient scope: need %q", requiredPerm)
		}
		// H-2：将已认证身份注入 ctx，使业务方法可做数据源级 ABAC（与 REST 双路径对齐）。
		return handler(ContextWithIdentity(ctx, identity), req)
	}
}

// AuthStreamInterceptor 返回 gRPC 流式鉴权拦截器。
func AuthStreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if pkgauth.IsHealthPathOrMethod(info.FullMethod) {
			return handler(srv, ss)
		}
		scopeKeys := currentScopeKeys()
		if authAPIKey == "" && len(scopeKeys) == 0 {
			pkgauth.AuthFailuresTotal.WithLabelValues("missing_token").Inc()
			return status.Error(codes.Unauthenticated, "authentication required: no API key configured")
		}
		identity, err := authenticateGRPCRequest(ss.Context(), authAPIKey, scopeKeys)
		if err != nil {
			return err
		}
		if requiredPerm := ServiceHubPermissionForGRPCMethod(info.FullMethod); requiredPerm != "" && !identity.HasPermission(requiredPerm) {
			pkgauth.AuthForbiddenTotal.Inc()
			return status.Errorf(codes.PermissionDenied, "insufficient scope: need %q", requiredPerm)
		}
		// H-2：注入身份后包装流，供流式 handler 做数据源级 ABAC。
		return handler(srv, &identityServerStream{ServerStream: ss, ctx: ContextWithIdentity(ss.Context(), identity)})
	}
}

// ServiceHubPermissionForGRPCMethod 将 service-hub gRPC 方法映射为所需权限字符串。
// 未命中任何显式映射的方法 fail-closed 归入最高 "admin" 权限（与 REST 侧
// ServiceHubPermissionForPath 及共享库 PermissionForGRPCMethod 的默认拒绝语义一致），
// 防止新增 RPC 因漏配 scope 而落入「仅需认证」的越权面。仅 Health 探针显式豁免（返回 ""）。
func ServiceHubPermissionForGRPCMethod(fullMethod string) string {
	switch {
	case strings.HasSuffix(fullMethod, "/Health"):
		return ""
	case strings.HasSuffix(fullMethod, "/HubStatus"),
		strings.HasSuffix(fullMethod, "/GetTask"),
		strings.HasSuffix(fullMethod, "/ListTasks"),
		strings.HasSuffix(fullMethod, "/PipelineStatus"):
		return "hub:read"
	case strings.HasSuffix(fullMethod, "/Dispatch"),
		strings.HasSuffix(fullMethod, "/ClassifyAndDispatch"),
		strings.HasSuffix(fullMethod, "/FetchAndDesensitize"):
		return "hub:dispatch"
	}
	// fail-closed：未显式映射的 gRPC 方法默认要求最高 admin 权限。
	return "admin"
}

func authenticateGRPCRequest(ctx context.Context, apiKey string, scopeKeys map[string]*pkgauth.KeyConfig) (*pkgauth.Identity, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		pkgauth.AuthFailuresTotal.WithLabelValues("missing_metadata").Inc()
		return nil, status.Error(codes.Unauthenticated, "missing metadata")
	}

	var token string
	for _, v := range md.Get("authorization") {
		if strings.HasPrefix(v, "Bearer ") {
			token = v[len("Bearer "):]
			break
		}
		token = v
		break
	}
	if token == "" {
		pkgauth.AuthFailuresTotal.WithLabelValues("missing_token").Inc()
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}

	// 遗留单 Key 不再授予通配符 scope；生产环境请迁移到 scope-based key。
	internalKeys := make(map[string]*pkgauth.KeyConfig)
	if apiKey != "" {
		internalKeys[apiKey] = &pkgauth.KeyConfig{Name: "default-internal", Scopes: []string{}}
	}

	settings := &pkgauth.Settings{
		AuthEnabled:  true,
		InternalKeys: internalKeys,
		ExternalKeys: scopeKeys,
	}
	identity := pkgauth.AuthenticateAPIKey(settings, token)
	if identity == nil {
		pkgauth.AuthFailuresTotal.WithLabelValues("invalid_token").Inc()
		return nil, status.Error(codes.Unauthenticated, "invalid credentials")
	}

	return identity, nil
}
