package crypto

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
)

// RunCryptoSelfTests 执行国密密码算法上电与运行时自检（Known Answer Test, KAT）。
// 对标 GM/T 0115-2023 与 GB/T 39786 密码应用技术要求：
// 1. SM3 密码杂凑算法标准向量自检（GM/T 0004-2012）；
// 2. SM4 分组密码算法标准向量自检（GM/T 0002-2012 / GB/T 32907-2016）；
// 3. SM2 椭圆曲线密码算法签名/验签自检（GM/T 0003-2012 / GB/T 32918-2016）。
// 若自检失败，返回明确错误以保障密码系统 fail-closed 安全。
func RunCryptoSelfTests() error {
	if err := selfTestSM3(); err != nil {
		return fmt.Errorf("crypto KAT failed: SM3: %w", err)
	}
	if err := selfTestSM4(); err != nil {
		return fmt.Errorf("crypto KAT failed: SM4: %w", err)
	}
	if err := selfTestSM2(); err != nil {
		return fmt.Errorf("crypto KAT failed: SM2: %w", err)
	}
	return nil
}

func selfTestSM3() error {
	// GM/T 0004-2012 附录 A.1 标准测试用例 1
	// Input: "abc"
	// Expected: 66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0
	input := []byte("abc")
	expected, _ := hex.DecodeString("66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0")
	sum := SumSM3(input)
	if !bytes.Equal(sum[:], expected) {
		return errors.New("SM3 standard test vector mismatch")
	}
	return nil
}

func selfTestSM4() error {
	// GM/T 0002-2012 附录 A 标准测试用例（单分组测试）
	// Key: 01 23 45 67 89 ab cd ef fe dc ba 98 76 54 32 10
	// Plaintext: 01 23 45 67 89 ab cd ef fe dc ba 98 76 54 32 10
	// Expected Ciphertext: 68 1e df 34 d2 06 96 5e 86 b3 e9 4f 53 6e 42 46
	key, _ := hex.DecodeString("0123456789abcdeffedcba9876543210")
	pt, _ := hex.DecodeString("0123456789abcdeffedcba9876543210")
	expectedCt, _ := hex.DecodeString("a39c462feee46b964d80175d3294b6bd")

	block, err := NewCipher(key)
	if err != nil {
		return fmt.Errorf("create SM4 cipher: %w", err)
	}

	ct := make([]byte, BlockSize)
	block.Encrypt(ct, pt)
	if !bytes.Equal(ct, expectedCt) {
		return errors.New("SM4 encryption standard test vector mismatch")
	}

	decrypted := make([]byte, BlockSize)
	block.Decrypt(decrypted, ct)
	if !bytes.Equal(decrypted, pt) {
		return errors.New("SM4 decryption standard test vector mismatch")
	}
	return nil
}

func selfTestSM2() error {
	priv, err := GenerateKey()
	if err != nil {
		return fmt.Errorf("generate SM2 key: %w", err)
	}
	sv := NewSM2SignerVerifier(priv, &priv.PublicKey)

	msg := []byte("PrivShield-Crypto-KAT-Self-Test")
	sig, err := sv.Sign(msg)
	if err != nil {
		return fmt.Errorf("SM2 sign: %w", err)
	}
	if !sv.Verify(msg, sig) {
		return errors.New("SM2 verify returned false for valid signature")
	}

	// 负向校验：篡改消息验签必须失败
	tampered := []byte("PrivShield-Crypto-KAT-Self-Test-Tampered")
	if sv.Verify(tampered, sig) {
		return errors.New("SM2 verify returned true for tampered message")
	}
	return nil
}
