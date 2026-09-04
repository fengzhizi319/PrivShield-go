package mapper

import (
	// context：用于传递 gRPC 调用的上下文
	// context: passes gRPC call context
	"context"
	// encoding/json：用于解析前端 JSON 请求体
	// encoding/json: parses frontend JSON request body
	"encoding/json"

	// pb：由 proto 生成的 gRPC 客户端与消息类型
	// pb: generated gRPC client and message types from proto
	pb "github.com/fengzhizi319/PrivShield-go/console/bff-go/proto"
)

// ---------------------------------------------------------------------------
// Query Obfuscation handlers —— Query Obfuscation / 查询混淆
//
// Injects dummy queries alongside real queries, making it impossible for attackers
// (e.g. DBAs, network monitors) to distinguish which queries are genuine.
// 向真实查询中注入虚假查询（dummy queries），
// 使攻击者（如数据库管理员、网络监控者）无法区分哪些是真实查询。
// ---------------------------------------------------------------------------

// handleObfuscateQuery 处理 /v1/privacy/qol/obfuscate 路径，单条查询混淆。
//
// 在真实查询旁注入 num_dummies 个虚假查询，返回混合后的查询列表。
func (m *Mapper) handleObfuscateQuery(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 ObfuscateQueryRequest：query 为真实查询，num_dummies 为虚假查询数量
	resp, err := client.ObfuscateQuery(ctx, &pb.ObfuscateQueryRequest{
		Query:       getString(v, "query", ""),         // 真实查询
		NumDummies:  getInt32(v, "num_dummies", 3),     // 虚假查询数量（默认 3）
		Domain:      getString(v, "domain", "medical"), // 查询领域（"medical"/"generic"）
		MedicalPool: getStrings(v, "medical_pool"),     // 自定义医疗领域虚假查询池
		GenericPool: getStrings(v, "generic_pool"),     // 自定义通用领域虚假查询池
	})
	if err != nil {
		return nil, err
	}
	// 返回混合后的查询列表（真实 + 虚假，顺序已打乱）
	return map[string][]string{"result": resp.Result}, nil
}

// handleObfuscateQueryBatch 处理 /v1/privacy/qol/obfuscate/batch 路径，批量查询混淆。
//
// 对多条查询分别注入虚假查询，返回每条查询的混淆结果。
func (m *Mapper) handleObfuscateQueryBatch(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 ObfuscateQueryBatchRequest：queries 为多条真实查询
	resp, err := client.ObfuscateQueryBatch(ctx, &pb.ObfuscateQueryBatchRequest{
		Queries:     getStrings(v, "queries"),          // 多条真实查询
		NumDummies:  getInt32(v, "num_dummies", 3),     // 每条查询的虚假查询数量
		Domain:      getString(v, "domain", "medical"), // 查询领域
		MedicalPool: getStrings(v, "medical_pool"),     // 自定义医疗虚假查询池
		GenericPool: getStrings(v, "generic_pool"),     // 自定义通用虚假查询池
	})
	if err != nil {
		return nil, err
	}
	// 将 protobuf 重复消息列表展平为 [][]string
	results := make([][]string, 0, len(resp.Results))
	for _, r := range resp.Results {
		// 每个 r.Result 为一条查询的混淆结果（真实 + 虚假）
		results = append(results, r.Result)
	}
	return map[string][][]string{"results": results}, nil
}
