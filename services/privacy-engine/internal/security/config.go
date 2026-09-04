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
	// AllowedCIDRs 网络层准入白名单（AGENT_ALLOWED_CIDRS）。
	// 必须同时作用于 REST 与 gRPC 两个监听端口：历史上它只被 REST 漏斗读取，
	// 导致收紧后 gRPC 端口仍是开放面（双路径不对称）。
	AllowedCIDRs      []string
	MTLSAllowedCNs    []string
	MTLSWhitelistFile string
	MTLSEnabled       bool
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
	var liveKeys func() map[string]*KeyConfig
	if keysFile != "" {
		ks, err := pkgauth.NewKeyStore(keysFile)
		if err != nil {
			slog.Error("failed to initialize API Key store; falling back to env vars", "path", keysFile, "error", err.Error())
		} else {
			keyStore = ks
			// 文件型密钥只经 LiveInternalKeys 活读，**不再**并入启动期静态快照：
			// 一旦并入，密钥从文件删除（吊销）后仍会命中旧快照而在 gRPC 面永久有效（撤销绕过）。
			// 环境变量密钥继续走 InternalKeys，与文件密钥取并集，语义与历史合并方式等价。
			liveKeys = ks.Keys
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
			AuthEnabled:      pkgconfig.EnvBool("AGENT_AUTH_ENABLED", false),
			TLSEnabled:       pkgconfig.EnvBool("AGENT_TLS_ENABLED", false),
			HealthNoAuth:     pkgconfig.EnvBool("AGENT_HEALTH_NO_AUTH", true),
			InternalKeys:     internalKeys,
			ExternalKeys:     externalKeys,
			LiveInternalKeys: liveKeys,
		},
		RateLimitEnabled:      pkgconfig.EnvBool("AGENT_RATE_LIMIT_ENABLED", false),
		HealthNoRateLimit:     pkgconfig.EnvBool("AGENT_HEALTH_NO_RATE_LIMIT", true),
		MTLSEnabled:           pkgconfig.EnvBool("AGENT_AUTH_INTERNAL_MTLS_ENABLED", false),
		MTLSWhitelistFile:     pkgconfig.EnvString("AGENT_AUTH_MTLS_WHITELIST_FILE", ""),
		AllowedCIDRs:          pkgmiddleware.AllowedCIDRsFromEnv("AGENT_ALLOWED_CIDRS"),
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
