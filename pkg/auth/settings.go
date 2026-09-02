package auth

import "time"

// KeyConfig 表示单个 API Key 配置。
type KeyConfig struct {
	Name      string
	Scopes    []string
	ExpiresAt *time.Time // 密钥过期时间（nil 表示永不过期）
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
}
