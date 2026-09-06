// Package reference is the executable specification of every formula in the Veil contracts.
// The limb functions mirror contracts/pool.bas line for line; the *Big functions are the math/big truth
// they are checked against in tests/.
package reference

import "math/big"

const m32 = 1<<32 - 1

// Mulhi returns the high 64 bits of a*b. Mirrors pool.bas mulhi.
func Mulhi(a, b uint64) uint64 {
	a0, a1 := a&m32, a>>32
	b0, b1 := b&m32, b>>32
	p00 := a0 * b0
	p01 := a0 * b1
	p10 := a1 * b0
	mid := (p00 >> 32) + (p01 & m32) + (p10 & m32)
	return a1*b1 + (p01 >> 32) + (p10 >> 32) + (mid >> 32)
}

// Nlz returns the number of leading zero bits of x (x != 0). Mirrors pool.bas nlz.
func Nlz(x uint64) uint64 {
	n := uint64(0)
	if x < 1<<32 {
		n += 32
		x <<= 32
	}
	if x < 1<<48 {
		n += 16
		x <<= 16
	}
	if x < 1<<56 {
		n += 8
		x <<= 8
	}
	if x < 1<<60 {
		n += 4
		x <<= 4
	}
	if x < 1<<62 {
		n += 2
		x <<= 2
	}
	if x < 1<<63 {
		n++
	}
	return n
}

// Divlu returns (u1<<64 | u0) / v. Requires u1 < v. Mirrors pool.bas divlu (Hacker's Delight divlu2, base 2^32).
func Divlu(u1, u0, v uint64) uint64 {
	s := Nlz(v)
	v <<= s
	vn1 := v >> 32
	vn0 := v & m32
	var un32 uint64
	if s == 0 {
		un32 = u1
	} else {
		un32 = (u1 << s) | (u0 >> (64 - s))
	}
	un10 := u0 << s
	un1 := un10 >> 32
	un0 := un10 & m32

	q1 := un32 / vn1
	rhat := un32 - q1*vn1
	for !(q1 < 1<<32 && q1*vn0 <= (rhat<<32)+un1) {
		q1--
		rhat += vn1
		if rhat >= 1<<32 {
			break
		}
	}
	un21 := (un32 << 32) + un1 - q1*v
	q0 := un21 / vn1
	rhat = un21 - q0*vn1
	for !(q0 < 1<<32 && q0*vn0 <= (rhat<<32)+un0) {
		q0--
		rhat += vn1
		if rhat >= 1<<32 {
			break
		}
	}
	return (q1 << 32) + q0
}

// MulDiv returns floor(a*b/c) and ok=false when c == 0 or the result does not fit in 64 bits
// (the contract panics, i.e. reverts, in that case). Mirrors pool.bas muldiv.
func MulDiv(a, b, c uint64) (uint64, bool) {
	hi := Mulhi(a, b)
	lo := a * b
	if hi >= c { // covers c == 0
		return 0, false
	}
	if hi == 0 {
		return lo / c, true
	}
	return Divlu(hi, lo, c), true
}

// MulDivUp returns ceil(a*b/c). Mirrors pool.bas muldivup.
func MulDivUp(a, b, c uint64) (uint64, bool) {
	q, ok := MulDiv(a, b, c)
	if !ok {
		return 0, false
	}
	if a*b-q*c == 0 {
		return q, true
	}
	if q == ^uint64(0) { // ceiling would be 2^64: the contract panics
		return 0, false
	}
	return q + 1, true
}

// Bps returns floor(x*f/10000) without overflow for f <= 10000. Mirrors pool.bas bps.
func Bps(x, f uint64) uint64 {
	return (x/10000)*f + ((x%10000)*f)/10000
}

// BpsUp returns ceil(x*f/10000) for f <= 10000. Mirrors pool.bas bpsup.
func BpsUp(x, f uint64) uint64 {
	r := Bps(x, f)
	if ((x%10000)*f)%10000 == 0 {
		return r
	}
	return r + 1
}

// ---- math/big truth ------------------------------------------------------------------------------

var maxU64 = new(big.Int).SetUint64(^uint64(0))

func bigU(x uint64) *big.Int { return new(big.Int).SetUint64(x) }

// MulhiBig is the high word of a*b computed with math/big.
func MulhiBig(a, b uint64) uint64 {
	p := new(big.Int).Mul(bigU(a), bigU(b))
	return new(big.Int).Rsh(p, 64).Uint64()
}

// MulDivBig is floor(a*b/c) with math/big; ok=false when c == 0 or the quotient exceeds 2^64-1.
func MulDivBig(a, b, c uint64) (uint64, bool) {
	if c == 0 {
		return 0, false
	}
	p := new(big.Int).Mul(bigU(a), bigU(b))
	q := new(big.Int).Quo(p, bigU(c))
	if q.Cmp(maxU64) > 0 {
		return 0, false
	}
	return q.Uint64(), true
}

// MulDivUpBig is ceil(a*b/c) with math/big.
func MulDivUpBig(a, b, c uint64) (uint64, bool) {
	if c == 0 {
		return 0, false
	}
	p := new(big.Int).Mul(bigU(a), bigU(b))
	q, r := new(big.Int).QuoRem(p, bigU(c), new(big.Int))
	if r.Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	if q.Cmp(maxU64) > 0 {
		return 0, false
	}
	return q.Uint64(), true
}

// BpsBig is floor(x*f/10000) with math/big.
func BpsBig(x, f uint64) uint64 {
	p := new(big.Int).Mul(bigU(x), bigU(f))
	return p.Quo(p, big.NewInt(10000)).Uint64()
}

// BpsUpBig is ceil(x*f/10000) with math/big.
func BpsUpBig(x, f uint64) uint64 {
	p := new(big.Int).Mul(bigU(x), bigU(f))
	q, r := new(big.Int).QuoRem(p, big.NewInt(10000), new(big.Int))
	if r.Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	return q.Uint64()
}
