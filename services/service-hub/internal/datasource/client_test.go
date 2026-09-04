package datasource

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/fengzhizi319/PrivShield-go/pkg/circuitbreaker"
	dspb "github.com/fengzhizi319/PrivShield-go/services/datasource-mgr/proto"
	"github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/config"
)

// mockDSPBServer implements the generated DataSourceManagerServiceServer interface for gRPC testing.
// mockDSPBServer 实现了 datasource-mgr 的 gRPC 服务端桩接口，用于在单元测试中模拟数据源的 RPC 响应。
type mockDSPBServer struct {
	dspb.UnimplementedDataSourceManagerServiceServer
}

func (s *mockDSPBServer) Health(ctx context.Context, _ *dspb.HealthRequest) (*dspb.HealthResponse, error) {
	return &dspb.HealthResponse{Status: "ok", LatencyMs: 1, Via: "datasource-mgr"}, nil
}

func (s *mockDSPBServer) ListDataSources(ctx context.Context, _ *dspb.ListMockSourcesRequest) (*dspb.ListMockSourcesResponse, error) {
	return &dspb.ListMockSourcesResponse{
		Total: 2,
		Sources: []*dspb.DataSourceProto{
			{Id: "ds_yibao", Name: "医保数据", Status: "connected"},
			{Id: "ds_kangyang", Name: "康养数据", Status: "connected"},
		},
		Via: "datasource-mgr",
	}, nil
}

func (s *mockDSPBServer) GetDataSource(ctx context.Context, req *dspb.GetDataSourceRequest) (*dspb.DataSourceProto, error) {
	return &dspb.DataSourceProto{Id: req.Id, Name: "测试数据源", Status: "connected"}, nil
}

func (s *mockDSPBServer) TestConnection(ctx context.Context, req *dspb.TestConnectionRequest) (*dspb.TestConnectionResponse, error) {
	return &dspb.TestConnectionResponse{DatasourceId: req.Id, Success: true, LatencyMs: 1, Via: "datasource-mgr"}, nil
}

// setupMockDatasourceServer initializes both HTTP REST and gRPC mock servers on dynamic free ports.
// setupMockDatasourceServer 启动本地 Mock HTTP 服务器与 Mock gRPC 服务器，构建测试环境 Config。
func setupMockDatasourceServer(t *testing.T) (*httptest.Server, *grpc.Server, net.Listener, *config.Config) {
	t.Helper()

	mux := http.NewServeMux()

	// 1. 注册 HTTP /health 端点
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "backend": "ok"})
	})

	// 2. 注册数据源列表端点
	mux.HandleFunc("/v1/datasources", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total":       2,
			"datasources": []string{"ds_yibao", "ds_kangyang"},
		})
	})

	// 7. 注册数据源连通性测试端点
	mux.HandleFunc("/v1/datasources/ds_yibao/test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"datasource_id": "ds_yibao",
			"success":       true,
		})
	})

	srv := httptest.NewServer(mux)

	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())

	// 启动 Mock gRPC 监听
	grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen gRPC failed: %v", err)
	}
	grpcHost, grpcPortStr, _ := net.SplitHostPort(grpcLis.Addr().String())
	grpcPort, _ := strconv.Atoi(grpcPortStr)

	grpcSrv := grpc.NewServer()
	dspb.RegisterDataSourceManagerServiceServer(grpcSrv, &mockDSPBServer{})

	go func() {
		_ = grpcSrv.Serve(grpcLis)
	}()

	cfg := &config.Config{
		DatasourceRESTHost: u.Hostname(),
		DatasourceRESTPort: port,
		DatasourceGRPCHost: grpcHost,
		DatasourceGRPCPort: grpcPort,
	}

	return srv, grpcSrv, grpcLis, cfg
}

// TestDatasourceClient tests both HTTP REST and gRPC endpoints of the Datasource Client.
// TestDatasourceClient 对 Client 的全部 HTTP REST 方法与 gRPC RPC 方法执行端到端单元测试。
func TestDatasourceClient(t *testing.T) {
	srv, grpcSrv, grpcLis, cfg := setupMockDatasourceServer(t)
	defer func() {
		grpcSrv.Stop()
		_ = grpcLis.Close()
		srv.Close()
	}()

	client := New(cfg)
	defer client.Close()
	ctx := context.Background()

	// 2. Health 健康检查
	h, err := client.Health(ctx)
	if err != nil || h["status"] != "ok" {
		t.Fatalf("Health failed: %v, resp: %+v", err, h)
	}

	// 3. ListDataSources 数据源列表
	list, err := client.ListDataSources(ctx)
	if err != nil || list["total"].(float64) != 2 {
		t.Fatalf("ListDataSources failed: %v", err)
	}

	// 4. TestConnection 连通性测试
	conn, err := client.TestConnection(ctx, "ds_yibao")
	if err != nil || conn["success"] != true {
		t.Fatalf("TestConnection failed: %v", err)
	}

	// ── B. gRPC 远程过程调用测试 ──
	// 5. HealthGRPC gRPC 健康检查
	grpcHealth, err := client.HealthGRPC(ctx)
	if err != nil || grpcHealth.Status != "ok" {
		t.Fatalf("HealthGRPC failed: %v", err)
	}

	// 6. ListDataSourcesGRPC gRPC 数据源列表获取
	listGRPC, err := client.ListDataSourcesGRPC(ctx)
	if err != nil || listGRPC.Total != 2 {
		t.Fatalf("ListDataSourcesGRPC failed: %v", err)
	}

	// 7. GetDataSourceGRPC gRPC 数据源元数据详情获取
	dsInfo, err := client.GetDataSourceGRPC(ctx, "ds_yibao")
	if err != nil || dsInfo.Id != "ds_yibao" {
		t.Fatalf("GetDataSourceGRPC failed: %v", err)
	}

	// 8. TestConnectionGRPC gRPC 连通性测试探针
	connGRPC, err := client.TestConnectionGRPC(ctx, "ds_yibao")
	if err != nil || !connGRPC.Success {
		t.Fatalf("TestConnectionGRPC failed: %v", err)
	}

	if st := client.CircuitStateString(); st != "closed" {
		t.Fatalf("expected circuit breaker to be closed, got: %s", st)
	}
}

func TestDatasourceClient_CircuitBreaker(t *testing.T) {
	// Server returns 500
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer failSrv.Close()

	u, _ := url.Parse(failSrv.URL)
	port, _ := strconv.Atoi(u.Port())

	cfg := &config.Config{
		DatasourceRESTHost: u.Hostname(),
		DatasourceRESTPort: port,
	}

	client := New(cfg)
	client.breaker = circuitbreaker.NewBreaker(2, 100*time.Millisecond)
	client.maxRetries = 0

	ctx := context.Background()

	// 1. Fail request 1
	_, err := client.Health(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// 2. Fail request 2 -> trips circuit breaker
	_, err = client.Health(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if client.CircuitStateString() != "open" {
		t.Fatalf("expected circuit breaker to be open, got: %s", client.CircuitStateString())
	}

	// 3. Fast fail while circuit is open
	_, err = client.Health(ctx)
	if err == nil || !strings.Contains(err.Error(), "circuit breaker open") {
		t.Fatalf("expected circuit breaker open error, got: %v", err)
	}

	// 4. Wait for cooldown
	time.Sleep(150 * time.Millisecond)

	// Half-open transition
	if err := client.checkCircuit(); err != nil {
		t.Fatalf("expected half-open allow probe, got: %v", err)
	}
	if client.CircuitStateString() != "half-open" {
		t.Fatalf("expected half-open state, got: %s", client.CircuitStateString())
	}
}

func TestDatasourceClient_MultiNodeFailover(t *testing.T) {
	// Node 1 fails with 500
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer failSrv.Close()

	// Node 2 succeeds
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "node": "node2"})
	}))
	defer okSrv.Close()

	cfg := &config.Config{
		DatasourceRESTHost: "127.0.0.1",
		DatasourceRESTPort: 8083,
	}

	client := New(cfg)
	client.baseURLs = []string{failSrv.URL, okSrv.URL}
	client.breakers = map[string]*circuitbreaker.Breaker{
		failSrv.URL: circuitbreaker.NewBreaker(2, 500*time.Millisecond),
		okSrv.URL:   circuitbreaker.NewBreaker(2, 500*time.Millisecond),
	}
	client.maxRetries = 1
	client.retryBaseDelay = 10 * time.Millisecond

	ctx := context.Background()

	// Call Health: node 1 should fail, then failover to node 2 and succeed!
	resp, err := client.Health(ctx)
	if err != nil {
		t.Fatalf("expected failover to succeed, got: %v", err)
	}
	if resp["node"] != "node2" {
		t.Fatalf("expected response from node2, got: %+v", resp)
	}
}
