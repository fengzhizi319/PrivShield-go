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
	// LiveInternalKeys 可选：返回密钥热轮转存储（KeyStore / SecretWatcher）的**最新**内部密钥快照。
	// 提供后，认证内核每次比对都会调用它，使密钥轮换与吊销在同一进程内对 REST 与 gRPC
	// 双路径即时生效（三级等保 G-14 运行期密钥失效）。
	// 语义约定：调用方必须保证 InternalKeys 只含静态（环境变量）密钥，文件型密钥一律仅经
	// 本回调提供；否则被吊销的密钥会因命中启动期快照而继续有效（撤销绕过）。
	LiveInternalKeys func() map[string]*KeyConfig
}
