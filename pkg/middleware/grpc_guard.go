// gRPC 侧对等防护 —— 与 HTTP 中间件漏斗（IPAllowlist / RateLimit）一一对应。
//
// 背景（双路径对称性）：本仓库的服务普遍同时监听 REST 与 gRPC 两个端口。HTTP 侧
// 已有 `IPAllowlist`（网络层 CIDR 准入）与 `KeyedRateLimit`（身份级令牌桶），但 gRPC
// 监听器长期只挂鉴权拦截器，导致「同一份 AGENT_ALLOWED_CIDRS / 限流配置只约束了一个
// 端口」——运维以为已经收紧，实际另一个端口仍是开放面与洪泛放大器。
//
// 本文件把这两种防护下沉为与 Gin 无关的 gRPC 拦截器，配置来源与 HTTP 侧完全共用，
// 使「一次配置、两个端口同时生效」成为默认，避免各服务再各自复制一份不完整的实现。
package middleware

import (
	"context"
	"log/slog"
	"net"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
)

// ParseAllowedNetworks 将 CIDR（或单 IP，自动补 /32、/128）列表编译为网段切片。
// 非法条目跳过并打 WARN，与 HTTP 侧 IPAllowlist 保持完全相同的宽松度与日志口径。
func ParseAllowedNetworks(allowedCIDRs []string) []*net.IPNet {
	if len(allowedCIDRs) == 0 {
		return nil
	}
	networks := make([]*net.IPNet, 0, len(allowedCIDRs))
	for _, cidr := range allowedCIDRs {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}
		if !strings.Contains(cidr, "/") {
			ip := net.ParseIP(cidr)
			if ip == nil {
				slog.Warn("IPAllowlist: invalid IP/CIDR skipped", "entry", cidr)
				continue
			}
			if ip.To4() != nil {
				cidr = cidr + "/32"
			} else {
				cidr = cidr + "/128"
			}
		}
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			slog.Warn("IPAllowlist: invalid CIDR skipped", "entry", cidr, "error", err.Error())
			continue
		}
		networks = append(networks, network)
	}
	return networks
}

// IPAllowed 判断 ip 是否命中任一网段；networks 为空表示未启用白名单（放行）。
func IPAllowed(networks []*net.IPNet, ip string) bool {
	if len(networks) == 0 {
		return true
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, network := range networks {
		if network.Contains(parsed) {
			return true
		}
	}
	return false
}

// GRPCPeerIP 从 gRPC context 中提取对端 IP（已剥离端口、去掉 IPv6 方括号）。
// 无法获取对端信息时返回空串（调用方须按 fail-closed 处理）。
func GRPCPeerIP(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p == nil || p.Addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(p.Addr.String())
	if err != nil {
		host = p.Addr.String()
	}
	return strings.Trim(strings.TrimSpace(host), "[]")
}

// UnaryIPAllowlist 返回 gRPC Unary 拦截器，按已编译网段做网络层准入。
// networks 为空时返回 nil（调用方据此跳过挂载，与 HTTP 侧「未配置即透传」一致）。
func UnaryIPAllowlist(networks []*net.IPNet) grpc.UnaryServerInterceptor {
	if len(networks) == 0 {
		return nil
	}
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		clientIP := GRPCPeerIP(ctx)
		if !IPAllowed(networks, clientIP) {
			slog.Warn("gRPC IPAllowlist: rejected peer outside allowed CIDRs",
				"peer_ip", clientIP, "method", info.FullMethod)
			return nil, status.Error(codes.PermissionDenied, "client IP not in allowed CIDR ranges")
		}
		return handler(ctx, req)
	}
}

// StreamIPAllowlist 返回 gRPC Stream 拦截器，语义同 UnaryIPAllowlist。
// 当前 PrivacyService 无流式方法，挂载后对未来新增流式 RPC 自动生效。
func StreamIPAllowlist(networks []*net.IPNet) grpc.StreamServerInterceptor {
	if len(networks) == 0 {
		return nil
	}
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		clientIP := GRPCPeerIP(ss.Context())
		if !IPAllowed(networks, clientIP) {
			slog.Warn("gRPC IPAllowlist: rejected peer outside allowed CIDRs",
				"peer_ip", clientIP, "method", info.FullMethod)
			return status.Error(codes.PermissionDenied, "client IP not in allowed CIDR ranges")
		}
		return handler(srv, ss)
	}
}

// GRPCRateLimitKeyFunc 决定限流分片键；返回空串表示该调用豁免限流（如健康探针）。
type GRPCRateLimitKeyFunc func(ctx context.Context, fullMethod string) string

// UnaryKeyedRateLimit 返回按 key 分片的 gRPC Unary 令牌桶拦截器。
// limiter 为 nil 或 rps<=0 时返回 nil（调用方跳过挂载）。
//
// 注意：本拦截器只做「按 key 削峰」，不解释身份语义，因此必须挂在鉴权拦截器之后，
// 由鉴权把身份写入 ctx，再经 keyFunc 读取；未认证的匿名流量应由 keyFunc 追加对端 IP。
func UnaryKeyedRateLimit(limiter *IPRateLimiter, rps, burst float64, keyFunc GRPCRateLimitKeyFunc) grpc.UnaryServerInterceptor {
	if limiter == nil || rps <= 0 || keyFunc == nil {
		return nil
	}
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		key := keyFunc(ctx, info.FullMethod)
		if key == "" {
			return handler(ctx, req)
		}
		if !limiter.AllowWithParams(key, rps, burst) {
			return nil, status.Error(codes.ResourceExhausted, "rate limit exceeded")
		}
		return handler(ctx, req)
	}
}

// StreamKeyedRateLimit 返回 gRPC Stream 建流速率限流拦截器（按流粒度计数）。
func StreamKeyedRateLimit(limiter *IPRateLimiter, rps, burst float64, keyFunc GRPCRateLimitKeyFunc) grpc.StreamServerInterceptor {
	if limiter == nil || rps <= 0 || keyFunc == nil {
		return nil
	}
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		key := keyFunc(ss.Context(), info.FullMethod)
		if key != "" && !limiter.AllowWithParams(key, rps, burst) {
			return status.Error(codes.ResourceExhausted, "rate limit exceeded")
		}
		return handler(srv, ss)
	}
}

// ExtractGRPCTraceID 从入站 gRPC metadata 中提取追踪 ID（x-request-id、x-trace-id 或 traceparent）。
// 若均未提供则自动生成高精度加密随机 TraceID。
func ExtractGRPCTraceID(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		for _, key := range []string{"x-request-id", "x-trace-id", "traceparent"} {
			if vals := md.Get(key); len(vals) > 0 && strings.TrimSpace(vals[0]) != "" {
				return strings.TrimSpace(vals[0])
			}
		}
	}
	return pkgobs.GenerateRequestID()
}

type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedServerStream) Context() context.Context {
	return w.ctx
}

// UnaryTraceInterceptor 提取或生成分布式追踪上下文（x-request-id / x-trace-id），
// 注入到 ctx 并在响应头部对齐透传，实现端到端链路追踪。
func UnaryTraceInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		traceID := ExtractGRPCTraceID(ctx)
		ctx = pkgobs.ContextWithRequestID(ctx, traceID)
		_ = grpc.SetHeader(ctx, metadata.Pairs("x-request-id", traceID, "x-trace-id", traceID))
		return handler(ctx, req)
	}
}

// StreamTraceInterceptor 为流式 RPC 提取或生成分布式追踪上下文并透传。
func StreamTraceInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()
		traceID := ExtractGRPCTraceID(ctx)
		ctx = pkgobs.ContextWithRequestID(ctx, traceID)
		_ = ss.SetHeader(metadata.Pairs("x-request-id", traceID, "x-trace-id", traceID))
		return handler(srv, &wrappedServerStream{ServerStream: ss, ctx: ctx})
	}
}
