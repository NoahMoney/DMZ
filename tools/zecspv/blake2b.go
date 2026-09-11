package zecspv

import (
	"encoding/binary"
	"math/bits"
)

// BLAKE2b (RFC 7693) with a personalisation string, the parameter block's bytes 48–63 (RFC 7693 §2.8), which
// Equihash needs ("ZcashPoW" ‖ LE32(n) ‖ LE32(k)) and golang.org/x/crypto/blake2b does not expose. It follows
// tools/zecswap/blake2.go, made incremental so that Equihash can absorb the header once and then finish one hash
// per index from a copy of the state. A hash has no secret and no timing surface. TestBlake2b pins it to the RFC's
// vector; the Equihash vectors pin the personalised form.

var blake2bIV = [8]uint64{
	0x6a09e667f3bcc908, 0xbb67ae8584caa73b, 0x3c6ef372fe94f82b, 0xa54ff53a5f1d36f1,
	0x510e527fade682d1, 0x9b05688c2b3e6c1f, 0x1f83d9abfb41bd6b, 0x5be0cd19137e2179,
}

var blake2bSigma = [12][16]byte{
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	{14, 10, 4, 8, 9, 15, 13, 6, 1, 12, 0, 2, 11, 7, 5, 3},
	{11, 8, 12, 0, 5, 2, 15, 13, 10, 14, 3, 6, 7, 1, 9, 4},
	{7, 9, 3, 1, 13, 12, 11, 14, 2, 6, 5, 10, 4, 0, 15, 8},
	{9, 0, 5, 7, 2, 4, 10, 15, 14, 1, 11, 12, 6, 8, 3, 13},
	{2, 12, 6, 10, 0, 11, 8, 3, 4, 13, 7, 5, 15, 14, 1, 9},
	{12, 5, 1, 15, 14, 13, 4, 10, 0, 7, 6, 3, 9, 2, 8, 11},
	{13, 11, 7, 14, 12, 1, 3, 9, 5, 0, 15, 4, 8, 6, 2, 10},
	{6, 15, 14, 9, 11, 3, 0, 8, 12, 2, 13, 7, 1, 4, 10, 5},
	{10, 2, 8, 4, 7, 6, 1, 5, 15, 11, 9, 14, 3, 12, 13, 0},
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	{14, 10, 4, 8, 9, 15, 13, 6, 1, 12, 0, 2, 11, 7, 5, 3},
}

// blake2b is an unkeyed BLAKE2b state. The zero value is not usable; start from newBlake2b.
type blake2b struct {
	h    [8]uint64
	t    uint64 // bytes compressed so far (the counter's high word stays zero for any input here)
	buf  [128]byte
	n    int // bytes waiting in buf
	size int // digest length in bytes
}

// newBlake2b starts a hash of size bytes (1–64) with an optional 16-byte personalisation.
func newBlake2b(size int, person []byte) blake2b {
	if size < 1 || size > 64 || (len(person) != 0 && len(person) != 16) {
		panic("zecspv: blake2b parameters")
	}
	// the parameter block: digest length, key length 0, fanout 1, depth 1, then salt (zero) and personalisation
	var p [64]byte
	p[0] = byte(size)
	p[2] = 1
	p[3] = 1
	copy(p[48:64], person)
	d := blake2b{size: size}
	for i := range d.h {
		d.h[i] = blake2bIV[i] ^ binary.LittleEndian.Uint64(p[8*i:])
	}
	return d
}

// write absorbs data. A full block is compressed only once more data follows it, because the last block must be
// compressed with the final flag.
func (d *blake2b) write(data []byte) {
	for len(data) > 0 {
		if d.n == len(d.buf) {
			d.t += uint64(len(d.buf))
			blake2bCompress(&d.h, &d.buf, d.t, false)
			d.n = 0
		}
		c := copy(d.buf[d.n:], data)
		d.n += c
		data = data[c:]
	}
}

// sum appends the digest to out. The receiver is a copy, so the state can go on absorbing or be summed again.
func (d blake2b) sum(out []byte) []byte {
	clear(d.buf[d.n:])
	d.t += uint64(d.n)
	blake2bCompress(&d.h, &d.buf, d.t, true)
	var full [64]byte
	for i := range d.h {
		binary.LittleEndian.PutUint64(full[8*i:], d.h[i])
	}
	return append(out, full[:d.size]...)
}

// blake2bSum hashes data to size bytes with an optional 16-byte personalisation.
func blake2bSum(size int, person, data []byte) []byte {
	d := newBlake2b(size, person)
	d.write(data)
	return d.sum(nil)
}

func blake2bG(v *[16]uint64, a, b, c, d int, x, y uint64) {
	v[a] += v[b] + x
	v[d] = bits.RotateLeft64(v[d]^v[a], -32)
	v[c] += v[d]
	v[b] = bits.RotateLeft64(v[b]^v[c], -24)
	v[a] += v[b] + y
	v[d] = bits.RotateLeft64(v[d]^v[a], -16)
	v[c] += v[d]
	v[b] = bits.RotateLeft64(v[b]^v[c], -63)
}

func blake2bCompress(h *[8]uint64, block *[128]byte, t uint64, last bool) {
	var m [16]uint64
	for i := range m {
		m[i] = binary.LittleEndian.Uint64(block[8*i:])
	}
	var v [16]uint64
	copy(v[:8], h[:])
	copy(v[8:], blake2bIV[:])
	v[12] ^= t
	if last {
		v[14] = ^v[14]
	}
	for r := range blake2bSigma {
		s := &blake2bSigma[r]
		blake2bG(&v, 0, 4, 8, 12, m[s[0]], m[s[1]])
		blake2bG(&v, 1, 5, 9, 13, m[s[2]], m[s[3]])
		blake2bG(&v, 2, 6, 10, 14, m[s[4]], m[s[5]])
		blake2bG(&v, 3, 7, 11, 15, m[s[6]], m[s[7]])
		blake2bG(&v, 0, 5, 10, 15, m[s[8]], m[s[9]])
		blake2bG(&v, 1, 6, 11, 12, m[s[10]], m[s[11]])
		blake2bG(&v, 2, 7, 8, 13, m[s[12]], m[s[13]])
		blake2bG(&v, 3, 4, 9, 14, m[s[14]], m[s[15]])
	}
	for i := range h {
		h[i] ^= v[i] ^ v[i+8]
	}
}
