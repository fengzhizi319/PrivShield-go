// Package main 提供 L7 自适应负载均衡网关入口。
//
// 双协议代理：
//   - HTTP 反向代理：REST API 流量 → :8000
//   - gRPC 透明流代理：gRPC 流量 → :50000
//
// 环境变量：
//   - ENGINE_GATEWAY_BACKENDS：后端 Agent 地址（逗号分隔）
//   - ENGINE_GATEWAY_STRATEGY：调度策略（p2c/round_robin/least_conn）
//   - ENGINE_GATEWAY_HOST / ENGINE_GATEWAY_PORT：HTTP 监听地址（默认 127.0.0.1）
//   - ENGINE_GATEWAY_GRPC_HOST / ENGINE_GATEWAY_GRPC_PORT：gRPC 代理监听地址（默认 127.0.0.1）
//   - ENGINE_GATEWAY_REQUIRE_TLS：声明「必须加密」；网关不终止入站 TLS，置真即拒绝启动
//   - ENGINE_GATEWAY_AUTH_ENABLED：声明「启用鉴权门禁」
//   - ENGINE_GATEWAY_LOG_LEVEL：日志级别（默认 INFO）
//
// 启动门禁（P0-1 零信任默认态，见 internal/config.Validate）：网关自身不校验入站凭据，
// 鉴权由被代理的 Agent 端 AGENT_AUTH_* 强制；因此非环回监听要求部署方已配置这些凭据，
// 否则启动即失败。入站 TLS 需由 mTLS 回源或前置入口（Ingress/Mesh）承担。
//
// 主启动流程：
//  1. LoadGateway 读取网关监听地址、端口和安全配置，Validate 执行启动前 fail-closed 校验；
//  2. 初始化结构化日志，读取 ENGINE_GATEWAY_BACKENDS 和 ENGINE_GATEWAY_STRATEGY，创建负载均衡器；
//  3. 创建 GatewayMetrics，准备后端并发数、EWMA 延迟、熔断状态和转发请求计数；
//  4. 创建 Gin HTTP 引擎，按顺序挂载 IP 白名单、Recovery、WAF、安全响应头、请求体限制、
//     HTTPS 转发协议校验、限流、访问日志和 Prometheus 中间件；
//  5. 注册网关本地的 /health、/gateway/backends、/metrics 端点；这些端点由网关直接处理；
//  6. 设置 NoRoute 代理处理器：其他 HTTP 请求先由负载均衡器选择 Agent 后端，再转发请求、
//     复制响应并更新后端和请求指标；
//  7. 启动 HTTP 代理监听，同时创建并启动独立的 gRPC 透明代理监听；
//  8. 主协程等待 SIGINT/SIGTERM，收到信号后先停止 HTTP 接收，再优雅停止 gRPC；
//  9. 优雅停止超时则强制 Stop，避免连接或流式请求阻塞进程退出。
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
	// 【第 1 步：加载并校验配置】
	// P0-1 零信任默认态：网关不终止 TLS、也不校验入站凭据，非环回监听必须先声明凭据已配置。
	cfg := engineconfig.LoadGateway()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	// 【第 2 步：初始化日志】
	// 设置全局 slog Logger；后续网关启动、转发和停机过程统一输出结构化日志。
	observability.InitLogger(pkgconfig.EnvString("ENGINE_GATEWAY_LOG_LEVEL", "INFO"))

	slog.Info("Starting PrivShield L7 Adaptive Gateway",
		"version", Version,
		"build_time", BuildTime,
	)

	// 【第 3 步：初始化后端节点和负载均衡器】
	// 解析后端地址。每个地址代表一个 Agent 实例，负载均衡器会在转发请求时
	// 从这些节点中选择目标；多个地址用逗号分隔。
	backends := pkgconfig.EnvString("ENGINE_GATEWAY_BACKENDS", "127.0.0.1:8079")
	addresses := strings.Split(backends, ",")

	// 选择后端调度策略；策略只影响目标节点选择，不改变客户端到网关的连接。
	strategy := pkgconfig.EnvString("ENGINE_GATEWAY_STRATEGY", "p2c")

	// 创建负载均衡器，内部维护 BackendNode 状态、连接/延迟信息及熔断状态。
	lb := gateway.NewLoadBalancer(addresses, strategy)

	// 【第 4 步：初始化网关指标】
	// 初始化网关 Prometheus 指标（设计文档 §11.1）。
	gwMetrics := observability.NewGatewayMetrics()

	// 【第 5 步：构造 HTTP 反向代理引擎和中间件链】
	// ── HTTP 反向代理 ──
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	middleware.ConfigureTrustedProxies(r, middleware.TrustedProxiesFromEnv("ENGINE_GATEWAY_TRUSTED_PROXIES")) // G-02
	r.Use(middleware.IPAllowlist(middleware.AllowedCIDRsFromEnv("ENGINE_GATEWAY_ALLOWED_CIDRS")))             // IP access control
	r.Use(gin.Recovery())
	r.Use(middleware.WAF(slog.Default())) // 三级等保 G-12：Web 攻击载荷检测
	r.Use(middleware.SecurityHeaders())   // CSP + HSTS + X-Frame-Options 等安全响应头
	r.Use(middleware.MaxBodySize(int64(pkgconfig.EnvInt("ENGINE_GATEWAY_MAX_BODY_BYTES", 32<<20))))

	// 【第 6 步：校验上游 TLS 终止状态】
	// Ingress/LB 上游 TLS 终止校验：若配置 ENGINE_GATEWAY_REQUIRE_FORWARDED_PROTO 或 ENGINE_GATEWAY_REQUIRE_TLS，强制校验 X-Forwarded-Proto 为 https。
	requireProto := pkgconfig.EnvBool("ENGINE_GATEWAY_REQUIRE_FORWARDED_PROTO", false) || pkgconfig.EnvBool("ENGINE_GATEWAY_REQUIRE_TLS", false)
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
		slog.Warn("Gateway TLS termination is skipped; ensure upstream Ingress or LoadBalancer terminates TLS (or set ENGINE_GATEWAY_REQUIRE_FORWARDED_PROTO=true)", "host", cfg.RESTHost)
	}

	if rps := pkgconfig.EnvInt("ENGINE_GATEWAY_RATE_LIMIT_RPS", 0); rps > 0 {
		burst := pkgconfig.EnvInt("ENGINE_GATEWAY_RATE_LIMIT_BURST", rps*2)
		r.Use(middleware.RateLimit(rps, burst)) // 32 分片令牌桶限流
		slog.Info("Gateway rate limiting enabled", "rps", rps, "burst", burst)
	}
	r.Use(observability.RequestLogger())
	r.Use(gwMetrics.PrometheusMiddleware()) // 网关转发指标

	// 【第 7 步：注册网关本地 HTTP 端点】
	// /health、/gateway/backends 和 /metrics 由网关本地处理，不会转发到 Agent。
	// 网关自身健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "component": "gateway"})
	})

	// 后端拓扑与状态查询（R-02：受保护端点，防拓扑信息泄漏）
	backendsHandler := gateway.NewHealthCheckHandler(lb)
	metricsKey := pkgconfig.EnvString("ENGINE_GATEWAY_METRICS_API_KEY", "")
	adminKey := pkgconfig.EnvString("ENGINE_GATEWAY_ADMIN_API_KEY", metricsKey)
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
	// P1-6: 可选鉴权 —— 若配置 ENGINE_GATEWAY_METRICS_API_KEY 则要求 Bearer Token，否则保持开放（环回/开发态）
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

	// 【第 8 步：注册 HTTP 反向代理兜底路由】
	// 反向代理：所有未匹配路由转发给后端。请求先到达 Gateway，再由代理选择
	// Agent 建立或复用后端连接，完成转发后将响应写回原客户端，并实时上报指标。
	r.NoRoute(gateway.NewHTTPProxyHandler(lb, gwMetrics))

	// 【第 9 步：创建并启动 HTTP 监听】
	// 构造 HTTP Server。此处绑定已注册好的 Gin 引擎，但 ListenAndServe 尚未调用，
	// 因此到这里仍处于初始化阶段。
	httpAddr := cfg.RESTAddress()
	httpServer := &http.Server{
		Addr:         httpAddr,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		// 在独立 goroutine 中启动 HTTP 监听，使主 goroutine 能继续初始化 gRPC
		// 代理并等待统一的退出信号。
		slog.Info("Gateway HTTP Proxy listening", "addr", httpAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Gateway HTTP server error", "err", err)
			os.Exit(1)
		}
	}()

	// 【第 10 步：创建并启动 gRPC 透明代理】
	// ── gRPC 透明流代理 ──
	// gRPC 代理与 HTTP 代理使用不同端口和 Listener；它不解析业务 RPC，
	// 而是将 HTTP/2 gRPC 流转发到负载均衡器选出的 Agent 后端。
	grpcAddr := cfg.GRPCAddress()

	grpcProxyServer, grpcLis, err := gateway.NewGrpcProxyListener(lb, grpcAddr, gwMetrics)
	if err != nil {
		slog.Error("gRPC proxy listener failed", "err", err)
		os.Exit(1)
	}

	go func() {
		// gRPC Serve 开始阻塞监听；与 HTTP 代理并行运行，任一协议异常都会记录日志。
		slog.Info("Gateway gRPC Transparent Proxy listening", "addr", grpcAddr)
		if err := grpcProxyServer.Serve(grpcLis); err != nil {
			slog.Error("Gateway gRPC server error", "err", err)
		}
	}()

	// 【第 11 步：输出最终运行配置】
	// ── 启动配置摘要 ──
	slog.Info("Configuration summary",
		"http_addr", httpAddr,
		"grpc_addr", grpcAddr,
		"strategy", strategy,
		"backends", addresses,
		"require_tls", cfg.RequireTLS,
		"inbound_credentials_configured", cfg.AuthEffectivelyEnabled(),
	)

	// 【第 12 步：等待终止信号并执行优雅停机】
	// 主 goroutine 阻塞等待操作系统终止信号，避免启动函数提前返回。
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	slog.Info("Shutdown signal received", "signal", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// HTTP Shutdown 停止接收新请求，并在超时上下文内等待已进入代理处理链的请求完成。
	if err := httpServer.Shutdown(ctx); err != nil {
		slog.Error("Gateway HTTP shutdown error", "err", err)
	}

	// gRPC 优雅停止带超时回退：先拒绝新流并等待现有流结束，超过 10 秒则强制断开。
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
