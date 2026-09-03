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
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
	pkgcrypto "github.com/fengzhizi319/PrivShield-go/pkg/crypto"
	pkggrpcserver "github.com/fengzhizi319/PrivShield-go/pkg/grpcserver"
	"github.com/fengzhizi319/PrivShield-go/pkg/metrics"
	"github.com/fengzhizi319/PrivShield-go/pkg/middleware"
	"github.com/fengzhizi319/PrivShield-go/pkg/naming"
	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
	"github.com/fengzhizi319/PrivShield-go/pkg/store"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/flusher"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/memory"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/postgres"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/sqlite"
	"github.com/fengzhizi319/PrivShield-go/pkg/tlsutil"

	"github.com/fengzhizi319/PrivShield-go/services/audit-log/internal/agent"
	"github.com/fengzhizi319/PrivShield-go/services/audit-log/internal/archive"
	"github.com/fengzhizi319/PrivShield-go/services/audit-log/internal/config"
	"github.com/fengzhizi319/PrivShield-go/services/audit-log/internal/grpcserver"
	"github.com/fengzhizi319/PrivShield-go/services/audit-log/internal/handlers"
	pb "github.com/fengzhizi319/PrivShield-go/services/audit-log/proto"
)

func main() {
	cfg := config.Load()

	// Validate configuration consistency (fail-fast with clear error messages).
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	// ── Envelope encryption key registry / 信封加密密钥版本注册（G-08）──
	// 多版本密钥轮换：从 PRIVACY_CRYPTO_KEY_<VERSION> 环境变量注册所有版本，
	// PRIVACY_CRYPTO_ACTIVE_VERSION 指定当前活跃版本（用于加密写入）。
	// 若未配置多版本环境变量，回退到配置主密钥注册为 v1。
	if n := pkgcrypto.RegisterKeyVersionsFromEnv("AUDIT_LOG_CRYPTO_"); n > 0 {
		log.Printf("envelope encryption key versions registered from env (count=%d, active=%s)",
			n, os.Getenv("AUDIT_LOG_CRYPTO_ACTIVE_VERSION"))
	} else if cfg.EncryptionKey != "" {
		pkgcrypto.RegisterKeyVersion("v1", []byte(cfg.EncryptionKey), true)
		log.Printf("envelope encryption key registered (version=v1, active=true)")
	}

	// ── SM2 审计存证签名器/验签器注册（G-10 不可否认性）──
	if cfg.SM2PrivateKey != "" || cfg.SM2PublicKey != "" {
		sv, err := pkgcrypto.NewSM2SignerVerifierFromHex(cfg.SM2PrivateKey, cfg.SM2PublicKey)
		if err != nil {
			log.Fatalf("failed to load SM2 signing key: %v", err)
		}
		store.SetSM2Signer(sv)
		store.SetSM2Verifier(sv)
		log.Printf("audit SM2 signer/verifier registered (has_private=%v, has_public=%v)",
			cfg.SM2PrivateKey != "", cfg.SM2PublicKey != "")
	}

	// ── Structured logger / 结构化日志 ────────────────────────
	pkgobs.InitLogger(cfg.LogFormat, cfg.LogLevel)
	logger := slog.Default()

	// ── API Key 文件热轮转（K8s Secret 投影场景）───────────────
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

	// ── Keyed evidence chain / 密钥化存证哈希（P1-2）───────────────
	// 存证哈希密钥由局方托管注入；未注入时退回无密钥 SM3，只能证明「未被误改」，
	// 无法阻止知悉前映像口径者重算哈希，因此生产环境必须配置 AUDIT_LOG_HASH_KEY。
	store.SetAuditChainKey(cfg.HashKey)
	if store.AuditChainKey() == "" {
		logger.Warn("evidence hash chain runs un-keyed (SM3); set AUDIT_LOG_HASH_KEY to the 局方托管 secret so records become forge-proof",
			"hash_algo", store.ComputeAuditIntegrityHashAlgo())
	} else {
		logger.Info("evidence hash chain keyed", "hash_algo", store.ComputeAuditIntegrityHashAlgo())
	}

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

	// ── Data Retention Cleanup / 存证留存清理协程 ────────────────
	// 每 6 小时执行一次「先归档后删除」：到期存证先写成加密且可独立验真的归档段，再删除。
	// RetentionDays=0（默认）永不清理，存证证据始终留在库内。
	retentionCtx, retentionCancel := context.WithCancel(context.Background())
	if cfg.RetentionDays > 0 {
		go auditRetentionLoop(retentionCtx, auditStore, logger, cfg)
	}

	// ── HTTP REST Server / HTTP REST 服务器 ──────────────────────
	gin.SetMode(gin.ReleaseMode)
	server := handlers.New(agentClient, cfg, keyStore, auditStore, logger, mc)
	router := gin.New()
	middleware.ConfigureTrustedProxies(router, middleware.TrustedProxiesFromEnv("AUDIT_LOG_TRUSTED_PROXIES")) // G-02
	router.Use(middleware.IPAllowlist(middleware.AllowedCIDRsFromEnv("AUDIT_LOG_ALLOWED_CIDRS")))             // IP access control
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
		serviceImpl = grpcserver.New(agentClient, cfg, auditStore, logger)
		grpcServer.RegisterService(&pb.AuditLogService_ServiceDesc, serviceImpl)
		logger.Info("gRPC server started with mTLS",
			"addr", cfg.GRPCAddress(),
			"tls_cert", cfg.TLSCertFile,
			"tls_key", cfg.TLSKeyFile,
		)
	} else {
		serviceImpl = grpcserver.New(agentClient, cfg, auditStore, logger)
		grpcServer.RegisterService(&pb.AuditLogService_ServiceDesc, serviceImpl)
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
		"auth_enabled", cfg.APIKey != "" || cfg.ReaderAPIKey != "" || len(cfg.ScopeKeys) > 0 || keyStore != nil,
		"cors_origins", len(cfg.CORSOrigins),
		"db_path", cfg.DBPath,
		"retention_days", cfg.RetentionDays,
		"shutdown_timeout", cfg.ShutdownTimeout,
		"log_format", cfg.LogFormat,
		"log_level", cfg.LogLevel,
	)

	// Emit a prominent security warning when all protections are disabled.
	// 当所有安全功能均未启用时输出醒目警告，防止生产环境意外裸奔。
	if !cfg.TLSEnabled && cfg.APIKey == "" && cfg.ReaderAPIKey == "" && len(cfg.ScopeKeys) == 0 && keyStore == nil {
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
		if err := grpcServer.ServeListener(grpcLis); err != nil {
			logger.Error("gRPC server error", "error", err.Error())
		}
	}()

	// Start HTTP server / 启动 HTTP 监听
	go func() {
		if tlsutil.IsTLCPEnabled("AUDIT_LOG_TLS_NATIONAL_CIPHER") {
			tlcpCfg := tlsutil.TLCPConfigFromEnv("AUDIT_LOG_")
			gmtlsConfig, tlcpErr := tlsutil.BuildTLCPConfig(tlcpCfg)
			if tlcpErr != nil {
				log.Fatalf("failed to build TLCP config: %v", tlcpErr)
			}
			tlcpLis, tlcpErr := tlsutil.NewTLCPListener("tcp", cfg.Address(), gmtlsConfig)
			if tlcpErr != nil {
				log.Fatalf("failed to create TLCP listener: %v", tlcpErr)
			}
			logger.Info("audit-log TLCP (国密) REST server started",
				"addr", cfg.Address(),
				"sign_cert", tlcpCfg.SignCertFile,
			)
			if err := httpSrv.Serve(tlcpLis); err != nil && err != http.ErrServerClosed {
				logger.Error("TLCP server error", "error", err.Error())
			}
		} else if cfg.TLSEnabled {
			logger.Info("audit-log HTTPS REST server started (TLS enabled)",
				"addr", cfg.Address(),
				"grpc_addr", cfg.GRPCAddress(),
				"agent_rest", cfg.AgentBaseURL(),
				"db_path", cfg.DBPath,
				"auth_enabled", cfg.APIKey != "" || cfg.ReaderAPIKey != "" || len(cfg.ScopeKeys) > 0 || keyStore != nil,
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
				"auth_enabled", cfg.APIKey != "" || cfg.ReaderAPIKey != "" || len(cfg.ScopeKeys) > 0 || keyStore != nil,
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

// auditRetentionLoop periodically archives and then deletes audit logs older than retentionDays.
// auditRetentionLoop 周期性执行「先归档后删除」的存证留存清理：早于保留期的存证先以
// 加密 + SM3 行哈希链的可验真归档段落盘并回读验真，随后才按归档批次精确删除。
// 归档或验真任一失败即中止删除（fail-closed），存证证据不会静默丢失。
func auditRetentionLoop(ctx context.Context, auditStore store.AuditStore, logger *slog.Logger, cfg *config.Config) {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	logger.Info("audit evidence retention cleanup started",
		"retention_days", cfg.RetentionDays,
		"interval_hours", 6,
		"archive_dir", cfg.ArchiveDir,
	)

	runOnce := func() {
		cutoff := time.Now().AddDate(0, 0, -cfg.RetentionDays)
		archiver, err := archive.New(archive.Options{
			ArchiveDir:    cfg.ArchiveDir,
			EncryptionKey: cfg.EncryptionKey,
			PageSize:      cfg.ArchivePageSize,
		}, logger)
		if err != nil {
			logger.Error("retention: archive guard unavailable, deletion refused", "error", err.Error())
			return
		}
		stats, err := archiver.ArchiveAndCleanup(auditStore, cutoff)
		if err != nil {
			var archived int64
			var segments int
			if stats != nil {
				archived, segments = stats.LogsArchived, len(stats.Segments)
			}
			logger.Error("retention: archive-before-delete failed, deletion stopped",
				"error", err.Error(),
				"logs_archived", archived,
				"segments", segments,
			)
			return
		}
		if stats.LogsArchived > 0 {
			logger.Info("retention: expired evidence archived then deleted",
				"logs_archived", stats.LogsArchived,
				"snapshots_archived", stats.SnapshotsArchived,
				"logs_deleted", stats.LogsDeleted,
				"segments", strings.Join(stats.Segments, ","),
			)
		}
	}

	runOnce()

	for {
		select {
		case <-ctx.Done():
			logger.Info("audit data retention cleanup stopped")
			return
		case <-ticker.C:
			runOnce()
		}
	}
}

func initAuditStore(cfg *config.Config, logger *slog.Logger) (store.AuditStore, error) {
	var underlying store.AuditStore

	// 1. Try PostgreSQL Phase B if DSN is configured (with short probe timeout)
	if cfg.PGDSN != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		pgStore, err := postgres.NewAuditStore(ctx, postgres.Config{
			DSN:     cfg.PGDSN,
			MaxConn: int32(cfg.PGMaxConn),
			MinConn: int32(cfg.PGMinConn),
		}, logger)
		if err == nil {
			logger.Info("postgresql audit store initialized (Phase B)")
			underlying = pgStore
			if cfg.DBWriteOnly {
				if err := verifyWriteOnlyPostgres(ctx, pgStore, logger); err != nil {
					_ = pgStore.Close()
					return nil, err
				}
			}
		} else {
			if cfg.StrictStorage {
				return nil, fmt.Errorf("strict storage mode: PostgreSQL connection probe failed: %w", err)
			}
			logger.Warn("PostgreSQL connection probe failed, falling back to SQLite / in-memory store", "error", err.Error())
		}
	}

	// 2. Fallback to SQLite if PG was not used/failed and DBPath is set
	if underlying == nil && cfg.DBPath != "" {
		if cfg.DBWriteOnly {
			return nil, fmt.Errorf("AUDIT_LOG_DB_WRITE_ONLY requires a PostgreSQL write-only role; SQLite file permissions cannot be self-checked, disable the flag or switch to PostgreSQL")
		}
		if err := sqlite.ValidateIntegrity(cfg.DBPath); err != nil {
			if cfg.StrictStorage {
				return nil, fmt.Errorf("strict storage mode: SQLite %s failed its integrity check, refusing to start or silently rebuild it: %w", cfg.DBPath, err)
			}
			logger.Error("sqlite integrity check failed; the database will be recreated and previously stored evidence is at risk",
				"path", cfg.DBPath, "error", err.Error())
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
	bufferedStore := flusher.NewBufferedAuditStore(underlying, flusherConfigFrom(cfg), logger)
	return bufferedStore, nil
}

// flusherConfigFrom 以 flusher.DefaultConfig() 为基线，仅覆盖显式配置为正的参数。
func flusherConfigFrom(cfg *config.Config) flusher.Config {
	fc := flusher.DefaultConfig()
	if cfg.FlushBatchSize > 0 {
		fc.MaxBatchSize = cfg.FlushBatchSize
	}
	if cfg.FlushQueueSize > 0 {
		fc.BufferSize = cfg.FlushQueueSize
	}
	if cfg.FlushIntervalMs > 0 {
		fc.FlushInterval = time.Duration(cfg.FlushIntervalMs) * time.Millisecond
	}
	if cfg.FlushEnqueueTimeoutMs > 0 {
		fc.EnqueueTimeout = time.Duration(cfg.FlushEnqueueTimeoutMs) * time.Millisecond
	}
	if cfg.FlushMaxStaged > 0 {
		fc.MaxStaged = cfg.FlushMaxStaged
	}
	if cfg.FlushCloseTimeoutMs > 0 {
		fc.CloseTimeout = time.Duration(cfg.FlushCloseTimeoutMs) * time.Millisecond
	}
	return fc
}

// verifyWriteOnlyPostgres 自检存证库连接角色是否为「只写不可改删」账号（P1-6）。
// 审计表一旦被授予 UPDATE/DELETE 权限，链式存证即可被事后改写，因此自检失败即拒绝启动。
// 前置条件：表结构由 DBA 预先执行 deploy/sql/audit_writeonly_role.sql 建好。
func verifyWriteOnlyPostgres(ctx context.Context, pgStore *postgres.AuditStore, logger *slog.Logger) error {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	granted := make([]string, 0, 4)
	for _, table := range []string{"audit_logs", "snapshots"} {
		for _, priv := range []string{"UPDATE", "DELETE"} {
			ok, err := pgStore.HasTablePrivilege(probeCtx, table, priv)
			if err != nil {
				return fmt.Errorf("write-only self-check failed: %w", err)
			}
			if ok {
				granted = append(granted, table+" "+priv)
			}
		}
	}
	if len(granted) > 0 {
		return fmt.Errorf("AUDIT_LOG_DB_WRITE_ONLY=true but the connected role still holds %s; grant INSERT/SELECT only (see deploy/sql/audit_writeonly_role.sql)",
			strings.Join(granted, ", "))
	}
	logger.Info("audit store write-only self-check passed", "insert_only_role", true)
	return nil
}
