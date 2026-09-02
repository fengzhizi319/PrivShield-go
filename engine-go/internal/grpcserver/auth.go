// Package grpcserver 提供 gRPC 服务端认证与鉴权拦截器。
package grpcserver

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/security"
)

// authUnaryInterceptor 为 gRPC 请求提供 API Key 认证与 Scope 权限校验。
// 三级等保/密评要求：通信双方进行身份鉴别；当 REST 侧启用 API Key 鉴权时，
// gRPC 侧不能单独依赖 mTLS 而缺失应用层鉴权。
func authUnaryInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	settings := security.GetSettings()
	// 未启用鉴权或健康检查时放行
	if !settings.AuthEnabled || pkgauth.IsHealthPathOrMethod(info.FullMethod) {
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
		// 也支持裸 token（部分内部 gRPC 客户端使用）
		token = v
		break
	}

	if token == "" {
		return nil, status.Error(codes.Unauthenticated, "missing authorization")
	}

	identity := pkgauth.AuthenticateAPIKey(&settings.Settings, token)
	if identity == nil {
		return nil, status.Error(codes.Unauthenticated, "invalid credentials")
	}

	requiredPerm := pkgauth.PermissionForGRPCMethod(info.FullMethod)
	if requiredPerm != "" && !identity.HasPermission(requiredPerm) {
		return nil, status.Error(codes.PermissionDenied, "insufficient scope")
	}

	return handler(ctx, req)
}
