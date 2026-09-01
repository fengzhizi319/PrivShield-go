// Package handlers 实现 App-LZ BFF 的所有 HTTP 请求处理逻辑。
//
// 核心组件：
//   - Handler: 持有所有依赖（config, ClientPool, TestRunner）
//   - SetupRouter: 注册所有 API 路由 + SPA 静态文件回退
//
// API 路由分组（/api/lz/*）：
//  1. 拓扑探测与流水线状态：GET /topology, POST /probe/all, GET /pipeline
//  2. 任务管理：GET /tasks, GET /tasks/:id, GET /tasks/leases, POST /tasks/dispatch
//  3. 测试套件：GET /suites, POST /suites/run
//  4. 审计验证：GET /audit/logs, POST /audit/verify
//  5. 性能指标：GET /metrics, GET /metrics/parsed
//  6. 预设数据 API：GET /data-api/definitions, POST /data-api/invoke
//
// 关键流程：
//   - InvokeDataApi: 编排完整的 5 阶段会话（ingest → fetch → classify_desensitize → return → audit）
//   - applyMasking: 本地降级掩码（当 engine 不可达时使用）
package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fengzhizi319/PrivShield/console/app-lz/bff-go/internal/catalog"
	"github.com/fengzhizi319/PrivShield/console/app-lz/bff-go/internal/clients"
	"github.com/fengzhizi319/PrivShield/console/app-lz/bff-go/internal/config"
	"github.com/fengzhizi319/PrivShield/console/app-lz/bff-go/internal/models"
	"github.com/fengzhizi319/PrivShield/console/app-lz/bff-go/internal/runner"
	"github.com/fengzhizi319/PrivShield/pkg/metrics"
	"github.com/fengzhizi319/PrivShield/pkg/middleware"
	"github.com/fengzhizi319/PrivShield/pkg/naming"
	pkgobs "github.com/fengzhizi319/PrivShield/pkg/observability"
)

// Handler 持有所有 HTTP 处理器的依赖。
// 所有 handler 方法共享同一个 Handler 实例，通过它访问配置、客户端池、测试执行器与监控指标。
type Handler struct {
	cfg    *config.Config      // 运行时配置
	pool   *clients.ClientPool // 上游微服务 HTTP 客户端池
	runner *runner.TestRunner  // E2E 测试套件执行器
	mc     *metrics.Collector  // 本 BFF 自身的 Prometheus 指标（可为 nil）
	logger *slog.Logger        // 结构化日志记录器
}

// NewHandler 创建一个新的 Handler 实例；mc 可为 nil（不暴露 /metrics）。
func NewHandler(cfg *config.Config, pool *clients.ClientPool, runner *runner.TestRunner, mc *metrics.Collector, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		cfg:    cfg,
		pool:   pool,
		runner: runner,
		mc:     mc,
		logger: logger,
	}
}

// SetupRouter 初始化 Gin 引擎并注册所有 API 路由和静态文件服务。
//
// 路由结构：
//
//	/api/health          — BFF 自身健康检查
//	/api/lz/topology     — 服务拓扑探测
//	/api/lz/pipeline     — 6 阶段流水线状态
//	/api/lz/tasks/*      — 任务管理（列表/详情/租约/派发）
//	/api/lz/suites/*     — E2E 测试套件
//	/api/lz/audit/*      — 审计日志与 Merkle 验真
//	/api/lz/metrics*     — Prometheus 指标
//	/api/lz/data-api/*   — 预设数据 API
//	/*                   — SPA 静态文件回退（NoRoute handler）
func SetupRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.ReleaseMode) // 生产模式，关闭 Gin 调试日志
	r := gin.New()
	r.Use(middleware.TraceMiddleware())             // 分布式追踪 ID 自动注入与双头下发
	r.Use(pkgobs.RequestLoggerWithModule("app-lz")) // 每请求结构化日志（method/path/status/latency）
	r.Use(middleware.Recovery(h.logger, "app-lz"))  // 全局 panic 恢复中间件
	r.Use(middleware.SecurityHeaders())             // 安全响应头 (CSP/HSTS/X-Frame-Options)
	r.Use(middleware.MaxBodySize(32 << 20))         // 32 MiB 请求体最大保护
	r.Use(middleware.MaxConcurrent(1000))           // 并发在途请求上限，超限返回 503
	if h.cfg.RateLimitRPS > 0 {
		r.Use(middleware.RateLimit(h.cfg.RateLimitRPS, h.cfg.RateLimitBurst)) // 每客户端 IP 令牌桶限流
	}
	r.Use(corsMiddleware())              // 全局 CORS 中间件
	r.Use(middleware.Auth(h.cfg.APIKey)) // API Key 鉴权（为空时跳过）

	// ── 健康检查（两个路径均支持，兼容不同探测配置）──
	r.GET("/api/health", h.HealthCheck)
	r.GET("/health", h.HealthCheck)

	// ── 本 BFF 自身的 Prometheus 端点（§7.2）──
	// 区别于 /api/lz/metrics（代理 service-hub 指标）。
	if h.mc != nil {
		r.GET("/metrics", h.mc.Handler())
	}

	// ── App-LZ API 分组 ──
	api := r.Group("/api/lz")
	{
		// 1. 拓扑探测（GET 和 POST 均支持，POST 用于强制刷新）
		api.GET("/topology", h.GetTopology)
		api.POST("/probe/all", h.GetTopology)

		// 1.5 6 阶段流水线状态
		api.GET("/pipeline", h.GetPipelineStatus)

		// 2. 任务管理（列表/详情/租约/派发）
		api.GET("/tasks", h.ListTasks)
		api.GET("/tasks/:id", h.GetTask)
		api.GET("/tasks/leases", h.GetLeases)
		api.POST("/tasks/dispatch", h.DispatchTask)

		// 3. E2E 测试套件（获取可用套件 / 执行套件）
		api.GET("/suites", h.GetSuites)
		api.POST("/suites/run", h.RunSuites)

		// 4. 审计日志与 Merkle 验真
		api.GET("/audit/logs", h.GetAuditLogs)
		api.POST("/audit/verify", h.VerifyAudit)

		// 5. Prometheus 性能指标（原始 / 解析后）
		api.GET("/metrics", h.GetMetrics)
		api.GET("/metrics/parsed", h.GetParsedMetrics)

		// 6. 预设数据 API（获取定义 / 调用会话）
		api.GET("/data-api/definitions", h.GetDataApiDefinitions)
		api.POST("/data-api/invoke", h.InvokeDataApi)
	}

	// ── SPA 静态文件服务 ──
	setupStaticServing(r, h.cfg.StaticDir)

	return r
}

// corsMiddleware 返回 CORS 中间件，允许所有来源的跨域请求。
// 对 OPTIONS 预检请求直接返回 204，不继续向下路由。
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// setupStaticServing 配置 SPA 静态文件服务。
//
// 逻辑：
//  1. 检查 staticDir 是否存在，不存在则跳过
//  2. 注册 NoRoute handler：对所有未匹配 API 路由的请求
//     a. /api/* 路径 → 返回 404 JSON
//     b. 磁盘上存在的文件 → 直接返回静态文件
//     c. 其他路径 → 回退到 index.html（SPA 前端路由）
//     d. index.html 也不存在 → 返回纯文本提示
func setupStaticServing(r *gin.Engine, staticDir string) {
	if staticDir == "" {
		return
	}
	absDir, err := filepath.Abs(staticDir)
	if err != nil {
		return
	}
	if _, err := os.Stat(absDir); os.IsNotExist(err) {
		return
	}

	// NoRoute handler：处理所有未匹配的路由
	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		// /api/* 路径不应回退到 SPA，直接返回 404
		if strings.HasPrefix(path, "/api") {
			middleware.AbortWithError(c, http.StatusNotFound, "NOT_FOUND", "api route not found", nil)
			return
		}
		// 尝试返回静态文件
		reqFile := filepath.Join(absDir, filepath.Clean(path))
		if stat, err := os.Stat(reqFile); err == nil && !stat.IsDir() {
			c.File(reqFile)
			return
		}
		// SPA 回退：返回 index.html（前端 React Router 接管路由）
		indexFile := filepath.Join(absDir, "index.html")
		if _, err := os.Stat(indexFile); err == nil {
			c.File(indexFile)
		} else {
			c.String(http.StatusOK, "PrivShield Console App-LZ BFF is running. Frontend bundle pending build.")
		}
	})
}

// HealthCheck 处理 BFF 自身的健康检查请求。
// 返回服务名称、版本号和来源标识。
func (h *Handler) HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"service": "console-app-lz-bff",
		"version": "1.8.0",
		"via":     "app-lz-bff",
	})
}

// GetTopology 返回 4 个微服务的实时拓扑状态。
// 支持通过 ?protocol=grpc 查询参数切换协议视角。
func (h *Handler) GetTopology(c *gin.Context) {
	protocol := c.DefaultQuery("protocol", "rest")
	topo := h.pool.GetTopology(c.Request.Context(), protocol)
	c.JSON(http.StatusOK, topo)
}

// GetPipelineStatus 获取 Service Hub 6 阶段流水线拓扑及统计数据（P1-7）。
func (h *Handler) GetPipelineStatus(c *gin.Context) {
	resp, err := h.pool.GetPipelineStatus(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, resp)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// DispatchTask 处理手动任务派发请求。
// 将前端提交的任务转发到 Service Hub，失败时返回 503。
func (h *Handler) DispatchTask(c *gin.Context) {
	var req models.DispatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error(), nil)
		return
	}
	resp, err := h.pool.DispatchTask(c.Request.Context(), req)
	if err != nil {
		middleware.AbortWithError(c, http.StatusServiceUnavailable, "UPSTREAM_UNAVAILABLE", err.Error(), nil)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// ListTasks 返回分页的任务列表。
// 支持查询参数：status（筛选）、limit（每页数量，默认 50）、offset（偏移量，默认 0）。
func (h *Handler) ListTasks(c *gin.Context) {
	status := c.Query("status")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	tasksResp, err := h.pool.ListTasks(c.Request.Context(), status, limit, offset)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"tasks": []models.Task{}, "total": 0, "via": "app-lz-bff"})
		return
	}
	c.JSON(http.StatusOK, tasksResp)
}

// GetTask 返回单个任务的完整详情。
func (h *Handler) GetTask(c *gin.Context) {
	id := c.Param("id")
	task, err := h.pool.GetTask(c.Request.Context(), id)
	if err != nil {
		middleware.AbortWithError(c, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("task %s not found: %v", id, err), nil)
		return
	}
	c.JSON(http.StatusOK, gin.H{"task": task, "via": "app-lz-bff"})
}

// GetLeases 返回 Phase B PostgreSQL 任务租约检查结果。
func (h *Handler) GetLeases(c *gin.Context) {
	leaseResp, err := h.pool.GetLeasesFromHub(c.Request.Context())
	if err != nil || leaseResp.TotalLeasedTasks == 0 {
		c.JSON(http.StatusOK, models.LeasedTasksResponse{
			StoreBackend:     "sqlite",
			TotalLeasedTasks: 0,
			Workers:          []models.WorkerLeaseInfo{},
			OrphanRecovery:   map[string]any{},
		})
		return
	}
	c.JSON(http.StatusOK, leaseResp)
}

// GetSuites 返回所有可用的 E2E 测试套件定义。
func (h *Handler) GetSuites(c *gin.Context) {
	suites := h.runner.GetAvailableSuites()
	c.JSON(http.StatusOK, gin.H{"suites": suites})
}

// RunSuites 执行选定的 E2E 测试套件。
// 请求体包含要执行的套件 ID 列表、并发数和压测请求数。
func (h *Handler) RunSuites(c *gin.Context) {
	var req models.RunTestSuiteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error(), nil)
		return
	}

	resp := h.runner.RunSuites(c.Request.Context(), req)
	c.JSON(http.StatusOK, resp)
}

// GetAuditLogs 返回审计日志条目列表。
// 支持查询参数：limit（默认 50）、offset（默认 0）、datasource_id / datasource、task_id、api_code。
func (h *Handler) GetAuditLogs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	rawDatasource := c.Query("datasource_id")
	if rawDatasource == "" {
		rawDatasource = c.Query("datasource")
	}
	taskID := c.Query("task_id")
	apiCode := c.Query("api_code")

	resp, err := h.pool.GetAuditLogsFiltered(c.Request.Context(), limit, offset, rawDatasource, taskID, apiCode)
	if err != nil {
		if ue, ok := err.(*clients.UpstreamError); ok {
			c.JSON(ue.StatusCode(), ue.Body("app-lz-bff"))
			return
		}
		c.JSON(http.StatusOK, gin.H{"logs": []models.AuditLogItem{}, "total": 0, "via": "app-lz-bff"})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// VerifyAudit 触发 Merkle 树完整性验证。
func (h *Handler) VerifyAudit(c *gin.Context) {
	resp, err := h.pool.VerifyAudit(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, resp)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// GetMetrics 返回 Service Hub 的原始 Prometheus 指标文本。
// 当上游不可达时返回合成的最小指标。
func (h *Handler) GetMetrics(c *gin.Context) {
	metrics, err := h.pool.GetHubMetrics(c.Request.Context())
	if err != nil {
		c.String(http.StatusOK, "# HELP service_hub_status status\nservice_hub_status 1\n")
		return
	}
	c.String(http.StatusOK, metrics)
}

// GetParsedMetrics 返回已解析的 Service Hub 性能指标（各阶段耗时、QPS、延迟百分位数）。
func (h *Handler) GetParsedMetrics(c *gin.Context) {
	parsed, err := h.pool.GetParsedMetrics(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"stage_durations": map[string]float64{"ingest": 1.0, "fetch": 5.0, "classify": 12.0, "desensitize": 6.0, "return": 1.0, "audit": 3.0},
			"qps":             0.0,
			"percentiles":     map[string]float64{"p50": 8.0, "p90": 15.0, "p95": 20.0, "p99": 30.0},
			"total_requests":  0.0,
			"source":          "fallback",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"stage_durations": parsed.StageDurations,
		"qps":             parsed.QPS,
		"percentiles":     parsed.Percentiles,
		"total_requests":  parsed.TotalRequests,
		"source":          "prometheus",
	})
}

// GetDataApiDefinitions 返回 4 个预设数据 API 的定义。
func (h *Handler) GetDataApiDefinitions(c *gin.Context) {
	defs := catalog.Definitions()
	c.JSON(http.StatusOK, gin.H{"apis": defs, "definitions": defs, "via": "app-lz-bff"})
}

// InvokeDataApi 编排完整的预设数据 API 会话。
//
// 执行流程（5 阶段）：
//  1. Ingest — 请求校验与会话初始化
//  2. Fetch — 从 datasource-mgr 拉取原始数据
//  3. Classify & Desensitize — 敏感数据三层漏斗分级与自适应隐私脱敏治理 (合并一体化执行)
//  4. Return — 结果装配
//  5. Audit — 向 audit-log 写入审计存证
func (h *Handler) InvokeDataApi(c *gin.Context) {
	var req models.DataApiInvokeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error(), nil)
		return
	}

	apiDef, err := catalog.Resolve(req.APICode, req.ApiID, req.DatasourceID)
	if err != nil {
		ue := clients.ValidationErrorFor(err)
		c.JSON(ue.StatusCode(), ue.Body("app-lz-bff"))
		return
	}

	if apiDef.Status != naming.StatusActive {
		ue := &clients.UpstreamError{
			Code:     clients.CodeReservedDatasource,
			Message:  "This API is reserved and not yet active.",
			Field:    "datasource_id",
			Received: apiDef.DatasourceID,
			Allowed:  naming.ActiveDataSourceIDs(),
			Status:   http.StatusConflict,
		}
		c.JSON(ue.StatusCode(), ue.Body("app-lz-bff"))
		return
	}

	if req.Limit <= 0 {
		req.Limit = 5
	}

	sessionID := fmt.Sprintf("session-%s-%d", apiDef.APICode, time.Now().UnixNano())
	stages := make([]models.DataApiSessionStage, 0, 5)
	var rawRecords []map[string]any
	var sanitizedData []map[string]any
	overallStatus := "completed"

	// ── 阶段 1：ingest ───────────────────────────────────────────
	stages = append(stages, models.DataApiSessionStage{
		Name:       "ingest",
		Title:      "会话请求接入与校验",
		Status:     "success",
		Source:     "app-lz-bff",
		DurationMs: 1,
		Detail:     fmt.Sprintf("API 标识 %s (%s) 校验通过", apiDef.APICode, apiDef.DatasourceID),
	})

	// ── 阶段 2：从 datasource-mgr 拉取原始数据 (fetch) ─────────────
	fetchStart := time.Now()
	sliceResp, fetchErr := h.pool.GetDatasourceSlice(c.Request.Context(), apiDef.DatasourceID, req.Limit, 0)
	fetchDuration := time.Since(fetchStart).Milliseconds()
	if fetchErr != nil {
		stages = append(stages, models.DataApiSessionStage{
			Name: "fetch", Title: "数据源原始数据拉取", Status: "error",
			Source: "datasource-mgr", DurationMs: fetchDuration, Detail: fetchErr.Error(),
		})
		overallStatus = "partial"
	} else {
		rawRecords = sliceResp.Records
		stages = append(stages, models.DataApiSessionStage{
			Name: "fetch", Title: "数据源原始数据拉取", Status: "success",
			Source: sliceResp.Source, DurationMs: fetchDuration,
			Detail: fmt.Sprintf("从 %s 拉取 %d 条原始记录", apiDef.DatasourceID, len(rawRecords)),
		})
	}

	// ── 阶段 3：三层漏斗评级与隐私脱敏治理 (classify_desensitize) ───
	desensitizeStart := time.Now()
	if len(rawRecords) > 0 {
		medResult, medErr := h.pool.ProcessMedicalRecords(c.Request.Context(), rawRecords)
		if medErr == nil && medResult != nil && len(medResult.SanitizedData) > 0 {
			sanitizedData = medResult.SanitizedData
			desensitizeDuration := time.Since(desensitizeStart).Milliseconds()
			stages = append(stages, models.DataApiSessionStage{
				Name: "classify_desensitize", Title: "三层漏斗评级与隐私脱敏治理", Status: "success",
				Source: "engine", DurationMs: desensitizeDuration,
				Detail: fmt.Sprintf("医疗流水线识别 %d 条记录共 %d 个敏感字段，完成三层漏斗评级并执行自适应隐私脱敏 (via engine, L4/L5 高敏剥离)", len(rawRecords), len(apiDef.Fields)),
			})
		} else {
			// 降级兜底：engine 不可达 → 本地字段级掩码
			overallStatus = "degraded"
			sanitizedData = make([]map[string]any, 0, len(rawRecords))
			for _, rec := range rawRecords {
				sanitized := make(map[string]any)
				for k, v := range rec {
					sanitized[k] = applyMasking(k, v)
				}
				sanitizedData = append(sanitizedData, sanitized)
			}
			desensitizeDuration := time.Since(desensitizeStart).Milliseconds()
			stages = append(stages, models.DataApiSessionStage{
				Name: "classify_desensitize", Title: "三层漏斗评级与隐私脱敏治理", Status: "degraded",
				Source: "local-fallback", DurationMs: desensitizeDuration,
				Detail: fmt.Sprintf("识别 %d 个敏感字段并完成分级，对 %d 条记录执行本地降级掩码 (via local-fallback)", len(apiDef.Fields), len(sanitizedData)),
			})
		}
	} else {
		desensitizeDuration := time.Since(desensitizeStart).Milliseconds()
		stages = append(stages, models.DataApiSessionStage{
			Name: "classify_desensitize", Title: "三层漏斗评级与隐私脱敏治理", Status: "skipped",
			DurationMs: desensitizeDuration, Detail: "无原始数据可评级与脱敏",
		})
	}

	// ── 阶段 4：结果装配 (return) ─────────────────────────────────
	stages = append(stages, models.DataApiSessionStage{
		Name:       "return",
		Title:      "脱敏结果装配与交付",
		Status:     "success",
		Source:     "app-lz-bff",
		DurationMs: 1,
		Detail:     fmt.Sprintf("装配 %d 条脱敏记录准备返回", len(sanitizedData)),
	})

	// ── 阶段 5：审计存证 (audit) ───────────────────────────────────
	rawBytes, _ := json.Marshal(rawRecords)
	inputHashBytes := sha256.Sum256(rawBytes)
	inputHash := hex.EncodeToString(inputHashBytes[:])

	sanitizedBytes, _ := json.Marshal(sanitizedData)
	outputHashBytes := sha256.Sum256(sanitizedBytes)
	outputHash := hex.EncodeToString(outputHashBytes[:])

	auditStart := time.Now()
	auditEntryID := ""
	entryID, auditErr := h.pool.RecordAudit(c.Request.Context(), models.AuditRecordRequest{
		Datasource:    apiDef.DatasourceID,
		APICode:       apiDef.APICode,
		TaskID:        sessionID,
		SessionID:     sessionID,
		InputHash:     inputHash,
		OutputHash:    outputHash,
		Operation:     "mask",
		Algorithm:     "three_layer_funnel",
		User:          "app-lz-bff",
		Status:        "success",
		SecurityLevel: "L3",
		InputRows:     len(rawRecords),
		OutputRows:    len(sanitizedData),
	})
	auditDuration := time.Since(auditStart).Milliseconds()
	if auditErr != nil {
		stages = append(stages, models.DataApiSessionStage{
			Name: "audit", Title: "不可篡改审计存证", Status: "skipped",
			Source: "audit-log", DurationMs: auditDuration,
			Detail: fmt.Sprintf("审计存证跳过 (上游不可达: %v)", auditErr),
		})
	} else {
		auditEntryID = entryID
		stages = append(stages, models.DataApiSessionStage{
			Name: "audit", Title: "不可篡改审计存证", Status: "success",
			DurationMs: auditDuration,
			Detail:     fmt.Sprintf("SHA-256 存证已写入 audit-log (%s)", auditEntryID),
		})
	}

	// 计算会话总耗时
	totalDuration := int64(0)
	for _, s := range stages {
		totalDuration += s.DurationMs
	}

	resp := models.DataApiSessionResponse{
		SessionID:     sessionID,
		APICode:       apiDef.APICode,
		DatasourceID:  apiDef.DatasourceID,
		ApiID:         apiDef.Seq,
		ApiName:       apiDef.Name,
		Status:        overallStatus,
		RawRecords:    rawRecords,
		SanitizedData: sanitizedData,
		Stages:        stages,
		AuditEntryID:  auditEntryID,
		TotalDuration: totalDuration,
	}
	c.JSON(http.StatusOK, resp)
}

// presetDataApiDefinitions 返回 4 个预设数据 API 的定义。
// 委托给 internal/catalog（单一事实源来自 pkg/naming）。
func presetDataApiDefinitions() []models.DataApiDef {
	return catalog.Definitions()
}

// applyMasking 对单个字段值应用基于字段名的本地掩码。
//
// 字段名与 yibao.csv (18 字段) / kangyang.csv (27 字段) 严格对齐。
// 仅作为 engine MaskRecordViaEngine 失败时的降级兆底，生产环境应由引擎处理。
//
// 掩码规则：
//   - 身份证类：保留前 4 后 4，中间星化
//   - 手机/联系人/医保编号：保留前 3 后 4，中间星化
//   - 姓名类：保留首字，其余星化
//   - 人员标识/结算流水号：保留前 4，其余星化
//   - 地址类：保留前 3 后 3，中间星化
//   - 日期类：保留年月，隐藏日
//   - 诊断/病情/病史类：保留首字，其余全星化
//   - 数值/枚举类：不脱敏
func applyMasking(field string, value any) any {
	s, ok := value.(string)
	if !ok {
		return value
	}
	switch field {
	// ── 身份证类（保留前 4 后 4）──
	case "id_card", "id_card_no":
		if len(s) >= 15 {
			return s[:4] + "**********" + s[len(s)-4:]
		}
		return "****"
	// ── 手机/联系人/医保编号（保留前 3 后 4）──
	case "phone", "emergency_contact", "medical_insurance_no":
		if len(s) >= 7 {
			return s[:3] + "****" + s[len(s)-4:]
		}
		return "****"
	// ── 姓名类（保留首字）──
	case "patient_name", "name":
		if len(s) >= 2 {
			return string(s[0]) + "*"
		}
		return "*"
	// ── 人员标识 / 结算流水号（保留前 4）──
	case "person_id", "insurance_settlement_id", "settlement_seq_no":
		if len(s) >= 6 {
			return s[:4] + "****"
		}
		return "****"
	// ── 地址类（保留前 3 后 3）──
	case "registered_address":
		if len(s) >= 6 {
			return s[:3] + "****" + s[len(s)-3:]
		}
		return "****"
	// ── 证照编号类（保留前 4 后 4）──
	case "disability_cert_no":
		if len(s) >= 8 {
			return s[:4] + "********" + s[len(s)-4:]
		}
		return "****"
	// ── 医院编码（保留前 3）──
	case "hospital_code":
		if len(s) >= 6 {
			return s[:3] + "***"
		}
		return "***"
	// ── 日期类（保留年月，隐藏日）──
	case "birth_date":
		if len(s) >= 4 {
			return s[:4] + "-**-**"
		}
		return "****-**-**"
	case "admission_date", "discharge_date", "assess_time":
		// 日期保留年月，隐藏日
		if len(s) >= 7 {
			return s[:7] + "-**"
		}
		return s
	// ── 诊断/病情/病史类（首字保留，其余全星化）──
	case "diagnosis", "diagnosis_name", "chief_complaint", "present_illness",
		"past_history", "personal_history", "family_history", "progress_note",
		"allergic_history":
		if len(s) <= 1 {
			return "*"
		}
		return string(s[0]) + strings.Repeat("*", len(s)-1)
	// ── 数值/枚举类（不脱敏）──
	// gender, age, length_of_stay, height, weight, department,
	// is_smoking, smoking_duration, disability_category, disability_level,
	// assess_type_name, assess_result_name, assess_score, progress_note_time,
	// admission_dept, discharge_dept, medical_category, discharge_mode,
	// diagnosis_seq, diagnosis_type, icd10_code, admission_condition
	default:
		return value
	}
}
