// Package crypto 单元测试套件
//
// ==============================================================================
// 【测试套件设计目标与覆盖范围】
// 本测试文件验证 Package crypto（国密 SM4 分组密码与信封加密）的核心功能：
//  1. 【标准测试向量验证】：使用 GB/T 32907-2016 官方标准附录测试向量，验证底层 SM4 分组加解密的正确性；
//  2. 【信封加密完整生命周期】：
//     - 验证 EncryptString 生成符合 "enc:v2:<base64(salt||nonce||ct||tag)>" 规范格式的密文；
//     - 验证 DecryptString 使用正确密钥能够无损还原明文；
//     - 验证使用错误密钥解密时坚决报错拒绝（GCM 认证标签校验失败）；
//     - 验证历史 v1 密文（SHA-256 派生、无前缀 AAD）仍可读取，保证存量证据不失效；
//     - 验证「剥离版本前缀」不再被静默当作明文接受（消除降级通道）；
//     - 验证空密钥写入被拒绝（ErrEmptyKey），不再静默明文落盘；
//     - 验证逐记录随机 salt 使同一明文两次加密产出不同密文。
// ==============================================================================

package crypto

import (
	"bytes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestSM4StandardVector 验证底层 SM4 分组密码算法与 GB/T 32907-2016 标准测试向量的一致性。
//
// 测试用例数据源自国家密码标准 GB/T 32907-2016 附录 A.1：
// - 密钥 (Key):   0123456789abcdeffedcba9876543210
// - 明文 (Plain): 0123456789abcdeffedcba9876543210
// - 密文 (Cipher): a39c462feee46b964d80175d3294b6bd
//
// 断言逻辑：
// 1. 单分组加密输出必须与官方标准密文逐字节完全一致；
// 2. 单分组解密输出必须精确还原为原始输入明文。
func TestSM4StandardVector(t *testing.T) {
	keyHex := "0123456789abcdeffedcba9876543210"
	plainHex := "0123456789abcdeffedcba9876543210"
	cipherHex := "a39c462feee46b964d80175d3294b6bd"

	key, _ := hex.DecodeString(keyHex)
	plain, _ := hex.DecodeString(plainHex)
	expectedCipher, _ := hex.DecodeString(cipherHex)

	block, err := NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher failed: %v", err)
	}

	dst := make([]byte, BlockSize)
	block.Encrypt(dst, plain)
	if !bytes.Equal(dst, expectedCipher) {
		t.Fatalf("SM4 encryption mismatch: got %x, want %x", dst, expectedCipher)
	}

	dec := make([]byte, BlockSize)
	block.Decrypt(dec, dst)
	if !bytes.Equal(dec, plain) {
		t.Fatalf("SM4 decryption mismatch: got %x, want %x", dec, plain)
	}
}

// TestEnvelopeEncryption 验证基于国密 SM4-GCM 的端到端信封加解密功能与边界条件。
//
// 测试步骤与断言逻辑：
// 1. 【加密与格式校验】：
//   - 调用 EncryptString 对 JSON 明文进行加密；
//   - 断言密文包含 "enc:v1:" 前缀（IsEncrypted 返回 true）；
//   - 断言密文内容与明文完全不一致。
//
// 2. 【正确密钥解密】：
//   - 调用 DecryptString 传入正确 secret；
//   - 断言解密后的明文与原始输入逐字一致。
//
// 3. 【错误密钥拒绝】：
//   - 传入错误密钥 "wrong-key-value" 执行解密；
//   - 断言 GCM 认证标签验证失败并报错，杜绝伪造与数据污染。
//
// 4. 【降级通道已消除】：
//   - 传入未带任何版本前缀的字符串；
//   - 断言 DecryptString 返回 ErrUnencryptedValue，不再静默当作明文放行。
//
// 5. 【空密钥拒绝写入】：
//   - 传入空密钥 "" 调用 EncryptString；
//   - 断言返回 ErrEmptyKey 且不产出任何密文。
func TestEnvelopeEncryption(t *testing.T) {
	secret := "privshield-master-key-2026"
	plaintext := "{\"patient_name\":\"张三\",\"id_card\":\"510101199001011234\"}"

	// 1. Encrypt and verify format / 1. 加密并验证密文格式与前缀
	encrypted, err := EncryptString(plaintext, secret)
	if err != nil {
		t.Fatalf("EncryptString failed: %v", err)
	}
	if !strings.HasPrefix(encrypted, EncryptedPrefixV2) {
		t.Fatalf("expected encrypted string to have prefix %q, got %q", EncryptedPrefixV2, encrypted)
	}
	if !IsEncrypted(encrypted) {
		t.Fatalf("IsEncrypted rejected a valid v2 envelope: %q", encrypted)
	}
	if encrypted == plaintext {
		t.Fatal("ciphertext must not match plaintext")
	}

	// 2. Decrypt with correct key / 2. 正确密钥解密验证
	decrypted, err := DecryptString(encrypted, secret)
	if err != nil {
		t.Fatalf("DecryptString failed: %v", err)
	}
	if decrypted != plaintext {
		t.Fatalf("decrypted text mismatch: got %q, want %q", decrypted, plaintext)
	}

	// 3. Decrypt with wrong key must fail / 3. 错误密钥解密必须被拦截
	_, errWrongKey := DecryptString(encrypted, "wrong-key-value")
	if errWrongKey == nil {
		t.Fatal("expected decryption failure with incorrect key")
	}

	// 4. Unprefixed value is refused, not silently treated as plaintext / 4. 无前缀值必须被拒绝
	if out, err := DecryptString("legacy_unencrypted_sample", secret); !errors.Is(err, ErrUnencryptedValue) {
		t.Fatalf("expected ErrUnencryptedValue, got out=%q err=%v", out, err)
	}

	// 5. Empty key aborts encryption / 5. 空密钥拒绝加密
	if out, err := EncryptString(plaintext, ""); !errors.Is(err, ErrEmptyKey) || out != "" {
		t.Fatalf("expected ErrEmptyKey with empty output, got out=%q err=%v", out, err)
	}
}

// TestEnvelopeVersionDowngradeRejected 验证「剥离/改写版本前缀」无法让密文被静默接受：
// v2 密文的前缀参与 GCM AAD，去掉前缀后既不通过 v1 认证也不被当作明文透传。
func TestEnvelopeVersionDowngradeRejected(t *testing.T) {
	const secret = "kms-held-passphrase"
	v2, err := EncryptString("sensitive-diagnosis-text", secret)
	if err != nil {
		t.Fatalf("EncryptString failed: %v", err)
	}

	if _, err := DecryptString(strings.TrimPrefix(v2, EncryptedPrefixV2), secret); !errors.Is(err, ErrUnencryptedValue) {
		t.Fatalf("stripping the version prefix must fail closed, got %v", err)
	}

	// 前缀改写为 v1 后仍不得解出内容（AAD 绑定 + 派生方式不同）
	spoofer := EncryptedPrefix + strings.TrimPrefix(v2, EncryptedPrefixV2)
	if out, err := DecryptString(spoofer, secret); err == nil {
		t.Fatalf("prefix substitution must not yield plaintext, got %q", out)
	}
}

// TestEnvelopeLegacyV1Readable 验证存量 v1 密文（SHA-256[:16] 派生、无 AAD）在新实现下仍可解密，
// 保证整改不会使历史存证样本失去取证效力。
func TestEnvelopeLegacyV1Readable(t *testing.T) {
	const (
		secret    = "legacy-master-key"
		plaintext = "{\"settlement_seq_no\":\"YS20260831001\"}"
	)

	block, err := NewCipher(DeriveKey(secret))
	if err != nil {
		t.Fatalf("NewCipher failed: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("NewGCM failed: %v", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		t.Fatalf("nonce: %v", err)
	}
	legacy := EncryptedPrefix + base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plaintext), nil))

	out, err := DecryptString(legacy, secret)
	if err != nil {
		t.Fatalf("legacy v1 envelope must stay readable: %v", err)
	}
	if out != plaintext {
		t.Fatalf("legacy v1 mismatch: got %q want %q", out, plaintext)
	}
}

// TestEnvelopeSaltMakesCiphertextUnique 验证逐记录随机 salt：同一明文两次加密产出不同密文，
// 且两者都能被同一口令正确还原。
func TestEnvelopeSaltMakesCiphertextUnique(t *testing.T) {
	const (
		secret    = "per-record-salt-key"
		plaintext = "identical-plaintext"
	)

	a, err := EncryptString(plaintext, secret)
	if err != nil {
		t.Fatalf("EncryptString: %v", err)
	}
	b, err := EncryptString(plaintext, secret)
	if err != nil {
		t.Fatalf("EncryptString: %v", err)
	}
	if a == b {
		t.Fatal("ciphertexts must differ due to per-record salt/nonce")
	}
	for _, c := range []string{a, b} {
		if out, err := DecryptString(c, secret); err != nil || out != plaintext {
			t.Fatalf("decrypt mismatch: out=%q err=%v", out, err)
		}
	}
}

func TestRegisterKeyVersionsFromEnv(t *testing.T) {
	// 1. 空前缀时返回 0
	if n := RegisterKeyVersionsFromEnv(""); n != 0 {
		t.Errorf("expected 0 without prefix, got %d", n)
	}

	// 2. 传入服务专属前缀时正确注册多版本
	t.Setenv("TEST_APP_CRYPTO_KEY_V1", "secret-key-1")
	t.Setenv("TEST_APP_CRYPTO_KEY_V2", "secret-key-2")
	t.Setenv("TEST_APP_CRYPTO_ACTIVE_VERSION", "V2")

	n := RegisterKeyVersionsFromEnv("TEST_APP_CRYPTO_")
	if n < 2 {
		t.Errorf("expected at least 2 versions registered, got %d", n)
	}

	keyV1, err := LookupKeyVersion("V1")
	if err != nil || string(keyV1.Key) != "secret-key-1" {
		t.Errorf("key v1 lookup failed, got %v, err: %v", keyV1, err)
	}
	keyV2, err := LookupKeyVersion("V2")
	if err != nil || string(keyV2.Key) != "secret-key-2" || !keyV2.Active {
		t.Errorf("key v2 active lookup failed, got %v, err: %v", keyV2, err)
	}
}
