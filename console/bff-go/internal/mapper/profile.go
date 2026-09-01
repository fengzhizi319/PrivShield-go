package mapper

import (
	// context：用于传递 gRPC 调用的上下文
	// context: passes gRPC call context
	"context"
	// encoding/json：用于解析前端 JSON 请求体与推荐参数 JSON
	// encoding/json: parses frontend JSON body and recommended params JSON
	"encoding/json"

	// pb：由 proto 生成的 gRPC 客户端与消息类型
	// pb: generated gRPC client and message types from proto
	pb "github.com/fengzhizi319/PrivShield-go/console/bff-go/proto"
)

// ---------------------------------------------------------------------------
// Profile handler —— Personalized Profile Recommendation / 个性化配置推荐
// ---------------------------------------------------------------------------

// handleRecommendParams 处理 /v1/privacy/profile/recommend 路径，个性化隐私参数推荐。
//
// 根据数据特征（数值分布、记录内容等）自动推荐最优隐私参数，
// 如 epsilon 值、噪声机制、裁剪范围等。
func (m *Mapper) handleRecommendParams(ctx context.Context, client pb.PrivacyServiceClient, body json.RawMessage) (any, error) {
	v, err := decode(body)
	if err != nil {
		return nil, err
	}
	// 构造 RecommendRequest：namespace + 数据特征
	resp, err := client.RecommendParams(ctx, &pb.RecommendRequest{
		Namespace: getString(v, "namespace", ""), // 预算命名空间
		Values:    getFloats(v, "values"),        // 数值数据（用于分布分析）
		Rows:      getRecordEntries(v, "rows"),   // 记录数据（用于内容分析）
		QiCols:    getStrings(v, "qi_cols"),      // 准标识符列名
	})
	if err != nil {
		return nil, err
	}
	// 尝试将推荐参数 JSON 解析为结构化对象，失败则保持原始字符串
	var recommended any
	if err := json.Unmarshal([]byte(resp.RecommendedParamsJson), &recommended); err != nil {
		recommended = resp.RecommendedParamsJson
	}
	return map[string]any{
		"status":             resp.Status,    // 推荐状态
		"namespace":          resp.Namespace, // 预算命名空间
		"recommended_params": recommended,    // 推荐的隐私参数（结构化对象或原始字符串）
	}, nil
}
