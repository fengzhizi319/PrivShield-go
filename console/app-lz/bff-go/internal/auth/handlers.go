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
	Username  string `json:"username" binding:"required"`
	Password  string `json:"password" binding:"required"`
	TOTPCode  string `json:"totp_code"` // 特权用户/admin 登录时必须提供
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
// TOTP 请求/响应模型
// ============================================================================

// EnableTOTPResponse 启用 TOTP 的响应体。
type EnableTOTPResponse struct {
	Secret  string `json:"secret"`   // Base32 编码的 TOTP 密钥
	AuthURL string `json:"auth_url"` // otpauth:// URI（供二维码扫描器使用）
	Enabled bool   `json:"enabled"`  // 是否已启用
	Via     string `json:"via"`      // 来源标识
}

// ValidateTOTPRequest TOTP 校验请求体。
type ValidateTOTPRequest struct {
	Code string `json:"code" binding:"required"` // 6 位 TOTP 码
}

// ValidateTOTPResponse TOTP 校验响应体。
type ValidateTOTPResponse struct {
	Valid   bool   `json:"valid"`   // 校验结果
	Message string `json:"message"` // 结果说明
	Via     string `json:"via"`     // 来源标识
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
		case errors.Is(err, ErrPasswordWeak):
			code = "PASSWORD_WEAK"
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
		if errors.Is(err, ErrAccountLocked) {
			h.logger.Warn("login attempt on locked account", "username", req.Username)
			c.JSON(http.StatusLocked, gin.H{
				"code":    "ACCOUNT_LOCKED",
				"message": "Account temporarily locked due to too many failed attempts. Try again later.",
				"via":     "app-lz-bff",
			})
			return
		}

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

	// G-11 特权用户双因素认证：admin 必须已启用 TOTP，且登录时提供有效 TOTP 码。
	if user.Role == "admin" {
		if !user.TOTPEnabled {
			h.logger.Warn("admin login rejected: TOTP not enabled", "username", user.Username)
			c.JSON(http.StatusForbidden, gin.H{
				"code":    "TOTP_REQUIRED",
				"message": "Admin users must enable TOTP before login. Use POST /api/auth/totp/enable first.",
				"via":     "app-lz-bff",
			})
			return
		}
		if req.TOTPCode == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    "TOTP_CODE_REQUIRED",
				"message": "TOTP code is required for admin login",
				"via":     "app-lz-bff",
			})
			return
		}
		if err := h.store.ValidateTOTP(user.Username, req.TOTPCode); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    "INVALID_TOTP_CODE",
				"message": "Invalid or expired TOTP code",
				"via":     "app-lz-bff",
			})
			return
		}
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

// HandleLogout 吊销当前用户的 JWT 令牌。
//
// POST /api/auth/logout
// Authorization: Bearer <token>
// Response 200: {"message": "Logged out", "via": "app-lz-bff"}
func (h *Handlers) HandleLogout(c *gin.Context) {
	token := extractBearerToken(c.GetHeader("Authorization"))
	if token != "" {
		h.jwtMgr.RevokeToken(token)
		claims := GetClaims(c)
		if claims != nil {
			h.logger.Info("user logged out", "username", claims.Subject)
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "Logged out successfully",
		"via":     "app-lz-bff",
	})
}

// HandleEnableTOTP 处理启用 TOTP 多因素认证请求。
//
// POST /api/auth/totp/enable
// Authorization: Bearer <token>
// Response 200: {"secret": "...", "auth_url": "...", "enabled": true, "via": "app-lz-bff"}
//
// 流程：
//  1. 从 JWT 中提取当前用户名
//  2. 为用户生成 TOTP 密钥并启用
//  3. 返回密钥和 otpauth:// URI（供扫描器生成二维码）
func (h *Handlers) HandleEnableTOTP(c *gin.Context) {
	claims := GetClaims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":    "UNAUTHORIZED",
			"message": "Unauthorized: authentication required",
			"via":     "app-lz-bff",
		})
		return
	}

	// 为用户生成 TOTP 密钥
	secret, err := h.store.EnableTOTP(claims.Subject)
	if err != nil {
		status := http.StatusBadRequest
		code := "TOTP_ENABLE_FAILED"

		if errors.Is(err, ErrTOTPAlreadyEnabled) {
			code = "TOTP_ALREADY_ENABLED"
		} else if errors.Is(err, ErrUserNotFound) {
			status = http.StatusNotFound
			code = "USER_NOT_FOUND"
		}

		c.JSON(status, gin.H{
			"code":    code,
			"message": err.Error(),
			"via":     "app-lz-bff",
		})
		return
	}

	// 生成 otpauth:// URI
	authURL := GenerateOTPAuthURL(secret, claims.Subject, "PrivShield")

	h.logger.Info("TOTP enabled for user",
		"username", claims.Subject,
	)

	c.JSON(http.StatusOK, EnableTOTPResponse{
		Secret:  secret,
		AuthURL: authURL,
		Enabled: true,
		Via:     "app-lz-bff",
	})
}

// HandleValidateTOTP 处理 TOTP 码校验请求。
//
// POST /api/auth/totp/validate
// Authorization: Bearer <token>
// Body: {"code": "123456"}
// Response 200: {"valid": true, "message": "...", "via": "app-lz-bff"}
//
// 流程：
//  1. 从 JWT 中提取当前用户名
//  2. 校验用户提交的 6 位 TOTP 码
//  3. 返回校验结果（true/false）
func (h *Handlers) HandleValidateTOTP(c *gin.Context) {
	claims := GetClaims(c)
	if claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":    "UNAUTHORIZED",
			"message": "Unauthorized: authentication required",
			"via":     "app-lz-bff",
		})
		return
	}

	var req ValidateTOTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    "INVALID_REQUEST",
			"message": "Invalid request: " + err.Error(),
			"via":     "app-lz-bff",
		})
		return
	}

	// 校验 TOTP 码
	err := h.store.ValidateTOTP(claims.Subject, req.Code)
	if err != nil {
		if errors.Is(err, ErrTOTPInvalidCode) {
			h.logger.Warn("TOTP validation failed",
				"username", claims.Subject,
				"reason", "invalid code",
			)
			c.JSON(http.StatusOK, ValidateTOTPResponse{
				Valid:   false,
				Message: "Invalid TOTP code",
				Via:     "app-lz-bff",
			})
			return
		}

		status := http.StatusBadRequest
		code := "TOTP_VALIDATION_FAILED"

		if errors.Is(err, ErrTOTPNotEnabled) {
			code = "TOTP_NOT_ENABLED"
		} else if errors.Is(err, ErrUserNotFound) {
			status = http.StatusNotFound
			code = "USER_NOT_FOUND"
		}

		c.JSON(status, gin.H{
			"code":    code,
			"message": err.Error(),
			"via":     "app-lz-bff",
		})
		return
	}

	h.logger.Info("TOTP validation successful",
		"username", claims.Subject,
	)

	c.JSON(http.StatusOK, ValidateTOTPResponse{
		Valid:   true,
		Message: "TOTP code validated successfully",
		Via:     "app-lz-bff",
	})
}

// RegisterRoutes 在 Gin 路由引擎上注册认证相关路由。
func (h *Handlers) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/register", h.HandleRegister)
	r.POST("/login", h.HandleLogin)
	r.POST("/logout", h.HandleLogout)
	r.GET("/me", h.HandleMe)
	r.POST("/totp/enable", h.HandleEnableTOTP)
	r.POST("/totp/validate", h.HandleValidateTOTP)
}
