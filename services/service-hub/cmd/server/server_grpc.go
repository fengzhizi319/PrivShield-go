// Command server is the entry point for the service-hub module.
// 本文件实现 service-hub gRPC 服务模块的构建、Keepalive 保活、mTLS 凭证加载、
// API Key + Scope 鉴权与 CN 白名单拦截器装配、以及带看门狗超时的优雅停机。
package main

import (
	"fmt"
	"log/slog"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	"github.com/fengzhizi319/PrivShield-go/pkg/store"
	"github.com/fengzhizi319/PrivShield-go/pkg/tlsutil"

	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/agent"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/config"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/datasource"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/grpcserver"
	pb "github.com/fengzhizi319/PrivShield-go/services/service-hub/proto"
)

// ─────────────────────────────────────────────────────────────────────────────
// gRPC 服务运行实体与生命周期管理
// ─────────────────────────────────────────────────────────────────────────────

// whitelistCloser stops the mTLS CN whitelist dynamic hot-reload watcher (may be nil).
type whitelistCloser interface {
	Close()
}

// GRPCServerRunner 封装 service-hub gRPC 服务的完整生命周期管理。
// 负责：
//  1. gRPC Server 装配：64 MiB 消息体上限、Keepalive 保活策略、API Key + Scope 鉴权拦截器
//     与可选 mTLS CN 白名单拦截器链（ChainUnary/ChainStream）；
//  2. TLS/mTLS 凭证构建与 ServiceHubService 服务桩注册；
//  3. 底层 TCP 监听套接字绑定与端口预检（绑定失败构造期快速失败）；
//  4. 带 30s 看门狗超时回退 Stop() 的确定性优雅停机。
type GRPCServerRunner struct {
	server          *grpc.Server
	serviceImpl     *grpcserver.GRPCServer
	listener        net.Listener
	addr            string
	logger          *slog.Logger
	whitelistCloser whitelistCloser // mTLS CN 白名单动态热重载监听器（可能为 nil）
}

// newGRPCServerRunner 构造 gRPC 服务运行实体并完成拦截器、凭证、服务桩与监听装配。
// 若 mTLS 白名单加载、TLS 凭证构建或 TCP 监听失败，直接返回错误，由 main 快速失败终止进程。
func newGRPCServerRunner(
	ag *agent.Client,
	ds *datasource.Client,
	cfg *config.Config,
	taskStore store.TaskStore,
	logger *slog.Logger,
) (*GRPCServerRunner, error) {
	// Production hardening: message size limits & keepalive (aligned with Python Agent gRPC server).
	// 生产加固：消息大小限制与 keepalive（与 Python Agent gRPC 服务端对齐）。
	const maxMsgSize = 64 * 1024 * 1024 // 64 MiB
	grpcServerOpts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(maxMsgSize),
		grpc.MaxSendMsgSize(maxMsgSize),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     15 * time.Minute,
			MaxConnectionAge:      2 * time.Hour,
			MaxConnectionAgeGrace: 5 * time.Second,
			Time:                  30 * time.Second,
			Timeout:               10 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	}

	// 三级等保/密评 G-17：挂载 gRPC 应用层 API Key + Scope 鉴权拦截器，
	// 并与 mTLS CN 白名单拦截器链式组合（避免单独使用 grpc.UnaryInterceptor 相互覆盖）。
	unaryInterceptors := []grpc.UnaryServerInterceptor{grpcserver.AuthUnaryInterceptor()}
	streamInterceptors := []grpc.StreamServerInterceptor{grpcserver.AuthStreamInterceptor()}
	var whitelistCloser whitelistCloser
	if cfg.MTLSWhitelistFile != "" {
		unaryInterceptor, streamInterceptor, dw, err := tlsutil.NewWhitelistInterceptor(cfg.MTLSWhitelistFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load mTLS whitelist: %w", err)
		}
		whitelistCloser = dw
		unaryInterceptors = append(unaryInterceptors, unaryInterceptor)
		streamInterceptors = append(streamInterceptors, streamInterceptor)
		logger.Info("gRPC server configured with mTLS CN whitelist",
			"path", cfg.MTLSWhitelistFile,
		)
	}
	grpcServerOpts = append(grpcServerOpts, grpc.ChainUnaryInterceptor(unaryInterceptors...))
	grpcServerOpts = append(grpcServerOpts, grpc.ChainStreamInterceptor(streamInterceptors...))

	// 根据配置判断是否启用 mTLS 双向认证：
	// - 启用 TLS: 加载服务端证书/私钥，挂载 CA 证书校验客户端身份，注册服务桩并开启 TLS 1.3 强加密；
	// - 未启用 TLS: 启动标准明文 gRPC Server 实例，适用于本地开发或 Service Mesh 代理。
	var grpcServer *grpc.Server
	serviceImpl := grpcserver.New(ag, ds, cfg, taskStore, logger)

	if cfg.TLSEnabled {
		creds, credErr := grpcserver.BuildServerCredentials(cfg)
		if credErr != nil {
			if whitelistCloser != nil {
				whitelistCloser.Close()
			}
			return nil, fmt.Errorf("failed to build TLS credentials: %w", credErr)
		}
		grpcServer = grpc.NewServer(append(grpcServerOpts, grpc.Creds(creds))...)
		pb.RegisterServiceHubServiceServer(grpcServer, serviceImpl)
		logger.Info("gRPC server started with mTLS",
			"addr", cfg.GRPCAddress(),
			"tls_cert", cfg.TLSCertFile,
			"tls_key", cfg.TLSKeyFile,
		)
	} else {
		grpcServer = grpc.NewServer(grpcServerOpts...)
		pb.RegisterServiceHubServiceServer(grpcServer, serviceImpl)
		logger.Info("gRPC server started (insecure)", "addr", cfg.GRPCAddress())
	}

	// 启动 gRPC TCP 监听端口（默认 :50052）；绑定失败构造期快速失败，避免启动后才发现端口冲突。
	grpcLis, err := net.Listen("tcp", cfg.GRPCAddress())
	if err != nil {
		if whitelistCloser != nil {
			whitelistCloser.Close()
		}
		return nil, fmt.Errorf("failed to listen on gRPC address %s: %w", cfg.GRPCAddress(), err)
	}

	return &GRPCServerRunner{
		server:          grpcServer,
		serviceImpl:     serviceImpl,
		listener:        grpcLis,
		addr:            cfg.GRPCAddress(),
		logger:          logger,
		whitelistCloser: whitelistCloser,
	}, nil
}

// ServiceImpl 返回 gRPC 服务桩实现（lease worker 与优雅停机依赖）。
func (g *GRPCServerRunner) ServiceImpl() *grpcserver.GRPCServer {
	return g.serviceImpl
}

// Start 启动 gRPC 服务监听（阻塞直至服务终止或被 GracefulStop/Stop 关闭）。
func (g *GRPCServerRunner) Start() error {
	if err := g.server.Serve(g.listener); err != nil {
		g.logger.Error("gRPC server error", "error", err.Error())
	}
	return nil
}

// Shutdown 采用带看门狗超时的两阶段确定性优雅停机：
//  1. 启动独立协程调用 GracefulStop()，拒绝新 RPC 并等待当前在途 RPC 处理完毕；
//  2. 若 30 秒超时仍有悬挂连接未退，强制降级调用 Stop() 斩断连接，杜绝僵死等待阻塞进程退出。
func (g *GRPCServerRunner) Shutdown() {
	grpcDone := make(chan struct{})
	go func() {
		g.server.GracefulStop()
		close(grpcDone)
	}()
	select {
	case <-grpcDone:
		g.logger.Info("gRPC server stopped")
	case <-time.After(30 * time.Second):
		g.logger.Warn("gRPC GracefulStop timed out after 30s, forcing stop")
		g.server.Stop()
		g.logger.Info("gRPC server force stopped")
	}
	if g.whitelistCloser != nil {
		g.whitelistCloser.Close()
	}
}

// Address 返回 gRPC 服务的监听网络地址（Host:Port）。
func (g *GRPCServerRunner) Address() string {
	return g.addr
}
