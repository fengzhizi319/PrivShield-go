// Command server is the entry point for the service-hub module.
// Command server 是数据服务调度中枢模块（service-hub）的程序主入口。
//
// ==============================================================================
// Architecture & Traffic Flow / 系统架构与流量拓扑：
// ==============================================================================
//
//	┌────────────────────────┐         HTTP / JSON (:8082)
//	│  React Web UI / BFF-Go │ ──────────────────────────────────┐
//	└────────────────────────┘                                   │
//	                                                             ▼
//	┌────────────────────────┐   gRPC + mTLS 双向加密 (:50052)     ┌───────────────────────────────┐
//	│ 上游业务系统 / 客户端      │ ───────────────────────────────▶  │ service-hub 数据服务调度中枢     │
//	└────────────────────────┘                                   │ - HTTP REST: :8082            │
//	                                                             │ - gRPC (mTLS/Plain): :50052   │
//	                                                             │ - 6 阶段流水线调度引擎            │
//	                                                             └──────────────┬────────────────┘
//	                                                                            │
//	                         ┌──────────────────────────────────────────────────┴──────────────────────────────────┐
//	                         │ HTTP REST                                                                           │ HTTP REST / gRPC
//	                         ▼                                                                                     ▼
//	        ┌──────────────────────────────────┐                                                  ┌──────────────────────────────────┐
//	        │ PrivShield Agent 隐私脱敏引擎       │                                                  │ datasource-mgr 模拟数据源服务     │
//	        │ - 动态分类分级 /v1/dynclassificatio │                                                  │ - 医保/康养模拟数据 :8083 / :50053 │
//	        │ - 隐私脱敏与K匿名 /v1/privacy       │                                                  └──────────────────────────────────┘
//	        └──────────────────────────────────┘
//
// ==============================================================================
// Key Responsibilities / 核心职责：
// ==============================================================================
// 1. 配置与日志加载：从环境变量读取配置并初始化基于 slog 的结构化日志记录器；
// 2. 任务持久化存储初始化：支持纯内存存储（测试/轻量）与 SQLite 持久化存储（生产容灾）；
// 3. Prometheus 指标收集器：初始化请求计数、耗时分布与流水线执行指标；
// 4. 下游客户端组件实例化：创建与 PrivShield Agent 及 datasource-mgr 通信的客户端；
// 5. 双协议并发服务监听：在独立协程中启动 HTTP REST (Gin) 与 gRPC (支持零信任 mTLS 与公钥固定)；
// 6. 优雅停机收敛：拦截 SIGINT/SIGTERM，先向异步任务协程发送取消信号，再顺序关闭 gRPC 与 HTTP 服务器。
// ==============================================================================

package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
	"github.com/fengzhizi319/PrivShield-go/pkg/metrics"
	"github.com/fengzhizi319/PrivShield-go/pkg/middleware"
	"github.com/fengzhizi319/PrivShield-go/pkg/naming"
	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
	"github.com/fengzhizi319/PrivShield-go/pkg/store"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/memory"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/postgres"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/sqlite"
	"github.com/fengzhizi319/PrivShield-go/pkg/tlsutil"

	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/agent"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/config"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/datasource"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/grpcserver"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/handlers"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/retry"
	pb "github.com/fengzhizi319/PrivShield-go/services/service-hub/proto"
)

func main() {
	// =========================================================================
	// 1. Configuration Loading / 配置解析与加载
	// =========================================================================
	// 从环境变量中读取运行配置（如 SERVICE_HUB_PORT, AGENT_REST_HOST, DB_PATH, TLS 配置等），
	// 未设置时采用安全合理的回退默认值（默认 HTTP :8082, gRPC :50052）。
	cfg := config.Load()

	// Validate configuration consistency (fail-fast with clear error messages).
	// 校验配置一致性（如 TLS 启用但证书文件缺失），快速失败并给出清晰错误。
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	// 三级等保/密评 G-17：初始化 gRPC API Key + Scope 应用层鉴权配置。
	// 当配置 KeysFile 时创建 KeyStore，支持 API Key 文件热轮转（K8s Secret 投影场景）。
	var keyStore *pkgauth.KeyStore
	if cfg.KeysFile != "" {
		ks, ksErr := pkgauth.NewKeyStore(cfg.KeysFile)
		if ksErr != nil {
			log.Fatalf("failed to initialize API Key store: %v", ksErr)
		}
		defer ks.Close()
		keyStore = ks
	}
	grpcserver.InitAuthSettings(cfg.APIKey, cfg.ScopeKeys, keyStore)

	// =========================================================================
	// 2. Structured Logger Setup / 结构化日志系统初始化
	// =========================================================================
	// 使用共享库 pkgobs.NewLogger 初始化基于 slog 的全局日志记录器（支持 json/text 格式）。
	logger := pkgobs.NewLogger(cfg.LogFormat, cfg.LogLevel)

	// =========================================================================
	// 3. Task Store Initialization / 任务持久化存储初始化
	// =========================================================================
	// 优先使用 PostgreSQL 租约存储；未配置时使用 SQLite 或进程内内存存储。
	//
	// 3.1 SQLite Integrity Check / SQLite 完整性校验
	// 启动时先校验数据库完整性，检测损坏并阻止服务启动，防止带病运行。
	if cfg.PGDSN == "" && cfg.DBPath != "" {
		if err := sqlite.ValidateIntegrity(cfg.DBPath); err != nil {
			log.Fatalf("sqlite integrity check failed: %v", err)
		}
		logger.Info("database integrity check passed", "path", cfg.DBPath)
	}

	taskStore, err := initLeasedTaskStore(cfg, logger)
	if err != nil {
		log.Fatalf("failed to initialize task store: %v", err)
	}
	logger.Info("service-hub storage posture",
		"strict_storage", cfg.StrictStorage,
		"pg_configured", cfg.PGDSN != "",
		"lease_capable", cfg.PGDSN != "")

	// =========================================================================
	// 4. Prometheus Metrics Collector / Prometheus 监控指标收集器
	// =========================================================================
	// 注册 service-hub 命名空间的 Prometheus 监控指标（QPS、延迟、流水线各阶段状态等）。
	// 注意：mc 必须在崩溃恢复/重试之前初始化，以便记录恢复/重试指标。
	mc := metrics.NewCollector("service-hub")
	// 注册命名观测器：pkg/naming 归一化时自动上报别名使用 / 脏 ID 指标（§7.2）。
	naming.SetObserver(mc)

	// =========================================================================
	// 3.5 Crash Recovery / 崩溃恢复机制
	// =========================================================================
	// 启动时自动扫描并恢复孤立任务：
	// - pending 任务：直接保留在队列中（尚未执行，无需标记失败）；
	// - running 任务：标记为 failed（可能已部分执行，需重新提交）。
	if err := recoverOrphanedTasks(taskStore, mc, logger); err != nil {
		log.Fatalf("failed to recover orphaned tasks: %v", err)
	}

	// =========================================================================
	// 3.6 Automatic Task Retry / 失败任务自动重试
	// =========================================================================
	// 启动时自动重试因临时错误（网络超时、连接失败等）而失败的任务。
	// 最多重试 3 次，使用结构化 RetryCount 字段（替代脆弱的字符串匹配）。
	// 重试采用指数退避延迟，避免下游服务仍不可用时立即再次失败。
	retryFailedTasks(taskStore, mc, logger)

	// =========================================================================
	// 3.7 Periodic Background Retry / 周期性后台重试协程
	// =========================================================================
	// 启动后台协程，每 60 秒扫描一次 failed 任务并自动重试。
	// 解决“运行时失败的任务必须等到下次服务重启才能重试”的问题。
	retryCtx, retryCancel := context.WithCancel(context.Background())
	go periodicRetryLoop(retryCtx, taskStore, mc, logger, 60*time.Second)

	// =========================================================================
	// 3.8 Periodic Data Retention Cleanup / 周期性数据保留清理协程
	// =========================================================================
	// 启动后台协程，每 6 小时扫描并清理超过保留期的终态任务，防止 SQLite 无限膨胀。
	// RetentionDays=0 时禁用清理（适用于调试或短期部署）。
	retentionCtx, retentionCancel := context.WithCancel(context.Background())
	if cfg.RetentionDays > 0 {
		go dataRetentionLoop(retentionCtx, taskStore, logger, cfg.RetentionDays)
	}

	// =========================================================================
	// 5. Upstream & Downstream Clients Setup / 下游依赖客户端实例化
	// =========================================================================
	// 1) AgentClient: 负责与 PrivShield Python Core Sidecar（:8079）通信，调用分类分级与脱敏算子；
	// 2) DatasourceClient: 负责与 datasource-mgr 模拟数据源服务（:8083/:50053）交互，采样抽取数据。
	agentClient, err := agent.New(cfg, mc)
	if err != nil {
		log.Fatalf("failed to create agent client: %v", err)
	}
	dsClient := datasource.New(cfg)

	// 3) EvidenceClient（⑥ 审计存证出站）装配自检（P0-6 / Gate G-05）：
	//    每一次出域必须由 audit-log 落一条不可篡改存证，存证不可写 = 任务必然失败。
	//    Config.Validate() 已对「回环绑定 + 未配置端点」直接拒绝启动；这里覆盖远程绑定的情形，
	//    显式告警而非静默放行，避免运维误以为服务正常而实际所有出域任务在 ⑥ 阶段失败。
	if urls := cfg.AuditLogURLs(); len(urls) == 0 {
		logger.Error("outbound evidence endpoint is not configured: every data-egress task will FAIL at pipeline stage audit (P0-6 fail-closed)",
			"remedy", "set SERVICE_HUB_AUDIT_LOG_URLS (or SERVICE_HUB_AUDIT_HTTP) to the audit-log service, e.g. http://audit-log:8084")
	} else {
		logger.Info("outbound evidence client enabled",
			"endpoints", strings.Join(urls, ","),
			"auth_configured", cfg.AuditLogAPIKey != "",
			"tls_enabled", cfg.AuditLogTLSEnabled)
	}
	// Start in a non-ready state until both HTTP and gRPC listeners are confirmed launched.
	mc.SetReady(false)

	// =========================================================================
	// 6. HTTP REST Server Setup / HTTP REST 路由与服务器构建
	// =========================================================================
	// 1) 锁定 Gin 为生产发布模式（ReleaseMode）；
	// 2) 实例化 HTTP 处理器集合，装配任务分发调度、流水线查询、数据源代理等端点；
	// 3) 初始化无默认中间件的 Gin 引擎，并通过 RegisterRoutes 挂载通用中间件链（RequestID、Logger、Recovery、CORS、Auth）；
	// 4) 显式配置 http.Server 网络超时参数，防范 Slowloris 慢速连接拒绝服务攻击。
	gin.SetMode(gin.ReleaseMode)
	server := handlers.New(agentClient, dsClient, cfg, keyStore, taskStore, logger, mc)
	router := gin.New()
	middleware.ConfigureTrustedProxies(router, middleware.TrustedProxiesFromEnv("SERVICE_HUB_TRUSTED_PROXIES")) // G-02
	router.Use(middleware.IPAllowlist(middleware.AllowedCIDRsFromEnv("SERVICE_HUB_ALLOWED_CIDRS")))             // IP access control
	server.RegisterRoutes(router)

	httpSrv := &http.Server{
		Addr:              cfg.Address(),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,   // 限制读取 HTTP Header 的最大时间，防御 Slowloris
		ReadTimeout:       30 * time.Second,  // 读取请求体的超时时间
		WriteTimeout:      60 * time.Second,  // 响应写入的超时时间
		IdleTimeout:       120 * time.Second, // Keep-Alive 空闲连接保活上限
		MaxHeaderBytes:    1 << 20,           // 1 MiB 单请求 Header 最大字节限制
	}

	// =========================================================================
	// 6.5 HTTP TLS/mTLS Configuration / HTTP TLS 双向认证配置
	// =========================================================================
	// 当启用 TLS 时，为 HTTP 服务器构建 TLS 配置，支持 mTLS 双向认证：
	// - TLS 1.3 强制最低版本；
	// - 可选客户端证书认证（require/verify/request）；
	// - 可选公钥固定（SPKI Pinning）。
	var httpTLSConfig *tls.Config
	if cfg.TLSEnabled {
		tlsCfg := &tlsutil.ServerTLSConfig{
			Enabled:          cfg.TLSEnabled,
			CertFile:         cfg.TLSCertFile,
			KeyFile:          cfg.TLSKeyFile,
			CAFile:           cfg.TLSCAFile,
			ClientAuth:       cfg.TLSClientAuth,
			PinnedPubKeyFile: cfg.TLSPinnedPubKeyFile,
		}
		var tlsErr error
		httpTLSConfig, tlsErr = tlsutil.BuildServerTLSConfig(tlsCfg)
		if tlsErr != nil {
			log.Fatalf("failed to build HTTP TLS config: %v", tlsErr)
		}
		httpSrv.TLSConfig = httpTLSConfig
		logger.Info("HTTP REST server configured with mTLS",
			"client_auth", cfg.TLSClientAuth,
			"tls_cert", cfg.TLSCertFile,
		)
	}

	// =========================================================================
	// 7. gRPC Server Setup (with optional mTLS) / gRPC 服务构建（支持可选 mTLS）
	// =========================================================================
	// 根据配置判断是否启用 mTLS 双向认证：
	// - 启用 TLS: 加载服务端证书/私钥，挂载 CA 证书校验客户端身份，注册服务桩并开启 TLS 1.3 强加密；
	// - 未启用 TLS: 启动标准明文 gRPC Server 实例，适用于本地开发或 Service Mesh 代理。
	var grpcServer *grpc.Server
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

	// 三级等保/密评 G-17：挂载 gRPC 应用层 API Key + Scope 鉴权拦截器，
	// 并与 mTLS CN 白名单拦截器链式组合（避免单独使用 grpc.UnaryInterceptor 相互覆盖）。
	unaryInterceptors := []grpc.UnaryServerInterceptor{grpcserver.AuthUnaryInterceptor()}
	streamInterceptors := []grpc.StreamServerInterceptor{grpcserver.AuthStreamInterceptor()}
	if cfg.MTLSWhitelistFile != "" {
		unaryInterceptor, streamInterceptor, dw, err := tlsutil.NewWhitelistInterceptor(cfg.MTLSWhitelistFile)
		if err != nil {
			log.Fatalf("failed to load mTLS whitelist: %v", err)
		}
		defer dw.Close()
		unaryInterceptors = append(unaryInterceptors, unaryInterceptor)
		streamInterceptors = append(streamInterceptors, streamInterceptor)
		logger.Info("gRPC server configured with mTLS CN whitelist",
			"path", cfg.MTLSWhitelistFile,
		)
	}
	grpcServerOpts = append(grpcServerOpts, grpc.ChainUnaryInterceptor(unaryInterceptors...))
	grpcServerOpts = append(grpcServerOpts, grpc.ChainStreamInterceptor(streamInterceptors...))

	if cfg.TLSEnabled {
		creds, credErr := grpcserver.BuildServerCredentials(cfg)
		if credErr != nil {
			log.Fatalf("failed to build TLS credentials: %v", credErr)
		}
		grpcServer = grpc.NewServer(append(grpcServerOpts, grpc.Creds(creds))...)
		serviceImpl = grpcserver.New(agentClient, dsClient, cfg, taskStore, logger)
		pb.RegisterServiceHubServiceServer(grpcServer, serviceImpl)
		logger.Info("gRPC server started with mTLS",
			"addr", cfg.GRPCAddress(),
			"tls_cert", cfg.TLSCertFile,
			"tls_key", cfg.TLSKeyFile,
		)
	} else {
		grpcServer = grpc.NewServer(grpcServerOpts...)
		serviceImpl = grpcserver.New(agentClient, dsClient, cfg, taskStore, logger)
		pb.RegisterServiceHubServiceServer(grpcServer, serviceImpl)
		logger.Info("gRPC server started (insecure)", "addr", cfg.GRPCAddress())
	}

	if cfg.PGDSN != "" {
		hostname, hostErr := os.Hostname()
		if hostErr != nil {
			log.Fatalf("resolve lease worker hostname: %v", hostErr)
		}
		owner := fmt.Sprintf("%s-%d", hostname, os.Getpid())
		if err := serviceImpl.StartLeaseWorker(owner, time.Duration(cfg.LeaseTTL)*time.Second); err != nil {
			log.Fatalf("start PostgreSQL lease worker: %v", err)
		}
	} else {
		if err := server.StartLocalWorker(); err != nil {
			log.Fatalf("start local pending task worker: %v", err)
		}
	}

	// =========================================================================
	// 7.5 Startup Config Summary / 启动配置摘要横幅
	// =========================================================================
	// Log key configuration flags at startup so operators can verify the
	// security posture and runtime parameters at a glance.
	// 启动时记录关键配置摘要，便于运维确认服务状态与安全姿态。
	logger.Info("service-hub startup configuration",
		"http_addr", cfg.Address(),
		"grpc_addr", cfg.GRPCAddress(),
		"agent_rest", cfg.AgentBaseURL(),
		"datasource_rest", cfg.DatasourceBaseURL(),
		"tls_enabled", cfg.TLSEnabled,
		"auth_enabled", cfg.APIKey != "" || len(cfg.ScopeKeys) > 0 || keyStore != nil,
		"cors_origins", len(cfg.CORSOrigins),
		"db_path", cfg.DBPath,
		"pg_dsn", redactDSN(cfg.PGDSN),
		"lease_ttl", cfg.LeaseTTL,
		"retention_days", cfg.RetentionDays,
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
			"    SERVICE_HUB_TLS_ENABLED=true\n" +
			"    SERVICE_HUB_API_KEYS=<token:name:scope1,scope2;...>\n" +
			"  See docs/production_security/ops.md for details.\n" +
			"========================================================================")
	}

	// =========================================================================
	// 8. Operating System Signal Registration / 系统中断信号监听
	// =========================================================================
	// 使用 signal.NotifyContext（Go 1.16+）替代传统的 signal.Notify + channel 模式，
	// 信号到达时自动取消 context，与下游协程的 ctx.Done() 无缝衔接。
	sigCtx, sigStop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer sigStop()

	// =========================================================================
	// 9. Dual-Protocol Concurrent Listeners / 双协议并发监听启动
	// =========================================================================
	// 1) 启动 gRPC TCP 监听端口（默认 :50052）并在后台协程中运行事件循环
	grpcLis, err := net.Listen("tcp", cfg.GRPCAddress())
	if err != nil {
		log.Fatalf("failed to listen on gRPC address %s: %v", cfg.GRPCAddress(), err)
	}

	go func() {
		if err := grpcServer.Serve(grpcLis); err != nil {
			logger.Error("gRPC server error", "error", err.Error())
		}
	}()

	// 2) 启动 HTTP REST 服务并在后台独立协程中监听请求
	go func() {
		if tlsutil.IsTLCPEnabled("SERVICE_HUB_TLS_NATIONAL_CIPHER") {
			tlcpCfg := tlsutil.TLCPConfigFromEnv("SERVICE_HUB_")
			gmtlsConfig, tlcpErr := tlsutil.BuildTLCPConfig(tlcpCfg)
			if tlcpErr != nil {
				log.Fatalf("failed to build TLCP config: %v", tlcpErr)
			}
			tlcpLis, tlcpErr := tlsutil.NewTLCPListener("tcp", cfg.Address(), gmtlsConfig)
			if tlcpErr != nil {
				log.Fatalf("failed to create TLCP listener: %v", tlcpErr)
			}
			logger.Info("service-hub TLCP (国密) REST server started",
				"addr", cfg.Address(),
				"sign_cert", tlcpCfg.SignCertFile,
			)
			if err := httpSrv.Serve(tlcpLis); err != nil && err != http.ErrServerClosed {
				logger.Error("TLCP server error", "error", err.Error())
			}
		} else if cfg.TLSEnabled {
			logger.Info("service-hub HTTPS REST server started (mTLS enabled)",
				"addr", cfg.Address(),
				"grpc_addr", cfg.GRPCAddress(),
				"agent_rest", cfg.AgentBaseURL(),
				"datasource_rest", cfg.DatasourceBaseURL(),
				"db_path", cfg.DBPath,
				"auth_enabled", cfg.APIKey != "" || len(cfg.ScopeKeys) > 0 || keyStore != nil,
				"mtls_client_auth", cfg.TLSClientAuth,
			)
			// ListenAndServeTLS 使用 httpSrv.TLSConfig 中的证书，空字符串表示从 TLSConfig 读取
			if err := httpSrv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				logger.Error("HTTPS server error", "error", err.Error())
			}
		} else {
			logger.Info("service-hub HTTP REST server started",
				"addr", cfg.Address(),
				"grpc_addr", cfg.GRPCAddress(),
				"agent_rest", cfg.AgentBaseURL(),
				"datasource_rest", cfg.DatasourceBaseURL(),
				"db_path", cfg.DBPath,
				"auth_enabled", cfg.APIKey != "" || len(cfg.ScopeKeys) > 0 || keyStore != nil,
			)
			if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("HTTP server error", "error", err.Error())
			}
		}
	}()

	// Both REST and gRPC listeners have been launched successfully; mark service-hub ready.
	mc.SetReady(true)

	// =========================================================================
	// 10. Graceful Shutdown Workflow / 优雅停机收敛流程
	// =========================================================================
	// 1) 阻塞等待退出信号（SIGINT / SIGTERM）
	<-sigCtx.Done()
	logger.Info("shutting down service-hub servers...")

	// 2) 停止周期性重试协程与数据保留清理协程
	retryCancel()
	retentionCancel()

	// 3) 优先向内部异步流水线任务发送取消信号，平滑等待在途处理协程完成
	serviceImpl.Shutdown()
	server.Shutdown()

	// 3) 优雅停止 gRPC 服务器，拒绝新连接并等待当前 RPC 调用返回
	// GracefulStop with timeout: fall back to hard Stop() to avoid indefinite blocking.
	// 带超时的优雅停机：超时后回退到强制停止，防止在途 RPC 阻塞无限等待。
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

	// 4) 优雅关闭 HTTP 服务器，等待在途请求完成（超时时间可配置）
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ShutdownTimeout)*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err.Error())
	} else {
		logger.Info("HTTP server stopped")
	}
}

// initTaskStore initializes either an in-memory task store or a persistent SQLite database.
// initTaskStore 根据配置的 dbPath 初始化任务存储介质：
// - dbPath 为空：使用轻量内存存储（memory.NewTaskStore()）；
// - dbPath 非空：打开并初始化 SQLite 数据库连接（sqlite.NewTaskStore(db)）。
func initTaskStore(dbPath string, logger *slog.Logger) (store.TaskStore, error) {
	if dbPath == "" {
		logger.Info("using in-memory task store (no persistence)")
		return memory.NewTaskStore(), nil
	}

	db, err := sqlite.Open(dbPath, logger)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	ts, err := sqlite.NewTaskStore(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create task store: %w", err)
	}

	logger.Info("sqlite task store initialized", "path", dbPath)
	return ts, nil
}

// initLeasedTaskStore initializes the task store with PostgreSQL lease support (Phase B).
// initLeasedTaskStore 初始化带 PostgreSQL 租约支持的任务存储（Phase B）。
//
// 优先级：
//  1. PG_DSN 非空 → PostgreSQL LeasedTaskStore（支持多副本 Hub）
//  2. DBPath 非空 → SQLite TaskStore（租约方法返回 ErrLeaseNotSupported）
//  3. 均为空 → 内存 TaskStore（租约方法返回 ErrLeaseNotSupported）
//
// P0-4 禁静音降级：PG_DSN 已配置而探测失败时，StrictStorage（默认 true）直接上抛错误，
// 由 main() log.Fatalf 终止进程——回退到 2/3 档会让多副本 Hub 无声丢失租约语义。
// 仅当显式 SERVICE_HUB_STRICT_STORAGE=false 时才允许回退（保留原 Warn 路径）。
func initLeasedTaskStore(cfg *config.Config, logger *slog.Logger) (store.LeasedTaskStore, error) {
	if cfg.PGDSN != "" {
		logger.Info("probing PostgreSQL leased task store (Phase B multi-replica Hub)")
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		pgStore, err := postgres.NewStore(
			ctx,
			postgres.Config{
				DSN:     cfg.PGDSN,
				MaxConn: int32(cfg.PGMaxConn),
				MinConn: int32(cfg.PGMinConn),
			},
			logger,
		)
		if err == nil {
			logger.Info("postgresql leased task store initialized",
				"max_conns", cfg.PGMaxConn,
				"min_conns", cfg.PGMinConn,
				"lease_ttl", cfg.LeaseTTL,
			)
			return pgStore, nil
		}
		if cfg.StrictStorage {
			return nil, fmt.Errorf("strict storage mode (SERVICE_HUB_STRICT_STORAGE=true): PostgreSQL leased task store probe failed, refusing to fall back to a store without lease semantics: %w", err)
		}
		logger.Warn("PostgreSQL connection probe failed, falling back to SQLite / in-memory store", "error", err.Error())
	}

	// Fallback to SQLite / memory (lease operations return ErrLeaseNotSupported)
	// 回退到 SQLite / 内存（租约操作返回 ErrLeaseNotSupported）
	ts, err := initTaskStore(cfg.DBPath, logger)
	if err != nil {
		return nil, err
	}

	// Both sqlite.TaskStore and memory.TaskStore implement LeasedTaskStore
	// (with lease methods returning ErrLeaseNotSupported).
	// sqlite.TaskStore 和 memory.TaskStore 均实现 LeasedTaskStore 接口
	// （租约方法返回 ErrLeaseNotSupported）。
	leased, ok := ts.(store.LeasedTaskStore)
	if !ok {
		return nil, fmt.Errorf("internal: task store does not implement LeasedTaskStore")
	}

	logger.Warn("using non-PostgreSQL store: lease operations will return ErrLeaseNotSupported. " +
		"Set SERVICE_HUB_PG_DSN for multi-replica Hub support.")
	return leased, nil
}

// redactDSN returns a safe-for-logging version of the PostgreSQL DSN.
// redactDSN 返回可安全记录日志的 PostgreSQL DSN（隐藏密码）。
func redactDSN(dsn string) string {
	if dsn == "" {
		return "(not configured)"
	}
	// Simple redaction: show only first 20 chars / 简单脱敏：仅显示前 20 个字符
	if len(dsn) > 20 {
		return dsn[:20] + "...[REDACTED]"
	}
	return "[SET]"
}

// recoverOrphanedTasks scans for tasks stuck in "running" or "pending" state
// after a crash/restart and handles them appropriately:
// - pending tasks: kept in queue (not yet executed, safe to requeue);
// - running tasks: marked as failed (may have partially executed).
// 崩溃恢复：区分处理 running 和 pending 状态的孤立任务。
//
// 当服务突然崩溃（kill -9、OOM Kill、断电）时，优雅停机代码不会执行，
// 导致 running/pending 状态的任务永远卡在数据库中。此函数在启动时自动恢复这些孤立任务。
//
// 改进点（#1）：pending 任务直接保留在队列中（它们尚未执行，无需标记失败）；
// running 任务标记为 failed（可能已部分执行，需要重新提交）。
func recoverOrphanedTasks(taskStore store.TaskStore, mc *metrics.Collector, logger *slog.Logger) error {
	// 1. 扫描所有 "running" 状态的任务 → 标记为 failed（可能已部分执行）
	runningTasks, _, err := taskStore.List(store.TaskFilter{Status: "running", Limit: 10000})
	if err != nil {
		return fmt.Errorf("list running tasks: %w", err)
	}

	for i := range runningTasks {
		runningTasks[i].Status = "failed"
		runningTasks[i].Error = "server crashed or restarted (recovered on startup)"
		runningTasks[i].ErrorClass = retry.ClassRecovered
		now := time.Now()
		runningTasks[i].CompletedAt = &now
		runningTasks[i].DurationMs = now.Sub(runningTasks[i].CreatedAt).Milliseconds()
		if err := taskStore.Update(&runningTasks[i]); err != nil {
			return fmt.Errorf("mark running task %s as failed: %w", runningTasks[i].ID, err)
		}
		if mc != nil {
			mc.RecordOrphanedRecovery("running")
		}
	}

	// 2. 扫描所有 "pending" 状态的任务 → 直接保留在队列中（尚未执行，无需标记失败）
	pendingTasks, _, err := taskStore.List(store.TaskFilter{Status: "pending", Limit: 10000})
	if err != nil {
		return fmt.Errorf("list pending tasks: %w", err)
	}

	// pending 任务无需修改状态，仅记录指标
	for range pendingTasks {
		if mc != nil {
			mc.RecordOrphanedRecovery("pending")
		}
	}

	// 3. 记录恢复日志
	if len(runningTasks) > 0 || len(pendingTasks) > 0 {
		logger.Warn("recovered orphaned tasks after crash/restart",
			"running_marked_failed", len(runningTasks),
			"pending_kept_in_queue", len(pendingTasks),
			"total_recovered", len(runningTasks)+len(pendingTasks))
	} else {
		logger.Info("no orphaned tasks found, all tasks are in terminal state")
	}
	return nil
}

// maxRetryCount is the maximum number of retry attempts for a failed task.
const maxRetryCount = 3

// retryFailedTasks automatically retries failed tasks whose persisted failure class
// is marked retryable.
// 自动重试机制：扫描所有因瞬时故障而失败的任务，重新提交执行。
//
// 改进点（#3）：使用结构化 RetryCount 字段替代脆弱的 strings.Count；
// 改进点（#10）：重试采用指数退避延迟（5s → 10s → 20s），避免下游仍不可用时立即再次失败；
// 改进点（P2-7）：是否重试改判为读取失败点落库的 error_class 枚举，
// 不再对 task.Error 这段自由文案做子串匹配（文案改写即会静默丧失重试能力）。
func retryFailedTasks(taskStore store.TaskStore, mc *metrics.Collector, logger *slog.Logger) {
	// 扫描所有 "failed" 状态的任务
	failedTasks, _, err := taskStore.List(store.TaskFilter{Status: "failed", Limit: 100})
	if err != nil {
		logger.Error("failed to list failed tasks for retry", "error", err.Error())
		return
	}

	retryCount := 0
	for i := range failedTasks {
		// 只重试失败点已判定为瞬时的分类（如 timeout / downstream / shutdown / recovered）
		if !retry.IsRetryableClass(failedTasks[i].ErrorClass) {
			continue
		}

		// 使用结构化 RetryCount 字段检查重试次数（替代脆弱的 strings.Count）
		if failedTasks[i].RetryCount >= maxRetryCount {
			logger.Warn("task exceeded max retry attempts, skipping",
				"task_id", failedTasks[i].ID,
				"retry_count", failedTasks[i].RetryCount,
				"max_retry", maxRetryCount)
			if mc != nil {
				mc.RecordTaskRetry("exhausted")
			}
			continue
		}

		// 检查退避延迟（#10）：如果 RetryAfter 尚未到期，跳过
		if failedTasks[i].RetryAfter != nil && time.Now().Before(*failedTasks[i].RetryAfter) {
			continue
		}

		// 计算指数退避延迟：5s * 2^(retryCount)
		newRetryCount := failedTasks[i].RetryCount + 1
		backoffDuration := 5 * time.Second * time.Duration(1<<uint(failedTasks[i].RetryCount))
		retryAfter := time.Now().Add(backoffDuration)

		// 重置任务状态为 pending
		failedTasks[i].Status = "pending"
		failedTasks[i].Stage = "queued"
		failedTasks[i].Error = fmt.Sprintf("retrying (attempt %d/%d)", newRetryCount, maxRetryCount)
		failedTasks[i].ErrorClass = ""
		failedTasks[i].StartedAt = nil
		failedTasks[i].CompletedAt = nil
		failedTasks[i].DurationMs = 0
		failedTasks[i].RetryCount = newRetryCount
		failedTasks[i].RetryAfter = &retryAfter

		if err := taskStore.Update(&failedTasks[i]); err != nil {
			logger.Error("failed to reset task for retry", "task_id", failedTasks[i].ID, "error", err.Error())
			continue
		}

		retryCount++
		if mc != nil {
			mc.RecordTaskRetry("queued")
		}
		logger.Info("task queued for retry",
			"task_id", failedTasks[i].ID,
			"attempt", newRetryCount,
			"backoff_seconds", backoffDuration.Seconds())
	}

	if retryCount > 0 {
		logger.Info("queued tasks for retry", "count", retryCount)
	} else {
		logger.Debug("no retryable failed tasks found")
	}
}

// periodicRetryLoop runs retryFailedTasks periodically until the context is cancelled.
// 周期性后台重试循环：每隔 interval 扫描一次 failed 任务并自动重试。
// 解决“运行时失败的任务必须等到下次服务重启才能重试”的问题（#2）。
func periodicRetryLoop(ctx context.Context, taskStore store.TaskStore, mc *metrics.Collector, logger *slog.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	logger.Info("periodic background retry started", "interval_seconds", interval.Seconds())

	for {
		select {
		case <-ctx.Done():
			logger.Info("periodic background retry stopped")
			return
		case <-ticker.C:
			retryFailedTasks(taskStore, mc, logger)
		}
	}
}

// dataRetentionLoop periodically deletes terminal tasks older than retentionDays.
// dataRetentionLoop 周期性删除超过保留期的终态任务，防止 SQLite 无限膨胀。
//
// 每 6 小时执行一次清理，仅删除 completed/failed 状态的任务，
// 保留 pending/running 状态的任务不受影响。
func dataRetentionLoop(ctx context.Context, taskStore store.TaskStore, logger *slog.Logger, retentionDays int) {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	logger.Info("data retention cleanup started", "retention_days", retentionDays, "interval_hours", 6)

	// Run once immediately on startup / 启动时立即执行一次
	runRetentionCleanup(taskStore, logger, retentionDays)

	for {
		select {
		case <-ctx.Done():
			logger.Info("data retention cleanup stopped")
			return
		case <-ticker.C:
			runRetentionCleanup(taskStore, logger, retentionDays)
		}
	}
}

// runRetentionCleanup performs a single cleanup pass.
func runRetentionCleanup(taskStore store.TaskStore, logger *slog.Logger, retentionDays int) {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	deleted, err := taskStore.CleanupOld(cutoff)
	if err != nil {
		logger.Error("data retention cleanup failed", "error", err.Error())
		return
	}
	if deleted > 0 {
		logger.Info("data retention cleanup completed",
			"deleted_tasks", deleted,
			"cutoff", cutoff.Format(time.RFC3339),
			"retention_days", retentionDays)
	}
}
