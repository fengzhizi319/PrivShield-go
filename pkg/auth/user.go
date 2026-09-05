// Package auth — 用户与权限模型及等保三级口令安全规范。
//
// 遵循 GB/T 22239-2019《信息安全技术 网络安全等级保护基本要求》第三级（G-04 口令复杂度与防重放控制）。
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// UserStatus 表示用户账号生命周期状态。
type UserStatus string

const (
	UserStatusActive   UserStatus = "active"
	UserStatusDisabled UserStatus = "disabled"
)

// APIKeyRecord 记录为用户颁发的动态 API Key 元信息。
//
// 安全约定：**明文 Token 永不落盘、永不进日志、永不出现在查询响应**；持久化仅保存
// SHA-256 摘要（TokenHash）与脱敏前缀（TokenPrefix）。明文仅在签发当次响应中下发一次。
type APIKeyRecord struct {
	KeyID     string `json:"key_id"`
	Name      string `json:"name"`
	TokenHash string `json:"token_hash"` // HashToken(明文) 的十六进制摘要，作为活密钥索引键
	// LegacyToken 仅用于兼容早期版本落盘的明文 Token：加载时转为摘要后立即清空，
	// 下一次保存即不再写入明文（一次性无损迁移）。
	LegacyToken string     `json:"token,omitempty"`
	TokenPrefix string     `json:"token_prefix"` // 前缀脱敏标识，如 "psk_a***"
	Scopes      []string   `json:"scopes"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// IsExpired 判断该 API Key 是否已过有效期。
func (k *APIKeyRecord) IsExpired() bool {
	if k.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*k.ExpiresAt)
}

// User 表示系统注册用户账号实体。
type User struct {
	Username     string     `json:"username"`
	DisplayName  string     `json:"display_name"`
	Role         string     `json:"role"`
	Scopes       []string   `json:"scopes"`
	Status       UserStatus `json:"status"`
	PasswordHash string     `json:"-"` // bcrypt 哈希密文，严格不输出到 JSON
	// PasswordHistory 保存最近 PasswordHistoryDepth 次口令的 bcrypt 哈希，用于禁止口令重用
	// （等保三级 G-04）；同样不得输出到任何响应或日志。
	PasswordHistory   []string                 `json:"-"`
	PasswordUpdatedAt time.Time                `json:"password_updated_at"` // 供合规巡检口令有效期
	APIKeys           map[string]*APIKeyRecord `json:"api_keys,omitempty"`  // key_id -> record
	CreatedAt         time.Time                `json:"created_at"`
	UpdatedAt         time.Time                `json:"updated_at"`
}

// UserSummary 提供对外列表展示的脱敏用户信息。
type UserSummary struct {
	Username          string     `json:"username"`
	DisplayName       string     `json:"display_name"`
	Role              string     `json:"role"`
	Scopes            []string   `json:"scopes"`
	Status            UserStatus `json:"status"`
	KeyCount          int        `json:"key_count"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	PasswordUpdatedAt time.Time  `json:"password_updated_at"`
}

// PasswordExpired 判定口令是否已超过 MaxPasswordAge（等保三级 G-04 口令定期更换）。
// PasswordUpdatedAt 为零值（历史脏数据）时不误报，交由合规巡检另行处置。
func (u *User) PasswordExpired() bool {
	if u == nil || u.PasswordUpdatedAt.IsZero() {
		return false
	}
	return time.Since(u.PasswordUpdatedAt) > MaxPasswordAge
}

// ToSummary 转换为脱敏摘要。
func (u *User) ToSummary() UserSummary {
	return UserSummary{
		Username:          u.Username,
		DisplayName:       u.DisplayName,
		Role:              u.Role,
		Scopes:            append([]string(nil), u.Scopes...),
		Status:            u.Status,
		KeyCount:          len(u.APIKeys),
		CreatedAt:         u.CreatedAt,
		UpdatedAt:         u.UpdatedAt,
		PasswordUpdatedAt: u.PasswordUpdatedAt,
	}
}

// SanitizedCopy 返回抹除敏感信息（口令哈希、口令历史、Token 明文）的用户克隆对象。
// 存储层对外一律返回克隆体，避免调用方在无锁情况下读到被并发改写的内部对象（数据竞争）。
func (u *User) SanitizedCopy() *User {
	if u == nil {
		return nil
	}
	clone := &User{
		Username:          u.Username,
		DisplayName:       u.DisplayName,
		Role:              u.Role,
		Scopes:            append([]string(nil), u.Scopes...),
		Status:            u.Status,
		PasswordUpdatedAt: u.PasswordUpdatedAt,
		APIKeys:           make(map[string]*APIKeyRecord, len(u.APIKeys)),
		CreatedAt:         u.CreatedAt,
		UpdatedAt:         u.UpdatedAt,
	}
	for id, k := range u.APIKeys {
		rec := *k
		rec.LegacyToken = "" // 迁移残留的明文一律丢弃
		rec.Scopes = append([]string(nil), k.Scopes...)
		clone.APIKeys[id] = &rec
	}
	return clone
}

// ============================================================================
// 角色与默认权限定义
// ============================================================================

// DefaultScopesForRole 根据角色返回预置的细粒度 Scope 列表。
//
// 最小权限原则：普通业务角色（developer / data-engineer / user）**不**预置 user:read，
// 否则每个业务账号都能枚举全量用户清单；本人账号与密钥的自助访问由 Identity.Subject
// 判定（见 user_handlers.go），不依赖 user:read。
func DefaultScopesForRole(role string) []string {
	switch role {
	case "admin":
		return []string{"*", "user:admin", "hub:admin", "ops:admin", "privacy:budget"}
	case "operator":
		return []string{"hub:dispatch", "hub:read", "hub:admin", "user:read"}
	case "data-engineer":
		return []string{"privacy:mask", "privacy:dp", "privacy:kano", "medical:process", "file:process"}
	case "compliance-officer":
		return []string{"dynclassification:read", "dynclassification:write", "privacy:budget", "ops:diagnostics", "user:read"}
	case "auditor":
		return []string{"audit:read", "ops:diagnostics", "health:read", "user:read"}
	case "developer":
		return []string{"privacy:mask", "medical:process", "hub:dispatch", "hub:read", "health:read"}
	case "guest":
		return []string{"health:read"}
	case "user":
		return []string{"privacy:mask", "health:read"}
	default:
		return []string{"health:read"}
	}
}

// KnownRoles 为系统预置角色白名单（与两份 security.md 的角色矩阵保持一致）。
var KnownRoles = []string{
	"admin", "operator", "data-engineer", "compliance-officer",
	"auditor", "developer", "guest", "user",
}

// IsValidRole 判定角色是否在预置白名单内。自由文本角色会造成审计口径混乱与
// 隐式降权（未命中 switch 仅得 health:read），因此注册与改权均强制校验。
func IsValidRole(role string) bool {
	for _, r := range KnownRoles {
		if r == role {
			return true
		}
	}
	return false
}

// IsPrivilegedRole 检查角色是否为特权管理员类角色。
func IsPrivilegedRole(role string) bool {
	switch role {
	case "admin", "operator", "compliance-officer", "auditor":
		return true
	default:
		return false
	}
}

// managementScopes 为管理类 scope 全集：仅特权角色（IsPrivilegedRole）可持有。
var managementScopes = map[string]bool{
	"*": true, "admin": true, "user:admin": true, "ops:admin": true, "hub:admin": true,
}

// validateRoleScopeConsistency 校验角色与 scope 的一致性，**注册与改权共用同一口径**。
//
// 非特权角色不得携带管理类 scope，即使由管理员显式指定也必须角色匹配；否则会出现
// 「guest 持 `*`」这类破坏角色矩阵与审计口径的隐式提权（改权路径历史上缺失本校验，
// 与注册路径口径分叉）。
func validateRoleScopeConsistency(role string, scopes []string) error {
	if IsPrivilegedRole(role) {
		return nil
	}
	for _, sc := range scopes {
		if managementScopes[sc] {
			return fmt.Errorf("%w: role %q cannot hold management scope %q", ErrForbiddenScope, role, sc)
		}
	}
	return nil
}

// HasAdminCapability 判定用户是否具备用户管理能力（用于「最后一个管理员」保护）。
func HasAdminCapability(u *User) bool {
	if u == nil || u.Status != UserStatusActive {
		return false
	}
	if u.Role == "admin" {
		return true
	}
	for _, s := range u.Scopes {
		if s == "*" || s == "user:admin" {
			return true
		}
	}
	return false
}

// NormalizeUsername 归一化用户名（去首尾空白 + 转小写）。
// 不区分大小写可防止 "Alice" 与 "alice" 被当作两个账号（账号唯一性与撞名混淆）。
func NormalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// ============================================================================
// 错误与安全常数定义
// ============================================================================

var (
	ErrUserAlreadyExists    = errors.New("auth: user already exists")
	ErrUserNotFound         = errors.New("auth: user not found")
	ErrUserDisabled         = errors.New("auth: user account is disabled")
	ErrInvalidPassword      = errors.New("auth: invalid password")
	ErrInvalidUsername      = errors.New("auth: username must be 3-32 alphanumeric or underscore characters")
	ErrPasswordTooShort     = errors.New("auth: password must be at least 8 characters")
	ErrPasswordTooLong      = errors.New("auth: password must not exceed 72 bytes (bcrypt input limit)")
	ErrPasswordWeak         = errors.New("auth: password must contain at least 3 classes of characters (uppercase, lowercase, digits, special characters)")
	ErrPasswordContainsName = errors.New("auth: password must not contain the username (in either order or case)")
	ErrPasswordBlacklisted  = errors.New("auth: password appears in the common weak-password dictionary")
	ErrPasswordReused       = errors.New("auth: new password must not reuse any of the last 3 passwords")
	ErrAccountLocked        = errors.New("auth: account temporarily locked due to too many failed attempts")
	ErrPasswordSame         = errors.New("auth: new password must be different from old password")
	ErrKeyNotFound          = errors.New("auth: api key not found")
	ErrSessionNotFound      = errors.New("auth: session not found")
	ErrForbiddenScope       = errors.New("auth: requested scope exceeds user permission")
	ErrUnauthorized         = errors.New("auth: unauthorized action")
	ErrInvalidRole          = errors.New("auth: unknown role")
	ErrInvalidStatus        = errors.New("auth: status must be 'active' or 'disabled'")
	ErrInvalidKeyName       = errors.New("auth: key_name must be 1-64 characters of [A-Za-z0-9._-]")
	ErrInvalidDisplayName   = errors.New("auth: display_name must not exceed 64 characters or contain control characters")
	ErrInvalidScope         = errors.New("auth: scope must be 1-64 characters of [A-Za-z0-9:_.*-]")
	ErrTooManyScopes        = errors.New("auth: too many scopes requested")
	ErrTooManyKeys          = errors.New("auth: api key quota exceeded for this user")
	ErrInvalidTTL           = errors.New("auth: ttl_seconds must be between 0 (default 30d) and 90 days")
	ErrLastAdmin            = errors.New("auth: operation refused because it would remove the last active administrator")
	ErrSelfRegisterDisabled = errors.New("auth: self-service registration is disabled; ask an administrator to create the account")
	// ErrBootstrapClosed 引导窗口已关闭：首个管理员既已创建，「用户库为空」这一免认证开户
	// 通道即永久失效（引导判定与写入同锁，见 UserStore.RegisterBootstrapAdmin）。
	ErrBootstrapClosed = errors.New("auth: bootstrap window is closed; the first administrator already exists")
	// ErrPasswordChangedConcurrently 改密期间口令已被并发修改，锁外 bcrypt 校验结论失效。
	ErrPasswordChangedConcurrently = errors.New("auth: password was changed concurrently; please retry with the current password")
)

const (
	// MaxFailedLoginAttempts 等保三级 G-03：连续失败阈值。
	MaxFailedLoginAttempts = 5
	// AccountLockoutDuration 等保三级 G-03：触发阈值后的锁定时长。
	AccountLockoutDuration = 15 * time.Minute
	// MinPasswordLength / MaxPasswordLength 等保三级 G-04 口令长度下限；上限取 bcrypt 的
	// 72 字节输入上限——超出部分会被 bcrypt **静默截断**，导致两个前 72 字节相同的不同口令等价。
	MinPasswordLength = 8
	MaxPasswordLength = 72
	// PasswordHistoryDepth 禁止重用的历史口令个数。
	PasswordHistoryDepth = 3
	// MaxAPIKeysPerUser 单用户活跃 API Key 上限（防密钥表无界膨胀；认证为 O(n) 常量时间比对）。
	MaxAPIKeysPerUser = 32
	// MaxSessionsPerUser 单用户并发登录会话上限，超出时淘汰最早会话。
	MaxSessionsPerUser = 8
	// DefaultSessionTTL / MaxSessionTTL 登录会话 Token 有效期（内存态，不落盘）。
	DefaultSessionTTL = 24 * time.Hour
	MaxSessionTTL     = 24 * time.Hour
	// DefaultAPIKeyTTL / MaxAPIKeyTTL 等保三级 G-14：密钥必须具备有效期，不提供「永不过期」默认值。
	DefaultAPIKeyTTL = 30 * 24 * time.Hour
	MaxAPIKeyTTL     = 90 * 24 * time.Hour
	// MaxScopeCount 单请求可提交的 scope 上限。
	MaxScopeCount = 64
	// MaxDisplayNameLength / MaxKeyNameLength 输入长度上限（防日志注入与存储膨胀）。
	MaxDisplayNameLength = 64
	MaxKeyNameLength     = 64
	// LoginThrottleWindow / LoginThrottleMaxPerIP 登录端点每 IP 固定窗口限速，
	// 缓解「多账号口令喷洒」与「故意锁死管理员」两类拒绝服务。
	// 注册端点共用同一配额（独立计数）：公开注册每次都要跑一遍 bcrypt(cost=12)，
	// 不限速即成为**未认证 CPU 耗尽放大器**。
	LoginThrottleWindow   = time.Minute
	LoginThrottleMaxPerIP = 20
	// MaxPasswordAge 等保三级 G-04：身份鉴别信息应具有有效期并定期更换。
	// 超期口令在登录响应中以 password_expired 标记并输出审计告警（**不阻断登录**，
	// 避免唯一管理员因口令过期被永久锁死），由合规巡检驱动闭环改密。
	MaxPasswordAge = 90 * 24 * time.Hour
)

var weakPasswords = []string{
	"password", "12345678", "123456789", "1234567890", "qwerty123456", "admin123456",
	"iloveyou1234", "abc12345678", "password1234", "letmein12345",
	"welcome12345", "monkey123456", "dragon123456", "master123456",
	"123456789012", "0987654321ab", "password12345", "adminadmin12",
}

// IsValidUsername 校验用户名格式（3-32位，允许英文字母、数字、下划线与连字符）。
func IsValidUsername(username string) bool {
	if len(username) < 3 || len(username) > 32 {
		return false
	}
	for _, c := range username {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// ValidatePasswordStrength 校验密码复杂度（GB/T 22239-2019 等保三级 G-04）。
// 要求：长度 8~72 字节，且至少包含大写字母、小写字母、数字、特殊字符中的 3 类，且不包含用户名（含逆序）及弱口令字典项。
// 上限 72 字节是 bcrypt 的硬限制：超出部分被静默截断会使两个不同口令在认证上等价。
// 各类拒绝返回独立哨兵错误（ErrPasswordTooShort / TooLong / Weak / ContainsName / Blacklisted），
// 便于客户端给出可操作的整改提示，避免统一返回「字符类别不足」造成误导。
func ValidatePasswordStrength(password, username string) error {
	if len(password) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	if len(password) > MaxPasswordLength {
		return ErrPasswordTooLong
	}

	classes := 0
	hasLower, hasUpper, hasDigit, hasSpecial := false, false, false, false
	for _, c := range password {
		switch {
		case c >= 'a' && c <= 'z':
			hasLower = true
		case c >= 'A' && c <= 'Z':
			hasUpper = true
		case c >= '0' && c <= '9':
			hasDigit = true
		default:
			hasSpecial = true
		}
	}
	if hasLower {
		classes++
	}
	if hasUpper {
		classes++
	}
	if hasDigit {
		classes++
	}
	if hasSpecial {
		classes++
	}
	if classes < 3 {
		return ErrPasswordWeak
	}

	lower := strings.ToLower(password)
	if name := strings.ToLower(strings.TrimSpace(username)); name != "" {
		// 等保三级 G-04：口令不得包含用户名。同时校验用户名逆序，避免 "toorAdmin#1"
		// 这类把账号标识反转嵌入的规避写法。
		if strings.Contains(lower, name) || strings.Contains(lower, reverseString(name)) {
			return ErrPasswordContainsName
		}
	}
	for _, wp := range weakPasswords {
		if lower == wp || strings.HasPrefix(lower, wp) {
			return ErrPasswordBlacklisted
		}
	}
	return nil
}

// reverseString 逐 rune 反转字符串（用于口令与用户名逆序的包含性校验）。
func reverseString(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

// GenerateSecureToken 生成密码学安全的随机 Token 串（psk_ 前缀 + 32位 16进制）。
func GenerateSecureToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "psk_" + hex.EncodeToString(b), nil
}

// GenerateKeyID 生成 API Key 唯一标识 ID。
func GenerateKeyID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "key_" + hex.EncodeToString(b), nil
}

// ValidateDisplayName 校验展示名（可为空；不得超长或携带控制字符，防日志注入）。
func ValidateDisplayName(name string) error {
	if len(name) > MaxDisplayNameLength {
		return ErrInvalidDisplayName
	}
	for _, c := range name {
		if c < 0x20 || c == 0x7f {
			return ErrInvalidDisplayName
		}
	}
	return nil
}

// ValidateKeyName 校验 API Key 名称（1-64 位，仅允许 [A-Za-z0-9._-]）。
// 名称会拼接进密钥标识与审计日志，因此必须限制字符集，防止分隔符冲突与日志注入。
func ValidateKeyName(name string) error {
	if len(name) == 0 || len(name) > MaxKeyNameLength {
		return ErrInvalidKeyName
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-') {
			return ErrInvalidKeyName
		}
	}
	return nil
}

// ValidateScopes 校验自定义 Scope 集合（数量上限 + 字符集 + 去重）。
// 返回归一化后的副本；空集合返回 nil（表示沿用角色预置权限）。
func ValidateScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return nil, nil
	}
	if len(scopes) > MaxScopeCount {
		return nil, ErrTooManyScopes
	}
	seen := make(map[string]bool, len(scopes))
	out := make([]string, 0, len(scopes))
	for _, raw := range scopes {
		s := strings.TrimSpace(raw)
		if s == "" || len(s) > 64 {
			return nil, ErrInvalidScope
		}
		for _, c := range s {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
				c == ':' || c == '_' || c == '.' || c == '-' || c == '*') {
				return nil, ErrInvalidScope
			}
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out, nil
}
