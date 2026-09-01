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

func (s *mockDSPBServer) GetYibaoData(ctx context.Context, req *dspb.DataQueryRequest) (*dspb.DataQueryResponse, error) {
	return &dspb.DataQueryResponse{
		SourceId:   "ds_yibao",
		SourceName: "医保就医结算",
		Total:      50,
		Records:    []*dspb.DataRowProto{{Fields: map[string]string{"name": "张三", "id_card": "110101199001011234"}}},
		Via:        "datasource-mgr",
	}, nil
}

func (s *mockDSPBServer) GetKangyangData(ctx context.Context, req *dspb.DataQueryRequest) (*dspb.DataQueryResponse, error) {
	return &dspb.DataQueryResponse{
		SourceId:   "ds_kangyang",
		SourceName: "康养健康档案",
		Total:      50,
		Records:    []*dspb.DataRowProto{{Fields: map[string]string{"name": "李四", "phone": "13800138000"}}},
		Via:        "datasource-mgr",
	}, nil
}

func (s *mockDSPBServer) GetMockData3(ctx context.Context, req *dspb.DataQueryRequest) (*dspb.DataQueryResponse, error) {
	return &dspb.DataQueryResponse{SourceId: "ds_mock3", Total: 10, Via: "datasource-mgr"}, nil
}

func (s *mockDSPBServer) GetMockData4(ctx context.Context, req *dspb.DataQueryRequest) (*dspb.DataQueryResponse, error) {
	return &dspb.DataQueryResponse{SourceId: "ds_mock4", Total: 10, Via: "datasource-mgr"}, nil
}

func (s *mockDSPBServer) GetDataBySource(ctx context.Context, req *dspb.SourceDataQueryRequest) (*dspb.DataQueryResponse, error) {
	if req.SourceId == "ds_kangyang" {
		return s.GetKangyangData(ctx, &dspb.DataQueryRequest{})
	}
	return s.GetYibaoData(ctx, &dspb.DataQueryRequest{})
}

func (s *mockDSPBServer) ListMockSources(ctx context.Context, _ *dspb.ListMockSourcesRequest) (*dspb.ListMockSourcesResponse, error) {
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

	// 1. 注册 HTTP /api/health 端点
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "backend": "ok"})
	})

	// 2. 注册 API 1: Yibao 医保数据端点
	mux.HandleFunc("/api/v1/yibao", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DataQueryResult{
			DatasourceID: "ds_yibao",
			SourceID:     "ds_yibao",
			SourceName:   "医保就医结算",
			Total:        50,
			Records:      []map[string]any{{"person_id": "110101", "name": "张三"}},
			Via:          "datasource-mgr",
		})
	})
	mux.HandleFunc("/api/datasources/ds_yibao/records", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DataQueryResult{
			DatasourceID: "ds_yibao",
			SourceID:     "ds_yibao",
			SourceName:   "医保就医结算",
			Total:        50,
			Records:      []map[string]any{{"person_id": "110101", "name": "张三"}},
			Via:          "datasource-mgr",
		})
	})

	// 3. 注册 API 2: Kangyang 康养档案端点
	mux.HandleFunc("/api/v1/kangyang", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DataQueryResult{
			DatasourceID: "ds_kangyang",
			SourceID:     "ds_kangyang",
			SourceName:   "康养健康档案",
			Total:        50,
			Records:      []map[string]any{{"elder_id": "KY001", "name": "李四"}},
			Via:          "datasource-mgr",
		})
	})
	mux.HandleFunc("/api/datasources/ds_kangyang/records", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DataQueryResult{
			DatasourceID: "ds_kangyang",
			SourceID:     "ds_kangyang",
			SourceName:   "康养健康档案",
			Total:        50,
			Records:      []map[string]any{{"elder_id": "KY001", "name": "李四"}},
			Via:          "datasource-mgr",
		})
	})

	// 4. 注册 API 3: Mock3 预留政务端点
	mux.HandleFunc("/api/v1/mock3", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DataQueryResult{
			DatasourceID: "ds_mock3",
			SourceID:     "ds_mock3",
			Total:        10,
			Records:      []map[string]any{{"service_code": "GOV_01"}},
		})
	})
	mux.HandleFunc("/api/datasources/ds_mock3/records", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DataQueryResult{
			DatasourceID: "ds_mock3",
			SourceID:     "ds_mock3",
			Total:        10,
			Records:      []map[string]any{{"service_code": "GOV_01"}},
		})
	})

	// 5. 注册 API 4: Mock4 预留企业端点
	mux.HandleFunc("/api/v1/mock4", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DataQueryResult{
			DatasourceID: "ds_mock4",
			SourceID:     "ds_mock4",
			Total:        10,
			Records:      []map[string]any{{"dept_code": "FIN_01"}},
		})
	})
	mux.HandleFunc("/api/datasources/ds_mock4/records", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DataQueryResult{
			DatasourceID: "ds_mock4",
			SourceID:     "ds_mock4",
			Total:        10,
			Records:      []map[string]any{{"dept_code": "FIN_01"}},
		})
	})

	// 6. 注册数据源列表端点
	mux.HandleFunc("/api/datasources", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total":       2,
			"datasources": []string{"ds_yibao", "ds_kangyang"},
		})
	})

	// 7. 注册数据源连通性测试端点
	mux.HandleFunc("/api/datasources/ds_yibao/test", func(w http.ResponseWriter, r *http.Request) {
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

	// ── A. HTTP REST 端点测试 ──
	// 1. Health 健康检查
	h, err := client.Health(ctx)
	if err != nil || h["status"] != "ok" {
		t.Fatalf("Health failed: %v, resp: %+v", err, h)
	}

	// 2. FetchYibaoData 医保数据抽取
	yb, err := client.FetchYibaoData(ctx, 10, 0)
	if err != nil || yb.SourceID != "ds_yibao" {
		t.Fatalf("FetchYibaoData failed: %v, resp: %+v", err, yb)
	}

	// 3. FetchKangyangData 康养数据抽取
	ky, err := client.FetchKangyangData(ctx, 10, 0)
	if err != nil || ky.SourceID != "ds_kangyang" {
		t.Fatalf("FetchKangyangData failed: %v, resp: %+v", err, ky)
	}

	// 4. FetchMockData3 & FetchMockData4 预留数据源抽取
	m3, err := client.FetchMockData3(ctx, 5, 0)
	if err != nil || m3.SourceID != "ds_mock3" {
		t.Fatalf("FetchMockData3 failed: %v", err)
	}
	m4, err := client.FetchMockData4(ctx, 5, 0)
	if err != nil || m4.SourceID != "ds_mock4" {
		t.Fatalf("FetchMockData4 failed: %v", err)
	}

	// 5. FetchDataBySource 中文/英文关键字自动分发路由
	bySrc, err := client.FetchDataBySource(ctx, "医保数据库", 5, 0)
	if err != nil || bySrc.SourceID != "ds_yibao" {
		t.Fatalf("FetchDataBySource dispatch failed: %v", err)
	}

	// 6. ListDataSources 数据源列表
	list, err := client.ListDataSources(ctx)
	if err != nil || list["total"].(float64) != 2 {
		t.Fatalf("ListDataSources failed: %v", err)
	}

	// 7. TestConnection 连通性测试
	conn, err := client.TestConnection(ctx, "ds_yibao")
	if err != nil || conn["success"] != true {
		t.Fatalf("TestConnection failed: %v", err)
	}

	// ── B. gRPC 远程过程调用测试 ──
	// 8. HealthGRPC gRPC 健康检查
	grpcHealth, err := client.HealthGRPC(ctx)
	if err != nil || grpcHealth.Status != "ok" {
		t.Fatalf("HealthGRPC failed: %v", err)
	}

	// 9. FetchYibaoDataGRPC gRPC 医保数据抽取
	ybGRPC, err := client.FetchYibaoDataGRPC(ctx, 10, 0)
	if err != nil || ybGRPC.SourceID != "ds_yibao" || len(ybGRPC.Records) == 0 {
		t.Fatalf("FetchYibaoDataGRPC failed: %v", err)
	}

	// 10. FetchKangyangDataGRPC gRPC 康养数据抽取
	kyGRPC, err := client.FetchKangyangDataGRPC(ctx, 10, 0)
	if err != nil || kyGRPC.SourceID != "ds_kangyang" || len(kyGRPC.Records) == 0 {
		t.Fatalf("FetchKangyangDataGRPC failed: %v", err)
	}

	// 11. FetchMockData3GRPC & FetchMockData4GRPC gRPC 预留数据源抽取
	m3GRPC, err := client.FetchMockData3GRPC(ctx, 5, 0)
	if err != nil || m3GRPC.SourceID != "ds_mock3" {
		t.Fatalf("FetchMockData3GRPC failed: %v", err)
	}
	m4GRPC, err := client.FetchMockData4GRPC(ctx, 5, 0)
	if err != nil || m4GRPC.SourceID != "ds_mock4" {
		t.Fatalf("FetchMockData4GRPC failed: %v", err)
	}

	// 12. FetchDataBySourceGRPC gRPC 动态源数据抽取
	bySrcGRPC, err := client.FetchDataBySourceGRPC(ctx, "ds_kangyang", 5, 0)
	if err != nil || bySrcGRPC.SourceID != "ds_kangyang" {
		t.Fatalf("FetchDataBySourceGRPC failed: %v", err)
	}

	// 13. ListMockSourcesGRPC gRPC 数据源列表获取
	listGRPC, err := client.ListMockSourcesGRPC(ctx)
	if err != nil || listGRPC.Total != 2 {
		t.Fatalf("ListMockSourcesGRPC failed: %v", err)
	}

	// 14. GetDataSourceGRPC gRPC 数据源元数据详情获取
	dsInfo, err := client.GetDataSourceGRPC(ctx, "ds_yibao")
	if err != nil || dsInfo.Id != "ds_yibao" {
		t.Fatalf("GetDataSourceGRPC failed: %v", err)
	}

	// 15. TestConnectionGRPC gRPC 连通性测试探针
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
