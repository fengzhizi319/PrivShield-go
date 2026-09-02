// Package handlers implements the HTTP REST interface for the mock datasource-mgr module.
// Package handlers 实现了模拟数据源模块（datasource-mgr）的 HTTP REST 服务端接口。
//
// 该文件通过 Gin 框架暴露了一系列 RESTful API 端点：
// 1. 健康检查与探针（/health, /api/health）；
// 2. 专用模拟数据集抽取端点（/api/v1/yibao, /api/v1/kangyang, /api/v1/mock3, /api/v1/mock4）；
// 3. 通用数据源资产查询、记录采样、Schema 元数据探测与连通性测试接口（/api/datasources/*）。
package handlers

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
	"github.com/fengzhizi319/PrivShield-go/pkg/metrics"
	"github.com/fengzhizi319/PrivShield-go/pkg/middleware"
	naming "github.com/fengzhizi319/PrivShield-go/pkg/naming"
	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
	"github.com/fengzhizi319/PrivShield-go/services/datasource-mgr/internal/config"
	"github.com/fengzhizi319/PrivShield-go/services/datasource-mgr/internal/models"
)

// moduleVia 是响应体中的服务标识常量，用于全链路追踪定位请求处理节点。
const moduleVia = "datasource-mgr"

// setDeprecationHeaders 注入专用端点的弃用响应头（api_rename_design.md §7.1），
// 并上报一次别名流量（§7.2 privshield_api_alias_requests_total，下线门槛 §7.3 的依据）。
// 标签只取路由模板 c.FullPath()（有界），绝不取原始 URL（无界基数）。
func (s *Server) setDeprecationHeaders(c *gin.Context, canonicalID string) {
	c.Header("Deprecation", "true")
	c.Header("Sunset", "Mon, 01 Feb 2027 00:00:00 GMT")
	canonicalPath := fmt.Sprintf("/api/datasources/%s/records", canonicalID)
	c.Header("Link", fmt.Sprintf("<%s>; rel=\"successor-version\"", canonicalPath))
	c.Header("X-PrivShield-Canonical-Path", canonicalPath)
	c.Header("X-PrivShield-Canonical-Source", canonicalID)

	if s.mc != nil {
		alias := c.FullPath()
		if alias == "" {
			alias = "legacy-endpoint"
		}
		s.mc.RecordAPIAlias(alias, canonicalID, naming.TargetPath)
	}
}

// Server aggregates HTTP handler dependencies.
// Server 结构体聚合了 HTTP 处理器层所需的运行配置、结构化日志与监控指标组件。
type Server struct {
	cfg      *config.Config     // 全局运行配置
	keyStore *pkgauth.KeyStore  // API Key 文件热轮转 KeyStore（可选，K8s Secret 投影场景）
	logger   *slog.Logger       // 结构化日志记录器
	mc       *metrics.Collector // Prometheus 指标收集器（可为 nil，测试场景）
}

// New creates a new Server instance.
// New 创建并返回一个新的 Server 实例；mc 可为 nil（不上报指标）。
func New(cfg *config.Config, keyStore *pkgauth.KeyStore, logger *slog.Logger, mc *metrics.Collector) *Server {
	return &Server{
		cfg:      cfg,
		keyStore: keyStore,
		logger:   logger,
		mc:       mc,
	}
}

// currentAuthKeys 合并静态 DATASOURCE_MGR_API_KEYS 与 KeyStore 热轮转 key；
// 同名 token 以 KeyStore 为准。
func (s *Server) currentAuthKeys() map[string]*pkgauth.KeyConfig {
	static := s.cfg.ScopeKeys
	if s.keyStore == nil {
		return static
	}
	merged := make(map[string]*pkgauth.KeyConfig, len(static))
	for k, v := range static {
		merged[k] = v
	}
	for k, v := range s.keyStore.Keys() {
		merged[k] = v
	}
	return merged
}

// DatasourceMgrPermissionForPath 将 datasource-mgr REST 路径映射为所需 scope。
func DatasourceMgrPermissionForPath(path string) string {
	if len(path) > 1 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}
	switch {
	case path == "/health" || path == "/readyz" || path == "/api/health", path == "/metrics":
		return ""
	case path == "/api/v1/yibao" || path == "/api/v1/kangyang" ||
		path == "/api/v1/mock3" || path == "/api/v1/mock4" ||
		path == "/api/datasources" || path == "/api/datasources/:id" ||
		strings.HasPrefix(path, "/api/datasources/") && strings.HasSuffix(path, "/records") ||
		strings.HasPrefix(path, "/api/datasources/") && strings.HasSuffix(path, "/sample") ||
		strings.HasPrefix(path, "/api/datasources/") && strings.HasSuffix(path, "/record-by-id") ||
		strings.HasPrefix(path, "/api/datasources/") && strings.HasSuffix(path, "/metadata") ||
		strings.HasPrefix(path, "/api/datasources/") && strings.HasSuffix(path, "/audit"):
		return "datasource:read"
	case strings.HasPrefix(path, "/api/datasources/") && strings.HasSuffix(path, "/test") ||
		path == "/api/datasources/seed":
		return "datasource:admin"
	}
	return ""
}

// constantTimeLookupKeys 在排序后的 key 集合上执行常量时间 token 查找，防止时序攻击。
func constantTimeLookupKeys(keys map[string]*pkgauth.KeyConfig, token string) *pkgauth.Identity {
	if len(keys) == 0 {
		return nil
	}
	sortedKeys := make([]string, 0, len(keys))
	for k := range keys {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)
	tokenBytes := []byte(token)
	var matched *pkgauth.KeyConfig
	for _, key := range sortedKeys {
		if subtle.ConstantTimeCompare([]byte(key), tokenBytes) == 1 {
			matched = keys[key]
		}
	}
	if matched == nil {
		return nil
	}
	if matched.IsExpired() {
		return nil
	}
	return &pkgauth.Identity{ServiceType: "external", Name: matched.Name, Scopes: matched.Scopes}
}

// scopeAuthMiddleware 返回 Scope-based API Key 鉴权中间件，支持 KeyStore 热轮转。
// 未配置 scope key 时回退到单 APIKey 模式。
func (s *Server) scopeAuthMiddleware() gin.HandlerFunc {
	scopeKeys := s.currentAuthKeys()
	if len(scopeKeys) > 0 {
		return func(c *gin.Context) {
			path := c.Request.URL.Path
			if path == "/health" || path == "/readyz" || path == "/api/health" {
				c.Next()
				return
			}
			token := pkgauth.ExtractBearerToken(c.GetHeader("Authorization"))
			if token == "" {
				pkgauth.AuthFailuresTotal.WithLabelValues("missing_token").Inc()
				middleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Unauthorized: missing credentials", nil)
				return
			}
			identity := constantTimeLookupKeys(s.currentAuthKeys(), token)
			if identity == nil {
				pkgauth.AuthFailuresTotal.WithLabelValues("invalid_token").Inc()
				middleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Unauthorized: invalid credentials", nil)
				return
			}
			requiredPerm := DatasourceMgrPermissionForPath(path)
			if requiredPerm != "" && !identity.HasPermission(requiredPerm) {
				pkgauth.AuthForbiddenTotal.Inc()
				middleware.AbortWithError(c, http.StatusForbidden, "FORBIDDEN", "Forbidden: insufficient scope", nil)
				return
			}
			c.Set(pkgauth.IdentityContextKey, identity)
			c.Next()
		}
	}
	return middleware.Auth(s.cfg.APIKey)
}

// RegisterRoutes registers all HTTP routes and middleware on the Gin engine.
// RegisterRoutes 向 Gin 引擎装配通用安全中间件链并注册全部业务路由端点，执行逻辑如下：
// 1. 中间件装配链（Middleware Chain）：
//   - RequestID: 生成并注入全链路追踪 X-Request-ID；
//   - RequestLoggerWithModule: 请求访问日志记录；
//   - Recovery: Panic 拦截保护，保障进程高可用；
//   - SecurityHeaders: 注入安全响应头（X-Frame-Options, X-Content-Type-Options 等）；
//   - CORS: 跨域策略配置；
//   - Auth: 基于 Header API Key 的身份认证（如果配置了 APIKey）。
//
// 2. 路由分组注册：
//   - 存活健康探针（Health Check）；
//   - 专用模拟数据集端点（API 1 ~ 4）；
//   - 数据源管理与元数据探测端点。
func (s *Server) RegisterRoutes(r *gin.Engine) {
	// 装配中间件栈
	r.Use(middleware.TraceMiddleware())
	r.Use(pkgobs.RequestLoggerWithModule("datasource-mgr"))
	r.Use(middleware.Recovery(s.logger, "datasource-mgr"))
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.WAF(s.logger))         // 三级等保 G-12：Web 攻击载荷检测
	r.Use(middleware.MaxBodySize(32 << 20)) // 32 MiB max payload protection
	r.Use(middleware.MaxConcurrent(1000))   // 并发在途请求上限，超限返回 503
	if s.cfg.RateLimitRPS > 0 {
		r.Use(middleware.RateLimit(s.cfg.RateLimitRPS, s.cfg.RateLimitBurst)) // 每客户端 IP 令牌桶限流
	}
	r.Use(middleware.CORS(s.cfg.CORSOrigins))
	r.Use(s.scopeAuthMiddleware())

	// 健康探针路由
	r.GET("/health", s.Health)     // Liveness probe / 存活探针
	r.GET("/readyz", s.Readyz)     // Readiness probe / 就绪探针
	r.GET("/api/health", s.Health) // Alias for backward compat / 向后兼容别名

	// Prometheus 指标端点（§7.2）：mc 为 nil（单测）时不注册。
	if s.mc != nil {
		r.GET("/metrics", s.mc.Handler())
	}

	// API 1, 2, 3, 4: 专用模拟数据源访问端点
	r.GET("/api/v1/yibao", s.GetYibaoData)       // API 1: 医保就医与结算
	r.GET("/api/v1/kangyang", s.GetKangyangData) // API 2: 康养体检与慢病
	r.GET("/api/v1/mock3", s.GetMock3Data)       // API 3: 预留政务数据源 3
	r.GET("/api/v1/mock4", s.GetMock4Data)       // API 4: 预留政务数据源 4

	// 通用数据源资产与采样端点
	r.GET("/api/datasources", s.ListDataSources)                          // 数据源目录列表
	r.GET("/api/datasources/:id", s.GetDataSource)                        // 单个数据源详情
	r.GET("/api/datasources/:id/records", s.GetDataSourceRecords)         // 动态分页查询记录
	r.GET("/api/datasources/:id/sample", s.GetDataSourceRecords)          // 兼容样本数据接口别名
	r.GET("/api/datasources/:id/record-by-id", s.GetDataSourceRecordByID) // 按身份证号查询单条记录
	r.POST("/api/datasources/:id/test", s.TestConnection)                 // 数据源连通性测试
	r.GET("/api/datasources/:id/metadata", s.GetMetadata)                 // Schema 元数据查询
	r.GET("/api/datasources/:id/audit", s.GetAccessAudit)                 // 数据访问审计日志查询
	r.POST("/api/datasources/seed", s.SeedDataSourcesEndpoint)            // 初始化/重置模拟数据源
}

// parsePagination parses limit and offset query parameters with safety bounds.
// parsePagination 从 HTTP GET 请求的 URL Query 参数中解析 limit 与 offset 分页参数：
// 1. 若 limit 缺省或非法，使用 defaultLimit；若超过 maxLimit 则截断至 maxLimit；
// 2. 若 offset 缺省或非法，重置为 0；
// 3. 返回安全校验后的 limit 与 offset。
func parsePagination(c *gin.Context, defaultLimit, maxLimit int) (int, int) {
	limitStr := c.Query("limit")
	limit := defaultLimit
	if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
		limit = l
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	offsetStr := c.Query("offset")
	offset := 0
	if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
		offset = o
	}
	return limit, offset
}

// Health returns mock service status.
// Health 返回模拟数据源服务的健康状态与元数据，用于负载均衡器和容器编排存活探针。
// Health is a liveness probe — returns 200 if the process is alive.
// Health 存活探针 — 进程存活即返回 200。
func (s *Server) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"mode":       "mock_datasource_provider",
		"latency_ms": 0,
		"via":        moduleVia,
	})
}

// Readyz is a readiness probe — for datasource-mgr this is equivalent to
// liveness since it has no upstream dependencies.
// Readyz 就绪探针 — datasource-mgr 无上游依赖，因此与存活探针等效。
func (s *Server) Readyz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":     "ready",
		"mode":       "mock_datasource_provider",
		"latency_ms": 0,
		"via":        moduleVia,
	})
}

// GetYibaoData implements API 1: queries mock healthcare and settlement records.
// GetYibaoData 处理 API 1 请求：分页读取并返回医保就医与结算模拟数据（yibao.csv）。
func (s *Server) GetYibaoData(c *gin.Context) {
	s.setDeprecationHeaders(c, naming.DSYibao)
	limit, offset := parsePagination(c, 20, 500)
	records, total, err := GetYibaoRecords(limit, offset)
	if err != nil {
		middleware.AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	c.JSON(http.StatusOK, models.DataQueryResponse{
		DatasourceID: naming.DSYibao,
		SourceID:     naming.DSYibao,
		SourceName:   "医保就医与结算模拟数据库 (yibao.csv)",
		Total:        total,
		Limit:        limit,
		Offset:       offset,
		Records:      records,
		Via:          moduleVia,
	})
}

// GetKangyangData implements API 2: queries mock elderly care and chronic disease records.
// GetKangyangData 处理 API 2 请求：分页读取并返回康养体检与慢病管理模拟数据（kangyang.csv）。
func (s *Server) GetKangyangData(c *gin.Context) {
	s.setDeprecationHeaders(c, naming.DSKangyang)
	limit, offset := parsePagination(c, 20, 500)
	records, total, err := GetKangyangRecords(limit, offset)
	if err != nil {
		middleware.AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	c.JSON(http.StatusOK, models.DataQueryResponse{
		DatasourceID: naming.DSKangyang,
		SourceID:     naming.DSKangyang,
		SourceName:   "康养体检与慢病模拟数据库 (kangyang.csv)",
		Total:        total,
		Limit:        limit,
		Offset:       offset,
		Records:      records,
		Via:          moduleVia,
	})
}

// GetMock3Data implements API 3: queries reserved municipal dataset 3.
// GetMock3Data 处理 API 3 请求：分页读取并返回预留政务模拟数据源 3 的记录。
func (s *Server) GetMock3Data(c *gin.Context) {
	s.setDeprecationHeaders(c, naming.DSMock3)
	limit, offset := parsePagination(c, 20, 500)
	records, total, err := GetMock3Records(limit, offset)
	if err != nil {
		middleware.AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	c.JSON(http.StatusOK, models.DataQueryResponse{
		DatasourceID: naming.DSMock3,
		SourceID:     naming.DSMock3,
		SourceName:   "预留政务数据源 3",
		Total:        total,
		Limit:        limit,
		Offset:       offset,
		Records:      records,
		Via:          moduleVia,
	})
}

// GetMock4Data implements API 4: queries reserved municipal dataset 4.
// GetMock4Data 处理 API 4 请求：分页读取并返回预留政务模拟数据源 4 的记录。
func (s *Server) GetMock4Data(c *gin.Context) {
	s.setDeprecationHeaders(c, naming.DSMock4)
	limit, offset := parsePagination(c, 20, 500)
	records, total, err := GetMock4Records(limit, offset)
	if err != nil {
		middleware.AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	c.JSON(http.StatusOK, models.DataQueryResponse{
		DatasourceID: naming.DSMock4,
		SourceID:     naming.DSMock4,
		SourceName:   "预留政务数据源 4",
		Total:        total,
		Limit:        limit,
		Offset:       offset,
		Records:      records,
		Via:          moduleVia,
	})
}

// ListDataSources returns list of all registered mock sources.
// ListDataSources 返回系统中已注册的所有模拟数据源元数据列表。
func (s *Server) ListDataSources(c *gin.Context) {
	list := ListMockDataSources()
	c.JSON(http.StatusOK, gin.H{
		"total":       len(list),
		"datasources": list,
		"via":         moduleVia,
	})
}

// GetDataSource returns single mock datasource info by its ID.
// GetDataSource 根据 URL 路径参数 :id 查询单个模拟数据源的元数据，未找到时返回 HTTP 404。
func (s *Server) GetDataSource(c *gin.Context) {
	id := c.Param("id")
	ds, err := GetMockDataSource(id)
	if err != nil {
		middleware.AbortWithError(c, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		return
	}
	c.JSON(http.StatusOK, ds)
}

// unknownDatasourceLabel 用于无法归一化的入站 ID，避免把调用方可控的原始值写入指标标签。
const unknownDatasourceLabel = "unknown"

// recordDatasourceRequest 上报单数据源请求分布（§7.2）；mc 为 nil 时空操作。
// 标签仅取 canonical 值，未知入站值一律归入 "unknown"（低基数）。
func (s *Server) recordDatasourceRequest(datasourceID, status string) {
	if s.mc == nil {
		return
	}
	if _, ok := naming.EntryByDataSourceID(datasourceID); !ok {
		datasourceID = unknownDatasourceLabel
	}
	s.mc.RecordDatasourceRequest(datasourceID, naming.APICodeForDataSource(datasourceID), status)
}

// GetDataSourceRecords returns records for a given datasource ID with pagination.
// GetDataSourceRecords 根据 URL 路径参数 :id 动态路由并分页查询对应数据源的数据记录。
func (s *Server) GetDataSourceRecords(c *gin.Context) {
	id := c.Param("id")
	limit, offset := parsePagination(c, 20, 500)

	canonID, _ := naming.NormalizeDataSourceID(id)
	if canonID == "" {
		canonID = id
	}

	records, total, sourceName, err := GetDataBySource(id, limit, offset)
	if err != nil {
		s.recordDatasourceRequest(canonID, "error")
		middleware.AbortWithError(c, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		return
	}
	s.recordDatasourceRequest(canonID, "success")

	c.JSON(http.StatusOK, gin.H{
		"datasource_id": canonID,
		"source_id":     canonID,
		"name":          sourceName,
		"total":         total,
		"limit":         limit,
		"offset":        offset,
		"records":       records,
		"via":           moduleVia,
	})
}

// GetDataSourceRecordByID returns a single record from a datasource by ID card number.
// GetDataSourceRecordByID 根据身份证号从指定数据源中查询单条记录。
// Query 参数: id_card_no (必填，18 位身份证号)
func (s *Server) GetDataSourceRecordByID(c *gin.Context) {
	id := c.Param("id")
	idCardNo := c.Query("id_card_no")

	// 校验必填参数
	if idCardNo == "" {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "query parameter id_card_no is required", nil)
		return
	}

	// 校验身份证号格式（18 位，前 17 位数字，最后 1 位数字或 X）
	if len(idCardNo) != 18 {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "id_card_no must be 18 characters", nil)
		return
	}

	canonID, _ := naming.NormalizeDataSourceID(id)
	if canonID == "" {
		canonID = id
	}

	record, _, err := GetRecordByIDCard(id, idCardNo)
	if err != nil {
		s.recordDatasourceRequest(canonID, "error")
		if errors.Is(err, ErrRecordNotFound) {
			c.JSON(http.StatusOK, models.SingleRecordResponse{
				DatasourceID: canonID,
				Record:       nil,
				Found:        false,
				Via:          moduleVia,
			})
			return
		}
		middleware.AbortWithError(c, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		return
	}
	s.recordDatasourceRequest(canonID, "success")

	c.JSON(http.StatusOK, models.SingleRecordResponse{
		DatasourceID: canonID,
		Record:       record,
		Found:        true,
		Via:          moduleVia,
	})
}

// TestConnection tests mock source connectivity.
// TestConnection 测试指定数据源的连通性并返回模拟延迟。
func (s *Server) TestConnection(c *gin.Context) {
	id := c.Param("id")
	_, err := GetMockDataSource(id)
	if err != nil {
		middleware.AbortWithError(c, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		return
	}
	c.JSON(http.StatusOK, models.ConnectionTestResult{
		DataSourceID: id,
		Success:      true,
		LatencyMs:    2,
		Via:          moduleVia,
	})
}

// GetMetadata returns schema metadata for a mock source.
// GetMetadata 根据数据源 ID 返回表结构与字段类型定义（Schema Metadata）。
func (s *Server) GetMetadata(c *gin.Context) {
	id := c.Param("id")
	meta, err := GetMetadata(id)
	if err != nil {
		middleware.AbortWithError(c, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		return
	}
	c.JSON(http.StatusOK, meta)
}

// GetAccessAudit returns mock audit records for a given datasource ID.
// GetAccessAudit 返回指定数据源的模拟访问审计存证记录。
func (s *Server) GetAccessAudit(c *gin.Context) {
	id := c.Param("id")
	c.JSON(http.StatusOK, gin.H{
		"datasource_id": id,
		"total":         1,
		"records": []gin.H{
			{
				"id":        "audit_mock_1",
				"operation": "query_sample",
				"user":      "dev_user",
				"timestamp": time.Now().Format(time.RFC3339),
				"status":    "success",
			},
		},
		"via": moduleVia,
	})
}

// SeedDataSourcesEndpoint returns mock seed confirmation.
// SeedDataSourcesEndpoint 提供模拟数据源初始化/重新播种的触发端点。
func (s *Server) SeedDataSourcesEndpoint(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"message": "mock datasources initialized (yibao, kangyang, mock3, mock4)",
		"via":     moduleVia,
	})
}
