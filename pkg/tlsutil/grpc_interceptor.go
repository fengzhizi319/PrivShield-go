// Package tlsutil — gRPC server interceptor for mTLS CN whitelist authorization.
// Package tlsutil — 基于 mTLS 客户端证书 Common Name 白名单的 gRPC 服务端统一安全鉴权拦截器。
//
// ==============================================================================
// 【核心能力与零信任鉴权流程】
// 1. 【Peer 凭证解析 (extractClientCN)】：
//    从 gRPC 请求上下文 Context 的 peer.Peer.AuthInfo 中提取 credentials.TLSInfo，
//    深入 VerifiedChains[0][0] 获取经过 CA 验证的客户端证书 Subject.CommonName；
// 2. 【身份与权限双重校验 (authorizeClient)】：
//    - 身份存在性：检查客户端 CN 是否存在于 DynamicWhitelist 中（若不存在返回 codes.PermissionDenied）；
//    - 方法级 Scope 匹配：比对该 CN 被允许的 scope 列表是否覆盖当前的 info.FullMethod；
// 3. 【一元与流式全覆盖】：
//    同时提供 UnaryServerInterceptor 与 StreamServerInterceptor，保障所有 RPC 类型的一致安全；
// 4. 【快速装配工厂 (NewWhitelistInterceptor)】：
//    提供一键初始化函数，加载 YAML 白名单、启动热重载轮询并返回装配好的拦截器三元组。
// ==============================================================================

package tlsutil

import (
	"context"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// extractClientIdentities 从当前 RPC 上下文中提取客户端 mTLS 证书的所有合法身份凭证：
// 优先提取 SAN URIs (如 SPIFFE ID: spiffe://...) 与 SAN DNSNames，并回退包含 Subject.CommonName。
func extractClientIdentities(ctx context.Context) ([]string, error) {
	p, ok := peer.FromContext(ctx)
	if !ok || p.AuthInfo == nil {
		return nil, status.Error(codes.Unauthenticated, "missing peer authentication info")
	}
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(tlsInfo.State.VerifiedChains) == 0 || len(tlsInfo.State.VerifiedChains[0]) == 0 {
		return nil, status.Error(codes.Unauthenticated, "invalid or unverified client certificate")
	}
	cert := tlsInfo.State.VerifiedChains[0][0]

	var identities []string
	// 1. SAN URIs (例如 SPIFFE ID: spiffe://cluster.local/ns/default/sa/service-hub)
	for _, u := range cert.URIs {
		if u != nil && u.String() != "" {
			identities = append(identities, u.String())
		}
	}
	// 2. SAN DNSNames
	for _, dns := range cert.DNSNames {
		if dns != "" {
			identities = append(identities, dns)
		}
	}
	// 3. Subject CommonName (CN)
	if cert.Subject.CommonName != "" {
		identities = append(identities, cert.Subject.CommonName)
	}

	if len(identities) == 0 {
		return nil, status.Error(codes.Unauthenticated, "client certificate has no CN or SAN identity")
	}
	return identities, nil
}

// extractClientCN 提取客户端的主标识（兼容保留）。
func extractClientCN(ctx context.Context) (string, error) {
	ids, err := extractClientIdentities(ctx)
	if err != nil {
		return "", err
	}
	// 优先返回首个身份凭据
	return ids[0], nil
}

// authorizeClientIdentities 在白名单中校验客户端证书的任意身份凭证（SAN URI / DNS / CN）是否被授权调用 fullMethod。
func (dw *DynamicWhitelist) authorizeClientIdentities(identities []string, fullMethod string) error {
	dw.mu.RLock()
	defer dw.mu.RUnlock()

	var matchedIdentity string
	var allowedScopes []string
	found := false

	for _, id := range identities {
		if scopes, exists := dw.clients[id]; exists {
			matchedIdentity = id
			allowedScopes = scopes
			found = true
			break
		}
	}

	if !found {
		slog.Warn("mTLS Auth: unauthorized client identities", "identities", identities, "method", fullMethod)
		return status.Errorf(codes.PermissionDenied, "client identities '%v' are not authorized", identities)
	}

	// 范围匹配：通配符 "*"、精确匹配或模式匹配
	for _, s := range allowedScopes {
		if s == "*" || s == fullMethod || matchScopePattern(s, fullMethod) {
			return nil
		}
	}
	slog.Warn("mTLS Auth: identity lacks scope for method", "identity", matchedIdentity, "method", fullMethod, "allowed_scopes", allowedScopes)
	return status.Errorf(codes.PermissionDenied, "client '%s' lacks scope for method '%s'", matchedIdentity, fullMethod)
}

// authorizeClient checks CN existence in whitelist and method scope matching.
func (dw *DynamicWhitelist) authorizeClient(clientCN, fullMethod string) error {
	return dw.authorizeClientIdentities([]string{clientCN}, fullMethod)
}

// UnaryServerInterceptor returns a gRPC unary server interceptor that enforces
// mTLS CN & SAN whitelist authorization on every incoming RPC call.
//
// UnaryServerInterceptor 返回一元 RPC 鉴权拦截器：
// 1. 提取对端客户端证书 SAN/CN 身份；
// 2. 校验白名单与 info.FullMethod 方法权限；
// 3. 鉴权通过后调用 handler(ctx, req) 继续执行业务逻辑。
func (dw *DynamicWhitelist) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		identities, err := extractClientIdentities(ctx)
		if err != nil {
			return nil, err
		}
		if err := dw.authorizeClientIdentities(identities, info.FullMethod); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamServerInterceptor returns a gRPC stream server interceptor that enforces
// mTLS CN & SAN whitelist authorization on every incoming streaming RPC call.
//
// StreamServerInterceptor 返回流式 RPC 鉴权拦截器：
// 1. 从流上下文 ss.Context() 提取对端客户端证书 SAN/CN 身份；
// 2. 校验白名单与 info.FullMethod 方法权限；
// 3. 鉴权通过后调用 handler(srv, ss) 启动流处理。
func (dw *DynamicWhitelist) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		identities, err := extractClientIdentities(ss.Context())
		if err != nil {
			return err
		}
		if err := dw.authorizeClientIdentities(identities, info.FullMethod); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

// NewWhitelistInterceptor loads a DynamicWhitelist from path and returns both
// unary and stream server interceptors. If path is empty, it returns nil
// interceptors and a nil DynamicWhitelist with no error.
//
// NewWhitelistInterceptor 快捷构造工厂：
// - 若 path 为空，返回 (nil, nil, nil, nil) 表示禁用 CN 白名单鉴权；
// - 若 path 非空，自动加载并启动后台热重载，返回 Unary/Stream 拦截器与 DynamicWhitelist 句柄。
func NewWhitelistInterceptor(path string) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor, *DynamicWhitelist, error) {
	if path == "" {
		return nil, nil, nil, nil
	}
	dw, err := NewDynamicWhitelist(path)
	if err != nil {
		return nil, nil, nil, err
	}
	return dw.UnaryServerInterceptor(), dw.StreamServerInterceptor(), dw, nil
}
