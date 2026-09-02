// Package crypto implements SM2 elliptic curve public key cryptography.
// SM2 非对称密码算法符合国家标准 GB/T 32918-2016《信息安全技术 SM2 椭圆曲线公钥密码算法》。
//
// ==============================================================================
// 【密码学标准与设计背景】
// SM2 算法基于椭圆曲线 sm2p256v1（256-bit 素域），提供：
//   - 数字签名：基于 SM3 杂凑的 ECDSA 变体，含用户标识 ZA 预处理；
//   - 公钥加密：ECIES 风格，KDF 由 SM3 驱动，密文格式为 C1‖C3‖C2（新国标）；
//   - 密钥交换（本包暂未实现，预留接口）。
// 本实现为纯 Go，不依赖任何外部 C 库或 crypto/elliptic，完全自主实现曲线运算。
// ==============================================================================
package crypto

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
)

// ---------------------------------------------------------------------------
// SM2 椭圆曲线 sm2p256v1 参数（GB/T 32918.1-2016 §4.1 / GM/T 0003.1-2012）
// 曲线方程：y² = x³ + ax + b  (mod p)，其中 a = −3（素域表示为 p − 3）。
// ---------------------------------------------------------------------------

var (
	// sm2P 是 sm2p256v1 曲线的素域模数 p（256-bit 素数）。
	sm2P, _ = new(big.Int).SetString("FFFFFFFEFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF00000000FFFFFFFFFFFFFFFF", 16)

	// sm2A 是曲线方程系数 a = p − 3（标准推荐值，加速点倍加倍运算）。
	sm2A = new(big.Int).Sub(sm2P, big.NewInt(3))

	// sm2B 是曲线方程系数 b（标准固定常数）。
	sm2B, _ = new(big.Int).SetString("28E9FA9E9D9F5E344D5A9E4BCF6509A7F39789F515AB8F92DDBCBD414D940E93", 16)

	// sm2N 是基点 G 的阶（素数），签名标量与加密随机数均取模 n。
	sm2N, _ = new(big.Int).SetString("FFFFFFFEFFFFFFFFFFFFFFFFFFFFFFFF7203DF6B21C6052B53BBF40939D54123", 16)

	// sm2Gx 是基点 G 的 X 坐标。
	sm2Gx, _ = new(big.Int).SetString("32C4AE2C1F1981195F9904466A39C9948FE30BBFF2660BE1715A4589334C74C7", 16)

	// sm2Gy 是基点 G 的 Y 坐标。
	sm2Gy, _ = new(big.Int).SetString("BC3736A2F4F6779C59BDCEE36B692153D0A9877CC62A474002DF32E52139F0A0", 16)

	// sm2Three 是常数 3 的 big.Int 表示，用于点倍加倍中的 3x² 运算，避免每次重新分配。
	sm2Three = big.NewInt(3)
)

// SM2 算法核心常量。
const (
	// SM2KeySize SM2 密钥与标量的字节长度（256-bit = 32 字节）。
	SM2KeySize = 32

	// SM2UncompressedLen SM2 非压缩公钥的字节长度（0x04 + 32 字节 X + 32 字节 Y = 65）。
	SM2UncompressedLen = 65

	// SM2SigPartLen SM2 签名分量 r / s 的固定字节长度（各 32 字节）。
	SM2SigPartLen = 32
)

// ---------------------------------------------------------------------------
// 密钥结构体
// ---------------------------------------------------------------------------

// SM2PublicKey 表示 SM2 公钥，即曲线上的一个点 (X, Y)。
type SM2PublicKey struct {
	X, Y *big.Int
}

// SM2PrivateKey 表示 SM2 私钥，包含标量 D 与对应公钥 PublicKey。
type SM2PrivateKey struct {
	D         *big.Int       // 私钥标量，满足 1 ≤ D ≤ n−2
	PublicKey SM2PublicKey   // 对应的公钥 Q = D·G
}

// ---------------------------------------------------------------------------
// 仿射坐标椭圆曲线点运算（基于素域 Fp）
// 以下函数均假设输入/输出点满足曲线方程 y² = x³ + ax + b (mod p)。
// 无穷远点（neutral element）以 x == nil, y == nil 表示。
// ---------------------------------------------------------------------------

// sm2IsInfinity 判断点 (x, y) 是否为无穷远点。
func sm2IsInfinity(x, y *big.Int) bool {
	return x == nil || y == nil
}

// sm2AffineAdd 执行仿射坐标下的椭圆曲线点加法 P + Q。
// 算法流程（GB/T 32918.1-2016 附录 A.1）：
//  1. 若 P 或 Q 为无穷远点，直接返回另一点；
//  2. 若 P.x == Q.x 且 P.y == Q.y，调用点倍加倍 sm2AffineDouble；
//  3. 若 P.x == Q.x 且 P.y ≠ Q.y（互逆点），返回无穷远点；
//  4. 计算斜率 λ = (y2 − y1) / (x2 − x1) mod p；
//  5. 计算 x3 = λ² − x1 − x2 mod p, y3 = λ(x1 − x3) − y1 mod p。
func sm2AffineAdd(x1, y1, x2, y2 *big.Int) (*big.Int, *big.Int) {
	// 若 P 为无穷远点，返回 Q（若 Q 也是无穷远点，则返回 nil, nil）
	if sm2IsInfinity(x1, y1) {
		if sm2IsInfinity(x2, y2) {
			return nil, nil
		}
		return new(big.Int).Set(x2), new(big.Int).Set(y2)
	}
	if sm2IsInfinity(x2, y2) {
		return new(big.Int).Set(x1), new(big.Int).Set(y1)
	}

	// 检查是否为同一点 → 走点倍加倍分支
	if x1.Cmp(x2) == 0 {
		if y1.Cmp(y2) == 0 {
			return sm2AffineDouble(x1, y1)
		}
		// 互逆点 P + (−P) = O
		return nil, nil
	}

	// λ = (y2 − y1) · (x2 − x1)⁻¹  mod p
	dy := new(big.Int).Sub(y2, y1)
	dx := new(big.Int).Sub(x2, x1)
	dx.ModInverse(dx, sm2P)
	lambda := new(big.Int).Mul(dy, dx)
	lambda.Mod(lambda, sm2P)

	// x3 = λ² − x1 − x2  mod p
	x3 := new(big.Int).Mul(lambda, lambda)
	x3.Sub(x3, x1)
	x3.Sub(x3, x2)
	x3.Mod(x3, sm2P)

	// y3 = λ(x1 − x3) − y1  mod p
	y3 := new(big.Int).Sub(x1, x3)
	y3.Mul(y3, lambda)
	y3.Sub(y3, y1)
	y3.Mod(y3, sm2P)

	return x3, y3
}

// sm2AffineDouble 执行仿射坐标下的椭圆曲线点倍加倍 2P。
// 算法流程（GB/T 32918.1-2016 附录 A.1）：
//  1. 若 P.y == 0，返回无穷远点；
//  2. 计算斜率 λ = (3x² + a) / (2y) mod p（利用 a = p − 3 简化）；
//  3. 计算 x3 = λ² − 2x mod p, y3 = λ(x − x3) − y mod p。
func sm2AffineDouble(x, y *big.Int) (*big.Int, *big.Int) {
	// 无穷远点的倍加倍仍为无穷远点
	if sm2IsInfinity(x, y) {
		return nil, nil
	}
	if y.Sign() == 0 {
		return nil, nil
	}

	// λ = (3x² + a) · (2y)⁻¹  mod p
	xSq := new(big.Int).Mul(x, x)
	xSq.Mod(xSq, sm2P)
	num := new(big.Int).Mul(sm2Three, xSq)
	num.Add(num, sm2A)
	num.Mod(num, sm2P)

	den := new(big.Int).Lsh(y, 1) // 2y
	den.ModInverse(den, sm2P)

	lambda := new(big.Int).Mul(num, den)
	lambda.Mod(lambda, sm2P)

	// x3 = λ² − 2x  mod p
	x3 := new(big.Int).Mul(lambda, lambda)
	x3.Sub(x3, new(big.Int).Lsh(x, 1))
	x3.Mod(x3, sm2P)

	// y3 = λ(x − x3) − y  mod p
	y3 := new(big.Int).Sub(x, x3)
	y3.Mul(y3, lambda)
	y3.Sub(y3, y)
	y3.Mod(y3, sm2P)

	return x3, y3
}

// sm2ScalarMul 执行椭圆曲线标量乘法 kP（double-and-add 算法，从高位到低位扫描）。
// 算法流程：
//  1. 初始化结果 R = O（无穷远点）；
//  2. 从 k 的最高有效位向最低位逐位扫描；
//  3. 每一位先执行点倍加倍 R = 2R，若该位为 1 则再执行点加法 R = R + P；
//  4. 返回最终点 R = kP。
//
// 时间复杂度为 O(log k) 次仿射点运算，适用于 256-bit 标量。
func sm2ScalarMul(px, py *big.Int, k *big.Int) (*big.Int, *big.Int) {
	if k.Sign() == 0 || sm2IsInfinity(px, py) {
		return nil, nil
	}

	// 对 k 取模 n，确保标量在 [0, n) 范围内
	kMod := new(big.Int).Mod(k, sm2N)
	if kMod.Sign() == 0 {
		return nil, nil
	}

	var rx, ry *big.Int // 累加点，初始为无穷远点

	for i := kMod.BitLen() - 1; i >= 0; i-- {
		// 点倍加倍
		rx, ry = sm2AffineDouble(rx, ry)
		// 若当前位为 1，执行点加法
		if kMod.Bit(i) == 1 {
			rx, ry = sm2AffineAdd(rx, ry, px, py)
		}
	}
	return rx, ry
}

// sm2ScalarBaseMul 执行基点标量乘法 kG（利用曲线固定基点 G 优化）。
// 等价于 sm2ScalarMul(Gx, Gy, k)，但直接引用全局基点常量。
func sm2ScalarBaseMul(k *big.Int) (*big.Int, *big.Int) {
	return sm2ScalarMul(sm2Gx, sm2Gy, k)
}

// ---------------------------------------------------------------------------
// 素域算术辅助函数
// ---------------------------------------------------------------------------

// sm2FieldNeg 计算 a mod p 的加法逆元（即 p − a mod p）。
func sm2FieldNeg(a *big.Int) *big.Int {
	r := new(big.Int).Neg(a)
	r.Mod(r, sm2P)
	return r
}

// sm2FieldInv 计算 a mod p 的乘法逆元（费马小定理 a⁻¹ ≡ a^(p−2) mod p）。
func sm2FieldInv(a *big.Int) *big.Int {
	return new(big.Int).ModInverse(a, sm2P)
}

// sm2FieldMod 将 a 约化到 [0, p) 范围内。
func sm2FieldMod(a *big.Int) *big.Int {
	r := new(big.Int).Set(a)
	r.Mod(r, sm2P)
	return r
}

// ---------------------------------------------------------------------------
// ZA 杂凑值与 SM3-KDF
// ---------------------------------------------------------------------------

// sm2DefaultIDA 是 SM2 签名/验签中用户 A 的默认标识字符串（16 字节 ASCII）。
// 标准规定 ENTLA = 0x0080（128 bit = 16 字节 × 8）。
var sm2DefaultIDA = []byte("1234567812345678")

// ComputeZA 计算 SM2 签名预处理中的用户标识杂凑值 ZA（GB/T 32918.2-2016 §5.5）。
//
// ZA = SM3(ENTLA ‖ IDA ‖ a ‖ b ‖ xG ‖ yG ‖ xA ‖ yA)
//
// 其中：
//   - ENTLA：用户标识 IDA 的位长度，以 2 字节大端编码（默认 IDA 为 16 字节 → ENTLA = 0x0080）；
//   - IDA ：用户标识字节串；
//   - a, b：曲线方程系数；
//   - xG, yG：基点 G 的坐标；
//   - xA, yA：用户公钥坐标。
//
// 返回 32 字节 SM3 摘要。
func ComputeZA(pub *SM2PublicKey, ida []byte) [SM3Size]byte {
	if ida == nil {
		ida = sm2DefaultIDA
	}

	entla := uint16(len(ida) * 8)

	h := NewSM3()

	// 1. ENTLA（2 字节大端）
	var entlaBuf [2]byte
	binary.BigEndian.PutUint16(entlaBuf[:], entla)
	h.Write(entlaBuf[:])

	// 2. IDA
	h.Write(ida)

	// 3. a（32 字节，大端零填充至曲线域元素长度）
	h.Write(sm2PadScalar(sm2A))

	// 4. b
	h.Write(sm2PadScalar(sm2B))

	// 5. xG, yG
	h.Write(sm2PadScalar(sm2Gx))
	h.Write(sm2PadScalar(sm2Gy))

	// 6. xA, yA
	h.Write(sm2PadScalar(pub.X))
	h.Write(sm2PadScalar(pub.Y))

	var za [SM3Size]byte
	copy(za[:], h.Sum(nil))
	return za
}

// sm2KDF 实现 SM2 标准的密钥派生函数 KDF（GB/T 32918.4-2016 附录 D）。
//
// KDF(Z, klen)：基于 SM3 杂凑的计数器模式密钥派生。
//   - 输入 Z 为共享秘密字节串，klen 为期望输出字节长度；
//   - 输出 klen 字节密钥材料；
//   - 计数器 ct 从 1 递增至 ⌈klen/32⌉，每轮计算 SM3(Z ‖ ct) 并拼接；
//   - 若所有轮次输出全零（概率极低），则返回 nil 表示失败，调用方应更换随机数重试。
func sm2KDF(z []byte, klen int) []byte {
	out := make([]byte, 0, klen)
	ct := uint32(1)

	for len(out) < klen {
		h := NewSM3()
		h.Write(z)

		var ctBuf [4]byte
		binary.BigEndian.PutUint32(ctBuf[:], ct)
		h.Write(ctBuf[:])

		out = append(out, h.Sum(nil)...)
		ct++

		// 计数器溢出保护：ct > 2^32 − 1 表示 KDF 失败
		if ct == 0 {
			return nil
		}
	}

	return out[:klen]
}

// sm2PadScalar 将 big.Int 编码为 32 字节大端字节串，不足部分左侧补零。
// 用于将曲线参数 a、b、G 坐标等写入 SM3 杂凑输入。
func sm2PadScalar(v *big.Int) []byte {
	buf := make([]byte, SM2KeySize)
	b := v.Bytes()
	if len(b) > SM2KeySize {
		// 理论不会发生，曲线参数均不超过 256 bit
		b = b[len(b)-SM2KeySize:]
	}
	copy(buf[SM2KeySize-len(b):], b)
	return buf
}

// ---------------------------------------------------------------------------
// 密钥生成
// ---------------------------------------------------------------------------

// GenerateKey 生成一对 SM2 公私钥。
//
// 算法流程（GB/T 32918.3-2016 §6.1）：
//  1. 从密码学安全随机源生成随机整数 d ∈ [1, n−2]；
//  2. 计算公钥点 Q = dG（基点标量乘法）；
//  3. 返回私钥结构体（含 D = d 与公钥 PublicKey = Q）。
//
// 密钥空间大小为 n − 2 ≈ 2^256，暴力搜索不可行。
func GenerateKey() (*SM2PrivateKey, error) {
	// n − 2 为私钥上界
	nMinus2 := new(big.Int).Sub(sm2N, big.NewInt(2))

	var d *big.Int
	for {
		// 生成 32 字节随机数
		buf := make([]byte, SM2KeySize)
		if _, err := io.ReadFull(rand.Reader, buf); err != nil {
			return nil, fmt.Errorf("crypto/sm2: generate random bytes: %w", err)
		}
		d = new(big.Int).SetBytes(buf)

		// 校验 d ∈ [1, n−2]
		if d.Sign() > 0 && d.Cmp(nMinus2) <= 0 {
			break
		}
	}

	// 计算公钥 Q = dG
	qx, qy := sm2ScalarBaseMul(d)

	return &SM2PrivateKey{
		D: d,
		PublicKey: SM2PublicKey{
			X: qx,
			Y: qy,
		},
	}, nil
}

// ---------------------------------------------------------------------------
// 数字签名（GB/T 32918.2-2016）
// ---------------------------------------------------------------------------

// Sign 使用 SM2 私钥对消息摘要 e 进行签名（GB/T 32918.2-2016 §7.1）。
//
// 输入参数：
//   - priv：SM2 私钥；
//   - e   ：消息的 SM3 杂凑值（已转为 big.Int），即 e = SM3(ZA ‖ M) 的整数表示。
//
// 算法流程：
//  1. 生成随机数 k ∈ [1, n−1]；
//  2. 计算椭圆曲线点 (x1, y1) = kG；
//  3. 计算 r = (e + x1) mod n，若 r = 0 或 r + k = n 则回到步骤 1；
//  4. 计算 s = ((1 + d)⁻¹ · (k − r·d)) mod n，若 s = 0 则回到步骤 1；
//  5. 签名值为 (r, s)，各 32 字节大端编码。
//
// 注意：调用方应先使用 ComputeZA 计算用户标识杂凑，再拼接消息 M 做 SM3 得到 e。
// 也可使用 SignMessage 便捷函数一步完成。
func Sign(priv *SM2PrivateKey, e *big.Int) (r, s *big.Int, err error) {
	eMod := new(big.Int).Mod(e, sm2N)
	one := big.NewInt(1)
	nMinus1 := new(big.Int).Sub(sm2N, one)

	for {
		// 步骤 1：生成随机数 k ∈ [1, n−1]
		k, err := sm2RandomScalar()
		if err != nil {
			return nil, nil, err
		}
		_ = nMinus1 // k 已由 sm2RandomScalar 保证在 [1, n−1]

		// 步骤 2：计算 (x1, y1) = kG
		x1, _ := sm2ScalarBaseMul(k)
		if x1 == nil {
			continue // 极小概率：kG 为无穷远点，更换 k
		}
		x1.Mod(x1, sm2N)

		// 步骤 3：r = (e + x1) mod n
		r = new(big.Int).Add(eMod, x1)
		r.Mod(r, sm2N)

		// 检查 r = 0 或 r + k = n（标准规定的两个重试条件）
		if r.Sign() == 0 {
			continue
		}
		if new(big.Int).Add(r, k).Cmp(sm2N) == 0 {
			continue
		}

		// 步骤 4：s = (1 + d)⁻¹ · (k − r·d) mod n
		dPlus1 := new(big.Int).Add(priv.D, one)
		dPlus1Inv := new(big.Int).ModInverse(dPlus1, sm2N)
		if dPlus1Inv == nil {
			return nil, nil, errors.New("crypto/sm2: modular inverse of (1+d) does not exist")
		}

		rd := new(big.Int).Mul(r, priv.D)
		kMinusRd := new(big.Int).Sub(k, rd)
		s = new(big.Int).Mul(dPlus1Inv, kMinusRd)
		s.Mod(s, sm2N)

		if s.Sign() == 0 {
			continue
		}

		return r, s, nil
	}
}

// SignMessage 对原始消息执行完整的 SM2 签名流程（ZA 计算 + SM3 杂凑 + 签名）。
//
// 调用方无需手动计算 ZA 和 e，此函数自动完成：
//  1. 使用默认标识计算 ZA = SM3(ENTLA ‖ IDA ‖ a ‖ b ‖ xG ‖ yG ‖ xA ‖ yA)；
//  2. 计算 e = SM3(ZA ‖ msg)；
//  3. 调用 Sign 生成签名 (r, s)。
func SignMessage(priv *SM2PrivateKey, msg []byte) (r, s *big.Int, err error) {
	za := ComputeZA(&priv.PublicKey, nil)
	e := sm2ComputeE(za, msg)
	return Sign(priv, e)
}

// sm2ComputeE 根据 ZA 与原始消息计算签名输入值 e = int(SM3(ZA ‖ M))。
func sm2ComputeE(za [SM3Size]byte, msg []byte) *big.Int {
	h := NewSM3()
	h.Write(za[:])
	h.Write(msg)
	var digest [SM3Size]byte
	copy(digest[:], h.Sum(nil))
	return new(big.Int).SetBytes(digest[:])
}

// sm2RandomScalar 从密码学安全随机源生成 [1, n−1] 范围内的随机标量。
func sm2RandomScalar() (*big.Int, error) {
	nMinus1 := new(big.Int).Sub(sm2N, big.NewInt(1))

	for {
		buf := make([]byte, SM2KeySize)
		if _, err := io.ReadFull(rand.Reader, buf); err != nil {
			return nil, fmt.Errorf("crypto/sm2: generate random scalar: %w", err)
		}
		k := new(big.Int).SetBytes(buf)
		// 确保 k ∈ [1, n−1]
		if k.Sign() > 0 && k.Cmp(nMinus1) <= 0 {
			return k, nil
		}
	}
}

// ---------------------------------------------------------------------------
// 签名验证（GB/T 32918.2-2016）
// ---------------------------------------------------------------------------

// Verify 使用 SM2 公钥验证消息摘要 e 上的签名 (r, s)（GB/T 32918.2-2016 §7.2）。
//
// 算法流程：
//  1. 检查 r, s ∈ [1, n−1]；
//  2. 计算 t = (r + s) mod n，若 t = 0 则验证失败；
//  3. 计算椭圆曲线点 (x1, y1) = sG + tQ（Q 为公钥）；
//  4. 验证 R' = (e + x1) mod n == r。
//
// 返回 nil 表示签名合法，否则返回描述失败原因的错误。
func Verify(pub *SM2PublicKey, e *big.Int, r, s *big.Int) error {
	// 步骤 1：检查 r, s 范围
	one := big.NewInt(1)
	nMinus1 := new(big.Int).Sub(sm2N, one)
	if r.Cmp(one) < 0 || r.Cmp(nMinus1) > 0 {
		return errors.New("crypto/sm2: signature r out of range")
	}
	if s.Cmp(one) < 0 || s.Cmp(nMinus1) > 0 {
		return errors.New("crypto/sm2: signature s out of range")
	}

	// 步骤 2：t = (r + s) mod n
	t := new(big.Int).Add(r, s)
	t.Mod(t, sm2N)
	if t.Sign() == 0 {
		return errors.New("crypto/sm2: signature verification failed (t = 0)")
	}

	// 步骤 3：(x1, y1) = sG + tQ
	sgx, sgy := sm2ScalarBaseMul(s)
	tqx, tqy := sm2ScalarMul(pub.X, pub.Y, t)
	x1, y1 := sm2AffineAdd(sgx, sgy, tqx, tqy)

	if sm2IsInfinity(x1, y1) {
		return errors.New("crypto/sm2: signature verification failed (point at infinity)")
	}

	// 步骤 4：R' = (e + x1) mod n，验证 R' == r
	eMod := new(big.Int).Mod(e, sm2N)
	x1Mod := new(big.Int).Mod(x1, sm2N)
	rPrime := new(big.Int).Add(eMod, x1Mod)
	rPrime.Mod(rPrime, sm2N)

	if rPrime.Cmp(r) != 0 {
		return errors.New("crypto/sm2: signature verification failed")
	}

	return nil
}

// VerifyMessage 对原始消息执行完整的 SM2 签名验证流程（ZA 计算 + SM3 杂凑 + 验证）。
func VerifyMessage(pub *SM2PublicKey, msg []byte, r, s *big.Int) error {
	za := ComputeZA(pub, nil)
	e := sm2ComputeE(za, msg)
	return Verify(pub, e, r, s)
}

// ---------------------------------------------------------------------------
// 公钥加密（GB/T 32918.4-2016）
// ---------------------------------------------------------------------------

// Encrypt 使用 SM2 公钥加密明文消息（GB/T 32918.4-2016 §7.1）。
//
// 输出密文格式为新国标推荐的 C1‖C3‖C2：
//   - C1（65 字节）：随机椭圆曲线点 kG 的非压缩编码（0x04 ‖ x1 ‖ y1）；
//   - C3（32 字节）：SM3(x2 ‖ M ‖ y2)，其中 (x2, y2) = kQ；
//   - C2（len(M) 字节）：M ⊕ KDF(x2 ‖ y2, len(M))。
//
// 加密流程：
//  1. 生成随机数 k ∈ [1, n−1]；
//  2. 计算椭圆曲线点 C1 = kG；
//  3. 计算共享点 S = kQ = (x2, y2)；
//  4. 若 KDF(x2‖y2, klen) 输出全零则回到步骤 1（极小概率）；
//  5. 计算 C2 = M ⊕ KDF(x2‖y2, len(M))；
//  6. 计算 C3 = SM3(x2 ‖ M ‖ y2)；
//  7. 输出 C1 ‖ C3 ‖ C2。
func Encrypt(pub *SM2PublicKey, plaintext []byte) ([]byte, error) {
	msgLen := len(plaintext)
	if msgLen == 0 {
		return nil, errors.New("crypto/sm2: cannot encrypt empty message")
	}

	// 明文长度上限：KDF 计数器 32-bit，每轮输出 32 字节 → 最大 2^32 × 32 字节
	// 实际应用中受限于性能，此处不做硬性限制但应合理控制消息长度。

	nMinus1 := new(big.Int).Sub(sm2N, big.NewInt(1))

	for {
		// 步骤 1：生成随机数 k
		k, err := sm2RandomScalar()
		if err != nil {
			return nil, err
		}
		_ = nMinus1

		// 步骤 2：C1 = kG（非压缩格式 0x04 ‖ x ‖ y）
		c1x, c1y := sm2ScalarBaseMul(k)
		c1 := make([]byte, SM2UncompressedLen)
		c1[0] = 0x04
		c1xBytes := sm2PadScalar(c1x)
		c1yBytes := sm2PadScalar(c1y)
		copy(c1[1:33], c1xBytes)
		copy(c1[33:65], c1yBytes)

		// 步骤 3：共享点 S = kQ = (x2, y2)
		sx, sy := sm2ScalarMul(pub.X, pub.Y, k)
		x2Bytes := sm2PadScalar(sx)
		y2Bytes := sm2PadScalar(sy)

		// 步骤 4：KDF 密钥派生
		kdfInput := make([]byte, 0, SM2KeySize*2)
		kdfInput = append(kdfInput, x2Bytes...)
		kdfInput = append(kdfInput, y2Bytes...)
		t := sm2KDF(kdfInput, msgLen)

		// KDF 全零输出检查（概率极低，标准规定此时须重新生成 k）
		allZero := true
		for _, b := range t {
			if b != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			continue
		}

		// 步骤 5：C2 = M ⊕ KDF
		c2 := make([]byte, msgLen)
		for i := 0; i < msgLen; i++ {
			c2[i] = plaintext[i] ^ t[i]
		}

		// 步骤 6：C3 = SM3(x2 ‖ M ‖ y2)
		h := NewSM3()
		h.Write(x2Bytes)
		h.Write(plaintext)
		h.Write(y2Bytes)
		c3 := h.Sum(nil)

		// 步骤 7：输出 C1 ‖ C3 ‖ C2
		result := make([]byte, 0, len(c1)+len(c3)+len(c2))
		result = append(result, c1...)
		result = append(result, c3...)
		result = append(result, c2...)

		return result, nil
	}
}

// ---------------------------------------------------------------------------
// 私钥解密（GB/T 32918.4-2016）
// ---------------------------------------------------------------------------

// Decrypt 使用 SM2 私钥解密密文（GB/T 32918.4-2016 §7.2）。
//
// 输入密文格式为 C1‖C3‖C2（与 Encrypt 输出一致）。
//
// 解密流程：
//  1. 从密文中分离 C1（65 字节）、C3（32 字节）、C2（剩余字节）；
//  2. 验证 C1 对应的点位于 SM2 曲线上（防止无效曲线攻击）；
//  3. 计算共享点 S = dC1 = (x2', y2')；
//  4. 计算 t' = KDF(x2'‖y2', len(C2))，若全零则解密失败；
//  5. 恢复明文 M' = C2 ⊕ t'；
//  6. 计算 u = SM3(x2' ‖ M' ‖ y2')，与 C3 逐字节比较（常量时间）；
//  7. 若 u ≠ C3 则拒绝，否则返回 M'。
func Decrypt(priv *SM2PrivateKey, ciphertext []byte) ([]byte, error) {
	// 密文最短长度：C1(65) + C3(32) + 至少 1 字节 C2
	const minLen = SM2UncompressedLen + SM3Size + 1
	if len(ciphertext) < minLen {
		return nil, errors.New("crypto/sm2: ciphertext too short")
	}

	// 步骤 1：分离 C1, C3, C2
	c1 := ciphertext[:SM2UncompressedLen]
	c3 := ciphertext[SM2UncompressedLen : SM2UncompressedLen+SM3Size]
	c2 := ciphertext[SM2UncompressedLen+SM3Size:]

	// 检查 C1 首字节为非压缩点标识 0x04
	if c1[0] != 0x04 {
		return nil, errors.New("crypto/sm2: invalid C1 point format (expected 0x04)")
	}

	// 步骤 2：解析 C1 点坐标并验证在曲线上
	c1x := new(big.Int).SetBytes(c1[1:33])
	c1y := new(big.Int).SetBytes(c1[33:65])

	if !sm2IsOnCurve(c1x, c1y) {
		return nil, errors.New("crypto/sm2: C1 point is not on SM2 curve")
	}

	// 步骤 3：共享点 S = d · C1
	sx, sy := sm2ScalarMul(c1x, c1y, priv.D)
	x2Bytes := sm2PadScalar(sx)
	y2Bytes := sm2PadScalar(sy)

	// 步骤 4：KDF 密钥派生
	kdfInput := make([]byte, 0, SM2KeySize*2)
	kdfInput = append(kdfInput, x2Bytes...)
	kdfInput = append(kdfInput, y2Bytes...)
	t := sm2KDF(kdfInput, len(c2))
	if t == nil {
		return nil, errors.New("crypto/sm2: KDF failed during decryption")
	}

	// KDF 全零检查
	allZero := true
	for _, b := range t {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return nil, errors.New("crypto/sm2: KDF output is all zeros")
	}

	// 步骤 5：恢复明文 M' = C2 ⊕ t'
	plaintext := make([]byte, len(c2))
	for i := range c2 {
		plaintext[i] = c2[i] ^ t[i]
	}

	// 步骤 6：计算 u = SM3(x2' ‖ M' ‖ y2')
	h := NewSM3()
	h.Write(x2Bytes)
	h.Write(plaintext)
	h.Write(y2Bytes)
	u := h.Sum(nil)

	// 步骤 7：常量时间比较 u 与 C3（防止时序侧信道）
	if !sm2ConstantTimeEqual(u, c3) {
		return nil, errors.New("crypto/sm2: decryption failed (hash mismatch)")
	}

	return plaintext, nil
}

// sm2IsOnCurve 验证点 (x, y) 是否满足 SM2 曲线方程 y² ≡ x³ + ax + b (mod p)。
func sm2IsOnCurve(x, y *big.Int) bool {
	// y² mod p
	ySq := new(big.Int).Mul(y, y)
	ySq.Mod(ySq, sm2P)

	// x³ + ax + b mod p
	x3 := new(big.Int).Mul(x, x)
	x3.Mul(x3, x)
	x3.Mod(x3, sm2P)

	ax := new(big.Int).Mul(sm2A, x)
	ax.Mod(ax, sm2P)

	rhs := new(big.Int).Add(x3, ax)
	rhs.Add(rhs, sm2B)
	rhs.Mod(rhs, sm2P)

	return ySq.Cmp(rhs) == 0
}

// sm2ConstantTimeEqual 以常量时间比较两个等长字节切片是否相等。
// 避免比较过程中因提前返回而泄露的差异位位置信息（防时序攻击）。
func sm2ConstantTimeEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v uint8
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
