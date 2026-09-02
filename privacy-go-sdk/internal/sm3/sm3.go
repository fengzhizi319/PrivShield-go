// Package sm3 implements the SM3 cryptographic hash algorithm (GB/T 32918.4-2016).
// 内部包，供 privacy-go-sdk 各子模块使用，避免跨模块依赖。
package sm3

import (
	"encoding/binary"
	"hash"
	"math/bits"
)

const (
	BlockSize = 64
	Size      = 32
)

var iv = [8]uint32{
	0x7380166f, 0x4914b2b9, 0x172442d7, 0xda8a0600,
	0xa96f30bc, 0x163138aa, 0xe38dee4d, 0xb0fb0e4e,
}

type digest struct {
	h   [8]uint32
	x   [BlockSize]byte
	nx  int
	len uint64
}

// New returns a new hash.Hash computing the SM3 checksum.
func New() hash.Hash {
	d := new(digest)
	d.Reset()
	return d
}

func (d *digest) Reset() {
	d.h = iv
	d.nx = 0
	d.len = 0
}

func (d *digest) Size() int      { return Size }
func (d *digest) BlockSize() int { return BlockSize }

func (d *digest) Write(p []byte) (nn int, err error) {
	nn = len(p)
	d.len += uint64(nn)
	if d.nx > 0 {
		n := copy(d.x[d.nx:], p)
		d.nx += n
		if d.nx == BlockSize {
			d.compress(d.x[:])
			d.nx = 0
		}
		p = p[n:]
	}
	if len(p) >= BlockSize {
		n := len(p) &^ (BlockSize - 1)
		d.compress(p[:n])
		p = p[n:]
	}
	if len(p) > 0 {
		d.nx = copy(d.x[:], p)
	}
	return
}

func (d *digest) Sum(in []byte) []byte {
	d0 := *d
	hash := d0.checkSum()
	return append(in, hash[:]...)
}

func (d *digest) checkSum() [Size]byte {
	lenBits := d.len << 3
	var pad [BlockSize]byte
	pad[0] = 0x80
	if d.nx < 56 {
		d.Write(pad[0 : 56-d.nx])
	} else {
		d.Write(pad[0 : BlockSize+56-d.nx])
	}
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], lenBits)
	d.Write(lenBuf[:])

	var digest [Size]byte
	for i, s := range d.h {
		binary.BigEndian.PutUint32(digest[i*4:], s)
	}
	return digest
}

func (d *digest) compress(p []byte) {
	for len(p) >= BlockSize {
		var w [68]uint32
		var wp [64]uint32

		for i := 0; i < 16; i++ {
			w[i] = binary.BigEndian.Uint32(p[i*4:])
		}
		for j := 16; j < 68; j++ {
			tmp := w[j-16] ^ w[j-9] ^ bits.RotateLeft32(w[j-3], 15)
			p1 := tmp ^ bits.RotateLeft32(tmp, 15) ^ bits.RotateLeft32(tmp, 23)
			w[j] = p1 ^ bits.RotateLeft32(w[j-13], 7) ^ w[j-6]
		}
		for j := 0; j < 64; j++ {
			wp[j] = w[j] ^ w[j+4]
		}

		a, b, c, dv := d.h[0], d.h[1], d.h[2], d.h[3]
		e, f, g, hv := d.h[4], d.h[5], d.h[6], d.h[7]

		for j := 0; j < 16; j++ {
			tj := uint32(0x79cc4519)
			ss1 := bits.RotateLeft32(bits.RotateLeft32(a, 12)+e+bits.RotateLeft32(tj, j), 7)
			ss2 := ss1 ^ bits.RotateLeft32(a, 12)
			tt1 := (a ^ b ^ c) + dv + ss2 + wp[j]
			tt2 := (e ^ f ^ g) + hv + ss1 + w[j]
			dv = c
			c = bits.RotateLeft32(b, 9)
			b = a
			a = tt1
			hv = g
			g = bits.RotateLeft32(f, 19)
			f = e
			e = tt2 ^ bits.RotateLeft32(tt2, 9) ^ bits.RotateLeft32(tt2, 17)
		}

		for j := 16; j < 64; j++ {
			tj := uint32(0x7a879d8a)
			ss1 := bits.RotateLeft32(bits.RotateLeft32(a, 12)+e+bits.RotateLeft32(tj, j%32), 7)
			ss2 := ss1 ^ bits.RotateLeft32(a, 12)
			tt1 := ((a & b) | (a & c) | (b & c)) + dv + ss2 + wp[j]
			tt2 := ((e & f) | (^e & g)) + hv + ss1 + w[j]
			dv = c
			c = bits.RotateLeft32(b, 9)
			b = a
			a = tt1
			hv = g
			g = bits.RotateLeft32(f, 19)
			f = e
			e = tt2 ^ bits.RotateLeft32(tt2, 9) ^ bits.RotateLeft32(tt2, 17)
		}

		d.h[0] ^= a
		d.h[1] ^= b
		d.h[2] ^= c
		d.h[3] ^= dv
		d.h[4] ^= e
		d.h[5] ^= f
		d.h[6] ^= g
		d.h[7] ^= hv

		p = p[BlockSize:]
	}
}

// Sum returns the SM3 checksum of data.
func Sum(data []byte) [Size]byte {
	var d digest
	d.Reset()
	d.Write(data)
	return d.checkSum()
}
