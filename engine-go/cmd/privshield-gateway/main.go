// Package main 提供 L7 自适应负载均衡网关入口。
//
// 双协议代理：
//   - HTTP 反向代理：REST API 流量 → :8000
//   - gRPC 透明流代理：gRPC 流量 → :50000
//
// 环境变量：
//   - GATEWAY_BACKENDS：后端 Agent 地址（逗号分隔）
//   - GATEWAY_STRATEGY：调度策略（p2c/round_robin/least_conn）
//   - GATEWAY_HOST / GATEWAY_PORT：HTTP 监听地址（默认 127.0.0.1）
//   - GATEWAY_GRPC_HOST / GATEWAY_GRPC_PORT：gRPC 代理监听地址（默认 127.0.0.1）
//   - GATEWAY_REQUIRE_TLS：声明「必须加密」；网关不终止入站 TLS，置真即拒绝启动
//   - PRIVACY_LOG_LEVEL：日志级别
//
// 启动门禁（P0-1 零信任默认态，见 internal/config.Validate）：网关自身不校验入站凭据，
// 鉴权由被代理的 Agent 端 PRIVACY_AUTH_* 强制；因此非环回监听要求部署方已配置这些凭据，
// 否则启动即失败。入站 TLS 需由 mTLS 回源或前置入口（Ingress/Mesh）承担。
package main

import (
	"context"
	"crypto/subtle"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	engineconfig "github.com/fengzhizi319/PrivShield-go/engine-go/internal/config"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/gateway"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/observability"
	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
	"github.com/fengzhizi319/PrivShield-go/pkg/middleware"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
)

func main() {
	// P0-1 零信任默认态：网关不终止 TLS、也不校验入站凭据，非环回监听必须先声明凭据已配置。
	cfg := engineconfig.LoadGateway()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	observability.InitLogger(pkgconfig.EnvString("PRIVACY_LOG_LEVEL", "INFO"))

	slog.Info("Starting PrivShield L7 Adaptive Gateway",
		"version", Version,
		"build_time", BuildTime,
	)

	// 解析后端地址
	backends := pkgconfig.EnvString("GATEWAY_BACKENDS", "127.0.0.1:8079")
	addresses := strings.Split(backends, ",")

	// 调度策略
	strategy := pkgconfig.EnvString("GATEWAY_STRATEGY", "p2c")

	// 创建负载均衡器
	lb := gateway.NewLoadBalancer(addresses, strategy)

	// 初始化网关 Prometheus 指标（设计文档 §11.1）
	gwMetrics := observability.NewGatewayMetrics()

	// ── HTTP 反向代理 ──
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	middleware.ConfigureTrustedProxies(r, middleware.TrustedProxiesFromEnv()) // G-02
	r.Use(middleware.IPAllowlist(middleware.AllowedCIDRsFromEnv()))           // IP access control
	r.Use(gin.Recovery())
	r.Use(middleware.WAF(slog.Default())) // 三级等保 G-12：Web 攻击载荷检测
	r.Use(middleware.SecurityHeaders())   // CSP + HSTS + X-Frame-Options 等安全响应头
	r.Use(middleware.MaxBodySize(int64(pkgconfig.EnvInt("GATEWAY_MAX_BODY_BYTES", 32<<20))))

	// Ingress/LB 上游 TLS 终止校验：若配置 GATEWAY_REQUIRE_FORWARDED_PROTO 或 GATEWAY_REQUIRE_TLS，强制校验 X-Forwarded-Proto 为 https
	requireProto := pkgconfig.EnvBool("GATEWAY_REQUIRE_FORWARDED_PROTO", false) || pkgconfig.EnvBool("GATEWAY_REQUIRE_TLS", false)
	if requireProto {
		r.Use(func(c *gin.Context) {
			path := c.Request.URL.Path
			if path == "/health" {
				c.Next()
				return
			}
			proto := c.GetHeader("X-Forwarded-Proto")
			if proto == "" && c.Request.TLS != nil {
				proto = "https"
			}
			clientIP := middleware.RealClientIP(c)
			if proto != "https" && !pkgconfig.IsLoopbackHost(clientIP) {
				middleware.AbortWithError(c, http.StatusUpgradeRequired, "HTTPS_REQUIRED", "HTTPS is required by gateway security policy; ensure upstream Ingress/LB terminates TLS", nil)
				return
			}
			c.Next()
		})
		slog.Info("Gateway HTTPS enforcement enabled (requires X-Forwarded-Proto: https)")
	} else if !pkgconfig.IsLoopbackHost(cfg.RESTHost) {
		slog.Warn("Gateway TLS termination is skipped; ensure upstream Ingress or LoadBalancer terminates TLS (or set GATEWAY_REQUIRE_FORWARDED_PROTO=true)", "host", cfg.RESTHost)
	}

	if rps := pkgconfig.EnvInt("GATEWAY_RATE_LIMIT_RPS", 0); rps > 0 {
		burst := pkgconfig.EnvInt("GATEWAY_RATE_LIMIT_BURST", rps*2)
		r.Use(middleware.RateLimit(rps, burst)) // 32 分片令牌桶限流
		slog.Info("Gateway rate limiting enabled", "rps", rps, "burst", burst)
	}
	r.Use(observability.RequestLogger())
	r.Use(gwMetrics.PrometheusMiddleware()) // 网关转发指标

	// 网关自身健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "component": "gateway"})
	})

	// 后端拓扑与状态查询（R-02：受保护端点，防拓扑信息泄漏）
	backendsHandler := gateway.NewHealthCheckHandler(lb)
	metricsKey := pkgconfig.EnvString("GATEWAY_METRICS_API_KEY", "")
	adminKey := pkgconfig.EnvString("GATEWAY_ADMIN_API_KEY", metricsKey)
	if adminKey != "" {
		r.GET("/gateway/backends", func(c *gin.Context) {
			token := pkgauth.ExtractBearerToken(c.GetHeader("Authorization"))
			if subtle.ConstantTimeCompare([]byte(token), []byte(adminKey)) != 1 {
				middleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized: invalid gateway admin credentials", nil)
				return
			}
			backendsHandler(c)
		})
		slog.Info("Gateway /gateway/backends requires Bearer token auth")
	} else {
		r.GET("/gateway/backends", backendsHandler)
	}

	// Prometheus /metrics 端点（设计文档 §11.1）
	// P1-6: 可选鉴权 —— 若配置 GATEWAY_METRICS_API_KEY 则要求 Bearer Token，否则保持开放（环回/开发态）
	metricsHandler := gwMetrics.Handler()
	if metricsKey != "" {
		r.GET("/metrics", func(c *gin.Context) {
			token := pkgauth.ExtractBearerToken(c.GetHeader("Authorization"))
			if subtle.ConstantTimeCompare([]byte(token), []byte(metricsKey)) != 1 {
				middleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized: invalid gateway metrics credentials", nil)
				return
			}
			metricsHandler(c)
		})
		slog.Info("Gateway /metrics requires Bearer token auth")
	} else {
		r.GET("/metrics", metricsHandler)
	}

	// 反向代理：所有未匹配路由转发给后端（传入 metrics 实时上报 Prometheus 指标）
	r.NoRoute(gateway.NewHTTPProxyHandler(lb, gwMetrics))

	// 启动 HTTP 服务器
	httpAddr := cfg.RESTAddress()
	httpServer := &http.Server{
		Addr:         httpAddr,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		slog.Info("Gateway HTTP Proxy listening", "addr", httpAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Gateway HTTP server error", "err", err)
			os.Exit(1)
		}
	}()

	// ── gRPC 透明流代理 ──
	grpcAddr := cfg.GRPCAddress()

	grpcProxyServer, grpcLis, err := gateway.NewGrpcProxyListener(lb, grpcAddr, gwMetrics)
	if err != nil {
		slog.Error("gRPC proxy listener failed", "err", err)
		os.Exit(1)
	}

	go func() {
		slog.Info("Gateway gRPC Transparent Proxy listening", "addr", grpcAddr)
		if err := grpcProxyServer.Serve(grpcLis); err != nil {
			slog.Error("Gateway gRPC server error", "err", err)
		}
	}()

	// ── 启动配置摘要 ──
	slog.Info("Configuration summary",
		"http_addr", httpAddr,
		"grpc_addr", grpcAddr,
		"strategy", strategy,
		"backends", addresses,
		"require_tls", cfg.RequireTLS,
		"inbound_credentials_configured", cfg.AuthEffectivelyEnabled(),
	)

	// 优雅停机
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	slog.Info("Shutdown signal received", "signal", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		slog.Error("Gateway HTTP shutdown error", "err", err)
	}

	// gRPC 优雅停止带超时回退
	grpcDone := make(chan struct{})
	go func() {
		grpcProxyServer.GracefulStop()
		close(grpcDone)
	}()
	select {
	case <-grpcDone:
		slog.Info("Gateway gRPC stopped gracefully")
	case <-time.After(10 * time.Second):
		slog.Warn("Gateway gRPC graceful stop timed out, forcing stop")
		grpcProxyServer.Stop()
	}

	// 反向代理实例已内聚到 BackendNode（随节点创建/回收），无后台协程需要停止

	slog.Info("Gateway stopped gracefully")
}
