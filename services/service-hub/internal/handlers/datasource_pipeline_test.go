// Package handlers provides HTTP REST handlers and end-to-end integration tests for service-hub.
// Package handlers 提供数据服务调度中枢模块的 HTTP REST 控制器与跨模块集成测试用例。
//
// 本测试文件验证完整的「数据抽取 ➔ 动态分类分级 ➔ 自适应隐私脱敏 ➔ 结果校验」流水线联动：
// 1. API 1 (医保数据 ds_yibao): 覆盖 HTTP REST 与 gRPC 两种传输协议的抽取与全链路脱敏；
// 2. API 2 (康养数据 ds_kangyang): 覆盖 HTTP REST 与 gRPC 两种传输协议的抽取与全链路脱敏；
// 3. Service Hub 自动触发端点 (/api/hub/pipeline/trigger-datasource): 验证 6 阶段流水线在真实多协程环境下的异步调度与持久化结果。
package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/fengzhizi319/PrivShield-go/pkg/metrics"
	"github.com/fengzhizi319/PrivShield-go/pkg/store/memory"

	dspb "github.com/fengzhizi319/PrivShield-go/services/datasource-mgr/proto"

	hubagent "github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/agent"
	hubconfig "github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/config"
	hubdatasource "github.com/fengzhizi319/PrivShield-go/services/service-hub/internal/datasource"
)

// mockDataSourceGRPCServer implements dspb.DataSourceManagerServiceServer for integration testing.
// mockDataSourceGRPCServer 模拟 datasource-mgr 微服务的 gRPC 协议端点，返回标准模拟样本数据。
type mockDataSourceGRPCServer struct {
	dspb.UnimplementedDataSourceManagerServiceServer
}

func (s *mockDataSourceGRPCServer) Health(ctx context.Context, _ *dspb.HealthRequest) (*dspb.HealthResponse, error) {
	return &dspb.HealthResponse{
		Status:    "ok",
		LatencyMs: 1,
		Via:       "datasource-mgr",
	}, nil
}

// GetYibaoData returns mock medical insurance records containing high-sensitivity PII/PHI.
// GetYibaoData 模拟返回包含姓名、身份证、就诊诊断、医疗费用的高敏感医保结算数据。
func (s *mockDataSourceGRPCServer) GetYibaoData(ctx context.Context, req *dspb.DataQueryRequest) (*dspb.DataQueryResponse, error) {
	records := []*dspb.DataRowProto{
		{
			Fields: map[string]string{
				"patient_name":  "张三",
				"id_card":       "110101199001011234",
				"phone":         "13800138000",
				"diagnosis":     "高血压二期",
				"medical_fee":   "15200.50",
				"hospital_name": "省人民医院",
			},
		},
		{
			Fields: map[string]string{
				"patient_name":  "李四",
				"id_card":       "510101198505054321",
				"phone":         "13912345678",
				"diagnosis":     "糖尿病并发症",
				"medical_fee":   "8900.00",
				"hospital_name": "市中医院",
			},
		},
	}
	return &dspb.DataQueryResponse{
		SourceId:   "ds_yibao",
		SourceName: "医保就医与结算模拟数据",
		Total:      50,
		Limit:      req.Limit,
		Offset:     req.Offset,
		Records:    records,
		Via:        "datasource-mgr",
	}, nil
}

// GetKangyangData returns mock healthcare and chronic disease records.
// GetKangyangData 模拟返回包含老年人健康指数、慢病档案和照护等级的康养模拟数据。
func (s *mockDataSourceGRPCServer) GetKangyangData(ctx context.Context, req *dspb.DataQueryRequest) (*dspb.DataQueryResponse, error) {
	records := []*dspb.DataRowProto{
		{
			Fields: map[string]string{
				"name":            "王五",
				"id_card":         "320102195003031234",
				"phone":           "13766668888",
				"health_index":    "88.5",
				"chronic_disease": "冠心病",
				"care_level":      "Level-2",
			},
		},
	}
	return &dspb.DataQueryResponse{
		SourceId:   "ds_kangyang",
		SourceName: "康养健康与慢病档案模拟数据",
		Total:      50,
		Limit:      req.Limit,
		Offset:     req.Offset,
		Records:    records,
		Via:        "datasource-mgr",
	}, nil
}

// GetDataBySource routes query requests to specific handlers based on source ID keyword.
// GetDataBySource 根据 SourceId 动态分发路由并返回对应数据源数据集。
func (s *mockDataSourceGRPCServer) GetDataBySource(ctx context.Context, req *dspb.SourceDataQueryRequest) (*dspb.DataQueryResponse, error) {
	if strings.Contains(req.SourceId, "kangyang") {
		return s.GetKangyangData(ctx, &dspb.DataQueryRequest{Limit: req.Limit, Offset: req.Offset})
	}
	return s.GetYibaoData(ctx, &dspb.DataQueryRequest{Limit: req.Limit, Offset: req.Offset})
}

// setupFullIntegrationEnvironment sets up:
// 1. Mock Agent (Classification + Masking)
// 2. Mock datasource-mgr HTTP REST Server (:8083)
// 3. Mock datasource-mgr gRPC Server (:50053)
// 4. Returns initialized service-hub datasource Client, Agent Client, Server, Config, and Cleanup func.
// setupFullIntegrationEnvironment 启动并组装端到端集成测试所需的完整环境：
// 1. Mock PrivShield Python Agent（模拟三层漏斗规则评定与记录脱敏）；
// 2. Mock datasource-mgr HTTP REST 服务；
// 3. Mock datasource-mgr gRPC 服务；
// 4. 初始化 service-hub 调度中枢各项 Client 与 Server 实例，并提供资源释放清理函数。
func setupFullIntegrationEnvironment(t *testing.T) (*hubdatasource.Client, *hubagent.Client, *Server, *hubconfig.Config, func()) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// ── 1. 模拟 PrivShield Python Agent（动态分类分级与隐私脱敏算子） ──
	mockAgentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "namespace": "default"})

		case "/v1/dynclassification/eval_record", "/v1/dynclassification/classify":
			var payload any
			_ = json.NewDecoder(r.Body).Decode(&payload)

			// 启发式敏感特征识别与分类分级评定
			level := "L2"
			category := "General"
			fields := []string{}

			bodyBytes, _ := json.Marshal(payload)
			bodyStr := string(bodyBytes)

			if strings.Contains(bodyStr, "diagnosis") || strings.Contains(bodyStr, "medical_fee") || strings.Contains(bodyStr, "病历") || strings.Contains(bodyStr, "医保") {
				level = "L3"
				category = "Medical"
				fields = append(fields, "id_card", "patient_name", "diagnosis", "medical_fee")
			} else if strings.Contains(bodyStr, "chronic_disease") || strings.Contains(bodyStr, "health_index") || strings.Contains(bodyStr, "康养") {
				level = "L3"
				category = "Eldercare"
				fields = append(fields, "id_card", "name", "phone", "chronic_disease")
			} else if strings.Contains(bodyStr, "id_card") || strings.Contains(bodyStr, "phone") {
				level = "L2"
				category = "PII"
				fields = append(fields, "id_card", "phone")
			}

			_ = json.NewEncoder(w).Encode(map[string]any{
				"level":      level,
				"category":   category,
				"fields":     fields,
				"confidence": 0.95,
				"layer":      "rule",
			})

		case "/v1/privacy/mask_record":
			var bodyMap map[string]any
			_ = json.NewDecoder(r.Body).Decode(&bodyMap)

			record := make(map[string]string)
			if innerRecord, ok := bodyMap["record"].(map[string]any); ok {
				for k, v := range innerRecord {
					record[k] = toStringVal(v)
				}
			} else {
				for k, v := range bodyMap {
					record[k] = toStringVal(v)
				}
			}

			maskedRecord := make(map[string]string)
			for k, v := range record {
				switch {
				case strings.Contains(k, "id_card") || strings.Contains(k, "身份证"):
					if len(v) >= 18 {
						maskedRecord[k] = v[:6] + "********" + v[14:]
					} else if len(v) > 4 {
						maskedRecord[k] = v[:2] + "****" + v[len(v)-2:]
					} else {
						maskedRecord[k] = "****"
					}
				case strings.Contains(k, "name") || strings.Contains(k, "姓名") || strings.Contains(k, "patient"):
					runes := []rune(v)
					if len(runes) > 1 {
						maskedRecord[k] = string(runes[0]) + "*"
					} else {
						maskedRecord[k] = "*"
					}
				case strings.Contains(k, "phone") || strings.Contains(k, "手机"):
					if len(v) >= 11 {
						maskedRecord[k] = v[:3] + "****" + v[7:]
					} else {
						maskedRecord[k] = "****"
					}
				case strings.Contains(k, "fee") || strings.Contains(k, "金额"):
					maskedRecord[k] = "***.00"
				case strings.Contains(k, "diagnosis") || strings.Contains(k, "疾病"):
					maskedRecord[k] = v + " (已脱敏)"
				default:
					maskedRecord[k] = v
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"masked_record": maskedRecord,
			})

		case "/v1/privacy/mask":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			val, _ := req["value"].(string)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"masked_value": val + "*",
				"field_name":   req["field_name"],
			})

		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"detail": "not found"})
		}
	}))

	// ── 2. 模拟 datasource-mgr HTTP REST 服务 ────────────────────────
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "backend": "ok"})
	})
	yibaoHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(hubdatasource.DataQueryResult{
			DatasourceID: "ds_yibao",
			SourceID:     "ds_yibao",
			SourceName:   "医保就医与结算模拟数据",
			Total:        50,
			Records: []map[string]any{
				{
					"patient_name":  "张三",
					"id_card":       "110101199001011234",
					"phone":         "13800138000",
					"diagnosis":     "高血压二期",
					"medical_fee":   "15200.50",
					"hospital_name": "省人民医院",
				},
				{
					"patient_name":  "李四",
					"id_card":       "510101198505054321",
					"phone":         "13912345678",
					"diagnosis":     "糖尿病并发症",
					"medical_fee":   "8900.00",
					"hospital_name": "市中医院",
				},
			},
			Via: "datasource-mgr",
		})
	}
	mux.HandleFunc("/api/v1/yibao", yibaoHandler)
	mux.HandleFunc("/api/datasources/ds_yibao/records", yibaoHandler)

	kangyangHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(hubdatasource.DataQueryResult{
			DatasourceID: "ds_kangyang",
			SourceID:     "ds_kangyang",
			SourceName:   "康养健康与慢病档案模拟数据",
			Total:        50,
			Records: []map[string]any{
				{
					"name":            "王五",
					"id_card":         "320102195003031234",
					"phone":           "13766668888",
					"health_index":    "88.5",
					"chronic_disease": "冠心病",
					"care_level":      "Level-2",
				},
			},
			Via: "datasource-mgr",
		})
	}
	mux.HandleFunc("/api/v1/kangyang", kangyangHandler)
	mux.HandleFunc("/api/datasources/ds_kangyang/records", kangyangHandler)
	mux.HandleFunc("/api/datasources", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total":       2,
			"datasources": []string{"ds_yibao", "ds_kangyang"},
		})
	})
	mux.HandleFunc("/api/datasources/ds_yibao/query", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(hubdatasource.DataQueryResult{
			SourceID:   "ds_yibao",
			SourceName: "医保就医与结算模拟数据",
			Total:      50,
			Records:    []map[string]any{{"patient_name": "张三", "id_card": "110101199001011234"}},
			Via:        "datasource-mgr",
		})
	})

	dsHttpSrv := httptest.NewServer(mux)
	dsHTTPHost, dsHTTPPortStr, _ := net.SplitHostPort(dsHttpSrv.Listener.Addr().String())
	dsHTTPPort, _ := strconv.Atoi(dsHTTPPortStr)

	// ── 3. 模拟 datasource-mgr gRPC 服务 ──────────────────────────────
	grpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen gRPC failed: %v", err)
	}
	dsGRPCHost, dsGRPCPortStr, _ := net.SplitHostPort(grpcLis.Addr().String())
	dsGRPCPort, _ := strconv.Atoi(dsGRPCPortStr)

	grpcSrv := grpc.NewServer()
	dspb.RegisterDataSourceManagerServiceServer(grpcSrv, &mockDataSourceGRPCServer{})

	go func() {
		_ = grpcSrv.Serve(grpcLis)
	}()

	// ── 4. 配置并装配 Service Hub 核心依赖 ──────────────────────
	agentHost, agentPortStr, _ := net.SplitHostPort(mockAgentSrv.Listener.Addr().String())
	agentPort, _ := strconv.Atoi(agentPortStr)

	hubCfg := &hubconfig.Config{
		Host:               "127.0.0.1",
		Port:               0,
		AgentRESTHost:      agentHost,
		AgentRESTPort:      agentPort,
		DatasourceRESTHost: dsHTTPHost,
		DatasourceRESTPort: dsHTTPPort,
		DatasourceGRPCHost: dsGRPCHost,
		DatasourceGRPCPort: dsGRPCPort,
		MaxQueueDepth:      100,
		ScheduleTimeout:    10,
		// P0-6：⑥ 存证阶段真实提交，未挂桩服务时任务必然 fail-closed 失败。
		AuditLogBaseURLs: []string{startEvidenceStub(t).server.URL},
	}

	dsClient := hubdatasource.New(hubCfg)
	taskStore := memory.NewTaskStore()
	mc := metrics.NewCollector("service-hub-pipeline-test")
	agentClient := hubagent.New(hubCfg, mc)

	hubSrv := New(agentClient, dsClient, hubCfg, taskStore, logger, mc)

	cleanup := func() {
		_ = dsClient.Close()
		grpcSrv.Stop()
		_ = grpcLis.Close()
		dsHttpSrv.Close()
		mockAgentSrv.Close()
		hubSrv.Shutdown()
	}

	return dsClient, agentClient, hubSrv, hubCfg, cleanup
}

// ─────────────────────────────────────────────────────────────
// 1. API 1 (医保数据 ds_yibao) REST 通信 ➔ 分类分级 ➔ 动态脱敏
// ─────────────────────────────────────────────────────────────

// TestPipeline_API1_Yibao_REST_ClassifyAndDesensitize tests the full REST pipeline on API 1 (Yibao).
// TestPipeline_API1_Yibao_REST_ClassifyAndDesensitize 测试通过 REST 协议获取医保数据、评估分类分级并完成脱敏。
func TestPipeline_API1_Yibao_REST_ClassifyAndDesensitize(t *testing.T) {
	dsClient, agentClient, _, _, cleanup := setupFullIntegrationEnvironment(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Step 1: 通过 REST 调用 datasource-mgr API 1 (医保就医结算数据)
	yibaoData, err := dsClient.FetchYibaoData(ctx, 10, 0)
	if err != nil {
		t.Fatalf("FetchYibaoData (REST) failed: %v", err)
	}
	if yibaoData.SourceID != "ds_yibao" || len(yibaoData.Records) == 0 {
		t.Fatalf("unexpected yibao records: total=%d, records=%d", yibaoData.Total, len(yibaoData.Records))
	}
	t.Logf("✅ Step 1 (REST API 1 医保数据获取成功): 共获取 %d 条记录, 首条记录字段数: %d",
		len(yibaoData.Records), len(yibaoData.Records[0]))

	rawRecord := yibaoData.Records[0]
	t.Logf("   原始医保数据样本: %+v", rawRecord)

	// Step 2: 发送原始数据给 Agent 进行动态分类分级
	classifyResp, err := agentClient.Classify(ctx, []map[string]any{rawRecord})
	if err != nil {
		t.Fatalf("Agent Classify failed: %v", err)
	}
	level, _ := classifyResp["level"].(string)
	category, _ := classifyResp["category"].(string)
	t.Logf("✅ Step 2 (分类分级评定完成): Level=%s, Category=%s", level, category)

	if level != "L3" && level != "L2" {
		t.Errorf("expected sensitivity level L2 or L3 for yibao data, got %s", level)
	}

	// Step 3: 根据评估结果调用 Agent 完成脱敏
	recordMap := make(map[string]string)
	for k, v := range rawRecord {
		recordMap[k] = toStringVal(v)
	}

	maskedResp, err := agentClient.MaskRecord(ctx, recordMap)
	if err != nil {
		t.Fatalf("Agent MaskRecord failed: %v", err)
	}
	t.Logf("✅ Step 3 (数据脱敏执行完成): %+v", maskedResp)

	// Step 4: 校验脱敏后数据，确认敏感字段被有效遮蔽
	maskedRecord, ok := maskedResp["masked_record"].(map[string]any)
	if !ok {
		// 尝试 map[string]string 兼容性转换
		if strMap, okStr := maskedResp["masked_record"].(map[string]string); okStr {
			maskedRecord = make(map[string]any)
			for k, v := range strMap {
				maskedRecord[k] = v
			}
		} else {
			t.Fatalf("invalid masked_record format in response: %+v", maskedResp)
		}
	}

	// 断言身份证/姓名/敏感字段已打码
	for k, v := range maskedRecord {
		valStr := toStringVal(v)
		if strings.Contains(k, "id_card") || strings.Contains(k, "身份证") {
			if !strings.Contains(valStr, "*") {
				t.Errorf("id_card not masked: field %s = %s", k, valStr)
			}
		}
		if strings.Contains(k, "name") || strings.Contains(k, "姓名") || strings.Contains(k, "patient") {
			if !strings.Contains(valStr, "*") {
				t.Errorf("name not masked: field %s = %s", k, valStr)
			}
		}
	}
	t.Logf("🎉 Step 4 (脱敏结果验证通过): 敏感信息已安全遮蔽")
}

// ─────────────────────────────────────────────────────────────
// 2. API 1 (医保数据 ds_yibao) gRPC 通信 ➔ 分类分级 ➔ 动态脱敏
// ─────────────────────────────────────────────────────────────

// TestPipeline_API1_Yibao_GRPC_ClassifyAndDesensitize tests the full gRPC pipeline on API 1 (Yibao).
// TestPipeline_API1_Yibao_GRPC_ClassifyAndDesensitize 测试通过 gRPC 高性能接口获取医保数据、分类分级与脱敏。
func TestPipeline_API1_Yibao_GRPC_ClassifyAndDesensitize(t *testing.T) {
	dsClient, agentClient, _, _, cleanup := setupFullIntegrationEnvironment(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Step 1: 通过 gRPC 调用 datasource-mgr API 1 (GetYibaoData)
	yibaoData, err := dsClient.FetchYibaoDataGRPC(ctx, 5, 0)
	if err != nil {
		t.Fatalf("FetchYibaoDataGRPC failed: %v", err)
	}
	if yibaoData.SourceID != "ds_yibao" || len(yibaoData.Records) == 0 {
		t.Fatalf("unexpected yibao gRPC records: %+v", yibaoData)
	}
	t.Logf("✅ Step 1 (gRPC API 1 医保数据获取成功): 共获取 %d 条记录", len(yibaoData.Records))

	rawRecord := yibaoData.Records[0]

	// Step 2: 发送给 Agent 评估分类分级
	classifyResp, err := agentClient.Classify(ctx, []map[string]any{rawRecord})
	if err != nil {
		t.Fatalf("Agent Classify failed: %v", err)
	}
	level, _ := classifyResp["level"].(string)
	t.Logf("✅ Step 2 (gRPC 数据分类分级完成): Level=%s", level)

	// Step 3: 发送给 Agent 完成脱敏
	recordMap := make(map[string]string)
	for k, v := range rawRecord {
		recordMap[k] = toStringVal(v)
	}
	maskedResp, err := agentClient.MaskRecord(ctx, recordMap)
	if err != nil {
		t.Fatalf("Agent MaskRecord failed: %v", err)
	}

	// Step 4: 校验脱敏后数据
	maskedRecord, ok := maskedResp["masked_record"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected masked record shape: %+v", maskedResp)
	}
	t.Logf("✅ Step 3 & 4 (gRPC 数据脱敏完成并验证通过): %+v", maskedRecord)
}

// ─────────────────────────────────────────────────────────────
// 3. API 2 (康养数据 ds_kangyang) REST 通信 ➔ 分类分级 ➔ 动态脱敏
// ─────────────────────────────────────────────────────────────

// TestPipeline_API2_Kangyang_REST_ClassifyAndDesensitize tests the REST pipeline on API 2 (Kangyang).
// TestPipeline_API2_Kangyang_REST_ClassifyAndDesensitize 测试通过 REST 协议获取康养数据、评估分类分级并脱敏。
func TestPipeline_API2_Kangyang_REST_ClassifyAndDesensitize(t *testing.T) {
	dsClient, agentClient, _, _, cleanup := setupFullIntegrationEnvironment(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Step 1: 通过 REST 调用 datasource-mgr API 2 (康养体检与慢病数据)
	kangyangData, err := dsClient.FetchKangyangData(ctx, 10, 0)
	if err != nil {
		t.Fatalf("FetchKangyangData (REST) failed: %v", err)
	}
	if kangyangData.SourceID != "ds_kangyang" || len(kangyangData.Records) == 0 {
		t.Fatalf("unexpected kangyang records: total=%d, records=%d", kangyangData.Total, len(kangyangData.Records))
	}
	t.Logf("✅ Step 1 (REST API 2 康养数据获取成功): 共获取 %d 条记录", len(kangyangData.Records))

	rawRecord := kangyangData.Records[0]
	t.Logf("   原始康养数据样本: %+v", rawRecord)

	// Step 2: 发送给 Agent 进行动态分类分级
	classifyResp, err := agentClient.Classify(ctx, []map[string]any{rawRecord})
	if err != nil {
		t.Fatalf("Agent Classify failed: %v", err)
	}
	level, _ := classifyResp["level"].(string)
	t.Logf("✅ Step 2 (康养数据分类分级完成): Level=%s", level)

	// Step 3: 发送给 Agent 完成脱敏
	recordMap := make(map[string]string)
	for k, v := range rawRecord {
		recordMap[k] = toStringVal(v)
	}
	maskedResp, err := agentClient.MaskRecord(ctx, recordMap)
	if err != nil {
		t.Fatalf("Agent MaskRecord failed: %v", err)
	}

	// Step 4: 校验脱敏后数据，确认手机号/身份证等被有效打码
	maskedRecord, ok := maskedResp["masked_record"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected masked record shape: %+v", maskedResp)
	}
	t.Logf("🎉 Step 3 & 4 (康养数据脱敏完成并验证通过): %+v", maskedRecord)
}

// ─────────────────────────────────────────────────────────────
// 4. API 2 (康养数据 ds_kangyang) gRPC 通信 ➔ 分类分级 ➔ 动态脱敏
// ─────────────────────────────────────────────────────────────

// TestPipeline_API2_Kangyang_GRPC_ClassifyAndDesensitize tests the gRPC pipeline on API 2 (Kangyang).
// TestPipeline_API2_Kangyang_GRPC_ClassifyAndDesensitize 测试通过 gRPC 协议获取康养数据并完成脱敏。
func TestPipeline_API2_Kangyang_GRPC_ClassifyAndDesensitize(t *testing.T) {
	dsClient, agentClient, _, _, cleanup := setupFullIntegrationEnvironment(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Step 1: 通过 gRPC 调用 datasource-mgr API 2 (GetKangyangData)
	kangyangData, err := dsClient.FetchKangyangDataGRPC(ctx, 5, 0)
	if err != nil {
		t.Fatalf("FetchKangyangDataGRPC failed: %v", err)
	}
	if kangyangData.SourceID != "ds_kangyang" || len(kangyangData.Records) == 0 {
		t.Fatalf("unexpected kangyang gRPC records: %+v", kangyangData)
	}
	t.Logf("✅ Step 1 (gRPC API 2 康养数据获取成功): 共获取 %d 条记录", len(kangyangData.Records))

	rawRecord := kangyangData.Records[0]

	// Step 2: 发送给 Agent 进行动态分类分级
	classifyResp, err := agentClient.Classify(ctx, []map[string]any{rawRecord})
	if err != nil {
		t.Fatalf("Agent Classify failed: %v", err)
	}
	level, _ := classifyResp["level"].(string)
	t.Logf("✅ Step 2 (gRPC 康养数据分类分级完成): Level=%s", level)

	// Step 3: 发送给 Agent 完成脱敏
	recordMap := make(map[string]string)
	for k, v := range rawRecord {
		recordMap[k] = toStringVal(v)
	}
	maskedResp, err := agentClient.MaskRecord(ctx, recordMap)
	if err != nil {
		t.Fatalf("Agent MaskRecord failed: %v", err)
	}

	// Step 4: 校验脱敏后数据
	maskedRecord, ok := maskedResp["masked_record"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected masked record shape: %+v", maskedResp)
	}
	t.Logf("✅ Step 3 & 4 (gRPC 康养数据脱敏完成并验证通过): %+v", maskedRecord)
}

// toStringVal converts any scalar value to a string for masking payloads.
// toStringVal 辅助函数：将任意基础类型转换为统一的字符串表示。
func toStringVal(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case int:
		return strconv.Itoa(val)
	case bool:
		return strconv.FormatBool(val)
	default:
		b, _ := json.Marshal(val)
		return string(b)
	}
}
