package auth

// KeyConfig 表示单个 API Key 配置。
type KeyConfig struct {
	Name   string
	Scopes []string
}

// Settings 保存认证中间件所需的共享安全配置。
// 额外字段（限流、mTLS 等）由 engine-go/internal/security 在嵌入后扩展。
type Settings struct {
	AuthEnabled  bool
	TLSEnabled   bool
	HealthNoAuth bool
	InternalKeys map[string]*KeyConfig // token -> config
	ExternalKeys map[string]*KeyConfig // token -> config
}
