package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/fengzhizi319/PrivShield-go/pkg/metrics"

	"github.com/fengzhizi319/PrivShield-go/console/bff-go/internal/agent"
	"github.com/fengzhizi319/PrivShield-go/console/bff-go/internal/config"
	pb "github.com/fengzhizi319/PrivShield-go/console/bff-go/proto"
)

// testPrivacyServer 是一个只实现 Health 和 Mask 的伪造 gRPC 服务器。
type testPrivacyServer struct {
	pb.UnimplementedPrivacyServiceServer

	HealthFunc              func(context.Context, *pb.HealthRequest) (*pb.HealthResponse, error)
	MaskFunc                func(context.Context, *pb.MaskRequest) (*pb.MaskResponse, error)
	MaskDataFrameFunc       func(context.Context, *pb.MaskDataFrameRequest) (*pb.MaskDataFrameResponse, error)
	KAnonymizeDataFrameFunc func(context.Context, *pb.KAnonymizeDataFrameRequest) (*pb.KAnonymizeDataFrameResponse, error)
}

func (s *testPrivacyServer) Health(ctx context.Context, req *pb.HealthRequest) (*pb.HealthResponse, error) {
	if s.HealthFunc != nil {
		return s.HealthFunc(ctx, req)
	}
	return s.UnimplementedPrivacyServiceServer.Health(ctx, req)
}

func (s *testPrivacyServer) Mask(ctx context.Context, req *pb.MaskRequest) (*pb.MaskResponse, error) {
	if s.MaskFunc != nil {
		return s.MaskFunc(ctx, req)
	}
	return s.UnimplementedPrivacyServiceServer.Mask(ctx, req)
}

func (s *testPrivacyServer) MaskDataFrame(ctx context.Context, req *pb.MaskDataFrameRequest) (*pb.MaskDataFrameResponse, error) {
	if s.MaskDataFrameFunc != nil {
		return s.MaskDataFrameFunc(ctx, req)
	}
	return s.UnimplementedPrivacyServiceServer.MaskDataFrame(ctx, req)
}

func (s *testPrivacyServer) KAnonymizeDataFrame(ctx context.Context, req *pb.KAnonymizeDataFrameRequest) (*pb.KAnonymizeDataFrameResponse, error) {
	if s.KAnonymizeDataFrameFunc != nil {
		return s.KAnonymizeDataFrameFunc(ctx, req)
	}
	return s.UnimplementedPrivacyServiceServer.KAnonymizeDataFrame(ctx, req)
}

// setupTestServer 启动内存 gRPC 服务器并创建带路由的 HTTP 测试服务器。
func setupTestServer(t *testing.T, grpcSrv *testPrivacyServer) (*httptest.Server, *config.Config) {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	gs := grpc.NewServer()
	pb.RegisterPrivacyServiceServer(gs, grpcSrv)
	go func() {
		if err := gs.Serve(listener); err != nil {
			t.Logf("gRPC serve error: %v", err)
		}
	}()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		gs.Stop()
		t.Fatalf("failed to create bufconn client: %v", err)
	}
	// 测试结束时会关闭 httptest.Server，连接关闭在 cleanup 中处理。
	t.Cleanup(func() {
		_ = conn.Close()
		gs.Stop()
	})

	cfg := &config.Config{
		AgentGRPCHost: "127.0.0.1",
		AgentGRPCPort: 50051,
		ConsoleHost:   "127.0.0.1",
		ConsolePort:   0,
	}
	client := agent.NewFromConnection(conn)
	testLogger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	testMC := metrics.NewCollector("backend-go-test")
	server := New(client, cfg, testLogger, testMC)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	server.RegisterRoutes(router)
	return httptest.NewServer(router), cfg
}

func TestHealthHandler(t *testing.T) {
	grpcSrv := &testPrivacyServer{
		HealthFunc: func(_ context.Context, _ *pb.HealthRequest) (*pb.HealthResponse, error) {
			return &pb.HealthResponse{Status: "healthy", Namespace: "default"}, nil
		},
	}
	ts, _ := setupTestServer(t, grpcSrv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if body["backend"] != "ok" {
		t.Fatalf("expected backend ok, got %v", body["backend"])
	}
	agent, ok := body["agent"].(map[string]any)
	if !ok || agent["status"] != "healthy" {
		t.Fatalf("unexpected agent status: %+v", body["agent"])
	}
	// 后端身份标识：Go 后端恒为 go-grpc / gRPC，供前端验证切换生效。
	if body["via"] != "go-grpc" || body["protocol"] != "gRPC" {
		t.Fatalf("expected via=go-grpc protocol=gRPC, got via=%v protocol=%v", body["via"], body["protocol"])
	}
}

func TestSamplesHandler(t *testing.T) {
	grpcSrv := &testPrivacyServer{}
	ts, _ := setupTestServer(t, grpcSrv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/samples")
	if err != nil {
		t.Fatalf("GET /v1/samples failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var body struct {
		Samples []any `json:"samples"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if len(body.Samples) == 0 {
		t.Fatalf("expected non-empty samples list")
	}
}

func TestProxyHandlerMask(t *testing.T) {
	grpcSrv := &testPrivacyServer{
		MaskFunc: func(_ context.Context, req *pb.MaskRequest) (*pb.MaskResponse, error) {
			if req.FieldName != "email" || req.Value != "alice@example.com" {
				t.Fatalf("unexpected mask request: %+v", req)
			}
			return &pb.MaskResponse{Result: "***@example.com"}, nil
		},
	}
	ts, _ := setupTestServer(t, grpcSrv)
	defer ts.Close()

	reqBody := map[string]any{
		"method": "POST",
		"path":   "/v1/privacy/mask",
		"body": map[string]string{
			"field_name": "email",
			"value":      "alice@example.com",
		},
	}
	b, _ := json.Marshal(reqBody)
	resp, err := http.Post(ts.URL+"/v1/proxy", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST /v1/proxy failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	data, ok := body["data"].(map[string]any)
	if !ok || data["result"] != "***@example.com" {
		t.Fatalf("unexpected proxy response: %+v", body)
	}
	// 后端身份标识随代理响应一同下发。
	if body["via"] != "go-grpc" || body["protocol"] != "gRPC" {
		t.Fatalf("expected via=go-grpc protocol=gRPC, got via=%v protocol=%v", body["via"], body["protocol"])
	}
}

// TestStaticServing 验证 Go 后端能独立提供 Console UI 静态资源与 SPA 回退。
func TestStaticServing(t *testing.T) {
	// 构造临时 dist 目录：index.html + assets/app.js
	distDir := t.TempDir()
	indexHTML := "<!doctype html><html><body>console-ui</body></html>"
	if err := os.WriteFile(filepath.Join(distDir, "index.html"), []byte(indexHTML), 0o644); err != nil {
		t.Fatalf("write index.html failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(distDir, "assets"), 0o755); err != nil {
		t.Fatalf("mkdir assets failed: %v", err)
	}
	jsContent := "console.log('app');"
	if err := os.WriteFile(filepath.Join(distDir, "assets", "app.js"), []byte(jsContent), 0o644); err != nil {
		t.Fatalf("write app.js failed: %v", err)
	}

	grpcSrv := &testPrivacyServer{}
	listener := bufconn.Listen(1024 * 1024)
	gs := grpc.NewServer()
	pb.RegisterPrivacyServiceServer(gs, grpcSrv)
	go func() { _ = gs.Serve(listener) }()
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		gs.Stop()
		t.Fatalf("failed to create bufconn client: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		gs.Stop()
	})

	cfg := &config.Config{
		AgentGRPCHost: "127.0.0.1",
		AgentGRPCPort: 50051,
		ConsoleHost:   "127.0.0.1",
		ConsolePort:   0,
		StaticDistDir: distDir,
	}
	server := New(agent.NewFromConnection(conn), cfg,
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		metrics.NewCollector("backend-go-test"),
	)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	server.RegisterRoutes(router)
	ts := httptest.NewServer(router)
	defer ts.Close()

	// 1. 根路径返回 index.html
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(bodyBytes), "console-ui") {
		t.Fatalf("GET / expected index.html, got status=%d body=%s", resp.StatusCode, bodyBytes)
	}

	// 2. 静态资源正常返回
	resp, err = http.Get(ts.URL + "/assets/app.js")
	if err != nil {
		t.Fatalf("GET /assets/app.js failed: %v", err)
	}
	bodyBytes, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(bodyBytes) != jsContent {
		t.Fatalf("GET /assets/app.js expected js content, got status=%d body=%s", resp.StatusCode, bodyBytes)
	}

	// 3. SPA 回退：任意非 /api 路径返回 index.html
	resp, err = http.Get(ts.URL + "/some/spa/route")
	if err != nil {
		t.Fatalf("GET /some/spa/route failed: %v", err)
	}
	bodyBytes, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(bodyBytes), "console-ui") {
		t.Fatalf("SPA fallback expected index.html, got status=%d body=%s", resp.StatusCode, bodyBytes)
	}

	// 4. 未注册的 /api 路径返回 404 JSON而非 index.html
	resp, err = http.Get(ts.URL + "/v1/nonexistent")
	if err != nil {
		t.Fatalf("GET /v1/nonexistent failed: %v", err)
	}
	bodyBytes, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(string(bodyBytes), "Not Found") {
		t.Fatalf("GET /v1/nonexistent expected 404 JSON, got status=%d body=%s", resp.StatusCode, bodyBytes)
	}
}

// TestBatchHandler 验证批量代理端点：逐个转发并汇总成功 / 失败统计，
// 单个请求失败不中断整个批次。
func TestBatchHandler(t *testing.T) {
	grpcSrv := &testPrivacyServer{
		MaskFunc: func(_ context.Context, req *pb.MaskRequest) (*pb.MaskResponse, error) {
			if req.Value == "bad" {
				return nil, status.Error(codes.InvalidArgument, "invalid value")
			}
			return &pb.MaskResponse{Result: "***"}, nil
		},
	}
	ts, _ := setupTestServer(t, grpcSrv)
	defer ts.Close()

	reqBody := map[string]any{
		"requests": []map[string]any{
			{"method": "POST", "path": "/v1/privacy/mask", "body": map[string]string{"field_name": "email", "value": "ok"}},
			{"method": "POST", "path": "/v1/privacy/mask", "body": map[string]string{"field_name": "email", "value": "bad"}},
		},
	}
	b, _ := json.Marshal(reqBody)
	resp, err := http.Post(ts.URL+"/v1/batch", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST /v1/batch failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var body struct {
		Total    int    `json:"total"`
		Passed   int    `json:"passed"`
		Failed   int    `json:"failed"`
		Via      string `json:"via"`
		Protocol string `json:"protocol"`
		Results  []struct {
			Path   string `json:"path"`
			Status int    `json:"status"`
			Error  string `json:"error"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if body.Total != 2 || body.Passed != 1 || body.Failed != 1 {
		t.Fatalf("unexpected batch summary: total=%d passed=%d failed=%d", body.Total, body.Passed, body.Failed)
	}
	if body.Via != "go-grpc" || body.Protocol != "gRPC" {
		t.Fatalf("expected via=go-grpc protocol=gRPC, got via=%q protocol=%q", body.Via, body.Protocol)
	}
	if body.Results[0].Status != http.StatusOK {
		t.Fatalf("expected first result 200, got %d", body.Results[0].Status)
	}
	if body.Results[1].Status == http.StatusOK || body.Results[1].Error == "" {
		t.Fatalf("expected second result to fail with error, got %+v", body.Results[1])
	}
}

// TestBatchHandler_MixedAndRestFallback 验证批量测试支持混合 gRPC 与 REST-only 路径及回退。
func TestBatchHandler_MixedAndRestFallback(t *testing.T) {
	mockAgent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/dynclassification/standards" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"standards":["std1"]}`))
			return
		}
		if r.URL.Path == "/v1/unmapped/rest" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok_from_rest"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer mockAgent.Close()

	u, err := url.Parse(mockAgent.URL)
	if err != nil {
		t.Fatalf("parse mock agent url failed: %v", err)
	}
	t.Setenv("BFF_AGENT_REST_HOST", u.Hostname())
	t.Setenv("BFF_AGENT_REST_PORT", u.Port())

	grpcSrv := &testPrivacyServer{
		MaskFunc: func(_ context.Context, req *pb.MaskRequest) (*pb.MaskResponse, error) {
			return &pb.MaskResponse{Result: "***"}, nil
		},
	}
	ts, _ := setupTestServer(t, grpcSrv)
	defer ts.Close()

	reqBody := map[string]any{
		"requests": []map[string]any{
			{"method": "POST", "path": "/v1/privacy/mask", "body": map[string]string{"field_name": "email", "value": "ok"}},
			{"method": "GET", "path": "/v1/dynclassification/standards"},
			{"method": "POST", "path": "/v1/unmapped/rest", "body": map[string]string{"foo": "bar"}},
		},
	}
	b, _ := json.Marshal(reqBody)
	resp, err := http.Post(ts.URL+"/v1/batch", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST /v1/batch failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var body struct {
		Total   int `json:"total"`
		Passed  int `json:"passed"`
		Failed  int `json:"failed"`
		Results []struct {
			Path   string `json:"path"`
			Status int    `json:"status"`
			Error  string `json:"error"`
			Data   any    `json:"data"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if body.Total != 3 || body.Passed != 3 || body.Failed != 0 {
		t.Fatalf("unexpected batch summary: total=%d passed=%d failed=%d (%+v)", body.Total, body.Passed, body.Failed, body.Results)
	}
	if body.Results[0].Status != http.StatusOK || body.Results[1].Status != http.StatusOK || body.Results[2].Status != http.StatusOK {
		t.Fatalf("expected all results 200, got %+v", body.Results)
	}
}

// TestStaticServingDisabled 验证 dist 目录不存在时仅提供 API（不挂载静态路由）。
func TestStaticServingDisabled(t *testing.T) {
	grpcSrv := &testPrivacyServer{}
	ts, cfg := setupTestServer(t, grpcSrv)
	defer ts.Close()

	// setupTestServer 未设置 StaticDistDir，静态服务应跳过。
	if cfg.StaticDistDir != "" {
		t.Fatalf("expected empty StaticDistDir in test config, got %q", cfg.StaticDistDir)
	}

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET / failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 without dist dir, got %d", resp.StatusCode)
	}
}

// postUploadMultipart 构造一个上传文件的 multipart 请求并发送。
func postUploadMultipart(t *testing.T, url, filename, content, operation, params string) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file failed: %v", err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatalf("write file content failed: %v", err)
	}
	_ = w.WriteField("operation", operation)
	if params != "" {
		_ = w.WriteField("params", params)
	}
	w.Close()

	resp, err := http.Post(url, w.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("POST /v1/upload failed: %v", err)
	}
	return resp
}

// TestUploadHandlerMask 验证上传 CSV 执行脱敏：解析文件 → 构造 gRPC 请求 → 包装返回。
func TestUploadHandlerMask(t *testing.T) {
	grpcSrv := &testPrivacyServer{
		MaskDataFrameFunc: func(_ context.Context, req *pb.MaskDataFrameRequest) (*pb.MaskDataFrameResponse, error) {
			if len(req.Data) != 2 {
				t.Fatalf("expected 2 record entries, got %d", len(req.Data))
			}
			if len(req.Columns) != 1 || req.Columns[0] != "email" {
				t.Fatalf("unexpected columns: %v", req.Columns)
			}
			// 返回脱敏后的记录。
			return &pb.MaskDataFrameResponse{
				Data: []*pb.RecordEntry{
					{Fields: map[string]string{"email": "a***@example.com", "phone": "13800138000"}},
					{Fields: map[string]string{"email": "b***@example.com", "phone": "13900139000"}},
				},
			}, nil
		},
	}
	ts, _ := setupTestServer(t, grpcSrv)
	defer ts.Close()

	csv := "email,phone\nalice@example.com,13800138000\nbob@example.com,13900139000\n"
	resp := postUploadMultipart(t, ts.URL+"/v1/upload", "data.csv", csv, "mask_dataframe", `{"columns":["email"]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	var body struct {
		Status   int    `json:"status"`
		Via      string `json:"via"`
		Protocol string `json:"protocol"`
		Data     struct {
			Operation string              `json:"operation"`
			RowsIn    int                 `json:"rows_in"`
			RowsOut   int                 `json:"rows_out"`
			Result    []map[string]string `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if body.Data.Operation != "mask_dataframe" || body.Data.RowsIn != 2 || body.Data.RowsOut != 2 {
		t.Fatalf("unexpected upload data: %+v", body.Data)
	}
	if body.Via != "go-grpc" || body.Protocol != "gRPC" {
		t.Fatalf("expected via=go-grpc protocol=gRPC, got via=%q protocol=%q", body.Via, body.Protocol)
	}
	if body.Data.Result[0]["email"] != "a***@example.com" {
		t.Fatalf("unexpected masked result: %+v", body.Data.Result)
	}
}

// TestUploadHandlerKAnonymize 验证上传 CSV 执行 K-匿名：解析文件 →
// 构造 gRPC 请求（qi_cols + k）→ 包装返回。
func TestUploadHandlerKAnonymize(t *testing.T) {
	grpcSrv := &testPrivacyServer{
		KAnonymizeDataFrameFunc: func(_ context.Context, req *pb.KAnonymizeDataFrameRequest) (*pb.KAnonymizeDataFrameResponse, error) {
			if len(req.Data) != 2 {
				t.Fatalf("expected 2 record entries, got %d", len(req.Data))
			}
			if len(req.QiCols) != 2 || req.QiCols[0] != "age" || req.QiCols[1] != "zip" {
				t.Fatalf("unexpected qi_cols: %v", req.QiCols)
			}
			if req.K != 3 {
				t.Fatalf("expected k=3, got %d", req.K)
			}
			// 返回泛化后的记录。
			return &pb.KAnonymizeDataFrameResponse{
				Data: []*pb.RecordEntry{
					{Fields: map[string]string{"age": "20-30", "zip": "100**"}},
					{Fields: map[string]string{"age": "20-30", "zip": "100**"}},
				},
			}, nil
		},
	}
	ts, _ := setupTestServer(t, grpcSrv)
	defer ts.Close()

	csv := "age,zip\n25,10001\n28,10002\n"
	resp := postUploadMultipart(t, ts.URL+"/v1/upload", "data.csv", csv, "k_anonymize", `{"qi_cols":["age","zip"],"k":3}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	var body struct {
		Status   int    `json:"status"`
		Via      string `json:"via"`
		Protocol string `json:"protocol"`
		Data     struct {
			Operation string              `json:"operation"`
			RowsIn    int                 `json:"rows_in"`
			RowsOut   int                 `json:"rows_out"`
			Result    []map[string]string `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if body.Data.Operation != "k_anonymize" || body.Data.RowsIn != 2 || body.Data.RowsOut != 2 {
		t.Fatalf("unexpected upload data: %+v", body.Data)
	}
	if body.Via != "go-grpc" || body.Protocol != "gRPC" {
		t.Fatalf("expected via=go-grpc protocol=gRPC, got via=%q protocol=%q", body.Via, body.Protocol)
	}
	if body.Data.Result[0]["age"] != "20-30" {
		t.Fatalf("unexpected generalized result: %+v", body.Data.Result)
	}
}

// TestUploadHandlerKAnonymizeMissingQiCols 验证 k_anonymize 操作缺少 qi_cols 参数时返回 400。
func TestUploadHandlerKAnonymizeMissingQiCols(t *testing.T) {
	grpcSrv := &testPrivacyServer{}
	ts, _ := setupTestServer(t, grpcSrv)
	defer ts.Close()

	csv := "age,zip\n25,10001\n"
	resp := postUploadMultipart(t, ts.URL+"/v1/upload", "data.csv", csv, "k_anonymize", `{"k":3}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 without qi_cols, got %d", resp.StatusCode)
	}
}

// TestUploadHandlerUnsupportedFormat 验证不支持的文件格式返回 400。
func TestUploadHandlerUnsupportedFormat(t *testing.T) {
	grpcSrv := &testPrivacyServer{}
	ts, _ := setupTestServer(t, grpcSrv)
	defer ts.Close()

	resp := postUploadMultipart(t, ts.URL+"/v1/upload", "data.txt", "hello", "mask_dataframe", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// TestUploadHandlerUnsupportedOperation 验证不支持的操作类型返回 400。
func TestUploadHandlerUnsupportedOperation(t *testing.T) {
	grpcSrv := &testPrivacyServer{}
	ts, _ := setupTestServer(t, grpcSrv)
	defer ts.Close()

	resp := postUploadMultipart(t, ts.URL+"/v1/upload", "data.csv", "a,b\n1,2\n", "foobar", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// TestLbTestHandler 验证负载均衡端点：分发探测请求并返回统计。
func TestLbTestHandler(t *testing.T) {
	// 起两个假后端作为探测目标。
	fakeA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer fakeA.Close()
	fakeB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer fakeB.Close()

	grpcSrv := &testPrivacyServer{}
	ts, _ := setupTestServer(t, grpcSrv)
	defer ts.Close()

	reqBody := map[string]any{
		"backends": []map[string]string{
			{"name": "a", "url": fakeA.URL},
			{"name": "b", "url": fakeB.URL},
		},
		"num_requests": 6,
		"strategy":     "round_robin",
	}
	b, _ := json.Marshal(reqBody)
	resp, err := http.Post(ts.URL+"/v1/lb_test", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST /v1/lb_test failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	var body struct {
		Strategy     string `json:"strategy"`
		Total        int    `json:"total"`
		Success      int    `json:"success"`
		Failed       int    `json:"failed"`
		Distribution []struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		} `json:"distribution"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if body.Strategy != "round_robin" || body.Total != 6 || body.Success != 6 || body.Failed != 0 {
		t.Fatalf("unexpected lb summary: %+v", body)
	}
	if len(body.Distribution) != 2 || body.Distribution[0].Count != 3 || body.Distribution[1].Count != 3 {
		t.Fatalf("expected even distribution, got %+v", body.Distribution)
	}
}

// setupTestServerWithCfg 使用自定义配置启动测试服务器，供安全中间件 /
// 上传大小限制等需要特定配置项的用例使用。
func setupTestServerWithCfg(t *testing.T, grpcSrv *testPrivacyServer, cfg *config.Config) *httptest.Server {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	gs := grpc.NewServer()
	pb.RegisterPrivacyServiceServer(gs, grpcSrv)
	go func() { _ = gs.Serve(listener) }()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		gs.Stop()
		t.Fatalf("failed to create bufconn client: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		gs.Stop()
	})

	server := New(agent.NewFromConnection(conn), cfg,
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		metrics.NewCollector("backend-go-test"),
	)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	server.RegisterRoutes(router)
	return httptest.NewServer(router)
}

// TestUploadHandlerOversizedFile 验证超过大小上限的上传返回 413。
func TestUploadHandlerOversizedFile(t *testing.T) {
	grpcSrv := &testPrivacyServer{}
	ts, cfg := setupTestServer(t, grpcSrv)
	defer ts.Close()

	// Upload 在请求时读取 cfg.MaxUploadBytes，故可在路由注册后设置。
	cfg.MaxUploadBytes = 10

	csv := strings.Repeat("a", 100) // 100 字节 > 10 字节上限
	resp := postUploadMultipart(t, ts.URL+"/v1/upload", "data.csv", csv, "mask_dataframe", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", resp.StatusCode)
	}
}

// TestSecurityMiddlewareAPIKey 验证配置 CONSOLE_API_KEY 后的鉴权行为：
// 缺失 / 错误凭证返回 401，正确凭证放行，/health 豁免。
func TestSecurityMiddlewareAPIKey(t *testing.T) {
	grpcSrv := &testPrivacyServer{
		HealthFunc: func(_ context.Context, _ *pb.HealthRequest) (*pb.HealthResponse, error) {
			return &pb.HealthResponse{Status: "healthy", Namespace: "default"}, nil
		},
	}
	cfg := &config.Config{
		AgentGRPCHost: "127.0.0.1",
		AgentGRPCPort: 50051,
		ConsoleHost:   "127.0.0.1",
		ConsolePort:   0,
		ConsoleAPIKey: "secret-key",
	}
	ts := setupTestServerWithCfg(t, grpcSrv, cfg)
	defer ts.Close()

	// 1. 缺失 Authorization 头 → 401
	resp, err := http.Get(ts.URL + "/v1/samples")
	if err != nil {
		t.Fatalf("GET /v1/samples failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without api key, got %d", resp.StatusCode)
	}

	// 2. 错误凭证 → 401
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/samples", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/samples with wrong key failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong key, got %d", resp.StatusCode)
	}

	// 3. 正确凭证 → 200
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/v1/samples", nil)
	req.Header.Set("Authorization", "Bearer secret-key")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/samples with correct key failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with correct key, got %d", resp.StatusCode)
	}

	// 4. /health 豁免鉴权（无需凭证）
	resp, err = http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected health exempt from auth, got %d", resp.StatusCode)
	}
}

// TestSecurityMiddlewareRateLimit 验证限流：超过阈值后返回 429。
func TestSecurityMiddlewareRateLimit(t *testing.T) {
	grpcSrv := &testPrivacyServer{}
	cfg := &config.Config{
		AgentGRPCHost:    "127.0.0.1",
		AgentGRPCPort:    50051,
		ConsoleHost:      "127.0.0.1",
		ConsolePort:      0,
		ConsoleRateLimit: 2, // 极低阈值便于触发
	}
	ts := setupTestServerWithCfg(t, grpcSrv, cfg)
	defer ts.Close()

	got429 := false
	for i := 0; i < 5; i++ {
		resp, err := http.Get(ts.URL + "/v1/samples")
		if err != nil {
			t.Fatalf("GET /v1/samples failed: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatalf("expected 429 after exceeding rate limit")
	}
}

// TestLbTestHandlerInvalidScheme 验证非法 scheme 的探测地址返回 400（SSRF 防护）。
func TestLbTestHandlerInvalidScheme(t *testing.T) {
	grpcSrv := &testPrivacyServer{}
	ts, _ := setupTestServer(t, grpcSrv)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/v1/lb_test", "application/json", strings.NewReader(`{"backends":[{"name":"a","url":"file:///etc/passwd"}],"num_requests":3,"strategy":"round_robin"}`))
	if err != nil {
		t.Fatalf("POST /v1/lb_test failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid scheme, got %d", resp.StatusCode)
	}
}

// TestProxyRestDynClassification 验证 dynclassification 路径走 REST 透明转发：
// 起一个 mock agent REST 后端，通过 PRIVACY_REST_HOST/PORT 指向它，
// 确认 /v1/dynclassification/* 请求被透传且返回 Via=go-rest-proxy / Protocol=REST。
func TestProxyRestDynClassification(t *testing.T) {
	// mock agent REST 后端：回显 standards 列表，并校验请求确实到达。
	hit := false
	mockAgent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		if r.URL.Path != "/v1/dynclassification/standards" {
			t.Fatalf("unexpected proxied path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"standards":[{"standard_id":"sc_health_db51","category_count":42}]}`))
	}))
	defer mockAgent.Close()

	// 解析 mock 后端地址，拆出 host / port 注入环境变量。
	u, err := url.Parse(mockAgent.URL)
	if err != nil {
		t.Fatalf("parse mock agent url failed: %v", err)
	}
	t.Setenv("BFF_AGENT_REST_HOST", u.Hostname())
	t.Setenv("BFF_AGENT_REST_PORT", u.Port())

	grpcSrv := &testPrivacyServer{}
	ts, _ := setupTestServer(t, grpcSrv)
	defer ts.Close()

	reqBody := map[string]any{
		"method": "GET",
		"path":   "/v1/dynclassification/standards",
	}
	b, _ := json.Marshal(reqBody)
	resp, err := http.Post(ts.URL+"/v1/proxy", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST /v1/proxy failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	var body struct {
		Status   int    `json:"status"`
		Via      string `json:"via"`
		Protocol string `json:"protocol"`
		Data     struct {
			Standards []struct {
				StandardID    string `json:"standard_id"`
				CategoryCount int    `json:"category_count"`
			} `json:"standards"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	// REST 透明转发的后端身份标识。
	if body.Via != "go-rest-proxy" || body.Protocol != "REST" {
		t.Fatalf("expected via=go-rest-proxy protocol=REST, got via=%q protocol=%q", body.Via, body.Protocol)
	}
	if !hit {
		t.Fatalf("expected mock agent REST backend to be hit")
	}
	// 上游数据（含新增 category_count 字段）应被原样透传。
	if len(body.Data.Standards) != 1 || body.Data.Standards[0].StandardID != "sc_health_db51" || body.Data.Standards[0].CategoryCount != 42 {
		t.Fatalf("unexpected proxied data: %+v", body.Data)
	}
}

// TestProxyRestErrorPassthrough 验证 REST 透明转发的错误契约：
// 当 agent REST 返回 4xx/5xx 时，Go 后端应透传原始状态码，
// 并在 detail 中保留上游错误体（前端依赖 {detail} 形状展示错误）。
func TestProxyRestErrorPassthrough(t *testing.T) {
	// mock agent REST 后端：对 eval 请求返回 400 + FastAPI 风格的 detail 错误体。
	mockAgent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail":"invalid standard: unknown"}`))
	}))
	defer mockAgent.Close()

	u, err := url.Parse(mockAgent.URL)
	if err != nil {
		t.Fatalf("parse mock agent url failed: %v", err)
	}
	t.Setenv("BFF_AGENT_REST_HOST", u.Hostname())
	t.Setenv("BFF_AGENT_REST_PORT", u.Port())

	grpcSrv := &testPrivacyServer{}
	ts, _ := setupTestServer(t, grpcSrv)
	defer ts.Close()

	reqBody := map[string]any{
		"method": "POST",
		"path":   "/v1/dynclassification/eval",
		"body":   map[string]any{"fieldName": "phone"},
	}
	b, _ := json.Marshal(reqBody)
	resp, err := http.Post(ts.URL+"/v1/proxy", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST /v1/proxy failed: %v", err)
	}
	defer resp.Body.Close()

	// 上游 400 状态码应被透传（而非包装为 200 或 502）。
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 400 passthrough, got %d: %s", resp.StatusCode, body)
	}

	var body struct {
		Detail string `json:"detail"`
		Code   string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response failed: %v", err)
	}
	// detail 应保留上游错误体，code 字段回显统一错误码。
	if !strings.Contains(body.Detail, "invalid standard") {
		t.Fatalf("expected detail to preserve upstream error body, got %q", body.Detail)
	}
	if body.Code != "INVALID_ARGUMENT" {
		t.Fatalf("expected code field INVALID_ARGUMENT, got %q", body.Code)
	}
}

// TestLbTestHandlerEmptyBackends 验证 backends 为空时返回 400。
func TestLbTestHandlerEmptyBackends(t *testing.T) {
	grpcSrv := &testPrivacyServer{}
	ts, _ := setupTestServer(t, grpcSrv)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/v1/lb_test", "application/json", strings.NewReader(`{"backends":[],"num_requests":3,"strategy":"round_robin"}`))
	if err != nil {
		t.Fatalf("POST /v1/lb_test failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// TestConcurrencyTestHandler 验证并发压测接口 /v1/concurrency_test。
func TestConcurrencyTestHandler(t *testing.T) {
	grpcSrv := &testPrivacyServer{
		MaskFunc: func(ctx context.Context, req *pb.MaskRequest) (*pb.MaskResponse, error) {
			return &pb.MaskResponse{Result: "****"}, nil
		},
	}
	ts, _ := setupTestServer(t, grpcSrv)
	defer ts.Close()

	reqBody := `{"path":"/v1/privacy/mask","method":"POST","concurrency":5,"total_requests":10}`
	resp, err := http.Post(ts.URL+"/v1/concurrency_test", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /v1/concurrency_test failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if int(data["total"].(float64)) != 10 {
		t.Fatalf("expected total 10, got %v", data["total"])
	}
	if int(data["success"].(float64)) != 10 {
		t.Fatalf("expected success 10, got %v", data["success"])
	}
}

// TestConcurrencyTestHandler_BlockedPath 验证压测接口拒绝非白名单路径（P37）。
func TestConcurrencyTestHandler_BlockedPath(t *testing.T) {
	grpcSrv := &testPrivacyServer{}
	ts, _ := setupTestServer(t, grpcSrv)
	defer ts.Close()

	// /v1/ops/* 属于运维内部接口，不应允许通过压测端点访问
	blockedPaths := []string{
		"/v1/ops/health",
		"/v1/ops/config",
		"/v1/internal/debug",
		"/v1/samples",
		"/metrics",
	}
	for _, p := range blockedPaths {
		reqBody := fmt.Sprintf(`{"path":%q,"method":"GET","concurrency":2,"total_requests":4}`, p)
		resp, err := http.Post(ts.URL+"/v1/concurrency_test", "application/json", strings.NewReader(reqBody))
		if err != nil {
			t.Fatalf("POST /v1/concurrency_test path=%s failed: %v", p, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("path %s: expected 400, got %d", p, resp.StatusCode)
		}
	}
}

// TestIsAllowedConcurrencyPath 单元测试白名单判定函数。
func TestIsAllowedConcurrencyPath(t *testing.T) {
	allowed := []string{
		"/v1/privacy/mask",
		"/v1/privacy/dp",
		"/v1/dynclassification/classify",
		"/v1/medical/pipeline",
		"/v1/pipeline/process",
		"/health",
	}
	for _, p := range allowed {
		if !isAllowedConcurrencyPath(p) {
			t.Errorf("expected %q to be allowed", p)
		}
	}

	blocked := []string{
		"/v1/ops/health",
		"/v1/ops/config",
		"/v1/internal/debug",
		"/v1/samples",
		"/metrics",
		"/v1/privacy/../ops/health", // 路径穿越（不会被前缀匹配放行，因为 /v1/privacy/../ops 不匹配任何白名单前缀）
		"",
	}
	for _, p := range blocked {
		if isAllowedConcurrencyPath(p) {
			t.Errorf("expected %q to be blocked", p)
		}
	}
}

// TestMedicalPipelineHandler 验证医疗敏感数据全流程治理接口 /v1/medical_pipeline。
func TestMedicalPipelineHandler(t *testing.T) {
	grpcSrv := &testPrivacyServer{}
	ts, _ := setupTestServer(t, grpcSrv)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/v1/medical_pipeline", "application/json", strings.NewReader(`{"records":[]}`))
	if err != nil {
		t.Fatalf("POST /v1/medical_pipeline failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 0 {
		t.Fatalf("expected non-zero HTTP response")
	}
}

// TestPipelineProcessHandler 验证通用分类分级与脱敏流水线接口 /v1/pipeline/process。
func TestPipelineProcessHandler(t *testing.T) {
	grpcSrv := &testPrivacyServer{}
	ts, _ := setupTestServer(t, grpcSrv)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/v1/pipeline/process", "application/json", strings.NewReader(`{"records":[]}`))
	if err != nil {
		t.Fatalf("POST /v1/pipeline/process failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 0 {
		t.Fatalf("expected non-zero HTTP response")
	}
}

// TestBatchTooLarge 验证批量请求超过 100 上限时返回 400。
// P41 fix: batch size limit enforcement.
func TestBatchTooLarge(t *testing.T) {
	grpcSrv := &testPrivacyServer{}
	ts, _ := setupTestServer(t, grpcSrv)
	defer ts.Close()

	// 构造 101 个请求的批量（超过 100 上限）
	requests := make([]map[string]any, 101)
	for i := range requests {
		requests[i] = map[string]any{"method": "POST", "path": "/v1/privacy/mask", "body": map[string]any{"field_name": "email", "value": "a@b.com"}}
	}
	body, _ := json.Marshal(map[string]any{"requests": requests})

	resp, err := http.Post(ts.URL+"/v1/batch", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/batch failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for batch > 100, got %d", resp.StatusCode)
	}
}

// TestDualProtocolSwitch 验证 gRPC 与 REST 双协议连接与切换能力：
// 1. 默认或 protocol=grpc 时走 gRPC 链路（via=go-grpc, protocol=gRPC）
// 2. protocol=rest 或 Header X-PrivShield-Protocol=REST 时走 REST 链路（via=go-rest-proxy, protocol=REST）
func TestDualProtocolSwitch(t *testing.T) {
	mockAgent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{"status":"ok","mode":"rest"}`))
			return
		}
		if r.URL.Path == "/v1/privacy/mask" {
			_, _ = w.Write([]byte(`{"result":"a***b@example.com"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer mockAgent.Close()

	u, err := url.Parse(mockAgent.URL)
	if err != nil {
		t.Fatalf("parse mock agent url: %v", err)
	}
	t.Setenv("BFF_AGENT_REST_HOST", u.Hostname())
	t.Setenv("BFF_AGENT_REST_PORT", u.Port())

	grpcSrv := &testPrivacyServer{
		MaskFunc: func(ctx context.Context, req *pb.MaskRequest) (*pb.MaskResponse, error) {
			return &pb.MaskResponse{Result: "a***b@example.com"}, nil
		},
	}
	ts, _ := setupTestServer(t, grpcSrv)
	defer ts.Close()

	// 1. 测试 Health 默认走 gRPC
	{
		resp, err := http.Get(ts.URL + "/health")
		if err != nil {
			t.Fatalf("GET /health failed: %v", err)
		}
		var h struct {
			Backend  string `json:"backend"`
			Via      string `json:"via"`
			Protocol string `json:"protocol"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&h)
		resp.Body.Close()
		if h.Via != "go-grpc" || h.Protocol != "gRPC" {
			t.Errorf("expected gRPC health, got via=%s protocol=%s", h.Via, h.Protocol)
		}
	}

	// 2. 测试 Health 传 protocol=rest 走 REST
	{
		resp, err := http.Get(ts.URL + "/health?protocol=rest")
		if err != nil {
			t.Fatalf("GET /health?protocol=rest failed: %v", err)
		}
		var h struct {
			Backend  string `json:"backend"`
			Via      string `json:"via"`
			Protocol string `json:"protocol"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&h)
		resp.Body.Close()
		if h.Via != "go-rest-proxy" || h.Protocol != "REST" {
			t.Errorf("expected REST health, got via=%s protocol=%s", h.Via, h.Protocol)
		}
	}

	// 3. 测试 Proxy 默认走 gRPC
	{
		reqBody, _ := json.Marshal(map[string]any{
			"method": "POST",
			"path":   "/v1/privacy/mask",
			"body":   map[string]any{"field_name": "email", "value": "alice@example.com"},
		})
		resp, err := http.Post(ts.URL+"/v1/proxy", "application/json", bytes.NewReader(reqBody))
		if err != nil {
			t.Fatalf("POST /v1/proxy failed: %v", err)
		}
		var p struct {
			Via      string `json:"via"`
			Protocol string `json:"protocol"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&p)
		resp.Body.Close()
		if p.Via != "go-grpc" || p.Protocol != "gRPC" {
			t.Errorf("expected gRPC proxy, got via=%s protocol=%s", p.Via, p.Protocol)
		}
	}

	// 4. 测试 Proxy 传 protocol=rest 走 REST
	{
		reqBody, _ := json.Marshal(map[string]any{
			"method": "POST",
			"path":   "/v1/privacy/mask",
			"body":   map[string]any{"field_name": "email", "value": "alice@example.com"},
		})
		resp, err := http.Post(ts.URL+"/v1/proxy?protocol=rest", "application/json", bytes.NewReader(reqBody))
		if err != nil {
			t.Fatalf("POST /v1/proxy?protocol=rest failed: %v", err)
		}
		var p struct {
			Via      string `json:"via"`
			Protocol string `json:"protocol"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&p)
		resp.Body.Close()
		if p.Via != "go-rest-proxy" || p.Protocol != "REST" {
			t.Errorf("expected REST proxy, got via=%s protocol=%s", p.Via, p.Protocol)
		}
	}

	// 5. 测试 Proxy 传 Header X-PrivShield-Protocol: REST 走 REST
	{
		reqBody, _ := json.Marshal(map[string]any{
			"method": "POST",
			"path":   "/v1/privacy/mask",
			"body":   map[string]any{"field_name": "email", "value": "alice@example.com"},
		})
		httpReq, _ := http.NewRequest("POST", ts.URL+"/v1/proxy", bytes.NewReader(reqBody))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("X-PrivShield-Protocol", "REST")
		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			t.Fatalf("POST /v1/proxy with Header failed: %v", err)
		}
		var p struct {
			Via      string `json:"via"`
			Protocol string `json:"protocol"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&p)
		resp.Body.Close()
		if p.Via != "go-rest-proxy" || p.Protocol != "REST" {
			t.Errorf("expected REST proxy via Header, got via=%s protocol=%s", p.Via, p.Protocol)
		}
	}
}
