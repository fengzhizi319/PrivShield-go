// Package main 提供 PrivShield Go 核心隐私与动态分类分级引擎（Core Agent / Sidecar）的服务端入口。
// 本文件实现 REST (Gin) 服务模块的构建、中间件编排、TLCP/TLS/HTTP 自适应监听与优雅停机。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/observability"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/rest"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/service"
	"github.com/fengzhizi319/PrivShield-go/pkg/middleware"
	"github.com/fengzhizi319/PrivShield-go/pkg/tlsutil"
)

// ─────────────────────────────────────────────────────────────────────────────
// REST 服务运行实体与生命周期管理
// ─────────────────────────────────────────────────────────────────────────────

// RESTServerRunner 封装基于 Gin 的 REST HTTP/HTTPS/TLCP 服务的生命周期管理。
// 负责：
//  1. Gin 引擎与双层防御性中间件过滤漏斗（基础设施层 + 安全防护层）的装配；
//  2. 44 项隐私原语与动态分类分级业务路由及 Prometheus /metrics 端点的注册；
//  3. 自适应支持三种传输安全模式：GM/T 0024 国密 TLCP 双证书、标准 TLS 1.2/1.3、明文 HTTP；
//  4. 生产级超时的 http.Server 实例化与基于 context 的无损优雅停机。
type RESTServerRunner struct {
	server   *http.Server
	listener net.Listener
	addr     string
	cfg      Config
}

// newRESTServerRunner 构造 REST 服务运行实体并完成中间件管道与路由初始化。
//
// 管道结构（两层防御过滤漏斗）：
//
//	┌────────────────────────────────────────────────────────────────────────┐
//	│ 基础设施层 (全局注册)                                                   │
//	│   [G-02] ConfigureTrustedProxies ── 校验可信代理 CIDR，防止伪造 XFF 来源   │
//	│   ① IPAllowlist ── 网络层 CIDR 白名单准入闸门，非白名单 IP 立即 403 阻断    │
//	│   ② gin.Recovery ── 捕获下游任何 panic 崩溃，记录堆栈并输出 500 标准信封    │
//	│   ③ TraceMiddleware ── 生成/透传 X-Request-ID 与 X-Trace-ID 上下文追踪标识 │
//	│   ④ RateLimit ── (可选) 全局粗粒度令牌桶限流，用于入口流量总削峰防雪崩     │
//	│   ⑤ RequestLogger ── 结构化请求访问日志记录 (方法/路径/状态码/耗时/TraceID) │
//	│   ⑥ PrometheusMiddleware ── 实时采集 HTTP QPS、耗时分位数与状态分布指标   │
//	├────────────────────────────────────────────────────────────────────────┤
//	│ 安全防护层 (rest.RegisterRoutes 内部前置注册)                           │
//	│   ⑦ SecurityHeadersMiddleware ── 注入 HSTS, CSP, X-Frame-Options 安全标头 │
//	│   ⑧ MaxBodySize ── 限制请求报文体 ≤64MB，防止大报文内存耗尽 DoS 攻击       │
//	│   ⑨ WAF ── 预编译正则引擎扫描，拦截 SQLi, XSS, 路径穿越与命令注入恶意载荷  │
//	│   ⑩ AuthMiddleware ── 双模式 API Key 认证（常量时间比较 subtle.ConstantTime）│
//	│   ⑪ RateLimitMiddleware ── 32 分片高并发令牌桶细粒度限流 (按身份/IP/路径)  │
//	├────────────────────────────────────────────────────────────────────────┤
//	│ 业务处理层                                                             │
//	│   ⑫ 业务 Handler ── 执行脱敏、差分隐私、K-匿名、动态分类分级等计算逻辑     │
//	└────────────────────────────────────────────────────────────────────────┘
func newRESTServerRunner(
	cfg Config,
	svc *service.PrivacyService,
	engineMetrics *observability.EngineMetrics,
) (*RESTServerRunner, error) {
	gin.SetMode(gin.ReleaseMode) // 生产模式：关闭调试日志与路由表打印，降低运行开销
	router := gin.New()          // 空引擎：不内置默认 Logger/Recovery，确保链路顺序完全受控

	// G-02：受信任代理配置。仅当对端 IP 属于可信 CIDR 时才信任 X-Forwarded-For 标头
	middleware.ConfigureTrustedProxies(router, middleware.TrustedProxiesFromEnv("AGENT_TRUSTED_PROXIES"))

	// ① IP 准入白名单（AGENT_ALLOWED_CIDRS）
	router.Use(middleware.IPAllowlist(middleware.AllowedCIDRsFromEnv("AGENT_ALLOWED_CIDRS")))

	// ② Panic 恢复保护：兜底捕获异常，防止协程崩溃造成服务宕机
	router.Use(gin.Recovery())

	// ③ 全链路分布式追踪：注入并透传 TraceID / RequestID
	router.Use(middleware.TraceMiddleware())

	// ④ 全局粗粒度限流：入口总量削峰，防御突发海量请求 DoS
	if cfg.RateLimitRPS > 0 {
		router.Use(middleware.RateLimit(cfg.RateLimitRPS, cfg.RateLimitBurst))
		slog.Info("Global REST rate limiting enabled", "rps", cfg.RateLimitRPS, "burst", cfg.RateLimitBurst)
	}

	// ⑤ 结构化请求访问日志（slog 输出）
	router.Use(observability.RequestLogger())

	// ⑥ Prometheus RED 指标统计中间件
	router.Use(engineMetrics.PrometheusMiddleware())

	// 注册全部 REST API 路由（内部装配安全防护层中间件并注册各业务 Handler、K8s 探针）
	rest.RegisterRoutes(router, svc)

	// Prometheus 抓取端点（免鉴权，供监控采集网络内网抓取）
	router.GET("/metrics", engineMetrics.Handler())

	restAddr := cfg.RESTAddress()
	var lis net.Listener
	if !tlsutil.IsTLCPEnabled("AGENT_TLS_NATIONAL_CIPHER") {
		var err error
		lis, err = net.Listen("tcp", restAddr)
		if err != nil {
			return nil, fmt.Errorf("REST listen failed on %s: %w", restAddr, err)
		}
		restAddr = lis.Addr().String()
	}

	server := &http.Server{
		Addr:         restAddr,
		Handler:      router,
		ReadTimeout:  30 * time.Second,  // 防 Slowloris 慢速读攻击
		WriteTimeout: 30 * time.Second,  // 防长连接悬挂
		IdleTimeout:  120 * time.Second, // 空闲连接保活上限
	}

	return &RESTServerRunner{
		server:   server,
		listener: lis,
		addr:     restAddr,
		cfg:      cfg,
	}, nil
}

// Start 启动 REST 服务监听（阻塞直至服务发生不可逆错误或被 Shutdown 关闭）。
//
// 协议自适应选择机制：
//  1. TLCP 国密模式（AGENT_TLS_NATIONAL_CIPHER=true）：根据 GM/T 0024 标准，绑定签名证书与加密证书双证书监听；
//  2. 标准 TLS 模式（AGENT_TLS_ENABLED=true）：启用标准 HTTPS 协议（TLS 1.2 / TLS 1.3）；
//  3. 明文 HTTP 模式：仅在本地环回地址 (127.0.0.1) 或安全受控集群内部启用。
func (r *RESTServerRunner) Start() error {
	if tlsutil.IsTLCPEnabled("AGENT_TLS_NATIONAL_CIPHER") {
		tlcpCfg := tlsutil.TLCPConfigFromEnv("AGENT_")
		gmtlsConfig, tlcpErr := tlsutil.BuildTLCPConfig(tlcpCfg)
		if tlcpErr != nil {
			return fmt.Errorf("failed to build TLCP config: %w", tlcpErr)
		}
		tlcpLis, tlcpErr := tlsutil.NewTLCPListener("tcp", r.addr, gmtlsConfig)
		if tlcpErr != nil {
			return fmt.Errorf("failed to create TLCP listener on %s: %w", r.addr, tlcpErr)
		}
		slog.Info("REST TLCP (国密双证书) server starting", "addr", r.addr, "sign_cert", tlcpCfg.SignCertFile)
		if err := r.server.Serve(tlcpLis); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("REST TLCP server terminated with error: %w", err)
		}
		return nil
	}

	if r.cfg.TLSEnabled {
		slog.Info("REST HTTPS server starting", "addr", r.addr, "cert", r.cfg.TLSCertFile)
		if err := r.server.ServeTLS(r.listener, r.cfg.TLSCertFile, r.cfg.TLSKeyFile); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("REST HTTPS server terminated with error: %w", err)
		}
		return nil
	}

	slog.Info("REST HTTP server starting", "addr", r.addr)
	if err := r.server.Serve(r.listener); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("REST HTTP server terminated with error: %w", err)
	}
	return nil
}

// Shutdown 在收到终止信号后执行 REST 服务优雅停机。
// 停止接收新连接，并在 ctx 超时期限内等待在途 HTTP 请求完成处理与响应。
func (r *RESTServerRunner) Shutdown(ctx context.Context) error {
	slog.Info("Shutting down REST server...", "addr", r.addr)
	if err := r.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("REST server shutdown error: %w", err)
	}
	slog.Info("REST server stopped cleanly")
	return nil
}

// Address 返回 REST 服务的实际监听网络地址（Host:Port）。
func (r *RESTServerRunner) Address() string {
	return r.addr
}
