package security

import (
	"os"
	"strings"
	"sync"

	pkgauth "github.com/fengzhizi319/PrivShield/pkg/auth"
	pkgconfig "github.com/fengzhizi319/PrivShield/pkg/config"
)

// KeyConfig 表示单个 API Key 配置。
type KeyConfig = pkgauth.KeyConfig

// EndpointRateLimit 单端点限流配置。
type EndpointRateLimit struct {
	RPS   float64
	Burst int
}

// Settings 安全配置，从环境变量加载。
// 嵌入 pkg/auth.Settings 以复用 scope-based 认证配置。
type Settings struct {
	pkgauth.Settings
	RateLimitEnabled      bool
	HealthNoRateLimit     bool
	RateLimitDefaultRPS   float64
	RateLimitDefaultBurst int
	RateLimitPerEndpoint  map[string]*EndpointRateLimit
	RateLimitRedisURL     string
	MTLSAllowedCNs        []string
	MTLSWhitelistFile     string
	MTLSEnabled           bool
}

var (
	settingsOnce   sync.Once
	cachedSettings *Settings
)

// GetSettings 返回缓存的安全配置单例。
func GetSettings() *Settings {
	settingsOnce.Do(func() {
		cachedSettings = loadSettings()
	})
	return cachedSettings
}

// ResetSettings 重置缓存（仅测试用）。
func ResetSettings() {
	settingsOnce = sync.Once{}
	cachedSettings = nil
}

func loadSettings() *Settings {
	internalKeys := parseAPIKeys("PRIVACY_AUTH_INTERNAL_API_KEYS")
	if internalKeys == nil {
		internalKeys = make(map[string]*KeyConfig)
	}
	for _, envK := range []string{"PRIVACY_AUTH_API_KEY", "PRIVACY_API_KEY"} {
		if k := os.Getenv(envK); k != "" {
			internalKeys[k] = &KeyConfig{Name: "default-internal", Scopes: []string{"*"}}
		}
	}

	externalKeys := parseAPIKeys("PRIVACY_AUTH_EXTERNAL_API_KEYS")
	if externalKeys == nil {
		externalKeys = make(map[string]*KeyConfig)
	}
	if ext := parseAPIKeys("PRIVACY_AUTH_STATIC_API_KEYS"); ext != nil {
		for k, v := range ext {
			externalKeys[k] = v
		}
	}

	s := &Settings{
		Settings: pkgauth.Settings{
			AuthEnabled:  pkgconfig.EnvBool("PRIVACY_AUTH_ENABLED", false),
			TLSEnabled:   pkgconfig.EnvBool("PRIVACY_TLS_ENABLED", false),
			HealthNoAuth: pkgconfig.EnvBool("PRIVACY_HEALTH_NO_AUTH", true),
			InternalKeys: internalKeys,
			ExternalKeys: externalKeys,
		},
		RateLimitEnabled:      pkgconfig.EnvBool("PRIVACY_RATE_LIMIT_ENABLED", false),
		HealthNoRateLimit:     pkgconfig.EnvBool("PRIVACY_HEALTH_NO_RATE_LIMIT", true),
		MTLSEnabled:           pkgconfig.EnvBool("PRIVACY_AUTH_INTERNAL_MTLS_ENABLED", false),
		MTLSWhitelistFile:     pkgconfig.EnvString("PRIVACY_AUTH_MTLS_WHITELIST_FILE", ""),
		RateLimitDefaultRPS:   pkgconfig.EnvFloat("PRIVACY_RATE_LIMIT_DEFAULT_RPS", 100),
		RateLimitDefaultBurst: pkgconfig.EnvInt("PRIVACY_RATE_LIMIT_DEFAULT_BURST", 200),
		RateLimitRedisURL:     pkgconfig.EnvString("PRIVACY_RATE_LIMIT_REDIS_URL", ""),
		MTLSAllowedCNs:        parseStringList(pkgconfig.EnvString("PRIVACY_AUTH_MTLS_ALLOWED_CNS", "")),
	}
	return s
}

// parseAPIKeys 解析 "key1:name1:scope1,scope2;key2:name2:scope3" 格式。
func parseAPIKeys(envKey string) map[string]*KeyConfig {
	raw := os.Getenv(envKey)
	if raw == "" {
		return nil
	}
	keys := make(map[string]*KeyConfig)
	for _, entry := range strings.Split(raw, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, ":", 3)
		if len(parts) < 2 {
			continue
		}
		token := parts[0]
		name := parts[1]
		var scopes []string
		if len(parts) == 3 && parts[2] != "" {
			scopes = strings.Split(parts[2], ",")
		} else {
			scopes = []string{"*"}
		}
		keys[token] = &KeyConfig{Name: name, Scopes: scopes}
	}
	return keys
}

func parseStringList(s string) []string {
	if s == "" {
		return nil
	}
	// Try JSON array first
	if strings.HasPrefix(s, "[") {
		s = strings.Trim(s, "[]")
	}
	var result []string
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		item = strings.Trim(item, "\"")
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}
