// Package grpcserver 提供 gRPC 服务端认证与鉴权拦截器。
package grpcserver

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/security"
	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
)

// identityCtxKey 是 gRPC context 中存储已认证身份的私有键（区别于 gin.Context 的 IdentityContextKey）。
type identityCtxKey struct{}

// ContextWithIdentity 将已认证身份注入 gRPC context，供下游限流分片键与审计日志复用。
func ContextWithIdentity(ctx context.Context, id *pkgauth.Identity) context.Context {
	return context.WithValue(ctx, identityCtxKey{}, id)
}

// IdentityFromContext 从 gRPC context 提取认证身份（未注入或无身份时返回 nil）。
func IdentityFromContext(ctx context.Context) *pkgauth.Identity {
	if ctx == nil {
		return nil
	}
	if id, ok := ctx.Value(identityCtxKey{}).(*pkgauth.Identity); ok {
		return id
	}
	return nil
}

// CallerName 返回当前 gRPC 调用者标识名称，未认证时返回 "anonymous"。
func CallerName(ctx context.Context) string {
	if id := IdentityFromContext(ctx); id != nil && id.Name != "" {
		return id.Name
	}
	return "anonymous"
}

// extractGRPCToken 从 incoming metadata 提取凭据。
// 兼容裸 token：部分内部 gRPC 客户端不携带 "Bearer " 前缀（仓库内客户端均已使用 Bearer）。
func extractGRPCToken(md metadata.MD) string {
	for _, v := range md.Get("authorization") {
		if strings.HasPrefix(v, "Bearer ") {
			return v[len("Bearer "):]
		}
		return v
	}
	return ""
}

// authUnaryInterceptor 为 gRPC 请求提供 API Key 认证与 Scope 权限校验。
// 三级等保/密评要求：通信双方进行身份鉴别；当 REST 侧启用 API Key 鉴权时，
// gRPC 侧不能单独依赖 mTLS 而缺失应用层鉴权。
//
// 与 REST 侧的对称性（本次加固）：
//  1. 认证统一走 pkgauth.AuthenticateAPIKey(&settings.Settings, token)，因此
//     AGENT_AUTH_KEYS_FILE 的热轮转/吊销对 gRPC 面同样即时生效（历史上 gRPC 只吃启动快照）；
//  2. 健康探针豁免同 REST 一样受 AGENT_HEALTH_NO_AUTH 约束（历史上 gRPC 无条件豁免 /Health）；
//  3. 认证通过后把身份注入 ctx，供身份级限流与审计使用。
func authUnaryInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	settings := security.GetSettings()
	// 未启用鉴权时放行（注入匿名身份，与 REST 侧行为一致）；健康检查按配置豁免。
	if pkgauth.IsHealthPathOrMethod(info.FullMethod) && settings.HealthNoAuth {
		return handler(ContextWithIdentity(ctx, nil), req)
	}
	if !settings.AuthEnabled {
		return handler(ContextWithIdentity(ctx, pkgauth.AnonymousIdentity), req)
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing metadata")
	}

	token := extractGRPCToken(md)
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

	return handler(ContextWithIdentity(ctx, identity), req)
}
