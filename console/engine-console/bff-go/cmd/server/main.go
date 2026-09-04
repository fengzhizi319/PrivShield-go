// Command server 是 Go gRPC 代理后端的程序入口。
// Command server is the entry point for the Go gRPC proxy backend.
//
// 执行流程 / Execution flow:
//  1. 从环境变量加载配置（agent 地址、监听端口、API Key 等）
//     Load configuration from env vars (agent address, listen port, API Key, etc.)
//  2. 创建到 PrivShield gRPC 服务的客户端连接
//     Create gRPC client connection to PrivShield service
//  3. 初始化 Gin HTTP 路由，注册所有 REST 代理接口与静态 UI 托管
//     Initialize Gin HTTP routes, register all REST proxy endpoints and static UI hosting
//  4. 启动 HTTP 服务器，监听前端请求
//     Start HTTP server, listen for frontend requests
//  5. 监听系统信号（SIGINT/SIGTERM），收到后执行优雅关闭
//     Listen for system signals (SIGINT/SIGTERM), perform graceful shutdown on receipt
//
// 整体架构 / Overall architecture:
//
//	React 前端  ──HTTP(S)/JSON──▶  BFF-Go (:8081)  ──gRPC (支持 TLS/mTLS/TLCP)──▶  PrivShield Agent (:50051)
//	React frontend  ──HTTP(S)/JSON──▶  BFF-Go (:8081)  ──gRPC (supports TLS/mTLS/TLCP)──▶  PrivShield Agent (:50051)
package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
	"github.com/fengzhizi319/PrivShield-go/pkg/metrics"
	"github.com/fengzhizi319/PrivShield-go/pkg/middleware"
	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
	"github.com/fengzhizi319/PrivShield-go/pkg/tlsutil"

	"github.com/fengzhizi319/PrivShield-go/console/bff-go/internal/agent"
	"github.com/fengzhizi319/PrivShield-go/console/bff-go/internal/config"
	"github.com/fengzhizi319/PrivShield-go/console/bff-go/internal/grpcserver"
	"github.com/fengzhizi319/PrivShield-go/console/bff-go/internal/handlers"
)

// main 是程序入口函数，按以下步骤顺序执行：
// main is the program entry point, executing in the following order:
//
//	加载配置 → 创建 gRPC 客户端 → 初始化 HTTP 路由 → 启动服务器 → 等待关闭信号
//	Load config → Create gRPC client → Init HTTP routes → Start server → Wait for shutdown signal
func main() {
	// ── 步骤 1：加载配置 ──────────────────────────────────────────────
	// 从环境变量读取所有配置项，包括：
	//   - BFF_AGENT_GRPC_HOST / BFF_AGENT_GRPC_PORT：上游 gRPC agent 地址
	//   - BFF_HOST / BFF_PORT：本代理 HTTP 监听地址
	//   - BFF_AGENT_API_KEY：可选的认证 API Key
	//   - BFF_STATIC_DIR：可选的前端静态文件目录
	cfg := config.Load()

	// Validate configuration consistency (fail-fast with clear error messages).
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	// ── 步骤 1.5：结构化日志 + Prometheus 指标 ─────────────────────
	pkgobs.InitLogger(
		pkgconfig.EnvString("CONSOLE_LOG_FORMAT", "json"),
		pkgconfig.EnvString("CONSOLE_LOG_LEVEL", "info"),
	)
	logger := slog.Default()
	mc := metrics.NewCollector("backend-go")

	// ── 步骤 2：创建 gRPC 客户端 ─────────────────────────────────────
	// 根据配置建立到 PrivShield 的 gRPC 连接。
	// 如果配置了 API Key，会自动附加 authorization 元数据。
	// 连接失败时打印错误并立即退出进程（log.Fatalf）。
	client, err := agent.New(cfg)
	if err != nil {
		log.Fatalf("failed to create agent client: %v", err) // 致命错误：无法连接上游 agent
	}
	// 注册 defer：main 函数退出前自动关闭 gRPC 连接，释放底层 TCP 连接与 HTTP/2 流
	defer func() { _ = client.Close() }()

	// 异步预热上游 gRPC 与 REST 连接，彻底消除首次请求的冷启动延迟
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = client.Health(ctx)
	}()

	// ── 步骤 3：初始化 HTTP 路由 ─────────────────────────────────────
	// 将 Gin 设置为发布模式，关闭调试日志输出，提升性能
	gin.SetMode(gin.ReleaseMode)
	// 创建 HTTP 处理器实例，持有 gRPC 客户端引用与配置信息，
	// 内部实现了 /health、/v1/samples、/v1/proxy、/v1/batch 等接口
	server := handlers.New(client, cfg, logger, mc)
	// 创建一个新的 Gin 引擎实例（包含默认的 Logger + Recovery 中间件）
	router := gin.New()
	middleware.ConfigureTrustedProxies(router, middleware.TrustedProxiesFromEnv("CONSOLE_BFF_TRUSTED_PROXIES")) // G-02
	router.Use(middleware.IPAllowlist(middleware.AllowedCIDRsFromEnv("CONSOLE_BFF_ALLOWED_CIDRS")))             // IP access control
	// 将所有 REST 代理路由与可选的静态 UI 托管路由注册到 Gin 引擎
	// 包括 CORS 中间件、健康检查、代理转发、批量测试、静态文件服务等
	server.RegisterRoutes(router)

	// ── 步骤 4：配置 HTTP/HTTPS 服务器 ───────────────────────────────
	srv := &http.Server{
		Addr:              cfg.ConsoleAddress(),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,  // Slowloris header timeout
		ReadTimeout:       30 * time.Second, // Slow request body timeout
		WriteTimeout:      60 * time.Second, // Slow client response timeout
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB max header size
	}

	if cfg.ConsoleTLSEnabled {
		tlsConfig, err := tlsutil.BuildServerTLSConfig(&tlsutil.ServerTLSConfig{
			Enabled:          cfg.ConsoleTLSEnabled,
			CertFile:         cfg.ConsoleTLSCertFile,
			KeyFile:          cfg.ConsoleTLSKeyFile,
			CAFile:           cfg.ConsoleTLSCAFile,
			ClientAuth:       cfg.ConsoleTLSClientAuth,
			PinnedPubKeyFile: cfg.ConsoleTLSPinnedPubKeyFile,
		})
		if err != nil {
			log.Fatalf("failed to build console server TLS config: %v", err)
		}
		srv.TLSConfig = tlsConfig
	}

	// ── 步骤 4.5：可选启动 gRPC 网关服务 ──────────────────────────────
	var grpcSrv *grpcserver.Server
	if cfg.ConsoleGRPCEnabled {
		grpcSrv = grpcserver.New(client, cfg, logger)
		go func() {
			if err := grpcSrv.Start(cfg.ConsoleGRPCAddress()); err != nil {
				logger.Error("bff grpc server failed", "error", err.Error())
			}
		}()
	}

	// ── 步骤 5：启动优雅关闭协程 ─────────────────────────────────────
	// 在独立 goroutine 中使用 signal.NotifyContext（Go 1.16+）监听系统信号，
	// 信号到达时自动取消 context，触发优雅停机流程。
	go func() {
		sigCtx, sigStop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer sigStop()
		<-sigCtx.Done()

		logger.Info("shutting down bff-go servers...")
		server.Shutdown()
		if grpcSrv != nil {
			grpcSrv.Stop()
		}

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("http server shutdown error", "error", err.Error())
		}
	}()

	// ── 步骤 6：启动服务 ──────────────────────────────────────────────
	logger.Info("backend-go started",
		"http_addr", cfg.ConsoleAddress(),
		"tls_enabled", cfg.ConsoleTLSEnabled,
		"mTLS", cfg.ConsoleTLSClientAuth != "",
		"grpc_enabled", cfg.ConsoleGRPCEnabled,
		"agent_grpc", cfg.AgentAddress(),
	)

	var srvErr error
	if cfg.ConsoleTLSEnabled {
		srvErr = srv.ListenAndServeTLS("", "")
	} else {
		srvErr = srv.ListenAndServe()
	}

	if srvErr != nil && srvErr != http.ErrServerClosed {
		log.Fatalf("http server failed: %v", srvErr)
	}
}
