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
// K-Anonymity handlers —— K-Anonymity / K-匿名
//
// K-anonymity generalizes quasi-identifiers (QI) so that each record is
// indistinguishable from at least K-1 other records, preventing individual
// identification via QI combinations. Uses Mondrian algorithm for table-level generalization.
// K-匿名通过泛化准标识符（QI）使每条记录至少与 K-1 条其他记录不可区分，
// 从而防止通过准标识符组合定位到个人。使用 Mondrian 算法进行表级泛化。
// ---------------------------------------------------------------------------

// handleKAnonymizeRecord 处理 /v1/privacy/k_anonymize/record 路径，单条记录 K-匿名检查。
//
// 检查给定记录在数据集中的 K-匿名度，返回泛化后的记录。
func (m *Mapper) handleKAnonymizeRecord(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 KAnonymizeRequest：record 为待检查记录，qi_cols 为准标识符列名
	resp, err := client.KAnonymizeRecord(ctx, &pb.KAnonymizeRequest{
		Record: getStringMap(v, "record"), // 待检查的记录（字段名→值）
		QiCols: getStrings(v, "qi_cols"),  // 准标识符列名列表
		K:      getInt32(v, "k", 5),       // K 值（默认 5）
	})
	if err != nil {
		return nil, err
	}
	// 返回泛化后的记录
	return map[string]map[string]string{"result": resp.Result}, nil
}

// handleKAnonymizeTable 处理 /v1/privacy/k_anonymize/table 路径，表级 K-匿名。
//
// 使用 Mondrian 算法对整个数据集进行 K-匿名泛化。
// max_depth 控制泛化树的最大深度，防止过度泛化。
func (m *Mapper) handleKAnonymizeTable(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 KAnonymizeTableRequest：rows 为数据集，qi_cols 为准标识符
	resp, err := client.KAnonymizeTable(ctx, &pb.KAnonymizeTableRequest{
		Rows:     getRecordEntries(v, "rows"),  // 数据集（多行记录）
		QiCols:   getStrings(v, "qi_cols"),     // 准标识符列名列表
		K:        getInt32(v, "k", 5),          // K 值（默认 5）
		MaxDepth: getInt32(v, "max_depth", 10), // 泛化树最大深度（默认 10）
	})
	if err != nil {
		return nil, err
	}
	// 返回泛化后的数据集（RecordEntry 列表）
	return map[string][]*pb.RecordEntry{"rows": resp.Rows}, nil
}

// handleKAnonymizeDataFrame 处理 /v1/privacy/k_anonymize/dataframe 路径，DataFrame 级 K-匿名。
//
// 与 handleKAnonymizeTable 功能相同，但使用 "data" 字段名而非 "rows"，
// 与 mask_dataframe 的 JSON 契约保持一致。
func (m *Mapper) handleKAnonymizeDataFrame(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 KAnonymizeDataFrameRequest：data 为数据集（字段名为 "data" 而非 "rows"）
	resp, err := client.KAnonymizeDataFrame(ctx, &pb.KAnonymizeDataFrameRequest{
		Data:     getRecordEntries(v, "data"),  // 数据集（多行记录）
		QiCols:   getStrings(v, "qi_cols"),     // 准标识符列名列表
		K:        getInt32(v, "k", 5),          // K 值（默认 5）
		MaxDepth: getInt32(v, "max_depth", 10), // 泛化树最大深度（默认 10）
	})
	if err != nil {
		return nil, err
	}
	// 返回泛化后的数据集
	return map[string][]*pb.RecordEntry{"rows": resp.Data}, nil
}
