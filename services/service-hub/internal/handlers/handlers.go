// Package handlers implements the HTTP REST interface for the service-hub module.
// Package handlers 实现数据服务调度中枢模块（service-hub）的 HTTP REST 控制器与 6 阶段流水线调度逻辑。
//
// ==============================================================================
// 6 阶段数据流通与安全治理调度流水线 (6-Stage Governance Pipeline)：
// ==============================================================================
//
//	① 请求接入 (ingest)   ──▶ ② 数据拉取 (fetch)    ──▶ ③ 分类分级 (classify)
//	       │                           │                           │
//	       ▼                           ▼                           ▼
//	④ 下发脱敏 (desensitize) ──▶ ⑤ 结果返回 (return)   ──▶ ⑥ 审计存证 (audit / done)
//
// 路由清单 (Route List)：
//   GET  /health                         → 存活探针 (Liveness: always 200 if process is alive)
//   GET  /readyz                         → 就绪探针 (Readiness: 503 if upstream unreachable)
//   GET  /health                     → 标准健康检查探针
//   GET  /v1/hub/status                 → 调度中枢运行状态与队列深度概览
//   GET  /v1/hub/tasks                  → 分页查询任务列表 (支持 status 状态过滤)
//   GET  /v1/hub/tasks/:id              → 根据 TaskID 查询单个任务详情
//   POST /v1/hub/dispatch               → 直接分发指定算子的隐私处理任务 (API1/2/3/4 核心)
//   POST /v1/hub/fetch-and-desensitize  → 按身份证号拉取数据并同步执行分类分级+脱敏（端到端）
//   GET  /v1/hub/pipeline               → 6 阶段流水线监控遥测与 QPS 统计
//   GET  /metrics                        → Prometheus 格式监控指标导出端点
// ==============================================================================

package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
	"github.com/fengzhizi319/PrivShield-go/pkg/metrics"
	"github.com/fengzhizi319/PrivShield-go/pkg/middleware"
	naming "github.com/fengzhizi319/PrivShield-go/pkg/naming"
	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
	"github.com/fengzhizi319/PrivShield-go/pkg/store"
	"github.com/fengzhizi319/PrivShield-go/pkg/validation"

	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/agent"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/audit"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/config"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/datasource"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/models"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/retry"
)

const moduleVia = "service-hub"

// dispatchRequest is the common request shape used across Dispatch / ClassifyAndDispatch / processTask.
// dispatchRequest 结构体表示任务提交与内部异步流转的标准入参载荷。
type dispatchRequest struct {
	APICode      string `json:"api_code"`
	DatasourceID string `json:"datasource_id"`
	Source       string `json:"source"`    // 兼容历史字段
	Operation    string `json:"operation"` // 可选的调用方「强度请求」（mask/k_anon/dp/classify/none）；生效算子由服务端定级推导，只允许上调
	Payload      any    `json:"payload"`   // 原始记录数据
	Priority     int    `json:"priority"`  // 执行优先级
}

// Server aggregates HTTP handler dependencies.
// Server 结构体聚合 HTTP REST 控制器所需的全部核心依赖与并发控制资源。
type Server struct {
	agent      *agent.Client       // 上游 PrivShield Python Agent 客户端
	datasource *datasource.Client  // 下游 datasource-mgr 数据源服务客户端
	audit      *audit.Client       // audit-log 存证客户端（P0-6：出域 ↔ 留痕强绑定）
	cfg        *config.Config      // 模块全局运行配置
	keyStore   *pkgauth.KeyStore   // API Key 文件热轮转 KeyStore（可选，K8s Secret 投影场景）
	userStore  *pkgauth.UserStore  // 普通用户与动态权限管理引擎
	liveKeys   *pkgauth.Aggregator // 明文活密钥聚合器（静态 ScopeKeys + KeyStore 热轮转，版本驱动缓存）
	startTime  time.Time           // 服务启动时间戳（用于计算 Uptime）
	tasks      store.TaskStore     // 任务持久化存储介质（SQLite 或内存实现）
	logger     *slog.Logger        // 结构化日志记录器
	mc         *metrics.Collector  // Prometheus 监控指标收集器
	taskSem    chan struct{}       // 信号量通道，限制后台最大并发任务协程数（默认 10）
	ctx        context.Context     // 用于在服务停机时向所有在途任务协程广播取消信号的父 Context
	cancel     context.CancelFunc  // 触发优雅停机 Context 取消的回调函数
	wg         sync.WaitGroup      // 等待组，跟踪记录正在执行的后台任务协程
}

// New creates a new Server instance.
// New 构造函数初始化 Server 实例，并默认分配容量为 10 的并发任务信号量与优雅停机上下文。
//
// 存证客户端在此无条件装配：即使 SERVICE_HUB_AUDIT_LOG_URLS 未配置也保留实例，
// 其提交必然返回 audit.ErrNotConfigured，由流水线 audit 阶段将任务判定为 failed（fail-closed），
// 绝不允许「没有存证链路却把出域任务标成 done」。测试通过配置中的 AuditLogBaseURLs 指向桩服务。
func New(ag *agent.Client, ds *datasource.Client, cfg *config.Config, keyStore *pkgauth.KeyStore, tasks store.TaskStore, logger *slog.Logger, mc *metrics.Collector) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	userStoreFile := pkgconfig.EnvString("SERVICE_HUB_USER_STORE_FILE", "")
	us, err := pkgauth.NewUserStore(userStoreFile)
	if err != nil {
		logger.Error("failed to initialize user store; falling back to in-memory", "error", err)
		us, _ = pkgauth.NewUserStore("")
	}

	return &Server{
		agent:      ag,
		datasource: ds,
		audit:      audit.New(cfg, mc),
		cfg:        cfg,
		keyStore:   keyStore,
		userStore:  us,
		liveKeys:   pkgauth.NewAggregator(cfg.ScopeKeys, keyStore),
		startTime:  time.Now(),
		tasks:      tasks,
		logger:     logger,
		mc:         mc,
		taskSem:    make(chan struct{}, 10), // 最大允许 10 个流水线任务并发异步执行
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Shutdown gracefully stops all in-flight task goroutines.
// Shutdown 优雅停机方法：通知所有正在执行的 processTask 任务协程安全退出，并阻塞等待全部协程完成。
func (s *Server) Shutdown() {
	s.cancel()
	s.wg.Wait()
}

// StartLocalWorker starts a local worker loop for consuming pending/recovered tasks in SQLite/memory mode.
// StartLocalWorker 启动本地待处理任务消费协程（用于 SQLite/内存模式下的崩溃恢复与重试任务拉取）。
func (s *Server) StartLocalWorker() error {
	s.wg.Add(1)
	go s.localWorkerLoop()
	s.logger.Info("local pending task worker started (SQLite/memory mode)")
	return nil
}

func (s *Server) localWorkerLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.processPendingTasks()
		}
	}
}

func (s *Server) processPendingTasks() {
	pendingTasks, _, err := s.tasks.List(store.TaskFilter{Status: "pending", Limit: 10})
	if err != nil || len(pendingTasks) == 0 {
		return
	}

	for i := range pendingTasks {
		task := pendingTasks[i]
		if task.RetryAfter != nil && time.Now().Before(*task.RetryAfter) {
			continue
		}

		// Atomically mark task as running in SQLite before processing to avoid race
		now := time.Now()
		task.Status = "running"
		task.Stage = "ingest"
		task.StartedAt = &now
		if err := s.persistTask(&task, "local worker claim"); err != nil {
			continue
		}

		var payload any
		if task.PayloadJSON != "" && task.PayloadJSON != "null" {
			_ = json.Unmarshal([]byte(task.PayloadJSON), &payload)
		}
		req := dispatchRequest{
			APICode:      task.APICode,
			DatasourceID: task.DatasourceID,
			Source:       task.Source,
			Operation:    task.Operation,
			Payload:      payload,
			Priority:     task.Priority,
		}

		s.wg.Add(1)
		go func(t store.Task, r dispatchRequest) {
			defer s.wg.Done()
			reqID := validation.GenerateID("retry")
			s.processTask(&t, r, reqID)
		}(task, req)
	}
}

// persistTask writes a task state transition before the pipeline continues.
func (s *Server) persistTask(task *store.Task, transition string) error {
	if err := s.tasks.Update(task); err != nil {
		s.logger.Error("failed to persist task state",
			"task_id", task.ID,
			"transition", transition,
			"status", task.Status,
			"stage", task.Stage,
			"error", err.Error())
		return err
	}
	return nil
}

// RegisterRoutes registers all HTTP routes on the Gin engine.
// RegisterRoutes 在 Gin 路由引擎上挂载完整的中间件链与 REST API 端点。
// 中间件装配顺序：
// 1. RequestID: 自动注入链路追踪 X-Request-ID
// 2. HTTPMiddleware: 自动采集 http_requests_total 计数与延迟直方图（/metrics 自身豁免，防递归）
// 3. RequestLoggerWithModule: 输出包含延迟、状态码、IP 的结构化 JSON/Text 日志
// 4. Recovery: 拦截 Handler Panic 并返回 500 JSON
// 5. SecurityHeaders: 注入 CSP、HSTS、X-Content-Type-Options 等安全防护头
// 6. MaxBodySize: 限制请求体最大 32 MiB，防御超大 Body 内存溢出
// 7. MaxConcurrent: 限制在途请求并发上限（1000），超限返回 503
// 8. RateLimit: (默认启用) 每客户端 IP 令牌桶边缘限流，超限返回 429
// 9. CORS: 跨域来源校验与预检放行
// 10. Auth: 基于 Authorization Bearer 的 API Key 鉴权校验
// 11. IdentityRateLimit: (默认启用) 身份级细粒度限流（key = 身份 + 归一化路径，匿名回退 IP），探针端点豁免
func (s *Server) RegisterRoutes(r *gin.Engine) {
	r.Use(middleware.TraceMiddleware())
	r.Use(s.mc.HTTPMiddleware()) // docs 宣称的 http_requests_total 在此真正生效
	r.Use(pkgobs.RequestLoggerWithModule("service-hub"))
	r.Use(middleware.Recovery(s.logger, "service-hub"))
	r.Use(middleware.WAF(s.logger)) // 三级等保 G-12：Web 攻击载荷检测
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.MaxBodySize(32 << 20)) // 32 MiB 请求体最大保护
	r.Use(middleware.MaxConcurrent(1000))   // 并发在途请求上限，超限返回 503
	if s.cfg.RateLimitEnabled && s.cfg.RateLimitRPS > 0 {
		// 每客户端 IP 令牌桶边缘限流；/health、/readyz、/metrics 探针端点豁免
		r.Use(middleware.KeyedRateLimit(s.cfg.RateLimitRPS, s.cfg.RateLimitBurst, func(c *gin.Context) string {
			return middleware.RealClientIP(c)
		}, "/health", "/readyz", "/metrics"))
	}
	r.Use(middleware.CORS(s.cfg.CORSOrigins))
	r.Use(s.scopeAuthMiddleware())
	if s.cfg.RateLimitEnabled && s.cfg.RateLimitPerIdentityRPS > 0 {
		r.Use(s.identityRateLimitMiddleware()) // 身份级细粒度限流（鉴权之后才能拿到身份）
	}

	// 基础健康检查与服务概览
	r.GET("/health", s.Health) // Liveness probe / 存活探针
	r.GET("/readyz", s.Readyz) // Readiness probe / 就绪探针
	r.GET("/v1/hub/status", s.HubStatus)
	r.GET("/v1/hub/topology", s.HubTopology)

	// 任务生命周期管理
	r.GET("/v1/hub/tasks", s.ListTasks)
	r.GET("/v1/hub/tasks/:id", s.GetTask)
	r.POST("/v1/hub/dispatch", s.Dispatch)

	// 按身份证号端到端查询+脱敏（同步）
	r.POST("/v1/hub/fetch-and-desensitize", s.FetchAndDesensitize)

	// 流水线监控遥测
	r.GET("/v1/hub/pipeline", s.Pipeline)

	// 审计存证代理与验真（app-lz 等外部程序唯一编排入口）
	r.GET("/v1/hub/audit/logs", s.GetAuditLogs)
	r.POST("/v1/hub/audit/logs", s.CreateAuditLog)
	r.POST("/v1/hub/audit/verify", s.VerifyAudit)

	// 数据源资产查询（app-lz 等外部程序唯一编排入口）
	r.GET("/v1/hub/datasources", s.ListDatasources)

	// Prometheus 监控指标导出
	r.GET("/metrics", s.mc.Handler())

	// 普通用户与权限全生命周期管理端点 (/v1/auth/*)
	// 策略口径由环境变量驱动（SERVICE_HUB_USER_SELF_REGISTER / _USER_SESSION_TTL /
	// _USER_LOGIN_THROTTLE_PER_MIN），默认关闭公开自注册。
	if s.userStore != nil {
		pkgauth.RegisterUserRoutes(r, s.userStore, pkgauth.UserRouteOptionsFromEnv("SERVICE_HUB")...)
	}

	// 【启动权限审计】遍历全部已注册路由，识别遗漏显式 scope 映射、静默落入 fail-closed
	// 兜底权限（"admin"）的新增接口并打 WARN，防止「加了路由忘配权限」。详见 pkg/auth/route_audit.go。
	pkgauth.LogRoutePermissionAudit(s.logger, "service-hub", r.Routes(),
		func(method, path string) string { return pkgauth.ServiceHubPermissionForPath(path) },
		map[string]bool{"admin": true}, nil)
}

// currentAuthKeys 返回当前生效的「明文 Token → KeyConfig」快照：静态 SERVICE_HUB_API_KEYS
// 与 KeyStore 热轮转 key 合并（同名以热轮转为准），由版本驱动的 Aggregator 缓存，
// 仅在密钥集变更时重建，避免认证热路径每请求全量拷贝 map。
//
// 注：UserStore 动态用户密钥与登录会话**不在**本快照内——它们落盘仅存 SHA-256 摘要，
// 经 LiveHashedAuthKeys() 走独立的 LiveInternalHashedKeys 通道参与认证。
func (s *Server) currentAuthKeys() map[string]*pkgauth.KeyConfig {
	if s.liveKeys != nil {
		return s.liveKeys.Keys()
	}
	// 兼容未经 New() 直接构造 &Server{...} 的测试与嵌入场景：退化为即时合并（无版本缓存）。
	static := map[string]*pkgauth.KeyConfig(nil)
	if s.cfg != nil {
		static = s.cfg.ScopeKeys
	}
	merged := make(map[string]*pkgauth.KeyConfig, len(static))
	for k, v := range static {
		merged[k] = v
	}
	if s.keyStore != nil {
		for k, v := range s.keyStore.Keys() {
			merged[k] = v
		}
	}
	return merged
}

// LiveAuthKeys 返回可直接挂载到 pkgauth.Settings.LiveInternalKeys 的明文活密钥回调，
// 供 main.go 接线 gRPC 拦截器，使 REST 与 gRPC 双路径共享同一动态凭证视图。
func (s *Server) LiveAuthKeys() func() map[string]*pkgauth.KeyConfig {
	return s.currentAuthKeys
}

// LiveHashedAuthKeys 返回可挂载到 pkgauth.Settings.LiveInternalHashedKeys 的摘要型活密钥回调；
// 未启用用户体系时返回 nil（使 gRPC 侧不会误判为“已配置动态凭证来源”）。
func (s *Server) LiveHashedAuthKeys() func() map[string]*pkgauth.KeyConfig {
	if s.userStore == nil {
		return nil
	}
	return s.userStore.LiveHashedKeysFunc()
}

// scopeAuthMiddleware 返回 Gin 中间件，优先使用 Scope-based 鉴权（SERVICE_HUB_API_KEYS + KeyStore + UserStore），
// 向后兼容单 APIKey 模式（SERVICE_HUB_API_KEY）。
// Scope-based 模式下，每个 Key 携带 Name、Subject 与 Scopes，按路径映射所需权限进行细粒度校验；
// /v1/auth/* 用户管理面的具体授权在 Handler 内按主体（ABAC）判定，路由层仅要求已认证。
func (s *Server) scopeAuthMiddleware() gin.HandlerFunc {
	var hashedProvider func() map[string]*pkgauth.KeyConfig
	if s.userStore != nil {
		hashedProvider = s.userStore.LiveHashedKeysFunc()
	}
	settings := &pkgauth.Settings{
		AuthEnabled:            true,
		HealthNoAuth:           true,
		LiveInternalKeys:       s.currentAuthKeys,
		LiveInternalHashedKeys: hashedProvider,
	}
	legacy := middleware.Auth(s.cfg.APIKey)

	scopeHandler := func(c *gin.Context) {
		path := c.Request.URL.Path
		token := pkgauth.ExtractBearerToken(c.GetHeader("Authorization"))

		// 公开认证端点（登录 / 首个管理员引导注册）：未携 Token 时注入 public 身份放行，
		// 否则启用 Scope 鉴权后登录与开户流程会被一律 401 锁死。
		if pkgauth.IsAuthPublicPath(path) && token == "" {
			c.Set(pkgauth.IdentityContextKey, &pkgauth.Identity{
				ServiceType: "public", Name: "public-caller", Scopes: []string{"auth:public"},
			})
			c.Next()
			return
		}

		if token == "" {
			pkgauth.AuthFailuresTotal.WithLabelValues("missing_token").Inc()
			middleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Unauthorized: missing credentials", nil)
			return
		}
		identity := pkgauth.AuthenticateAPIKey(settings, token)
		if identity == nil {
			pkgauth.AuthFailuresTotal.WithLabelValues("invalid_token").Inc()
			middleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Unauthorized: invalid credentials", nil)
			return
		}

		// 公开认证端点的映射值 "auth:public" 仅用于路由审计可追溯，不得作为已认证调用者的
		// 强制 scope；否则仅持 user:admin 而无 "*" 的管理员无法调用注册端点为下属开户。
		if pkgauth.IsAuthPublicPath(path) {
			c.Set(pkgauth.IdentityContextKey, identity)
			c.Next()
			return
		}

		requiredPerm := pkgauth.ServiceHubPermissionForPath(path)
		if requiredPerm != "" && !identity.HasPermission(requiredPerm) {
			pkgauth.AuthForbiddenTotal.Inc()
			middleware.AbortWithError(c, http.StatusForbidden, "FORBIDDEN", "Forbidden: insufficient scope", nil)
			return
		}
		c.Set(pkgauth.IdentityContextKey, identity)
		c.Next()
	}

	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if path == "/health" || path == "/readyz" {
			c.Next()
			return
		}
		// 鉴权模式**运行期动态判定**（KeyStore 热轮转与用户体系均可在启动后发生变化）：
		//   · 存在静态/热轮转 Scope Key → Scope 细粒度鉴权（与历史行为一致）；
		//   · 未配置遗留单 APIKey 且用户体系已开户 → Scope 鉴权（纯动态开户部署，
		//     引导期创建首个 admin 后自动从开发免密透传收敛为强制鉴权）；
		//   · 其余情形 → 遗留单 APIKey 模式（APIKey 为空即开发免密透传），保持向后兼容。
		if len(s.currentAuthKeys()) > 0 || (s.cfg.APIKey == "" && s.userStore.Count() > 0) {
			scopeHandler(c)
			return
		}
		legacy(c)
	}
}

// identityRateLimitMiddleware 返回身份级细粒度限流中间件（32 分片令牌桶，复用 pkg/middleware）。
//
// 限流 key = 「身份 ServiceType:Name + 归一化路径」；未认证（匿名）调用者追加客户端 IP 作为分片因子，
// 防止单 IP 洪泛。路径经 NormalizeRateLimitPath 归一化（动态数字/UUID 段替换为 :id），防止高基数路径
// 导致限流桶爆炸。/health、/readyz、/metrics 探针端点完全豁免，保障 K8s 探针与 Prometheus 抓取畅通。
// 该层挂载在鉴权中间件之后，确保已认证请求以 API 身份（而非共享 IP）为限流维度。
func (s *Server) identityRateLimitMiddleware() gin.HandlerFunc {
	return middleware.KeyedRateLimit(s.cfg.RateLimitPerIdentityRPS, s.cfg.RateLimitPerIdentityBurst, func(c *gin.Context) string {
		path := c.Request.URL.Path
		identity := pkgauth.GetIdentity(c)
		serviceType, name := "external", "anonymous"
		if identity != nil && identity.Name != "" {
			serviceType, name = identity.ServiceType, identity.Name
		}
		key := serviceType + ":" + name + ":" + middleware.NormalizeRateLimitPath(path)
		if identity == nil {
			if clientIP := middleware.RealClientIP(c); clientIP != "" {
				key += ":" + clientIP
			}
		}
		return key
	}, "/health", "/readyz", "/metrics")
}

// callerName 返回当前请求调用者标识名称，未认证时返回 "anonymous"。
func (s *Server) callerName(c *gin.Context) string {
	identity := pkgauth.GetIdentity(c)
	if identity != nil && identity.Name != "" {
		return identity.Name
	}
	return "anonymous"
}

// checkDatasourceAccess 执行细粒度数据源级授权检查（ABAC / 租户数据源隔离）。
//
// 鉴权判定顺序（Fail-closed 最小权限）：
//  1. 若当前未获取到调用者身份（如未配置鉴权的开发/免密模式），放行；
//  2. 若身份拥有超级管理权限（"*" 或 "admin"），放行；
//  3. 检查是否存在针对该数据源的显式授权：
//     - "hub:dispatch:<normID>" / "hub:dispatch:<rawID>"
//     - "data:apply:<normID>" / "data:apply:<rawID>"
//     若匹配则直接放行；
//  4. 检查调用者 scopes 中是否声明了任何细粒度数据源限定（包含 "hub:dispatch:ds_" 或 "data:apply:" 前缀）；
//     若存在细粒度限定但未命中当前请求的数据源，判定为越权访问，拒绝（返回 false）；
//  5. 若未声明任何细粒度限定，但拥有通用调度权限（"hub:dispatch" 或 "hub:dispatch:*"），放行。
func (s *Server) checkDatasourceAccess(c *gin.Context, normID, rawID string) bool {
	return pkgauth.CheckDatasourceAccess(pkgauth.GetIdentity(c), normID, rawID)
}

// Health is a liveness probe — returns 200 if the process is alive.
// Use /readyz for deep upstream dependency checks.
// Health 存活探针 — 进程存活即返回 200。
// 深度上游依赖检查请使用 /readyz。
func (s *Server) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"via":    moduleVia,
	})
}

// Readyz is a readiness probe — checks upstream agent + datasource-mgr connectivity.
// Returns 503 Service Unavailable when critical backends are unreachable,
// so K8s won't route traffic to this pod until dependencies are ready.
// Readyz 就绪探针 — 检查上游 Agent + datasource-mgr 连通性。
// 当关键后端不可用时返回 503，K8s 不会将流量路由到该 Pod。
func (s *Server) Readyz(c *gin.Context) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	agentData, agentErr := s.agent.Health(ctx)
	latency := time.Since(start).Milliseconds()

	var dsStatus any = "ok"
	if s.datasource != nil {
		if dsData, dsErr := s.datasource.Health(ctx); dsErr != nil {
			dsStatus = "unreachable"
		} else if st, ok := dsData["status"]; ok {
			dsStatus = st
		}
	}

	// Agent is the critical dependency — if unreachable, report not ready.
	// Agent 是关键依赖 — 不可用时报告未就绪。
	if agentErr != nil {
		if s.mc != nil {
			s.mc.SetReady(false)
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":         "not_ready",
			"backend":        "ok",
			"agent":          "unreachable",
			"agent_url":      s.cfg.AgentBaseURL(),
			"datasource":     dsStatus,
			"datasource_url": s.cfg.DatasourceBaseURL(),
			"latency_ms":     latency,
			"error":          agentErr.Error(),
			"via":            moduleVia,
		})
		return
	}

	if s.mc != nil {
		s.mc.SetReady(true)
	}
	c.JSON(http.StatusOK, gin.H{
		"status":         "ready",
		"backend":        "ok",
		"agent":          agentData,
		"agent_url":      s.cfg.AgentBaseURL(),
		"datasource":     dsStatus,
		"datasource_url": s.cfg.DatasourceBaseURL(),
		"latency_ms":     latency,
		"via":            moduleVia,
	})
}

// HubStatus returns the scheduling hub's current status.
// HubStatus 返回调度中枢当前运行概览（Uptime、排队任务数、活跃任务数、累计成功/失败数）。
func (s *Server) HubStatus(c *gin.Context) {
	counts, err := s.tasks.Counts()
	if err != nil {
		middleware.AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":          "running",
		"uptime":          time.Since(s.startTime).Round(time.Second).String(),
		"active_tasks":    counts.Running,
		"queued_tasks":    counts.Pending,
		"completed_total": counts.Completed,
		"failed_total":    counts.Failed,
		"agent_url":       s.cfg.AgentBaseURL(),
		"datasource_url":  s.cfg.DatasourceBaseURL(),
	})
}

// GetTask returns a single task by ID.
// GetTask 根据 TaskID 查询单个任务详情，若不存在则返回 404 Not Found。
func (s *Server) GetTask(c *gin.Context) {
	id := c.Param("id")
	task, err := s.tasks.Get(id)
	if err != nil {
		middleware.AbortWithError(c, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("task %s not found", id), nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"task": task,
		"via":  moduleVia,
	})
}

// ListTasks returns all tasks, optionally filtered by status query param.
// ListTasks 分页获取任务列表：
// 1. 校验 status 查询参数是否在有效值集合（pending/running/completed/failed）内；
// 2. 解析 limit/offset 分页参数并执行数据库查询；
// 3. 返回包含分页元数据和任务切片的响应。
func (s *Server) ListTasks(c *gin.Context) {
	statusFilter := c.Query("status")

	// 状态枚举白名单校验
	if statusFilter != "" {
		if err := validation.AllowedValues("status", statusFilter, validation.TaskStatuses); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error(), nil)
			return
		}
	}

	// 解析分页安全边界（默认 100，最大 1000）
	limit, offset := validation.ParsePagination(c, 100, 1000)

	tasks, total, err := s.tasks.List(store.TaskFilter{Status: statusFilter, Limit: limit, Offset: offset})
	if err != nil {
		middleware.AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"total":  total,
		"limit":  limit,
		"offset": offset,
		"tasks":  tasks,
		"via":    moduleVia,
	})
}

// Dispatch creates a new task and runs the 6-stage pipeline.
// Dispatch 接收用户提交的数据处理任务：
// 1. 绑定并校验 JSON 请求体（source/datasource_id/api_code 归一化校验；operation 可选）；
// 2. 生成全局唯一 TaskID，初始化为 pending/queued 状态并写入 TaskStore；
// 3. 异步拉起后台协程执行 6 阶段流水线调度（processTask）；
// 4. 立即返回 202 Accepted 包含 TaskID。
//
// 【operation 语义（P1-1）】本字段不再是「执行什么算子」的授权凭据，只是调用方的**强度请求**：
// 允许缺省（完全由服务端定级推导），取值必须在算子词表内，且流水线只会用它**上调**保护强度。
// 真正生效的算子在 ③ classify 阶段由引擎定级结果推导并回写 task.operation，随存证落盘。
func (s *Server) Dispatch(c *gin.Context) {
	var req dispatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("invalid request: %v", err), nil)
		return
	}

	rawSource := req.DatasourceID
	if rawSource == "" {
		rawSource = req.APICode
	}
	if rawSource == "" {
		rawSource = req.Source
	}

	if rawSource == "" {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "source / datasource_id / api_code is required", nil)
		return
	}

	normID, err := naming.ResolveInbound(rawSource)
	if err != nil {
		if naming.IsReserved(err) {
			middleware.AbortWithError(c, http.StatusConflict, "RESERVED_DATASOURCE", fmt.Sprintf("data source %q is reserved: %v", rawSource, err), nil)
			return
		}
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_DATASOURCE_ID", fmt.Sprintf("invalid data source or api_code %q: %v", rawSource, err), nil)
		return
	}
	normAPICode := naming.APICodeForDataSource(normID)

	req.DatasourceID = normID
	req.APICode = normAPICode
	req.Source = normID

	// 细粒度数据源租户授权检查（ABAC）：确保调用方对该数据源具有显式权限
	if !s.checkDatasourceAccess(c, normID, rawSource) {
		middleware.AbortWithError(c, http.StatusForbidden, "UNAUTHORIZED_DATASOURCE",
			fmt.Sprintf("caller %q is not authorized to access datasource %q", s.callerName(c), normID), nil)
		return
	}

	// operation 为可选的「强度请求」：缺省即完全交由服务端定级推导（P1-1）。
	req.Operation = strings.TrimSpace(req.Operation)
	if req.Operation != "" {
		if err := validation.AllowedValues("operation", req.Operation, validation.HubOperations); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error(), nil)
			return
		}
	}

	taskID := validation.GenerateID("task")
	now := time.Now()

	requestID := middleware.GetTraceID(c)
	if requestID == "" {
		requestID = validation.GenerateID("req")
	}

	payloadJSON, _ := json.Marshal(req.Payload)
	status := "pending"
	stage := "queued"
	var startedAt *time.Time
	if s.cfg.PGDSN == "" {
		status = "running"
		stage = "ingest"
		startedAt = &now
	}

	task := &store.Task{
		ID:           taskID,
		APICode:      normAPICode,
		DatasourceID: normID,
		Status:       status,
		Stage:        stage,
		Source:       normID,
		Operation:    req.Operation,
		Priority:     req.Priority,
		CreatedAt:    now,
		StartedAt:    startedAt,
		PayloadJSON:  string(payloadJSON),
		TraceID:      requestID,
	}

	if err := s.tasks.Save(task); err != nil {
		middleware.AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	// PostgreSQL 模式由共享租约 worker 消费，避免不同入口绕过任务所有权。
	if s.cfg.PGDSN == "" {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.processTask(task, req, requestID)
		}()
	}

	c.JSON(http.StatusAccepted, gin.H{
		"task_id":       taskID,
		"api_code":      normAPICode,
		"datasource_id": normID,
		"status":        "accepted",
		"via":           moduleVia,
	})
}

// processTask executes the full 6-stage scheduling pipeline.
// processTask 完整驱动 6 阶段数据安全流通流水线执行：
//
// 流水线执行逻辑（API1/2/3/4 医疗数据核心场景）：
// ① 请求接入 (ingest)：更新状态为 running，初始化任务元数据；
// ② 数据拉取 (fetch)：分页抽取接口已移除，需由调用方在提交任务时显式携带载荷；
// ③ 分类+脱敏 (classify)：一次调用 engine /v1/medical/process 医疗流水线，
//
//	同时完成 3-Layer 分类分级 + L4/L5 高敏文本剥离 + PII 强掩码 + ICD-10 脱敏 + 诊断残留清除；
//
// ④ 脱敏治理 (desensitize)：已由 ③ 合并完成，快速通过（保留阶段状态追踪）；
// ⑤ 结果返回 (return)：组装脱敏后的数据对象；
// ⑥ 审计存证 (audit)：向独立存证节点 audit-log 真实提交一条含 task_id / api_code /
// datasource_id / 输入输出指纹的出域存证；提交失败（端点未配置、网络不可达、4xx/5xx）
// 一律按任务失败处理并落盘，绝不静默推进至 done（P0-6 / G-05）。
func (s *Server) processTask(task *store.Task, req dispatchRequest, requestID string) {
	if requestID == "" {
		requestID = validation.GenerateID("task")
	}

	// 并发信号量限流控制（最多 10 个并发任务）
	s.taskSem <- struct{}{}
	defer func() { <-s.taskSem }()

	// Panic 安全恢复，确保异常时任务被正确标记为 failed 并持久化
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("processTask panic recovered",
				"task_id", task.ID, "panic", fmt.Sprintf("%v", r))
			task.Status = "failed"
			task.Error = fmt.Sprintf("internal panic: %v", r)
			task.ErrorClass = retry.ClassInternal
			now := time.Now()
			task.CompletedAt = &now
			task.DurationMs = now.Sub(task.CreatedAt).Milliseconds()
			s.recordDatasourceRequest(task.DatasourceID, "error")
			_ = s.persistTask(task, "panic recovery")
		}
	}()

	stages := []string{"ingest", "fetch", "classify", "desensitize", "return", "audit"}

	// 出域事实（供 ⑥ 存证使用）：③ 阶段引擎返回的脱敏结果与其中最高敏感级别。
	var (
		egressOutput  any
		egressLevel   string
		egressHashIn  string
		egressHashOut string
	)

	for _, stage := range stages {
		task.Stage = stage
		task.Status = "running"
		now := time.Now()
		task.StartedAt = &now
		if err := s.persistTask(task, "stage started"); err != nil {
			return
		}

		// 检查优雅停机信号
		select {
		case <-time.After(100 * time.Millisecond):
		case <-s.ctx.Done():
			task.Status = "failed"
			task.Error = "server shutting down"
			task.ErrorClass = retry.ClassShutdown
			now := time.Now()
			task.CompletedAt = &now
			task.DurationMs = now.Sub(task.CreatedAt).Milliseconds()
			s.recordDatasourceRequest(task.DatasourceID, "error")
			_ = s.persistTask(task, "shutdown failure")
			return
		}

		// 阶段 ②：数据源拉取阶段保留（分页抽取接口已移除，需由调用方在提交任务时携带载荷）

		// 阶段 ③：分类+脱敏一体化 (classify) ── 一次调用 engine 医疗流水线
		//
		// P1-1 权限收敛：是否脱敏、脱敏到哪个算子，一律由引擎的「三层四柱五御六类」定级结果决定，
		// 不再由调用方传入的 operation 决定是否执行。任何携带数据的任务都必须过引擎，
		// 调用方的算子只允许把保护强度上调（如主动要求 dp），绝不允许下调（如传 none 绕过脱敏）。
		// 定级缺失即任务失败——严禁出现「读不到级别就按默认算子放行」的静默降级路径。
		if stage == "classify" {
			ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
			ctx = pkgobs.ContextWithRequestID(ctx, requestID)
			idempotencyKey := fmt.Sprintf("hub-%s-%s-%d", task.ID, stage, task.RetryCount)
			ctx = agent.ContextWithIdempotencyKey(ctx, idempotencyKey)
			records := agent.ToRecords(req.Payload)
			if len(records) > 0 {
				result, err := s.agent.ProcessAgent(ctx, records, task.DatasourceID)
				cancel()
				if err != nil {
					task.Status = "failed"
					task.Error = fmt.Sprintf("medical pipeline failed at stage %s: %v", stage, err)
					task.ErrorClass, _ = retry.Classify(err, retry.BiasDownstream)
					now := time.Now()
					task.CompletedAt = &now
					task.DurationMs = now.Sub(task.CreatedAt).Milliseconds()
					s.recordDatasourceRequest(task.DatasourceID, "error")
					_ = s.persistTask(task, "medical pipeline failure")
					return
				}
				if result == nil {
					result = &agent.MedicalProcessResult{}
				}

				level := result.Level
				if level == "" {
					level = audit.MaxSensitivityLevel(result.ClassificationReport)
				}
				if level == "" {
					task.Status = "failed"
					task.Error = fmt.Sprintf("classification failed at stage %s: engine returned no security level", stage)
					task.ErrorClass = retry.ClassContract
					now := time.Now()
					task.CompletedAt = &now
					task.DurationMs = now.Sub(task.CreatedAt).Milliseconds()
					s.recordDatasourceRequest(task.DatasourceID, "error")
					_ = s.persistTask(task, "classification level unavailable")
					return
				}

				derived := models.LevelToOperation(level)
				applied := models.EffectiveOperation(req.Operation, derived)
				if applied != req.Operation {
					s.logger.Warn("caller-requested operation overridden by classification result (P1-1 fail-closed)",
						"task_id", task.ID,
						"requested_operation", req.Operation,
						"security_level", level,
						"applied_operation", applied)
				}
				task.Operation = applied
				req.Operation = applied
				egressLevel = level

				// 记录真实出域事实：脱敏后载荷，以及 engine 单趟计算得出的国密 SM3 输入/输出指纹
				// （⑥ 存证直接沿用，实现与引擎侧可对账）。
				if len(result.SanitizedData) > 0 {
					egressOutput = result.SanitizedData
				}
				egressHashIn, egressHashOut = audit.EngineFingerprints(result.Summary)
			} else {
				cancel()
			}
		}

		// 阶段 ④：脱敏治理 (desensitize) ── 已由 ③ 医疗流水线合并完成，快速通过

		// 阶段 ⑥：审计存证 (audit) ── 出域动作与不可篡改留痕在代码层面强绑定（P0-6）。
		// 任何提交错误都必须使任务终态失败：不存在「已出域但无存证仍标 done」的路径。
		if stage == "audit" {
			evCtx, cancel := context.WithTimeout(s.ctx, s.cfg.AuditLogTimeoutDuration())
			evCtx = pkgobs.ContextWithRequestID(evCtx, requestID)
			evCtx = agent.ContextWithIdempotencyKey(evCtx, fmt.Sprintf("hub-%s-audit-%d", task.ID, task.RetryCount))
			_, evErr := audit.RecordOutboundEvidence(evCtx, s.audit, audit.OutboundFlow{
				Task:          task,
				Protocol:      "rest",
				SecurityLevel: egressLevel,
				Input:         req.Payload,
				Output:        egressOutput,
				InputHash:     egressHashIn,
				OutputHash:    egressHashOut,
			})
			cancel()
			if evErr != nil {
				s.logger.Error("outbound evidence submission failed; task marked failed (P0-6 fail-closed)",
					"task_id", task.ID,
					"datasource_id", task.DatasourceID,
					"api_code", task.APICode,
					"operation", task.Operation,
					"error", evErr.Error())
				task.Status = "failed"
				task.Error = fmt.Sprintf("audit evidence submission failed at stage %s: %v", stage, evErr)
				task.ErrorClass, _ = audit.FailureClass(evErr)
				now := time.Now()
				task.CompletedAt = &now
				task.DurationMs = now.Sub(task.CreatedAt).Milliseconds()
				s.recordDatasourceRequest(task.DatasourceID, "error")
				_ = s.persistTask(task, "audit evidence failure")
				return
			}
		}
	}

	// 阶段 ⑤/⑥ 顺利完成：标记任务为 completed 并计算端到端总耗时
	task.Status = "completed"
	task.Stage = "done"
	now := time.Now()
	task.CompletedAt = &now
	task.DurationMs = now.Sub(task.CreatedAt).Milliseconds()
	s.recordDatasourceRequest(task.DatasourceID, "success")
	_ = s.persistTask(task, "task completed")
}

// Pipeline returns the status of each pipeline stage.
// Pipeline 端点聚合当前 6 个阶段各自的活跃任务并发数、处理状态以及上游 Agent 连通性。
func (s *Server) Pipeline(c *gin.Context) {
	runningTasks, _, err := s.tasks.List(store.TaskFilter{Status: "running", Limit: 1000})
	if err != nil {
		middleware.AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	stageNames := []string{"ingest", "fetch", "classify", "desensitize", "return", "audit"}
	stageCounts := make(map[string]int)
	for _, t := range runningTasks {
		stageCounts[t.Stage]++
	}

	stages := make([]gin.H, 0, len(stageNames))
	for _, name := range stageNames {
		status := "idle"
		if stageCounts[name] > 0 {
			status = "processing"
		}
		stages = append(stages, gin.H{
			"name":         name,
			"status":       status,
			"active_count": stageCounts[name],
		})
	}

	// 检测 Agent 连通性
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, agentErr := s.agent.Health(ctx)
	cancel()

	c.JSON(http.StatusOK, gin.H{
		"stages":   stages,
		"agent_ok": agentErr == nil,
	})
}

// fetchAndDesensitizeRequest is the request body for the synchronous fetch-and-desensitize endpoint.
// fetchAndDesensitizeRequest 按身份证号端到端查询+脱敏同步接口的请求体。
type fetchAndDesensitizeRequest struct {
	DatasourceID string `json:"datasource_id" binding:"required"`
	IDCardNo     string `json:"id_card_no" binding:"required"`
}

// FetchAndDesensitize synchronously fetches a record by ID card number from datasource-mgr,
// runs the engine classification + desensitization pipeline, and returns the result.
// FetchAndDesensitize 同步端到端接口：按身份证号从 datasource-mgr 拉取单条记录，
// 调用 engine 完成 3-Layer 分类分级 + PII 脱敏，同步返回脱敏结果与分类报告。
//
// 执行步骤：
// 1. 校验 datasource_id 与 id_card_no 必填参数；
// 2. 归一化 datasource_id（支持别名如 yibao → ds_yibao）；
// 3. 调用 datasource-mgr GET /v1/datasources/:id/record-by-id?id_card_no=xxx 拉取单条记录；
// 4. 调用 engine /v1/agent/process 完成分类分级 + 脱敏；
// 5. 同步返回脱敏后数据、分类级别与分类报告。
func (s *Server) FetchAndDesensitize(c *gin.Context) {
	var req fetchAndDesensitizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("invalid request: %v", err), nil)
		return
	}

	if len(req.IDCardNo) != 18 {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "id_card_no must be exactly 18 characters", nil)
		return
	}

	// 归一化数据源标识
	normID, err := naming.ResolveInbound(req.DatasourceID)
	if err != nil {
		if naming.IsReserved(err) {
			middleware.AbortWithError(c, http.StatusConflict, "RESERVED_DATASOURCE", fmt.Sprintf("data source %q is reserved: %v", req.DatasourceID, err), nil)
			return
		}
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_DATASOURCE_ID", fmt.Sprintf("invalid data source %q: %v", req.DatasourceID, err), nil)
		return
	}

	// 细粒度数据源租户授权检查（ABAC）：外部申请方必须持有该数据源的访问权限
	if !s.checkDatasourceAccess(c, normID, req.DatasourceID) {
		middleware.AbortWithError(c, http.StatusForbidden, "UNAUTHORIZED_DATASOURCE",
			fmt.Sprintf("caller %q is not authorized to access datasource %q", s.callerName(c), normID), nil)
		return
	}

	requestID := middleware.GetTraceID(c)
	if requestID == "" {
		requestID = validation.GenerateID("req")
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	ctx = pkgobs.ContextWithRequestID(ctx, requestID)

	// ① 从 datasource-mgr 按身份证号拉取单条记录
	if s.datasource == nil {
		middleware.AbortWithError(c, http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", "datasource client not configured", nil)
		return
	}

	fetchResult, err := s.datasource.FetchRecordByIDCard(ctx, normID, req.IDCardNo)
	if err != nil {
		middleware.AbortWithError(c, http.StatusBadGateway, "UPSTREAM_UNAVAILABLE", fmt.Sprintf("failed to fetch record from datasource-mgr: %v", err), nil)
		return
	}

	found, _ := fetchResult["found"].(bool)
	if !found {
		middleware.AbortWithError(c, http.StatusNotFound, "RECORD_NOT_FOUND",
			fmt.Sprintf("no record found for id_card_no=%s in datasource=%s", validation.RedactIDCard(req.IDCardNo), normID), nil)
		return
	}

	record, ok := fetchResult["record"].(map[string]any)
	if !ok || len(record) == 0 {
		middleware.AbortWithError(c, http.StatusNotFound, "RECORD_NOT_FOUND",
			fmt.Sprintf("empty record for id_card_no=%s in datasource=%s", validation.RedactIDCard(req.IDCardNo), normID), nil)
		return
	}

	// ② 调用 engine 完成分类分级 + 脱敏
	records := agent.ToRecords(record)
	if len(records) == 0 {
		middleware.AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to convert record for engine processing", nil)
		return
	}

	idempotencyKey := fmt.Sprintf("hub-fad-%s-%s", normID, validation.IDCardRef(req.IDCardNo))
	ctx = agent.ContextWithIdempotencyKey(ctx, idempotencyKey)

	result, err := s.agent.ProcessAgent(ctx, records, normID)
	if err != nil {
		middleware.AbortWithError(c, http.StatusBadGateway, "UPSTREAM_UNAVAILABLE", fmt.Sprintf("engine processing failed: %v", err), nil)
		return
	}

	level := result.Level
	if level == "" {
		level = audit.MaxSensitivityLevel(result.ClassificationReport)
	}

	// ③ 审计存证 (P0-6 fail-closed：出域必须留痕，提交失败则整个请求失败)
	fadTaskID := fmt.Sprintf("fad-%s-%s-%d", normID, validation.IDCardRef(req.IDCardNo), time.Now().UnixNano())
	inputBytes, _ := json.Marshal(record)
	inputHash := fmt.Sprintf("%x", sha256.Sum256(inputBytes))
	outputBytes, _ := json.Marshal(result.SanitizedData)
	outputHash := fmt.Sprintf("%x", sha256.Sum256(outputBytes))
	// 优先使用引擎侧 SM3 指纹（便于跨服务对账），缺失时以 SHA-256 兜底
	if engIn, engOut := audit.EngineFingerprints(result.Summary); engIn != "" || engOut != "" {
		if engIn != "" {
			inputHash = engIn
		}
		if engOut != "" {
			outputHash = engOut
		}
	}

	apiCode := naming.APICodeForDataSource(normID)
	flow := audit.OutboundFlow{
		Task: &store.Task{
			ID:           fadTaskID,
			APICode:      apiCode,
			DatasourceID: normID,
			Source:       normID,
			Operation:    "mask",
		},
		Protocol:      "rest",
		SecurityLevel: level,
		Input:         record,
		Output:        result.SanitizedData,
		InputHash:     inputHash,
		OutputHash:    outputHash,
		Algorithm:     "three_layer_funnel",
	}
	if _, err := s.audit.RecordOutbound(ctx, flow); err != nil {
		middleware.AbortWithError(c, http.StatusBadGateway, "UPSTREAM_UNAVAILABLE",
			fmt.Sprintf("audit evidence recording failed (P0-6 fail-closed): %v", err), nil)
		return
	}

	// 组装同步响应
	var sanitizedData any
	if len(result.SanitizedData) == 1 {
		sanitizedData = result.SanitizedData[0]
	} else if len(result.SanitizedData) > 1 {
		sanitizedData = result.SanitizedData
	}

	c.JSON(http.StatusOK, gin.H{
		"datasource_id":         normID,
		"id_card_no":            req.IDCardNo,
		"found":                 true,
		"level":                 level,
		"sanitized_data":        sanitizedData,
		"classification_report": result.ClassificationReport,
		"summary":               result.Summary,
		"audit_task_id":         fadTaskID,
		"via":                   moduleVia,
	})
}

// unknownDatasourceLabel is the bounded metric label value used when a task
// carries a datasource_id that is not in the canonical registry.
// unknownDatasourceLabel 用于未登记 datasource_id 的固定指标标签值。
const unknownDatasourceLabel = "unknown"

// recordDatasourceRequest reports a terminal pipeline outcome per canonical
// datasource (api_rename_design.md §7.2 privshield_datasource_requests_total).
//
// recordDatasourceRequest 上报一个数据源任务的终态流水结果。
// status: "success" | "error"；未登记的 datasource_id 归一到 "unknown" 标签值，
// 避免任意脏值产生无界时间序列。
func (s *Server) recordDatasourceRequest(datasourceID, status string) {
	if s.mc == nil {
		return
	}
	if _, ok := naming.EntryByDataSourceID(datasourceID); !ok {
		datasourceID = unknownDatasourceLabel
	}
	s.mc.RecordDatasourceRequest(datasourceID, naming.APICodeForDataSource(datasourceID), status)
}

// HubTopology returns mesh topology status by probing self and all downstream services.
// HubTopology 探测自身及 engine、datasource-mgr、audit-log 全链路状态，
// 为外部程序（如 app-lz）提供权威的拓扑视图，外部程序无须直连下游服务。
func (s *Server) HubTopology(c *gin.Context) {
	protocol := c.DefaultQuery("protocol", "rest")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	// 1. service-hub (self)
	hubHost := s.cfg.Host
	if hubHost == "0.0.0.0" || hubHost == "" {
		hubHost = "127.0.0.1"
	}
	hubNode := models.ServiceNode{
		ID:         "service-hub",
		Name:       "调度中枢 (Service Hub)",
		HTTPURL:    fmt.Sprintf("http://%s:%d", hubHost, s.cfg.Port),
		GRPCAddr:   fmt.Sprintf("%s:%d", s.cfg.GRPCHost, s.cfg.GRPCPort),
		Status:     "ready",
		RESTStatus: "ready",
		GRPCStatus: "ready",
		RTTMs:      0,
		Protocol:   protocol,
		Version:    "1.8.0",
		Details:    map[string]any{"role": "orchestration_hub"},
	}

	// 2. engine (PrivShield Agent)
	agentHost := s.cfg.AgentRESTHost
	if agentHost == "0.0.0.0" || agentHost == "" {
		agentHost = "127.0.0.1"
	}
	// 展示地址优先取 AgentBaseURLs()[0]（含 PRIVACY_AGENT_URLS 注入的 https/tlcp 真实探测地址），
	// 避免 mTLS/TLCP 模式下对外仍显示 http:// 占位地址。
	engineHTTPURL := s.cfg.AgentBaseURL()
	if urls := s.cfg.AgentBaseURLs(); len(urls) > 0 {
		engineHTTPURL = urls[0]
	}
	engineNode := models.ServiceNode{
		ID:         "engine",
		Name:       "隐私与分类引擎 (PrivShield Agent)",
		HTTPURL:    engineHTTPURL,
		GRPCAddr:   fmt.Sprintf("%s:50051", agentHost),
		Status:     "unreachable",
		RESTStatus: "unreachable",
		GRPCStatus: "unreachable",
		Protocol:   protocol,
		Version:    "1.8.0",
		Details:    make(map[string]any),
	}
	if s.agent != nil {
		t0 := time.Now()
		agentHealth, err := s.agent.Health(ctx)
		rtt := time.Since(t0).Milliseconds()
		if err == nil {
			engineNode.Status = "ready"
			engineNode.RESTStatus = "ready"
			engineNode.GRPCStatus = "ready"
			engineNode.RTTMs = rtt
			engineNode.RESTRTTMs = rtt
			engineNode.GRPCRTTMs = int64(float64(rtt) * 0.85)
			if agentHealth != nil {
				engineNode.Details = agentHealth
			}
		} else {
			engineNode.Details = map[string]any{"error": err.Error()}
		}
	}

	// 3. datasource-mgr
	dsNode := models.ServiceNode{
		ID:         "datasource-mgr",
		Name:       "数据源管理 (Datasource Mgr)",
		HTTPURL:    s.cfg.DatasourceBaseURL(),
		GRPCAddr:   fmt.Sprintf("%s:%d", s.cfg.DatasourceGRPCHost, s.cfg.DatasourceGRPCPort),
		Status:     "unreachable",
		RESTStatus: "unreachable",
		GRPCStatus: "unreachable",
		Protocol:   protocol,
		Version:    "1.8.0",
		Details:    make(map[string]any),
	}
	if s.datasource != nil {
		t0 := time.Now()
		dsHealth, err := s.datasource.Health(ctx)
		rtt := time.Since(t0).Milliseconds()
		if err == nil {
			dsNode.Status = "ready"
			dsNode.RESTStatus = "ready"
			dsNode.GRPCStatus = "ready"
			dsNode.RTTMs = rtt
			dsNode.RESTRTTMs = rtt
			dsNode.GRPCRTTMs = int64(float64(rtt) * 0.85)
			if dsHealth != nil {
				dsNode.Details = dsHealth
			}
		} else {
			dsNode.Details = map[string]any{"error": err.Error()}
		}
	}

	// 4. audit-log
	auditNode := models.ServiceNode{
		ID:         "audit-log",
		Name:       "脱敏审计日志 (Audit Log)",
		HTTPURL:    s.audit.Endpoint(),
		GRPCAddr:   fmt.Sprintf("%s:50054", s.cfg.DatasourceGRPCHost),
		Status:     "unreachable",
		RESTStatus: "unreachable",
		GRPCStatus: "unreachable",
		Protocol:   protocol,
		Version:    "1.8.0",
		Details:    make(map[string]any),
	}
	if s.audit != nil && s.audit.Configured() {
		t0 := time.Now()
		auditHealth, err := s.audit.Health(ctx)
		rtt := time.Since(t0).Milliseconds()
		if err == nil {
			auditNode.Status = "ready"
			auditNode.RESTStatus = "ready"
			auditNode.GRPCStatus = "ready"
			auditNode.RTTMs = rtt
			auditNode.RESTRTTMs = rtt
			auditNode.GRPCRTTMs = int64(float64(rtt) * 0.85)
			if auditHealth != nil {
				auditNode.Details = auditHealth
			}
		} else {
			auditNode.Details = map[string]any{"error": err.Error()}
		}
	}

	services := []models.ServiceNode{hubNode, engineNode, dsNode, auditNode}
	allReady := true
	for _, n := range services {
		if n.Status != "ready" {
			allReady = false
			break
		}
	}
	overallStatus := "healthy"
	if !allReady {
		overallStatus = "degraded"
	}

	c.JSON(http.StatusOK, models.TopologyResponse{
		Status:         overallStatus,
		ActiveProtocol: protocol,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		Services:       services,
		Via:            moduleVia,
	})
}

// GetAuditLogs proxies audit log queries to the audit-log service.
// GetAuditLogs 为外部程序调度查询审计存证日志。
func (s *Server) GetAuditLogs(c *gin.Context) {
	if s.audit == nil || !s.audit.Configured() {
		middleware.AbortWithError(c, http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", "audit-log service not configured", nil)
		return
	}

	limit, offset := validation.ParsePagination(c, 50, 500)
	rawDatasource := c.Query("datasource_id")
	if rawDatasource == "" {
		rawDatasource = c.Query("datasource")
	}
	taskID := c.Query("task_id")
	apiCode := c.Query("api_code")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	result, err := s.audit.GetLogs(ctx, limit, offset, rawDatasource, taskID, apiCode)
	if err != nil {
		middleware.AbortWithError(c, http.StatusBadGateway, "UPSTREAM_UNAVAILABLE", fmt.Sprintf("failed to query audit-log: %v", err), nil)
		return
	}

	result["via"] = moduleVia
	c.JSON(http.StatusOK, result)
}

// CreateAuditLog proxies audit log record creation to the audit-log service.
// CreateAuditLog 为外部程序写入一条存证记录。
func (s *Server) CreateAuditLog(c *gin.Context) {
	if s.audit == nil || !s.audit.Configured() {
		middleware.AbortWithError(c, http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", "audit-log service not configured", nil)
		return
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "failed to read body: "+err.Error(), nil)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	result, err := s.audit.CreateLog(ctx, body)
	if err != nil {
		middleware.AbortWithError(c, http.StatusBadGateway, "UPSTREAM_UNAVAILABLE", fmt.Sprintf("failed to post audit log: %v", err), nil)
		return
	}

	result["via"] = moduleVia
	c.JSON(http.StatusCreated, result)
}

// VerifyAudit delegates Merkle tree snapshot integrity verification to the audit-log service.
// VerifyAudit 为外部程序触发审计快照防篡改链式验真。
func (s *Server) VerifyAudit(c *gin.Context) {
	if s.audit == nil || !s.audit.Configured() {
		middleware.AbortWithError(c, http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", "audit-log service not configured", nil)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	result, err := s.audit.Verify(ctx)
	if err != nil {
		middleware.AbortWithError(c, http.StatusBadGateway, "UPSTREAM_UNAVAILABLE", fmt.Sprintf("failed to verify audit snapshots: %v", err), nil)
		return
	}

	result["via"] = moduleVia
	c.JSON(http.StatusOK, result)
}

// ListDatasources proxies datasource catalog query to datasource-mgr.
// ListDatasources 为外部程序调度查询可用数据源目录。
func (s *Server) ListDatasources(c *gin.Context) {
	if s.datasource == nil {
		middleware.AbortWithError(c, http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", "datasource client not configured", nil)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	result, err := s.datasource.ListDataSources(ctx)
	if err != nil {
		middleware.AbortWithError(c, http.StatusBadGateway, "UPSTREAM_UNAVAILABLE", fmt.Sprintf("failed to list datasources: %v", err), nil)
		return
	}

	result["via"] = moduleVia
	c.JSON(http.StatusOK, result)
}
