package grpcserver

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
)

// AuthUnaryInterceptor 为 audit-log gRPC 提供应用层 API Key 鉴权（三级等保 G-17）。
// 当 cfg.APIKey 非空且请求不是健康检查时，从 metadata 读取 Bearer token 并校验。
func AuthUnaryInterceptor(apiKey string, scopeKeys map[string]*pkgauth.KeyConfig) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// 健康检查免鉴权
		if pkgauth.IsHealthPathOrMethod(info.FullMethod) {
			return handler(ctx, req)
		}

		// 未配置 API Key 时放行（保持现有 mTLS 鉴权）
		if apiKey == "" && len(scopeKeys) == 0 {
			return handler(ctx, req)
		}

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

		settings := &pkgauth.Settings{
			AuthEnabled:  true,
			InternalKeys: make(map[string]*pkgauth.KeyConfig),
		}
		if apiKey != "" {
			settings.InternalKeys[apiKey] = &pkgauth.KeyConfig{Name: "default", Scopes: []string{"*"}}
		}
		for k, v := range scopeKeys {
			settings.InternalKeys[k] = v
		}
		identity := pkgauth.AuthenticateAPIKey(settings, token)
		if identity == nil {
			return nil, status.Error(codes.Unauthenticated, "invalid credentials")
		}

		return handler(ctx, req)
	}
}
