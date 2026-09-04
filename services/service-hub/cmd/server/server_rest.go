// Command server is the entry point for the service-hub module.
// 本文件实现 service-hub 南北向边缘 REST (Gin) 服务模块的构建、中间件编排、
// TLCP/TLS/HTTP 自适应监听与优雅停机（仿照 privshield-agent 的 RESTServerRunner 模式）。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
	"github.com/fengzhizi319/PrivShield-go/pkg/middleware"
	"github.com/fengzhizi319/PrivShield-go/pkg/tlsutil"

	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/config"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/handlers"
)

// ─────────────────────────────────────────────────────────────────────────────
// REST 服务运行实体与生命周期管理
// ─────────────────────────────────────────────────────────────────────────────

// RESTServerRunner 封装基于 Gin 的 REST HTTP/HTTPS/TLCP (国密) 服务的生命周期管理。
// 负责：
//  1. Gin 引擎装配：可信代理校验、IP 白名单、handlers.RegisterRoutes 中间件漏斗与业务路由；
//  2. 生产级 http.Server 超时参数（Slowloris 防护）；
//  3. 可选 TLS/mTLS 服务端配置构建（TLS 1.3 最低版本、客户端证书校验、SPKI Pinning）；
//  4. 自适应三种传输模式：GM/T 0024 国密 TLCP 双证书、标准 TLS 1.2/1.3、明文 HTTP；
//  5. 基于 context 的优雅停机。
type RESTServerRunner struct {
	server      *http.Server
	cfg         *config.Config
	addr        string
	logger      *slog.Logger
	authEnabled bool
}

// newRESTServerRunner 构造 REST 服务运行实体并完成路由、中间件管道与 TLS 配置装配。
// 若 TLS 配置构建失败直接返回错误，由 main 快速失败终止进程。
func newRESTServerRunner(cfg *config.Config, server *handlers.Server, keyStore *pkgauth.KeyStore, logger *slog.Logger) (*RESTServerRunner, error) {
	// 1) 锁定 Gin 为生产发布模式（ReleaseMode）；
	// 2) 初始化无默认中间件的 Gin 引擎，并通过 RegisterRoutes 挂载通用中间件链（RequestID、Logger、Recovery、CORS、Auth）；
	// 3) 显式配置 http.Server 网络超时参数，防范 Slowloris 慢速连接拒绝服务攻击。
	gin.SetMode(gin.ReleaseMode)
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

	// 当启用 TLS 时，为 HTTP 服务器构建 TLS 配置，支持 mTLS 双向认证：
	// - TLS 1.3 强制最低版本；
	// - 可选客户端证书认证（require/verify/request）；
	// - 可选公钥固定（SPKI Pinning）。
	if cfg.TLSEnabled {
		tlsCfg := &tlsutil.ServerTLSConfig{
			Enabled:          cfg.TLSEnabled,
			CertFile:         cfg.TLSCertFile,
			KeyFile:          cfg.TLSKeyFile,
			CAFile:           cfg.TLSCAFile,
			ClientAuth:       cfg.TLSClientAuth,
			PinnedPubKeyFile: cfg.TLSPinnedPubKeyFile,
		}
		httpTLSConfig, tlsErr := tlsutil.BuildServerTLSConfig(tlsCfg)
		if tlsErr != nil {
			return nil, fmt.Errorf("failed to build HTTP TLS config: %w", tlsErr)
		}
		httpSrv.TLSConfig = httpTLSConfig
		logger.Info("HTTP REST server configured with mTLS",
			"client_auth", cfg.TLSClientAuth,
			"tls_cert", cfg.TLSCertFile,
		)
	}

	return &RESTServerRunner{
		server:      httpSrv,
		cfg:         cfg,
		addr:        cfg.Address(),
		logger:      logger,
		authEnabled: cfg.APIKey != "" || len(cfg.ScopeKeys) > 0 || keyStore != nil,
	}, nil
}

// Start 启动 REST 服务监听（阻塞直至服务发生不可逆错误或被 Shutdown 关闭）。
//
// 协议自适应选择机制：
//  1. TLCP 国密模式（SERVICE_HUB_TLS_NATIONAL_CIPHER=true）：根据 GM/T 0024 标准，绑定签名证书与加密证书双证书监听；
//  2. 标准 TLS 模式（SERVICE_HUB_TLS_ENABLED=true）：启用标准 HTTPS 协议（TLS 1.2 / TLS 1.3）；
//  3. 明文 HTTP 模式：仅在本地环回地址或安全受控集群内部启用。
//
// TLCP 配置/监听器构建失败返回错误（main 快速失败）；Serve 期错误仅记录日志（与原行为一致）。
func (r *RESTServerRunner) Start() error {
	if tlsutil.IsTLCPEnabled("SERVICE_HUB_TLS_NATIONAL_CIPHER") {
		tlcpCfg := tlsutil.TLCPConfigFromEnv("SERVICE_HUB_")
		gmtlsConfig, tlcpErr := tlsutil.BuildTLCPConfig(tlcpCfg)
		if tlcpErr != nil {
			return fmt.Errorf("failed to build TLCP config: %w", tlcpErr)
		}
		tlcpLis, tlcpErr := tlsutil.NewTLCPListener("tcp", r.addr, gmtlsConfig)
		if tlcpErr != nil {
			return fmt.Errorf("failed to create TLCP listener: %w", tlcpErr)
		}
		r.logger.Info("service-hub TLCP (国密) REST server started",
			"addr", r.addr,
			"sign_cert", tlcpCfg.SignCertFile,
		)
		if err := r.server.Serve(tlcpLis); err != nil && err != http.ErrServerClosed {
			r.logger.Error("TLCP server error", "error", err.Error())
		}
		return nil
	}

	if r.cfg.TLSEnabled {
		r.logger.Info("service-hub HTTPS REST server started (mTLS enabled)",
			"addr", r.addr,
			"grpc_addr", r.cfg.GRPCAddress(),
			"agent_rest", r.cfg.AgentBaseURL(),
			"datasource_rest", r.cfg.DatasourceBaseURL(),
			"db_path", r.cfg.DBPath,
			"auth_enabled", r.authEnabled,
			"mtls_client_auth", r.cfg.TLSClientAuth,
		)
		// ListenAndServeTLS 使用 httpSrv.TLSConfig 中的证书，空字符串表示从 TLSConfig 读取
		if err := r.server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			r.logger.Error("HTTPS server error", "error", err.Error())
		}
		return nil
	}

	r.logger.Info("service-hub HTTP REST server started",
		"addr", r.addr,
		"grpc_addr", r.cfg.GRPCAddress(),
		"agent_rest", r.cfg.AgentBaseURL(),
		"datasource_rest", r.cfg.DatasourceBaseURL(),
		"db_path", r.cfg.DBPath,
		"auth_enabled", r.authEnabled,
	)
	if err := r.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		r.logger.Error("HTTP server error", "error", err.Error())
	}
	return nil
}

// Shutdown 在收到终止信号后执行 REST 服务优雅停机。
// 停止接收新连接，并在 ctx 超时期限内等待在途 HTTP 请求完成处理与响应。
func (r *RESTServerRunner) Shutdown(ctx context.Context) error {
	if err := r.server.Shutdown(ctx); err != nil {
		r.logger.Error("HTTP server shutdown error", "error", err.Error())
		return err
	}
	r.logger.Info("HTTP server stopped")
	return nil
}

// Address 返回 REST 服务的监听网络地址（Host:Port）。
func (r *RESTServerRunner) Address() string {
	return r.addr
}
