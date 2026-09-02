// userstore.go 实现用户存储与密码管理。
//
// 使用内存 map + sync.RWMutex 作为存储后端（开发模式），
// 密码使用 bcrypt (cost=12) 哈希存储，不可逆。

package auth

import (
	"errors"
	"fmt"
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
	ErrPasswordTooShort  = errors.New("auth: password must be at least 6 characters")
)

// ============================================================================
// UserStore 用户存储
// ============================================================================

// UserStore 管理用户注册与认证。
type UserStore struct {
	mu    sync.RWMutex
	users map[string]*User // username -> User
}

// NewUserStore 创建用户存储实例。
func NewUserStore() *UserStore {
	return &UserStore{
		users: make(map[string]*User),
	}
}

// Register 注册新用户。
// 密码使用 bcrypt (cost=12) 哈希后存储。
func (s *UserStore) Register(username, password, displayName, role string) (*User, error) {
	// 校验用户名
	if !isValidUsername(username) {
		return nil, ErrInvalidUsername
	}

	// 校验密码长度
	if len(password) < 6 {
		return nil, ErrPasswordTooShort
	}

	// 校验角色
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
func (s *UserStore) Authenticate(username, password string) (*User, error) {
	s.mu.RLock()
	user, exists := s.users[username]
	s.mu.RUnlock()

	if !exists {
		return nil, ErrUserNotFound
	}

	// bcrypt 校验密码（内部使用常量时间比较）
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidPassword
	}

	return user, nil
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
