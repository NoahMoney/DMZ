package zecspv

import (
	"errors"
	"fmt"
	"math/big"
)

var (
	// ErrBits is nBits that is negative, zero, overflowing, or easier than the network's PoWLimit.
	ErrBits = errors.New("zecspv: nBits out of range")
	// ErrAboveTarget is a header whose hash is above the target its own nBits names (§7.7.2).
	ErrAboveTarget = errors.New("zecspv: header hash above its target")
	// ErrBelowFloor is a header easier than the floor the caller set: its target is above Options.MaxTarget.
	ErrBelowFloor = errors.New("zecspv: header easier than the floor")
)

// CompactToTarget expands a compact nBits value as zcashd's arith_uint256::SetCompact does, reporting the sign
// bit and overflow as it does. ToTarget in §7.7.4 is the same map on the values consensus accepts.
func CompactToTarget(bits uint32) (target *big.Int, negative, overflow bool) {
	size := bits >> 24
	word := bits & 0x007fffff
	target = new(big.Int)
	if size <= 3 {
		word >>= 8 * (3 - size)
		target.SetUint64(uint64(word))
	} else {
		target.SetUint64(uint64(word))
		target.Lsh(target, uint(8*(size-3)))
	}
	negative = word != 0 && bits&0x00800000 != 0
	overflow = word != 0 && (size > 34 || (word > 0xff && size > 33) || (word > 0xffff && size > 32))
	return target, negative, overflow
}

// TargetToCompact is ToCompact (§7.7.4), as zcashd's arith_uint256::GetCompact computes it for a non-negative
// value: the mantissa never has its sign bit set.
func TargetToCompact(target *big.Int) uint32 {
	if target.Sign() < 0 {
		panic("zecspv: negative target")
	}
	size := (target.BitLen() + 7) / 8
	var compact uint32
	if size <= 3 {
		compact = uint32(target.Uint64() << (8 * (3 - size)))
	} else {
		compact = uint32(new(big.Int).Rsh(target, uint(8*(size-3))).Uint64())
	}
	if compact&0x00800000 != 0 {
		compact >>= 8
		size++
	}
	return compact | uint32(size)<<24
}

// Target is the target nBits names, refused as zcashd's CheckProofOfWork refuses it: negative, zero, overflowing,
// or above PoWLimit.
func (p *Params) Target(bits uint32) (*big.Int, error) {
	t, negative, overflow := CompactToTarget(bits)
	if negative || overflow || t.Sign() == 0 || t.Cmp(p.PowLimit) > 0 {
		return nil, fmt.Errorf("%w: %08x", ErrBits, bits)
	}
	return t, nil
}

var two256 = new(big.Int).Lsh(big.NewInt(1), 256)

// Work is the work of a block with this target: floor(2^256 / (target + 1)) (§7.7.5).
func Work(target *big.Int) *big.Int {
	return new(big.Int).Quo(two256, new(big.Int).Add(target, big.NewInt(1)))
}

// FloorFromBits returns the easiest target to allow when the network's difficulty is known to have been nBits
// and may since have fallen by at most factor: the target of nBits times factor. It is a policy input to
// Options.MaxTarget; docs/ZCASH_HEADER_VERIFICATION.md §4 explains how the companion picks both numbers.
func (p *Params) FloorFromBits(bits uint32, factor int64) (*big.Int, error) {
	if factor < 1 {
		return nil, errors.New("zecspv: the floor's factor must be at least 1")
	}
	t, err := p.Target(bits)
	if err != nil {
		return nil, err
	}
	t.Mul(t, big.NewInt(factor))
	if t.Cmp(p.PowLimit) > 0 {
		t.Set(p.PowLimit)
	}
	return t, nil
}
