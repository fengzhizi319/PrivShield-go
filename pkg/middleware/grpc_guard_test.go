// Package middleware 测试套件 —— gRPC 侧对等防护（IP 准入 / 身份级限流）。
//
// ==============================================================================
// 【覆盖目标】
//  1. ParseAllowedNetworks / IPAllowed：与 HTTP 侧 IPAllowlist 共用的网段编译与命中判定，
//     含单 IP 自动补 /32、/128、非法条目跳过、空列表透传（未启用）；
//  2. UnaryIPAllowlist / StreamIPAllowlist：未配置即返回 nil（调用方跳过挂载），
//     配置后非白名单对端以 PermissionDenied 拒绝，且取不到对端信息时 fail-closed；
//  3. UnaryKeyedRateLimit：空 key 豁免、超限返回 ResourceExhausted、未启用限流器返回 nil；
//  4. GRPCPeerIP：host:port 拆分与 IPv6 方括号剥离。
// ==============================================================================

package middleware

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func ctxWithPeerIP(ip string) context.Context {
	return peer.NewContext(context.Background(), &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP(ip), Port: 50051},
	})
}

func allowHandler(ctx context.Context, _ any) (any, error) { return "ok", nil }

func TestParseAllowedNetworks(t *testing.T) {
	if got := ParseAllowedNetworks(nil); got != nil {
		t.Errorf("empty input must yield nil (allowlist disabled), got %v", got)
	}

	networks := ParseAllowedNetworks([]string{"10.0.0.0/8", "192.168.1.5", "fd00::/64", "  ", "not-an-ip"})
	// 3 条合法 + 1 条非法（跳过）+ 1 条空白（跳过）
	if len(networks) != 3 {
		t.Fatalf("expected 3 valid networks, got %d: %v", len(networks), networks)
	}
	if !IPAllowed(networks, "192.168.1.5") {
		t.Error("bare IPv4 must be normalized to /32 and match itself")
	}
	if IPAllowed(networks, "192.168.1.6") {
		t.Error("adjacent IPv4 must not match a /32 entry")
	}
	if !IPAllowed(networks, "fd00::42") {
		t.Error("IPv6 address inside fd00::/64 must match")
	}
}

func TestIPAllowed(t *testing.T) {
	networks := ParseAllowedNetworks([]string{"10.0.0.0/8"})

	if !IPAllowed(nil, "8.8.8.8") {
		t.Error("empty network list means disabled and must allow everything")
	}
	if !IPAllowed(networks, "10.1.2.3") {
		t.Error("in-range IP must be allowed")
	}
	if IPAllowed(networks, "11.1.2.3") {
		t.Error("out-of-range IP must be denied")
	}
	if IPAllowed(networks, "garbage") {
		t.Error("unparsable client IP must be denied (fail-closed)")
	}
	if IPAllowed(networks, "") {
		t.Error("missing peer IP must be denied (fail-closed)")
	}
}

func TestGRPCPeerIP(t *testing.T) {
	if got := GRPCPeerIP(context.Background()); got != "" {
		t.Errorf("no peer in ctx must yield empty string, got %q", got)
	}
	if got := GRPCPeerIP(ctxWithPeerIP("10.1.2.3")); got != "10.1.2.3" {
		t.Errorf("peer IP = %q, want 10.1.2.3 (port stripped)", got)
	}
	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.UDPAddr{IP: net.ParseIP("fd00::1")}})
	if got := GRPCPeerIP(ctx); got != "fd00::1" {
		t.Errorf("peer IP without port = %q, want fd00::1", got)
	}
}

func TestUnaryIPAllowlist(t *testing.T) {
	if got := UnaryIPAllowlist(nil); got != nil {
		t.Error("no networks must return nil so the caller skips mounting")
	}

	interceptor := UnaryIPAllowlist(ParseAllowedNetworks([]string{"10.0.0.0/8"}))
	info := &grpc.UnaryServerInfo{FullMethod: "/privacy.local.PrivacyService/Mask"}

	if _, err := interceptor(ctxWithPeerIP("10.9.9.9"), nil, info, allowHandler); err != nil {
		t.Errorf("allowed peer must pass, got %v", err)
	}
	if _, err := interceptor(ctxWithPeerIP("8.8.8.8"), nil, info, allowHandler); status.Code(err) != codes.PermissionDenied {
		t.Errorf("blocked peer must get PermissionDenied, got %v", err)
	}
	if _, err := interceptor(context.Background(), nil, info, allowHandler); status.Code(err) != codes.PermissionDenied {
		t.Errorf("unknown peer must be denied (fail-closed), got %v", err)
	}
}

func TestStreamIPAllowlist(t *testing.T) {
	if got := StreamIPAllowlist(nil); got != nil {
		t.Error("no networks must return nil")
	}
	interceptor := StreamIPAllowlist(ParseAllowedNetworks([]string{"10.0.0.0/8"}))
	called := false
	handler := func(_ any, ss grpc.ServerStream) error {
		called = true
		return nil
	}

	if err := interceptor(nil, &stubServerStream{ctx: ctxWithPeerIP("8.8.8.8")},
		&grpc.StreamServerInfo{FullMethod: "/x/Y"}, handler); status.Code(err) != codes.PermissionDenied {
		t.Errorf("blocked peer must get PermissionDenied, got %v", err)
	}
	if err := interceptor(nil, &stubServerStream{ctx: ctxWithPeerIP("10.1.1.1")},
		&grpc.StreamServerInfo{FullMethod: "/x/Y"}, handler); err != nil || !called {
		t.Errorf("allowed peer must reach handler, err=%v called=%v", err, called)
	}
}

func TestUnaryKeyedRateLimit(t *testing.T) {
	if got := UnaryKeyedRateLimit(nil, 10, 20, func(context.Context, string) string { return "k" }); got != nil {
		t.Error("nil limiter must return nil")
	}
	if got := UnaryKeyedRateLimit(NewIPRateLimiter(1, 1), 0, 1, func(context.Context, string) string { return "k" }); got != nil {
		t.Error("rps<=0 must return nil (disabled)")
	}

	limiter := NewIPRateLimiter(1, 2)
	t.Cleanup(limiter.Close)
	interceptor := UnaryKeyedRateLimit(limiter, 1, 2, func(_ context.Context, fullMethod string) string {
		if fullMethod == "/x/Health" {
			return "" // 豁免
		}
		return "internal:svc:Mask"
	})
	info := &grpc.UnaryServerInfo{FullMethod: "/x/Mask"}

	// burst=2：前两次放行，第三次必须被拒。
	for i := 0; i < 2; i++ {
		if _, err := interceptor(context.Background(), nil, info, allowHandler); err != nil {
			t.Fatalf("call %d must be allowed, got %v", i, err)
		}
	}
	if _, err := interceptor(context.Background(), nil, info, allowHandler); status.Code(err) != codes.ResourceExhausted {
		t.Errorf("third call must be ResourceExhausted, got %v", err)
	}

	healthInfo := &grpc.UnaryServerInfo{FullMethod: "/x/Health"}
	for i := 0; i < 10; i++ {
		if _, err := interceptor(context.Background(), nil, healthInfo, allowHandler); err != nil {
			t.Fatalf("exempt method must never be throttled, got %v", err)
		}
	}
}

// stubServerStream 只实现拦截器用到的 Context()，其余方法在测试路径上不会被调用。
type stubServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *stubServerStream) Context() context.Context { return s.ctx }
