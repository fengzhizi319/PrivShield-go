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
		return handler(ctx, req)
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
		return handler(srv, ss)
	}
}

// ServiceHubPermissionForGRPCMethod 将 service-hub gRPC 方法映射为所需权限字符串。
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
		strings.HasSuffix(fullMethod, "/ClassifyAndDispatch"):
		return "hub:dispatch"
	}
	return ""
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
