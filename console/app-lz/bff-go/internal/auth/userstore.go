// userstore.go 实现用户存储与密码管理。
//
// 使用内存 map + sync.RWMutex 作为存储后端（开发模式），
// 密码使用 bcrypt (cost=12) 哈希存储，不可逆。

package auth

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ============================================================================
// 用户模型
// ============================================================================

// User 表示一个已注册的系统用户。
type User struct {
	Username     string    `json:"username"`     // 用户名（唯一标识）
	DisplayName  string    `json:"display_name"` // 显示名称
	Role         string    `json:"role"`         // 角色: "user" | "admin"
	PasswordHash string    `json:"-"`            // bcrypt 哈希（不序列化到 JSON）
	TOTPSecret   string    `json:"-"`            // TOTP 密钥（Base32 编码，不序列化到 JSON）
	TOTPEnabled  bool      `json:"totp_enabled"` // 是否已启用 TOTP 多因素认证
	CreatedAt    time.Time `json:"created_at"`   // 注册时间
}

// ============================================================================
// 错误定义
// ============================================================================

var (
	ErrUserAlreadyExists = errors.New("auth: user already exists")
	ErrUserNotFound      = errors.New("auth: user not found")
	ErrInvalidPassword   = errors.New("auth: invalid password")
	ErrInvalidRole       = errors.New("auth: invalid role (must be 'user' or 'admin')")
	ErrInvalidUsername   = errors.New("auth: username must be 3-32 alphanumeric characters")
	ErrPasswordTooShort  = errors.New("auth: password must be at least 12 characters")
	ErrPasswordWeak      = errors.New("auth: password must contain at least 3 of: uppercase, lowercase, digits, special characters")
	ErrAccountLocked     = errors.New("auth: account temporarily locked due to too many failed attempts")
)

const (
	maxFailedAttempts = 5
	lockoutDuration   = 15 * time.Minute
)

var weakPasswords = []string{
	"password", "1234567890", "qwerty123456", "admin1234567",
	"iloveyou1234", "abc123456789", "password1234", "letmein12345",
	"welcome12345", "monkey123456", "dragon123456", "master123456",
	"123456789012", "0987654321ab", "password12345", "adminadmin12",
}

// ============================================================================
// UserStore 用户存储
// ============================================================================

// UserStore 管理用户注册与认证。
type UserStore struct {
	mu             sync.RWMutex
	users          map[string]*User
	failedAttempts map[string]int       // username -> consecutive failures
	lockedUntil    map[string]time.Time // username -> lockout expiry
}

// NewUserStore 创建用户存储实例。
func NewUserStore() *UserStore {
	return &UserStore{
		users:          make(map[string]*User),
		failedAttempts: make(map[string]int),
		lockedUntil:    make(map[string]time.Time),
	}
}

// Register 注册新用户。
// 密码使用 bcrypt (cost=12) 哈希后存储。
// 密码策略（三级等保 G-04）：最少 12 位，至少包含 3 类字符（大写/小写/数字/特殊），不在弱密码字典中。
func (s *UserStore) Register(username, password, displayName, role string) (*User, error) {
	if !isValidUsername(username) {
		return nil, ErrInvalidUsername
	}

	if err := validatePasswordStrength(password, username); err != nil {
		return nil, err
	}

	if role != "user" && role != "admin" {
		return nil, ErrInvalidRole
	}

	// bcrypt 哈希密码 (cost=12)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, fmt.Errorf("auth: hash password: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 检查用户名是否已存在
	if _, exists := s.users[username]; exists {
		return nil, ErrUserAlreadyExists
	}

	user := &User{
		Username:     username,
		DisplayName:  displayName,
		Role:         role,
		PasswordHash: string(hash),
		CreatedAt:    time.Now(),
	}

	if user.DisplayName == "" {
		user.DisplayName = username
	}

	s.users[username] = user
	return user, nil
}

// Authenticate 校验用户名和密码，成功返回用户信息。
// 三级等保 G-03：连续 5 次失败锁定账号 15 分钟。
func (s *UserStore) Authenticate(username, password string) (*User, error) {
	s.mu.Lock()

	if until, locked := s.lockedUntil[username]; locked {
		if time.Now().Before(until) {
			s.mu.Unlock()
			return nil, ErrAccountLocked
		}
		delete(s.lockedUntil, username)
		s.failedAttempts[username] = 0
	}

	user, exists := s.users[username]
	if !exists {
		s.mu.Unlock()
		return nil, ErrUserNotFound
	}
	s.mu.Unlock()

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		s.recordFailedLogin(username)
		return nil, ErrInvalidPassword
	}

	s.resetFailedLogin(username)
	return user, nil
}

func (s *UserStore) recordFailedLogin(username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failedAttempts[username]++
	if s.failedAttempts[username] >= maxFailedAttempts {
		s.lockedUntil[username] = time.Now().Add(lockoutDuration)
	}
}

func (s *UserStore) resetFailedLogin(username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failedAttempts, username)
	delete(s.lockedUntil, username)
}

// IsLocked 检查账号是否处于锁定状态。
func (s *UserStore) IsLocked(username string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	until, locked := s.lockedUntil[username]
	return locked && time.Now().Before(until)
}

// GetUser 根据用户名获取用户信息。
func (s *UserStore) GetUser(username string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, exists := s.users[username]
	if !exists {
		return nil, ErrUserNotFound
	}
	return user, nil
}

// Count 返回已注册用户总数。
func (s *UserStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.users)
}

// EnableTOTP 为用户启用 TOTP 多因素认证。
// 生成新的 TOTP 密钥并返回，密钥同时存储在用户记录中。
func (s *UserStore) EnableTOTP(username string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, exists := s.users[username]
	if !exists {
		return "", ErrUserNotFound
	}

	if user.TOTPEnabled {
		return "", ErrTOTPAlreadyEnabled
	}

	// 生成 TOTP 密钥
	secret, err := GenerateSecret()
	if err != nil {
		return "", err
	}

	user.TOTPSecret = secret
	user.TOTPEnabled = true
	return secret, nil
}

// DisableTOTP 为用户禁用 TOTP 多因素认证。
func (s *UserStore) DisableTOTP(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, exists := s.users[username]
	if !exists {
		return ErrUserNotFound
	}

	if !user.TOTPEnabled {
		return ErrTOTPNotEnabled
	}

	user.TOTPSecret = ""
	user.TOTPEnabled = false
	return nil
}

// ValidateTOTP 校验用户的 TOTP 码。
// 仅当用户已启用 TOTP 时才能校验，否则返回 ErrTOTPNotEnabled。
func (s *UserStore) ValidateTOTP(username, code string) error {
	s.mu.RLock()
	user, exists := s.users[username]
	if !exists {
		s.mu.RUnlock()
		return ErrUserNotFound
	}

	if !user.TOTPEnabled {
		s.mu.RUnlock()
		return ErrTOTPNotEnabled
	}

	secret := user.TOTPSecret
	s.mu.RUnlock()

	// 校验 TOTP 码（使用常量时间比较）
	if !ValidateCode(secret, code) {
		return ErrTOTPInvalidCode
	}

	return nil
}

// ============================================================================
// 校验辅助函数
// ============================================================================

// isValidUsername 校验用户名是否合法（3-32 字符，字母数字下划线）。
func isValidUsername(username string) bool {
	if len(username) < 3 || len(username) > 32 {
		return false
	}
	for _, c := range username {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

// IsValidRole 校验角色标识是否合法。
func IsValidRole(role string) bool {
	return role == "user" || role == "admin"
}

// validatePasswordStrength 校验密码强度（三级等保 G-04）。
func validatePasswordStrength(password, username string) error {
	if len(password) < 12 {
		return ErrPasswordTooShort
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
	if username != "" && strings.Contains(lower, strings.ToLower(username)) {
		return ErrPasswordWeak
	}
	for _, wp := range weakPasswords {
		if lower == wp || strings.HasPrefix(lower, wp) {
			return ErrPasswordWeak
		}
	}
	return nil
}
