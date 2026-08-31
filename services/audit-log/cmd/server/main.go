// Command server is the entry point for the audit-log module.
// Command server 是脱敏审计日志与存证模块的程序入口。
//
// Architecture / 架构：
//
//	React 前端  ──HTTP/JSON──▶  audit-log(Go)  ──HTTP──▶  PrivShield Agent
//	                          └─gRPC(mTLS)───▶  调度中枢/外部客户端
package main

import (
	"context"
	"fmt"
	"io"
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

	pkgconfig "github.com/fengzhizi319/PrivShield/pkg/config"
	"github.com/fengzhizi319/PrivShield/pkg/metrics"
	"github.com/fengzhizi319/PrivShield/pkg/naming"
	"github.com/fengzhizi319/PrivShield/pkg/store"
	"github.com/fengzhizi319/PrivShield/pkg/store/flusher"
	"github.com/fengzhizi319/PrivShield/pkg/store/memory"
	"github.com/fengzhizi319/PrivShield/pkg/store/postgres"
	"github.com/fengzhizi319/PrivShield/pkg/store/sqlite"
	"github.com/fengzhizi319/PrivShield/pkg/tlsutil"

	"github.com/fengzhizi319/PrivShield/services/audit-log/internal/agent"
	"github.com/fengzhizi319/PrivShield/services/audit-log/internal/config"
	"github.com/fengzhizi319/PrivShield/services/audit-log/internal/grpcserver"
	"github.com/fengzhizi319/PrivShield/services/audit-log/internal/handlers"
	pb "github.com/fengzhizi319/PrivShield/services/audit-log/proto"
)

func main() {
	cfg := config.Load()

	// Validate configuration consistency (fail-fast with clear error messages).
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	// ── Structured logger / 结构化日志 ────────────────────────
	logger := pkgconfig.SetupLogger(cfg.LogFormat, cfg.LogLevel)

	// ── SQLite Integrity Check / SQLite 完整性校验 ──────────────
	// 启动时校验 SQLite 数据库完整性，检测损坏并阻止服务启动。
	// 使用共享库 sqlite.ValidateIntegrity() 统一实现，避免各模块重复代码。
	if cfg.PGDSN == "" && cfg.DBPath != "" {
		if err := sqlite.ValidateIntegrity(cfg.DBPath); err != nil {
			log.Fatalf("sqlite integrity check failed: %v", err)
		}
		logger.Info("database integrity check passed", "path", cfg.DBPath)
	}

	// ── Audit store / 审计存储 ─────────────────────────────────
	auditStore, err := initAuditStore(cfg, logger)
	if err != nil {
		log.Fatalf("failed to initialize audit store: %v", err)
	}

	// ── Prometheus metrics / Prometheus 指标 ───────────────────
	mc := metrics.NewCollector("audit-log")
	// 注册命名观测器：pkg/naming 归一化时自动上报别名使用 / 脏 ID 指标（§7.2）。
	naming.SetObserver(mc)

	// ── Agent client / Agent 客户端 ────────────────────────────
	agentClient := agent.New(cfg)

	// ── Data Retention Cleanup / 数据保留清理协程 ───────────────
	// 启动后台协程，每 6 小时扫描并清理超过保留期的审计日志，防止 SQLite 无限膨胀。
	// RetentionDays=0 时禁用清理（适用于调试或短期部署）。
	retentionCtx, retentionCancel := context.WithCancel(context.Background())
	if cfg.RetentionDays > 0 {
		go auditRetentionLoop(retentionCtx, auditStore, logger, cfg.RetentionDays)
	}

	// ── HTTP REST Server / HTTP REST 服务器 ──────────────────────
	gin.SetMode(gin.ReleaseMode)
	server := handlers.New(agentClient, cfg, auditStore, logger, mc)
	router := gin.New()
	server.RegisterRoutes(router)

	httpSrv := &http.Server{
		Addr:              cfg.Address(),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,  // Slowloris header timeout
		ReadTimeout:       30 * time.Second, // Slow request body timeout
		WriteTimeout:      60 * time.Second, // Slow client response timeout
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB max header size
	}

	// ── HTTP TLS / HTTPS 配置 ───────────────────────────────────
	// 与 service-hub/datasource-mgr 对齐：当配置了 TLS 证书时，HTTP 也启用 HTTPS。
	if cfg.TLSEnabled {
		httpTLSConfig, err := grpcserver.BuildServerTLSConfig(cfg)
		if err != nil {
			log.Fatalf("failed to build TLS config for HTTP server: %v", err)
		}
		httpSrv.TLSConfig = httpTLSConfig
	}

	// ── gRPC Server (with optional mTLS) / gRPC 服务器（可选 mTLS）──
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

	// mTLS CN whitelist authorization for inbound gRPC connections.
	if cfg.MTLSWhitelistFile != "" {
		unaryInterceptor, streamInterceptor, dw, err := tlsutil.NewWhitelistInterceptor(cfg.MTLSWhitelistFile)
		if err != nil {
			log.Fatalf("failed to load mTLS whitelist: %v", err)
		}
		defer dw.Close()
		grpcServerOpts = append(grpcServerOpts,
			grpc.UnaryInterceptor(unaryInterceptor),
			grpc.StreamInterceptor(streamInterceptor),
		)
		logger.Info("gRPC server configured with mTLS CN whitelist",
			"path", cfg.MTLSWhitelistFile,
		)
	}

	if cfg.TLSEnabled {
		creds, credErr := grpcserver.BuildServerCredentials(cfg)
		if credErr != nil {
			log.Fatalf("failed to build TLS credentials: %v", credErr)
		}
		grpcServer = grpc.NewServer(append(grpcServerOpts, grpc.Creds(creds))...)
		serviceImpl = grpcserver.New(agentClient, cfg, auditStore, logger)
		pb.RegisterAuditLogServiceServer(grpcServer, serviceImpl)
		logger.Info("gRPC server started with mTLS",
			"addr", cfg.GRPCAddress(),
			"tls_cert", cfg.TLSCertFile,
			"tls_key", cfg.TLSKeyFile,
		)
	} else {
		grpcServer = grpc.NewServer(grpcServerOpts...)
		serviceImpl = grpcserver.New(agentClient, cfg, auditStore, logger)
		pb.RegisterAuditLogServiceServer(grpcServer, serviceImpl)
		logger.Info("gRPC server started (insecure)", "addr", cfg.GRPCAddress())
	}

	// ── Startup Config Summary / 启动配置摘要横幅 ─────────────────
	// Log key configuration flags at startup so operators can verify the
	// security posture and runtime parameters at a glance.
	// 启动时记录关键配置摘要，便于运维确认服务状态与安全姿态。
	logger.Info("audit-log startup configuration",
		"http_addr", cfg.Address(),
		"grpc_addr", cfg.GRPCAddress(),
		"agent_rest", fmt.Sprintf("http://%s:%d", cfg.AgentRESTHost, cfg.AgentRESTPort),
		"tls_enabled", cfg.TLSEnabled,
		"auth_enabled", cfg.APIKey != "",
		"cors_origins", len(cfg.CORSOrigins),
		"db_path", cfg.DBPath,
		"retention_days", cfg.RetentionDays,
		"shutdown_timeout", cfg.ShutdownTimeout,
		"log_format", cfg.LogFormat,
		"log_level", cfg.LogLevel,
	)

	// Emit a prominent security warning when all protections are disabled.
	// 当所有安全功能均未启用时输出醒目警告，防止生产环境意外裸奔。
	if !cfg.TLSEnabled && cfg.APIKey == "" {
		logger.Warn("========================================================================\n" +
			"  SECURITY WARNING: All security features are DISABLED.\n" +
			"  TLS=off  Auth=off\n" +
			"  All endpoints are exposed without encryption or authentication.\n" +
			"  For production deployments, set:\n" +
			"    AUDIT_LOG_TLS_ENABLED=true\n" +
			"    AUDIT_LOG_API_KEY=<your-key>\n" +
			"  See docs/production_security/ops.md for details.\n" +
			"========================================================================")
	}

	// ── Signal handling / 信号处理 ───────────────────────────────
	// 使用 signal.NotifyContext（Go 1.16+）替代传统的 signal.Notify + channel 模式，
	// 信号到达时自动取消 context，与下游协程的 ctx.Done() 无缝衔接。
	sigCtx, sigStop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer sigStop()

	// Start gRPC listener / 启动 gRPC 监听
	grpcLis, err := net.Listen("tcp", cfg.GRPCAddress())
	if err != nil {
		log.Fatalf("failed to listen on gRPC address %s: %v", cfg.GRPCAddress(), err)
	}

	go func() {
		if err := grpcServer.Serve(grpcLis); err != nil {
			logger.Error("gRPC server error", "error", err.Error())
		}
	}()

	// Start HTTP server / 启动 HTTP 监听
	go func() {
		if cfg.TLSEnabled {
			logger.Info("audit-log HTTPS REST server started (TLS enabled)",
				"addr", cfg.Address(),
				"grpc_addr", cfg.GRPCAddress(),
				"agent_rest", cfg.AgentBaseURL(),
				"db_path", cfg.DBPath,
				"auth_enabled", cfg.APIKey != "",
				"retention_days", cfg.RetentionDays,
			)
			if err := httpSrv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				logger.Error("HTTPS server error", "error", err.Error())
			}
		} else {
			logger.Info("audit-log HTTP REST server started",
				"addr", cfg.Address(),
				"grpc_addr", cfg.GRPCAddress(),
				"agent_rest", cfg.AgentBaseURL(),
				"db_path", cfg.DBPath,
				"auth_enabled", cfg.APIKey != "",
				"retention_days", cfg.RetentionDays,
			)
			if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("HTTP server error", "error", err.Error())
			}
		}
	}()

	// Wait for shutdown signal / 等待优雅停机信号
	<-sigCtx.Done()
	logger.Info("shutting down audit-log servers...")

	// Stop data retention cleanup goroutine / 停止数据保留清理协程
	retentionCancel()

	// Graceful shutdown gRPC
	serviceImpl.Shutdown()
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

	// Graceful shutdown HTTP（超时时间可配置）
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ShutdownTimeout)*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err.Error())
	} else {
		logger.Info("HTTP server stopped")
	}

	// Flush and close buffered batch audit store / 优雅关闭微批缓冲刷盘器
	if buf, ok := auditStore.(*flusher.BufferedAuditStore); ok {
		if err := buf.Close(); err != nil {
			logger.Error("buffered audit store close error", "error", err.Error())
		}
	} else if closer, ok := auditStore.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			logger.Error("audit store close error", "error", err.Error())
		}
	}
}

// auditRetentionLoop periodically deletes audit logs older than retentionDays.
// auditRetentionLoop 周期性删除超过保留期的审计日志及其关联快照，防止 SQLite 无限膨胀。
func auditRetentionLoop(ctx context.Context, auditStore store.AuditStore, logger *slog.Logger, retentionDays int) {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	logger.Info("audit data retention cleanup started", "retention_days", retentionDays, "interval_hours", 6)

	// Run once immediately on startup / 启动时立即执行一次
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	if deleted, err := auditStore.CleanupOld(cutoff); err != nil {
		logger.Error("audit retention cleanup failed", "error", err.Error())
	} else if deleted > 0 {
		logger.Info("audit retention cleanup completed", "deleted_logs", deleted, "retention_days", retentionDays)
	}

	for {
		select {
		case <-ctx.Done():
			logger.Info("audit data retention cleanup stopped")
			return
		case <-ticker.C:
			cutoff := time.Now().AddDate(0, 0, -retentionDays)
			deleted, err := auditStore.CleanupOld(cutoff)
			if err != nil {
				logger.Error("audit retention cleanup failed", "error", err.Error())
			} else if deleted > 0 {
				logger.Info("audit retention cleanup completed", "deleted_logs", deleted, "retention_days", retentionDays)
			}
		}
	}
}

func initAuditStore(cfg *config.Config, logger *slog.Logger) (store.AuditStore, error) {
	var underlying store.AuditStore

	// 1. Try PostgreSQL Phase B if DSN is configured (with short probe timeout)
	if cfg.PGDSN != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		pgStore, err := postgres.NewAuditStore(ctx, postgres.Config{DSN: cfg.PGDSN}, logger)
		if err == nil {
			logger.Info("postgresql audit store initialized (Phase B)")
			underlying = pgStore
		} else {
			if cfg.StrictStorage {
				return nil, fmt.Errorf("strict storage mode: PostgreSQL connection probe failed: %w", err)
			}
			logger.Warn("PostgreSQL connection probe failed, falling back to SQLite / in-memory store", "error", err.Error())
		}
	}

	// 2. Fallback to SQLite if PG was not used/failed and DBPath is set
	if underlying == nil && cfg.DBPath != "" {
		if err := sqlite.ValidateIntegrity(cfg.DBPath); err != nil {
			logger.Warn("sqlite integrity check warning, recreating database", "path", cfg.DBPath, "error", err.Error())
		}
		db, err := sqlite.Open(cfg.DBPath, logger)
		if err == nil {
			as, err := sqlite.NewAuditStore(db)
			if err == nil {
				logger.Info("sqlite audit store initialized", "path", cfg.DBPath)
				underlying = as
			} else {
				db.Close()
				if cfg.StrictStorage {
					return nil, fmt.Errorf("strict storage mode: SQLite audit store initialization failed: %w", err)
				}
				logger.Warn("sqlite audit store initialization failed, falling back to in-memory", "error", err.Error())
			}
		} else {
			if cfg.StrictStorage {
				return nil, fmt.Errorf("strict storage mode: open SQLite failed: %w", err)
			}
			logger.Warn("open sqlite failed, falling back to in-memory", "error", err.Error())
		}
	}

	// 3. Fallback to in-memory store
	if underlying == nil {
		if cfg.StrictStorage {
			return nil, fmt.Errorf("strict storage mode enabled: no persistent storage configured or available")
		}
		logger.Warn("using in-memory audit store (volatile / non-persistent)")
		underlying = memory.NewAuditStore()
	}

	// 4. Wrap with micro-batch flusher for high-throughput concurrency
	flusherCfg := flusher.DefaultConfig()
	bufferedStore := flusher.NewBufferedAuditStore(underlying, flusherCfg, logger)
	return bufferedStore, nil
}
