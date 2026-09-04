// Command server is the entry point for the service-hub module.
// Command server 是数据服务调度中枢模块（service-hub）的程序主入口。
//
// ==============================================================================
// Architecture & Traffic Flow / 系统架构与流量拓扑：
// ==============================================================================
//
//	┌────────────────────────┐   HTTP(S) REST (明文 / TLS 1.3 mTLS / TLCP 国密) (:8082)
//	│  React Web UI / BFF-Go │ ────────────────────────────────────────────────────────┐
//	└────────────────────────┘                                                         │
//	                                                                                   ▼
//	┌────────────────────────┐   gRPC (明文 / TLS 1.3 mTLS 双向认证) (:50052)            ┌────────────────────────────────────────┐
//	│ 上游业务系统 / 集群客户端  │ ──────────────────────────────────────────────────────▶ │ service-hub 数据服务调度中枢              │
//	└────────────────────────┘                                                         │ - HTTP(S) REST: :8082 (mTLS/TLCP/明文) │
//	                                                                                   │ - gRPC: :50052 (mTLS/明文)             │
//	                                                                                   │ - 6 阶段流水线调度引擎                     │
//	                                                                                   └───────────────────┬────────────────────┘
//	                                                                                                       │
//	                         ┌─────────────────────────────────┬───────────────────────────────────────────┴───────────────────────┐
//	                         │ HTTP(S) REST / TLCP 国密         │ HTTP(S) REST / gRPC                                               │ HTTP(S) REST / gRPC (mTLS)
//	                         ▼                                 ▼                                                                   ▼
//	        ┌──────────────────────────────────┐      ┌──────────────────────────────────┐                        ┌──────────────────────────────────┐
//	        │ PrivShield Privacy Engine 引擎    │      │ mock-datasource 模拟数据源服务    │                        │ audit-log 脱敏审计存证服务        │
//	        │ - 动态分类分级 /v1/dynclassificatio │      │ - 医保/康养模拟数据 :8083 / :50053 │                        │ - 审计存证与校验 :8084 / :50054  │
//	        │ - 隐私脱敏与K匿名 /v1/privacy       │      └──────────────────────────────────┘                        └──────────────────────────────────┘
//	        └──────────────────────────────────┘
//
// ==============================================================================
// Key Responsibilities / 核心职责：
// ==============================================================================
// 1. 配置与日志加载：从环境变量读取配置并初始化基于 slog 的结构化日志记录器；
// 2. 任务持久化存储初始化：支持纯内存存储（测试/轻量）与 SQLite 持久化存储（生产容灾）；
// 3. Prometheus 指标收集器：初始化请求计数、耗时分布与流水线执行指标；
// 4. 下游客户端组件实例化：创建与 PrivShield Engine、mock-datasource 及 audit-log 通信的客户端（支持重试、熔断与 mTLS/TLCP）；
// 5. 双协议并发服务监听：装配 RESTServerRunner 与 GRPCServerRunner，在独立协程中启动
//    HTTP(S) REST (Gin，支持明文 / TLS 1.3 mTLS 双向认证 / GM/T 0024 TLCP 国密双证书)
//    与 gRPC (支持明文 / 零信任 mTLS 双向认证与公钥固定)；
// 6. 优雅停机收敛：拦截 SIGINT/SIGTERM，先向异步任务协程发送取消信号，再顺序关闭 gRPC 与 HTTP 服务器。
//
// 代码组织（仿照 privshield-agent 的 runner 模式）：
//   - main.go            : 装配流程（配置 → 存储 → 客户端 → runner → 启动 → 信号等待 → 优雅停机编排）
//   - server_rest.go     : RESTServerRunner（Gin 管道、TLCP/TLS/明文三分支 Serve、Shutdown/Address）
//   - server_grpc.go     : GRPCServerRunner（拦截器链、TLS 凭证、服务桩注册、30s 看门狗优雅停机）
//   - store_helpers.go   : 任务存储初始化 / 崩溃恢复 / 自动重试 / 数据保留清理辅助函数
// ==============================================================================

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
	"github.com/fengzhizi319/PrivShield-go/pkg/metrics"
	"github.com/fengzhizi319/PrivShield-go/pkg/naming"
	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/sqlite"

	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/agent"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/config"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/datasource"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/grpcserver"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/handlers"
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
	// - SQLite Integrity Check: 启动时先校验数据库完整性，检测损坏并阻止服务启动，防止带病运行。
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
	// 5. Crash Recovery / 崩溃恢复机制
	// =========================================================================
	// 启动时自动扫描并恢复孤立任务：
	// - pending 任务：直接保留在队列中（尚未执行，无需标记失败）；
	// - running 任务：标记为 failed（可能已部分执行，需重新提交）。
	if err := recoverOrphanedTasks(taskStore, mc, logger); err != nil {
		log.Fatalf("failed to recover orphaned tasks: %v", err)
	}

	// =========================================================================
	// 6. Automatic Task Retry / 失败任务自动重试
	// =========================================================================
	// 启动时自动重试因临时错误（网络超时、连接失败等）而失败的任务。
	// 最多重试 3 次，使用结构化 RetryCount 字段（替代脆弱的字符串匹配）。
	// 重试采用指数退避延迟，避免下游服务仍不可用时立即再次失败。
	retryFailedTasks(taskStore, mc, logger)

	// =========================================================================
	// 7. Periodic Background Retry / 周期性后台重试协程
	// =========================================================================
	// 启动后台协程，每 60 秒扫描一次 failed 任务并自动重试。
	// 解决“运行时失败的任务必须等到下次服务重启才能重试”的问题。
	retryCtx, retryCancel := context.WithCancel(context.Background())
	go periodicRetryLoop(retryCtx, taskStore, mc, logger, 60*time.Second)

	// =========================================================================
	// 8. Periodic Data Retention Cleanup / 周期性数据保留清理协程
	// =========================================================================
	// 启动后台协程，每 6 小时扫描并清理超过保留期的终态任务，防止 SQLite 无限膨胀。
	// RetentionDays=0 时禁用清理（适用于调试或短期部署）。
	retentionCtx, retentionCancel := context.WithCancel(context.Background())
	if cfg.RetentionDays > 0 {
		go dataRetentionLoop(retentionCtx, taskStore, logger, cfg.RetentionDays)
	}

	// =========================================================================
	// 9. Upstream & Downstream Clients Setup / 下游依赖客户端实例化
	// =========================================================================
	// 1) AgentClient: 负责与 PrivShield Privacy Engine 引擎（:8079）通信，调用分类分级与脱敏算子；
	// 2) DatasourceClient: 负责与 mock-datasource 模拟数据源服务（:8083/:50053）交互，采样抽取数据。
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
	// 10. HTTP REST Server Runner / REST 服务运行实体装配
	// =========================================================================
	// 实例化 HTTP 处理器集合（任务分发调度、流水线查询、数据源代理等端点），
	// 并构造 RESTServerRunner：Gin 引擎 + 中间件漏斗 + http.Server 超时 + TLS/mTLS 配置。
	server := handlers.New(agentClient, dsClient, cfg, keyStore, taskStore, logger, mc)
	restRunner, err := newRESTServerRunner(cfg, server, keyStore, logger)
	if err != nil {
		log.Fatalf("%v", err)
	}

	// =========================================================================
	// 11. gRPC Server Runner & Task Worker / gRPC 服务实体装配与任务消费 Worker
	// =========================================================================
	// 构造 GRPCServerRunner：64 MiB 消息上限、Keepalive 保活、API Key + Scope 鉴权拦截器
	// 与可选 mTLS CN 白名单拦截器链、TLS credentials 分支、服务桩注册与 TCP 监听预绑定。
	grpcRunner, err := newGRPCServerRunner(agentClient, dsClient, cfg, taskStore, logger)
	if err != nil {
		log.Fatalf("%v", err)
	}
	serviceImpl := grpcRunner.ServiceImpl()

	// 启动任务消费 worker：PostgreSQL 模式由共享租约 worker 领取任务；
	// SQLite/内存模式由本地 pending 任务消费协程驱动。
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
	// 12. Startup Config Summary / 启动配置摘要横幅
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
		"rate_limit_enabled", cfg.RateLimitEnabled,
		"rate_limit_rps", cfg.RateLimitRPS,
		"rate_limit_burst", cfg.RateLimitBurst,
		"rate_limit_per_identity_rps", cfg.RateLimitPerIdentityRPS,
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

	// M-2: Warn when the hub is reachable off-box but CORS is left at the wildcard default.
	// 当本服务绑定非环回地址（对外可达）却未显式配置 CORS 白名单时告警：
	// pkg/middleware.CORS(nil) 会下发 Access-Control-Allow-Origin: *。虽然入站采用
	// Authorization: Bearer（非 Cookie，且不下发 Allow-Credentials），实际跨站风险有限，
	// 但作为南北向唯一入口仍应按最小化原则收紧。
	if !cfg.LoopbackOnlyBind() && len(cfg.CORSOrigins) == 0 {
		logger.Warn("CORS wildcard (*) is in effect on a non-loopback bind: set SERVICE_HUB_CORS_ORIGINS to an explicit allow-list for production", "cors_origins", 0)
	}

	// =========================================================================
	// 13. Operating System Signal Registration / 系统中断信号监听
	// =========================================================================
	// 使用 signal.NotifyContext（Go 1.16+）替代传统的 signal.Notify + channel 模式，
	// 信号到达时自动取消 context，与下游协程的 ctx.Done() 无缝衔接。
	sigCtx, sigStop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer sigStop()

	// =========================================================================
	// 14. Dual-Protocol Concurrent Listeners / 双协议并发监听启动
	// =========================================================================
	// 1) 启动 gRPC 服务事件循环（TCP 监听已在构造阶段完成预绑定）
	go func() {
		if err := grpcRunner.Start(); err != nil {
			log.Fatalf("%v", err)
		}
	}()

	// 2) 启动 HTTP REST 服务并在后台独立协程中监听请求（TLCP/TLS/明文三分支）
	go func() {
		if err := restRunner.Start(); err != nil {
			log.Fatalf("%v", err)
		}
	}()

	// Both REST and gRPC listeners have been launched successfully; mark service-hub ready.
	mc.SetReady(true)

	// =========================================================================
	// 15. Graceful Shutdown Workflow / 优雅停机收敛流程
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

	// 4) 优雅停止 gRPC 服务器，拒绝新连接并等待当前 RPC 调用返回
	//    （GracefulStop 带 30s 超时看门狗，超时回退强制 Stop）
	grpcRunner.Shutdown()

	// 5) 优雅关闭 HTTP 服务器，等待在途请求完成（超时时间可配置）
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ShutdownTimeout)*time.Second)
	defer cancel()
	if err := restRunner.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err.Error())
	}
}
