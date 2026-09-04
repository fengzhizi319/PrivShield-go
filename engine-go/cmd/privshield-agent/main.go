// Package main 提供 PrivShield Go 核心隐私与动态分类分级引擎（Core Agent / Sidecar）的服务端入口。
//
// ─────────────────────────────────────────────────────────────────────────────
// 【系统架构定位与模块化多协议支持】
// PrivShield Agent 是数盾数据安全与隐私治理体系的核心执行实体（Sidecar / Agent），负责下发与执行：
//   - 44 项前沿隐私数学原语（SM3/SM4 掩码、单趟融合向量差分隐私、多核分块局部差分隐私、Mondrian K-匿名、查询置乱等）；
//   - 3 层动态敏感特征分类分级漏斗（AC 规则引擎 → Small-NER ONNX 推理 → 外部 LLM 熔断治理）；
//   - 无锁原子 CAS 隐私预算会计与医学 DICOM 二进制合规重构流水线。
//
// 进程支持 REST 与 gRPC 模块解耦与动态选择性启停（默认双协议并发，100% 向下兼容）：
//  1. REST API 模块 (Gin 驱动，默认端口 :8079，由 AGENT_REST_ENABLED 控制)：
//     面向控制台 (BFF) 及外部 HTTP 业务调用方，提供 DataFrame 批处理、文件脱敏、合规探针、动态分类及诊断端点；
//  2. gRPC Server 模块 (低延迟 RPC，默认端口 :50051，由 AGENT_GRPC_ENABLED 控制)：
//     面向微服务调度中枢 (service-hub) 与内部集群节点，基于 Protobuf 协议与 RawCodec 实现高吞吐零拷贝的数据交换。
//
// ─────────────────────────────────────────────────────────────────────────────
// 【Agent 启动与运行全生命周期（7 大执行阶段）】
//
//	[阶段 1: 配置与门禁] ── LoadAgent() + P0-1 零信任门禁校验 (Validate)
//	       │
//	[阶段 2: 核心引擎初始化] ── slog 日志 + PrivacyService 隐私编排器 + Prometheus 指标 + 命名观测器
//	       │
//	[阶段 3: REST 模块按需装配] ── 若 AGENT_REST_ENABLED=true，构造 RESTServerRunner 并协程启动
//	       │
//	[阶段 4: gRPC 模块按需装配] ── 若 AGENT_GRPC_ENABLED=true，构造 GRPCServerRunner 并协程启动
//	       │
//	[阶段 5: 启动摘要与告警] ── 输出运行状态、监听地址与预算快照；对本地明文或单协议模式输出提示
//	       │
//	[阶段 6: 信号捕获与探针置位] ── 捕获 SIGINT/SIGTERM → K8s 就绪探针置 false → 流量排空等待
//	       │
//	[阶段 7: 确定性优雅停机] ── 优雅关闭已开启的 REST Server 与 gRPC Server（带看门狗兜底）
//
// ─────────────────────────────────────────────────────────────────────────────
// 【核心环境变量矩阵】（agent 只读取 AGENT_* 前缀，见 engine-go/internal/config/config.go）
//   - AGENT_REST_ENABLED: 是否启用 REST 服务（默认 true）
//   - AGENT_GRPC_ENABLED: 是否启用 gRPC 服务（默认 true）
//   - AGENT_REST_HOST / AGENT_REST_PORT: REST 监听地址与端口（默认 127.0.0.1:8079，生产编排注入 0.0.0.0）
//   - AGENT_GRPC_HOST / AGENT_GRPC_PORT: gRPC 监听地址与端口（默认 127.0.0.1:50051）
//   - AGENT_LOG_LEVEL: 日志级别（DEBUG/INFO/WARN/ERROR，默认 INFO）
//   - AGENT_TLS_ENABLED: 是否启用 TLS 通信加密 (HTTPS 与 gRPC TLS)
//   - AGENT_REQUIRE_TLS: 强制加密红线标志，若为 true 但 TLS 未成功开启则阻断启动
//   - AGENT_TLS_NATIONAL_CIPHER: 是否启用 GM/T 0024 国密通信协议 (TLCP 双证书模式)
//   - AGENT_TLS_CERT_FILE / AGENT_TLS_KEY_FILE / AGENT_TLS_CA_FILE: 标准 TLS 证书、私钥与根 CA 路径
//   - AGENT_AUTH_ENABLED + AGENT_AUTH_INTERNAL_API_KEYS: 入站 API Key 鉴权开关与密钥列表 (逗号分隔)
//   - AGENT_AUTH_INTERNAL_MTLS_ENABLED: 是否开启 gRPC 客户端双向证书认证 (mTLS)
//   - AGENT_AUTH_MTLS_WHITELIST_FILE: 客户端证书 CN 白名单配置文件路径 (支持 5s 无依赖热重载)
//   - AGENT_RATE_LIMIT_RPS / AGENT_RATE_LIMIT_BURST: 入站全局令牌桶限流速率与突发容量
//   - AGENT_SHUTDOWN_DRAIN_SECONDS: 收到停机信号后等待网络流量排空的等待秒数（默认 5s）
//   - AGENT_GRPC_GRACEFUL_STOP_SECONDS: gRPC 优雅停机看门狗超时时间（默认 15s，超时则强制关闭）
package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	engineconfig "github.com/fengzhizi319/PrivShield-go/engine-go/internal/config"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/observability"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/rest"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/service"
	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
	"github.com/fengzhizi319/PrivShield-go/pkg/naming"
)

// ──────────────────────────────────────────────
// 版本信息（编译时通过 -ldflags 注入）
// ──────────────────────────────────────────────

var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

// ──────────────────────────────────────────────
// 配置结构体与加载
// ──────────────────────────────────────────────

type Config struct {
	// Runtime 承载监听面（Host/Port/Enabled）与安全开关，并提供 P0-1 fail-closed 门禁 Validate()。
	*engineconfig.Runtime
	LogLevel       string
	RateLimitRPS   int
	RateLimitBurst int
}

func loadConfig() Config {
	return Config{
		Runtime:        engineconfig.LoadAgent(),
		LogLevel:       pkgconfig.EnvString("AGENT_LOG_LEVEL", "INFO"),
		RateLimitRPS:   pkgconfig.EnvInt("AGENT_RATE_LIMIT_RPS", 1000),
		RateLimitBurst: pkgconfig.EnvInt("AGENT_RATE_LIMIT_BURST", 2000),
	}
}

// ──────────────────────────────────────────────
// 主入口：生命周期统一编排
// ──────────────────────────────────────────────

func main() {
	// =========================================================================
	// 【阶段 1：环境配置加载与 P0-1 零信任启动门禁】
	// =========================================================================
	// 1. 从环境变量加载运行时配置（含 REST/gRPC 开关、监听地址、TLS、mTLS 等）；
	// 2. 默认绑定 127.0.0.1 环回地址（开发安全态），生产容器显式注入 0.0.0.0；
	// 3. 执行 cfg.Validate() 实施严格的 fail-closed 门禁校验：
	//    - 至少启用 REST 或 gRPC 之一，禁止两协议全关；
	//    - 非环回暴露面必须配置凭证；
	//    - AGENT_REQUIRE_TLS=true 时若 TLS 未启用直接阻断；
	//    - mTLS 启用但白名单文件不可达直接阻断。
	cfg := loadConfig()

	if err := cfg.Validate(); err != nil {
		log.Fatalf("[FATAL] 启动门禁拦截失败 (P0-1 零信任安全原则): %v", err)
	}

	// =========================================================================
	// 【阶段 2：可观测性底座与隐私计算编排核心初始化】
	// =========================================================================
	// 1. 初始化标准库 log/slog 结构化日志系统；
	// 2. 构造 PrivacyService 核心服务编排器（44 项隐私原语、3 层分类漏斗、预算会计、DICOM）；
	// 3. 构造 Prometheus 引擎指标收集器；
	// 4. 注册 pkg/naming 观测器：实时监控非标准 API 别名调用并双写日志与指标。
	observability.InitLogger(cfg.LogLevel)

	slog.Info("Starting PrivShield Go Engine",
		"version", Version,
		"build_time", BuildTime,
		"git_commit", GitCommit,
		"rest_enabled", cfg.RESTEnabled,
		"grpc_enabled", cfg.GRPCEnabled,
	)

	svcCfg := service.DefaultConfig()
	svc, err := service.NewPrivacyService(svcCfg)
	if err != nil {
		slog.Error("Failed to init PrivacyService core orchestration", "err", err)
		os.Exit(1)
	}

	engineMetrics := observability.NewEngineMetrics()
	naming.SetObserver(namingObserver{metrics: engineMetrics})

	// =========================================================================
	// 【阶段 3：REST 模块按需构建与异步启动】
	// =========================================================================
	var restRunner *RESTServerRunner
	if cfg.RESTEnabled {
		runner, err := newRESTServerRunner(cfg, svc, engineMetrics)
		if err != nil {
			slog.Error("Failed to build REST server runner", "err", err)
			os.Exit(1)
		}
		restRunner = runner

		go func() {
			if err := restRunner.Start(); err != nil {
				slog.Error("REST server fatal error", "err", err)
				os.Exit(1)
			}
		}()
	} else {
		slog.Warn("REST server is DISABLED by configuration (AGENT_REST_ENABLED=false); " +
			"HTTP endpoints (/readyz, /healthz, /metrics) will not be served over HTTP")
	}

	// =========================================================================
	// 【阶段 4：gRPC 模块按需构建与异步启动】
	// =========================================================================
	var grpcRunner *GRPCServerRunner
	if cfg.GRPCEnabled {
		runner, err := newGRPCServerRunner(cfg, svc, engineMetrics)
		if err != nil {
			slog.Error("Failed to build gRPC server runner", "err", err)
			os.Exit(1)
		}
		grpcRunner = runner

		go func() {
			if err := grpcRunner.Start(); err != nil {
				slog.Error("gRPC server fatal error", "err", err)
				os.Exit(1)
			}
		}()
	} else {
		slog.Info("gRPC server is DISABLED by configuration (AGENT_GRPC_ENABLED=false)")
	}

	// =========================================================================
	// 【阶段 5：运行时配置自检、隐私预算快照与安全告警】
	// =========================================================================
	budgetStatus := svc.BudgetStatus()
	slog.Info("Configuration summary",
		"rest_enabled", cfg.RESTEnabled,
		"rest_addr", cfg.RESTAddress(),
		"grpc_enabled", cfg.GRPCEnabled,
		"grpc_addr", cfg.GRPCAddress(),
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

	if !cfg.AuthEffectivelyEnabled() && !cfg.TLSEnabled {
		slog.Warn("Running with authentication and TLS DISABLED on a loopback bind; " +
			"for any exposed deployment set AGENT_AUTH_ENABLED=true with AGENT_AUTH_INTERNAL_API_KEYS " +
			"and AGENT_TLS_ENABLED=true (plus AGENT_AUTH_MTLS_WHITELIST_FILE)")
	}

	// =========================================================================
	// 【阶段 6：信号捕获与流量平滑排空】
	// =========================================================================
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	slog.Info("Shutdown signal received, starting graceful draining", "signal", sig)

	// 1. 将 K8s 就绪探针置为 unready，触发集群 Endpoint 控制器剔除当前实例
	if restRunner != nil {
		rest.SetReady(false)
	}

	// 2. 流量排空等待窗口（等待 kube-proxy 刷新路由规则，让在途请求完成处理）
	drainSec := pkgconfig.EnvInt("AGENT_SHUTDOWN_DRAIN_SECONDS", 5)
	if drainSec > 0 {
		slog.Info("Draining in-flight traffic", "seconds", drainSec)
		time.Sleep(time.Duration(drainSec) * time.Second)
	}

	// =========================================================================
	// 【阶段 7：确定性分步优雅停机】
	// =========================================================================
	// 3. 优雅停止 REST Server
	if restRunner != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := restRunner.Shutdown(ctx); err != nil {
			slog.Error("REST server shutdown error", "err", err)
		}
	}

	// 4. 优雅停止 gRPC Server（带看门狗超时强制停机保护）
	if grpcRunner != nil {
		grpcGraceSec := pkgconfig.EnvInt("AGENT_GRPC_GRACEFUL_STOP_SECONDS", 15)
		timeout := time.Duration(grpcGraceSec) * time.Second
		if err := grpcRunner.Shutdown(timeout); err != nil {
			slog.Warn("gRPC server shutdown warning", "err", err)
		}
	}

	slog.Info("Engine stopped gracefully")
}

// ──────────────────────────────────────────────
// 辅助类型与方法
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
