// Package main 提供 PrivShield Go 核心隐私与动态分类分级引擎（Core Agent / Sidecar）的服务端入口。
// 本文件实现高性能 gRPC 服务模块的构建、Keepalive 保活、mTLS 凭证加载、动态 CN 白名单拦截与看门狗优雅停机。
//
// gRPC 与 REST 的职责和启动流程对比：
//
//	| 对比项       | REST                                      | gRPC                                      |
//	|--------------|-------------------------------------------|-------------------------------------------|
//	| 服务构建     | 创建 Gin 引擎                              | 创建 grpc.Server                          |
//	| 中间件/安全  | router.Use(...) 注册 HTTP 中间件           | Unary/Stream Interceptor 注册拦截器        |
//	| 接口注册     | RegisterRoutes(...) 逐条注册方法和 URL      | 注册一次 PrivacyService，RPC 自动分发       |
//	| 数据协议     | HTTP/JSON                                  | HTTP/2 + protobuf                         |
//	| 默认监听     | 127.0.0.1:8079                             | 127.0.0.1:50051                            |
//	| 启动调用     | Start() → Serve/ServeTLS                   | Start() → Serve                            |
//	| 请求入口     | URL 路由匹配后执行 Handler                  | RPC 方法匹配后执行服务方法                  |
//
// 两者都在初始化阶段完成配置、TCP Listener、安全组件和指标装配，但此时尚未处理业务请求；
// 只有 Start() 调用 Serve、ServeTLS 或 Serve 后，才开始接收连接。两个监听器、连接池、
// 超时、认证拦截器和优雅停机流程彼此独立。
//
// gRPC 启动与请求处理的完整步骤：
//  1. 主程序读取配置并调用 newGRPCServerRunner，先由 cfg.GRPCAddress() 计算 Host:Port，
//     再通过 net.Listen("tcp", ...) 绑定端口；绑定失败立即返回，避免启动后才发现端口冲突。
//  2. 构造 grpcOpts：先加入 Keepalive 参数，再按配置追加 TLS 凭证和 mTLS CN 白名单
//     Unary/Stream 拦截器；安全组件初始化失败时关闭已创建的 Listener 并拒绝启动。
//  3. 调用 grpcserver.NewServer(svc, grpcOpts...) 创建 gRPC 服务对象，并注入 Prometheus
//     指标；此时只完成服务装配，尚未接收任何客户端连接或执行 RPC。
//  4. 主程序调用 Start()，底层 g.server.Serve(g.listener) 开始阻塞监听；gRPC 接受 TCP
//     连接并完成 HTTP/2、TLS/mTLS 握手后，依据 protobuf 的 ServiceDesc 找到对应 RPC。
//  5. 每次 RPC 先经过认证、CN 白名单和指标等拦截器，再调用服务方法，服务方法通过 svc
//     执行业务逻辑并返回 protobuf 响应；Unary 与 Stream 分别沿对应拦截器链处理。
//  6. 收到停机信号后调用 Shutdown()：先 GracefulStop() 拒绝新 RPC 并等待在途请求完成；
//     超过看门狗时间仍未结束时调用 Stop() 强制断开连接，避免进程无限等待。
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
	// 仅当配置了白名单文件时启用 CN 校验；空路径表示不挂载该拦截器。
	whitelistPath := cfg.MTLSWhitelistFile
	if whitelistPath != "" {
		// 创建 Unary 和 Stream 两类拦截器。它们分别覆盖普通 RPC 和流式 RPC，
		// 并在每次调用进入业务方法前读取客户端证书的 Common Name 进行校验。
		unaryInter, streamInter, _, err := tlsutil.NewWhitelistInterceptor(whitelistPath)
		if err != nil {
			_ = grpcLis.Close()
			return nil, fmt.Errorf("failed to init mTLS whitelist interceptor: %w", err)
		}
		// 白名单已声明但拦截器未成功创建时必须拒绝启动，避免服务在无客户端身份校验的
		// 不安全状态下继续对外提供 gRPC。
		if unaryInter == nil || streamInter == nil {
			_ = grpcLis.Close()
			return nil, fmt.Errorf("mTLS whitelist interceptor was not registered; refusing to serve gRPC without CN authorization (path=%s)", whitelistPath)
		}
		// Chain*Interceptor 将 CN 校验加入 gRPC ServerOption；真正创建 gRPC Server
		// 时传入 grpcserver.NewServer，启动后的每个 Unary/Stream RPC 都会经过此校验。
		grpcOpts = append(grpcOpts,
			grpc.ChainUnaryInterceptor(unaryInter),
			grpc.ChainStreamInterceptor(streamInter),
		)
		slog.Info("mTLS CN whitelist interceptor enabled", "path", whitelistPath)
	}

	// 构造底层 gRPC 隐私服务：传入 TLS、Keepalive、mTLS 白名单等 ServerOption，
	// 再注入 Prometheus 指标拦截器。此处只完成对象和配置装配，尚未开始接收 RPC。
	grpcSrv := grpcserver.NewServer(svc, grpcOpts...).WithMetrics(engineMetrics)

	// 返回运行实体；Start() 随后使用同一个 Listener 调用 Serve()，正式进入阻塞监听。
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
//  2. 若超时（默认 AGENT_GRPC_GRACEFUL_STOP_SECONDS 15s）仍有悬挂连接未退，
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
