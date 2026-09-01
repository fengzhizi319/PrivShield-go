// Package grpcserver 测试套件
//
// ==============================================================================
// 【测试套件设计目标与覆盖范围】
// 本测试文件验证 gRPC 服务器构建器（Builder 模式）的生命周期管理：
//  1. 【链式配置验证】：验证 New() 构建器支持 WithOptions、WithUnaryInterceptor 等链式调用，
//     正确组合 gRPC ServerOption 并返回非 nil 服务器实例；
//  2. 【ServeListener + Stop 生命周期】：验证服务器能正常监听 TCP 端口、注册 gRPC 服务（Health），
//     调用 Stop() 后 ServeListener 正常返回、无错误泄露；
//  3. 【GracefulStop 安全性】：验证在 Serve 之前调用 GracefulStop 不会 panic，保证安全停机。
// ==============================================================================

package grpcserver

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// ──────────────────────────────────────────────
// 1. 构建器链式配置测试
// ──────────────────────────────────────────────

// TestNewServer_WithOptions 验证 gRPC 服务器构建器的 Builder 模式链式配置。
// 执行逻辑：调用 New() 创建构建器，链式设置 MaxConcurrentStreams 选项和自定义 UnaryInterceptor，
// 断言返回的服务器实例非 nil。
func TestNewServer_WithOptions(t *testing.T) {
	s := New("127.0.0.1:0").
		WithOptions(grpc.MaxConcurrentStreams(10)).
		WithUnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			return handler(ctx, req)
		})
	if s == nil {
		t.Fatal("New returned nil")
	}
}

// ──────────────────────────────────────────────
// 2. 服务启动与停止生命周期测试
// ──────────────────────────────────────────────

// TestServer_ServeAndStop 验证 gRPC 服务器的完整启动-停止生命周期。
// 执行逻辑：创建服务器 → 监听随机端口 → 注册 Health 服务 → 启动 ServeListener 协程 →
// 等待 50ms 后调用 Stop() → 断言 ServeListener 在 2s 内正常返回无错误。
func TestServer_ServeAndStop(t *testing.T) {
	s := New("127.0.0.1:0")

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer lis.Close()

	// Register a health service to verify registration works.
	hs := health.NewServer()
	s.RegisterService(&grpc_health_v1.Health_ServiceDesc, hs)

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- s.ServeListener(lis)
	}()

	time.Sleep(50 * time.Millisecond)
	s.Stop()

	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("ServeListener returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ServeListener did not stop")
	}
}

// ──────────────────────────────────────────────
// 3. 优雅停机安全性测试
// ──────────────────────────────────────────────

// TestServer_GracefulStop 验证在 Serve 之前调用 GracefulStop 不会 panic。
// 执行逻辑：创建服务器后直接调用 GracefulStop()，断言无 panic 发生，
// 保证在未启动状态下优雅停机调用的安全性。
func TestServer_GracefulStop(t *testing.T) {
	s := New("127.0.0.1:0")
	// GracefulStop should be safe before Serve.
	s.GracefulStop()
}
