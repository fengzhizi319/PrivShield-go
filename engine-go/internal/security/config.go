package security

import (
	"log/slog"
	"os"
	"strings"
	"sync"

	pkgauth "github.com/fengzhizi319/PrivShield-go/pkg/auth"
	pkgconfig "github.com/fengzhizi319/PrivShield-go/pkg/config"
	pkgmiddleware "github.com/fengzhizi319/PrivShield-go/pkg/middleware"
)

// KeyConfig 表示单个 API Key 配置。
type KeyConfig = pkgauth.KeyConfig

// EndpointRateLimit 单端点限流配置。
type EndpointRateLimit = pkgmiddleware.EndpointRateLimit

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
	keyStore       *pkgauth.KeyStore
)

// GetSettings 返回缓存的安全配置单例。
func GetSettings() *Settings {
	settingsOnce.Do(func() {
		cachedSettings = loadSettings()
	})
	return cachedSettings
}

// GetKeyStore 返回 API Key 热重载存储（可能为 nil）。
func GetKeyStore() *pkgauth.KeyStore {
	return keyStore
}

// ResetSettings 重置缓存（仅测试用）。
func ResetSettings() {
	settingsOnce = sync.Once{}
	cachedSettings = nil
	if keyStore != nil {
		keyStore.Close()
		keyStore = nil
	}
}

func loadSettings() *Settings {
	internalKeys := pkgauth.LoadAPIKeysFromEnv("AGENT_AUTH_INTERNAL_API_KEYS")
	if internalKeys == nil {
		internalKeys = make(map[string]*KeyConfig)
	}
	if k := os.Getenv("AGENT_AUTH_API_KEY"); k != "" {
		internalKeys[k] = &KeyConfig{Name: "default-internal", Scopes: []string{"*"}}
	}

	keysFile := pkgconfig.EnvString("AGENT_AUTH_KEYS_FILE", "")
	if keysFile != "" {
		ks, err := pkgauth.NewKeyStore(keysFile)
		if err != nil {
			slog.Error("failed to initialize API Key store; falling back to env vars", "path", keysFile, "error", err.Error())
		} else {
			keyStore = ks
			for k, v := range ks.Keys() {
				internalKeys[k] = v
			}
			slog.Info("API Key store initialized with hot-reload", "path", keysFile, "keys", len(ks.Keys()))
		}
	}

	externalKeys := pkgauth.LoadAPIKeysFromEnv("AGENT_AUTH_EXTERNAL_API_KEYS")
	if externalKeys == nil {
		externalKeys = make(map[string]*KeyConfig)
	}
	if ext := pkgauth.LoadAPIKeysFromEnv("AGENT_AUTH_STATIC_API_KEYS"); ext != nil {
		for k, v := range ext {
			externalKeys[k] = v
		}
	}

	s := &Settings{
		Settings: pkgauth.Settings{
			AuthEnabled:  pkgconfig.EnvBool("AGENT_AUTH_ENABLED", false),
			TLSEnabled:   pkgconfig.EnvBool("AGENT_TLS_ENABLED", false),
			HealthNoAuth: pkgconfig.EnvBool("AGENT_HEALTH_NO_AUTH", true),
			InternalKeys: internalKeys,
			ExternalKeys: externalKeys,
		},
		RateLimitEnabled:      pkgconfig.EnvBool("AGENT_RATE_LIMIT_ENABLED", false),
		HealthNoRateLimit:     pkgconfig.EnvBool("AGENT_HEALTH_NO_RATE_LIMIT", true),
		MTLSEnabled:           pkgconfig.EnvBool("AGENT_AUTH_INTERNAL_MTLS_ENABLED", false),
		MTLSWhitelistFile:     pkgconfig.EnvString("AGENT_AUTH_MTLS_WHITELIST_FILE", ""),
		RateLimitDefaultRPS:   pkgconfig.EnvFloat("AGENT_RATE_LIMIT_DEFAULT_RPS", 100),
		RateLimitDefaultBurst: pkgconfig.EnvInt("AGENT_RATE_LIMIT_DEFAULT_BURST", 200),
		RateLimitPerEndpoint:  pkgmiddleware.ParseEndpointRateLimits(pkgconfig.EnvString("AGENT_RATE_LIMIT_PER_ENDPOINT", "")),
		RateLimitRedisURL:     pkgconfig.EnvString("AGENT_RATE_LIMIT_REDIS_URL", ""),
		MTLSAllowedCNs:        parseStringList(pkgconfig.EnvString("AGENT_AUTH_MTLS_ALLOWED_CNS", "")),
	}
	return s
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
