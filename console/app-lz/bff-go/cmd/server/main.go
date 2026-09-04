// Package main 是 PrivShield Console App-LZ BFF（调度之眼聚合后端）的入口程序。
//
// 启动流程：
//  1. 从环境变量加载配置（config.Load）
//  2. 校验配置合法性（cfg.Validate），TLS 开启时确认证书路径可访问
//  3. 创建 HTTP 客户端池（clients.NewClientPool），管理与 4 个上游微服务的连接
//  4. 创建 E2E 测试执行器（runner.NewTestRunner）
//  5. 创建 HTTP Handler 层并注册路由（handlers.NewHandler + SetupRouter）
//  6. 启动 HTTP Server（含 ReadHeaderTimeout 防 Slowloris 攻击）
//  7. 监听 SIGINT/SIGTERM 信号，执行优雅停机（5 秒超时）
//
// 上游微服务拓扑：
//   - Service Hub     (:8082 / :50052) — 流水线调度中枢（唯一业务编排入口，支持 HTTP(S) mTLS / TLCP 及 gRPC mTLS）
//   - Privacy Engine  (:8079 / :50051) — 核心隐私计算引擎（REST / gRPC）
//   - Mock Datasource (:8083 / :50053) — 模拟多源异构数据源服务（REST / gRPC）
//   - Audit Log       (:8084 / :50054) — 审计存证服务（REST / gRPC）
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/fengzhizi319/PrivShield-go/console/app-lz/bff-go/internal/auth"
	"github.com/fengzhizi319/PrivShield-go/console/app-lz/bff-go/internal/clients"
	"github.com/fengzhizi319/PrivShield-go/console/app-lz/bff-go/internal/config"
	"github.com/fengzhizi319/PrivShield-go/console/app-lz/bff-go/internal/handlers"
	"github.com/fengzhizi319/PrivShield-go/console/app-lz/bff-go/internal/runner"
	"github.com/fengzhizi319/PrivShield-go/pkg/metrics"
	"github.com/fengzhizi319/PrivShield-go/pkg/naming"
	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
)

func main() {
	// ── 第 1 步：加载配置 ──────────────────────────────────────────────
	// 从环境变量读取所有上游服务地址、TLS 配置、静态文件目录等。
	// 支持 APP_LZ_* 前缀变量，兼容无前缀的旧变量名。
	cfg := config.Load()

	// ── 第 2 步：校验配置合法性 ────────────────────────────────────────
	// 当 TLS 启用时，验证证书和私钥文件路径存在且可访问（fail-fast 策略）。
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	// ── 第 2.5 步：初始化结构化日志记录器 ──────────────────────────────
	// 使用共享库 pkg/observability.InitLogger 初始化基于 slog 的全局日志记录器（支持 json/text 格式）。
	pkgobs.InitLogger(cfg.LogFormat, cfg.LogLevel)
	logger := slog.Default()

	// ── 第 3 步：初始化核心组件 ────────────────────────────────────────────
	// Collector: Prometheus 指标收集器；注册为 naming 的观测器后，
	// 别名流量 / 归一化失败会在解析收口处自动上报（api_rename_design.md §7.2）。
	mc := metrics.NewCollector("app-lz-bff")
	naming.SetObserver(mc)
	// ClientPool: 封装对 4 个上游微服务的所有 HTTP 调用，含降级兜底逻辑
	pool := clients.NewClientPool(cfg)
	// TestRunner: E2E 测试套件执行器（TS-01 审计验真 / TS-02 压测 / TS-03 租约争抢）
	testRunner := runner.NewTestRunner(pool)

	// ── 第 3.5 步：初始化 RBAC 用户认证组件 ────────────────────────────
	var authHandler *auth.Handlers
	if cfg.AuthEnabled && cfg.JWTSecret != "" {
		jwtMgr, jwtErr := auth.NewJWTManager(cfg.JWTSecret, cfg.JWTExpiryHours)
		if jwtErr != nil {
			log.Fatalf("invalid JWT configuration: %v", jwtErr)
		}
		userStore := auth.NewUserStore()
		authHandler = auth.NewHandlers(userStore, jwtMgr, logger)
		logger.Info("RBAC user authentication enabled",
			"jwt_expiry_hours", cfg.JWTExpiryHours,
			"user_db_path", cfg.UserDBPath)
	} else {
		logger.Info("RBAC user authentication disabled (development mode)")
	}

	// Handler: 所有 HTTP 请求的处理层，编排 ClientPool 和 TestRunner
	h := handlers.NewHandler(cfg, pool, testRunner, mc, logger, authHandler)
	// SetupRouter: 注册所有 API 路由 + SPA 静态文件回退
	router := handlers.SetupRouter(h)

	// ── 第 4 步：配置 HTTP Server ──────────────────────────────────────
	addr := fmt.Sprintf("%s:%s", cfg.Host, cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadTimeout:       15 * time.Second, // 完整请求体读取超时
		WriteTimeout:      15 * time.Second, // 响应写入超时
		IdleTimeout:       60 * time.Second, // Keep-Alive 空闲超时
		ReadHeaderTimeout: 5 * time.Second,  // 请求头读取超时（防 Slowloris 攻击）
	}

	// ── 第 5 步：打印启动 Banner ──────────────────────────────────────
	// 显示 BFF 监听地址和所有上游微服务的连接地址，方便运维确认。
	fmt.Println("==================================================================")
	fmt.Println(" 🚀 启动 PrivShield Console App-LZ BFF (调度之眼 模拟业务程序)")
	fmt.Println("==================================================================")
	fmt.Printf("  REST API:       http://%s\n", addr)
	fmt.Printf("  Service Hub:    %s (唯一调度编排中枢)\n", cfg.HubURL)
	fmt.Printf("  Static SPA:     %s\n", cfg.StaticDir)
	fmt.Println("  架构原则:       零直接下游访问 (datasource/audit/engine 均走 hub 编排)")
	fmt.Println("==================================================================")

	// ── 第 6 步：在后台 goroutine 启动 HTTP 监听 ─────────────────────
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	// ── 第 7 步：优雅停机 ─────────────────────────────────────────────
	// 使用 signal.NotifyContext（Go 1.16+）监听系统信号，信号到达时自动取消 context。
	// 阻塞等待 SIGINT（Ctrl+C）或 SIGTERM（K8s kill）信号。
	// 收到信号后，调用 srv.Shutdown 给已连接客户端 5 秒时间完成请求，
	// 超时后强制退出。
	sigCtx, sigStop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer sigStop()
	<-sigCtx.Done()

	log.Println("Shutting down Console App-LZ BFF gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}
	log.Println("Console App-LZ BFF exited cleanly.")
}
