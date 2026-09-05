package auth

import "time"

// KeyConfig 表示单个 API Key 配置。
type KeyConfig struct {
	Name string
	// Subject 为该密钥绑定的责任主体（注册用户名）。静态环境变量/文件密钥通常为空，
	// 由 UserStore 动态签发或登录会话派生的密钥必填，使「Token → 自然人/机构」可溯源，
	// 并支撑 /v1/auth/users/:username 系列端点的「本人自助」判定（ABAC）。
	Subject   string
	Scopes    []string
	ExpiresAt *time.Time // 密钥过期时间（nil 表示永不过期）
}

// Clone 返回深拷贝，避免调用方持有的快照被存储层后续变更并发改写（数据竞争）。
// ExpiresAt 也做值拷贝（而非共享指针），确保快照与存储内部状态完全解耦。
func (k *KeyConfig) Clone() *KeyConfig {
	if k == nil {
		return nil
	}
	clone := &KeyConfig{
		Name:    k.Name,
		Subject: k.Subject,
		Scopes:  append([]string(nil), k.Scopes...),
	}
	if k.ExpiresAt != nil {
		exp := *k.ExpiresAt
		clone.ExpiresAt = &exp
	}
	return clone
}

// IsExpired 检查密钥是否已过期（三级等保 G-14）。
func (k *KeyConfig) IsExpired() bool {
	if k.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*k.ExpiresAt)
}

// Settings 保存认证中间件所需的共享安全配置。
// 额外字段（限流、mTLS 等）由 engine-go/internal/security 在嵌入后扩展。
type Settings struct {
	AuthEnabled  bool
	TLSEnabled   bool
	HealthNoAuth bool
	InternalKeys map[string]*KeyConfig // token -> config
	ExternalKeys map[string]*KeyConfig // token -> config
	// LiveInternalKeys 可选：返回密钥热轮转存储（KeyStore / SecretWatcher）的**最新**内部密钥快照。
	// 提供后，认证内核每次比对都会调用它，使密钥轮换与吊销在同一进程内对 REST 与 gRPC
	// 双路径即时生效（三级等保 G-14 运行期密钥失效）。
	// 语义约定：调用方必须保证 InternalKeys 只含静态（环境变量）密钥，文件型密钥一律仅经
	// 本回调提供；否则被吊销的密钥会因命中启动期快照而继续有效（撤销绕过）。
	// 键语义：map 的键为 **Token 明文**（环境变量密钥、KeyStore 文件密钥）。
	LiveInternalKeys func() map[string]*KeyConfig
	// LiveInternalHashedKeys 可选：返回「不落盘明文」的动态用户密钥快照，map 的键为
	// HashToken(token)（SHA-256 十六进制摘要），值为对应 KeyConfig。
	// UserStore 用它在持久化文件中只保存 Token 摘要（等保三级 G-14 / 密评：敏感凭证不以明文
	// 长期存储），同时保证进程重启后已签发密钥仍然可用。认证内核在明文活密钥未命中时，
	// 对来访 Token 计算一次摘要再走同一套常量时间比对，语义与吊销时效完全一致。
	LiveInternalHashedKeys func() map[string]*KeyConfig
}
