// Package main 提供 PrivShield Go 引擎的双协议（REST + gRPC）服务端入口。
//
// 架构：
//   - REST API (Gin)：面向外部调用方，端口 8079
//   - gRPC Server：面向内部微服务，端口 50051
//   - 信号处理：SIGINT/SIGTERM 优雅停机
//
// 环境变量：
//   - PRIVACY_REST_HOST / PRIVACY_REST_PORT：REST 监听地址（默认 127.0.0.1，容器编排显式注入 0.0.0.0）
//   - PRIVACY_GRPC_HOST / PRIVACY_GRPC_PORT：gRPC 监听地址（默认 127.0.0.1）
//   - PRIVACY_LOG_LEVEL：日志级别（DEBUG/INFO/WARN/ERROR）
//   - PRIVACY_TLS_ENABLED：是否启用 TLS (HTTPS / gRPC TLS)
//   - PRIVACY_REQUIRE_TLS：生产编排声明「必须加密」，TLS 关闭时启动即失败
//   - PRIVACY_TLS_CERT_FILE / PRIVACY_TLS_KEY_FILE / PRIVACY_TLS_CA_FILE：证书路径
//   - PRIVACY_AUTH_ENABLED + PRIVACY_AUTH_INTERNAL_API_KEYS：入站 API Key 鉴权
//   - PRIVACY_AUTH_INTERNAL_MTLS_ENABLED：是否启用 mTLS 客户端双向认证
//   - PRIVACY_AUTH_MTLS_WHITELIST_FILE：gRPC 客户端证书 CN 白名单（启用 TLS/mTLS 时必填）
//
// 启动门禁（P0-1 零信任默认态，见 internal/config.Validate）：非环回监听且未配置入站凭据、
// 声明需要 TLS 却未启用、启用 gRPC TLS 却缺少 CN 白名单文件，均直接终止进程而非静默降级。
package main

import (
	"context"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	engineconfig "github.com/fengzhizi319/PrivShield-go/engine-go/internal/config"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/grpcserver"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/observability"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/rest"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/security"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/service"
	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
	"github.com/fengzhizi319/PrivShield-go/pkg/middleware"
	"github.com/fengzhizi319/PrivShield-go/pkg/naming"
	"github.com/fengzhizi319/PrivShield-go/pkg/tlsutil"
)

// ──────────────────────────────────────────────
// 版本信息（编译时注入）
// ──────────────────────────────────────────────

var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

// ──────────────────────────────────────────────
// 配置
// ──────────────────────────────────────────────

type Config struct {
	// Runtime 承载监听面与安全开关，并提供 P0-1 fail-closed 门禁 Validate()。
	*engineconfig.Runtime
	LogLevel       string
	RateLimitRPS   int
	RateLimitBurst int
}

func loadConfig() Config {
	return Config{
		Runtime:        engineconfig.LoadAgent(),
		LogLevel:       pkgconfig.EnvString("PRIVACY_LOG_LEVEL", "INFO"),
		RateLimitRPS:   pkgconfig.EnvInt("PRIVACY_RATE_LIMIT_RPS", 1000),
		RateLimitBurst: pkgconfig.EnvInt("PRIVACY_RATE_LIMIT_BURST", 2000),
	}
}

// ──────────────────────────────────────────────
// 主入口
// ──────────────────────────────────────────────

func main() {
	cfg := loadConfig()

	// P0-1 零信任默认态：在打开任何监听端口之前通过 fail-closed 门禁，
	// 命中红线（远端无密钥 / 声明 TLS 却未启用 / mTLS 白名单缺失 / 证书不可读）直接终止进程。
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	// 初始化日志
	observability.InitLogger(cfg.LogLevel)

	slog.Info("Starting PrivShield Go Engine",
		"version", Version,
		"build_time", BuildTime,
		"git_commit", GitCommit,
	)

	// 初始化 PrivacyService 统一编排层
	svcCfg := service.DefaultConfig()
	svc, err := service.NewPrivacyService(svcCfg)
	if err != nil {
		slog.Error("Failed to init PrivacyService", "err", err)
		os.Exit(1)
	}

	// 初始化 Prometheus 指标收集器（设计文档 §11.1）
	engineMetrics := observability.NewEngineMetrics()

	// 注册 pkg/naming 观测器（P2-5）：别名命中与归一化失败既以结构化告警入日志，
	// 也计入 privshield_api_alias_requests_total / privshield_datasource_normalize_errors_total，
	// 与中台四服务同名同标签，直连枚举探测不再在指标面静默。
	naming.SetObserver(namingObserver{metrics: engineMetrics})

	// ── REST API (Gin) ──
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	middleware.ConfigureTrustedProxies(router, middleware.TrustedProxiesFromEnv()) // G-02
	router.Use(middleware.IPAllowlist(middleware.AllowedCIDRsFromEnv()))           // IP access control
	router.Use(gin.Recovery())
	router.Use(middleware.TraceMiddleware()) // 全链路分布式追踪 (X-Request-ID + X-Trace-ID)
	router.Use(security.SecurityHeadersMiddleware())
	router.Use(middleware.WAF(slog.Default())) // 三级等保 G-12：Web 攻击载荷检测
	router.Use(security.AuthMiddleware())
	router.Use(security.RateLimitMiddleware())

	// 可选限流中间件（设计文档 §12.7 / §13.4）
	if cfg.RateLimitRPS > 0 {
		router.Use(middleware.RateLimit(cfg.RateLimitRPS, cfg.RateLimitBurst))
		slog.Info("Rate limiting enabled", "rps", cfg.RateLimitRPS, "burst", cfg.RateLimitBurst)
	}

	router.Use(observability.RequestLogger())
	router.Use(engineMetrics.PrometheusMiddleware()) // Prometheus 实际指标注册

	// 注册全部 REST API 路由
	rest.RegisterRoutes(router, svc)

	// Prometheus /metrics 端点（设计文档 §11.1）
	router.GET("/metrics", engineMetrics.Handler())

	restAddr := cfg.RESTAddress()
	restServer := &http.Server{
		Addr:         restAddr,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		if cfg.TLSEnabled {
			slog.Info("REST HTTPS server starting", "addr", restAddr, "cert", cfg.TLSCertFile)
			if err := restServer.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile); err != nil && err != http.ErrServerClosed {
				slog.Error("REST HTTPS server error", "err", err)
				os.Exit(1)
			}
		} else {
			slog.Info("REST server starting", "addr", restAddr)
			if err := restServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("REST server error", "err", err)
				os.Exit(1)
			}
		}
	}()

	// ── gRPC Server ──
	grpcAddr := cfg.GRPCAddress()
	grpcLis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		slog.Error("gRPC listen failed", "err", err)
		os.Exit(1)
	}

	// 生产级 gRPC Keepalive 保活策略配置
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
			slog.Error("Failed to build gRPC TLS credentials", "err", err)
			os.Exit(1)
		}
		grpcOpts = append(grpcOpts, grpc.Creds(credentials.NewTLS(tlsCfg)))
		slog.Info("gRPC TLS credentials enabled", "mtls", cfg.MTLSEnabled)
	}

	// mTLS CN 白名单拦截器：路径已由门禁保证「启用 gRPC TLS / internal mTLS 时必然存在」，
	// 这里再对「已给路径却拿不到拦截器」的情形显式终止，杜绝静默跳过注册。
	whitelistPath := cfg.MTLSWhitelistFile
	if whitelistPath != "" {
		unaryInter, streamInter, _, err := tlsutil.NewWhitelistInterceptor(whitelistPath)
		if err != nil {
			slog.Error("Failed to init mTLS whitelist interceptor", "err", err)
			os.Exit(1)
		}
		if unaryInter == nil || streamInter == nil {
			slog.Error("mTLS whitelist interceptor was not registered; refusing to serve gRPC without CN authorization",
				"path", whitelistPath)
			os.Exit(1)
		}
		grpcOpts = append(grpcOpts,
			grpc.ChainUnaryInterceptor(unaryInter),
			grpc.ChainStreamInterceptor(streamInter),
		)
		slog.Info("mTLS CN whitelist interceptor enabled", "path", whitelistPath)
	}

	grpcSrv := grpcserver.NewServer(svc, grpcOpts...).WithMetrics(engineMetrics)
	go func() {
		slog.Info("gRPC server starting", "addr", grpcAddr)
		if err := grpcSrv.Serve(grpcLis); err != nil {
			slog.Error("gRPC server error", "err", err)
		}
	}()

	// ── 启动配置摘要 ──
	budgetStatus := svc.BudgetStatus()
	slog.Info("Configuration summary",
		"rest_addr", restAddr,
		"grpc_addr", grpcAddr,
		"tls_enabled", cfg.TLSEnabled,
		"require_tls", cfg.RequireTLS,
		"mtls_enabled", cfg.MTLSEnabled,
		"mtls_whitelist_file", cfg.MTLSWhitelistFile,
		"auth_enabled", cfg.AuthEffectivelyEnabled(),
		"rate_limit_rps", cfg.RateLimitRPS,
		"log_level", cfg.LogLevel,
		"budget_total_epsilon", budgetStatus["total_epsilon"],
		"budget_remaining_epsilon", budgetStatus["remaining_epsilon"],
	)

	// 本地环回无密钥开发形态：门禁已放行，但必须显式告警，防止被误当作生产形态长期运行。
	if !cfg.AuthEffectivelyEnabled() && !cfg.TLSEnabled {
		slog.Warn("running with authentication and TLS DISABLED on a loopback bind; " +
			"for any exposed deployment set PRIVACY_AUTH_ENABLED=true with PRIVACY_AUTH_INTERNAL_API_KEYS " +
			"and PRIVACY_TLS_ENABLED=true (plus PRIVACY_AUTH_MTLS_WHITELIST_FILE)")
	}

	// 等待退出信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	slog.Info("Shutdown signal received, starting graceful draining", "signal", sig)

	// 1. 标记 K8s 就绪探针为 unready
	rest.SetReady(false)

	// 2. 流量排空等待窗口
	drainSec := pkgconfig.EnvInt("PRIVACY_SHUTDOWN_DRAIN_SECONDS", 5)
	if drainSec > 0 {
		slog.Info("Draining in-flight traffic", "seconds", drainSec)
		time.Sleep(time.Duration(drainSec) * time.Second)
	}

	// 3. 优雅停止 REST 与 gRPC Server
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := restServer.Shutdown(ctx); err != nil {
		slog.Error("REST server shutdown error", "err", err)
	}

	// gRPC GracefulStop 带超时回退：若 RPC 不结束则强制停止，防止挂死
	grpcDone := make(chan struct{})
	go func() {
		grpcSrv.GracefulStop()
		close(grpcDone)
	}()
	grpcGraceSec := pkgconfig.EnvInt("PRIVACY_GRPC_GRACEFUL_STOP_SECONDS", 15)
	select {
	case <-grpcDone:
		slog.Info("gRPC server stopped gracefully")
	case <-time.After(time.Duration(grpcGraceSec) * time.Second):
		slog.Warn("gRPC graceful stop timed out, forcing stop", "timeout_sec", grpcGraceSec)
		grpcSrv.Stop()
	}

	slog.Info("Server stopped gracefully")
}

// ──────────────────────────────────────────────
// 辅助函数
// ──────────────────────────────────────────────

// namingObserver 同时以结构化日志与 Prometheus 计数承载 pkg/naming 的漂移事件（P2-5）。
// 基数策略与 pkg/metrics 侧一致：原始脏值只入日志，指标标签只用 canonical / 有界枚举。
type namingObserver struct {
	metrics *observability.EngineMetrics
}

func (o namingObserver) RecordAPIAlias(alias, canonical, target string) {
	slog.Warn("naming alias used", "alias", alias, "canonical", canonical, "target", target)
	o.metrics.RecordNamingAlias(alias, canonical, target)
}

func (o namingObserver) RecordNormalizeError(reason string) {
	slog.Warn("naming normalize failed", "reason", reason)
	o.metrics.RecordNamingError(reason)
}
