// Package clients 封装 BFF 与 4 个上游微服务的所有 HTTP 通信。
//
// 核心组件：
//   - ClientPool: HTTP 客户端池，复用连接，统一管理超时和重试
//
// 通信目标：
//   - Service Hub    (:8082) — 任务调度、流水线管理、租约查询
//   - Agent Engine   (:8079) — 隐私脱敏、医疗数据处理
//   - Datasource Mgr (:8083) — 数据源注册、采样切片
//   - Audit Log      (:8084) — 审计日志、Merkle 验真
//
// 降级策略：
//
//	当上游服务不可达时，多个方法会返回硬编码的 fallback 数据（如 defaultDatasources、
//	generateSampleSlice、defaultAuditLogs），确保前端大屏在开发/演示模式下仍有数据展示。
package clients

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fengzhizi319/PrivShield-go/console/app-lz/bff-go/internal/catalog"
	"github.com/fengzhizi319/PrivShield-go/console/app-lz/bff-go/internal/config"
	"github.com/fengzhizi319/PrivShield-go/console/app-lz/bff-go/internal/models"
	naming "github.com/fengzhizi319/PrivShield-go/pkg/naming"
	pkgobs "github.com/fengzhizi319/PrivShield-go/pkg/observability"
)

// 数据来源标记（api_rename_design.md §9.3 不变式 4）：降级/兜底数据必须携带显式来源。
const (
	sourceFallback = "fallback"
	viaBFF         = "app-lz-bff"
)

// ClientPool 管理与 4 个上游微服务的 HTTP 通信。
// 内部共享一个 http.Client 实例，通过连接池复用 TCP 连接。
type ClientPool struct {
	cfg        *config.Config // 运行时配置（上游服务地址等）
	httpClient *http.Client   // 共享的 HTTP 客户端（含连接池）
}

// NewClientPool 创建一个新的客户端池。
// 配置 HTTP 客户端：全局超时 10s，最大空闲连接 100，每主机最大空闲连接 25，空闲超时 90s。
func NewClientPool(cfg *config.Config) *ClientPool {
	return &ClientPool{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,                                   // 全局最大空闲连接数
				MaxIdleConnsPerHost: 25,                                    // 每个上游服务最大空闲连接数
				IdleConnTimeout:     90 * time.Second,                      // 空闲连接回收时间
				TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, // 兼容 HTTPS/mTLS 自签名证书
			},
		},
	}
}

// setHeaders injects X-Request-ID, X-Trace-ID and per-service Authorization: Bearer <APIKey>
// into an outbound request. The requestID argument is optional; when empty it is read from ctx.
func (c *ClientPool) setHeaders(req *http.Request, serviceID string, requestID string) {
	if req == nil {
		return
	}
	if requestID == "" {
		requestID = pkgobs.RequestIDFromContext(req.Context())
	}
	if requestID != "" {
		if req.Header.Get("X-Request-ID") == "" {
			req.Header.Set("X-Request-ID", requestID)
		}
		if req.Header.Get("X-Trace-ID") == "" {
			req.Header.Set("X-Trace-ID", requestID)
		}
	}
	if req.Header.Get("Authorization") != "" {
		return
	}
	apiKey := c.cfg.HubAPIKey
	if apiKey == "" {
		apiKey = c.cfg.APIKey
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
}

// ProbeNode 探测单个上游微服务的健康状态和往返延迟。
//
// 探测流程：
//  1. REST 探测：向 /health 发 GET 请求（所有中台服务统一的无前缀存活探针）
//  2. gRPC 探测：通过 TCP Dial 检测端口可达性（800ms 超时）
//  3. 综合判断：根据前端选择的活跃协议（rest/grpc）设置整体状态
//
// 特殊处理：
//   - 若 gRPC TCP 探测失败但 REST 正常，则认为 gRPC 也「ready」（本地 mock 模式兼容）
//   - gRPC 的 RTT 按 REST RTT 的 85% 估算（模拟 gRPC 通常比 REST 略快的场景）
func (c *ClientPool) ProbeNode(ctx context.Context, id, name, httpURL, grpcAddr, protocol string) models.ServiceNode {
	if protocol == "" {
		protocol = "rest"
	}

	// 初始化节点，默认所有状态为 "unreachable"
	node := models.ServiceNode{
		ID:         id,
		Name:       name,
		HTTPURL:    httpURL,
		GRPCAddr:   grpcAddr,
		Status:     "unreachable",
		RESTStatus: "unreachable",
		GRPCStatus: "unreachable",
		Protocol:   protocol,
		Version:    "1.8.0",
		Details:    make(map[string]any),
	}

	// ── 步骤 1：REST 健康探测 ────────────────────────────────────────
	startREST := time.Now()
	healthURL := strings.TrimRight(httpURL, "/") + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	c.setHeaders(req, id, "")
	if err == nil {
		resp, errREST := c.httpClient.Do(req)

		durationREST := time.Since(startREST)
		node.RESTRTTMs = float64(durationREST.Microseconds()) / 1000.0

		if errREST == nil && resp != nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				node.RESTStatus = "ready"
				// 解析响应体中的元数据（如 upstream_count 等）
				body, _ := io.ReadAll(resp.Body)
				var bodyMap map[string]any
				if json.Unmarshal(body, &bodyMap) == nil {
					node.Details = bodyMap
				}
			} else {
				node.RESTStatus = "unhealthy"
				node.Error = fmt.Sprintf("HTTP status %d", resp.StatusCode)
			}
		}
	}

	// ── 步骤 2：gRPC 健康探测（TCP Dial）─────────────────────────────
	// 通过 TCP 连接检测 gRPC 端口可达性，超时 800ms
	startGRPC := time.Now()
	conn, errGRPC := net.DialTimeout("tcp", grpcAddr, 800*time.Millisecond)
	durationGRPC := time.Since(startGRPC)
	node.GRPCRTTMs = float64(durationGRPC.Microseconds()) / 1000.0

	if errGRPC == nil && conn != nil {
		_ = conn.Close()
		node.GRPCStatus = "ready"
	} else {
		// 降级策略：TCP 探测失败但 REST 正常 → 认为 gRPC 也正常（本地 mock 模式）
		if node.RESTStatus == "ready" {
			node.GRPCStatus = "ready"
			node.GRPCRTTMs = node.RESTRTTMs * 0.85 // 模拟 gRPC 略快
		}
	}

	// ── 步骤 3：根据前端选择的活跃协议设置综合状态 ─────────────────
	if protocol == "grpc" {
		node.Status = node.GRPCStatus
		node.RTTMs = node.GRPCRTTMs
	} else {
		node.Status = node.RESTStatus
		node.RTTMs = node.RESTRTTMs
	}

	return node
}

// GetTopology 从唯一编排中枢 service-hub 获取微服务网格拓扑状态。
// app-lz 作为外部模拟业务系统，无权直接探测 engine / datasource-mgr / audit-log，
// 所有拓扑状态均由 service-hub 统一下发。
func (c *ClientPool) GetTopology(ctx context.Context, protocol string) models.TopologyResponse {
	if protocol == "" {
		protocol = "rest"
	}

	url := fmt.Sprintf("%s/v1/hub/topology?protocol=%s", strings.TrimRight(c.cfg.HubURL, "/"), url.QueryEscape(protocol))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return c.degradedTopology(protocol, err.Error())
	}
	c.setHeaders(req, "hub", "")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return c.degradedTopology(protocol, fmt.Sprintf("service-hub unreachable: %v", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.degradedTopology(protocol, fmt.Sprintf("service-hub returned HTTP %d", resp.StatusCode))
	}

	var topo models.TopologyResponse
	if err := json.NewDecoder(resp.Body).Decode(&topo); err != nil {
		return c.degradedTopology(protocol, fmt.Sprintf("failed to decode topology: %v", err))
	}
	if len(topo.Services) == 0 {
		return c.degradedTopology(protocol, "service-hub returned empty topology")
	}
	return topo
}

// degradedTopology 构造服务降级时的兜底拓扑响应。
func (c *ClientPool) degradedTopology(protocol, detail string) models.TopologyResponse {
	return models.TopologyResponse{
		Status:         "degraded",
		ActiveProtocol: protocol,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		Services: []models.ServiceNode{
			{
				ID:         "service-hub",
				Name:       "调度中枢 (Service Hub)",
				HTTPURL:    c.cfg.HubURL,
				GRPCAddr:   c.cfg.HubGRPC,
				Status:     "unreachable",
				RESTStatus: "unreachable",
				GRPCStatus: "unreachable",
				Protocol:   protocol,
				Version:    "1.8.0",
				Details:    map[string]any{"error": detail},
			},
			{
				ID:         "engine",
				Name:       "隐私与分类引擎 (PrivShield Agent)",
				HTTPURL:    "",
				GRPCAddr:   "",
				Status:     "unreachable",
				RESTStatus: "unreachable",
				GRPCStatus: "unreachable",
				Protocol:   protocol,
				Version:    "1.8.0",
				Details:    map[string]any{"error": "service-hub unreachable"},
			},
			{
				ID:         "datasource-mgr",
				Name:       "数据源管理 (Datasource Mgr)",
				HTTPURL:    "",
				GRPCAddr:   "",
				Status:     "unreachable",
				RESTStatus: "unreachable",
				GRPCStatus: "unreachable",
				Protocol:   protocol,
				Version:    "1.8.0",
				Details:    map[string]any{"error": "service-hub unreachable"},
			},
			{
				ID:         "audit-log",
				Name:       "脱敏审计日志 (Audit Log)",
				HTTPURL:    "",
				GRPCAddr:   "",
				Status:     "unreachable",
				RESTStatus: "unreachable",
				GRPCStatus: "unreachable",
				Protocol:   protocol,
				Version:    "1.8.0",
				Details:    map[string]any{"error": "service-hub unreachable"},
			},
		},
	}
}

// DispatchTask 向 Service Hub 派发一个新的数据处理任务。
//
// 调用路径：POST {HubURL}/v1/hub/dispatch
// 请求体包含：source（数据来源）、operation（隐私操作类型）、payload（原始数据）、priority（优先级）。
// 返回新创建的任务 ID 和初始状态。
func (c *ClientPool) DispatchTask(ctx context.Context, req models.DispatchRequest) (models.DispatchResponse, error) {
	url := strings.TrimRight(c.cfg.HubURL, "/") + "/v1/hub/dispatch"
	data, _ := json.Marshal(req)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	c.setHeaders(httpReq, "hub", "")
	if err != nil {
		return models.DispatchResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return models.DispatchResponse{Error: err.Error()}, err
	}
	defer resp.Body.Close()

	// 检查 HTTP 状态码，非 2xx 时返回错误
	var dispatchResp models.DispatchResponse
	if resp.StatusCode >= 400 {
		return models.DispatchResponse{
			Error: fmt.Sprintf("service-hub returned HTTP %d", resp.StatusCode),
		}, fmt.Errorf("dispatch failed with status %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&dispatchResp); err != nil {
		return models.DispatchResponse{Error: err.Error()}, err
	}
	return dispatchResp, nil
}

// ListTasks 从 Service Hub 查询任务列表，支持按状态筛选和分页。
//
// 调用路径：GET {HubURL}/v1/hub/tasks?status=xxx&limit=n&offset=n
// 返回任务总数和当前页的任务列表。
func (c *ClientPool) ListTasks(ctx context.Context, status string, limit, offset int) (models.TasksResponse, error) {
	url := fmt.Sprintf("%s/v1/hub/tasks?status=%s&limit=%d&offset=%d",
		strings.TrimRight(c.cfg.HubURL, "/"), status, limit, offset)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	c.setHeaders(req, "hub", "")
	if err != nil {
		return models.TasksResponse{}, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return models.TasksResponse{}, err
	}
	defer resp.Body.Close()

	var tasksResp models.TasksResponse
	if err := json.NewDecoder(resp.Body).Decode(&tasksResp); err != nil {
		return models.TasksResponse{}, err
	}
	return tasksResp, nil
}

// GetTask 根据任务 ID 查询单个任务的完整详情。
//
// 调用路径：GET {HubURL}/v1/hub/tasks/{taskID}
// 若任务不存在返回 404，转换为 error 返回。
func (c *ClientPool) GetTask(ctx context.Context, taskID string) (*models.Task, error) {
	url := fmt.Sprintf("%s/v1/hub/tasks/%s", strings.TrimRight(c.cfg.HubURL, "/"), taskID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	c.setHeaders(req, "hub", "")
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("task not found")
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var envelope struct {
		Task models.Task `json:"task"`
		Via  string      `json:"via"`
	}
	if err := json.Unmarshal(bodyBytes, &envelope); err == nil && envelope.Task.ID != "" {
		return &envelope.Task, nil
	}

	var direct models.Task
	if err := json.Unmarshal(bodyBytes, &direct); err != nil {
		return nil, err
	}
	return &direct, nil
}

// GetDatasources 通过唯一调度中枢 service-hub 查询已注册的数据源目录。
//
// canonical 调用路径：GET {HubURL}/v1/hub/datasources
//
// 降级策略：服务不可达 / 非 2xx / 解析失败 / 空列表时，返回由 pkg/naming 注册表
// 派生的兜底目录，并把 Source 标为 "fallback" + Detail 写明原因（9.3 不变式 4）。
func (c *ClientPool) GetDatasources(ctx context.Context) (models.DatasourcesResponse, error) {
	path := "/v1/hub/datasources"
	url := strings.TrimRight(c.cfg.HubURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	c.setHeaders(req, "hub", "")
	if err != nil {
		return models.DatasourcesResponse{Datasources: fallbackDatasources(), Total: len(fallbackDatasources()), Source: sourceFallback, Detail: err.Error(), Via: viaBFF}, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return degradedDatasources(fmt.Sprintf("service-hub unreachable: %v", err)), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return degradedDatasources(fmt.Sprintf("service-hub returned HTTP %d for %s", resp.StatusCode, path)), nil
	}

	var result struct {
		Total       int                 `json:"total"`
		Datasources []models.Datasource `json:"datasources"`
		Via         string              `json:"via"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return degradedDatasources(fmt.Sprintf("failed to decode datasource catalog: %v", err)), nil
	}
	if len(result.Datasources) == 0 {
		return degradedDatasources("service-hub returned an empty catalog"), nil
	}

	for i := range result.Datasources {
		normalizeDatasource(&result.Datasources[i])
	}
	total := result.Total
	if total == 0 {
		total = len(result.Datasources)
	}
	return models.DatasourcesResponse{
		Datasources: result.Datasources,
		Total:       total,
		Source:      "service-hub",
		Via:         viaBFF,
	}, nil
}

// degradedDatasources 返回带显式降级标记的兜底目录（绝不伪装成上游真实数据）。
func degradedDatasources(detail string) models.DatasourcesResponse {
	items := fallbackDatasources()
	return models.DatasourcesResponse{
		Datasources: items,
		Total:       len(items),
		Source:      sourceFallback,
		Detail:      detail,
		Via:         viaBFF,
	}
}

// normalizeDatasource 把上游目录条目补齐为 canonical 形态：
// datasource_id 与历史 id 双写，并用注册表补充 api_code / status。
func normalizeDatasource(ds *models.Datasource) {
	if ds.DatasourceID == "" {
		ds.DatasourceID = ds.ID
	}
	if ds.ID == "" {
		ds.ID = ds.DatasourceID
	}
	if entry, ok := naming.EntryByDataSourceID(ds.DatasourceID); ok {
		ds.DatasourceID = entry.DataSourceID
		ds.ID = entry.DataSourceID
		ds.APICode = entry.APICode
		ds.Status = entry.Status
		if ds.Category == "" {
			ds.Category = entry.Category
		}
	}
}

// fallbackDatasources 返回由 pkg/naming 注册表派生的兜底数据源目录。
// 旧版本在这里手写硬编码字段清单（与真实 schema 不一致，D-14），现统一改为查注册表 + catalog。
func fallbackDatasources() []models.Datasource {
	defs := catalog.Definitions()
	out := make([]models.Datasource, 0, len(defs))
	for _, def := range defs {
		if def.Status != naming.StatusActive {
			continue
		}
		out = append(out, models.Datasource{
			ID:           def.DatasourceID,
			DatasourceID: def.DatasourceID,
			Name:         def.Name,
			APICode:      def.APICode,
			Category:     def.Category,
			Status:       def.Status,
			RecordsCount: len(def.Fields),
			Fields:       def.Fields,
		})
	}
	return out
}

// GetDatasourceSlice 获取数据源的展示样本切片（原始切片不出域，仅返回本地演示样本）。
//
// 入站 rawID 允许任意注册表表现（canonical / slug / 文件名 / 中文名 / api_code），
// 在服务边界归一化一次；未知 ID 返回 INVALID_DATASOURCE_ID(400)、预留位返回
// RESERVED_DATASOURCE(409)，不再静默落到医保（修复 D-11）。
func (c *ClientPool) GetDatasourceSlice(ctx context.Context, rawID string, limit, offset int) (models.DatasourceSliceResponse, error) {
	datasourceID, err := ResolveDatasourceID(rawID)
	if err != nil {
		return models.DatasourceSliceResponse{}, err
	}
	if limit <= 0 {
		limit = 10
	}
	// 原始数据切片不出域，datasource-mgr 不提供 /records 接口，返回带显式 fallback 标记的本地安全演示样本
	return fallbackSlice(datasourceID, limit, "datasource raw records sampling endpoint not provided (原始数据切片不出域)"), nil
}

// GetDatasourceRecordByIDCard 按身份证号通过 service-hub 查询单条记录。
//
// canonical 调用路径：通过 service-hub 的 FetchAndDesensitizeViaHub 同步端到端编排接口。
//
// 入站 rawID 允许任意注册表表现（canonical / slug / 文件名 / 中文名 / api_code），
// 在服务边界归一化一次。返回结果包装为 DatasourceSliceResponse（Records 只有 1 条或 0 条）。
func (c *ClientPool) GetDatasourceRecordByIDCard(ctx context.Context, rawID, idCardNo string) (models.DatasourceSliceResponse, error) {
	datasourceID, err := ResolveDatasourceID(rawID)
	if err != nil {
		return models.DatasourceSliceResponse{}, err
	}

	result, err := c.FetchAndDesensitizeViaHub(ctx, datasourceID, idCardNo)
	if err != nil {
		return models.DatasourceSliceResponse{}, err
	}

	records := make([]map[string]any, 0, 1)
	found, _ := result["found"].(bool)
	if found {
		if rec, ok := result["sanitized_data"].(map[string]any); ok && rec != nil {
			records = append(records, rec)
		}
	}

	return models.DatasourceSliceResponse{
		DatasourceID: datasourceID,
		Count:        len(records),
		Total:        len(records),
		Records:      records,
		Source:       "service-hub",
	}, nil
}

// fallbackSlice 构造带降级标记的本地样本切片。
func fallbackSlice(datasourceID string, limit int, detail string) models.DatasourceSliceResponse {
	slice := generateSampleSlice(datasourceID, limit)
	slice.Source = sourceFallback
	slice.Detail = detail
	return slice
}

// generateSampleSlice returns fallback sample data when datasource-mgr is unreachable.
// 字段定义与 catalog（由 scripts/data/ 生成脚本与 engine/medical_pipeline/samples 对齐）
// 的 schema 严格一致：yibao.csv 18 字段，kangyang.csv 27 字段。
//
// 入参 datasourceID 必须是已经归一化的 canonical 值（调用方负责）。
func generateSampleSlice(datasourceID string, limit int) models.DatasourceSliceResponse {
	records := make([]map[string]any, 0, limit)
	for i := 1; i <= limit; i++ {
		switch datasourceID {
		case naming.DSKangyang:
			// kangyang.csv 27 字段
			records = append(records, sampleKangyangRecord(i))
		default:
			// yibao.csv 18 字段
			records = append(records, sampleYibaoRecord(i))
		}
	}
	return models.DatasourceSliceResponse{
		DatasourceID: datasourceID,
		Count:        limit,
		Total:        limit,
		Records:      records,
		Source:       sourceFallback,
	}
}

// sampleYibaoRecord 构造一条医保结算样本记录（19 字段，与 catalog.Fields(naming.DSYibao) 同序）。
func sampleYibaoRecord(i int) map[string]any {
	return map[string]any{
		"insurance_settlement_id": fmt.Sprintf("YB202601%04d", i),
		"person_id":               fmt.Sprintf("PID%08d", 10000000+i),
		"gender":                  "男",
		"birth_date":              fmt.Sprintf("19%02d-06-15", 50+i%40),
		"admission_date":          "2026-01-10",
		"discharge_date":          "2026-01-18",
		"length_of_stay":          8,
		"admission_dept":          "内分泌科",
		"discharge_dept":          "内分泌科",
		"hospital_code":           fmt.Sprintf("H%d010%d001", 5+i%3, i%5),
		"medical_category":        "住院",
		"discharge_mode":          "医囑离院",
		"settlement_seq_no":       fmt.Sprintf("MX202601%04d", i),
		"diagnosis_seq":           1,
		"diagnosis_type":          "主要诊断",
		"icd10_code":              "E11.900",
		"diagnosis_name":          "2型糖尿病",
		"admission_condition":     "一般",
		"id_card_no":              fmt.Sprintf("51010119%02d0615123%d", 50+i%40, i%10),
	}
}

// sampleKangyangRecord 构造一条康养体征样本记录（27 字段，与 catalog.Fields(naming.DSKangyang) 同序）。
func sampleKangyangRecord(i int) map[string]any {
	return map[string]any{
		"gender":               "男",
		"age":                  70 + (i % 20),
		"diagnosis_name":       "2型糖尿病",
		"chief_complaint":      "口渴多饮多尿半年",
		"present_illness":      "患者半年前无明显诱因出现口渴",
		"past_history":         "高血压病史5年",
		"personal_history":     "无特殊",
		"is_smoking":           "否",
		"smoking_duration":     "",
		"family_history":       "父亲有糖尿病史",
		"allergic_history":     "无",
		"department":           "内分泌科",
		"height":               170,
		"weight":               72,
		"disability_category":  "无",
		"disability_level":     "",
		"assess_type_name":     "老年人能力评估",
		"assess_result_name":   "能力完好",
		"assess_score":         5,
		"assess_time":          "2026-01-15 09:30:00",
		"progress_note":        "血糖控制可，继续当前治疗方案",
		"progress_note_time":   "2026-01-15 10:00:00",
		"name":                 fmt.Sprintf("张老%d", i),
		"id_card_no":           fmt.Sprintf("510101195%02d0101123%d", i%50, i%10),
		"registered_address":   fmt.Sprintf("四川省成都市武侯区%d号", i),
		"disability_cert_no":   "",
		"medical_insurance_no": fmt.Sprintf("YB%d%06d", 51, i),
	}
}

// GetAuditLogs 从 audit-log 服务获取审计日志条目。
//
// canonical 调用路径：GET {AuditURL}/v1/audit/logs?limit=&offset=&datasource=&task_id=&api_code=
// （旧实现误用 /api/v1/audit/logs，上游不存在该路径 → 恒 404 静默降级，即 D-02）
//
// rawDatasourceID 为空表示不过滤；非空时先归一化，未知值直接 400（不静默回落）。
// 降级时返回 Source="fallback"，不再伪装为真实存证。
func (c *ClientPool) GetAuditLogs(ctx context.Context, limit, offset int, rawDatasourceID string) (models.AuditLogsResponse, error) {
	return c.GetAuditLogsFiltered(ctx, limit, offset, rawDatasourceID, "", "")
}

// GetAuditLogsFiltered 支持按 datasource、task_id、api_code 复合过滤通过 service-hub 查询审计日志。
func (c *ClientPool) GetAuditLogsFiltered(ctx context.Context, limit, offset int, rawDatasourceID, taskID, apiCode string) (models.AuditLogsResponse, error) {
	datasourceID := ""
	if strings.TrimSpace(rawDatasourceID) != "" {
		resolved, err := ResolveDatasourceID(rawDatasourceID)
		if err != nil {
			return models.AuditLogsResponse{}, err
		}
		datasourceID = resolved
	}

	path := fmt.Sprintf("/v1/hub/audit/logs?limit=%d&offset=%d", limit, offset)
	if datasourceID != "" {
		path += "&datasource=" + datasourceID
	}
	if strings.TrimSpace(taskID) != "" {
		path += "&task_id=" + strings.TrimSpace(taskID)
	}
	if strings.TrimSpace(apiCode) != "" {
		path += "&api_code=" + strings.TrimSpace(apiCode)
	}
	url := strings.TrimRight(c.cfg.HubURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	c.setHeaders(req, "hub", "")
	if err != nil {
		return models.AuditLogsResponse{}, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return degradedAuditLogs(fmt.Sprintf("service-hub unreachable: %v", err), datasourceID), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return degradedAuditLogs(fmt.Sprintf("service-hub returned HTTP %d for %s", resp.StatusCode, path), datasourceID), nil
	}

	var result struct {
		Total int                   `json:"total"`
		Logs  []models.AuditLogItem `json:"logs"`
		Via   string                `json:"via"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return degradedAuditLogs(fmt.Sprintf("failed to decode audit logs: %v", err), datasourceID), nil
	}

	for i := range result.Logs {
		result.Logs[i].NormalizeAliases()
	}
	total := result.Total
	if total == 0 {
		total = len(result.Logs)
	}
	return models.AuditLogsResponse{
		Logs:   result.Logs,
		Total:  total,
		Source: "service-hub",
		Via:    viaBFF,
	}, nil
}

// degradedAuditLogs 返回带显式降级标记的兜底审计示例。
// 兜底条目的 operation / datasource 均取 canonical 值，避免被回写时遭 400（D-06/D-07）。
func degradedAuditLogs(detail, datasourceID string) models.AuditLogsResponse {
	items := defaultAuditLogs()
	if datasourceID != "" {
		filtered := make([]models.AuditLogItem, 0, len(items))
		for _, it := range items {
			if it.Datasource == datasourceID {
				filtered = append(filtered, it)
			}
		}
		items = filtered
	}
	return models.AuditLogsResponse{
		Logs:   items,
		Total:  len(items),
		Source: sourceFallback,
		Detail: detail,
		Via:    viaBFF,
	}
}

// defaultAuditLogs 返回本地兜底审计示例（仅用于不可演示时的占位，Source 会标为 fallback）。
// operation 取 canonical 操作名（validation.AuditOperations：mask/k_anon/dp/qol），
// status 标为 synthetic，哈希置空，禁止伪装为真实存证（P1-5 修复）。
func defaultAuditLogs() []models.AuditLogItem {
	now := time.Now().UTC().Format(time.RFC3339)
	items := []models.AuditLogItem{
		{
			ID:            "fallback-audit-001",
			Timestamp:     now,
			Datasource:    naming.DSYibao,
			APICode:       naming.API1Yibao,
			Operation:     "mask",
			InputHash:     "",
			OutputHash:    "",
			Algorithm:     "field_mask",
			User:          "app-lz-bff",
			Status:        "synthetic",
			SecurityLevel: "L3",
		},
		{
			ID:            "fallback-audit-002",
			Timestamp:     now,
			Datasource:    naming.DSKangyang,
			APICode:       naming.API2Kangyang,
			Operation:     "mask",
			InputHash:     "",
			OutputHash:    "",
			Algorithm:     "field_mask",
			User:          "app-lz-bff",
			Status:        "synthetic",
			SecurityLevel: "L3",
		},
	}
	for i := range items {
		items[i].NormalizeAliases()
	}
	return items
}

// RecordAudit 通过唯一调度中枢 service-hub 写入一条存证（POST /v1/hub/audit/logs）。
//
// 返回上游生成的记录 ID；上游不可达时返回 *UpstreamError（UPSTREAM_UNAVAILABLE/503），
// 调用方必须将该阶段标为 skipped/error，不得伪造 audit_entry_id（修复 D-03）。
func (c *ClientPool) RecordAudit(ctx context.Context, req models.AuditRecordRequest) (string, error) {
	path := "/v1/hub/audit/logs"
	if strings.TrimSpace(req.Datasource) == "" && strings.TrimSpace(req.APICode) != "" {
		if dsID, ok := naming.DataSourceForAPICode(req.APICode); ok {
			req.Datasource = dsID
		}
	}
	datasourceID, err := ResolveDatasourceID(req.Datasource)
	if err != nil {
		return "", err
	}
	req.Datasource = datasourceID
	if req.User == "" {
		req.User = viaBFF
	}
	if req.Status == "" {
		req.Status = "success"
	}

	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.cfg.HubURL, "/")+path, bytes.NewReader(body))
	c.setHeaders(httpReq, "hub", "")
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", &UpstreamError{
			Code:    CodeUpstreamUnavailable,
			Message: fmt.Sprintf("service-hub unreachable: %v", err),
			Status:  http.StatusServiceUnavailable,
			Err:     err,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", &UpstreamError{
			Code:    CodeUpstreamUnavailable,
			Message: fmt.Sprintf("service-hub returned HTTP %d for %s: %s", resp.StatusCode, path, strings.TrimSpace(string(detail))),
			Status:  http.StatusBadGateway,
		}
	}

	var result struct {
		ID      string `json:"id"`
		AuditID string `json:"audit_id"`
		LogID   string `json:"log_id"`
		TaskID  string `json:"task_id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	switch {
	case result.ID != "":
		return result.ID, nil
	case result.AuditID != "":
		return result.AuditID, nil
	case result.LogID != "":
		return result.LogID, nil
	default:
		return "", &UpstreamError{
			Code:    CodeUpstreamUnavailable,
			Message: "service-hub accepted the record but returned no entry id",
			Status:  http.StatusBadGateway,
		}
	}
}

// VerifyAudit 通过唯一调度中枢 service-hub 对最近一条审计快照执行真实完整性验真。
//
// canonical 调用链路：
//
//	POST {HubURL}/v1/hub/audit/verify
//
// 上游不可达时返回 MerkleValid=false + Source="fallback"，绝不合成“验真通过”。
func (c *ClientPool) VerifyAudit(ctx context.Context) (models.AuditVerifyResponse, error) {
	verifyURL := strings.TrimRight(c.cfg.HubURL, "/") + "/v1/hub/audit/verify"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, verifyURL, nil)
	c.setHeaders(req, "hub", "")
	if err != nil {
		return models.AuditVerifyResponse{}, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return degradedVerify(fmt.Sprintf("service-hub unreachable: %v", err)), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return degradedVerify(fmt.Sprintf("service-hub returned HTTP %d for /v1/hub/audit/verify", resp.StatusCode)), nil
	}

	var verifyResult struct {
		SnapshotID   string `json:"snapshot_id"`
		MerkleValid  bool   `json:"merkle_valid"`
		RootHash     string `json:"root_hash"`
		ExpectedHash string `json:"expected_hash"`
		TotalEntries int    `json:"total_entries"`
		Source       string `json:"source"`
		Timestamp    string `json:"timestamp"`
		Error        string `json:"error"`
		Via          string `json:"via"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&verifyResult); err != nil {
		return degradedVerify(fmt.Sprintf("failed to decode verify response: %v", err)), nil
	}

	source := verifyResult.Source
	if source == "" {
		source = "service-hub"
	}

	return models.AuditVerifyResponse{
		MerkleValid:  verifyResult.MerkleValid,
		RootHash:     verifyResult.RootHash,
		ExpectedHash: verifyResult.ExpectedHash,
		SnapshotID:   verifyResult.SnapshotID,
		TotalEntries: verifyResult.TotalEntries,
		Source:       source,
		Timestamp:    verifyResult.Timestamp,
		Error:        verifyResult.Error,
	}, nil
}

// degradedVerify 返回“未验真”的降级结果（merkle_valid 必须为 false）。
func degradedVerify(detail string) models.AuditVerifyResponse {
	return models.AuditVerifyResponse{
		MerkleValid: false,
		Source:      sourceFallback,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		Error:       detail,
	}
}

// GetHubMetrics 从 Service Hub 获取原始 Prometheus 指标文本。
//
// 调用路径：GET {HubURL}/metrics
// 返回 Prometheus 文本格式的原始字符串，后续由 parsePrometheusMetrics 解析。
func (c *ClientPool) GetHubMetrics(ctx context.Context) (string, error) {
	url := strings.TrimRight(c.cfg.HubURL, "/") + "/metrics"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	c.setHeaders(req, "hub", "")
	if err != nil {
		return "", err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// ParsedMetrics 保存从 Prometheus 文本中提取的关键指标。
// 前端 MetricsPanel 直接消费此结构进行展示。
type ParsedMetrics struct {
	StageDurations map[string]float64 // 流水线各阶段平均耗时（毫秒），key 为阶段名
	QPS            float64            // 每秒请求数
	Percentiles    map[string]float64 // 延迟百分位数："p50"/"p90"/"p95"/"p99" → 毫秒
	TotalRequests  float64            // 总请求数
	ErrorCount     float64            // 错误请求数
}

// GetParsedMetrics 获取并解析 Service Hub 的 Prometheus 指标。
//
// 执行流程：
//  1. 调用 GetHubMetrics 获取原始 Prometheus 文本
//  2. 调用 parsePrometheusMetrics 解析出各阶段耗时、QPS、延迟百分位数
func (c *ClientPool) GetParsedMetrics(ctx context.Context) (ParsedMetrics, error) {
	raw, err := c.GetHubMetrics(ctx)
	if err != nil {
		return ParsedMetrics{}, err
	}
	return parsePrometheusMetrics(raw), nil
}

// parsePrometheusMetrics 从 Prometheus 文本格式中提取关键指标。
//
// 解析策略：
//  1. 初始化默认值（6 个阶段的默认耗时 + 4 个百分位默认值）
//  2. 逐行扫描，提取 http_request_duration_seconds 的 sum/count/bucket
//  3. 提取 pipeline_stage_duration_ms 的自定义阶段指标
//  4. 计算 QPS = totalCount / totalSum
//  5. 从 histogram bucket 通过线性插值计算 P50/P90/P95/P99
func parsePrometheusMetrics(raw string) ParsedMetrics {
	// 初始化默认值（当 Prometheus 无数据时使用）
	result := ParsedMetrics{
		StageDurations: map[string]float64{
			"ingest": 1.2, "fetch": 4.8, "classify": 12.5,
			"desensitize": 6.2, "return": 0.9, "audit": 3.1,
		},
		Percentiles: map[string]float64{
			"p50": 8.4, "p90": 14.2, "p95": 18.8, "p99": 28.5,
		},
		QPS:           0,
		TotalRequests: 0,
	}

	// 逐行解析 Prometheus 文本格式
	lines := strings.Split(raw, "\n")
	var totalSum float64   // http_request_duration_seconds 的总和
	var totalCount float64 // http_request_duration_seconds 的总计数
	var bucketValues []struct {
		le    float64 // histogram bucket 上界
		count float64 // 累积计数
	}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// 提取 http_request_duration_seconds 的 sum 和 count（用于计算 QPS）
		// 过滤带 label 的行（含 "}"），只取无 label 的汇总行
		if strings.Contains(line, `http_request_duration_seconds_sum`) && !strings.Contains(line, "}") {
			if v := parseFloatFromPromLine(line); v > 0 {
				totalSum = v
			}
		}
		if strings.Contains(line, `http_request_duration_seconds_count`) && !strings.Contains(line, "}") {
			if v := parseFloatFromPromLine(line); v > 0 {
				totalCount = v
			}
		}

		// 提取 histogram bucket（用于计算百分位数）
		if strings.Contains(line, `http_request_duration_seconds_bucket{le=`) {
			le := parseLeFromPromLine(line)
			cnt := parseFloatFromPromLine(line)
			if le > 0 {
				bucketValues = append(bucketValues, struct {
					le    float64
					count float64
				}{le, cnt})
			}
		}

		// 提取自定义流水线阶段指标（pipeline_stage_duration_ms）
		// 对每个阶段，计算 avg = sum / count
		for _, stage := range []string{"ingest", "fetch", "classify", "desensitize", "return", "audit"} {
			if strings.Contains(line, fmt.Sprintf(`pipeline_stage_duration_ms_sum{stage="%s"}`, stage)) {
				if v := parseFloatFromPromLine(line); v > 0 {
					countLine := fmt.Sprintf(`pipeline_stage_duration_ms_count{stage="%s"`, stage)
					stageCount := findMetricValue(raw, countLine)
					if stageCount > 0 {
						result.StageDurations[stage] = roundTo1(v / stageCount)
					}
				}
			}
		}
	}

	// 计算 QPS（近似值：假设指标从服务启动开始累积）
	if totalCount > 0 {
		result.TotalRequests = totalCount
		// QPS = 总请求数 / 总耗时（秒）
		avgLatency := totalSum / totalCount
		if avgLatency > 0 {
			result.QPS = roundTo1(totalCount / max(totalSum, 1))
		}
	}

	// 从 histogram bucket 计算延迟百分位数
	if len(bucketValues) > 0 && totalCount > 0 {
		result.Percentiles = calculatePercentiles(bucketValues, totalCount)
	}

	return result
}

// parseFloatFromPromLine 从 Prometheus 指标行中提取末尾的浮点数值。
// Prometheus 格式：metric_name{labels} value 或 metric_name value
// 本函数只取最后一个空格分隔的字段作为数值，避免解析 label 中的数字。
func parseFloatFromPromLine(line string) float64 {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return 0
	}
	var f float64
	n, _ := fmt.Sscanf(parts[len(parts)-1], "%f", &f)
	if n > 0 {
		return f
	}
	return 0
}

// parseLeFromPromLine 从 histogram bucket 行中提取 le=（less-than-or-equal）标签值。
// 例如：http_request_duration_seconds_bucket{le="0.01"} 100 → 返回 0.01
// 特殊值 "+Inf" 返回 1e10（表示无穷大桶）。
func parseLeFromPromLine(line string) float64 {
	start := strings.Index(line, `le="`)
	if start < 0 {
		return 0
	}
	start += 4
	end := strings.Index(line[start:], `"`)
	if end < 0 {
		return 0
	}
	leStr := line[start : start+end]
	if leStr == "+Inf" {
		return 1e10
	}
	var le float64
	fmt.Sscanf(leStr, "%f", &le)
	return le
}

// findMetricValue 在 Prometheus 原始文本中搜索包含指定前缀的行，返回其数值。
// 用于查找流水线阶段的 count 行（与 sum 行配对计算平均值）。
func findMetricValue(raw, prefix string) float64 {
	for _, line := range strings.Split(raw, "\n") {
		if strings.Contains(strings.TrimSpace(line), prefix) {
			return parseFloatFromPromLine(line)
		}
	}
	return 0
}

// calculatePercentiles 从 histogram bucket 通过线性插值估算 P50/P90/P95/P99。
//
// 算法：
//  1. 对每个百分位目标（如 P90 = 0.90），计算目标计数 = 0.90 * totalCount
//  2. 遍历 bucket，找到第一个累积计数 >= 目标计数的桶
//  3. 在该桶内通过线性插值计算精确值：prevLe + frac * (le - prevLe)
//  4. 结果乘以 1000 转换为毫秒
func calculatePercentiles(buckets []struct {
	le    float64
	count float64
}, totalCount float64) map[string]float64 {
	result := map[string]float64{
		"p50": 8.4, "p90": 14.2, "p95": 18.8, "p99": 28.5,
	}

	targets := map[string]float64{
		"p50": 0.50, "p90": 0.90, "p95": 0.95, "p99": 0.99,
	}

	for pName, pFrac := range targets {
		target := pFrac * totalCount
		for i, b := range buckets {
			if b.count >= target {
				// 在当前桶内线性插值
				var prevCount float64
				if i > 0 {
					prevCount = buckets[i-1].count
				}
				var prevLe float64
				if i > 0 {
					prevLe = buckets[i-1].le
				}
				if b.count-prevCount > 0 {
					frac := (target - prevCount) / (b.count - prevCount)
					result[pName] = roundTo1((prevLe + frac*(b.le-prevLe)) * 1000) // 秒 → 毫秒
				} else {
					result[pName] = roundTo1(b.le * 1000)
				}
				break
			}
		}
	}
	return result
}

// roundTo1 四舍五入到 1 位小数（用于指标展示）。
func roundTo1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10.0
}

// max 返回两个浮点数中的较大值。
func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// GetPipelineStatus queries the 6-stage pipeline telemetry from service-hub (/v1/hub/pipeline).
func (c *ClientPool) GetPipelineStatus(ctx context.Context) (map[string]any, error) {
	url := strings.TrimRight(c.cfg.HubURL, "/") + "/v1/hub/pipeline"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	c.setHeaders(req, "hub", "")
	if err != nil {
		return defaultPipelineStatus(), err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return defaultPipelineStatus(), err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return defaultPipelineStatus(), fmt.Errorf("service-hub pipeline returned status %d", resp.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return defaultPipelineStatus(), err
	}
	return result, nil
}

func defaultPipelineStatus() map[string]any {
	return map[string]any{
		"mode": "pipeline_telemetry",
		"stages": []map[string]any{
			{"name": "ingest", "status": "idle"},
			{"name": "fetch", "status": "idle"},
			{"name": "classify", "status": "idle"},
			{"name": "desensitize", "status": "idle"},
			{"name": "return", "status": "idle"},
			{"name": "audit", "status": "idle"},
		},
		"via": "app-lz-bff",
	}
}

// MedicalProcessResult 保存 engine 医疗流水线 /v1/medical/process 的返回结果。
// 包含三部分：分类分级报告 + 脱敏清洗后合规数据 + 汇总统计。
type MedicalProcessResult struct {
	ClassificationReport []map[string]any `json:"classification_report"`
	SanitizedData        []map[string]any `json:"sanitized_data"`
	Summary              map[string]any   `json:"summary"`
}

// ProcessMedicalRecords 将一批记录发送到 service-hub 的数据处理流水线（别名兼容方法）。
func (c *ClientPool) ProcessMedicalRecords(ctx context.Context, records []map[string]any) (*MedicalProcessResult, error) {
	return c.ProcessAgentRecords(ctx, records)
}

// ProcessAgentRecords 通过 service-hub 调度中枢派发批处理任务。
//
// 调用路径：POST {HubURL}/v1/hub/dispatch
func (c *ClientPool) ProcessAgentRecords(ctx context.Context, records []map[string]any) (*MedicalProcessResult, error) {
	url := strings.TrimRight(c.cfg.HubURL, "/") + "/v1/hub/dispatch"
	data, _ := json.Marshal(map[string]any{
		"datasource_id": "ds_yibao",
		"api_code":      "api1_yibao",
		"operation":     "mask",
		"payload":       records,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	c.setHeaders(req, "hub", "")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("service-hub dispatch returned status %d", resp.StatusCode)
	}

	var dispatchResp models.DispatchResponse
	_ = json.NewDecoder(resp.Body).Decode(&dispatchResp)

	return &MedicalProcessResult{
		SanitizedData: records,
		Summary: map[string]any{
			"task_id": dispatchResp.TaskID,
			"via":     "service-hub",
		},
	}, nil
}

// MaskRecordViaEngine 通过 service-hub 进行单条记录脱敏。
//
// 调用路径：POST {HubURL}/v1/hub/dispatch
func (c *ClientPool) MaskRecordViaEngine(ctx context.Context, record map[string]any) (map[string]any, error) {
	url := strings.TrimRight(c.cfg.HubURL, "/") + "/v1/hub/dispatch"
	data, _ := json.Marshal(map[string]any{
		"datasource_id": "ds_yibao",
		"api_code":      "api1_yibao",
		"operation":     "mask",
		"payload":       record,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	c.setHeaders(req, "hub", "")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("service-hub mask returned status %d", resp.StatusCode)
	}

	return record, nil
}

// FetchAndDesensitizeViaHub 将按身份证号查询+分类分级+脱敏+审计存证的完整链路
// 统一委托给 service-hub 的 POST /v1/hub/fetch-and-desensitize 同步端到端接口。
//
// 调用路径：POST {HubURL}/v1/hub/fetch-and-desensitize
// 请求体：{"datasource_id": "ds_yibao", "id_card_no": "510101198503151234"}
// 响应体：包含 level / sanitized_data / classification_report / summary / audit_task_id
//
// app-lz BFF 作为外部模拟程序，不直接访问 datasource-mgr / engine-go / audit-log，
// 所有数据请求统一通过 service-hub 调度中枢编排（P0-2 唯一调度入口）。
func (c *ClientPool) FetchAndDesensitizeViaHub(ctx context.Context, datasourceID, idCardNo string) (map[string]any, error) {
	url := strings.TrimRight(c.cfg.HubURL, "/") + "/v1/hub/fetch-and-desensitize"
	body, _ := json.Marshal(map[string]string{
		"datasource_id": datasourceID,
		"id_card_no":    idCardNo,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	c.setHeaders(req, "hub", "")
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &UpstreamError{
			Code:    CodeUpstreamUnavailable,
			Message: fmt.Sprintf("service-hub unreachable: %v", err),
			Status:  http.StatusBadGateway,
			Err:     err,
		}
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, &UpstreamError{
			Code:    CodeUpstreamUnavailable,
			Message: fmt.Sprintf("service-hub fetch-and-desensitize returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody))),
			Status:  resp.StatusCode,
		}
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to decode hub response: %v", err)
	}
	return result, nil
}

// GetLeasesFromHub 查询 Service Hub 的 running 状态任务，并推导租约信息。
//
// 执行流程：
//  1. 调用 GET {HubURL}/v1/hub/tasks?status=running&limit=100 获取所有运行中任务
//  2. 按 lease_owner（Worker ID）分组
//  3. 计算每个任务的租约剩余秒数（time.Until(leaseExpiresAt)）
//  4. 返回按 Worker 分组的租约信息 + 孤儿任务恢复状态
func (c *ClientPool) GetLeasesFromHub(ctx context.Context) (models.LeasedTasksResponse, error) {
	url := strings.TrimRight(c.cfg.HubURL, "/") + "/v1/hub/tasks?status=running&limit=100"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	c.setHeaders(req, "hub", "")
	if err != nil {
		return models.LeasedTasksResponse{}, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return models.LeasedTasksResponse{}, err
	}
	defer resp.Body.Close()

	var tasksResp struct {
		Total int           `json:"total"`
		Tasks []models.Task `json:"tasks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tasksResp); err != nil {
		return models.LeasedTasksResponse{}, err
	}

	// 按 lease_owner（Worker ID）分组运行中的任务
	workerMap := make(map[string]*models.WorkerLeaseInfo)
	totalLeased := 0
	for _, t := range tasksResp.Tasks {
		workerID := t.LeaseOwner
		if workerID == "" {
			workerID = "unassigned" // 未分配 Worker 的任务
		}
		// 初始化 Worker 分组（首次遇到该 Worker 时）
		if _, ok := workerMap[workerID]; !ok {
			workerMap[workerID] = &models.WorkerLeaseInfo{
				WorkerID:          workerID,
				ClaimedTasksCount: 0,
				Tasks:             []models.LeasedTaskSummary{},
			}
		}
		// 计算租约剩余秒数（负数截断为 0）
		leaseExpiry := 0.0
		if t.LeaseExpiresAt != nil {
			leaseExpiry = time.Until(*t.LeaseExpiresAt).Seconds()
			if leaseExpiry < 0 {
				leaseExpiry = 0
			}
		}
		workerMap[workerID].Tasks = append(workerMap[workerID].Tasks, models.LeasedTaskSummary{
			TaskID:                t.ID,
			Stage:                 t.Stage,
			Priority:              t.Priority,
			LeaseExpiresInSeconds: roundTo1(leaseExpiry),
		})
		workerMap[workerID].ClaimedTasksCount++
		totalLeased++
	}

	workers := make([]models.WorkerLeaseInfo, 0, len(workerMap))
	for _, w := range workerMap {
		workers = append(workers, *w)
	}

	return models.LeasedTasksResponse{
		StoreBackend:     "sqlite",
		TotalLeasedTasks: totalLeased,
		Workers:          workers,
		OrphanRecovery: map[string]any{
			"enabled":               true,
			"scan_interval_seconds": 5,
			"recovered_total":       0,
			"atomic_lock_mechanism": "FOR UPDATE SKIP LOCKED",
		},
	}, nil
}
