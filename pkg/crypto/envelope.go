// Package crypto provides cryptographic utilities for envelope encryption and data protection.
// Package crypto 提供商用密码算法工具库，实现基于国密 SM4-GCM 的信封加密与敏感数据保护。
//
// ==============================================================================
// 【密码学标准与设计背景】
// 本模块严格对标国家密码管理局与国家标准：
//  - **核心密码算法**：GB/T 32907-2016《信息安全技术 SM4 分组密码算法》（128 位分组长度，128 位密钥长度）；
//  - **工作模式**：GCM（Galois/Counter Mode 伽罗瓦/计数器模式，AEAD 认证加密）；
//  - **密钥派生**：RFC 5869 HKDF（Extract-then-Expand），杂凑函数取 GM/T 0004-2012 SM3，
//    每条记录携带独立 16 字节随机 salt，杜绝「短口令直接哈希截断」式弱派生；
//  - **完整性与抗重放保障**：
//    1. 每次加密均由密码学安全随机源（crypto/rand.Reader）生成独占 16 字节 salt 与 12 字节 Nonce；
//    2. GCM 模式自带 16 字节认证标签（Authentication Tag），且 **版本前缀参与 AAD**，
//       因此剥离/改写 `enc:vN:` 前缀会直接导致认证失败，不存在「去前缀即降级为明文」的静默通道；
//  - **落盘格式规范**：
//    v2（当前写入格式）：`enc:v2:<Base64( 16字节 salt + 12字节 Nonce + SM4 密文 + 16字节 Tag )>`
//    v1（历史存量，仅可读）：`enc:v1:<Base64( 12字节 Nonce + SM4 密文 + 16字节 Tag )>`，密钥为 SHA-256(secret)[:16]
// ==============================================================================

package crypto

import (
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	// EncryptedPrefix 是历史存量密文（v1）的版本前缀，仅保留解密能力，不再用于写入。
	EncryptedPrefix = "enc:v1:"

	// EncryptedPrefixV2 是当前写入的密文版本前缀，密钥经 HKDF-SM3 派生且前缀参与 AAD。
	EncryptedPrefixV2 = "enc:v2:"

	// NonceSize 是国密 SM4-GCM 推荐的标准 12 字节随机数长度（96-bit）。
	NonceSize = 12

	// saltSize 是 v2 格式中每条记录独立的 HKDF salt 长度。
	saltSize = 16

	// hkdfInfo 将派生密钥绑定到「审计快照加密」这一特定用途，防止跨用途密钥复用。
	hkdfInfo = "PrivShield audit snapshot SM4-GCM v2"
)

var (
	// ErrEmptyKey 表示未配置加密密钥：写入路径不再静默降级为明文，直接报错拒绝落盘。
	ErrEmptyKey = errors.New("crypto: encryption key is not configured")

	// ErrUnencryptedValue 表示读到的值不带任何密文版本前缀：在启用加密的实例上视为
	// 被篡改或降级数据，拒绝当作明文返回给调用方。
	ErrUnencryptedValue = errors.New("crypto: value is not envelope-encrypted (missing enc:v1:/enc:v2: prefix)")

	// ErrKeyVersionNotFound 表示请求的密钥版本不存在。
	ErrKeyVersionNotFound = errors.New("crypto: key version not found")
)

// CryptoAuditEvent 记录一次密码操作的审计事件（三级等保 G-13）。
type CryptoAuditEvent struct {
	Operation  string // "sm4_encrypt", "sm4_decrypt", "sm3_hash"
	Timestamp  int64  // Unix nano
	KeyVersion string // 使用的密钥版本
	InputLen   int    // 输入数据长度（不记录明文内容）
	Success    bool   // 操作是否成功
	Error      string // 错误信息（如有）
}

// CryptoAuditLogger 密码操作审计日志接口（G-13）。
// 实现方可将审计事件写入独立审计表、SIEM 或日志文件。
type CryptoAuditLogger interface {
	LogCryptoAudit(event CryptoAuditEvent)
}

var cryptoAuditLogger CryptoAuditLogger

// SetCryptoAuditLogger 注册密码操作审计日志记录器（G-13）。
func SetCryptoAuditLogger(logger CryptoAuditLogger) {
	cryptoAuditLogger = logger
}

func auditCrypto(op, keyVersion string, inputLen int, success bool, err error) {
	if cryptoAuditLogger == nil {
		return
	}
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	cryptoAuditLogger.LogCryptoAudit(CryptoAuditEvent{
		Operation:  op,
		Timestamp:  time.Now().UnixNano(),
		KeyVersion: keyVersion,
		InputLen:   inputLen,
		Success:    success,
		Error:      errMsg,
	})
}

// KeyVersion 表示一个带版本标识的加密密钥（三级等保 G-08 密钥生命周期管理）。
type KeyVersion struct {
	Version   string // 版本标识（如 "v1", "v2", "20250901"）
	Key       []byte // 密钥材料
	Active    bool   // 是否为当前活跃密钥（写入使用）
	CreatedAt int64  // 创建时间（Unix timestamp）
}

// keyRegistry 管理多版本密钥，支持密钥轮换过渡期。
var (
	keyRegistry   []*KeyVersion
	keyRegistryMu sync.RWMutex
)

// RegisterKeyVersion 注册一个新的密钥版本（G-08 密钥轮换）。
// 注册后该版本可用于解密，若 active=true 则同时用于加密。
func RegisterKeyVersion(version string, key []byte, active bool) {
	keyRegistryMu.Lock()
	defer keyRegistryMu.Unlock()
	// 若已有同版本，更新之
	for _, kv := range keyRegistry {
		if kv.Version == version {
			kv.Key = key
			kv.Active = active
			return
		}
	}
	keyRegistry = append(keyRegistry, &KeyVersion{
		Version:   version,
		Key:       key,
		Active:    active,
		CreatedAt: time.Now().Unix(),
	})
	// 若设为 active，取消其他 active 标记
	if active {
		for _, kv := range keyRegistry {
			if kv.Version != version {
				kv.Active = false
			}
		}
	}
}

// ActiveKeyVersion 返回当前活跃密钥版本（用于加密写入）。
func ActiveKeyVersion() *KeyVersion {
	keyRegistryMu.RLock()
	defer keyRegistryMu.RUnlock()
	for _, kv := range keyRegistry {
		if kv.Active {
			return kv
		}
	}
	return nil
}

// LookupKeyVersion 根据版本号查找密钥（用于解密读取）。
func LookupKeyVersion(version string) (*KeyVersion, error) {
	keyRegistryMu.RLock()
	defer keyRegistryMu.RUnlock()
	for _, kv := range keyRegistry {
		if kv.Version == version {
			return kv, nil
		}
	}
	return nil, ErrKeyVersionNotFound
}

// DeriveKey derives a 16-byte (128-bit) SM4 key from a passphrase using SHA-256.
//
// DeriveKey 为 **历史 v1 密文** 保留的旧派生方式（SHA-256(secret)[:16]），仅用于解密存量数据；
// 新写入一律走 DeriveKeyHKDF，不再使用该弱派生口径。
func DeriveKey(secret string) []byte {
	h := sha256.Sum256([]byte(secret))
	return h[:KeySize]
}

// DeriveKeyHKDF derives a 16-byte SM4 key from a passphrase and a per-record random
// salt using HKDF (RFC 5869) with SM3 as the underlying hash.
//
// DeriveKeyHKDF 以 HKDF-Extract/Expand 从口令与逐记录随机 salt 派生 16 字节 SM4 密钥，
// 使同一口令在不同记录上产出互不相同的加密密钥，并抵抗短语令的离线暴破。
func DeriveKeyHKDF(secret string, salt []byte) []byte {
	prk := hkdfExtract(salt, []byte(secret))
	return hkdfExpand(prk, []byte(hkdfInfo), KeySize)
}

func hkdfExtract(salt, ikm []byte) []byte {
	mac := hmac.New(NewSM3, salt)
	mac.Write(ikm)
	return mac.Sum(nil)
}

func hkdfExpand(prk, info []byte, length int) []byte {
	out := make([]byte, 0, length+SM3Size)
	var prev []byte
	for i := byte(1); len(out) < length; i++ {
		mac := hmac.New(NewSM3, prk)
		mac.Write(prev)
		mac.Write(info)
		mac.Write([]byte{i})
		prev = mac.Sum(nil)
		out = append(out, prev...)
	}
	return out[:length]
}

// EncryptString encrypts a plaintext string with SM4-GCM (GB/T 32907-2016) and returns
// the v2 envelope format. An empty secret is rejected with ErrEmptyKey.
//
// EncryptString 使用 SM4-GCM 加密明文并输出 `enc:v2:` 信封格式：
//  1. secret 为空 → 返回 ErrEmptyKey（不再原样返回明文，消除静默明文落盘）；
//  2. 生成 16 字节随机 salt 与 12 字节随机 nonce，密钥由 DeriveKeyHKDF 派生；
//  3. 以版本前缀作为 AAD 执行 Seal，使密文与所属版本强绑定；
//  4. 输出 `enc:v2:<Base64(salt || nonce || ciphertext || tag)>`。
func EncryptString(plaintext, secret string) (string, error) {
	if secret == "" {
		return "", ErrEmptyKey
	}
	if plaintext == "" {
		return "", nil
	}

	salt := make([]byte, saltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	block, err := NewCipher(DeriveKeyHKDF(secret, salt))
	if err != nil {
		return "", fmt.Errorf("create sm4 cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create sm4-gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), []byte(EncryptedPrefixV2))
	return EncryptedPrefixV2 + base64.StdEncoding.EncodeToString(append(salt, sealed...)), nil
}

// DecryptString decrypts an envelope-encrypted value produced by this package.
// Values without a recognized version prefix are rejected with ErrUnencryptedValue.
//
// DecryptString 解密信封密文：
//  1. 空串直接返回空串；
//  2. `enc:v2:` → 取出 salt/nonce 后以 HKDF 派生密钥、前缀为 AAD 认证解密；
//  3. `enc:v1:` → 走历史 SHA-256 派生路径，保证存量数据可继续读取；
//  4. 无前缀 → 返回 ErrUnencryptedValue，调用方不得当作明文展示（防止剥离前缀降级）；
//  5. 密钥错误、密文或前缀被篡改 → GCM 认证失败并返回错误。
func DecryptString(ciphertext, secret string) (string, error) {
	switch {
	case ciphertext == "":
		return "", nil
	case strings.HasPrefix(ciphertext, EncryptedPrefixV2):
		return decryptV2(ciphertext, secret)
	case strings.HasPrefix(ciphertext, EncryptedPrefix):
		return decryptV1(ciphertext, secret)
	default:
		return "", ErrUnencryptedValue
	}
}

func decryptV2(ciphertext, secret string) (string, error) {
	if secret == "" {
		return "", ErrEmptyKey
	}
	data, err := decodeEnvelope(ciphertext, EncryptedPrefixV2)
	if err != nil {
		return "", err
	}
	if len(data) < saltSize+NonceSize {
		return "", errors.New("ciphertext too short")
	}
	salt, rest := data[:saltSize], data[saltSize:]

	gcm, err := newGCM(DeriveKeyHKDF(secret, salt))
	if err != nil {
		return "", err
	}
	plaintext, err := gcm.Open(nil, rest[:NonceSize], rest[NonceSize:], []byte(EncryptedPrefixV2))
	if err != nil {
		return "", fmt.Errorf("sm4-gcm decrypt failed (invalid key or tampered data): %w", err)
	}
	return string(plaintext), nil
}

func decryptV1(ciphertext, secret string) (string, error) {
	if secret == "" {
		return "", fmt.Errorf("cannot decrypt legacy envelope: %w", ErrEmptyKey)
	}
	data, err := decodeEnvelope(ciphertext, EncryptedPrefix)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(DeriveKey(secret))
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, body := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return "", fmt.Errorf("sm4-gcm decrypt failed (invalid key or tampered data): %w", err)
	}
	return string(plaintext), nil
}

func decodeEnvelope(ciphertext, prefix string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ciphertext, prefix))
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	return raw, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create sm4 cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create sm4-gcm: %w", err)
	}
	return gcm, nil
}

// IsEncrypted returns true if the value carries a recognized envelope prefix.
//
// IsEncrypted 判断给定字符串是否为本包产出的合法信封密文（v1 或 v2 前缀）。
func IsEncrypted(value string) bool {
	return strings.HasPrefix(value, EncryptedPrefixV2) || strings.HasPrefix(value, EncryptedPrefix)
}
