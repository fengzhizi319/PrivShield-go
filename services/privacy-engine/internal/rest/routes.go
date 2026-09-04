// Package rest 提供 REST API 路由注册。
//
// 路由前缀与 Python engine 完全对齐：
//   - /v1/privacy/* — 隐私原语
//   - /v1/agent/*   — 通用处理流水线
//   - /v1/ops/*     — 运维诊断
//   - /v1/medical/* — 医疗流水线
//
// 所有错误响应统一使用 middleware.AbortWithError 输出标准信封格式。
package rest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	httppprof "net/http/pprof"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/gin-gonic/gin"

	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/dynclassification"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/security"
	"github.com/fengzhizi319/PrivShield-go/engine-go/internal/service"
	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
	"github.com/fengzhizi319/PrivShield-go/pkg/middleware"
	"github.com/fengzhizi319/PrivShield-go/pkg/naming"
	"github.com/fengzhizi319/PrivShield-go/privacy-go-sdk/kano"
)

var isServerReady atomic.Bool

func init() {
	isServerReady.Store(true)
}

// SetReady 设置 Pod 服务就绪状态（用于 K8s 优雅停机摘流）
func SetReady(ready bool) {
	isServerReady.Store(ready)
}

// RegisterRoutes 注册所有 REST API 路由（与 Python engine URL 方案完全对齐）。
func RegisterRoutes(r *gin.Engine, svc *service.PrivacyService) {
	// 【安全防护层中间件】以下 Use() 均在注册任何路由之前调用，会与 main.go 中先行注册的「基础设施层」
	// 合并为同一条有序处理链，执行顺序紧接在 Prometheus 之后、业务 Handler 之前：
	//   SecurityHeaders → MaxBodySize → WAF → Auth → RateLimit(身份级) → Handler
	// ① 安全响应头：注入 HSTS、X-Content-Type-Options、X-Frame-Options=DENY 等，加固浏览器侧防护。
	r.Use(security.SecurityHeadersMiddleware())
	// ② 请求体上限：将 Body 包裹为 http.MaxBytesReader，限制最大可读 64MB，超限读取即失败中断，
	//    防御超大载荷耗尽内存（复用 pkg/middleware）。
	r.Use(middleware.MaxBodySize(64 * 1024 * 1024))
	// ③ 增强版 WAF：对 URL / 查询串 / 关键请求头 / 请求体做多规则扫描，拦截 SQLi、XSS、路径穿越、
	//    命令注入，命中即以 403 短路并记录告警日志。
	r.Use(middleware.WAF(nil))
	// ④ 认证与接口级鉴权：健康端点按配置豁免；启用鉴权时校验 Bearer Token(API Key)，再按路径所需
	//    scope 校验权限，缺失/非法凭证返回 401，权限不足返回 403。
	r.Use(security.AuthMiddleware())
	// ⑤ 身份级限流：按「身份 + 归一化路径(+匿名客户端 IP)」分片的令牌桶，健康端点豁免，
	//    防止单身份 / 单 IP 洪泛（与 main.go 的全局限流互补）。
	r.Use(security.RateLimitMiddleware())

	// 性能分析端点（环境变量控制，生产环境默认关闭）
	if pkgconfig.EnvString("AGENT_PPROF_ENABLED", "false") == "true" {
		registerPprof(r)
	}

	// 健康检查（无前缀，与 Python /health, /livez, /readyz 对齐）
	//curl http://127.0.0.1:8079/health
	r.GET("/health", healthHandlerWithService(svc))
	// Demo: curl http://127.0.0.1:8079/livez
	r.GET("/livez", livezHandler)
	// Demo: curl http://127.0.0.1:8079/readyz
	r.GET("/readyz", readyzHandler)
	// Demo: curl http://127.0.0.1:8079/readyz/llm
	r.GET("/readyz/llm", readyzLLMHandler(svc))

	// 根路径直调别名路由（兼容直接调用）
	// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/agent/process
	r.POST("/agent/process", agentProcessHandler(svc))
	// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/medical/process
	r.POST("/medical/process", medicalProcessHandler(svc))
	// Demo: curl http://127.0.0.1:8079/ops/diagnostics
	r.GET("/ops/diagnostics", diagnosticsHandler(svc))
	// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/privacy/process_file
	r.POST("/privacy/process_file", processFileHandler(svc))

	// /v1/privacy/* — 隐私原语
	v1p := r.Group("/v1/privacy")
	{
		// 掩码
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/mask
		v1p.POST("/mask", maskHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/mask/record
		v1p.POST("/mask/record", maskRecordHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/mask_record
		v1p.POST("/mask_record", maskRecordHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/mask/batch
		v1p.POST("/mask/batch", maskBatchHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/mask/dataframe
		v1p.POST("/mask/dataframe", maskDataFrameHandler(svc))

		// 差分隐私 — 基础
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/dp/count
		v1p.POST("/dp/count", dpCountHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/dp/sum
		v1p.POST("/dp/sum", dpSumHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/dp/mean
		v1p.POST("/dp/mean", dpMeanHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/dp/histogram
		v1p.POST("/dp/histogram", dpHistogramHandler(svc))
		// 差分隐私 — 噪声
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/dp/noisy_count
		v1p.POST("/dp/noisy_count", noisyCountHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/dp/noisy_sum
		v1p.POST("/dp/noisy_sum", noisySumHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/dp/noisy_mean
		v1p.POST("/dp/noisy_mean", noisyMeanHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/dp/noisy_histogram
		v1p.POST("/dp/noisy_histogram", dpNoisyHistogramHandler(svc))
		// 差分隐私 — 分块
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/dp/chunked_count
		v1p.POST("/dp/chunked_count", dpChunkedCountHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/dp/chunked_sum
		v1p.POST("/dp/chunked_sum", dpChunkedSumHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/dp/chunked_mean
		v1p.POST("/dp/chunked_mean", dpChunkedMeanHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/dp/chunked_histogram
		v1p.POST("/dp/chunked_histogram", dpChunkedHistogramHandler(svc))
		// 差分隐私 — 向量/高级
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/dp/vector_sum
		v1p.POST("/dp/vector_sum", dpVectorSumHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/dp/vector_mean
		v1p.POST("/dp/vector_mean", dpVectorMeanHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/dp/aggregate
		v1p.POST("/dp/aggregate", dpAggregateHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/dp/adaptive_clip
		v1p.POST("/dp/adaptive_clip", dpAdaptiveClipHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/dp/groupby
		v1p.POST("/dp/groupby", dpGroupByHandler(svc))

		// 本地差分隐私
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/ldp/randomized_response
		v1p.POST("/ldp/randomized_response", randomizedResponseHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/ldp/orr
		v1p.POST("/ldp/orr", orrHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/ldp/perturb/binary
		v1p.POST("/ldp/perturb/binary", perturbBinaryBatchHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/ldp/perturb/categorical
		v1p.POST("/ldp/perturb/categorical", perturbCategoricalBatchHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/ldp/estimate/binary
		v1p.POST("/ldp/estimate/binary", estimateBinaryFrequencyHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/ldp/estimate/categorical
		v1p.POST("/ldp/estimate/categorical", estimateCategoricalHistogramHandler(svc))

		// K-匿名（记录级 + 表级 Mondrian + DataFrame）
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/k_anonymize
		v1p.POST("/k_anonymize", kAnonymizeHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/k_anonymize/record
		v1p.POST("/k_anonymize/record", kAnonymizeHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/k_anonymize/table
		v1p.POST("/k_anonymize/table", kAnonymizeTableHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/k_anonymize/dataframe
		v1p.POST("/k_anonymize/dataframe", kAnonymizeDataFrameHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/k_anonymize_table
		v1p.POST("/k_anonymize_table", kAnonymizeTableHandler(svc))

		// 查询混淆
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/qol/obfuscate
		v1p.POST("/qol/obfuscate", obfuscateHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/qol/obfuscate/batch
		v1p.POST("/qol/obfuscate/batch", obfuscateBatchHandler(svc))

		// HMAC 散列
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/hash
		v1p.POST("/hash", hashHMACHanlder(svc))

		// 预算
		// Demo: curl http://127.0.0.1:8079/v1/privacy/budget
		v1p.GET("/budget", budgetHandler(svc))
		// Demo: curl -X POST http://127.0.0.1:8079/v1/privacy/budget/reset
		v1p.POST("/budget/reset", budgetResetHandler(svc))

		// 文件上传处理 (CSV / JSON)
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/process_file
		v1p.POST("/process_file", processFileHandler(svc))

		// Profile 推荐
		// Demo: curl http://127.0.0.1:8079/v1/privacy/profile/recommend
		v1p.GET("/profile/recommend", profileRecommendHandler(svc))

		// 分类（兼容旧路径）
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/classify/field
		v1p.POST("/classify/field", classifyHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/privacy/classify/record
		v1p.POST("/classify/record", classifyBatchHandler(svc))
	}

	// /v1/agent/* — 通用处理流水线 (P0)
	v1a := r.Group("/v1/agent")
	{
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/agent/process
		v1a.POST("/process", agentProcessHandler(svc))
	}

	// /v1/ops/* — 运维诊断 (P1)
	v1o := r.Group("/v1/ops")
	{
		// Demo: curl http://127.0.0.1:8079/v1/ops/diagnostics
		v1o.GET("/diagnostics", diagnosticsHandler(svc))
	}

	// /v1/medical/* — 医疗流水线
	v1m := r.Group("/v1/medical")
	{
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/medical/process
		v1m.POST("/process", medicalProcessHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/medical/sanitize
		v1m.POST("/sanitize", medicalSanitizeHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/medical/sanitize/batch
		v1m.POST("/sanitize/batch", medicalBatchHandler(svc))
	}

	// /v1/dynclassification/* — 动态分类
	v1d := r.Group("/v1/dynclassification")
	{
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/dynclassification/classify
		v1d.POST("/classify", classifyHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/dynclassification/classify/batch
		v1d.POST("/classify/batch", classifyBatchHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/dynclassification/eval_record
		v1d.POST("/eval_record", evalRecordHandler(svc))
		// Demo: curl -X POST -H "Content-Type: application/json" -d '{}' http://127.0.0.1:8079/v1/dynclassification/profiles/reload
		v1d.POST("/profiles/reload", dynProfilesReloadHandler(svc))
	}

	// 【启动权限审计】遍历全部已注册路由，识别遗漏显式 scope 映射、静默落入 fail-closed
	// 兜底权限（"admin"）的新增接口并打 WARN，防止「加了路由忘配权限」。详见 pkg/auth/route_audit.go。
	pkgauth.LogRoutePermissionAudit(nil, "privacy-engine", r.Routes(),
		func(method, path string) string { return pkgauth.PermissionForRESTPath(path) },
		map[string]bool{"admin": true}, nil)
}

// ──────────────────────────────────────────────
// 健康检查
// ──────────────────────────────────────────────

// healthHandlerWithService 返回带 service 引用的健康检查 handler，
// 支持 ?deep=true 查询参数触发细粒度组件级健康快照。
func healthHandlerWithService(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Query("deep") == "true" && svc != nil {
			c.JSON(http.StatusOK, svc.DeepHealthCheck())
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok", "engine": "go"})
	}
}

func livezHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func readyzHandler(c *gin.Context) {
	if !isServerReady.Load() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":  "draining",
			"message": "Server is shutting down, draining in-flight traffic",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func readyzLLMHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if svc == nil {
			c.JSON(http.StatusOK, gin.H{"status": "ok", "llm": "not_configured"})
			return
		}
		configured, available := svc.LLMStatus(c.Request.Context())
		switch {
		case !configured:
			c.JSON(http.StatusOK, gin.H{"status": "ok", "llm": "not_configured"})
		case available:
			c.JSON(http.StatusOK, gin.H{"status": "ok", "llm": "ready"})
		default:
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "llm": "unavailable"})
		}
	}
}

// ──────────────────────────────────────────────
// 掩码处理器
// ──────────────────────────────────────────────

func maskHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Field     string `json:"field"`
			FieldName string `json:"field_name"`
			Value     string `json:"value" binding:"required"`
			Type      string `json:"type"`
			Salt      string `json:"salt"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		fieldName := req.Field
		if fieldName == "" {
			fieldName = req.FieldName
		}
		fieldType := req.Type
		if fieldType == "" {
			fieldType = inferMaskType(fieldName)
		}
		var result string
		var err error
		if fieldType == "sm3" || fieldType == "hash_sm3" {
			result = svc.HashSM3(req.Value, req.Salt)
		} else if fieldType == "hmac" || fieldType == "hash_hmac" {
			result = svc.HashHMAC(req.Value, req.Salt)
		} else {
			result, err = svc.MaskField(fieldType, req.Value)
			if err != nil {
				middleware.AbortWithError(c, http.StatusInternalServerError, "MASK_FAILED", "脱敏处理失败", err.Error())
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"field": fieldName, "masked": result, "result": result})
	}
}

func maskRecordHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Record map[string]string `json:"record" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		result := svc.MaskRecord(req.Record)
		c.JSON(http.StatusOK, gin.H{"result": result})
	}
}

func maskBatchHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Records []map[string]string `json:"records" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		results := svc.MaskBatch(req.Records)
		c.JSON(http.StatusOK, gin.H{"results": results})
	}
}

func maskDataFrameHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Data    []map[string]string `json:"data" binding:"required"`
			Columns []string            `json:"columns"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		results := svc.MaskBatch(req.Data)
		c.JSON(http.StatusOK, gin.H{"data": results})
	}
}

// ──────────────────────────────────────────────
// 差分隐私处理器
// ──────────────────────────────────────────────

func dpCountHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Count   int     `json:"count" binding:"required"`
			Epsilon float64 `json:"epsilon" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		result, err := svc.NoisyCount(c.Request.Context(), req.Count, req.Epsilon)
		if err != nil {
			middleware.AbortWithError(c, http.StatusTooManyRequests, "BUDGET_EXHAUSTED", "隐私预算已耗尽", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"result": result, "epsilon": req.Epsilon})
	}
}

func dpSumHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Values    []float64 `json:"values" binding:"required"`
			Epsilon   float64   `json:"epsilon" binding:"required"`
			ClipLower float64   `json:"clip_lower"`
			ClipUpper float64   `json:"clip_upper"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		sensitivity := req.ClipUpper - req.ClipLower
		if sensitivity <= 0 {
			sensitivity = 1.0
		}
		// 截断值至 [clipLower, clipUpper] 确保实际敏感度与声明一致
		clipped := clipValues(req.Values, req.ClipLower, req.ClipUpper)
		result, err := svc.NoisySum(c.Request.Context(), clipped, req.Epsilon, sensitivity)
		if err != nil {
			middleware.AbortWithError(c, http.StatusTooManyRequests, "BUDGET_EXHAUSTED", "隐私预算已耗尽", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"result": result, "epsilon": req.Epsilon})
	}
}

func dpMeanHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Values    []float64 `json:"values" binding:"required"`
			Epsilon   float64   `json:"epsilon" binding:"required"`
			Delta     float64   `json:"delta" binding:"required"`
			ClipBound float64   `json:"clip_bound"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		if req.ClipBound <= 0 {
			req.ClipBound = 1.0
		}
		result, err := svc.NoisyMean(c.Request.Context(), req.Values, req.Epsilon, req.Delta, req.ClipBound)
		if err != nil {
			middleware.AbortWithError(c, http.StatusTooManyRequests, "BUDGET_EXHAUSTED", "隐私预算已耗尽", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"result": result, "epsilon": req.Epsilon})
	}
}

func dpHistogramHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Values     []string `json:"values" binding:"required"`
			Categories []string `json:"categories" binding:"required"`
			Epsilon    float64  `json:"epsilon" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		trueCounts := make(map[string]int)
		for _, cat := range req.Categories {
			trueCounts[cat] = 0
		}
		for _, v := range req.Values {
			if _, ok := trueCounts[v]; ok {
				trueCounts[v]++
			}
		}
		result, err := svc.DPHistogram(c.Request.Context(), trueCounts, req.Epsilon)
		if err != nil {
			middleware.AbortWithError(c, http.StatusTooManyRequests, "BUDGET_EXHAUSTED", "隐私预算已耗尽", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"result": result})
	}
}

func noisyCountHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Count   int     `json:"count" binding:"required"`
			Epsilon float64 `json:"epsilon" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		result, err := svc.NoisyCount(c.Request.Context(), req.Count, req.Epsilon)
		if err != nil {
			middleware.AbortWithError(c, http.StatusTooManyRequests, "BUDGET_EXHAUSTED", "隐私预算已耗尽", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"noisy_count": result, "epsilon": req.Epsilon})
	}
}

func noisySumHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Values      []float64 `json:"values" binding:"required"`
			Epsilon     float64   `json:"epsilon" binding:"required"`
			Sensitivity float64   `json:"sensitivity" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		result, err := svc.NoisySum(c.Request.Context(), req.Values, req.Epsilon, req.Sensitivity)
		if err != nil {
			middleware.AbortWithError(c, http.StatusTooManyRequests, "BUDGET_EXHAUSTED", "隐私预算已耗尽", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"noisy_sum": result, "epsilon": req.Epsilon})
	}
}

func noisyMeanHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Values    []float64 `json:"values" binding:"required"`
			Epsilon   float64   `json:"epsilon" binding:"required"`
			Delta     float64   `json:"delta" binding:"required"`
			ClipBound float64   `json:"clip_bound" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		result, err := svc.NoisyMean(c.Request.Context(), req.Values, req.Epsilon, req.Delta, req.ClipBound)
		if err != nil {
			middleware.AbortWithError(c, http.StatusTooManyRequests, "BUDGET_EXHAUSTED", "隐私预算已耗尽", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"noisy_mean": result, "epsilon": req.Epsilon})
	}
}

func dpNoisyHistogramHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			TrueCounts map[string]int `json:"true_counts" binding:"required"`
			Epsilon    float64        `json:"epsilon" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		result, err := svc.DPHistogram(c.Request.Context(), req.TrueCounts, req.Epsilon)
		if err != nil {
			middleware.AbortWithError(c, http.StatusTooManyRequests, "BUDGET_EXHAUSTED", "隐私预算已耗尽", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"result": result})
	}
}

// ──────────────────────────────────────────────
// DP 分块处理器
// ──────────────────────────────────────────────

func dpChunkedCountHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Chunks  [][]float64 `json:"chunks" binding:"required"`
			Epsilon float64     `json:"epsilon" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		total := 0
		for _, chunk := range req.Chunks {
			total += len(chunk)
		}
		result, err := svc.NoisyCount(c.Request.Context(), total, req.Epsilon)
		if err != nil {
			middleware.AbortWithError(c, http.StatusTooManyRequests, "BUDGET_EXHAUSTED", "隐私预算已耗尽", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"result": result})
	}
}

func dpChunkedSumHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Chunks    [][]float64 `json:"chunks" binding:"required"`
			Epsilon   float64     `json:"epsilon" binding:"required"`
			ClipLower float64     `json:"clip_lower"`
			ClipUpper float64     `json:"clip_upper"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		var allValues []float64
		for _, chunk := range req.Chunks {
			allValues = append(allValues, chunk...)
		}
		sensitivity := req.ClipUpper - req.ClipLower
		if sensitivity <= 0 {
			sensitivity = 1.0
		}
		// 截断值至 [clipLower, clipUpper] 确保实际敏感度与声明一致
		clipped := clipValues(allValues, req.ClipLower, req.ClipUpper)
		result, err := svc.NoisySum(c.Request.Context(), clipped, req.Epsilon, sensitivity)
		if err != nil {
			middleware.AbortWithError(c, http.StatusTooManyRequests, "BUDGET_EXHAUSTED", "隐私预算已耗尽", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"result": result})
	}
}

func dpChunkedMeanHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Chunks    [][]float64 `json:"chunks" binding:"required"`
			Epsilon   float64     `json:"epsilon" binding:"required"`
			Delta     float64     `json:"delta" binding:"required"`
			ClipBound float64     `json:"clip_bound"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		var allValues []float64
		for _, chunk := range req.Chunks {
			allValues = append(allValues, chunk...)
		}
		if req.ClipBound <= 0 {
			req.ClipBound = 1.0
		}
		result, err := svc.NoisyMean(c.Request.Context(), allValues, req.Epsilon, req.Delta, req.ClipBound)
		if err != nil {
			middleware.AbortWithError(c, http.StatusTooManyRequests, "BUDGET_EXHAUSTED", "隐私预算已耗尽", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"result": result})
	}
}

func dpChunkedHistogramHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Chunks     [][]string `json:"chunks" binding:"required"`
			Categories []string   `json:"categories" binding:"required"`
			Epsilon    float64    `json:"epsilon" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		trueCounts := make(map[string]int)
		for _, cat := range req.Categories {
			trueCounts[cat] = 0
		}
		for _, chunk := range req.Chunks {
			for _, v := range chunk {
				if _, ok := trueCounts[v]; ok {
					trueCounts[v]++
				}
			}
		}
		result, err := svc.DPHistogram(c.Request.Context(), trueCounts, req.Epsilon)
		if err != nil {
			middleware.AbortWithError(c, http.StatusTooManyRequests, "BUDGET_EXHAUSTED", "隐私预算已耗尽", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"result": result})
	}
}

// ──────────────────────────────────────────────
// DP 向量/高级处理器
// ──────────────────────────────────────────────

func dpVectorSumHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Vectors [][]float64 `json:"vectors" binding:"required"`
			MaxNorm float64     `json:"max_norm" binding:"required"`
			Epsilon float64     `json:"epsilon" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		result, err := svc.DPVectorSum(c.Request.Context(), req.Vectors, req.MaxNorm, req.Epsilon)
		if err != nil {
			middleware.AbortWithError(c, http.StatusTooManyRequests, "BUDGET_EXHAUSTED", "隐私预算已耗尽", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"noisy_vector": result})
	}
}

func dpVectorMeanHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Vectors [][]float64 `json:"vectors" binding:"required"`
			MaxNorm float64     `json:"max_norm" binding:"required"`
			Epsilon float64     `json:"epsilon" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		result, err := svc.DPVectorMean(c.Request.Context(), req.Vectors, req.MaxNorm, req.Epsilon)
		if err != nil {
			middleware.AbortWithError(c, http.StatusTooManyRequests, "BUDGET_EXHAUSTED", "隐私预算已耗尽", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"mean_vector": result})
	}
}

func dpAggregateHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Rows      []map[string]string `json:"rows" binding:"required"`
			Specs     map[string]string   `json:"specs" binding:"required"` // map[字段名]聚合算子 (count/sum/mean)
			Epsilon   float64             `json:"epsilon" binding:"required"`
			Delta     float64             `json:"delta"`
			ClipLower float64             `json:"clip_lower"`
			ClipUpper float64             `json:"clip_upper"`
			Mechanism string              `json:"mechanism"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		// 委托给 service 层的 DPAggregate，该路径调用 SDK 的 dp.Aggregate，
		// 内置值截断（ClipValue → clipUpper）保障敏感度有界，确保 DP 保证成立。
		result, err := svc.DPAggregate(req.Rows, req.Specs, req.Epsilon, req.Delta, req.ClipLower, req.ClipUpper, req.Mechanism)
		if err != nil {
			middleware.AbortWithError(c, http.StatusTooManyRequests, "BUDGET_EXHAUSTED", "隐私预算已耗尽", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"result": result})
	}
}

func dpAdaptiveClipHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Values      []float64 `json:"values" binding:"required"`
			Epsilon     float64   `json:"epsilon" binding:"required"`
			InitialClip float64   `json:"initial_clip"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		clipUpper := req.InitialClip
		if clipUpper <= 0 {
			clipUpper = 1.0
		}
		// 自适应裁剪：使用百分位数估算
		sorted := make([]float64, len(req.Values))
		copy(sorted, req.Values)
		sort.Float64s(sorted)
		p95idx := int(float64(len(sorted)) * 0.95)
		if p95idx >= len(sorted) {
			p95idx = len(sorted) - 1
		}
		if len(sorted) > 0 {
			clipUpper = sorted[p95idx]
		}
		clipLower := clipUpper * 0.1
		c.JSON(http.StatusOK, gin.H{"clip_lower": clipLower, "clip_upper": clipUpper})
	}
}

func dpGroupByHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Rows      []map[string]string `json:"rows" binding:"required"`
			GroupCol  string              `json:"group_col" binding:"required"`
			TargetCol string              `json:"target_col" binding:"required"`
			Agg       string              `json:"agg" binding:"required"`
			Epsilon   float64             `json:"epsilon" binding:"required"`
			Delta     float64             `json:"delta"`
			ClipLower float64             `json:"clip_lower"`
			ClipUpper float64             `json:"clip_upper"`
			Mechanism string              `json:"mechanism"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		// 委托给 service 层的 DPGroupBy，该路径调用 SDK 的 dp.GroupBy，
		// 内置值截断（ClipValue → clipUpper）保障敏感度有界，确保 DP 保证成立。
		// 旧实现内联分组聚合但缺失截断步骤，sum/mean 的敏感度无界，隐私保证失效。
		result, err := svc.DPGroupBy(req.Rows, req.GroupCol, req.TargetCol, req.Agg,
			req.Epsilon, req.Delta, req.ClipLower, req.ClipUpper, req.Mechanism)
		if err != nil {
			middleware.AbortWithError(c, http.StatusTooManyRequests, "BUDGET_EXHAUSTED", "隐私预算已耗尽", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"result": result})
	}
}

// ──────────────────────────────────────────────
// LDP 处理器
// ──────────────────────────────────────────────

func randomizedResponseHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Value   bool    `json:"value" binding:"required"`
			Epsilon float64 `json:"epsilon" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		result := svc.RandomizedResponse(req.Value, req.Epsilon)
		c.JSON(http.StatusOK, gin.H{"result": result})
	}
}

func orrHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Value      int     `json:"value" binding:"required"`
			Epsilon    float64 `json:"epsilon" binding:"required"`
			DomainSize int     `json:"domain_size" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		result := svc.ORRResponse(req.Value, req.Epsilon, req.DomainSize)
		c.JSON(http.StatusOK, gin.H{"result": result})
	}
}

func perturbBinaryBatchHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Values  []int   `json:"values" binding:"required"`
			Epsilon float64 `json:"epsilon" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		result := svc.PerturbBinaryBatch(req.Values, req.Epsilon)
		c.JSON(http.StatusOK, gin.H{"result": result})
	}
}

func perturbCategoricalBatchHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Values     []string `json:"values" binding:"required"`
			Categories []string `json:"categories" binding:"required"`
			Epsilon    float64  `json:"epsilon" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		result := svc.PerturbCategoricalBatch(req.Values, req.Categories, req.Epsilon)
		c.JSON(http.StatusOK, gin.H{"result": result})
	}
}

func estimateBinaryFrequencyHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			ReportedValues []int   `json:"reported_values" binding:"required"`
			Epsilon        float64 `json:"epsilon" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		result := svc.EstimateBinaryFrequency(req.ReportedValues, req.Epsilon)
		c.JSON(http.StatusOK, gin.H{"frequency": result})
	}
}

func estimateCategoricalHistogramHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			ReportedValues []string `json:"reported_values" binding:"required"`
			Categories     []string `json:"categories" binding:"required"`
			Epsilon        float64  `json:"epsilon" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		result := svc.EstimateCategoricalHistogram(req.ReportedValues, req.Categories, req.Epsilon)
		c.JSON(http.StatusOK, gin.H{"histogram": result})
	}
}

// ──────────────────────────────────────────────
// K-匿名处理器
// ──────────────────────────────────────────────

func kAnonymizeHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Records  []map[string]string `json:"records" binding:"required"`
			QIFields []string            `json:"qi_fields" binding:"required"`
			K        int                 `json:"k" binding:"required,min=1"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		kanoRecords := make([]kano.Record, len(req.Records))
		for i, r := range req.Records {
			kanoRecords[i] = kano.Record(r)
		}
		result, err := svc.KAnonymize(kanoRecords, req.QIFields, req.K)
		if err != nil {
			middleware.AbortWithError(c, http.StatusInternalServerError, "KANONYMIZE_FAILED", "K-匿名处理失败", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"records":     result.Records,
			"k":           result.K,
			"group_count": result.GroupCount,
		})
	}
}

// kAnonymizeTableHandler 表级 K-匿名（Mondrian 算法），对齐 Python kano_table。
func kAnonymizeTableHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Records  []map[string]string `json:"records" binding:"required"`
			QICols   []string            `json:"qi_cols" binding:"required"`
			K        int                 `json:"k" binding:"required,min=2"`
			MaxDepth int                 `json:"max_depth"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		rows := make([]kano.Record, len(req.Records))
		for i, r := range req.Records {
			rows[i] = kano.Record(r)
		}
		result, err := svc.KAnonymizeTable(rows, req.QICols, req.K)
		if err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "KANONYMIZE_TABLE_FAILED", "表级K-匿名处理失败", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"records":                   result.Records,
			"k":                         result.K,
			"qi_cols":                   req.QICols,
			"group_count":               result.GroupCount,
			"equivalence_classes_count": result.GroupCount,
		})
	}
}

// kAnonymizeDataFrameHandler 结构化 DataFrame K-匿名。
func kAnonymizeDataFrameHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Records []map[string]interface{} `json:"records" binding:"required"`
			QICols  []string                 `json:"qi_cols" binding:"required"`
			K       int                      `json:"k" binding:"required,min=2"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		result, err := svc.KAnonymizeDataFrame(req.Records, req.QICols, req.K)
		if err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "KANONYMIZE_DATAFRAME_FAILED", "DataFrame K-匿名处理失败", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"result":   result,
			"k":        req.K,
			"qi_cols":  req.QICols,
			"rows_out": len(result),
		})
	}
}

// ──────────────────────────────────────────────
// 查询混淆处理器
// ──────────────────────────────────────────────

func obfuscateHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Query     string `json:"query" binding:"required"`
			NumDecoys int    `json:"num_decoys" binding:"required,min=1"`
			Domain    string `json:"domain" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		queries, realIdx := svc.ObfuscateQuery(req.Query, req.NumDecoys, req.Domain)
		c.JSON(http.StatusOK, gin.H{
			"queries":    queries,
			"real_index": realIdx,
		})
	}
}

func obfuscateBatchHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Queries   []string `json:"queries" binding:"required"`
			NumDecoys int      `json:"num_decoys" binding:"required,min=1"`
			Domain    string   `json:"domain" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		results := svc.ObfuscateQueryBatch(req.Queries, req.NumDecoys, req.Domain)
		c.JSON(http.StatusOK, gin.H{"results": results})
	}
}

// ──────────────────────────────────────────────
// 分类处理器
// ──────────────────────────────────────────────

func classifyHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Field string `json:"field" binding:"required"`
			Value string `json:"value" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		result := svc.Classify(req.Field, req.Value)
		c.JSON(http.StatusOK, result)
	}
}

func classifyBatchHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return evalRecordHandler(svc)
}

func evalRecordHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Record  map[string]any   `json:"record"`
			Records []map[string]any `json:"records"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		if len(req.Records) > 0 {
			converted := make([]map[string]string, len(req.Records))
			for i, r := range req.Records {
				cm := make(map[string]string, len(r))
				for k, v := range r {
					cm[k] = fmt.Sprintf("%v", v)
				}
				converted[i] = cm
			}
			results := svc.ClassifyBatch(converted)
			overall := overallSecurityLevel(results...)
			c.JSON(http.StatusOK, gin.H{
				"classifications": results,
				"level":           overall,
				"overall_level":   overall,
			})
			return
		}
		if len(req.Record) > 0 {
			results := make(map[string]any, len(req.Record))
			classified := make([]*dynclassification.ClassificationResult, 0, len(req.Record))
			for k, v := range req.Record {
				res := svc.Classify(k, fmt.Sprintf("%v", v))
				results[k] = res
				classified = append(classified, res)
			}
			overall := overallSecurityLevel(classified...)
			c.JSON(http.StatusOK, gin.H{
				"result":          results,
				"classifications": results,
				"level":           overall,
				"overall_level":   overall,
			})
			return
		}
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "record or records is required", nil)
	}
}

// overallSecurityLevel 取多条字段分类结果中最高的敏感级别，返回规则库 L1~L5 标识。
// 无任何可识别级别时返回空串——调用方必须按 fail-closed 处理，不得替换为默认等级。
func overallSecurityLevel(results ...*dynclassification.ClassificationResult) string {
	ids := make([]string, 0, len(results))
	for _, res := range results {
		if res != nil {
			// 优先使用 Level.LevelID() 方法获取 L1~L5 标识（与 arbitrate 互补，
			// 确保即使未来新增不经过 arbitrate 的分类路径也能正确取到定级）。
			ids = append(ids, res.Level.LevelID())
		}
	}
	return naming.MaxSecurityLevelID(ids...)
}

// ──────────────────────────────────────────────
// 医疗流水线处理器
// ──────────────────────────────────────────────

func medicalSanitizeHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Record map[string]string `json:"record" binding:"required"`
			Domain string            `json:"domain" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		result, err := svc.SanitizeMedicalRecord(req.Record, req.Domain)
		if err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_DATASOURCE_ID", "未知或不支持的数据源标识", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"result": result})
	}
}

func medicalBatchHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Records []map[string]string `json:"records" binding:"required"`
			Domain  string              `json:"domain" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		results, err := svc.SanitizeMedicalBatch(req.Records, req.Domain)
		if err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_DATASOURCE_ID", "未知或不支持的数据源标识", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"results": results})
	}
}

// ──────────────────────────────────────────────
// HMAC 散列处理器
// ──────────────────────────────────────────────

func hashHMACHanlder(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Value string `json:"value" binding:"required"`
			Salt  string `json:"salt" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		result := svc.HashHMAC(req.Value, req.Salt)
		c.JSON(http.StatusOK, gin.H{"hash": result})
	}
}

// ──────────────────────────────────────────────
// 预算查询处理器
// ──────────────────────────────────────────────

func budgetHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		status := svc.BudgetStatus()
		c.JSON(http.StatusOK, status)
	}
}

func budgetResetHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		status := svc.BudgetReset()
		c.JSON(http.StatusOK, status)
	}
}

// ──────────────────────────────────────────────
// Agent & Medical 通用处理流水线（/v1/agent/process, /v1/medical/process）
// ──────────────────────────────────────────────

func agentProcessHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Records      []map[string]interface{} `json:"records" binding:"required"`
			APICode      string                   `json:"api_code"`
			DatasourceID string                   `json:"datasource_id"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		if len(req.Records) > 500 {
			middleware.AbortWithError(c, http.StatusBadRequest, "PAYLOAD_TOO_LARGE", "记录数超过上限 500", "")
			return
		}

		result, err := svc.ProcessAgentData(req.Records, req.APICode, req.DatasourceID)
		if err != nil {
			middleware.AbortWithError(c, http.StatusInternalServerError, "PROCESS_AGENT_FAILED", "Agent 数据处理失败", err.Error())
			return
		}

		c.JSON(http.StatusOK, result)
	}
}

func medicalProcessHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Records []map[string]interface{} `json:"records" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "请求参数校验失败", err.Error())
			return
		}
		if len(req.Records) > 500 {
			middleware.AbortWithError(c, http.StatusBadRequest, "PAYLOAD_TOO_LARGE", "记录数超过上限 500", "")
			return
		}

		result, err := svc.ProcessMedicalData(req.Records)
		if err != nil {
			middleware.AbortWithError(c, http.StatusInternalServerError, "PROCESS_MEDICAL_FAILED", "医疗数据处理失败", err.Error())
			return
		}

		c.JSON(http.StatusOK, result)
	}
}

// ──────────────────────────────────────────────
// 运维诊断（/v1/ops/diagnostics）
// ──────────────────────────────────────────────

func diagnosticsHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		refresh := c.Query("refresh") == "true"
		if refresh {
			identity := security.GetIdentity(c)
			if identity != nil && !identity.HasPermission("ops:admin") {
				middleware.AbortWithError(c, http.StatusForbidden, "FORBIDDEN", "Forbidden: refresh requires ops:admin scope", "")
				return
			}
		}

		diag := svc.Diagnostics(refresh)
		c.JSON(http.StatusOK, diag)
	}
}

// ──────────────────────────────────────────────
// 文件上传处理（/v1/privacy/process_file）
// ──────────────────────────────────────────────

func processFileHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 解析 multipart form (上限 50MB)
		if err := c.Request.ParseMultipartForm(50 << 20); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_MULTIPART", "无法解析 multipart 表单", err.Error())
			return
		}

		file, header, err := c.Request.FormFile("file")
		if err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "MISSING_FILE", "缺少 file 字段", err.Error())
			return
		}
		defer file.Close()

		if header.Size > 50<<20 {
			middleware.AbortWithError(c, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "文件过大（超过 50MB 上限）", "")
			return
		}

		operation := c.Request.FormValue("operation")
		if operation == "" {
			operation = "mask_dataframe"
		}
		paramsJSON := c.Request.FormValue("params")
		if paramsJSON == "" {
			paramsJSON = "{}"
		}

		var params map[string]interface{}
		if err := json.Unmarshal([]byte(paramsJSON), &params); err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_PARAMS", "params 需为有效 JSON", err.Error())
			return
		}

		// 流式处理：CSV/JSON 的 mask_dataframe 逐行解码不入内存快照，
		// 其余格式/操作由 ProcessFileStream 内部回退到物化路径。
		result, err := svc.ProcessFileStream(file, header.Filename, operation, params)
		if err != nil {
			if errors.Is(err, service.ErrFileTooLarge) {
				middleware.AbortWithError(c, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "文件过大（超过 50MB 上限）", err.Error())
				return
			}
			middleware.AbortWithError(c, http.StatusBadRequest, "PROCESS_FILE_FAILED", "文件处理失败", err.Error())
			return
		}

		c.JSON(http.StatusOK, result)
	}
}

// ──────────────────────────────────────────────
// 动态分类 Profiles 重载
// ──────────────────────────────────────────────

func dynProfilesReloadHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := svc.ReloadDynamicProfiles(); err != nil {
			middleware.AbortWithError(c, http.StatusInternalServerError, "RELOAD_FAILED", "重载动态配置失败", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"message": "Dynamic classification profiles reloaded successfully",
		})
	}
}

// ──────────────────────────────────────────────
// Profile 推荐
// ──────────────────────────────────────────────

type profileRecommendRequest struct {
	Namespace string                   `json:"namespace"`
	Values    []float64                `json:"values"`
	Rows      []map[string]interface{} `json:"rows"`
	QICols    []string                 `json:"qi_cols"`
}

func profileRecommendHandler(svc *service.PrivacyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req profileRecommendRequest
		if c.Request.ContentLength > 0 {
			if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
				middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_REQUEST", "解析请求体失败", err.Error())
				return
			}
		}
		if req.Namespace == "" {
			req.Namespace = "default"
		}

		params := svc.RecommendParams(req.Namespace, req.Values, req.Rows, req.QICols)

		c.JSON(http.StatusOK, gin.H{
			"status":             "success",
			"namespace":          req.Namespace,
			"recommended_params": params,
		})
	}
}

// ──────────────────────────────────────────────
// 辅助函数
// ──────────────────────────────────────────────

// clipValues 将数值切片截断至 [lower, upper] 区间。
// clipLower == clipUpper 时不做截断（未指定裁剪参数）。
func clipValues(vals []float64, clipLower, clipUpper float64) []float64 {
	if clipLower == 0 && clipUpper == 0 {
		return vals // 未指定裁剪参数
	}
	if clipUpper <= clipLower {
		return vals
	}
	clipped := make([]float64, len(vals))
	for i, v := range vals {
		switch {
		case v > clipUpper:
			clipped[i] = clipUpper
		case v < clipLower:
			clipped[i] = clipLower
		default:
			clipped[i] = v
		}
	}
	return clipped
}

func registerPprof(r *gin.Engine) {
	pprofGroup := r.Group("/debug/pprof")
	{
		pprofGroup.GET("/", gin.WrapF(httppprof.Index))
		pprofGroup.GET("/cmdline", gin.WrapF(httppprof.Cmdline))
		pprofGroup.GET("/profile", gin.WrapF(httppprof.Profile))
		pprofGroup.POST("/symbol", gin.WrapF(httppprof.Symbol))
		pprofGroup.GET("/symbol", gin.WrapF(httppprof.Symbol))
		pprofGroup.GET("/trace", gin.WrapF(httppprof.Trace))
		pprofGroup.GET("/allocs", gin.WrapH(httppprof.Handler("allocs")))
		pprofGroup.GET("/block", gin.WrapH(httppprof.Handler("block")))
		pprofGroup.GET("/goroutine", gin.WrapH(httppprof.Handler("goroutine")))
		pprofGroup.GET("/heap", gin.WrapH(httppprof.Handler("heap")))
		pprofGroup.GET("/mutex", gin.WrapH(httppprof.Handler("mutex")))
		pprofGroup.GET("/threadcreate", gin.WrapH(httppprof.Handler("threadcreate")))
	}
}

func inferMaskType(fieldName string) string {
	lower := strings.ToLower(fieldName)
	switch {
	case containsAny(lower, "id_card", "idcard", "cert_no", "identity", "身份证"):
		return "id_card"
	case containsAny(lower, "phone", "mobile", "tel", "手机", "电话"):
		return "phone"
	case containsAny(lower, "bank", "credit_card", "银行卡"):
		return "bank_card"
	case containsAny(lower, "email", "mail", "邮箱"):
		return "email"
	case containsAny(lower, "address", "addr", "地址"):
		return "address"
	case containsAny(lower, "name", "姓名", "patient_name"):
		return "name"
	case containsAny(lower, "officer", "军官"):
		return "officer_id"
	default:
		return "default"
	}
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
