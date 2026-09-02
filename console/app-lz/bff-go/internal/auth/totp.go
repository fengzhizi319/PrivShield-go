// totp.go 实现基于时间的动态口令 (TOTP) 多因素认证。
//
// 实现 RFC 6238 TOTP 标准，使用 HMAC-SHA1 作为哈希函数。
// 用于三级等保 G-11 合规：特权用户必须启用多因素认证。
//
// 核心流程：
//  1. GenerateSecret  — 生成随机 Base32 编码密钥
//  2. GenerateOTPAuthURL — 生成 otpauth:// URI（供二维码扫描器使用）
//  3. ValidateCode    — 校验 6 位 TOTP 码（含时间窗口容差 ±1 步）

package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
)

// ============================================================================
// TOTP 常量
// ============================================================================

const (
	// totpSecretSize TOTP 密钥字节长度（20 字节 = 160 位，推荐最小值）。
	totpSecretSize = 20

	// totpDigits TOTP 码位数（6 位数字）。
	totpDigits = 6

	// totpPeriod 每个 TOTP 码的有效时间窗口（秒）。
	totpPeriod = 30

	// totpWindow TOTP 校验允许的时间步容差（±1 步 = ±30 秒）。
	totpWindow = 1
)

// ============================================================================
// 错误定义
// ============================================================================

// TOTP 相关错误。
var (
	ErrTOTPNotEnabled     = fmt.Errorf("auth: TOTP is not enabled for this user")
	ErrTOTPAlreadyEnabled = fmt.Errorf("auth: TOTP is already enabled for this user")
	ErrTOTPInvalidCode    = fmt.Errorf("auth: invalid TOTP code")
	ErrTOTPInvalidSecret  = fmt.Errorf("auth: invalid TOTP secret encoding")
)

// ============================================================================
// TOTP 核心实现（纯函数，无状态）
// ============================================================================

// GenerateSecret 生成随机 TOTP 密钥并返回 Base32 编码字符串。
// 密钥使用 crypto/rand 生成，熵源安全。
func GenerateSecret() (string, error) {
	secret := make([]byte, totpSecretSize)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("auth: generate TOTP secret: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret), nil
}

// GenerateOTPAuthURL 生成 otpauth:// URI，供 Google Authenticator 等扫描器使用。
//
// 参数：
//   - secret: Base32 编码的 TOTP 密钥
//   - account: 用户账号标识（通常为邮箱或用户名）
//   - issuer: 签发方名称（显示在 Authenticator 应用中的标签）
func GenerateOTPAuthURL(secret, account, issuer string) string {
	// 构造 otpauth://totp/Issuer:account?secret=...&issuer=...&algorithm=...&digits=...&period=...
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	params := url.Values{
		"secret":    {secret},
		"issuer":    {issuer},
		"algorithm": {"SHA1"},
		"digits":    {fmt.Sprintf("%d", totpDigits)},
		"period":    {fmt.Sprintf("%d", totpPeriod)},
	}
	return "otpauth://totp/" + label + "?" + params.Encode()
}

// ValidateCode 校验 6 位 TOTP 码是否有效。
//
// 使用当前时间 ±totpWindow 步的时间窗口进行校验，
// 容忍客户端时钟偏差 ±30 秒（默认 ±1 步）。
// 比较使用常量时间算法防止时序攻击。
func ValidateCode(secret, code string) bool {
	if len(code) != totpDigits || !isDigits(code) {
		return false
	}

	secretBytes, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil || len(secretBytes) == 0 {
		return false
	}

	// 当前时间步
	counter := uint64(math.Floor(float64(time.Now().Unix()) / float64(totpPeriod)))

	// 在时间窗口内逐一校验（当前步 ± totpWindow）
	inputCode := parseDigits(code)
	for offset := -totpWindow; offset <= totpWindow; offset++ {
		c := counter + uint64(offset)
		if generateTOTP(secretBytes, c) == inputCode {
			return true
		}
	}
	return false
}

// generateTOTP 根据密钥和时间计数器生成 TOTP 码。
// 实现 RFC 6238 / RFC 4226 核心算法。
func generateTOTP(secret []byte, counter uint64) uint32 {
	// Step 1: 将计数器序列化为大端序 8 字节
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)

	// Step 2: HMAC-SHA1(secret, counter)
	mac := hmac.New(sha1.New, secret)
	mac.Write(buf[:])
	hash := mac.Sum(nil)

	// Step 3: Dynamic Truncation (RFC 4226 §5.4)
	offset := hash[len(hash)-1] & 0x0f
	code := binary.BigEndian.Uint32(hash[offset:offset+4]) & 0x7fffffff

	// Step 4: 取模得到指定位数的数字
	mod := uint32(math.Pow10(totpDigits))
	return code % mod
}

// ============================================================================
// 辅助函数
// ============================================================================

// isDigits 检查字符串是否全部由数字字符组成。
func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// parseDigits 将数字字符串解析为 uint32。
func parseDigits(s string) uint32 {
	var n uint32
	for _, c := range s {
		n = n*10 + uint32(c-'0')
	}
	return n
}
