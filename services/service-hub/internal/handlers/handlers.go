// Package handlers implements the HTTP REST interface for the service-hub module.
// Package handlers 实现数据服务调度中枢模块（service-hub）的 HTTP REST 控制器与 6 阶段流水线调度逻辑。
//
// ==============================================================================
// 6 阶段数据流通与安全治理调度流水线 (6-Stage Governance Pipeline)：
// ==============================================================================
//
//	① 请求接入 (ingest)   ──▶ ② 申请原数 (fetch)    ──▶ ③ 分类分级 (classify)
//	       │                           │                           │
//	       ▼                           ▼                           ▼
//	④ 下发脱敏 (desensitize) ──▶ ⑤ 结果返回 (return)   ──▶ ⑥ 审计存证 (audit / done)
//
// 路由清单 (Route List)：
//   GET  /health                         → 存活探针 (Liveness: always 200 if process is alive)
//   GET  /readyz                         → 就绪探针 (Readiness: 503 if upstream unreachable)
//   GET  /api/health                     → 标准健康检查探针
//   GET  /api/hub/status                 → 调度中枢运行状态与队列深度概览
//   GET  /api/hub/tasks                  → 分页查询任务列表 (支持 status 状态过滤)
//   GET  /api/hub/tasks/:id              → 根据 TaskID 查询单个任务详情
//   POST /api/hub/dispatch               → 直接分发指定算子的隐私处理任务 (API1/2/3/4 核心)
//   GET  /api/hub/pipeline               → 6 阶段流水线监控遥测与 QPS 统计
//   GET  /metrics                        → Prometheus 格式监控指标导出端点
// ==============================================================================

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fengzhizi319/PrivShield/pkg/metrics"
	"github.com/fengzhizi319/PrivShield/pkg/middleware"
	naming "github.com/fengzhizi319/PrivShield/pkg/naming"
	"github.com/fengzhizi319/PrivShield/pkg/store"
	"github.com/fengzhizi319/PrivShield/pkg/validation"

	"github.com/fengzhizi319/PrivShield/services/service-hub/internal/agent"
	"github.com/fengzhizi319/PrivShield/services/service-hub/internal/audit"
	"github.com/fengzhizi319/PrivShield/services/service-hub/internal/config"
	"github.com/fengzhizi319/PrivShield/services/service-hub/internal/datasource"
	"github.com/fengzhizi319/PrivShield/services/service-hub/internal/models"
	"github.com/fengzhizi319/PrivShield/services/service-hub/internal/retry"
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
	agent      *agent.Client      // 上游 PrivShield Python Agent 客户端
	datasource *datasource.Client // 下游 datasource-mgr 数据源服务客户端
	audit      *audit.Client      // audit-log 存证客户端（P0-6：出域 ↔ 留痕强绑定）
	cfg        *config.Config     // 模块全局运行配置
	startTime  time.Time          // 服务启动时间戳（用于计算 Uptime）
	tasks      store.TaskStore    // 任务持久化存储介质（SQLite 或内存实现）
	logger     *slog.Logger       // 结构化日志记录器
	mc         *metrics.Collector // Prometheus 监控指标收集器
	taskSem    chan struct{}      // 信号量通道，限制后台最大并发任务协程数（默认 10）
	ctx        context.Context    // 用于在服务停机时向所有在途任务协程广播取消信号的父 Context
	cancel     context.CancelFunc // 触发优雅停机 Context 取消的回调函数
	wg         sync.WaitGroup     // 等待组，跟踪记录正在执行的后台任务协程
}

// New creates a new Server instance.
// New 构造函数初始化 Server 实例，并默认分配容量为 10 的并发任务信号量与优雅停机上下文。
//
// 存证客户端在此无条件装配：即使 SERVICE_HUB_AUDIT_LOG_URLS 未配置也保留实例，
// 其提交必然返回 audit.ErrNotConfigured，由流水线 audit 阶段将任务判定为 failed（fail-closed），
// 绝不允许「没有存证链路却把出域任务标成 done」。测试通过配置中的 AuditLogBaseURLs 指向桩服务。
func New(ag *agent.Client, ds *datasource.Client, cfg *config.Config, tasks store.TaskStore, logger *slog.Logger, mc *metrics.Collector) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		agent:      ag,
		datasource: ds,
		audit:      audit.New(cfg, mc),
		cfg:        cfg,
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
// 2. StructuredLogger: 输出包含延迟、状态码、IP 的结构化 JSON/Text 日志
// 3. Recovery: 拦截 Handler Panic 并返回 500 JSON
// 4. SecurityHeaders: 注入 CSP、HSTS、X-Content-Type-Options 等安全防护头
// 5. MaxBodySize: 限制请求体最大 32 MiB，防御超大 Body 内存溢出
// 6. MaxConcurrent: 限制在途请求并发上限（1000），超限返回 503
// 7. CORS: 跨域来源校验与预检放行
// 8. Auth: 基于 Authorization Bearer 的 API Key 鉴权校验
func (s *Server) RegisterRoutes(r *gin.Engine) {
	r.Use(middleware.TraceMiddleware())
	r.Use(middleware.StructuredLogger(s.logger, "service-hub"))
	r.Use(middleware.Recovery(s.logger, "service-hub"))
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.MaxBodySize(32 << 20)) // 32 MiB 请求体最大保护
	r.Use(middleware.MaxConcurrent(1000))   // 并发在途请求上限，超限返回 503
	if s.cfg.RateLimitRPS > 0 {
		r.Use(middleware.RateLimit(s.cfg.RateLimitRPS, s.cfg.RateLimitBurst)) // 每客户端 IP 令牌桶限流
	}
	r.Use(middleware.CORS(s.cfg.CORSOrigins))
	r.Use(middleware.Auth(s.cfg.APIKey))

	// 基础健康检查与服务概览
	r.GET("/health", s.Health)     // Liveness probe / 存活探针
	r.GET("/readyz", s.Readyz)     // Readiness probe / 就绪探针
	r.GET("/api/health", s.Health) // Alias for backward compat / 向后兼容别名
	r.GET("/api/hub/status", s.HubStatus)

	// 任务生命周期管理
	r.GET("/api/hub/tasks", s.ListTasks)
	r.GET("/api/hub/tasks/:id", s.GetTask)
	r.POST("/api/hub/dispatch", s.Dispatch)
	r.POST("/api/hub/classify", s.Dispatch) // Backward compatible alias for classify dispatch

	// 流水线监控遥测
	r.GET("/api/hub/pipeline", s.Pipeline)

	// Prometheus 监控指标导出
	r.GET("/metrics", s.mc.Handler())
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
// ② 申请原数 (fetch)：若 Payload 为空，自动向 datasource-mgr 发起远程抽样获取数据；
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

		// 阶段 ②：申请原数 (fetch) ── 若载荷为空，自动向数据源微服务拉取数据
		if stage == "fetch" && s.datasource != nil {
			if req.Payload == nil || isEmptyPayload(req.Payload) {
				ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
				ctx = agent.ContextWithRequestID(ctx, requestID)
				if res, err := s.datasource.FetchData(ctx, req.DatasourceID, 10, 0); err == nil && len(res.Records) > 0 {
					req.Payload = res.Records
					payloadBytes, _ := json.Marshal(req.Payload)
					task.PayloadJSON = string(payloadBytes)
					if err := s.persistTask(task, "payload fetched"); err != nil {
						cancel()
						return
					}
				}
				cancel()
			}
		}

		// 阶段 ③：分类+脱敏一体化 (classify) ── 一次调用 engine 医疗流水线
		//
		// P1-1 权限收敛：是否脱敏、脱敏到哪个算子，一律由引擎的「三层四柱五御六类」定级结果决定，
		// 不再由调用方传入的 operation 决定是否执行。任何携带数据的任务都必须过引擎，
		// 调用方的算子只允许把保护强度上调（如主动要求 dp），绝不允许下调（如传 none 绕过脱敏）。
		// 定级缺失即任务失败——严禁出现「读不到级别就按默认算子放行」的静默降级路径。
		if stage == "classify" {
			ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
			ctx = agent.ContextWithRequestID(ctx, requestID)
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
			evCtx = agent.ContextWithRequestID(evCtx, requestID)
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

// isEmptyPayload checks whether a generic payload is nil or empty.
// isEmptyPayload 检查通用 Payload 是否为空（nil、空字符串、空 JSON 对象或空切片）。
func isEmptyPayload(p any) bool {
	if p == nil {
		return true
	}
	switch v := p.(type) {
	case string:
		return v == "" || v == "{}" || v == "[]"
	case map[string]any:
		return len(v) == 0
	case []any:
		return len(v) == 0
	case []map[string]any:
		return len(v) == 0
	}
	return false
}
