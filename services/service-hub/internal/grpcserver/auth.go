package grpcserver

import (
	"context"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/config"
)

var (
	authOnce    sync.Once
	authAPIKey  string
	authScopeKeys map[string]*pkgauth.KeyConfig
)

// InitAuthSettings 存储 gRPC 鉴权配置，供 AuthUnaryInterceptor/AuthStreamInterceptor 使用。
func InitAuthSettings(cfg *config.Config) {
	authOnce.Do(func() {
		authAPIKey = cfg.APIKey
		authScopeKeys = cfg.ScopeKeys
	})
}

// AuthUnaryInterceptor 返回使用已初始化配置的 gRPC 一元鉴权拦截器。
func AuthUnaryInterceptor() grpc.UnaryServerInterceptor {
	return authUnaryInterceptor(authAPIKey, authScopeKeys)
}

// AuthStreamInterceptor 返回使用已初始化配置的 gRPC 流式鉴权拦截器。
func AuthStreamInterceptor() grpc.StreamServerInterceptor {
	return authStreamInterceptor(authAPIKey, authScopeKeys)
}

// ServiceHubPermissionForGRPCMethod 将 service-hub gRPC 方法映射为所需权限字符串。
// 与 REST 侧 ServiceHubPermissionForPath 保持权限语义一致。
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

// authUnaryInterceptor 为 service-hub gRPC 提供应用层 API Key 鉴权（三级等保）。
// 将 legacy apiKey 映射到 InternalKeys、scopeKeys 映射到 ExternalKeys，
// 复用 pkg/auth.AuthenticateAPIKey 的常量时间查找链路。
func authUnaryInterceptor(apiKey string, scopeKeys map[string]*pkgauth.KeyConfig) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if pkgauth.IsHealthPathOrMethod(info.FullMethod) {
			return handler(ctx, req)
		}

		if apiKey == "" && len(scopeKeys) == 0 {
			return handler(ctx, req)
		}

		identity, err := authenticateGRPCRequest(ctx, apiKey, scopeKeys)
		if err != nil {
			return nil, err
		}

		requiredPerm := ServiceHubPermissionForGRPCMethod(info.FullMethod)
		if requiredPerm != "" && !identity.HasPermission(requiredPerm) {
			return nil, status.Error(codes.PermissionDenied, "insufficient scope")
		}

		return handler(ctx, req)
	}
}

// authStreamInterceptor 为 service-hub gRPC 流式调用提供应用层 API Key 鉴权。
func authStreamInterceptor(apiKey string, scopeKeys map[string]*pkgauth.KeyConfig) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if pkgauth.IsHealthPathOrMethod(info.FullMethod) {
			return handler(srv, ss)
		}

		if apiKey == "" && len(scopeKeys) == 0 {
			return handler(srv, ss)
		}

		identity, err := authenticateGRPCRequest(ss.Context(), apiKey, scopeKeys)
		if err != nil {
			return err
		}

		requiredPerm := ServiceHubPermissionForGRPCMethod(info.FullMethod)
		if requiredPerm != "" && !identity.HasPermission(requiredPerm) {
			return status.Error(codes.PermissionDenied, "insufficient scope")
		}

		return handler(srv, ss)
	}
}

// authenticateGRPCRequest 从 gRPC metadata 提取 Bearer token 并校验身份。
func authenticateGRPCRequest(ctx context.Context, apiKey string, scopeKeys map[string]*pkgauth.KeyConfig) (*pkgauth.Identity, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
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
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}

	internalKeys := make(map[string]*pkgauth.KeyConfig)
	if apiKey != "" {
		internalKeys[apiKey] = &pkgauth.KeyConfig{Name: "default-internal", Scopes: []string{"*"}}
	}

	settings := &pkgauth.Settings{
		AuthEnabled:  true,
		InternalKeys: internalKeys,
		ExternalKeys: scopeKeys,
	}
	identity := pkgauth.AuthenticateAPIKey(settings, token)
	if identity == nil {
		return nil, status.Error(codes.Unauthenticated, "invalid credentials")
	}

	return identity, nil
}
