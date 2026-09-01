// Package handlers implements the HTTP REST interface layer for the Go gRPC proxy backend.
// Package handlers 实现 Go gRPC 代理后端的 HTTP REST 接口层。
//
// Responsibilities / 职责：
//   - Receive HTTP/JSON requests from the frontend React console
//     接收前端 React 控制台的 HTTP/JSON 请求
//   - Map REST paths to corresponding gRPC calls via mapper
//     通过 mapper 将 REST 路径映射为对应的 gRPC 调用
//   - Convert protobuf responses to JSON format displayable by frontend
//     将 protobuf 响应转换为前端可展示的 JSON 格式
//   - Optionally host frontend static build artifacts, enabling Go backend to serve full Console UI
//     可选托管前端静态构建产物，使 Go 后端可独立提供完整 Console UI
//
// Design goal / 设计目标：
//
//	Expose a unified REST/JSON contract for the React console and proxy requests
//	to the upstream PrivShield agent via gRPC.
//	为 React 控制台提供统一的 REST/JSON 契约，并通过 gRPC 代理到上游 PrivShield agent。
//
// Route list / 路由清单：
//
//	GET  /api/health   → Health check (backend self + upstream agent)
//	GET  /api/samples  → Return sample payloads for all endpoints
//	POST /api/proxy    → Single request proxy forwarding (REST → gRPC)
//	POST /api/batch    → Batch request forwarding
//	POST /api/upload   → File upload + privacy processing (masking/K-anonymity/classification)
//	POST /api/lb_test  → Load-balancing strategy test
package handlers

import (
	"bytes"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	// encoding/json：用于 JSON 序列化/反序列化（params 解析、RecordEntry 转换）
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/fengzhizi319/PrivShield/console/bff-go/internal/agent"
	"github.com/fengzhizi319/PrivShield/console/bff-go/internal/config"
	"github.com/fengzhizi319/PrivShield/console/bff-go/internal/fileparse"
	"github.com/fengzhizi319/PrivShield/console/bff-go/internal/lbtest"
	"github.com/fengzhizi319/PrivShield/console/bff-go/internal/mapper"
	"github.com/fengzhizi319/PrivShield/console/bff-go/internal/microservices"
	"github.com/fengzhizi319/PrivShield/console/bff-go/internal/models"
	"github.com/fengzhizi319/PrivShield/console/bff-go/internal/samples"
	pb "github.com/fengzhizi319/PrivShield/console/bff-go/proto"
	pkgagent "github.com/fengzhizi319/PrivShield/pkg/agent"
	"github.com/fengzhizi319/PrivShield/pkg/metrics"
	"github.com/fengzhizi319/PrivShield/pkg/middleware"
)

// 本控制台后端的身份标识常量，随每个响应下发给前端。
//
// 用途：前端界面展示"当前请求由哪个后端、以何种协议与 agent 通信"，
// 使 REST / gRPC 两种上游协议切换可被直观验证。
const (
	// backendVia：标识响应经由的后端类型，"go-grpc" 表示通过 Go 代理后端转发
	backendVia = "go-grpc"
	// agentProtocol：标识与上游 agent 通信的协议，"gRPC" 表示使用 gRPC 调用
	agentProtocol = "gRPC"
)

// Server 聚合 HTTP 处理器所需的全部依赖。
type Server struct {
	client     *agent.Client
	mapper     *mapper.Mapper
	cfg        *config.Config
	logger     *slog.Logger
	mc         *metrics.Collector
	httpClient *http.Client              // Shared HTTP client for REST calls / 共享 HTTP 客户端
	msClient   *microservices.ClientPool // Direct Go microservice proxy clients / 直连 Go 微服务代理客户端
	secCleanup func()                    // P57 fix: cleanup function for securityMiddleware ticker goroutine
}

func New(client *agent.Client, cfg *config.Config, logger *slog.Logger, mc *metrics.Collector) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	var tlsClientConfig *tls.Config
	if cfg.AgentTLSEnabled {
		tlsClientConfig = &tls.Config{
			ServerName:         cfg.AgentTLSServerName,
			InsecureSkipVerify: cfg.AgentTLSInsecureSkipVerify,
		}
		if cfg.AgentTLSCAFile != "" {
			if caPEM, err := os.ReadFile(cfg.AgentTLSCAFile); err == nil {
				caPool := x509.NewCertPool()
				if caPool.AppendCertsFromPEM(caPEM) {
					tlsClientConfig.RootCAs = caPool
				}
			}
		}
		if cfg.AgentTLSCertFile != "" && cfg.AgentTLSKeyFile != "" {
			if cert, err := tls.LoadX509KeyPair(cfg.AgentTLSCertFile, cfg.AgentTLSKeyFile); err == nil {
				tlsClientConfig.Certificates = []tls.Certificate{cert}
			}
		}
	}

	transport := &http.Transport{
		TLSClientConfig:     tlsClientConfig,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}

	return &Server{
		client: client,
		mapper: mapper.New(),
		cfg:    cfg,
		logger: logger,
		mc:     mc,
		httpClient: &http.Client{
			Timeout:   60 * time.Second,
			Transport: transport,
		},
		msClient: microservices.NewClientPool(cfg),
	}
}

// Shutdown gracefully stops background goroutines.
// P57 fix: stop the securityMiddleware ticker goroutine to prevent goroutine leak.
func (s *Server) Shutdown() {
	if s.secCleanup != nil {
		s.secCleanup()
	}
}

func (s *Server) RegisterRoutes(r *gin.Engine) {
	// Shared middleware chain / 共享中间件链
	r.Use(middleware.TraceMiddleware())
	r.Use(middleware.StructuredLogger(s.logger, "backend-go"))
	r.Use(middleware.Recovery(s.logger, "backend-go"))
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.MaxBodySize(64 << 20)) // 64 MiB max payload protection (supports larger CSV uploads)
	r.Use(middleware.MaxConcurrent(1000))   // 并发在途请求上限，超限返回 503
	r.Use(middleware.CORS(nil))             // backend-go 默认允许所有来源（开发模式）
	// P57 fix: capture cleanup function from securityMiddleware to stop ticker goroutine on shutdown.
	secHandler, secCleanup := securityMiddleware(s.cfg.ConsoleAPIKey, s.cfg.ConsoleRateLimit)
	s.secCleanup = secCleanup
	r.Use(secHandler)

	r.GET("/health", s.Health)
	r.GET("/api/health", s.Health)
	r.GET("/api/samples", s.Samples)
	r.POST("/api/proxy", s.Proxy)
	r.POST("/api/batch", s.Batch)
	r.POST("/api/upload", s.Upload)
	r.POST("/api/lb_test", s.LbTest)
	r.POST("/api/concurrency_test", s.ConcurrencyTest)
	r.POST("/api/medical_pipeline", s.MedicalPipeline)
	r.POST("/api/yibao_pipeline", s.YibaoPipeline)
	r.POST("/api/pipeline/process", s.PipelineProcess)

	// Direct Go microservice proxy routes (Phase 2)
	// 主控制台 BFF 直连 service-hub / datasource-mgr / audit-log 的代理入口。
	// P0-7（门禁 G-01）：不再是无限制透明代理，转发的每个方法 + 路径都要过
	// isAllowedMicroserviceProxyPath 默认拒绝白名单，原始记录/样本端点禁止出域。
	r.Any("/api/hub/*path", s.ProxyHub)
	r.Any("/api/datasource/*path", s.ProxyDatasource)
	r.Any("/api/audit/*path", s.ProxyAudit)

	r.GET("/metrics", s.mc.Handler())
	s.registerStatic(r)
}

// registerStatic 挂载前端构建产物（SPA），使 Go 后端能独立提供 Console UI。
//
// 执行逻辑：
//  1. 检查配置中的 StaticDistDir 是否为空，空则跳过（纯 API 模式）
//  2. 检查目录是否存在且为合法目录，不存在则跳过
//  3. 检查 index.html 是否存在，不存在则跳过
//  4. 挂载 /assets 静态资源目录
//  5. 注册 SPA 回退路由：非 /api 路由一律返回 index.html
//
// 路由规则与历史 Python 后端保持一致：
//   - /assets/* → 静态资源（带内容哈希，可强缓存）
//   - 其余非 /api 路由 → 返回 index.html（SPA 回退，禁止缓存）
func (s *Server) registerStatic(r *gin.Engine) {
	// 读取配置中的静态文件目录路径
	distDir := s.cfg.StaticDistDir
	// 目录路径为空时直接返回，仅以 API 模式运行
	if distDir == "" {
		return
	}
	// 检查目录是否存在且为合法目录（非文件）
	info, err := os.Stat(distDir)
	if err != nil || !info.IsDir() {
		// 目录不存在或不是目录时打印日志并跳过，不阻止服务启动
		s.logger.Warn("static dist dir not found", "path", distDir, "serving", "API only")
		return
	}
	// 拼接 index.html 完整路径，检查其是否存在
	indexPath := filepath.Join(distDir, "index.html")
	if _, err := os.Stat(indexPath); err != nil {
		// index.html 不存在说明前端未构建，跳过静态托管
		s.logger.Warn("index.html not found", "dir", distDir, "serving", "API only")
		return
	}

	// 检查 assets 子目录是否存在，存在则挂载为静态资源服务
	// /assets/* 路径下的文件带有内容哈希，浏览器可安全强缓存
	if assetsDir := filepath.Join(distDir, "assets"); dirExists(assetsDir) {
		// r.Static 将 /assets 路径映射到本地 assetsDir 目录，
		// Gin 会自动设置正确的 Content-Type 与 Last-Modified 头
		r.Static("/assets", assetsDir)
	}

	// 注册 NoRoute 处理器：当请求不匹配任何已注册路由时触发。
	// 用于实现 SPA 的前端路由回退：
	//   - /api/* 路径 → 返回 404 JSON 错误（API 路由未匹配说明请求无效）
	//   - 其他路径 → 返回 index.html（让前端 React Router 处理路由）
	r.NoRoute(func(c *gin.Context) {
		// 判断请求路径是否以 /api/ 开头
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			// API 路由未匹配，返回标准 404 JSON 响应
			middleware.AbortWithError(c, http.StatusNotFound, "NOT_FOUND", "Not Found", nil)
			return
		}
		// 非 API 路由：设置 no-cache 响应头，防止浏览器缓存 index.html。
		// 必须禁止缓存，否则重新构建前端后浏览器仍会加载旧版本的 index.html；
		// 而 /assets/* 下的带哈希资源则由浏览器正常缓存（内容变则 URL 变）。
		c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
		// 返回 index.html 文件，由前端 React Router 接管后续路由
		c.File(indexPath)
	})
	// 打印静态托管启用日志，便于调试确认
	s.logger.Info("Console UI enabled", "static_dir", distDir)
}

// dirExists 判断指定路径是否存在且为目录。
// 用于静态文件托管前检查 assets 子目录是否可用。
func dirExists(path string) bool {
	// os.Stat 获取文件/目录信息，err != nil 表示不存在
	info, err := os.Stat(path)
	// 存在且为目录时返回 true
	return err == nil && info.IsDir()
}

// corsMiddleware is replaced by shared middleware.CORS() in RegisterRoutes.
// corsMiddleware 已由共享 middleware.CORS() 替代，保留函数签名以免破坏兼容性。

// Health 检查 Go 代理自身与上游 agent 的连通性，返回结构化健康状态。
//
// 响应字段与历史 Python 后端保持一致：
//   - backend：Go 代理自身状态（始终为 "ok"）
//   - agent：上游 agent 状态（"ok" 或 "unreachable"）
//   - agent_url：上游 agent 的 gRPC 地址
//   - latency_ms：Health RPC 调用耗时（毫秒）
//   - error：连接失败时的错误信息
//
// isRestProtocol 检查请求是否显式要求走 REST 协议转发到 Agent
func isRestProtocol(c *gin.Context) bool {
	return strings.EqualFold(c.Query("protocol"), "rest") ||
		strings.EqualFold(c.GetHeader("X-PrivShield-Protocol"), "REST")
}

// Health 检查 Go 代理自身与上游 agent 的连通性，返回结构化健康状态。
//
// 响应字段：
//   - backend：Go 代理自身状态（始终为 "ok"）
//   - agent：上游 agent 状态（"ok" 或 "unreachable"）
//   - agent_url：上游 agent 的 gRPC/REST 地址
//   - latency_ms：调用耗时（毫秒）
//   - error：连接失败时的错误信息
//   - via：后端标识 ("go-grpc" 或 "go-rest-proxy")
//   - protocol：协议标识 ("gRPC" 或 "REST")
//
// 前端通过该接口判断后端连接是否正常，并展示状态灯。
func (s *Server) Health(c *gin.Context) {
	start := time.Now()
	if isRestProtocol(c) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		respData, statusCode, err := s.callRest(ctx, "GET", "/health", nil, "", "")
		latency := time.Since(start).Milliseconds()
		if err != nil || statusCode >= 400 {
			errMsg := "agent unreachable"
			if err != nil {
				errMsg = err.Error()
			}
			c.JSON(http.StatusOK, models.ConsoleHealth{
				Backend:   "ok",
				Agent:     "unreachable",
				AgentURL:  s.agentRestBaseURL(),
				LatencyMs: &latency,
				Error:     errMsg,
				Via:       "go-rest-proxy",
				Protocol:  "REST",
			})
			return
		}
		c.JSON(http.StatusOK, models.ConsoleHealth{
			Backend:   "ok",
			Agent:     respData,
			AgentURL:  s.agentRestBaseURL(),
			LatencyMs: &latency,
			Via:       "go-rest-proxy",
			Protocol:  "REST",
		})
		return
	}

	// 默认使用 gRPC 协议检查
	// 将追踪 ID 注入 context，确保 gRPC 健康检查也携带分布式追踪上下文
	healthCtx := s.client.WithTrace(context.Background(), middleware.GetTraceID(c))
	ctx, cancel := context.WithTimeout(healthCtx, 3*time.Second)
	defer cancel()

	resp, err := s.client.Health(ctx)
	if err != nil {
		// 瞬态重试一次（处理连接握手与初始化期间的抖动）
		retryCtx, retryCancel := context.WithTimeout(s.client.WithTrace(context.Background(), middleware.GetTraceID(c)), 2*time.Second)
		resp, err = s.client.Health(retryCtx)
		retryCancel()
	}
	latency := time.Since(start).Milliseconds()

	if err != nil {
		c.JSON(http.StatusOK, models.ConsoleHealth{
			Backend:   "ok",
			Agent:     "unreachable",
			AgentURL:  s.cfg.AgentAddress(),
			LatencyMs: &latency,
			Error:     err.Error(),
			Via:       backendVia,
			Protocol:  agentProtocol,
		})
		return
	}

	c.JSON(http.StatusOK, models.ConsoleHealth{
		Backend:   "ok",
		Agent:     map[string]string{"status": resp.Status, "namespace": resp.Namespace},
		AgentURL:  s.cfg.AgentAddress(),
		LatencyMs: &latency,
		Via:       backendVia,
		Protocol:  agentProtocol,
	})
}

// Samples 返回所有端点的示例 payload 列表。
func (s *Server) Samples(c *gin.Context) {
	c.JSON(http.StatusOK, models.SamplesResponse{Samples: samples.List()})
}

// Proxy 将前端的单请求转发到上游 agent 的对应 gRPC 或 REST 方法。
func (s *Server) Proxy(c *gin.Context) {
	var req models.ProxyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("invalid request body: %v", err), nil)
		return
	}

	start := time.Now()
	// 当显式指定 REST 协议或路径属于 REST-only 时走 REST 转发
	if isRestProtocol(c) || restOnlyPath(req.Path) {
		s.proxyRest(c, start, req)
		return
	}

	// 核心调用：通过 gRPC mapper 转发
	// 将追踪 ID 注入 gRPC outgoing metadata，实现 HTTP → gRPC 跨协议全链路追踪
	ctx, cancel := context.WithTimeout(c.Request.Context(), s.grpcCallTimeout())
	defer cancel()
	data, err := s.mapper.Dispatch(s.client.WithTrace(s.client.WithAuth(ctx), middleware.GetTraceID(c)), s.client.Raw(), req.Path, req.Body)
	duration := time.Since(start).Milliseconds()

	if err != nil {
		if strings.Contains(err.Error(), "unsupported gRPC path") {
			s.proxyRest(c, start, req)
			return
		}
		status := http.StatusBadRequest
		if isUnavailable(err) {
			status = http.StatusBadGateway
		}
		middleware.AbortWithError(c, status, middleware.ErrorCodeFromStatus(status), err.Error(), nil)
		return
	}

	c.JSON(http.StatusOK, models.ProxyResponse{
		Status:     http.StatusOK,
		DurationMs: duration,
		Data:       data,
		Via:        backendVia,
		Protocol:   agentProtocol,
	})
}

// restOnlyPath 报告路径是否仅存在于 Agent REST 服务（无 gRPC 对应方法），
// 这些路径直接走 REST 转发。Proxy 与 ConcurrencyTest 共用同一判定。
func restOnlyPath(path string) bool {
	return strings.HasPrefix(path, "/v1/dynclassification/") ||
		strings.HasPrefix(path, "/v1/ops/") ||
		path == "/health"
}

// isAllowedConcurrencyPath 校验压测路径是否在白名单内。
// P37 fix: 防止通过压测端点访问敏感内部接口（如 /v1/ops/* 运维接口）。
// 白名单与 Proxy 路由保持一致，仅允许隐私处理与分类分级相关路径。
func isAllowedConcurrencyPath(rawPath string) bool {
	// path.Clean 先规范化路径，消除 ".." 穿越，防止 /v1/privacy/../ops/health 绕过前缀检查
	cleaned := path.Clean(rawPath)
	return strings.HasPrefix(cleaned, "/v1/privacy/") ||
		strings.HasPrefix(cleaned, "/v1/dynclassification/") ||
		strings.HasPrefix(cleaned, "/v1/medical/") ||
		strings.HasPrefix(cleaned, "/v1/pipeline/") ||
		cleaned == "/health"
}

// agentRestBaseURL 返回 agent REST 服务的基础地址。
// REST 与 gRPC 是 agent 的两个独立服务，主机/端口可能不同，
// 当开启 TLS 时自动将协议切换为 https://。
func (s *Server) agentRestBaseURL() string {
	if u := os.Getenv("PRIVACY_AGENT_REST_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	if u := os.Getenv("PRIVACY_AGENT_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}

	scheme := "http"
	if s.cfg.AgentTLSEnabled || os.Getenv("PRIVACY_TLS_ENABLED") == "true" {
		scheme = "https"
	}

	restHost := os.Getenv("PRIVACY_AGENT_REST_HOST")
	if restHost == "" {
		restHost = os.Getenv("PRIVACY_REST_HOST")
	}
	if restHost == "" {
		restHost = s.cfg.AgentGRPCHost
	}
	if restHost == "" {
		restHost = "127.0.0.1"
	}

	restPort := os.Getenv("PRIVACY_REST_PORT")
	if restPort == "" {
		restPort = "8079"
	}

	return fmt.Sprintf("%s://%s:%s", scheme, restHost, restPort)
}

// extractRestErrorDetail 从上游错误响应体中提取可读的错误描述。
// 优先取 JSON 体中的 detail 字段（FastAPI 规范，与上游 agent 的错误格式保持一致）；
// 非 JSON 或无 detail 字段时降级为截断后的原始文本，避免把整段
// HTML/堆栈塞进响应 detail。
func extractRestErrorDetail(body []byte, statusCode int) string {
	var data any
	if err := json.Unmarshal(body, &data); err == nil {
		if m, ok := data.(map[string]any); ok {
			if d, exists := m["detail"]; exists {
				if s, ok := d.(string); ok {
					return s
				}
				// detail 非字符串（如校验错误数组）时序列化为 JSON 返回
				if raw, err := json.Marshal(d); err == nil {
					return string(raw)
				}
			}
		}
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return fmt.Sprintf("agent REST returned status %d", statusCode)
	}
	const maxDetailLen = 512
	if len(text) > maxDetailLen {
		text = text[:maxDetailLen] + "..."
	}
	return text
}

// normalizeRestPath 规范化 REST 路径别名与映射
func normalizeRestPath(path string) string {
	switch path {
	case "/v1/privacy/health":
		return "/health"
	case "/v1/privacy/mask_batch":
		return "/v1/privacy/mask/batch"
	case "/v1/privacy/mask_dataframe":
		return "/v1/privacy/mask/dataframe"
	default:
		return path
	}
}

// normalizeRestPayload 将 gRPC 风格的 payload 自动转换为 FastAPI 期望的模型结构
func normalizeRestPayload(path string, body json.RawMessage) (string, json.RawMessage) {
	normPath := normalizeRestPath(path)
	if len(body) == 0 {
		return normPath, body
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return normPath, body
	}

	// 1. 扁平化 rows / data (protobuf Repeated Struct {"fields": {...}} -> 普通 JSON 字典切片)
	for _, key := range []string{"rows", "data"} {
		if rawSlice, ok := m[key].([]any); ok {
			newSlice := make([]any, len(rawSlice))
			for i, elem := range rawSlice {
				if elemMap, ok := elem.(map[string]any); ok {
					if fields, ok := elemMap["fields"].(map[string]any); ok {
						newSlice[i] = fields
						continue
					}
				}
				newSlice[i] = elem
			}
			m[key] = newSlice
		}
	}

	// 2. 扁平化 chunks / vectors (protobuf repeated Chunk {"values": [...]} -> 普通二维切片)
	for _, key := range []string{"chunks", "vectors"} {
		if rawSlice, ok := m[key].([]any); ok {
			newSlice := make([]any, len(rawSlice))
			for i, elem := range rawSlice {
				if elemMap, ok := elem.(map[string]any); ok {
					if values, ok := elemMap["values"]; ok {
						newSlice[i] = values
						continue
					}
				}
				newSlice[i] = elem
			}
			m[key] = newSlice
		}
	}

	// 3. 将 specs_json 解析为 specs 字典
	if specsJSON, ok := m["specs_json"].(string); ok && specsJSON != "" {
		var specs map[string]any
		if err := json.Unmarshal([]byte(specsJSON), &specs); err == nil {
			m["specs"] = specs
		}
		delete(m, "specs_json")
	}

	// 4. 针对 DP 接口，将顶层调优参数打包进 params 字典
	if strings.HasPrefix(normPath, "/v1/privacy/dp/") {
		params, _ := m["params"].(map[string]any)
		if params == nil {
			params = make(map[string]any)
		}
		dpParamKeys := []string{
			"epsilon", "delta", "mechanism", "clip_lower", "clip_upper",
			"sensitivity", "min_count", "target_quantile", "num_iterations",
			"initial_clip", "max_norm",
		}
		for _, k := range dpParamKeys {
			if v, exists := m[k]; exists {
				params[k] = v
			}
		}
		if len(params) > 0 {
			m["params"] = params
		}
	}

	newBytes, err := json.Marshal(m)
	if err != nil {
		return normPath, body
	}
	return normPath, json.RawMessage(newBytes)
}

// callRest 执行底层的 HTTP REST 请求并返回解析后的数据、HTTP 状态码和可能的错误。
func (s *Server) callRest(ctx context.Context, method, path string, body json.RawMessage, rawPayloadB64, contentType string) (any, int, error) {
	method = strings.ToUpper(method)
	if method == "" {
		method = "POST"
	}
	normPath, normBody := normalizeRestPayload(path, body)
	targetURL := s.agentRestBaseURL() + normPath

	var reqBodyReader io.Reader
	if rawPayloadB64 != "" {
		rawBytes, err := base64.StdEncoding.DecodeString(rawPayloadB64)
		if err != nil {
			return nil, http.StatusBadRequest, fmt.Errorf("invalid base64 payload: %w", err)
		}
		reqBodyReader = bytes.NewReader(rawBytes)
		// 二进制请求（如 Arrow IPC）：若附带 JSON body，将其作为 URL query params 传递
		if len(normBody) > 0 {
			var paramsMap map[string]any
			if err := json.Unmarshal(normBody, &paramsMap); err == nil && len(paramsMap) > 0 {
				q := url.Values{}
				for k, v := range paramsMap {
					q.Set(k, fmt.Sprintf("%v", v))
				}
				if strings.Contains(targetURL, "?") {
					targetURL += "&" + q.Encode()
				} else {
					targetURL += "?" + q.Encode()
				}
			}
		}
	} else if len(normBody) > 0 {
		reqBodyReader = bytes.NewReader(normBody)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, targetURL, reqBodyReader)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	if contentType != "" {
		httpReq.Header.Set("Content-Type", contentType)
	} else {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if s.cfg.AgentAPIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+s.cfg.AgentAPIKey)
	}
	// Propagate distributed trace headers to the upstream Python engine.
	if rid := pkgagent.RequestIDFromContext(ctx); rid != "" {
		httpReq.Header.Set("X-Request-ID", rid)
		httpReq.Header.Set("X-Trace-ID", rid)
	}

	client := s.httpClient
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, http.StatusBadGateway, fmt.Errorf("Agent REST HTTP error: %w", err)
	}
	defer resp.Body.Close()

	// P32 fix: limit response body size to prevent OOM from malicious upstream
	const maxRespSize = 64 << 20 // 64 MB
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxRespSize))
	if err != nil {
		return nil, http.StatusBadGateway, fmt.Errorf("reading REST response: %w", err)
	}
	if int64(len(respBytes)) >= maxRespSize {
		return nil, http.StatusBadGateway, fmt.Errorf("REST response exceeded %d MB limit", maxRespSize>>20)
	}

	var respData any
	_ = json.Unmarshal(respBytes, &respData)

	if resp.StatusCode >= 400 {
		detail := extractRestErrorDetail(respBytes, resp.StatusCode)
		return respData, resp.StatusCode, errors.New(detail)
	}

	return respData, resp.StatusCode, nil
}

// proxyRest 辅助函数：通过 HTTP 将 REST 请求透明代理到 Agent REST 服务
func (s *Server) proxyRest(c *gin.Context, start time.Time, req models.ProxyRequest) {
	respData, statusCode, err := s.callRest(c.Request.Context(), req.Method, req.Path, req.Body, req.RawPayloadB64, req.ContentType)
	duration := time.Since(start).Milliseconds()

	if err != nil {
		// 将上游错误体作为 detail 字段保留在统一错误信封中，
		// 前端依赖 {detail} 展示上游原始错误信息。
		middleware.AbortWithError(c, statusCode, middleware.ErrorCodeFromStatus(statusCode), "upstream error", err.Error())
		return
	}

	c.JSON(http.StatusOK, models.ProxyResponse{
		Status:     http.StatusOK,
		DurationMs: duration,
		Data:       respData,
		Via:        "go-rest-proxy",
		Protocol:   "REST",
	})
}

// callRestOnce 以 REST 方式向 agent 发送单个请求（不写出 HTTP 响应），
// 供 ConcurrencyTest 压测 REST-only 路径或 gRPC 不支持路径的回退使用。
func (s *Server) callRestOnce(method, path string, body json.RawMessage) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _, err := s.callRest(ctx, method, path, body, "", "")
	return err
}

// Batch 逐个转发一组请求并汇总成功/失败统计。
//
// 用于前端“一键批量测试”：单个请求失败不会中断整个批次，
// 返回与历史 Python 后端保持一致的 {total, passed, failed, results} 结构。
//
// 执行逻辑：
//  1. 解析请求体为 BatchRequest（包含多个待转发请求）
//  2. 逐个转发请求（REST-only 路径走 REST，其它路径走 gRPC 并在 unsupported 时回退 REST）
//  3. 每个请求独立记录成功/失败与耗时
//  4. 汇总统计后返回 BatchResponse
func (s *Server) Batch(c *gin.Context) {
	// 解析请求体 JSON，绑定到 BatchRequest 结构体
	var req models.BatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// 请求体格式不合法时返回 400 错误
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("invalid request body: %v", err), nil)
		return
	}

	// P41 fix: 限制批量请求数量上限为 100，防止单次提交数千请求导致长时间占用连接（DoS 防护）
	const maxBatchSize = 100
	if len(req.Requests) > maxBatchSize {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("batch too large: %d requests (max %d)", len(req.Requests), maxBatchSize), nil)
		return
	}

	// 批量测试前重置预算，防止自动化测试/回归测试因预算耗尽而报错
	_, _, _ = s.callRest(c.Request.Context(), "POST", "/v1/privacy/budget/reset", nil, "", "")

	// 预分配结果切片，容量为请求数量以避免多次扩容
	results := make([]models.BatchResultItem, 0, len(req.Requests))
	// 成功计数器
	passed := 0
	// 逐个转发每个请求，单个失败不中断整个批次
	for _, item := range req.Requests {
		// 将 HTTP 方法转为大写（如 "post" → "POST"），用于结果展示
		method := strings.ToUpper(item.Method)
		if method == "" {
			method = "POST"
		}
		// 记录单个请求的开始时间
		start := time.Now()

		var (
			data       any
			statusCode int
			callErr    error
		)

		if isRestProtocol(c) || restOnlyPath(item.Path) {
			data, statusCode, callErr = s.callRest(c.Request.Context(), method, item.Path, item.Body, item.RawPayloadB64, item.ContentType)
		} else {
			// 通过 mapper 转发到上游 agent 的对应 gRPC 方法。
			// 使用 grpcCallTimeout 超时包裹：agent 重启期间请求等待连接恢复而非立即失败。
			// 将追踪 ID 注入 gRPC outgoing metadata，保持全链路追踪连续性
			ctx, cancel := context.WithTimeout(c.Request.Context(), s.grpcCallTimeout())
			data, callErr = s.mapper.Dispatch(s.client.WithTrace(s.client.WithAuth(ctx), middleware.GetTraceID(c)), s.client.Raw(), item.Path, item.Body)
			cancel()

			if callErr != nil && strings.Contains(callErr.Error(), "unsupported gRPC path") {
				// gRPC 不支持该路径时回退到 REST 转发
				data, statusCode, callErr = s.callRest(c.Request.Context(), method, item.Path, item.Body, item.RawPayloadB64, item.ContentType)
			} else if callErr != nil {
				statusCode = http.StatusBadRequest
				if isUnavailable(callErr) {
					statusCode = http.StatusBadGateway // 上游不可达返回 502
				}
			} else {
				statusCode = http.StatusOK
			}
		}

		// 计算单个请求耗时（毫秒）
		duration := time.Since(start).Milliseconds()

		if callErr != nil {
			// 记录失败结果，包含错误信息，继续处理下一个请求
			results = append(results, models.BatchResultItem{
				Method:     method,          // HTTP 方法
				Path:       item.Path,       // 请求路径
				Status:     statusCode,      // HTTP 状态码
				DurationMs: duration,        // 耗时（毫秒）
				Error:      callErr.Error(), // 错误信息
			})
			continue // 跳过后续成功逻辑，处理下一个请求
		}

		// 请求成功：累加成功计数并记录结果
		passed++
		results = append(results, models.BatchResultItem{
			Method:     method,        // HTTP 方法
			Path:       item.Path,     // 请求路径
			Status:     http.StatusOK, // 成功状态码 200
			DurationMs: duration,      // 耗时（毫秒）
			Data:       data,          // 响应数据
		})
	}

	// 返回批量测试汇总结果
	c.JSON(http.StatusOK, models.BatchResponse{
		Total:    len(results),          // 总请求数
		Passed:   passed,                // 成功数
		Failed:   len(results) - passed, // 失败数
		Results:  results,               // 逐条结果详情
		Via:      backendVia,            // "go-grpc"
		Protocol: agentProtocol,         // "gRPC"
	})
}

// Upload 接收前端上传的 CSV/JSON 文件并执行隐私处理。
//
// 支持的表单字段：
//   - file：数据文件（.csv 或 .json）
//   - operation：操作类型（mask_dataframe | k_anonymize）
//   - params：JSON 字符串，如 {"columns":[...],"qi_cols":[...],"k":2,"context":""}
//
// 执行逻辑：
//  1. 从 multipart 表单中读取上传文件
//  2. 按文件扩展名解析为 records + schema
//  3. 解析 params JSON 为操作参数
//  4. 根据 operation 调用对应的 gRPC 方法
//  5. 返回统一的 ProxyResponse（data 为 UploadData）
func (s *Server) Upload(c *gin.Context) {
	// 从 multipart 表单中读取名为 "file" 的上传文件
	// file：文件读取句柄；header：文件元信息（文件名、大小等）
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		// 缺少文件或读取失败时返回 400 错误
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("缺少文件: %v", err), nil)
		return
	}
	// 注册 defer：函数退出时自动关闭文件句柄，释放资源
	defer file.Close()

	// 上传大小限制：超限返回 413，避免大文件耗尽内存（DoS 防护）。
	if s.cfg.MaxUploadBytes > 0 && header.Size > s.cfg.MaxUploadBytes {
		middleware.AbortWithError(c, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", fmt.Sprintf("文件过大（%d 字节），上限 %d 字节", header.Size, s.cfg.MaxUploadBytes), nil)
		return
	}

	// 读取表单中的 operation 字段，决定执行哪种隐私处理操作
	operation := c.PostForm("operation")
	// 读取表单中的 params 字段，JSON 格式的操作参数
	params := c.PostForm("params")
	// params 为空时默认为空 JSON 对象，避免后续解析失败
	if params == "" {
		params = "{}"
	}

	// P34 fix: 读取文件内容，使用 io.LimitReader 防止 forged Content-Length 攻击
	// 即使 header.Size 检查通过，实际读取时仍加上限保护
	maxReadSize := int64(50 << 20) // 50 MB hard limit
	if s.cfg.MaxUploadBytes > 0 && s.cfg.MaxUploadBytes < maxReadSize {
		maxReadSize = s.cfg.MaxUploadBytes
	}
	content, err := io.ReadAll(io.LimitReader(file, maxReadSize+1))
	if err != nil {
		// 文件读取失败时返回 400 错误
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("读取文件失败: %v", err), nil)
		return
	}
	if int64(len(content)) > maxReadSize {
		// 文件实际大小超过限制
		middleware.AbortWithError(c, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", fmt.Sprintf("文件实际大小超过上限 %d 字节", maxReadSize), nil)
		return
	}

	// 按文件扩展名解析为 records（行数据）
	var records []map[string]string // 每行是一个 map[column_name]value
	// 将文件名转为小写，确保扩展名匹配不区分大小写
	filename := strings.ToLower(header.Filename)
	switch {
	case strings.HasSuffix(filename, ".csv"):
		// CSV 文件：解析表头为 schema，每行解析为 map
		records, _, err = fileparse.ParseCSV(content)
	case strings.HasSuffix(filename, ".json"):
		// JSON 文件：解析为对象数组，键名作为 schema
		records, _, err = fileparse.ParseJSON(content)
	default:
		// 不支持的文件格式时返回 400 错误
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "仅支持 .csv 与 .json 文件", nil)
		return
	}
	if err != nil {
		// 文件解析失败（如格式不合法）时返回 400 错误
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error(), nil)
		return
	}

	// 解析 params 字段为 map，用于提取操作参数（columns、qi_cols、k 等）
	var options map[string]any
	if err := json.Unmarshal([]byte(params), &options); err != nil {
		// params 不是合法 JSON 时返回 400 错误
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("params 需为合法 JSON: %v", err), nil)
		return
	}

	// 将 records 转换为 gRPC 的 RecordEntry 列表（protobuf 格式）
	entries := toRecordEntries(records)
	// 记录输入行数，用于响应中返回
	rowsIn := len(records)
	// 获取底层 gRPC 客户端，用于直接调用 RPC 方法
	client := s.client.Raw()
	// 使用请求的 context，支持客户端取消操作
	// 将追踪 ID 注入 gRPC outgoing metadata，保持全链路追踪连续性
	ctx := s.client.WithTrace(s.client.WithAuth(c.Request.Context()), middleware.GetTraceID(c))

	// 记录操作开始时间，用于计算总耗时
	start := time.Now()
	// result 保存最终操作结果（不同操作返回不同类型）
	var result any
	// rowsOut 保存输出行数
	var rowsOut int

	// 根据 operation 分发到对应的 gRPC 方法
	switch operation {
	case "mask_dataframe":
		// 脱敏操作：调用 MaskDataFrame gRPC 方法
		resp, e := client.MaskDataFrame(ctx, &pb.MaskDataFrameRequest{
			Data:    entries,                         // 输入数据
			Columns: stringSlice(options, "columns"), // 需脱敏的列名列表
			Context: stringVal(options, "context"),   // 脱敏上下文（影响脱敏策略）
		})
		if e != nil {
			// gRPC 调用失败时转换为 HTTP 错误响应
			s.writeUpstreamError(c, e)
			return
		}
		// 将 protobuf RecordEntry 列表转回 map 数组，便于 JSON 序列化
		result = recordEntriesToMaps(resp.Data)
		rowsOut = len(resp.Data) // 输出行数等于响应数据行数

	case "k_anonymize":
		// K-匿名操作：提取准标识符列名（必填参数）
		qiCols := stringSlice(options, "qi_cols")
		if len(qiCols) == 0 {
			// 缺少 qi_cols 参数时返回 400 错误
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "k_anonymize 操作需提供 qi_cols 参数", nil)
			return
		}
		// 调用 KAnonymizeDataFrame gRPC 方法
		resp, e := client.KAnonymizeDataFrame(ctx, &pb.KAnonymizeDataFrameRequest{
			Data:     entries,                            // 输入数据
			QiCols:   qiCols,                             // 准标识符列名列表
			K:        int32Val(options, "k", 5),          // K 值，默认 5
			MaxDepth: int32Val(options, "max_depth", 10), // 最大泛化深度，默认 10
		})
		if e != nil {
			// gRPC 调用失败时转换为 HTTP 错误响应
			s.writeUpstreamError(c, e)
			return
		}
		// 将 protobuf RecordEntry 列表转回 map 数组
		result = recordEntriesToMaps(resp.Data)
		rowsOut = len(resp.Data) // 输出行数等于响应数据行数

	default:
		// 不支持的操作类型时返回 400 错误，并列出可选操作
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("不支持的操作 '%s'，可选: k_anonymize, mask_dataframe", operation), nil)
		return
	}

	// 计算操作总耗时（毫秒）
	duration := time.Since(start).Milliseconds()
	// 返回统一的 ProxyResponse 格式，data 为 UploadData 结构
	c.JSON(http.StatusOK, models.ProxyResponse{
		Status:     http.StatusOK, // HTTP 状态码 200
		DurationMs: duration,      // 操作总耗时（毫秒）
		Data: models.UploadData{
			Operation: operation, // 操作类型
			RowsIn:    rowsIn,    // 输入行数
			RowsOut:   rowsOut,   // 输出行数
			Result:    result,    // 操作结果
		},
		Via:      backendVia,    // "go-grpc"
		Protocol: agentProtocol, // "gRPC"
	})
}

// LbTest 按策略向多个后端节点分发探测请求并统计结果。
//
// 由控制台后端自行实现策略分发（round_robin / random / least_connections），
// 探测目标为用户填写的各 agent REST 地址，返回各节点命中数与延迟分布。
//
// 执行逻辑：
//  1. 解析请求体为 LbTestRequest（包含节点列表、策略、探测次数等）
//  2. 调用 lbtest.Run 执行策略分发与探测
//  3. 返回各节点的命中数与延迟统计
func (s *Server) LbTest(c *gin.Context) {
	// 解析请求体 JSON，绑定到 LbTestRequest 结构体
	var req models.LbTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// 请求体格式不合法时返回 400 错误
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("invalid request body: %v", err), nil)
		return
	}
	// SSRF 防护：逐个校验探测目标 URL 的 scheme / host 白名单。
	if err := lbtest.ValidateBackends(req.Backends, splitHosts(s.cfg.LBAllowedHosts)); err != nil {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error(), nil)
		return
	}
	// 调用 lbtest 模块执行负载均衡测试，第三个参数为可选的自定义 HTTP 客户端（nil 使用默认）
	resp, err := lbtest.Run(c.Request.Context(), req, nil)
	if err != nil {
		// 测试执行失败时返回 400 错误，包含具体错误信息
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error(), nil)
		return
	}
	// 返回测试结果 JSON
	c.JSON(http.StatusOK, resp)
}

// grpcCallTimeout 返回单次 gRPC 调用的超时时间。
//
// 默认 60 秒；可用环境变量 PRIVACY_GRPC_CALL_TIMEOUT 覆盖（Go duration 格式，如 "30s"）。
// 作用：waitForReady 开启后，agent 重启期间 RPC 会等待连接恢复，该超时提供兜底，
// 避免连接长期不可用时请求无限挂起。
func (s *Server) grpcCallTimeout() time.Duration {
	if v := os.Getenv("PRIVACY_GRPC_CALL_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 60 * time.Second
}

// writeUpstreamError 将 gRPC 上游错误转换为 HTTP JSON 响应。
//
// 错误分类策略：
//   - 连接类错误（上游不可达/超时/DNS 失败）→ 502 Bad Gateway
//   - 其他错误（参数错误/业务错误）→ 400 Bad Request
//
// 该方法是 Proxy/Upload 等多个 handler 的公共错误处理入口。
func (s *Server) writeUpstreamError(c *gin.Context, err error) {
	// 默认返回 400（客户端错误）
	status := http.StatusBadRequest
	// 如果是上游连接类错误，则返回 502（网关错误）
	if isUnavailable(err) {
		status = http.StatusBadGateway
	}
	// 返回 JSON 格式的错误响应，包含错误详情与状态码
	middleware.AbortWithError(c, status, middleware.ErrorCodeFromStatus(status), err.Error(), nil)
}

// toRecordEntries 将 Go map 数组转换为 gRPC RecordEntry 列表。
//
// 前端上传的文件解析结果为 []map[string]string，
// 而 gRPC 接口要求 []*pb.RecordEntry 格式，
// 本函数负责完成两种表示之间的转换。
func toRecordEntries(records []map[string]string) []*pb.RecordEntry {
	// 预分配切片，容量等于记录数以避免多次扩容
	entries := make([]*pb.RecordEntry, 0, len(records))
	// 遍历每条记录，将 map 转换为 RecordEntry 的 Fields 字段
	for _, r := range records {
		// 创建新 map 副本，避免修改原始数据
		fields := make(map[string]string, len(r))
		for k, v := range r {
			fields[k] = v
		}
		// 将 map 包装为 RecordEntry 并追加到结果列表
		entries = append(entries, &pb.RecordEntry{Fields: fields})
	}
	return entries
}

// recordEntriesToMaps 将 gRPC RecordEntry 列表转换回 Go map 数组。
//
// 与 toRecordEntries 相反，用于将 gRPC 响应转换为 JSON 可序列化格式，
// 便于前端直接展示。
func recordEntriesToMaps(entries []*pb.RecordEntry) []map[string]string {
	// 预分配切片，容量等于条目数
	out := make([]map[string]string, 0, len(entries))
	// 直接取出每个 RecordEntry 的 Fields map 追加到结果列表
	for _, e := range entries {
		out = append(out, e.Fields)
	}
	return out
}

// stringSlice 从 JSON 解析后的 map 中提取字符串数组字段。
//
// JSON 反序列化后数组类型为 []any，元素类型为 any，
// 本函数负责安全地类型断言并转换为 []string。
// 字段不存在或类型不匹配时返回 nil。
func stringSlice(m map[string]any, key string) []string {
	// 查找指定 key 是否存在
	if v, ok := m[key]; ok {
		// 尝试将值断言为 []any（JSON 数组反序列化后的默认类型）
		if arr, ok := v.([]any); ok {
			// 预分配切片，容量为数组长度
			out := make([]string, 0, len(arr))
			// 遍历数组元素，仅保留字符串类型的元素
			for _, item := range arr {
				if s, ok := item.(string); ok {
					out = append(out, s)
				}
			}
			return out
		}
	}
	// 字段不存在或类型不匹配时返回 nil
	return nil
}

// stringVal 从 JSON 解析后的 map 中提取字符串字段。
// 字段不存在或类型不是 string 时返回空字符串。
func stringVal(m map[string]any, key string) string {
	// 查找指定 key 是否存在
	if v, ok := m[key]; ok {
		// 尝试将值断言为 string 类型
		if s, ok := v.(string); ok {
			return s
		}
	}
	// 字段不存在或类型不匹配时返回空字符串
	return ""
}

// int32Val 从 JSON 解析后的 map 中提取整数字段。
//
// JSON 数字在 Go 中反序列化为 float64，
// 本函数支持 float64、int、int64 三种类型的安全转换。
// 字段不存在或类型不匹配时返回默认值 def。
func int32Val(m map[string]any, key string, def int32) int32 {
	// 查找指定 key 是否存在
	if v, ok := m[key]; ok {
		// 使用类型 switch 处理 JSON 数字可能的 Go 类型
		switch n := v.(type) {
		case float64:
			// JSON 数字默认反序列化为 float64，直接截断为 int32
			return int32(n)
		case int:
			// 部分场景下可能为 int 类型
			return int32(n)
		case int64:
			// 部分场景下可能为 int64 类型
			return int32(n)
		}
	}
	// 字段不存在或类型不匹配时返回默认值
	return def
}

// isUnavailable 判断错误是否表示上游 agent 不可达。
//
// 健壮性判断策略：
//  1. 优先通过 gRPC status.FromError 检查标准状态码（Unavailable / DeadlineExceeded / ResourceExhausted）；
//  2. 兜底检查错误文本中的底层网络连接异常关键字。
//
// 返回 true 表示应返回 502 Bad Gateway，false 表示应返回 400 Bad Request。
func isUnavailable(err error) bool {
	// nil 错误表示无异常，不属于不可达
	if err == nil {
		return false
	}
	// 1. gRPC 标准状态码类型检查
	if st, ok := status.FromError(err); ok {
		code := st.Code()
		if code == codes.Unavailable || code == codes.DeadlineExceeded || code == codes.ResourceExhausted {
			return true
		}
	}
	// 2. 文本关键字兜底匹配
	msg := err.Error()
	return containsAny(msg, []string{
		"connection refused", "dns", "timeout", "Unavailable",
		"connection reset", "broken pipe", "context deadline exceeded",
	})
}

// containsAny 检查字符串 s 是否包含 subs 列表中的任意一个子串。
// 用于 isUnavailable 中匹配连接类错误关键词。
func containsAny(s string, subs []string) bool {
	// 遍历子串列表，任一匹配即返回 true
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	// 全部不匹配时返回 false
	return false
}

// securityMiddleware 返回可选的 API Key 鉴权 + 限流中间件（默认关闭 / 宽松）。
//
//   - apiKey 非空时，/api/*（除 /api/health）需携带 Authorization: Bearer <key>；
//   - rateLimit > 0 时，每分钟每客户端 IP 超过该阈值返回 429（进程内滑动窗口）。
//
// CORS 预检（OPTIONS）已由 corsMiddleware 提前返回 204，不会进入本中间件；
// 静态资源等非 /api 路径与 /api/health 均子以豁免。
func securityMiddleware(apiKey string, rateLimit int) (gin.HandlerFunc, func()) {
	// 限流状态：每个客户端 IP 的请求时间戳列表（60 秒滑动窗口）。
	var mu sync.Mutex
	hits := make(map[string][]time.Time)

	// P57 fix: done channel signals the cleanup goroutine to exit on shutdown,
	// preventing goroutine leak when the server is stopped.
	done := make(chan struct{})

	// 后台 goroutine 定期清理过期 IP 条目，防止长期运行时 map 无限增长（内存泄漏）。
	// 每 5 分钟扫描一次，删除 60 秒内无请求的 IP 记录。
	if rateLimit > 0 {
		go func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					mu.Lock()
					cutoff := time.Now().Add(-60 * time.Second)
					for ip, window := range hits {
						// 过滤掉 60 秒内的记录；若过滤后为空则删除该 IP 条目
						kept := window[:0]
						for _, t := range window {
							if t.After(cutoff) {
								kept = append(kept, t)
							}
						}
						if len(kept) == 0 {
							delete(hits, ip)
						} else {
							hits[ip] = kept
						}
					}
					mu.Unlock()
				}
			}
		}()
	}

	cleanup := func() {
		select {
		case <-done:
			// already closed
		default:
			close(done)
		}
	}

	handler := func(c *gin.Context) {
		path := c.Request.URL.Path
		// 仅对 /api/* 生效；健康检查豁免。
		if !strings.HasPrefix(path, "/api/") || path == "/api/health" {
			c.Next()
			return
		}
		// API Key 鉴权（配置了才校验）。
		if apiKey != "" {
			token := extractBearer(c.GetHeader("Authorization"))
			if subtle.ConstantTimeCompare([]byte(token), []byte(apiKey)) != 1 {
				middleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized: invalid console api key", nil)
				return
			}
		}
		// 限流（rateLimit <= 0 时关闭）。
		if rateLimit > 0 {
			ip := c.ClientIP()
			now := time.Now()
			cutoff := now.Add(-60 * time.Second)
			mu.Lock()
			window := hits[ip]
			// 就地过滤掉 60 秒窗口外的旧记录。
			kept := window[:0]
			for _, t := range window {
				if t.After(cutoff) {
					kept = append(kept, t)
				}
			}
			if len(kept) >= rateLimit {
				hits[ip] = kept
				mu.Unlock()
				middleware.AbortWithError(c, http.StatusTooManyRequests, "RATE_LIMITED", "Too many requests", nil)
				return
			}
			hits[ip] = append(kept, now)
			mu.Unlock()
		}
		c.Next()
	}
	return handler, cleanup
}

// extractBearer 从 Authorization 头提取 Bearer token，格式不符时返回空字符串。
func extractBearer(header string) string {
	parts := strings.Fields(header)
	if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
		return parts[1]
	}
	return ""
}

// ConcurrencyTest 并发压测：以指定并发度向 agent 发送请求并统计延迟分布与吞吐量。
func (s *Server) ConcurrencyTest(c *gin.Context) {
	var req models.ConcurrencyTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("invalid request body: %v", err), nil)
		return
	}
	if req.Path == "" {
		req.Path = "/v1/privacy/mask"
	}
	// P37 fix: validate path against allowlist to prevent SSRF via pressure test endpoint
	// 校验压测路径白名单，防止通过压测端点访问敏感内部接口
	if !isAllowedConcurrencyPath(req.Path) {
		middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("path %q not allowed for concurrency test; allowed prefixes: /v1/privacy/, /v1/dynclassification/, /v1/medical/, /v1/pipeline/, /health", req.Path), nil)
		return
	}
	if req.Method == "" {
		req.Method = "POST"
	}
	if req.Concurrency <= 0 {
		req.Concurrency = 50
	}
	if req.Concurrency > 500 {
		req.Concurrency = 500
	}
	if req.TotalRequests <= 0 {
		req.TotalRequests = 200
	}
	if req.TotalRequests > 5000 {
		req.TotalRequests = 5000
	}

	total := req.TotalRequests
	concurrency := req.Concurrency
	if concurrency > total {
		concurrency = total
	}

	latencies := make([]float64, 0, total)
	var latenciesMu sync.Mutex
	var successCount, failedCount int

	jobs := make(chan struct{}, total)
	for i := 0; i < total; i++ {
		jobs <- struct{}{}
	}
	close(jobs)

	// REST-only 路径（/health、/v1/dynclassification/* 等）无 gRPC 对应方法，
	// 与 Proxy 保持一致的前缀拦截：整段压测直接走 REST，避免全部请求失败。
	useREST := restOnlyPath(req.Path)

	startTime := time.Now()
	var wg sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				start := time.Now()
				var err error
				if useREST {
					err = s.callRestOnce(req.Method, req.Path, req.Body)
				} else {
					// 将追踪 ID 注入 gRPC metadata，确保压测请求也参与全链路追踪
					ctx, cancel := context.WithTimeout(s.client.WithTrace(s.client.WithAuth(context.Background()), middleware.GetTraceID(c)), 30*time.Second)
					_, err = s.mapper.Dispatch(ctx, s.client.Raw(), req.Path, req.Body)
					cancel()
					// gRPC 不支持该路径时回退 REST（与 Proxy 的错误回退策略一致）
					if err != nil && strings.Contains(err.Error(), "unsupported gRPC path") {
						err = s.callRestOnce(req.Method, req.Path, req.Body)
					}
				}
				elapsedMs := float64(time.Since(start).Microseconds()) / 1000.0

				latenciesMu.Lock()
				latencies = append(latencies, elapsedMs)
				if err == nil {
					successCount++
				} else {
					failedCount++
				}
				latenciesMu.Unlock()
			}
		}()
	}

	wg.Wait()
	durationMs := float64(time.Since(startTime).Microseconds()) / 1000.0

	sort.Float64s(latencies)
	n := len(latencies)

	if n == 0 {
		c.JSON(http.StatusOK, models.ConcurrencyTestResponse{
			Total:        total,
			Success:      0,
			Failed:       total,
			DurationMs:   math.Round(durationMs*100) / 100,
			Qps:          0,
			AvgLatencyMs: 0,
			MinLatencyMs: 0,
			MaxLatencyMs: 0,
			P50LatencyMs: 0,
			P90LatencyMs: 0,
			P95LatencyMs: 0,
			P99LatencyMs: 0,
		})
		return
	}

	var sum float64
	for _, l := range latencies {
		sum += l
	}

	percentile := func(p float64) float64 {
		if n == 1 {
			return latencies[0]
		}
		k := float64(n-1) * (p / 100.0)
		f := int(k)
		cIdx := f + 1
		if cIdx >= n {
			cIdx = n - 1
		}
		if f == cIdx {
			return latencies[f]
		}
		return latencies[f]*(float64(cIdx)-k) + latencies[cIdx]*(k-float64(f))
	}

	qps := 0.0
	if durationMs > 0 {
		qps = float64(total) / (durationMs / 1000.0)
	}

	resp := models.ConcurrencyTestResponse{
		Total:        total,
		Success:      successCount,
		Failed:       failedCount,
		DurationMs:   math.Round(durationMs*100) / 100,
		Qps:          math.Round(qps*100) / 100,
		AvgLatencyMs: math.Round((sum/float64(n))*100) / 100,
		MinLatencyMs: math.Round(latencies[0]*100) / 100,
		MaxLatencyMs: math.Round(latencies[n-1]*100) / 100,
		P50LatencyMs: math.Round(percentile(50)*100) / 100,
		P90LatencyMs: math.Round(percentile(90)*100) / 100,
		P95LatencyMs: math.Round(percentile(95)*100) / 100,
		P99LatencyMs: math.Round(percentile(99)*100) / 100,
	}

	c.JSON(http.StatusOK, resp)
}

// loadSampleRecords 读取 internal/samples 或 data 下的内置 CSV 样本。
// 优先解析相对 StaticDistDir 的部署布局路径，其次多级回退到 CWD 相对路径与 data 根目录；
// 文件缺失或解析为空时返回明确错误，调用方应映射为 404 而非转发空数据。
func (s *Server) loadSampleRecords(name string) ([]map[string]string, error) {
	candidates := []string{
		filepath.Join(s.cfg.StaticDistDir, "..", "internal", "samples", name),
		filepath.Join("internal", "samples", name),
		filepath.Join("console", "bff-go", "internal", "samples", name),
		filepath.Join("data", name),
		filepath.Join("..", "..", "data", name),
		filepath.Join("..", "data", name),
	}
	var samplePath string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			samplePath = c
			break
		}
	}
	if samplePath == "" {
		return nil, fmt.Errorf("示例数据文件缺失: %s", name)
	}
	data, err := os.ReadFile(samplePath)
	if err != nil {
		return nil, fmt.Errorf("示例数据文件缺失: %s", name)
	}
	parsed, _, err := fileparse.ParseCSV(data)
	if err != nil || len(parsed) == 0 {
		return nil, fmt.Errorf("示例数据文件为空或解析失败: %s", name)
	}
	return parsed, nil
}

// MedicalPipeline 医疗敏感数据全流程治理代理端点：分类分级与 L4/L5 数据脱敏。
func (s *Server) MedicalPipeline(c *gin.Context) {
	var body struct {
		Records []map[string]string `json:"records"`
	}
	_ = c.ShouldBindJSON(&body)

	records := body.Records
	if len(records) == 0 {
		loaded, err := s.loadSampleRecords("kangyang.csv")
		if err != nil {
			// 明确报错而非代理空记录集，避免前端把"样本缺失"误显示为"0 条记录"
			middleware.AbortWithError(c, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
			return
		}
		records = loaded
	}

	start := time.Now()
	proxyReq := models.ProxyRequest{
		Method: "POST",
		Path:   "/v1/agent/process",
	}
	reqBytes, _ := json.Marshal(map[string]any{"records": records})
	proxyReq.Body = reqBytes

	s.proxyRest(c, start, proxyReq)
}

// YibaoPipeline 医保结算数据全流程治理代理端点：读入 yibao.csv 18 字段进行治理。
func (s *Server) YibaoPipeline(c *gin.Context) {
	var body struct {
		Records []map[string]string `json:"records"`
		Dataset string              `json:"dataset"`
	}
	_ = c.ShouldBindJSON(&body)

	records := body.Records
	if len(records) == 0 {
		loaded, err := s.loadSampleRecords("yibao.csv")
		if err != nil {
			// 明确报错而非代理空记录集，避免前端把"样本缺失"误显示为"0 条记录"
			middleware.AbortWithError(c, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
			return
		}
		records = loaded
	}

	start := time.Now()
	proxyReq := models.ProxyRequest{
		Method: "POST",
		Path:   "/v1/agent/process",
	}
	reqBytes, _ := json.Marshal(map[string]any{"records": records})
	proxyReq.Body = reqBytes

	s.proxyRest(c, start, proxyReq)
}

// PipelineProcess 通用分类分级与脱敏流水线代理端点。
func (s *Server) PipelineProcess(c *gin.Context) {
	var body struct {
		Records  []map[string]string `json:"records"`
		Standard string              `json:"standard"`
		MaskL4   *bool               `json:"mask_l4"`
		MaskL5   *bool               `json:"mask_l5"`
	}
	_ = c.ShouldBindJSON(&body)

	records := body.Records
	if len(records) == 0 {
		loaded, err := s.loadSampleRecords("kangyang.csv")
		if err != nil {
			// 明确报错而非代理空记录集，避免前端把"样本缺失"误显示为"0 条记录"
			middleware.AbortWithError(c, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
			return
		}
		records = loaded
	}

	standard := body.Standard
	if standard == "" {
		standard = "jrt0197"
	}
	maskL4 := true
	if body.MaskL4 != nil {
		maskL4 = *body.MaskL4
	}
	maskL5 := true
	if body.MaskL5 != nil {
		maskL5 = *body.MaskL5
	}

	start := time.Now()
	proxyReq := models.ProxyRequest{
		Method: "POST",
		Path:   "/v1/pipeline/process_records",
	}
	reqBytes, _ := json.Marshal(map[string]any{
		"records":  records,
		"standard": standard,
		"mask_l4":  maskL4,
		"mask_l5":  maskL5,
	})
	proxyReq.Body = reqBytes

	s.proxyRest(c, start, proxyReq)
}

// splitHosts 把逗号分隔的 host 白名单字符串拆分为去除空白后的切片；
// 空字符串返回 nil（表示不限制）。
func splitHosts(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ── P0-7 / 门禁 G-01：中台透明代理「方法 + 路径」白名单 ──────────────────────
//
// /api/hub、/api/datasource、/api/audit 三个透明代理历史上把**任意方法 + 任意
// 路径**原样转发给中台微服务，浏览器客户端可借此直达 datasource-mgr 的未脱敏
// 记录端点（/api/datasources/:id/records、/sample）与原始领域 API
// （/api/v1/yibao、/api/v1/kangyang、/api/v1/mock3、/api/v1/mock4），
// 既不经 engine 脱敏漏斗、也不产生任何存证 —— 与「原始数据不出域」直接冲突。
//
// 下列白名单为**默认拒绝**：仅放行控制台确实需要的只读元数据 / 探查 / 统计与
// 任务调度端点，其余一律 403 FORBIDDEN_PATH。白名单不提供关闭开关。

// proxyRule 白名单条目。
//   - method：HTTP 方法，大小写不敏感；
//   - pattern：上游路径模式，其中 "*" 作为独立路径段时匹配**恰好一个**非空段
//     （如 /api/datasources/*/metadata）。因此不存在前缀放大效应：
//     /api/datasources/* 永远不会匹配 /api/datasources/ds_yibao/records。
type proxyRule struct {
	method  string
	pattern string
}

// proxyHealthRules 三个中台服务共用的存活/就绪探针白名单。
var proxyHealthRules = []proxyRule{
	{method: http.MethodGet, pattern: "/health"},
	{method: http.MethodGet, pattern: "/readyz"},
	{method: http.MethodGet, pattern: "/api/health"},
}

// microserviceProxyAllowlist 按代理目标（hub / datasource / audit）列出放行规则。
// 未列出的路径、方法或目标服务一律拒绝。
var microserviceProxyAllowlist = map[string][]proxyRule{
	// service-hub：流水线调度与任务遥测
	"hub": append(append([]proxyRule{}, proxyHealthRules...),
		proxyRule{method: http.MethodGet, pattern: "/api/hub/status"},
		proxyRule{method: http.MethodGet, pattern: "/api/hub/tasks"},
		proxyRule{method: http.MethodGet, pattern: "/api/hub/tasks/*"},
		proxyRule{method: http.MethodGet, pattern: "/api/hub/pipeline"},
		proxyRule{method: http.MethodPost, pattern: "/api/hub/dispatch"},
		proxyRule{method: http.MethodPost, pattern: "/api/hub/classify"},
	),
	// datasource-mgr：仅数据源目录与 Schema 元数据；
	// /records、/sample、/api/v1/* 原始领域 API 与 seed 写接口**禁止**经 BFF 出域。
	"datasource": append(append([]proxyRule{}, proxyHealthRules...),
		proxyRule{method: http.MethodGet, pattern: "/api/datasources"},
		proxyRule{method: http.MethodGet, pattern: "/api/datasources/*"},
		proxyRule{method: http.MethodGet, pattern: "/api/datasources/*/metadata"},
		proxyRule{method: http.MethodGet, pattern: "/api/datasources/*/audit"},
		proxyRule{method: http.MethodPost, pattern: "/api/datasources/*/test"},
	),
	// audit-log：存证查询、统计与哈希链验真（治理巡检必需）
	"audit": append(append([]proxyRule{}, proxyHealthRules...),
		proxyRule{method: http.MethodGet, pattern: "/api/audit/logs"},
		proxyRule{method: http.MethodGet, pattern: "/api/audit/logs/*"},
		proxyRule{method: http.MethodPost, pattern: "/api/audit/logs"},
		proxyRule{method: http.MethodGet, pattern: "/api/audit/stats"},
		proxyRule{method: http.MethodGet, pattern: "/api/audit/snapshots"},
		proxyRule{method: http.MethodPost, pattern: "/api/audit/snapshots/verify"},
		proxyRule{method: http.MethodGet, pattern: "/api/audit/chain/verify"},
		proxyRule{method: http.MethodPost, pattern: "/api/audit/chain/verify"},
		proxyRule{method: http.MethodPost, pattern: "/api/audit/report"},
	),
}

// proxyDenyPathPrefixes 黑名单前缀：/api/v1/* 是 datasource-mgr 的原始领域数据 API，
// 即使后续白名单被放宽也必须拒绝。
var proxyDenyPathPrefixes = []string{"/api/v1/", "/debug/", "/internal/", "/metrics"}

// proxyDenyPathSegments 黑名单尾段：原始记录 / 样本导出端点的统一形态，
// 作为白名单之外的第二道硬拦截（P0-7 验收口径显式点名）。
var proxyDenyPathSegments = []string{"records", "sample", "raw"}

// proxyEncodedTraversalMarkers 百分号编码混淆特征（%2e=.、%2f=/、%5c=\、%00=NUL、
// %25=%，用于识别双重编码绕过）。
var proxyEncodedTraversalMarkers = []string{"%2e", "%2f", "%5c", "%00", "%25"}

// isAllowedMicroserviceProxyPath 校验「代理目标 + HTTP 方法 + 上游路径」是否命中白名单。
// upstreamPath 必须是已经 path.Clean 规范化、且已剥离 BFF 路由前缀的上游路径。
// 采用默认拒绝：目标未知、路径为空、命中黑名单或不在白名单内均返回 false。
func isAllowedMicroserviceProxyPath(service, method, upstreamPath string) bool {
	if !strings.HasPrefix(upstreamPath, "/") {
		return false
	}
	cleaned := path.Clean(upstreamPath)
	if cleaned == "/" {
		return false
	}
	for _, denied := range proxyDenyPathPrefixes {
		if strings.HasPrefix(cleaned, denied) {
			return false
		}
	}
	if last := cleaned[strings.LastIndex(cleaned, "/")+1:]; last != "" {
		for _, denied := range proxyDenyPathSegments {
			if strings.EqualFold(last, denied) {
				return false
			}
		}
	}
	rules, ok := microserviceProxyAllowlist[service]
	if !ok {
		return false
	}
	for _, rule := range rules {
		if !strings.EqualFold(rule.method, method) {
			continue
		}
		if matchProxyPathPattern(rule.pattern, cleaned) {
			return true
		}
	}
	return false
}

// matchProxyPathPattern 按路径段逐个比对，"*" 匹配恰好一个非空段。
func matchProxyPathPattern(pattern, cleanedPath string) bool {
	if pattern == cleanedPath {
		return true
	}
	ruleSegs := strings.Split(pattern, "/")
	pathSegs := strings.Split(cleanedPath, "/")
	if len(ruleSegs) != len(pathSegs) {
		return false
	}
	for i := range ruleSegs {
		if ruleSegs[i] == "*" {
			// "*" 不接受空段，避免 // 或尾斜杠被当作通配命中
			if pathSegs[i] == "" {
				return false
			}
			continue
		}
		if ruleSegs[i] != pathSegs[i] {
			return false
		}
	}
	return true
}

// rewriteProxyRequestPath 由入站 URL 还原上游路径，并识别编码穿越。
// 返回 ok=false 表示路径不可解析、含 %2e%2e 之类编码穿越，或试图用 ".."
// 逃出自身前缀（如 /api/datasource/../audit/logs）——调用方必须拒绝。
func rewriteProxyRequestPath(u *url.URL, prefix string) (string, bool) {
	if u == nil || !strings.HasPrefix(u.Path, "/") {
		return "", false
	}
	if hasEncodedTraversal(u) {
		return "", false
	}
	// 先 Clean 再校验前缀：保证 ".." 无法把请求抬到 /api/{service} 之外
	cleaned := path.Clean(u.Path)
	if cleaned != prefix && !strings.HasPrefix(cleaned, prefix+"/") {
		// 覆盖两类越权：/api/datasource/../audit/...（抬出自身前缀）
		// 与 /api/datasourceX/...（前缀命中但不是段边界）
		return "", false
	}
	upstream := strings.TrimPrefix(cleaned, prefix)
	if upstream == "" {
		// /api/{service} 自身不是合法上游路径（Clean 已消除尾斜杠）
		return "", false
	}
	return path.Clean(upstream), true
}

// hasEncodedTraversal 检测原始编码形态中的穿越与分隔符混淆（含双重编码）。
func hasEncodedTraversal(u *url.URL) bool {
	// EscapedPath() 返回未解码的原始形态；无 RawPath 时与 Path 等价
	escaped := strings.ToLower(u.EscapedPath())
	if escaped == "" {
		escaped = strings.ToLower(u.Path)
	}
	for _, marker := range proxyEncodedTraversalMarkers {
		if strings.Contains(escaped, marker) {
			return true
		}
	}
	return false
}

// logMicroserviceProxyCall 为每一次代理调用（放行与拒绝皆包括）输出结构化审计日志。
// P0-7：封堵旁路后，任何经 BFF 出域的中台调用都必须留痕可查。
func (s *Server) logMicroserviceProxyCall(c *gin.Context, service, method, upstreamPath string, denied bool) {
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	level := slog.LevelInfo
	if denied {
		level = slog.LevelWarn
	}
	logger.Log(c.Request.Context(), level, "microservice_proxy_call",
		"proxy_target", service,
		"method", method,
		"upstream_path", upstreamPath,
		"denied", denied,
		"caller", proxyCallerIdentity(c),
		"request_id", middleware.GetTraceID(c),
	)
}

// proxyCallerIdentity 复用 BFF 已有的身份线索（控制台 API Key 主体 + 客户端 IP），
// 不引入新的鉴权机制；未携带 Bearer 凭据时 subject 记为 anonymous。
func proxyCallerIdentity(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return "subject=unknown;ip=unknown"
	}
	subject := "anonymous"
	if extractBearer(c.GetHeader("Authorization")) != "" {
		subject = "console-api-key"
	}
	ip := c.ClientIP()
	if ip == "" {
		ip = "unknown"
	}
	return "subject=" + subject + ";ip=" + ip
}

// displayProxyPath 供拒绝响应展示被拦截的路径：上游路径为空（入站路径本身不可
// 解析 / 含编码穿越）时回退展示入站请求路径，便于运维定位具体请求。
func displayProxyPath(c *gin.Context, upstream string) string {
	if upstream != "" {
		return upstream
	}
	if c != nil && c.Request != nil && c.Request.URL != nil {
		return c.Request.URL.Path
	}
	return ""
}

// ProxyHub transparently forwards requests to service-hub.
// The /api/hub prefix is stripped so the upstream sees its own route (e.g. /api/hub/tasks → /tasks).
// 转发前经过 isAllowedMicroserviceProxyPath 方法 + 路径白名单校验（P0-7）。
func (s *Server) ProxyHub(c *gin.Context) {
	s.proxyMicroservice(c, "hub")
}

// ProxyDatasource transparently forwards requests to datasource-mgr.
func (s *Server) ProxyDatasource(c *gin.Context) {
	s.proxyMicroservice(c, "datasource")
}

// ProxyAudit transparently forwards requests to audit-log.
func (s *Server) ProxyAudit(c *gin.Context) {
	s.proxyMicroservice(c, "audit")
}

// proxyMicroservice performs a method+path allowlisted HTTP proxy to a named Go microservice.
// It strips the BFF route prefix, validates the rewritten upstream path against the
// deny-by-default allowlist (P0-7 / G-01), logs every call (allowed or denied), then
// forwards method/query/body using the shared microservices client.
func (s *Server) proxyMicroservice(c *gin.Context, service string) {
	// Strip the leading /api/{service} prefix to reconstruct the upstream path.
	// 规范化 + 前缀剥离 + 编码穿越检测一次完成。
	prefix := "/api/" + service
	upstream, ok := rewriteProxyRequestPath(c.Request.URL, prefix)

	// P0-7 门禁 G-01：默认拒绝，仅放行白名单内的只读元数据/探查/统计与调度端点。
	if ok {
		ok = isAllowedMicroserviceProxyPath(service, c.Request.Method, upstream)
	}
	if !ok {
		s.logMicroserviceProxyCall(c, service, c.Request.Method, upstream, true)
		middleware.AbortWithError(c, http.StatusForbidden, "FORBIDDEN_PATH",
			fmt.Sprintf("%s %s is not allowlisted for the %s proxy", c.Request.Method, displayProxyPath(c, upstream), service),
			nil)
		return
	}
	s.logMicroserviceProxyCall(c, service, c.Request.Method, upstream, false)

	var body []byte
	if c.Request.Body != nil {
		var err error
		body, err = io.ReadAll(io.LimitReader(c.Request.Body, 64<<20+1))
		if err != nil {
			middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_ARGUMENT", "failed to read request body", err.Error())
			return
		}
		if int64(len(body)) > 64<<20 {
			middleware.AbortWithError(c, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body exceeds 64 MiB", nil)
			return
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	resp, respBody, err := s.msClient.Proxy(
		ctx,
		service,
		c.Request.Method,
		upstream,
		c.Request.URL.Query(),
		body,
		c.Request.Header.Get("Content-Type"),
		middleware.GetTraceID(c),
	)
	if err != nil {
		s.logger.Warn("microservice proxy failed",
			"service", service,
			"path", upstream,
			"error", err.Error(),
		)
		middleware.AbortWithError(c, http.StatusBadGateway, "UPSTREAM_ERROR", fmt.Sprintf("%s unreachable", service), err.Error())
		return
	}

	// Preserve upstream content type if present.
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		c.Header("Content-Type", ct)
	}
	c.Status(resp.StatusCode)
	if len(respBody) > 0 {
		_, _ = c.Writer.Write(respBody)
	}
}
