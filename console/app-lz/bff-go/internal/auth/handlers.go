// handlers.go 实现用户注册、登录和当前用户信息 HTTP 端点。
//
// 端点清单：
//   POST /api/auth/register  → 用户注册（公开）
//   POST /api/auth/login     → 用户登录（公开）
//   GET  /api/auth/me        → 获取当前用户信息（需认证）

package auth

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Handlers 聚合认证相关的 HTTP 处理器。
type Handlers struct {
	store  *UserStore
	jwtMgr *JWTManager
	logger *slog.Logger
}

// NewHandlers 创建认证处理器实例。
func NewHandlers(store *UserStore, jwtMgr *JWTManager, logger *slog.Logger) *Handlers {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handlers{
		store:  store,
		jwtMgr: jwtMgr,
		logger: logger,
	}
}

// ============================================================================
// 请求/响应模型
// ============================================================================

// RegisterRequest 用户注册请求体。
type RegisterRequest struct {
	Username    string `json:"username" binding:"required"`
	Password    string `json:"password" binding:"required"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role" binding:"required"`
}

// LoginRequest 用户登录请求体。
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// AuthResponse 认证响应（注册/登录共用）。
type AuthResponse struct {
	Token string   `json:"token"`
	User  UserInfo `json:"user"`
}

// UserInfo 用户信息（不含密码哈希）。
type UserInfo struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	CreatedAt   string `json:"created_at,omitempty"`
}

// ============================================================================
// HTTP Handlers
// ============================================================================

// HandleRegister 处理用户注册请求。
//
// POST /api/auth/register
// Body: {"username": "...", "password": "...", "display_name": "...", "role": "user|admin"}
// Response 201: {"token": "...", "user": {...}}
func (h *Handlers) HandleRegister(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    "INVALID_REQUEST",
			"message": "Invalid request: " + err.Error(),
			"via":     "app-lz-bff",
		})
		return
	}

	// 注册新用户
	user, err := h.store.Register(req.Username, req.Password, req.DisplayName, req.Role)
	if err != nil {
		status := http.StatusBadRequest
		code := "REGISTRATION_FAILED"

		switch {
		case errors.Is(err, ErrUserAlreadyExists):
			code = "USER_EXISTS"
		case errors.Is(err, ErrInvalidUsername):
			code = "INVALID_USERNAME"
		case errors.Is(err, ErrPasswordTooShort):
			code = "PASSWORD_TOO_SHORT"
		case errors.Is(err, ErrInvalidRole):
			code = "INVALID_ROLE"
		}

		c.JSON(status, gin.H{
			"code":    code,
			"message": err.Error(),
			"via":     "app-lz-bff",
		})
		return
	}

	// 签发 JWT 令牌
	token, err := h.jwtMgr.GenerateToken(user.Username, user.Role, user.DisplayName)
	if err != nil {
		h.logger.Error("failed to generate JWT token", "error", err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    "TOKEN_GENERATION_FAILED",
			"message": "Failed to generate authentication token",
			"via":     "app-lz-bff",
		})
		return
	}

	h.logger.Info("new user registered",
		"username", user.Username,
		"role", user.Role,
		"display_name", user.DisplayName,
	)

	c.JSON(http.StatusCreated, AuthResponse{
		Token: token,
		User: UserInfo{
			Username:    user.Username,
			DisplayName: user.DisplayName,
			Role:        user.Role,
			CreatedAt:   user.CreatedAt.Format("2006-01-02T15:04:05Z"),
		},
	})
}

// HandleLogin 处理用户登录请求。
//
// POST /api/auth/login
// Body: {"username": "...", "password": "..."}
// Response 200: {"token": "...", "user": {...}}
func (h *Handlers) HandleLogin(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    "INVALID_REQUEST",
			"message": "Invalid request: " + err.Error(),
			"via":     "app-lz-bff",
		})
		return
	}

	// 校验用户名和密码
	user, err := h.store.Authenticate(req.Username, req.Password)
	if err != nil {
		status := http.StatusUnauthorized
		code := "AUTH_FAILED"

		if errors.Is(err, ErrUserNotFound) {
			code = "USER_NOT_FOUND"
		} else if errors.Is(err, ErrInvalidPassword) {
			code = "INVALID_PASSWORD"
		}

		c.JSON(status, gin.H{
			"code":    code,
			"message": "Authentication failed",
			"via":     "app-lz-bff",
		})
		return
	}

	// 签发 JWT 令牌
	token, err := h.jwtMgr.GenerateToken(user.Username, user.Role, user.DisplayName)
	if err != nil {
		h.logger.Error("failed to generate JWT token", "error", err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    "TOKEN_GENERATION_FAILED",
			"message": "Failed to generate authentication token",
			"via":     "app-lz-bff",
		})
		return
	}

	h.logger.Info("user logged in",
		"username", user.Username,
		"role", user.Role,
	)

	c.JSON(http.StatusOK, AuthResponse{
		Token: token,
		User: UserInfo{
			Username:    user.Username,
			DisplayName: user.DisplayName,
			Role:        user.Role,
		},
	})
}

// HandleMe 获取当前登录用户信息。
//
// GET /api/auth/me
// Authorization: Bearer <token>
// Response 200: {"username": "...", "display_name": "...", "role": "..."}
func (h *Handlers) HandleMe(c *gin.Context) {
	claims := GetClaims(c)
	if claims == nil {
		// 认证未启用时返回匿名用户
		c.JSON(http.StatusOK, UserInfo{
			Username:    "anonymous",
			DisplayName: "Anonymous User",
			Role:        "admin",
		})
		return
	}

	// 从 store 获取最新用户信息
	user, err := h.store.GetUser(claims.Subject)
	if err != nil {
		// 用户不存在（可能被删除），使用 claims 中的信息
		c.JSON(http.StatusOK, UserInfo{
			Username:    claims.Subject,
			DisplayName: claims.DisplayName,
			Role:        claims.Role,
		})
		return
	}

	c.JSON(http.StatusOK, UserInfo{
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Role:        user.Role,
		CreatedAt:   user.CreatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

// RegisterRoutes 在 Gin 路由引擎上注册认证相关路由。
func (h *Handlers) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/register", h.HandleRegister)
	r.POST("/login", h.HandleLogin)
	r.GET("/me", h.HandleMe)
}
