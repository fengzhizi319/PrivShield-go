// Package main 提供 PrivShield Go 核心隐私与动态分类分级引擎（Core Agent / Sidecar）的服务端入口。
// 本文件实现高性能 gRPC 服务模块的构建、Keepalive 保活、mTLS 凭证加载、动态 CN 白名单拦截与看门狗优雅停机。
package main

import (
	"fmt"
	"log/slog"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/grpcserver"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/observability"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/service"
	"github.com/fengzhizi319/PrivShield-go/pkg/tlsutil"
)

// ─────────────────────────────────────────────────────────────────────────────
// gRPC 服务运行实体与生命周期管理
// ─────────────────────────────────────────────────────────────────────────────

// GRPCServerRunner 封装高性能 gRPC 服务的完整生命周期管理。
// 负责：
//  1. 底层 TCP 监听套接字的绑定与端口预检；
//  2. 生产级 Keepalive 长连接保活策略（空闲回收、周期重连防负载倾斜、防 Ping 风暴）；
//  3. TLS/mTLS 凭证构建与基于证书链的双向身份认证；
//  4. 挂载 5 秒无依赖动态热重载的客户端证书 CN 白名单拦截器（Unary + Stream）；
//  5. 挂载 Prometheus RED 指标与 64MB 防 OOM 消息体限制；
//  6. 带独立协程与超时看门狗降级的确定性优雅停机。
type GRPCServerRunner struct {
	server   *grpcserver.Server
	listener net.Listener
	addr     string
	cfg      Config
}

// newGRPCServerRunner 构造 gRPC 服务运行实体并完成端口监听与安全拦截器装配。
// 若 TCP 端口冲突或 TLS/白名单安全配置有误，直接快速失败并返回显式错误。
func newGRPCServerRunner(
	cfg Config,
	svc *service.PrivacyService,
	engineMetrics *observability.EngineMetrics,
) (*GRPCServerRunner, error) {
	grpcAddr := cfg.GRPCAddress()
	grpcLis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return nil, fmt.Errorf("gRPC listen failed on %s: %w", grpcAddr, err)
	}

	// 生产级 Keepalive 保活策略装配：
	//  - MaxConnectionIdle (5m)：回收长期闲置连接，防连接资源泄露；
	//  - MaxConnectionAge (2h)：定期重连促使 L4 负载均衡器重新均衡长连接；
	//  - Keepalive Ping (2m/20s)：快速发现半开死连接；
	//  - EnforcementPolicy (MinTime 5s, PermitWithoutStream true)：防御恶意客户端 Ping 风暴。
	var grpcOpts = []grpc.ServerOption{
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: 5 * time.Minute,
			MaxConnectionAge:  2 * time.Hour,
			Time:              2 * time.Minute,
			Timeout:           20 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	}

	// TLS / mTLS 双向认证证书加载与校验
	if cfg.TLSEnabled {
		clientAuth := ""
		if cfg.MTLSEnabled {
			clientAuth = "require"
		}
		tlsCfg, err := tlsutil.BuildServerTLSConfig(&tlsutil.ServerTLSConfig{
			Enabled:    true,
			CertFile:   cfg.TLSCertFile,
			KeyFile:    cfg.TLSKeyFile,
			CAFile:     cfg.TLSCAFile,
			ClientAuth: clientAuth,
		})
		if err != nil {
			_ = grpcLis.Close()
			return nil, fmt.Errorf("failed to build gRPC TLS credentials: %w", err)
		}
		grpcOpts = append(grpcOpts, grpc.Creds(credentials.NewTLS(tlsCfg)))
		slog.Info("gRPC TLS credentials enabled", "mtls", cfg.MTLSEnabled)
	}

	// mTLS CN 白名单动态拦截器挂载：
	//  - 严格 fail-closed：白名单文件配置但初始化拦截器失败时拒绝启动；
	//  - 覆盖全部 Unary 与 Stream 调用，校验客户端证书 Common Name；
	//  - 内置 5 秒文件 mtime 检测热重载，修改白名单无需重启服务进程。
	whitelistPath := cfg.MTLSWhitelistFile
	if whitelistPath != "" {
		unaryInter, streamInter, _, err := tlsutil.NewWhitelistInterceptor(whitelistPath)
		if err != nil {
			_ = grpcLis.Close()
			return nil, fmt.Errorf("failed to init mTLS whitelist interceptor: %w", err)
		}
		if unaryInter == nil || streamInter == nil {
			_ = grpcLis.Close()
			return nil, fmt.Errorf("mTLS whitelist interceptor was not registered; refusing to serve gRPC without CN authorization (path=%s)", whitelistPath)
		}
		grpcOpts = append(grpcOpts,
			grpc.ChainUnaryInterceptor(unaryInter),
			grpc.ChainStreamInterceptor(streamInter),
		)
		slog.Info("mTLS CN whitelist interceptor enabled", "path", whitelistPath)
	}

	// 构造底层 gRPC 隐私服务（内置 64MB 报文限制、鉴权与 Prometheus 监控）
	grpcSrv := grpcserver.NewServer(svc, grpcOpts...).WithMetrics(engineMetrics)

	return &GRPCServerRunner{
		server:   grpcSrv,
		listener: grpcLis,
		addr:     grpcLis.Addr().String(),
		cfg:      cfg,
	}, nil
}

// Start 启动 gRPC 服务监听（阻塞直至服务终止或被 Stop/GracefulStop 关闭）。
func (g *GRPCServerRunner) Start() error {
	slog.Info("gRPC server starting", "addr", g.addr)
	if err := g.server.Serve(g.listener); err != nil {
		return fmt.Errorf("gRPC server terminated with error: %w", err)
	}
	return nil
}

// Shutdown 采用带看门狗超时的两阶段确定性优雅停机：
//  1. 启动独立协程调用 GracefulStop()，拒绝新 RPC 并等待当前在途 RPC 处理完毕；
//  2. 若超时（默认 PRIVACY_GRPC_GRACEFUL_STOP_SECONDS 15s）仍有悬挂连接未退，
//     强制降级调用 Stop() 斩断连接，杜绝僵死等待阻塞 Pod 销毁。
func (g *GRPCServerRunner) Shutdown(timeout time.Duration) error {
	slog.Info("Shutting down gRPC server...", "addr", g.addr, "watchdog_timeout", timeout)
	done := make(chan struct{})
	go func() {
		g.server.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
		slog.Info("gRPC server stopped gracefully")
		return nil
	case <-time.After(timeout):
		slog.Warn("gRPC graceful stop timed out, forcing stop", "timeout", timeout)
		g.server.Stop()
		return fmt.Errorf("gRPC graceful stop timed out after %v, force stopped", timeout)
	}
}

// Address 返回 gRPC 服务的实际监听网络地址（Host:Port）。
func (g *GRPCServerRunner) Address() string {
	return g.addr
}
