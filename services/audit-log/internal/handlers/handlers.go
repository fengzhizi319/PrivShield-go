// Package handlers implements the HTTP REST interface for the audit-log module.
package handlers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fengzhizi319/PrivShield/pkg/crypto"
	"github.com/fengzhizi319/PrivShield/pkg/metrics"
	"github.com/fengzhizi319/PrivShield/pkg/middleware"
	naming "github.com/fengzhizi319/PrivShield/pkg/naming"
	"github.com/fengzhizi319/PrivShield/pkg/store"
	"github.com/fengzhizi319/PrivShield/pkg/store/flusher"
	"github.com/fengzhizi319/PrivShield/pkg/validation"

	"github.com/fengzhizi319/PrivShield/services/audit-log/internal/agent"
	"github.com/fengzhizi319/PrivShield/services/audit-log/internal/config"
)

const moduleVia = "audit-log"

// Server aggregates HTTP handler dependencies.
type Server struct {
	agent  *agent.Client
	cfg    *config.Config
	audit  store.AuditStore
	logger *slog.Logger
	mc     *metrics.Collector
}

// New creates a new Server instance.
func New(ag *agent.Client, cfg *config.Config, audit store.AuditStore, logger *slog.Logger, mc *metrics.Collector) *Server {
	return &Server{
		agent:  ag,
		cfg:    cfg,
		audit:  audit,
		logger: logger,
		mc:     mc,
	}
}

// RegisterRoutes registers all HTTP routes on the Gin engine.
func (s *Server) RegisterRoutes(r *gin.Engine) {
	r.Use(middleware.TraceMiddleware())
	r.Use(middleware.StructuredLogger(s.logger, "audit-log"))
	r.Use(middleware.Recovery(s.logger, "audit-log"))
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.MaxBodySize(32 << 20)) // 32 MiB max payload protection
	r.Use(middleware.MaxConcurrent(1000))   // 并发在途请求上限，超限返回 503
	if s.cfg.RateLimitRPS > 0 {
		r.Use(middleware.RateLimit(s.cfg.RateLimitRPS, s.cfg.RateLimitBurst)) // 每客户端 IP 令牌桶限流
	}
	r.Use(middleware.CORS(s.cfg.CORSOrigins))
	r.Use(middleware.Auth(s.cfg.APIKey))

	r.GET("/health", s.Health)     // Liveness probe / 存活探针
	r.GET("/readyz", s.Readyz)     // Readiness probe / 就绪探针
	r.GET("/api/health", s.Health) // Alias for backward compat / 向后兼容别名
	r.GET("/api/audit/logs", s.ListLogs)
	r.POST("/api/audit/logs", s.CreateLog)
	r.GET("/api/audit/logs/:id", s.GetLog)
	r.GET("/api/audit/stats", s.GetStats)
	r.GET("/api/audit/snapshots", s.ListSnapshots)
	r.POST("/api/audit/snapshots/verify", s.VerifyIntegrity)
	r.GET("/api/audit/chain/verify", s.VerifyChain)  // Hash chain continuous integrity verification (GET)
	r.POST("/api/audit/chain/verify", s.VerifyChain) // Hash chain continuous integrity verification (POST)
	r.POST("/api/audit/report", s.GenerateReport)
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

	inputHash := req.InputHash
	outputHash := req.OutputHash
	if inputHash == "" {
		h := crypto.SumSM3([]byte(fmt.Sprintf("input|%s|%d|%s|%s", normID, req.InputRows, req.User, string(paramsJSON))))
		inputHash = hex.EncodeToString(h[:])
	}
	if outputHash == "" {
		h := crypto.SumSM3([]byte(fmt.Sprintf("output|%s|%d|%s|%s|%s", normID, req.OutputRows, req.Status, req.SecurityLevel, string(paramsJSON))))
		outputHash = hex.EncodeToString(h[:])
	}

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
	encInputSample := req.InputSample
	encOutputSample := req.OutputSample
	if s.cfg.EncryptionKey != "" {
		if enc, err := crypto.EncryptString(req.InputSample, s.cfg.EncryptionKey); err == nil {
			encInputSample = enc
		}
		if enc, err := crypto.EncryptString(req.OutputSample, s.cfg.EncryptionKey); err == nil {
			encOutputSample = enc
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
	if s.cfg.EncryptionKey != "" {
		for i := range snaps {
			if crypto.IsEncrypted(snaps[i].InputSample) {
				if dec, err := crypto.DecryptString(snaps[i].InputSample, s.cfg.EncryptionKey); err == nil {
					snaps[i].InputSample = dec
				}
			}
			if crypto.IsEncrypted(snaps[i].OutputSample) {
				if dec, err := crypto.DecryptString(snaps[i].OutputSample, s.cfg.EncryptionKey); err == nil {
					snaps[i].OutputSample = dec
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"total":     total,
		"limit":     limit,
		"offset":    offset,
		"snapshots": snaps,
		"via":       moduleVia,
	})
}

// VerifyIntegrity verifies the integrity of a snapshot using its hash.
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
		prevHash = log.PrevHash
	}

	valid, _ := store.VerifyAuditIntegrityHash(
		snap.IntegrityHash, snap.AuditLogID, prevHash, snap.Timestamp, snap.Algorithm,
		log.InputHash, log.OutputHash, log.User, log.SecurityLevel, snap.ParametersJSON,
	)
	expectedHash := store.ComputeAuditIntegrityHash(
		snap.AuditLogID, prevHash, snap.Timestamp, snap.Algorithm,
		log.InputHash, log.OutputHash, log.User, log.SecurityLevel, snap.ParametersJSON,
	)

	c.JSON(http.StatusOK, gin.H{
		"snapshot_id": req.SnapshotID,
		"valid":       valid,
		"expected":    expectedHash,
		"actual":      snap.IntegrityHash,
		"prev_hash":   prevHash,
		"via":         moduleVia,
	})
}

// VerifyChain verifies the cryptographic hash chain of recent records.
func (s *Server) VerifyChain(c *gin.Context) {
	limit := 1000
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
		"total_verified": res.TotalVerified,
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

