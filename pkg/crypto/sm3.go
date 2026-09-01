// Package crypto implements SM3 cryptographic hash and SM4 block cipher.
// SM3 cryptographic hash complies with Chinese National Standard GB/T 32918.4-2016 and GM/T 0004-2012.
package crypto

import (
	"crypto/hmac"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"math/bits"
)

// SM3 算法核心常量（GB/T 32918.4-2016）。
const (
	// SM3BlockSize SM3 分组长度（字节），每 512-bit 为一个压缩函数输入块。
	SM3BlockSize = 64
	// SM3Size SM3 摘要输出长度（字节），即 256-bit。
	SM3Size = 32
)

// sm3IV 是 SM3 标准规定的 256-bit 初始向量 IV^(0)，定义在 GB/T 32918.4-2016 §5.3。
var sm3IV = [8]uint32{
	0x7380166f, 0x4914b2b9, 0x172442d7, 0xda8a0600,
	0xa96f30bc, 0x163138aa, 0xe38dee4d, 0xb0fb0e4e,
}

// sm3Digest 实现 hash.Hash 接口的 SM3 摘要计算状态机。
type sm3Digest struct {
	h   [8]uint32          // 当前 256-bit 中间哈希值（8 个 32-bit 字 A~H）
	x   [SM3BlockSize]byte // 未完整块的缓冲字节（不足 64 字节时暂存）
	nx  int                // 缓冲区 x 中已填充的字节数（0~63）
	len uint64             // 已写入的总字节数（用于终态填充长度编码）
}

// NewSM3 returns a new hash.Hash computing the SM3 checksum.
func NewSM3() hash.Hash {
	d := new(sm3Digest)
	d.Reset()
	return d
}

// SumSM3 returns the SM3 checksum of the data.
func SumSM3(data []byte) [SM3Size]byte {
	var d sm3Digest
	d.Reset()
	d.Write(data)
	return d.checkSum()
}

// SumSM3Hex returns the hex-encoded SM3 checksum of data.
func SumSM3Hex(data []byte) string {
	sum := SumSM3(data)
	return hex.EncodeToString(sum[:])
}

// HMACSM3 returns the keyed SM3 message authentication code (GB/T 32918 / GM/T 0004 based HMAC).
// HMACSM3 返回基于国密 SM3 的密钥化消息认证码，用于存证哈希链的「可验真不可伪造」锚定。
func HMACSM3(key, data []byte) [SM3Size]byte {
	mac := hmac.New(NewSM3, key)
	mac.Write(data)
	sum := mac.Sum(nil)
	var out [SM3Size]byte
	copy(out[:], sum)
	return out
}

// HMACSM3Hex returns the hex-encoded keyed SM3 MAC of data.
func HMACSM3Hex(key, data []byte) string {
	sum := HMACSM3(key, data)
	return hex.EncodeToString(sum[:])
}

// Reset 将摘要状态重置为初始向量 IV^(0)，清空缓冲区与长度计数器。
func (d *sm3Digest) Reset() {
	d.h = sm3IV
	d.nx = 0
	d.len = 0
}

func (d *sm3Digest) Size() int {
	return SM3Size
}

func (d *sm3Digest) BlockSize() int {
	return SM3BlockSize
}

// Write 将输入字节流 p 喂入 SM3 摘要计算。
// 执行逻辑：
// 1. 若缓冲区有残留字节，先补齐至 64 字节并执行一次压缩函数 block()；
// 2. 对剩余数据按 64 字节整块批量调用 block()；
// 3. 不足 64 字节的尾部暂存于缓冲区 x，等待后续 Write 或 Sum 时处理。
func (d *sm3Digest) Write(p []byte) (nn int, err error) {
	nn = len(p)
	d.len += uint64(nn)
	// 补齐缓冲区残留字节至完整块
	if d.nx > 0 {
		n := copy(d.x[d.nx:], p)
		d.nx += n
		if d.nx == SM3BlockSize {
			d.block(d.x[:])
			d.nx = 0
		}
		p = p[n:]
	}
	// 批量处理完整 64 字节块
	if len(p) >= SM3BlockSize {
		n := len(p) &^ (SM3BlockSize - 1)
		d.block(p[:n])
		p = p[n:]
	}
	// 尾部不足一块的字节暂存于缓冲区
	if len(p) > 0 {
		d.nx = copy(d.x[:], p)
	}
	return
}

func (d *sm3Digest) Sum(in []byte) []byte {
	d0 := *d
	hash := d0.checkSum()
	return append(in, hash[:]...)
}

// checkSum 执行 SM3 标准终态填充与输出（GB/T 32918.4-2016 §5.4）。
// 执行逻辑：
// 1. 在消息末尾追加 0x80 填充字节，后跟零字节，直到总长度 ≡ 448 (mod 512) 位（即 56 mod 64 字节）；
// 2. 追加 8 字节大端编码的原始消息总位长（len << 3）；
// 3. 将最终 8 个 32-bit 字 A~H 按大端序拼接为 256-bit 摘要输出。
func (d *sm3Digest) checkSum() [SM3Size]byte {
	lenBits := d.len << 3
	var pad [SM3BlockSize]byte
	pad[0] = 0x80
	// 填充至 56 mod 64 字节位置，为 8 字节长度编码预留空间
	if d.nx < 56 {
		d.Write(pad[0 : 56-d.nx])
	} else {
		d.Write(pad[0 : SM3BlockSize+56-d.nx])
	}
	// 追加 64-bit 大端消息总位长
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], lenBits)
	d.Write(lenBuf[:])

	var digest [SM3Size]byte
	for i, s := range d.h {
		binary.BigEndian.PutUint32(digest[i*4:], s)
	}
	return digest
}

// block 是 SM3 核心压缩函数 CF（Compression Function），对每个 512-bit 块执行消息扩展与 64 轮迭代。
// 算法流程（GB/T 32918.4-2016 §5.3）：
// 1. 消息扩展：将 16 个 32-bit 消息字扩展为 68 个字 W[0..67]，再派生 W'[0..63]；
// 2. 64 轮压缩：前 16 轮使用布尔函数 FF0/GG0（异或型），后 48 轮使用 FF1/GG1（择一型）；
// 3. 将压缩结果与上一轮中间值异或，更新 8 个状态字 A~H。
func (d *sm3Digest) block(p []byte) {
	for len(p) >= SM3BlockSize {
		var w [68]uint32
		var wPrime [64]uint32

		// 消息扩展第一阶段：直接读取 16 个 32-bit 大端消息字
		for i := 0; i < 16; i++ {
			w[i] = binary.BigEndian.Uint32(p[i*4:])
		}

		// 消息扩展第二阶段：递推计算 W[16..67]（含 P1 置换）
		for j := 16; j < 68; j++ {
			tmp := w[j-16] ^ w[j-9] ^ bits.RotateLeft32(w[j-3], 15)
			p1 := tmp ^ bits.RotateLeft32(tmp, 15) ^ bits.RotateLeft32(tmp, 23)
			w[j] = p1 ^ bits.RotateLeft32(w[j-13], 7) ^ w[j-6]
		}

		// 派生 W'[0..63] = W[j] ⊕ W[j+4]
		for j := 0; j < 64; j++ {
			wPrime[j] = w[j] ^ w[j+4]
		}

		a, b, c, dVal := d.h[0], d.h[1], d.h[2], d.h[3]
		e, f, g, hVal := d.h[4], d.h[5], d.h[6], d.h[7]

		// 前 16 轮压缩（0 ≤ j ≤ 15）：使用异或型布尔函数 FF0(X,Y,Z) = X⊕Y⊕Z, GG0(X,Y,Z) = X⊕Y⊕Z
		// 常量 T_j = 0x79CC4519
		for j := 0; j < 16; j++ {
			tj := uint32(0x79cc4519)
			ss1 := bits.RotateLeft32(bits.RotateLeft32(a, 12)+e+bits.RotateLeft32(tj, j), 7)
			ss2 := ss1 ^ bits.RotateLeft32(a, 12)
			tt1 := (a ^ b ^ c) + dVal + ss2 + wPrime[j]
			tt2 := (e ^ f ^ g) + hVal + ss1 + w[j]
			dVal = c
			c = bits.RotateLeft32(b, 9)
			b = a
			a = tt1
			hVal = g
			g = bits.RotateLeft32(f, 19)
			f = e
			e = tt2 ^ bits.RotateLeft32(tt2, 9) ^ bits.RotateLeft32(tt2, 17)
		}

		// 后 48 轮压缩（16 ≤ j ≤ 63）：使用择一型布尔函数 FF1(X,Y,Z) = (X&Y)|(X&Z)|(Y&Z), GG1(X,Y,Z) = (X&F)|(^X&G)
		// 常量 T_j = 0x7A879D8A
		for j := 16; j < 64; j++ {
			tj := uint32(0x7a879d8a)
			ss1 := bits.RotateLeft32(bits.RotateLeft32(a, 12)+e+bits.RotateLeft32(tj, j%32), 7)
			ss2 := ss1 ^ bits.RotateLeft32(a, 12)
			tt1 := ((a & b) | (a & c) | (b & c)) + dVal + ss2 + wPrime[j]
			tt2 := ((e & f) | (^e & g)) + hVal + ss1 + w[j]
			dVal = c
			c = bits.RotateLeft32(b, 9)
			b = a
			a = tt1
			hVal = g
			g = bits.RotateLeft32(f, 19)
			f = e
			e = tt2 ^ bits.RotateLeft32(tt2, 9) ^ bits.RotateLeft32(tt2, 17)
		}

		// 将压缩结果与上一轮中间值异或（Davies-Meyer 结构），更新 8 个状态字 A~H
		d.h[0] ^= a
		d.h[1] ^= b
		d.h[2] ^= c
		d.h[3] ^= dVal
		d.h[4] ^= e
		d.h[5] ^= f
		d.h[6] ^= g
		d.h[7] ^= hVal

		p = p[SM3BlockSize:]
	}
}
