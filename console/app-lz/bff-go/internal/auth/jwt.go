// Package auth 提供用户认证与角色权限管理能力。
//
// 核心组件：
//   - JWT: HMAC-SHA256 令牌签发与校验（纯标准库实现，零外部依赖）
//   - UserStore: 用户存储（内存 + 可选持久化，bcrypt 密码哈希）
//   - Middleware: Gin 中间件，JWT 认证 + 角色注入
//   - Handlers: 注册 / 登录 / 当前用户 HTTP 端点
//
// 安全设计：
//  1. 密码使用 bcrypt (cost=12) 哈希存储，不可逆
//  2. JWT 签名使用 HMAC-SHA256，密钥最少 32 字符
//  3. 令牌默认 24 小时有效期，可配置
//  4. 常量时间比较防止时序攻击
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ============================================================================
// JWT 令牌结构
// ============================================================================

// Claims 表示 JWT 令牌中的载荷声明。
type Claims struct {
	Subject     string `json:"sub"`          // 用户名
	Role        string `json:"role"`         // 角色: "user" | "admin"
	DisplayName string `json:"display_name"` // 显示名称
	IssuedAt    int64  `json:"iat"`          // 签发时间 (Unix)
	ExpiresAt   int64  `json:"exp"`          // 过期时间 (Unix)
}

// Valid 校验令牌是否过期。
func (c *Claims) Valid() bool {
	return time.Now().Unix() < c.ExpiresAt
}

// ============================================================================
// JWT 签发与校验
// ============================================================================

// JWTManager 管理 JWT 令牌的签发与校验。
type JWTManager struct {
	secret      []byte // HMAC 签名密钥
	expiryHours int    // 令牌有效期（小时）
}

// NewJWTManager 创建 JWT 管理器。
// secret 最少 32 字符，expiryHours 为令牌有效小时数（默认 24）。
func NewJWTManager(secret string, expiryHours int) (*JWTManager, error) {
	if len(secret) < 32 {
		return nil, errors.New("auth: JWT secret must be at least 32 characters")
	}
	if expiryHours <= 0 {
		expiryHours = 24
	}
	return &JWTManager{
		secret:      []byte(secret),
		expiryHours: expiryHours,
	}, nil
}

// GenerateToken 为指定用户签发 JWT 令牌。
func (m *JWTManager) GenerateToken(username, role, displayName string) (string, error) {
	now := time.Now()
	claims := Claims{
		Subject:     username,
		Role:        role,
		DisplayName: displayName,
		IssuedAt:    now.Unix(),
		ExpiresAt:   now.Add(time.Duration(m.expiryHours) * time.Hour).Unix(),
	}

	// Header: {"alg":"HS256","typ":"JWT"}
	header := base64URLEncode([]byte(`{"alg":"HS256","typ":"JWT"}`))

	// Payload: claims JSON
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("auth: marshal claims: %w", err)
	}
	payload := base64URLEncode(payloadJSON)

	// Signature: HMAC-SHA256(header.payload, secret)
	signingInput := header + "." + payload
	sig := m.sign([]byte(signingInput))

	return signingInput + "." + sig, nil
}

// ValidateToken 校验并解析 JWT 令牌，返回载荷声明。
func (m *JWTManager) ValidateToken(tokenString string) (*Claims, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, errors.New("auth: invalid token format")
	}

	// 校验签名
	signingInput := parts[0] + "." + parts[1]
	expectedSig := m.sign([]byte(signingInput))
	if !hmac.Equal([]byte(parts[2]), []byte(expectedSig)) {
		return nil, errors.New("auth: invalid token signature")
	}

	// 解析 Payload
	payloadJSON, err := base64URLDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("auth: decode payload: %w", err)
	}

	var claims Claims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, fmt.Errorf("auth: unmarshal claims: %w", err)
	}

	// 校验过期
	if !claims.Valid() {
		return nil, errors.New("auth: token expired")
	}

	return &claims, nil
}

// sign 使用 HMAC-SHA256 对输入数据签名。
func (m *JWTManager) sign(data []byte) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write(data)
	return base64URLEncode(mac.Sum(nil))
}

// ============================================================================
// Base64URL 编解码辅助函数
// ============================================================================

func base64URLEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

func base64URLDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
