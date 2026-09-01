// Package agent provides an HTTP client to the upstream PrivShield agent.
// Package agent 封装与上游 PrivShield Python 隐私计算核心引擎（Sidecar / Agent）交互的 HTTP 客户端。
//
// 架构设计：
// 本客户端作为轻量级薄封装（Thin Wrapper），底层复用 pkg/agent.Client 共享基础库，
// 天然享有以下企业级能力：
// 1. 多 Agent 实例自动负载均衡与高可用健康探测（BaseURLs）；
// 2. 自动注入 Authorization Bearer API Key 安全鉴权头；
// 3. 熔断器模式（Circuit Breaker）与超时自动重试，防御下游级联故障；
// 4. 专职提供动态分类分级（/v1/dynclassification/*）与隐私脱敏算子（/v1/privacy/*）调用。
package agent

import (
	"context"
	"encoding/json"
	"strings"

	pkgagent "github.com/fengzhizi319/PrivShield/pkg/agent"
	"github.com/fengzhizi319/PrivShield/pkg/metrics"
	"github.com/fengzhizi319/PrivShield/pkg/naming"
	"github.com/fengzhizi319/PrivShield/services/service-hub/internal/config"
)

// Client wraps the shared agent client with service-hub-specific endpoints.
// Client 结构体在底层共享 pkgagent.Client 基础上，扩展装配 service-hub 流水线特需的领域调用方法。
type Client struct {
	*pkgagent.Client
}

// New creates a new agent client from the given config and metrics collector.
// New 函数根据 service-hub 的运行配置构造并初始化 Agent 客户端实例。
// 执行步骤：
// 1. 从 Config 提取所有 Agent URL 列表（支持单节点与多节点配置）及 APIKey；
// 2. 初始化底层 pkgagent.Client 实例并绑定熔断重试机制；
// 3. 可选注册熔断器状态观测器，将节点熔断状态上报到 Prometheus；
// 4. 返回封装后的 *Client 实例。
func New(cfg *config.Config, mc *metrics.Collector) *Client {
	pkgCfg := pkgagent.Config{
		BaseURLs: cfg.AgentBaseURLs(),
		APIKey:   cfg.AgentAPIKey,
	}
	if mc != nil {
		pkgCfg.StateObserver = func(node, state string) {
			mc.SetCircuitBreakerState(node, state)
		}
	}
	shared := pkgagent.New(pkgCfg)
	return &Client{Client: shared}
}

// ContextWithRequestID wraps pkgagent.ContextWithRequestID.
func ContextWithRequestID(ctx context.Context, requestID string) context.Context {
	return pkgagent.ContextWithRequestID(ctx, requestID)
}

// ContextWithIdempotencyKey wraps pkgagent.ContextWithIdempotencyKey.
func ContextWithIdempotencyKey(ctx context.Context, key string) context.Context {
	return pkgagent.ContextWithIdempotencyKey(ctx, key)
}

// Classify sends records to the dynamic classification endpoint.
// Classify 方法将待评估的记录批量发送至 PrivShield Agent 的动态分类分级评估接口。
//
// 执行逻辑：
// 1. 以 {"records": [...]} 结构发起 HTTP POST 调用 /v1/dynclassification/eval_record；
// 2. Agent 依据「三层漏斗（规则->NER->LLM）」逐字段定级；
// 3. 响应同时给出逐字段结果与本次评估的最高敏感级别。
//
// Agent 端点规范：
//   - URL: POST /v1/dynclassification/eval_record
//   - 请求结构: {"records": [{"field1": "val1", ...}, ...]}
//   - 响应结构: {"classifications": {"field1": {"level": "confidential", "level_id": "L3", ...}},
//     "level": "L3", "overall_level": "L3"}
//
// 顶层 level 使用规则库 L1~L5 词表。调用方 MUST 通过 audit.MaxSensitivityLevel 解析并
// 在拿不到级别时让请求失败，严禁再写死「读不到级别就用 L2」之类的静默兜底（P1-1）。
func (c *Client) Classify(ctx context.Context, records []map[string]any) (map[string]any, error) {
	return c.Post(ctx, "/v1/dynclassification/eval_record", map[string]any{"records": records})
}

// Mask sends data to the field-level masking endpoint.
// Mask 方法将原始载荷批量发送至字段级脱敏接口。
//
// 执行逻辑：
// 1. 发起 HTTP POST 请求调用 /v1/privacy/mask 端点；
// 2. Agent 依据内置规则或动态策略对字段名已知的敏感数据执行掩码或置换；
// 3. 返回脱敏后的结构化数据字典。
func (c *Client) Mask(ctx context.Context, payload any) (map[string]any, error) {
	return c.Post(ctx, "/v1/privacy/mask", payload)
}

// MaskRecord sends a full record to the record-level masking endpoint.
// MaskRecord 方法将整条单条数据记录（键值对 map[string]string）发送至记录级脱敏接口。
//
// 执行逻辑：
// 1. 构造包含 record 键值映射与可选上下文 context 的请求体；
// 2. 发起 HTTP POST 请求调用 /v1/privacy/mask_record 端点；
// 3. Agent 结合字段名与值内容完成自适应动态脱敏并返回脱敏记录。
func (c *Client) MaskRecord(ctx context.Context, record map[string]string) (map[string]any, error) {
	payload := map[string]any{
		"record":  record,
		"context": "",
	}
	return c.Post(ctx, "/v1/privacy/mask_record", payload)
}

// MedicalProcessResult holds the response from engine's /v1/medical/process endpoint.
// MedicalProcessResult 医疗流水线一次调用的返回结构：分类分级报告 + 脱敏合规数据 + 汇总统计。
//
// Level 是引擎侧三层漏斗给出的最高敏感级别（规则库 L1~L5 词表）。空串代表
// 「本次没有产生任何可识别定级」，调用方 MUST 让任务失败，不得替换为默认等级（P1-1）。
type MedicalProcessResult struct {
	ClassificationReport []map[string]any `json:"classification_report"`
	SanitizedData        []map[string]any `json:"sanitized_data"`
	Summary              map[string]any   `json:"summary"`
	Level                string           `json:"level"`
}

// ProcessMedical 将批量记录发送至 engine 处理流水线（不指定数据源的兼容别名）。
func (c *Client) ProcessMedical(ctx context.Context, records []map[string]any) (*MedicalProcessResult, error) {
	return c.ProcessAgent(ctx, records, "")
}

// ProcessAgent 将批量记录发送至 engine /v1/agent/process 通用处理流水线，
// 一次 HTTP 调用同时完成 3-Layer 分类分级 + L4/L5 高敏文本剥离 + PII 强掩码 +
// 诊断残留清除，替代原先 classify + desensitize 两步分离调用。
//
// datasourceID 为 canonical 数据源标识（如 ds_yibao）：引擎据此路由到对应领域的
// 字段级脱敏规格；缺省或未知时引擎按通用 MaskRecord 兜底处理。中枢必须透传该字段，
// 否则「医保 18 / 康养 27 逐字段规格」在流水线上永远不会生效（P0-2）。
//
// 当上游返回 404 时自动回退至兼容别名 /v1/medical/process。
func (c *Client) ProcessAgent(ctx context.Context, records []map[string]any, datasourceID string) (*MedicalProcessResult, error) {
	payload := map[string]any{
		"records": records,
	}
	if ds := strings.TrimSpace(datasourceID); ds != "" {
		payload["datasource_id"] = ds
		if apiCode := naming.APICodeForDataSource(ds); apiCode != "" {
			payload["api_code"] = apiCode
		}
	}
	result, err := c.Post(ctx, "/v1/agent/process", payload)
	if err != nil {
		// If 404, fallback to legacy /v1/medical/process
		if strings.Contains(err.Error(), "404") {
			result, err = c.Post(ctx, "/v1/medical/process", payload)
		}
		if err != nil {
			return nil, err
		}
	}

	// 将通用 map 解析为结构化结果
	mpr := &MedicalProcessResult{}
	mpr.ClassificationReport = normalizeRecords(result["classification_report"])
	mpr.SanitizedData = normalizeRecords(result["sanitized_data"])
	if summary, ok := result["summary"].(map[string]any); ok {
		mpr.Summary = summary
	}
	mpr.Level = engineOverallLevel(result, mpr.Summary)
	return mpr, nil
}

// engineOverallLevel 读取引擎响应的最高敏感级别：优先顶层 level 字段，
// 其次 summary.overall_level；两处都不可识别时返回空串（调用方 fail-closed）。
func engineOverallLevel(result, summary map[string]any) string {
	if lvl, ok := result["level"].(string); ok && naming.SecurityLevelRank(lvl) > 0 {
		return naming.NormalizeSecurityLevelID(lvl)
	}
	if summary != nil {
		if lvl, ok := summary["overall_level"].(string); ok && naming.SecurityLevelRank(lvl) > 0 {
			return naming.NormalizeSecurityLevelID(lvl)
		}
	}
	return ""
}

// normalizeRecords coerces a JSON-decoded array/object into []map[string]any.
// normalizeRecords 将 json.Unmarshal 产出的泛型结构（[]any / map[string]any / map[string]string）
// 统一归一化为 []map[string]any。
//
// 注意：engine 响应经 json.Unmarshal 后数组元素的静态类型永远是 []any，
// 直接对 result["classification_report"].([]map[string]any) 断言必然失败并静默丢空，
// 导致流水线读不到分类分级报告与脱敏结果（P0-6 存证因此缺失级别与输出指纹）。
func normalizeRecords(node any) []map[string]any {
	switch v := node.(type) {
	case nil:
		return nil
	case []map[string]any:
		return v
	case map[string]any:
		return []map[string]any{v}
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			switch m := item.(type) {
			case map[string]any:
				out = append(out, m)
			case map[string]string:
				converted := make(map[string]any, len(m))
				for key, val := range m {
					converted[key] = val
				}
				out = append(out, converted)
			}
		}
		return out
	case []map[string]string:
		out := make([]map[string]any, 0, len(v))
		for _, m := range v {
			converted := make(map[string]any, len(m))
			for key, val := range m {
				converted[key] = val
			}
			out = append(out, converted)
		}
		return out
	default:
		return nil
	}
}

// ToRecords normalizes a generic payload into []map[string]any for ProcessMedical.
// ToRecords 将通用载荷（单条 map、切片、JSON 字符串）统一转换为记录切片。
func ToRecords(payload any) []map[string]any {
	switch v := payload.(type) {
	case []map[string]any:
		return v
	case map[string]any:
		return []map[string]any{v}
	case []any:
		records := make([]map[string]any, 0, len(v))
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				records = append(records, m)
			}
		}
		return records
	case string:
		var parsed any
		if err := json.Unmarshal([]byte(v), &parsed); err == nil {
			return ToRecords(parsed)
		}
	}
	return nil
}
