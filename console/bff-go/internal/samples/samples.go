// Package samples provides minimal, deterministic request payloads for every
// gRPC method exposed by PrivShield.
// Package samples 为 PrivShield 暴露的每个 gRPC 方法提供最小化、确定性的请求负载。
//
// These sample payloads directly correspond to gRPC request message structures
// (rather than REST's params wrapper style). After frontend loads them, the Go
// backend converts JSON to protobuf messages and invokes the corresponding RPC.
// 这些示例数据直接对应 gRPC 请求消息的结构（而非 REST 的 params 包装风格）。
// 前端加载后，Go 后端将 JSON 转换为 protobuf 消息并调用对应 RPC。
package samples

import (
	// encoding/json：提供 json.RawMessage 类型，用于延迟解析示例 JSON
	// encoding/json: provides json.RawMessage for lazy-parsing sample JSON
	"encoding/json"

	// models：与前端共享的 EndpointSample 结构定义
	// models: shared EndpointSample struct definition with frontend
	"github.com/fengzhizi319/PrivShield-go/console/bff-go/internal/models"
)

// raw is a small helper that converts a string literal to json.RawMessage.
// raw 是一个小工具函数，将字符串字面量转换为 json.RawMessage。
func raw(s string) json.RawMessage {
	return json.RawMessage(s)
}

// List returns all gRPC-supported endpoint samples.
// List 返回所有 gRPC 支持的端点示例。
//
// Note: the following endpoints are only defined in REST, not in gRPC proto,
// thus not supported by the Go backend:
// 注意：以下端点仅在 REST 中定义，未包含在 gRPC proto 中，因此不在 Go 后端支持范围内：
//   - /livez, /readyz, /readyz/llm
//   - /v1/privacy/dp/arrow_ipc
//   - /v1/privacy/budget
func List() []models.EndpointSample {
	return []models.EndpointSample{
		// Health
		{
			Method: "POST", Path: "/v1/privacy/health", Label: "Health", Category: "Health",
			Description: "gRPC 健康检查", Body: raw(`{}`), Backend: "grpc",
		},

		// Masking
		{
			Method: "POST", Path: "/v1/privacy/mask", Label: "Mask", Category: "Masking",
			Description: "单字段脱敏",
			Body:        raw(`{"field_name":"email","value":"alice@example.com","context":""}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/mask_record", Label: "Mask Record", Category: "Masking",
			Description: "整条记录脱敏",
			Body:        raw(`{"record":{"email":"alice@example.com","phone":"13800138000","name":"Alice","id_card":"11010119900101XXXX"},"context":""}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/mask_batch", Label: "Mask Batch", Category: "Masking",
			Description: "批量字段脱敏",
			Body:        raw(`{"field_names":["email","phone","name"],"values":["bob@example.com","13900139000","Bob"],"context":""}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/mask_dataframe", Label: "Mask DataFrame", Category: "Masking",
			Description: "DataFrame 脱敏",
			Body:        raw(`{"data":[{"fields":{"email":"alice@example.com","phone":"13800138000"}},{"fields":{"email":"bob@example.com","phone":"13900139000"}}],"columns":["email","phone"],"context":""}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/hash", Label: "Hash", Category: "Hash",
			Description: "HMAC 哈希",
			Body:        raw(`{"value":"sensitive-value","salt":"demo-salt"}`), Backend: "grpc",
		},

		// DP
		{
			Method: "POST", Path: "/v1/privacy/dp/count", Label: "DP Count", Category: "DP",
			Description: "差分隐私计数",
			Body:        raw(`{"values":[1.0,2.0,3.0,4.0,5.0],"epsilon":0.1,"mechanism":"laplace"}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/dp/sum", Label: "DP Sum", Category: "DP",
			Description: "差分隐私求和",
			Body:        raw(`{"values":[1000.0,2000.0,3000.0,4000.0,5000.0],"epsilon":0.1,"mechanism":"laplace","clip_lower":0.0,"clip_upper":10000.0}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/dp/mean", Label: "DP Mean", Category: "DP",
			Description: "差分隐私均值",
			Body:        raw(`{"values":[20.0,30.0,40.0,50.0,60.0],"epsilon":0.1,"mechanism":"laplace","clip_lower":0.0,"clip_upper":100.0}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/dp/histogram", Label: "DP Histogram", Category: "DP",
			Description: "差分隐私直方图",
			Body:        raw(`{"values":["eng","hr","eng","sales","eng"],"categories":["eng","hr","sales","marketing"],"epsilon":0.1,"mechanism":"laplace"}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/dp/noisy_count", Label: "Noisy Count", Category: "DP",
			Description: "对已聚合计数加噪",
			Body:        raw(`{"true_count":100.0,"epsilon":0.1,"mechanism":"laplace"}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/dp/noisy_sum", Label: "Noisy Sum", Category: "DP",
			Description: "对已聚合求和加噪",
			Body:        raw(`{"true_sum":10000.0,"epsilon":0.1,"mechanism":"laplace","sensitivity":10000.0}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/dp/noisy_mean", Label: "Noisy Mean", Category: "DP",
			Description: "对已聚合均值加噪",
			Body:        raw(`{"true_sum":10000.0,"true_count":100.0,"epsilon":0.1,"mechanism":"laplace","sensitivity":10000.0}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/dp/noisy_histogram", Label: "Noisy Histogram", Category: "DP",
			Description: "对已聚合直方图加噪",
			Body:        raw(`{"true_counts":{"eng":50.0,"hr":20.0,"sales":30.0},"epsilon":0.1,"mechanism":"laplace"}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/dp/chunked_count", Label: "Chunked Count", Category: "DP",
			Description: "分块流式 DP 计数",
			Body:        raw(`{"chunks":[{"values":[1.0,2.0]},{"values":[3.0,4.0]},{"values":[5.0,6.0]}],"epsilon":0.1,"mechanism":"laplace"}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/dp/chunked_sum", Label: "Chunked Sum", Category: "DP",
			Description: "分块流式 DP 求和",
			Body:        raw(`{"chunks":[{"values":[1.0,2.0]},{"values":[3.0,4.0]},{"values":[5.0,6.0]}],"epsilon":0.1,"mechanism":"laplace","clip_lower":0.0,"clip_upper":10.0}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/dp/chunked_mean", Label: "Chunked Mean", Category: "DP",
			Description: "分块流式 DP 均值",
			Body:        raw(`{"chunks":[{"values":[1.0,2.0]},{"values":[3.0,4.0]},{"values":[5.0,6.0]}],"epsilon":0.1,"mechanism":"laplace","clip_lower":0.0,"clip_upper":10.0,"min_count":5.0}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/dp/chunked_histogram", Label: "Chunked Histogram", Category: "DP",
			Description: "分块流式 DP 直方图",
			Body:        raw(`{"chunks":[{"values":["eng","hr"]},{"values":["eng","sales"]},{"values":["eng","marketing"]}],"categories":["eng","hr","sales","marketing"],"epsilon":0.1,"mechanism":"laplace"}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/dp/aggregate", Label: "DP Aggregate", Category: "DP",
			Description: "表格级原位 DP 聚合",
			Body:        raw(`{"rows":[{"fields":{"age":"20","salary":"1000.0","dept":"eng"}},{"fields":{"age":"30","salary":"2000.0","dept":"hr"}},{"fields":{"age":"40","salary":"3000.0","dept":"eng"}},{"fields":{"age":"50","salary":"4000.0","dept":"sales"}}],"specs_json":"{\"age\":[\"mean\",{\"clip_lower\":0,\"clip_upper\":100}],\"salary\":[\"sum\",{\"clip_lower\":0,\"clip_upper\":10000}],\"dept\":[\"histogram\",{\"categories\":[\"eng\",\"hr\",\"sales\"]}]}","epsilon":0.5,"delta":0.0,"mechanism":"laplace"}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/dp/vector_sum", Label: "DP Vector Sum", Category: "DP",
			Description: "高维向量 DP 求和",
			Body:        raw(`{"vectors":[{"values":[1.0,2.0]},{"values":[3.0,4.0]},{"values":[5.0,6.0]}],"max_norm":10.0,"epsilon":0.1,"delta":0.00001,"mechanism":"gaussian"}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/dp/adaptive_clip", Label: "Adaptive Clip", Category: "DP",
			Description: "自适应二分搜索估计截断上下界",
			Body:        raw(`{"values":[1.0,5.0,10.0,15.0,20.0],"epsilon":0.1,"target_quantile":0.95,"num_iterations":15,"initial_clip":10.0}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/dp/groupby", Label: "DP GroupBy", Category: "DP",
			Description: "Tau-Thresholding 差分隐私 Group-By",
			Body:        raw(`{"rows":[{"fields":{"dept":"eng","salary":"1000.0"}},{"fields":{"dept":"hr","salary":"2000.0"}},{"fields":{"dept":"eng","salary":"3000.0"}},{"fields":{"dept":"sales","salary":"4000.0"}}],"group_col":"dept","target_col":"salary","agg":"sum","epsilon":0.1,"delta":0.00001,"mechanism":"laplace","clip_lower":0.0,"clip_upper":10000.0}`), Backend: "grpc",
		},

		// LDP
		{
			Method: "POST", Path: "/v1/privacy/ldp/perturb/binary", Label: "Perturb Binary", Category: "LDP",
			Description: "二值本地 DP 扰动",
			Body:        raw(`{"values":[0,1,1,0,1],"epsilon":1.0}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/ldp/perturb/categorical", Label: "Perturb Categorical", Category: "LDP",
			Description: "类别型本地 DP 扰动",
			Body:        raw(`{"values":["eng","hr","eng","sales"],"categories":["eng","hr","sales"],"epsilon":1.0}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/ldp/estimate/binary", Label: "Estimate Binary", Category: "LDP",
			Description: "二值本地 DP 估计",
			Body:        raw(`{"reported_values":[0,1,1,0,1],"epsilon":1.0}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/ldp/estimate/categorical", Label: "Estimate Categorical", Category: "LDP",
			Description: "类别型本地 DP 估计",
			Body:        raw(`{"reported_values":["eng","hr","eng","sales"],"categories":["eng","hr","sales"],"epsilon":1.0}`), Backend: "grpc",
		},

		// K-Anonymity
		{
			Method: "POST", Path: "/v1/privacy/k_anonymize/record", Label: "K-Anonymize Record", Category: "K-Anonymity",
			Description: "单条记录 K-匿名泛化",
			Body:        raw(`{"record":{"age":"30","zip":"100000","gender":"F"},"qi_cols":["age","zip","gender"],"k":2}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/k_anonymize/table", Label: "K-Anonymize Table", Category: "K-Anonymity",
			Description: "整张表 K-匿名泛化",
			Body:        raw(`{"rows":[{"fields":{"age":"30","zip":"100000","gender":"F"}},{"fields":{"age":"31","zip":"100001","gender":"F"}},{"fields":{"age":"32","zip":"100002","gender":"M"}},{"fields":{"age":"33","zip":"100003","gender":"M"}}],"qi_cols":["age","zip","gender"],"k":2,"max_depth":10}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/k_anonymize/dataframe", Label: "K-Anonymize DataFrame", Category: "K-Anonymity",
			Description: "DataFrame K-匿名泛化",
			Body:        raw(`{"data":[{"fields":{"age":"30","zip":"100000","gender":"F"}},{"fields":{"age":"31","zip":"100001","gender":"F"}},{"fields":{"age":"32","zip":"100002","gender":"M"}},{"fields":{"age":"33","zip":"100003","gender":"M"}}],"qi_cols":["age","zip","gender"],"k":2,"max_depth":10}`), Backend: "grpc",
		},

		// Query Obfuscation
		{
			Method: "POST", Path: "/v1/privacy/qol/obfuscate", Label: "Obfuscate Query", Category: "Query Obfuscation",
			Description: "查询混淆",
			Body:        raw(`{"query":"糖尿病患者用药推荐","num_dummies":3,"domain":"medical"}`), Backend: "grpc",
		},
		{
			Method: "POST", Path: "/v1/privacy/qol/obfuscate/batch", Label: "Obfuscate Batch", Category: "Query Obfuscation",
			Description: "批量查询混淆",
			Body:        raw(`{"queries":["糖尿病患者用药推荐","高血压患者饮食建议"],"num_dummies":3,"domain":"medical"}`), Backend: "grpc",
		},

		// Profile
		{
			Method: "POST", Path: "/v1/privacy/profile/recommend", Label: "Recommend Params", Category: "Profile",
			Description: "自动推荐隐私参数",
			Body:        raw(`{"namespace":"demo-recommend","values":[1.0,2.0,3.0,4.0,5.0,6.0,7.0,8.0,9.0,10.0],"rows":[{"fields":{"age":"30","zip":"100000","gender":"F"}},{"fields":{"age":"31","zip":"100001","gender":"M"}}],"qi_cols":["age","zip","gender"]}`), Backend: "grpc",
		},

		// Dynamic Classification
		{
			Method: "POST", Path: "/v1/dynclassification/eval", Label: "Dynamic Eval", Category: "DynamicClassification",
			Description: "声明式动态分类分级评估",
			Body:        raw(`{"fieldName":"mobile_phone","value":"13800138000","domain":"general-pii"}`), Backend: "both",
		},
		{
			Method: "POST", Path: "/v1/dynclassification/eval_record", Label: "Dynamic Eval Record", Category: "DynamicClassification",
			Description: "记录级动态分类分级（逐字段）",
			Body:        raw(`{"record":{"name":"张三","id_card":"110101199001011237","phone":"13800138000"}}`), Backend: "both",
		},
		{
			Method: "POST", Path: "/v1/dynclassification/generate_profile", Label: "Auto Generate Profiles", Category: "DynamicClassification",
			Description: "从标准 Markdown 文档自动提取生成 YAML 配置",
			Body:        raw(`{"docPath":"docs/standard/四川省健康医疗大数据应用指南.md"}`), Backend: "both",
		},
		{
			Method: "GET", Path: "/v1/dynclassification/standards", Label: "List Standards", Category: "DynamicClassification",
			Description: "列出所有分类分级标准", Body: raw(`{}`), Backend: "both",
		},
		{
			Method: "GET", Path: "/v1/dynclassification/domains", Label: "List Domains", Category: "DynamicClassification",
			Description: "列出所有领域匹配包", Body: raw(`{}`), Backend: "both",
		},
		{
			Method: "GET", Path: "/v1/dynclassification/operators", Label: "List Operators", Category: "DynamicClassification",
			Description: "列出所有已注册匹配算子", Body: raw(`{}`), Backend: "both",
		},
		{
			Method: "POST", Path: "/v1/dynclassification/validate", Label: "Validate Profiles", Category: "DynamicClassification",
			Description: "校验规则 YAML 配置合法性", Body: raw(`{}`), Backend: "both",
		},
	}
}
