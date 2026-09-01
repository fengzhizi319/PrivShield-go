// Package integration_test 提供 Go gRPC 代理后端的端到端集成测试。
// Package integration_test provides end-to-end integration tests for the Go gRPC proxy backend.
//
// 测试策略 / Testing Strategy:
//   - 需要真实运行的 PrivShield（gRPC 127.0.0.1:50051）；
//     Requires a running PrivShield (gRPC 127.0.0.1:50051);
//   - 若 agent 未启动则自动跳过（t.Skip），不会导致 CI 失败；
//     If agent is not running, tests are automatically skipped (t.Skip), won't fail CI;
//   - 使用 httptest.NewServer 启动完整的 Gin 路由，通过真实 HTTP 请求验证端到端链路。
//     Uses httptest.NewServer to start full Gin routes, verifying end-to-end chain via real HTTP requests.
//
// 运行方式 / How to Run:
//
//	make test-integration
//	# 或 / or: go test -run Integration ./tests/...
package integration_test

import (
	"bytes"             // 构造 HTTP 请求体 / construct HTTP request body
	"context"           // gRPC 调用上下文与超时控制 / gRPC call context and timeout control
	"encoding/json"     // JSON 序列化/反序列化 / JSON marshal/unmarshal
	"log/slog"          // 结构化日志（测试用）/ structured logger (test only)
	"net/http"          // HTTP 客户端与状态码常量 / HTTP client and status code constants
	"net/http/httptest" // 内存 HTTP 测试服务器 / in-memory HTTP test server
	"os"                // stderr 日志输出 / stderr log output
	"testing"           // Go 测试框架 / Go testing framework
	"time"              // 超时与重试间隔 / timeout and retry interval

	"github.com/gin-gonic/gin"                    // Gin Web 框架（测试模式）/ Gin web framework (test mode)
	"google.golang.org/grpc"                      // gRPC 客户端连接 / gRPC client connection
	"google.golang.org/grpc/credentials/insecure" // 无 TLS 传输凭证（仅测试）/ no-TLS transport creds (test only)

	"github.com/fengzhizi319/PrivShield-go/pkg/metrics" // Prometheus 指标收集器 / Prometheus metrics collector

	"github.com/fengzhizi319/PrivShield-go/console/bff-go/internal/agent"    // gRPC 客户端封装 / gRPC client wrapper
	"github.com/fengzhizi319/PrivShield-go/console/bff-go/internal/config"   // 配置结构体 / config struct
	"github.com/fengzhizi319/PrivShield-go/console/bff-go/internal/handlers" // HTTP 路由处理器 / HTTP route handlers
	pb "github.com/fengzhizi319/PrivShield-go/console/bff-go/proto"          // Protobuf 生成代码 / generated protobuf code
)

// realAgentAddr 集成测试目标 agent 的 gRPC 地址（与默认配置一致）。
// realAgentAddr is the gRPC address of the target agent for integration tests (matches default config).
const realAgentAddr = "127.0.0.1:50051"

// TestIntegration_HealthAndProxy 尝试连接真实 agent；如果 agent 未启动则跳过。
// TestIntegration_HealthAndProxy attempts to connect to the real agent; skips if agent is not running.
//
// 测试流程 / Test Flow:
//  1. 重试 3 次连接 agent gRPC Health，失败则 t.Skip
//     Retry connecting agent gRPC Health 3 times, t.Skip on failure
//  2. 建立稳定连接，构造完整 Gin 路由 + httptest 服务器
//     Establish stable connection, build full Gin routes + httptest server
//  3. 验证 /api/health、/api/proxy (mask)、/api/samples 三个端点
//     Verify /api/health, /api/proxy (mask), /api/samples three endpoints
func TestIntegration_HealthAndProxy(t *testing.T) {
	// 尝试连接真实 agent，如果未启动则跳过。为了兼容 agent 启动较慢的情况，重试 3 次。
	// Attempt to connect to real agent; skip if not running. Retry 3 times to accommodate slow agent startup.
	var lastErr error
	for i := 0; i < 3; i++ {
		// 创建 3 秒超时的上下文，避免无限等待。
		// Create a 3-second timeout context to avoid indefinite waiting.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		// 使用无 TLS 凭证创建 gRPC 客户端连接（仅本地测试）。
		// Create gRPC client connection with insecure credentials (local testing only).
		conn, err := grpc.NewClient(realAgentAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			lastErr = err
			cancel()
			time.Sleep(500 * time.Millisecond) // 等待后重试 / wait before retry
			continue
		}
		// 构造 PrivacyService gRPC 客户端并调用 Health RPC 探测可达性。
		// Construct PrivacyService gRPC client and call Health RPC to probe reachability.
		grpcClient := pb.NewPrivacyServiceClient(conn)
		_, err = grpcClient.Health(ctx, &pb.HealthRequest{})
		cancel()
		conn.Close()
		if err == nil {
			lastErr = nil // 连接成功 / connection successful
			break
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond) // 等待后重试 / wait before retry
	}
	// 3 次均失败，跳过集成测试（不视为测试失败）。
	// All 3 attempts failed, skip integration test (not treated as test failure).
	if lastErr != nil {
		t.Skipf("跳过集成测试：agent %s 未可达：%v", realAgentAddr, lastErr)
	}

	// 使用稳定连接运行后续测试。
	// Use stable connection for subsequent tests.
	conn, err := grpc.NewClient(realAgentAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to create stable agent connection: %v", err)
	}
	defer func() { _ = conn.Close() }() // 测试结束后关闭连接 / close connection after test
	// 构造最小化配置（仅集成测试所需字段）。
	// Construct minimal config (only fields needed for integration test).
	cfg := &config.Config{
		AgentGRPCHost: "127.0.0.1",
		AgentGRPCPort: 50051,
		ConsoleHost:   "127.0.0.1",
		ConsolePort:   0, // 0 = 不实际监听 / 0 = don't actually listen
	}
	// 基于已有连接创建 agent 客户端（跳过拨号）。
	// Create agent client from existing connection (skip dialing).
	agentClient := agent.NewFromConnection(conn)

	// 设置 Gin 为测试模式（禁用调试日志）。
	// Set Gin to test mode (disable debug logs).
	gin.SetMode(gin.TestMode)
	// 创建 HTTP 处理器并注册所有路由。
	// Create HTTP handlers and register all routes.
	server := handlers.New(agentClient, cfg,
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		metrics.NewCollector("backend-go-integration"),
	)
	router := gin.New()
	server.RegisterRoutes(router)
	// 启动内存 HTTP 测试服务器（无需真实端口）。
	// Start in-memory HTTP test server (no real port needed).
	ts := httptest.NewServer(router)
	defer ts.Close()

	// 1. 测试 /api/health：验证后端自身正常且能获取 agent 健康信息。
	// 1. Test /api/health: verify backend itself is ok and can retrieve agent health info.
	healthResp, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatalf("GET /api/health failed: %v", err)
	}
	defer healthResp.Body.Close()
	if healthResp.StatusCode != http.StatusOK {
		t.Fatalf("expected /api/health 200, got %d", healthResp.StatusCode)
	}
	var healthBody map[string]any
	if err := json.NewDecoder(healthResp.Body).Decode(&healthBody); err != nil {
		t.Fatalf("decode health response failed: %v", err)
	}
	if healthBody["backend"] != "ok" {
		t.Fatalf("expected backend ok, got %v", healthBody["backend"])
	}
	if healthBody["agent"] == nil {
		t.Fatalf("expected agent health info, got nil")
	}

	// 2. 测试 /api/proxy 转发 /v1/privacy/mask：验证完整的 REST→gRPC→agent 链路。
	// 2. Test /api/proxy forwarding /v1/privacy/mask: verify full REST→gRPC→agent chain.
	reqBody := map[string]any{
		"method": "POST",
		"path":   "/v1/privacy/mask",
		"body": map[string]string{
			"field_name": "email",
			"value":      "alice@example.com",
		},
	}
	b, _ := json.Marshal(reqBody)
	proxyResp, err := http.Post(ts.URL+"/api/proxy", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST /api/proxy failed: %v", err)
	}
	defer proxyResp.Body.Close()
	if proxyResp.StatusCode != http.StatusOK {
		t.Fatalf("expected /api/proxy 200, got %d", proxyResp.StatusCode)
	}
	var proxyBody map[string]any
	if err := json.NewDecoder(proxyResp.Body).Decode(&proxyBody); err != nil {
		t.Fatalf("decode proxy response failed: %v", err)
	}
	data, ok := proxyBody["data"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected proxy response shape: %+v", proxyBody)
	}
	result, ok := data["result"].(string)
	if !ok || result == "" {
		t.Fatalf("expected non-empty masked result, got %+v", data)
	}

	// 3. 测试 /api/samples：验证内置示例列表非空。
	// 3. Test /api/samples: verify built-in sample list is non-empty.
	samplesResp, err := http.Get(ts.URL + "/api/samples")
	if err != nil {
		t.Fatalf("GET /api/samples failed: %v", err)
	}
	defer samplesResp.Body.Close()
	if samplesResp.StatusCode != http.StatusOK {
		t.Fatalf("expected /api/samples 200, got %d", samplesResp.StatusCode)
	}
	var samplesBody struct {
		Samples []any `json:"samples"`
	}
	if err := json.NewDecoder(samplesResp.Body).Decode(&samplesBody); err != nil {
		t.Fatalf("decode samples response failed: %v", err)
	}
	if len(samplesBody.Samples) == 0 {
		t.Fatalf("expected non-empty samples list")
	}
}
