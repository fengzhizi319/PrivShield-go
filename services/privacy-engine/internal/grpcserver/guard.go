// Package grpcserver 提供 gRPC 监听器的网络层准入与身份级限流拦截器。
//
// 【为什么要单独一个文件】REST 侧漏斗早已具备「① IP CIDR 白名单」与「⑪ 身份级令牌桶」
// 两道防护，但 gRPC 监听端口只挂了鉴权拦截器：同一份 `AGENT_ALLOWED_CIDRS` /
// `AGENT_RATE_LIMIT_*` 配置实际上只约束了一个端口，收紧 REST 后 gRPC 仍是开放面与
// 洪泛放大器（双路径不对称）。本文件把这两道防护补齐到 gRPC 侧，配置来源与 REST 完全共用。
package grpcserver

import (
	"context"
	"strings"

	"google.golang.org/grpc"

	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/security"
	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
	"github.com/fengzhizi319/PrivShield-go/pkg/middleware"
)

// grpcGuardInterceptors 组装 gRPC 侧完整拦截器链：IP 准入 → 鉴权 → 身份级限流。
// 未启用的防护自动跳过，因此返回切片可能只含鉴权一项。
//
// IP 准入置于鉴权之前，既省去对非法来源做无谓的常量时间密钥比对（避免成为免费的身份
// 验证 oracle），也让非法来源立即被拒；限流置于鉴权之后，才能拿到身份做分片键。
func grpcGuardInterceptors(auth grpc.UnaryServerInterceptor) (unary []grpc.UnaryServerInterceptor, stream []grpc.StreamServerInterceptor) {
	settings := security.GetSettings()

	networks := middleware.ParseAllowedNetworks(settings.AllowedCIDRs)
	if u := middleware.UnaryIPAllowlist(networks); u != nil {
		unary = append(unary, u)
	}
	if s := middleware.StreamIPAllowlist(networks); s != nil {
		stream = append(stream, s)
	}

	if auth != nil {
		unary = append(unary, auth)
	}

	if settings.RateLimitEnabled {
		rps := settings.RateLimitDefaultRPS
		burst := float64(settings.RateLimitDefaultBurst)
		limiter := middleware.NewIPRateLimiter(int(rps), int(burst))
		keyFunc := grpcRateLimitKeyFunc(settings)
		if u := middleware.UnaryKeyedRateLimit(limiter, rps, burst, keyFunc); u != nil {
			unary = append(unary, u)
		}
		if s := middleware.StreamKeyedRateLimit(limiter, rps, burst, keyFunc); s != nil {
			stream = append(stream, s)
		}
	}
	return unary, stream
}

// grpcRateLimitKeyFunc 返回与 REST 侧同构的分片键函数：
// 「身份类型:身份名:RPC 短方法名」，匿名调用者再追加对端 IP 作为分片因子，
// 防止单个未认证来源把某个身份键的桶打满（与 REST 的 IP 维度补偿一致）。
func grpcRateLimitKeyFunc(settings *security.Settings) middleware.GRPCRateLimitKeyFunc {
	return func(ctx context.Context, fullMethod string) string {
		if settings.HealthNoRateLimit && pkgauth.IsHealthPathOrMethod(fullMethod) {
			return "" // 空串表示豁免
		}
		identity := IdentityFromContext(ctx)
		if identity == nil {
			identity = &pkgauth.Identity{ServiceType: "external", Name: "anonymous"}
		}
		key := identity.ServiceType + ":" + identity.Name + ":" + shortMethodName(fullMethod)
		if identity.Name == "anonymous" {
			if peerIP := middleware.GRPCPeerIP(ctx); peerIP != "" {
				key += ":" + peerIP
			}
		}
		return key
	}
}

// shortMethodName 把 "/privacy.local.PrivacyService/Mask" 收敛为 "Mask"，
// 保证限流键基数有界（RPC 名集合固定，不会像动态路径那样造成桶爆炸）。
func shortMethodName(fullMethod string) string {
	if i := strings.LastIndex(fullMethod, "/"); i >= 0 && i+1 < len(fullMethod) {
		return fullMethod[i+1:]
	}
	return fullMethod
}
