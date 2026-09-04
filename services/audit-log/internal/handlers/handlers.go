// Package handlers implements the HTTP REST interface for the audit-log module.
package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
	"github.com/fengzhizi319/PrivShield-go/pkg/crypto"
	"github.com/fengzhizi319/PrivShield-go/pkg/metrics"
	"github.com/fengzhizi319/PrivShield-go/pkg/middleware"
	naming "github.com/fengzhizi319/PrivShield-go/pkg/naming"
	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
	"github.com/fengzhizi319/PrivShield-go/pkg/store"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/flusher"
	"github.com/fengzhizi319/PrivShield-go/pkg/validation"

	"github.com/fengzhizi319/PrivShield-go/services/audit-log/internal/agent"
	"github.com/fengzhizi319/PrivShield-go/services/audit-log/internal/config"
)

const moduleVia = "audit-log"

// auditReadOnlyEndpoints 是「只读核验员 Key」可访问的 方法+路径 白名单（第十二章 P1-6 ②③）。
// 只放查询与验真端点；POST /v1/audit/logs（写存证）与 POST /v1/audit/report（报表导出）
// 刻意排除——核验专区是「被查者」，不得具备任何写入能力。
var auditReadOnlyEndpoints = []middleware.ReadOnlyEndpoint{
	{Method: http.MethodGet, Path: "/v1/audit/logs"},
	{Method: http.MethodGet, Path: "/v1/audit/stats"},
	{Method: http.MethodGet, Path: "/v1/audit/snapshots"},
	{Method: http.MethodPost, Path: "/v1/audit/snapshots/verify"},
	{Method: http.MethodGet, Path: "/v1/audit/chain/verify"},
	{Method: http.MethodPost, Path: "/v1/audit/chain/verify"},
	{Method: http.MethodGet, Path: "/metrics"},
}

// Server aggregates HTTP handler dependencies.
type Server struct {
	agent    *agent.Client
	cfg      *config.Config
	keyStore *pkgauth.KeyStore
	audit    store.AuditStore
	logger   *slog.Logger
	mc       *metrics.Collector
}

// New creates a new Server instance.
func New(ag *agent.Client, cfg *config.Config, keyStore *pkgauth.KeyStore, audit store.AuditStore, logger *slog.Logger, mc *metrics.Collector) *Server {
	return &Server{
		agent:    ag,
		cfg:      cfg,
		keyStore: keyStore,
		audit:    audit,
		logger:   logger,
		mc:       mc,
	}
}

// currentAuthKeys 合并静态 AUDIT_LOG_API_KEYS 与 KeyStore 热轮转 key；
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

// AuditLogPermissionForPath 将 audit-log REST 路径映射为所需 scope。
func AuditLogPermissionForPath(method, path string) string {
	if len(path) > 1 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}
	switch {
	case path == "/health" || path == "/readyz", path == "/metrics":
		return ""
	case path == "/v1/audit/logs" && method == http.MethodPost,
		path == "/v1/audit/report" && method == http.MethodPost:
		return "audit:write"
	case path == "/v1/audit/logs" && method == http.MethodGet,
		strings.HasPrefix(path, "/v1/audit/logs/") && method == http.MethodGet,
		path == "/v1/audit/stats" && method == http.MethodGet,
		path == "/v1/audit/snapshots" && method == http.MethodGet:
		return "audit:read"
	case path == "/v1/audit/snapshots/verify" && method == http.MethodPost,
		path == "/v1/audit/chain/verify":
		return "audit:verify"
	default:
		// fail-closed：未显式映射的非豁免路径默认归入最高 admin 权限，防止空 scope 绕过
		return "audit:admin"
	}
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

// authMiddleware 返回统一的 API Key 鉴权中间件，优先级如下：
//  1. Scope-based key（AUDIT_LOG_API_KEYS / KeyStore）按路径 scope 校验；
//  2. 主 APIKey 授予全部权限（运维 / 业务写入身份）；
//  3. ReaderAPIKey 仅允许访问 auditReadOnlyEndpoints 白名单端点；
//  4. 都不匹配返回 401。
//
// apiKey 与 readerKey 均未配置且没有 scope key 时，保持开发模式放行。
func (s *Server) authMiddleware() gin.HandlerFunc {
	apiKey := s.cfg.APIKey
	readerKey := s.cfg.ReaderAPIKey
	scopeKeys := s.currentAuthKeys()

	return func(c *gin.Context) {
		path := c.Request.URL.Path
		method := c.Request.Method

		// 健康探针豁免
		if path == "/health" || path == "/readyz" {
			c.Next()
			return
		}
		// 非核心路径豁免（/metrics 纳入鉴权，P1-6）
		if !strings.HasPrefix(path, "/v1/") && path != "/metrics" {
			c.Next()
			return
		}

		// 无认证配置：开发模式放行
		if apiKey == "" && readerKey == "" && len(scopeKeys) == 0 {
			c.Next()
			return
		}

		token := pkgauth.ExtractBearerToken(c.GetHeader("Authorization"))
		if token == "" {
			pkgauth.AuthFailuresTotal.WithLabelValues("missing_token").Inc()
			middleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Unauthorized: missing credentials", nil)
			return
		}

		// 1. Scope-based key（支持 KeyStore 热轮转）
		if len(scopeKeys) > 0 {
			identity := constantTimeLookupKeys(scopeKeys, token)
			if identity != nil {
				requiredPerm := AuditLogPermissionForPath(method, path)
				if requiredPerm != "" && !identity.HasPermission(requiredPerm) {
					pkgauth.AuthForbiddenTotal.Inc()
					middleware.AbortWithError(c, http.StatusForbidden, "FORBIDDEN", "Forbidden: insufficient scope", nil)
					return
				}
				c.Set(pkgauth.IdentityContextKey, identity)
				c.Next()
				return
			}
		}

		// 2. 主写入 Key
		if apiKey != "" && subtle.ConstantTimeCompare([]byte(token), []byte(apiKey)) == 1 {
			c.Next()
			return
		}

		// 3. 只读核验员 Key
		if readerKey != "" && subtle.ConstantTimeCompare([]byte(token), []byte(readerKey)) == 1 {
			if !middleware.IsReadOnlyEndpoint(method, path, auditReadOnlyEndpoints) {
				pkgauth.AuthForbiddenTotal.Inc()
				middleware.AbortWithError(c, http.StatusForbidden, "FORBIDDEN", "Forbidden: reader key is limited to verification endpoints", nil)
				return
			}
			c.Next()
			return
		}

		pkgauth.AuthFailuresTotal.WithLabelValues("invalid_token").Inc()
		middleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHENTICATED", "Unauthorized: invalid credentials", nil)
	}
}

// RegisterRoutes registers all HTTP routes on the Gin engine.
func (s *Server) RegisterRoutes(r *gin.Engine) {
	r.Use(middleware.TraceMiddleware())
	r.Use(pkgobs.RequestLoggerWithModule("audit-log"))
	r.Use(middleware.Recovery(s.logger, "audit-log"))
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.WAF(s.logger))         // 三级等保 G-12：Web 攻击载荷检测
	r.Use(middleware.MaxBodySize(32 << 20)) // 32 MiB max payload protection
	r.Use(middleware.MaxConcurrent(1000))   // 并发在途请求上限，超限返回 503
	if s.cfg.RateLimitRPS > 0 {
		r.Use(middleware.RateLimit(s.cfg.RateLimitRPS, s.cfg.RateLimitBurst)) // 每客户端 IP 令牌桶限流
	}
	r.Use(middleware.CORS(s.cfg.CORSOrigins))
	// P1-6 权责分离：数据局核验专区持「只读核验员 Key」，只能命中 auditReadOnlyEndpoints
	// 这张 方法+路径 白名单；写入端点（POST /v1/audit/logs）与报表导出（POST /v1/audit/report）
	// 不在表内，越权直接 403。ReaderAPIKey 为空则退化为原单 Key 语义。
	// 同时支持 AUDIT_LOG_API_KEYS / KeyStore 的 scope-based 热轮转鉴权。
	r.Use(s.authMiddleware())
	if s.cfg.ReaderAPIKey == "" {
		s.logger.Warn("audit reader role disabled: verification endpoints share the write API key (P1-6)",
			"component", "audit-log", "module", moduleVia)
	} else {
		s.logger.Info("audit reader role enabled: verification endpoints restricted to reader key",
			"component", "audit-log", "module", moduleVia,
			"readonly_endpoints", len(auditReadOnlyEndpoints))
	}

	r.GET("/health", s.Health) // Liveness probe / 存活探针
	r.GET("/readyz", s.Readyz) // Readiness probe / 就绪探针
	r.GET("/v1/audit/logs", s.ListLogs)
	r.POST("/v1/audit/logs", s.CreateLog)
	r.GET("/v1/audit/logs/:id", s.GetLog)
	r.GET("/v1/audit/stats", s.GetStats)
	r.GET("/v1/audit/snapshots", s.ListSnapshots)
	r.POST("/v1/audit/snapshots/verify", s.VerifyIntegrity)
	r.GET("/v1/audit/chain/verify", s.VerifyChain)  // Hash chain continuous integrity verification (GET)
	r.POST("/v1/audit/chain/verify", s.VerifyChain) // Hash chain continuous integrity verification (POST)
	r.POST("/v1/audit/report", s.GenerateReport)
	r.GET("/metrics", s.mc.Handler())
}

// Health is a liveness probe — returns 200 if the process is alive.
func (s *Server) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"via":    moduleVia,
	})
}

// Readyz is a readiness probe — checks upstream agent connectivity and storage flush health.
func (s *Server) Readyz(c *gin.Context) {
	// Check storage flush health
	if flusherStore, ok := s.audit.(*flusher.BufferedAuditStore); ok {
		if flusherStore.HasFlushError() {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status":        "not_ready",
				"backend":       "degraded",
				"storage":       "flush_error",
				"last_error":    flusherStore.LastFlushError(),
				"queue_depth":   flusherStore.QueueDepth(),
				"retry_backlog": flusherStore.RetryPending(),
				"staged":        flusherStore.StagedCount(),
				"rejected":      flusherStore.OverflowTotal(),
				"via":           moduleVia,
			})
			return
		}
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	agentData, err := s.agent.Health(ctx)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":     "not_ready",
			"backend":    "ok",
			"agent":      "unreachable",
			"agent_url":  s.cfg.AgentBaseURL(),
			"latency_ms": latency,
			"error":      err.Error(),
			"via":        moduleVia,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "ready",
		"backend":    "ok",
		"storage":    "healthy",
		"agent":      agentData,
		"agent_url":  s.cfg.AgentBaseURL(),
		"latency_ms": latency,
		"via":        moduleVia,
	})
}

// ListLogs returns audit logs with optional filtering.
func (s *Server) ListLogs(c *gin.Context) {
	limit, offset := validation.ParsePagination(c, 100, 1000)

	rawDS := c.Query("datasource_id")
	if rawDS == "" {
		rawDS = c.Query("datasource")
	}
	normDS := ""
	if rawDS != "" {
		if id, err := naming.NormalizeDataSourceID(rawDS); err == nil {
			normDS = id
		} else {
			normDS = rawDS
		}
	}

	filter := store.AuditFilter{
		TaskID:        c.Query("task_id"),
		APICode:       c.Query("api_code"),
		DatasourceID:  normDS,
		Operation:     c.Query("operation"),
		DataSource:    normDS,
		User:          c.Query("user"),
		Status:        c.Query("status"),
		SecurityLevel: c.Query("security_level"),
		Limit:         limit,
		Offset:        offset,
	}

	logs, total, err := s.audit.ListLogs(filter)
	if err != nil {
		middleware.AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"total":  total,
		"limit":  limit,
		"offset": offset,
		"logs":   logs,
		"via":    moduleVia,
	})
}

// CreateLog creates a new audit log entry with cryptographic hash chain and sample envelope encryption.
func (s *Server) CreateLog(c *gin.Context) {
	var req struct {
		TaskID        string `json:"task_id"`
		APICode       string `json:"api_code"`
		DatasourceID  string `json:"datasource_id"`
		Operation     string `json:"operation" binding:"required"`
		DataSource    string `json:"datasource"`
		InputHash     string `json:"input_hash"`
		OutputHash    string `json:"output_hash"`
		InputSample   string `json:"input_sample"`
		OutputSample  string `json:"output_sample"`
		Algorithm     string `json:"algorithm"`
		Parameters    any    `json:"parameters"`
		InputRows     int    `json:"input_rows"`
		OutputRows    int    `json:"output_rows"`
		DurationMs    int64  `json:"duration_ms"`
		User          string `json:"user"`
		Status        string `json:"status" binding:"required"`
		ErrorMessage  string `json:"error"`
		SecurityLevel string `json:"security_level"`
		PrevHash      string `json:"prev_hash"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("invalid request: %v", err), nil)
		return
	}

	// The hash chain tail is server-assigned by the audit store. Accepting a caller-supplied
	// prev_hash would let any client fork or permanently break the tamper-evidence chain.
	if req.PrevHash != "" {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT",
			"prev_hash is assigned by the audit store and must be omitted", nil)
		return
	}

	rawDS := req.DatasourceID
	if rawDS == "" {
		rawDS = req.APICode
	}
	if rawDS == "" {
		rawDS = req.DataSource
	}

	if strings.TrimSpace(rawDS) == "" {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "datasource is required", nil)
		return
	}

	entry, err := naming.Normalize(rawDS)
	if err != nil {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_DATASOURCE_ID", fmt.Sprintf("invalid datasource %q: %v", rawDS, err), nil)
		return
	}
	if err := naming.CheckWritable(entry.DataSourceID); err != nil {
		middleware.AbortWithError(c, http.StatusConflict, "RESERVED_DATASOURCE", err.Error(), nil)
		return
	}
	normID := entry.DataSourceID
	normAPICode := req.APICode
	if normAPICode == "" {
		normAPICode = entry.APICode
	}

	// Input validation
	if err := validation.AllowedValues("operation", req.Operation, validation.AuditOperations); err != nil {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error(), nil)
		return
	}
	if err := validation.AllowedValues("status", req.Status, validation.AuditStatuses); err != nil {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error(), nil)
		return
	}
	if req.SecurityLevel != "" {
		if err := validation.AllowedValues("security_level", req.SecurityLevel, validation.SensitivityLevels); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error(), nil)
			return
		}
	}

	logID := validation.GenerateID("audit")
	now := time.Now()

	paramsJSON, _ := json.Marshal(req.Parameters)
	const maxParamsSize = 1 << 20 // 1 MB
	if len(paramsJSON) > maxParamsSize {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("parameters too large: %d bytes (max %d bytes)", len(paramsJSON), maxParamsSize), nil)
		return
	}

	// P0 fix: input_hash and output_hash must be supplied by the caller. Server-side fallback
	// to metadata-only hashes weakens the tamper-evidence binding because it does not cover
	// the actual data content.
	if req.InputHash == "" {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT",
			"input_hash is required and must be a cryptographic hash of the actual input data", nil)
		return
	}
	if req.OutputHash == "" {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT",
			"output_hash is required and must be a cryptographic hash of the actual output data", nil)
		return
	}
	inputHash := req.InputHash
	outputHash := req.OutputHash

	log := &store.AuditLog{
		ID:             logID,
		TaskID:         req.TaskID,
		APICode:        normAPICode,
		DatasourceID:   normID,
		Timestamp:      now,
		Operation:      req.Operation,
		DataSource:     normID,
		InputHash:      inputHash,
		OutputHash:     outputHash,
		Algorithm:      req.Algorithm,
		Parameters:     req.Parameters,
		ParametersJSON: string(paramsJSON),
		InputRows:      req.InputRows,
		OutputRows:     req.OutputRows,
		DurationMs:     req.DurationMs,
		User:           req.User,
		Status:         req.Status,
		ErrorMessage:   req.ErrorMessage,
		SecurityLevel:  req.SecurityLevel,
	}

	// Encrypt sensitive snapshot samples before storage (Envelope Encryption)
	encInputSample, encOutputSample := req.InputSample, req.OutputSample
	if s.cfg.EncryptionKey != "" {
		var err error
		if encInputSample, err = crypto.EncryptString(req.InputSample, s.cfg.EncryptionKey); err != nil {
			s.logger.Error("failed to encrypt input snapshot sample", "error", err.Error())
			middleware.AbortWithError(c, http.StatusInternalServerError, "ENCRYPTION_FAILED", "failed to encrypt input snapshot sample", nil)
			return
		}
		if encOutputSample, err = crypto.EncryptString(req.OutputSample, s.cfg.EncryptionKey); err != nil {
			s.logger.Error("failed to encrypt output snapshot sample", "error", err.Error())
			middleware.AbortWithError(c, http.StatusInternalServerError, "ENCRYPTION_FAILED", "failed to encrypt output snapshot sample", nil)
			return
		}
	}

	snapshot := &store.SnapshotRecord{
		ID:             validation.GenerateID("snap"),
		AuditLogID:     logID,
		Timestamp:      now,
		InputSample:    encInputSample,
		OutputSample:   encOutputSample,
		Algorithm:      req.Algorithm,
		Parameters:     req.Parameters,
		ParametersJSON: string(paramsJSON),
	}

	// Single Authority: store assigns and syncs PrevHash and IntegrityHash to log and snapshot
	if err := s.audit.SaveLogWithSnapshot(log, snapshot); err != nil {
		s.logger.Error("failed to persist audit log and snapshot", "error", err.Error())
		middleware.AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to persist audit log and snapshot", nil)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":             logID,
		"snapshot_id":    snapshot.ID,
		"integrity_hash": log.IntegrityHash,
		"prev_hash":      log.PrevHash,
		"via":            moduleVia,
	})
}

// GetLog returns a specific audit log by ID.
func (s *Server) GetLog(c *gin.Context) {
	id := c.Param("id")
	log, err := s.audit.GetLog(id)
	if err != nil {
		middleware.AbortWithError(c, http.StatusNotFound, "NOT_FOUND", "audit log not found", nil)
		return
	}

	c.JSON(http.StatusOK, log)
}

// GetStats returns aggregated audit statistics.
func (s *Server) GetStats(c *gin.Context) {
	stats, err := s.audit.GetStats()
	if err != nil {
		middleware.AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"total_operations":  stats.TotalOperations,
		"by_operation":      stats.ByOperation,
		"by_status":         stats.ByStatus,
		"by_security_level": stats.BySecurityLevel,
		"avg_duration_ms":   stats.AvgDurationMs,
		"period":            c.DefaultQuery("period", "24h"),
	})
}

// ListSnapshots returns desensitization snapshots (decrypting samples if key configured).
func (s *Server) ListSnapshots(c *gin.Context) {
	limit, offset := validation.ParsePagination(c, 50, 500)

	snaps, total, err := s.audit.ListSnapshots(limit, offset)
	if err != nil {
		middleware.AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	// Decrypt sample fields transparently if encrypted
	var unencrypted []string
	if s.cfg.EncryptionKey != "" {
		for i := range snaps {
			if crypto.IsEncrypted(snaps[i].InputSample) {
				if dec, err := crypto.DecryptString(snaps[i].InputSample, s.cfg.EncryptionKey); err == nil {
					snaps[i].InputSample = dec
				} else {
					s.logger.Warn("snapshot sample decryption failed",
						"snapshot_id", snaps[i].ID, "field", "input_sample", "error", err.Error())
				}
			} else if snaps[i].InputSample != "" {
				unencrypted = append(unencrypted, snaps[i].ID)
			}
			if crypto.IsEncrypted(snaps[i].OutputSample) {
				if dec, err := crypto.DecryptString(snaps[i].OutputSample, s.cfg.EncryptionKey); err == nil {
					snaps[i].OutputSample = dec
				} else {
					s.logger.Warn("snapshot sample decryption failed",
						"snapshot_id", snaps[i].ID, "field", "output_sample", "error", err.Error())
				}
			} else if snaps[i].OutputSample != "" {
				unencrypted = append(unencrypted, snaps[i].ID)
			}
		}
		if len(unencrypted) > 0 {
			s.logger.Warn("snapshot samples stored without envelope prefix while encryption is enabled",
				"count", len(unencrypted))
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"total":               total,
		"limit":               limit,
		"offset":              offset,
		"snapshots":           snaps,
		"unencrypted_samples": unencrypted,
		"via":                 moduleVia,
	})
}

// VerifyIntegrity verifies the integrity of a snapshot using its hash.
//
// VerifyIntegrity 单条快照验真端点。P2-4 响应补 `reason` 机器可读枚举：
//   - `ok`：按「当前写入口径」（配置密钥时为 HMAC-SM3）验真通过；
//   - `legacy_hashed`：仅命中迁移前历史候选（无密钥 SM3 / SHA-256 / 本机时区）——证据真实、
//     仅需重签，**不得被解读为篡改**；
//   - `hash_mismatch`：所有候选前映像均无法复原存储哈希，即快照内容（含密文样本）已被改写，
//     `valid=false`（fail-closed 语义保持不变）。
//
// 注：快照核验标签带 `-SNAPSHOT` 后缀，而 `store.IsCanonicalHashLabel` 判定的是日志写入口径，
// 故比较前需先剥离该后缀。
func (s *Server) VerifyIntegrity(c *gin.Context) {
	var req struct {
		SnapshotID string `json:"snapshot_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("invalid request: %v", err), nil)
		return
	}

	snap, err := s.audit.GetSnapshot(req.SnapshotID)
	if err != nil {
		middleware.AbortWithError(c, http.StatusNotFound, "NOT_FOUND", "snapshot not found", nil)
		return
	}

	log, err := s.audit.GetLog(snap.AuditLogID)
	if err != nil {
		middleware.AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "associated audit log not found", nil)
		return
	}

	prevHash := snap.PrevHash
	if prevHash == "" {
		// Backward compatibility: snapshots written before the P0 fix may have an empty prev_hash.
		prevHash = log.PrevHash
	}

	// P0 fix: verify the snapshot using its own integrity hash that covers input/output samples.
	valid, hashLabel := store.VerifySnapshotIntegrityHash(
		snap.IntegrityHash, snap.ID, snap.AuditLogID, prevHash, snap.Timestamp, snap.Algorithm,
		snap.InputSample, snap.OutputSample, snap.ParametersJSON,
	)
	// Legacy fallback: snapshots written before this fix copied the parent audit log's integrity hash.
	if !valid {
		valid, hashLabel = store.VerifyAuditIntegrityHash(
			snap.IntegrityHash, snap.AuditLogID, prevHash, snap.Timestamp, snap.Algorithm,
			log.InputHash, log.OutputHash, log.User, log.SecurityLevel, snap.ParametersJSON,
		)
	}
	expectedHash := store.ComputeSnapshotIntegrityHash(
		snap.ID, snap.AuditLogID, prevHash, snap.Timestamp, snap.Algorithm,
		snap.InputSample, snap.OutputSample, snap.ParametersJSON,
	)

	// P2-4：把「为什么验真通过/失败」结构化透出，避免看板解析 hash_label 字符串猜口径。
	reason := store.ChainReasonHashMismatch
	legacyHashed := false
	if valid {
		// 快照标签带 -SNAPSHOT 后缀，IsCanonicalHashLabel 按日志写入口径判定，故先剥离再比较。
		if store.IsCanonicalHashLabel(strings.TrimSuffix(hashLabel, "-SNAPSHOT")) {
			reason = store.ChainReasonOK
		} else {
			reason = store.ChainReasonLegacyHashed
			legacyHashed = true
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"snapshot_id":   req.SnapshotID,
		"valid":         valid,
		"reason":        reason,
		"legacy_hashed": legacyHashed,
		"hash_label":    hashLabel,
		"expected":      expectedHash,
		"actual":        snap.IntegrityHash,
		"prev_hash":     prevHash,
		"via":           moduleVia,
	})
}

// VerifyChain verifies the cryptographic hash chain of records.
// P1 fix: when limit is omitted or zero, the entire chain is verified by default, and the
// response includes total_records so callers can detect physical deletion.
//
// VerifyChain 哈希链对账验真端点。P2-4 补齐验真响应信息量：
//   - `reason`：机器可读核验结论枚举（ok / legacy_hashed / tampered_payload / hash_mismatch /
//     broken_chain / missing_prev / missing_records），看板无需再解析英文 `message` 字符串；
//   - `legacy_hashed`：以「迁移前历史口径」（无密钥 SM3 / SHA-256 / 本机时区）验真通过的记录数，
//     属于「证据真实、仅待重签」，**不代表被篡改**；此时 `valid` 仍为 true 且 `reason` 为
//     `legacy_hashed`，与真实失配（`tampered_payload` / `hash_mismatch`，`valid=false`）严格区分。
func (s *Server) VerifyChain(c *gin.Context) {
	limit := 0
	if lStr := c.Query("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	} else {
		var req struct {
			Limit int `json:"limit"`
		}
		if err := c.ShouldBindJSON(&req); err == nil && req.Limit > 0 {
			limit = req.Limit
		}
	}

	res, err := s.audit.VerifyChain(limit)
	if err != nil {
		middleware.AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"reason":         res.Reason,
		"legacy_hashed":  res.LegacyHashed,
		"total_verified": res.TotalVerified,
		"total_records":  res.TotalRecords,
		"valid":          res.Valid,
		"broken_at_id":   res.BrokenAtID,
		"expected_hash":  res.ExpectedHash,
		"actual_hash":    res.ActualHash,
		"message":        res.Message,
		"via":            moduleVia,
	})
}

// GenerateReport generates a compliance audit report.
func (s *Server) GenerateReport(c *gin.Context) {
	var req struct {
		Period string `json:"period"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		req.Period = "24h"
	}

	report, err := s.audit.GenerateReport(req.Period)
	if err != nil {
		middleware.AbortWithError(c, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":                fmt.Sprintf("report-%d", time.Now().Unix()),
		"generated_at":      time.Now(),
		"period":            req.Period,
		"total_operations":  report.TotalOperations,
		"success_rate":      report.SuccessRate,
		"by_security_level": report.BySecurityLevel,
		"top_operations":    report.TopOperations,
		"recommendations":   report.Recommendations,
	})
}
