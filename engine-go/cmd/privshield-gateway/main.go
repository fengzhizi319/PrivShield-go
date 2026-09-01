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
	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
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
	r.Use(gin.Recovery())
	r.Use(observability.RequestLogger())
	r.Use(gwMetrics.PrometheusMiddleware()) // 网关转发指标

	// 网关自身健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "component": "gateway"})
	})

	// 后端状态查询
	r.GET("/gateway/backends", gateway.NewHealthCheckHandler(lb))

	// Prometheus /metrics 端点（设计文档 §11.1）
	r.GET("/metrics", gwMetrics.Handler())

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
