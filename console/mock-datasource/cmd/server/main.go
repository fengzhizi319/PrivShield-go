// Command server is the entry point for the mock datasource-mgr module.
// Command server 是模拟数据源模块（datasource-mgr）的程序入口。
//
// 本服务在 PrivShield 架构中扮演“数据源资产与敏感特征模拟服务”角色，
// 作为数据提供者（Data Provider）仅向数盾调度中枢 service-hub 输出结构化医保、康养及测试数据源；
// 前端控制台与 BFF 网关不直连本服务，统一经 service-hub 编排调度，同时支持 HTTP REST 与双向认证 gRPC (mTLS) 双协议接入。
//
// ==============================================================================
// Architecture & Traffic Flow / 系统架构与流量图：
// ==============================================================================
//
//	┌────────────────────────┐   gRPC + mTLS 双向加密 (:50053)   ┌───────────────────────────────┐
//	│ service-hub 数据调度中枢 │ ───────────────────────────────▶ │ datasource-mgr 模拟数据源服务  │
//	│   【唯一直接调用方】     │   HTTPS REST + mTLS (:8083)      │ - HTTP REST: :8083           │
//	└────────────────────────┘ ───────────────────────────────▶ │ - gRPC (mTLS/Plain): :50053  │
//	                                                            │ - 提供 yibao/kangyang 模拟数据 │
//	                                                            └───────────────────────────────┘
//	（前端控制台 / BFF 网关不直连本服务，统一经 service-hub 编排调度）
//
// ==============================================================================
// Key Responsibilities / 核心职责：
// ==============================================================================
// 1. 配置加载 (Configuration)：从环境变量解析 HTTP/gRPC 端口、mTLS 证书链、安全鉴权与日志参数；
// 2. 结构化日志 (Structured Logging)：初始化统一的 slog 结构化日志输出（JSON/Text）；
// 3. HTTP REST 服务 (Gin + net/http)：暴露标准 REST API 及探针，配置超时与防 Slowloris 参数；
// 4. gRPC 服务 (Protobuf + mTLS)：根据配置支持零信任 mTLS 双向认证或明文模式，注册服务实现；
// 5. 并发协程服务 (Dual-Protocol Goroutines)：在独立 goroutine 中异步启动 HTTP 与 gRPC 服务；
// 6. 优雅停机 (Graceful Shutdown)：捕获 SIGINT/SIGTERM，顺序执行 gRPC 停机与带超时的 HTTP 优雅退出。
// ==============================================================================

package main

import (
	"context"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
	pkggrpcserver "github.com/fengzhizi319/PrivShield-go/pkg/grpcserver"
	"github.com/fengzhizi319/PrivShield-go/pkg/metrics"
	"github.com/fengzhizi319/PrivShield-go/pkg/middleware"
	"github.com/fengzhizi319/PrivShield-go/pkg/naming"
	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
	"github.com/fengzhizi319/PrivShield-go/pkg/tlsutil"
	"github.com/fengzhizi319/PrivShield-go/services/datasource-mgr/internal/config"
	"github.com/fengzhizi319/PrivShield-go/services/datasource-mgr/internal/grpcserver"
	"github.com/fengzhizi319/PrivShield-go/services/datasource-mgr/internal/handlers"
	pb "github.com/fengzhizi319/PrivShield-go/services/datasource-mgr/proto"
)

func main() {
	// =========================================================================
	// 1. Configuration Loading / 配置解析与加载
	// =========================================================================
	// 从环境变量读取运行配置（如 DATASOURCE_MGR_HOST/PORT, TLS/mTLS 证书路径, API_KEY 等），
	// 未设置时采用安全合理的默认值（默认 HTTP :8083, gRPC :50053）。
	cfg := config.Load()

	// Validate configuration consistency (fail-fast with clear error messages).
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	// =========================================================================
	// 2. Structured Logger Setup / 结构化日志系统初始化
	// =========================================================================
	// 使用共享库 pkg/observability.InitLogger 初始化基于 slog 的全局结构化日志记录器，
	// 支持 JSON（生产环境推荐，便于 Loki/ELK 采集）与 Text（本地开发高可读）两种格式。
	pkgobs.InitLogger(cfg.LogFormat, cfg.LogLevel)
	logger := slog.Default()

	// =========================================================================
	// 2.5 Strict storage / 严格存储模式（P0-4 禁静音降级）
	// =========================================================================
	// 默认开启：样本/探查数据文件出现损坏行时直接上抛为请求失败，
	// 而不是静默丢弃记录后照常返回 200（调用方无法感知数据集被缩小）。
	handlers.SetStrictDataIntegrity(cfg.StrictStorage)
	logger.Info("datasource-mgr storage posture", "strict_data_integrity", cfg.StrictStorage)

	// =========================================================================
	// 2.6 API Key 文件热轮转（K8s Secret 投影场景）
	// =========================================================================
	var keyStore *pkgauth.KeyStore
	if cfg.KeysFile != "" {
		ks, ksErr := pkgauth.NewKeyStore(cfg.KeysFile)
		if ksErr != nil {
			log.Fatalf("failed to initialize API Key store: %v", ksErr)
		}
		defer ks.Close()
		keyStore = ks
		logger.Info("API Key store initialized with hot-reload",
			"path", cfg.KeysFile, "keys", len(ks.Keys()))
	}

	// =========================================================================
	// 3. HTTP REST Server Setup / HTTP REST 路由与服务器构建
	// =========================================================================
	// 1) 锁定 Gin 为生产发布模式（ReleaseMode），禁用控制台调试冗余输出与性能损耗；
	// 2) 实例化 HTTP 处理器集合，封装数据源 CRUD、模拟数据集（yibao/kangyang）与健康探针；
	// 3) 初始化无默认中间件的纯净 Gin 引擎，并通过 handlers.RegisterRoutes 装配中间件栈：
	//    - RequestID: 全链路请求追踪 ID 生成与注入；
	//    - StructuredLogger: 请求访问日志记录；
	//    - Recovery: Panic 拦截与自动恢复，防止单请求崩溃导致进程退出；
	//    - SecurityHeaders: HSTS, X-Content-Type-Options 等安全响应头；
	//    - CORS: 跨域资源共享策略；
	//    - Auth: 基于 Header API Key 的身份认证（配置时生效）。
	gin.SetMode(gin.ReleaseMode)
	// Prometheus 指标收集器（§7.2）：暴露 GET /metrics，并注册为 pkg/naming 观测器，
	// 使别名流量 / 脏 ID 计数在归一化统一入口自动上报。
	mc := metrics.NewCollector("datasource-mgr")
	naming.SetObserver(mc)
	server := handlers.New(cfg, keyStore, logger, mc)
	router := gin.New()
	middleware.ConfigureTrustedProxies(router, middleware.TrustedProxiesFromEnv("DATASOURCE_MGR_TRUSTED_PROXIES")) // G-02
	router.Use(middleware.IPAllowlist(middleware.AllowedCIDRsFromEnv("DATASOURCE_MGR_ALLOWED_CIDRS")))             // IP access control
	server.RegisterRoutes(router)

	// 4) 显式配置 http.Server 网络超时参数，防范 Slowloris 慢连接拒绝服务攻击与连接泄露：
	//    - ReadHeaderTimeout: 5s （限制读取 HTTP Header 的最大时间，防御 Slowloris）
	//    - ReadTimeout: 30s       （读取整个请求体的时间）
	//    - WriteTimeout: 60s      （响应写入的最长时间）
	//    - IdleTimeout: 120s      （Keep-Alive 空闲连接保活复用上限）
	//    - MaxHeaderBytes: 1MB    （单请求 Header 最大字节限制）
	httpSrv := &http.Server{
		Addr:              cfg.Address(),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	if cfg.TLSEnabled {
		httpTLSConfig, err := grpcserver.BuildServerTLSConfig(cfg)
		if err != nil {
			log.Fatalf("failed to build TLS config for HTTP/HTTPS server: %v", err)
		}
		httpSrv.TLSConfig = httpTLSConfig
	}

	// =========================================================================
	// 4. gRPC Server Setup (with optional mTLS) / gRPC 服务构建（支持可选 mTLS）
	// =========================================================================
	// 根据配置决定是否开启 mTLS 双向认证：
	// - 开启 TLS (cfg.TLSEnabled = true):
	//   通过 grpcserver.BuildServerCredentials 加载服务端私钥/证书，并挂载 CA 证书校验客户端身份，
	//   支持基于证书指纹（Pinned Public Key）或 ClientAuth（RequireAndVerifyClientCert）强校验；
	// - 未开启 TLS:
	//   创建标准明文 gRPC Server 实例，适用于本地快速开发或由 Service Mesh (如 Istio/Envoy) 代理 TLS 的场景。
	var grpcServer *pkggrpcserver.Server
	var serviceImpl *grpcserver.GRPCServer

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

	grpcServer = pkggrpcserver.New(cfg.GRPCAddress(), grpcServerOpts...)

	// mTLS CN whitelist authorization for inbound gRPC connections.
	if cfg.MTLSWhitelistFile != "" {
		unaryInterceptor, streamInterceptor, dw, err := tlsutil.NewWhitelistInterceptor(cfg.MTLSWhitelistFile)
		if err != nil {
			log.Fatalf("failed to load mTLS whitelist: %v", err)
		}
		defer dw.Close()
		grpcServer = grpcServer.
			WithUnaryInterceptor(unaryInterceptor).
			WithStreamInterceptor(streamInterceptor)
		logger.Info("gRPC server configured with mTLS CN whitelist",
			"path", cfg.MTLSWhitelistFile,
		)
	}

	// G-17: 应用层 API Key 鉴权拦截器（与 mTLS 叠加，形成双层鉴权）。
	grpcserver.InitAuthSettings(cfg.APIKey, cfg.ScopeKeys, keyStore)
	grpcServer = grpcServer.
		WithUnaryInterceptor(grpcserver.AuthUnaryInterceptor()).
		WithStreamInterceptor(grpcserver.AuthStreamInterceptor())
	if cfg.APIKey != "" || len(cfg.ScopeKeys) > 0 || keyStore != nil {
		logger.Info("gRPC server configured with API Key auth",
			"scope_keys", len(cfg.ScopeKeys),
			"keys_file", cfg.KeysFile,
		)
	}

	if cfg.TLSEnabled {
		creds, credErr := grpcserver.BuildServerCredentials(cfg)
		if credErr != nil {
			log.Fatalf("failed to build TLS credentials: %v", credErr)
		}
		grpcServer = grpcServer.WithOptions(grpc.Creds(creds))
		serviceImpl = grpcserver.New(cfg, logger)
		grpcServer.RegisterService(&pb.DataSourceManagerService_ServiceDesc, serviceImpl)
		logger.Info("mock datasource-mgr gRPC server started with mTLS",
			"addr", cfg.GRPCAddress(),
			"tls_cert", cfg.TLSCertFile,
			"tls_key", cfg.TLSKeyFile,
		)
	} else {
		serviceImpl = grpcserver.New(cfg, logger)
		grpcServer.RegisterService(&pb.DataSourceManagerService_ServiceDesc, serviceImpl)
		logger.Info("mock datasource-mgr gRPC server started (insecure)", "addr", cfg.GRPCAddress())
	}

	// =========================================================================
	// 4.5 Startup Config Summary / 启动配置摘要横幅
	// =========================================================================
	// Log key configuration flags at startup so operators can verify the
	// security posture and runtime parameters at a glance.
	// 启动时记录关键配置摘要，便于运维确认服务状态与安全姿态。
	logger.Info("datasource-mgr startup configuration",
		"http_addr", cfg.Address(),
		"grpc_addr", cfg.GRPCAddress(),
		"tls_enabled", cfg.TLSEnabled,
		"auth_enabled", cfg.APIKey != "" || len(cfg.ScopeKeys) > 0 || keyStore != nil,
		"require_tls", cfg.RequireTLS,
		"strict_storage", cfg.StrictStorage,
		"cors_origins", len(cfg.CORSOrigins),
		"shutdown_timeout", cfg.ShutdownTimeout,
		"log_format", cfg.LogFormat,
		"log_level", cfg.LogLevel,
	)

	// Emit a prominent security warning when all protections are disabled.
	// 当所有安全功能均未启用时输出醒目警告，防止生产环境意外裸奔。
	if !cfg.TLSEnabled && cfg.APIKey == "" && len(cfg.ScopeKeys) == 0 && keyStore == nil {
		logger.Warn("========================================================================\n" +
			"  SECURITY WARNING: All security features are DISABLED.\n" +
			"  TLS=off  Auth=off\n" +
			"  All endpoints are exposed without encryption or authentication.\n" +
			"  For production deployments, set:\n" +
			"    DATASOURCE_MGR_TLS_ENABLED=true\n" +
			"    DATASOURCE_MGR_API_KEY=<your-key>\n" +
			"  See docs/production_security/ops.md for details.\n" +
			"========================================================================")
	}

	// =========================================================================
	// 5. Operating System Signal Registration / 系统中断信号监听
	// =========================================================================
	// 使用 signal.NotifyContext（Go 1.16+）替代传统的 signal.Notify + channel 模式，
	// 信号到达时自动取消 context，与下游协程的 ctx.Done() 无缝衔接。
	// 确保服务在收到退出指令时能够完成正在处理中的请求，防止数据损坏或客户端异常断连。
	sigCtx, sigStop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer sigStop()

	// =========================================================================
	// 6. Dual-Protocol Concurrent Listeners / 双协议并发监听启动
	// =========================================================================
	// 1) 启动 gRPC TCP 监听端口（默认 :50053），失败时阻断进程启动
	grpcLis, err := net.Listen("tcp", cfg.GRPCAddress())
	if err != nil {
		log.Fatalf("failed to listen on gRPC address %s: %v", cfg.GRPCAddress(), err)
	}

	// 2) 在后台独立 Goroutine 中启动 gRPC 事件循环
	// ─────────────────────────────────────────────────────────────────────────
	// 💡 【核心执行机制与请求处理全流程 / How gRPC Handles Connections & Requests】：
	//
	// a. 【连接监听与接收 (TCP Accept & TLS Handshake)】：
	//    `grpcServer.ServeListener(grpcLis)` 内部执行标准的 `for { rawConn, err := grpcLis.Accept(); ... }` 阻塞循环：
	//    - 当上游（如 service-hub 或外部微服务）发起连接时，Accept() 接收底层的 raw TCP 连接；
	//    - 若开启 mTLS（`cfg.TLSEnabled=true`），立即进行 TLS 1.3 握手，执行证书链校验与公钥固定 (SPKI Pinning)；
	//    - 握手成功后，为该 TCP 连接创建独立的 HTTP/2 传输层处理器 (transport)，单个 TCP 即可支持高并发 Stream 多路复用。
	//
	// b. 【请求路由与并发派发 (HTTP/2 Frame & Method Dispatch)】：
	//    - 当客户端通过该连接发送 RPC 请求时（如 HTTP/2 HEADERS 帧携带 `:path: /datasourcemgr.DataSourceManagerService/GetRecordByIDCard`）；
	//    - gRPC 运行时根据注册表（通过上文 `pb.RegisterDataSourceManagerServiceServer` 注册的服务描述 `ServiceDesc`）查找对应的方法处理器；
	//    - 为每个 RPC 请求独立派发一个 Worker Goroutine，实现高并发无阻塞处理。
	//
	// c. 【相关核心代码位置 / Related Code Locations】：
	//    1. 路由分发与 Protobuf 编解码桩代码：
	//       - 文件：`proto/datasourcemgr_grpc.pb.go`
	//       - 符号：`_DataSourceManagerService_GetRecordByIDCard_Handler`、`DataSourceManagerService_ServiceDesc`
	//    2. 业务逻辑核心实现（方法接收者为 `*grpcserver.GRPCServer`）：
	//       - 文件：`internal/grpcserver/server.go`
	//       - 方法：
	//         * `GetRecordByIDCard(ctx, req)`：按身份证号查询单条记录（医保/康养）
	//         * `ListDataSources(ctx, req)`：数据源资产目录列表查询（委托内部 ListMockSources）
	//         * `GetDataSource(ctx, req)`：单个数据源元数据详情查询
	//         * `TestConnection(ctx, req)`：数据源连通性测试
	//         * `Health(ctx, req)`：gRPC 服务存活与就绪探针
	//    3. 底层高保真数据加载与检索引擎：
	//       - 文件：`internal/handlers/data_provider.go`
	//       - 函数：`LoadCSVRecords(filename)`、`GetRecordByIDCard(sourceID, idCardNo)`
	// ─────────────────────────────────────────────────────────────────────────
	go func() {
		if err := grpcServer.ServeListener(grpcLis); err != nil {
			logger.Error("gRPC server error", "error", err.Error())
		}
	}()

	// 3) 在后台独立 Goroutine 中启动 HTTP/HTTPS REST 服务
	go func() {
		if tlsutil.IsTLCPEnabled("DATASOURCE_MGR_TLS_NATIONAL_CIPHER") {
			tlcpCfg := tlsutil.TLCPConfigFromEnv("DATASOURCE_MGR_")
			gmtlsConfig, tlcpErr := tlsutil.BuildTLCPConfig(tlcpCfg)
			if tlcpErr != nil {
				log.Fatalf("failed to build TLCP config: %v", tlcpErr)
			}
			tlcpLis, tlcpErr := tlsutil.NewTLCPListener("tcp", cfg.Address(), gmtlsConfig)
			if tlcpErr != nil {
				log.Fatalf("failed to create TLCP listener: %v", tlcpErr)
			}
			logger.Info("datasource-mgr TLCP (国密) REST server started",
				"addr", cfg.Address(),
				"sign_cert", tlcpCfg.SignCertFile,
			)
			if err := httpSrv.Serve(tlcpLis); err != nil && err != http.ErrServerClosed {
				logger.Error("TLCP server error", "error", err.Error())
			}
		} else if cfg.TLSEnabled {
			logger.Info("mock datasource-mgr HTTPS REST server started with mTLS",
				"addr", cfg.Address(),
				"grpc_addr", cfg.GRPCAddress(),
				"tls_cert", cfg.TLSCertFile,
				"client_auth", cfg.TLSClientAuth,
				"pinned_pubkey", cfg.TLSPinnedPubKeyFile,
				"mode", "mock_development_and_debugging",
			)
			// 利用 httpSrv.TLSConfig 中预置的证书链、ClientCA 池与公钥固定钩子启动 HTTPS
			if err := httpSrv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				logger.Error("HTTPS server error", "error", err.Error())
			}
		} else {
			logger.Info("mock datasource-mgr HTTP REST server started",
				"addr", cfg.Address(),
				"grpc_addr", cfg.GRPCAddress(),
				"mode", "mock_development_and_debugging",
			)
			if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("HTTP server error", "error", err.Error())
			}
		}
	}()

	// =========================================================================
	// 7. Graceful Shutdown Workflow / 优雅停机收敛流程
	// =========================================================================
	// 1) 阻塞等待退出信号到达
	<-sigCtx.Done()
	logger.Info("shutting down mock datasource-mgr servers...")

	// 2) 优雅关闭 gRPC 内部后台任务与服务：
	//    - serviceImpl.Shutdown(): 发送内部 context 取消通知并等待后台异步任务完成；
	//    - grpcServer.GracefulStop(): 停止接受新连接，等待在途（In-flight）RPC 请求处理完毕。
	// GracefulStop with timeout: fall back to hard Stop() to avoid indefinite blocking.
	// 带超时的优雅停机：超时后回退到强制停止，防止在途 RPC 阻塞无限等待。
	serviceImpl.Shutdown()
	grpcDone := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(grpcDone)
	}()
	select {
	case <-grpcDone:
		logger.Info("gRPC server stopped")
	case <-time.After(30 * time.Second):
		logger.Warn("gRPC GracefulStop timed out after 30s, forcing stop")
		grpcServer.Stop()
		logger.Info("gRPC server force stopped")
	}

	// 3) 优雅关闭 HTTP REST 服务：
	//    - 使用带有可配置超时上限的 context，等待现有 HTTP 请求结束；
	//    - 若超时内未完成则强制断开连接，释放 TCP 端口资源。
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ShutdownTimeout)*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err.Error())
	} else {
		logger.Info("HTTP server stopped")
	}
}
