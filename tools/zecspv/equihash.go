package zecspv

import (
	"encoding/binary"
	"errors"
)

// Equihash solution verification (§7.7.1, §5.4.1.11). It follows zcashd's reference validator, now the
// librustzcash equihash crate's is_valid_solution (components/equihash/src/verify.rs), check for check and in
// the same order, so that the crate's invalid vectors fail here for the same reason.

var (
	// ErrEquihashParams is an unsupported (n, k) or a solution of the wrong length.
	ErrEquihashParams = errors.New("zecspv: equihash: invalid parameters or solution length")
	// ErrEquihashCollision is two sibling subtrees whose hashes do not collide on the next ℓ bits.
	ErrEquihashCollision = errors.New("zecspv: equihash: invalid collision length between subtrees")
	// ErrEquihashOrder is a right subtree whose first index is below its left sibling's.
	ErrEquihashOrder = errors.New("zecspv: equihash: index tree incorrectly ordered")
	// ErrEquihashDuplicate is an index used twice.
	ErrEquihashDuplicate = errors.New("zecspv: equihash: duplicate indices")
	// ErrEquihashNonZero is a tree whose XOR of all 2^k hashes is not zero.
	ErrEquihashNonZero = errors.New("zecspv: equihash: root hash of tree is non-zero")
)

type equihashParams struct {
	n, k           int
	collisionBits  int // ℓ = n / (k + 1)
	indicesPerHash int // floor(512 / n): how many n-bit strings one BLAKE2b output holds
	hashOutput     int // bytes of BLAKE2b output: indicesPerHash · n / 8
	solutionLen    int // 2^k indices of ℓ + 1 bits
}

func newEquihashParams(n, k uint32) (equihashParams, error) {
	// librustzcash's Params::new: n a multiple of 8 and of k + 1, 3 ≤ k < n. This implementation also needs one
	// hash output to hold at least one string (n ≤ 512) and an index to fit 32 bits (ℓ + 1 ≤ 32), and caps k so
	// that 2^k stays a sane allocation.
	if n == 0 || n%8 != 0 || k < 3 || k >= n || n%(k+1) != 0 || n > 512 || n/(k+1)+1 > 32 || k > 20 {
		return equihashParams{}, ErrEquihashParams
	}
	p := equihashParams{n: int(n), k: int(k), collisionBits: int(n / (k + 1)), indicesPerHash: int(512 / n)}
	p.hashOutput = p.indicesPerHash * p.n / 8
	p.solutionLen = (1 << k) * (p.collisionBits + 1) / 8
	return p, nil
}

// equihashPersonal is "ZcashPoW" ‖ LE32(n) ‖ LE32(k) (§5.4.1.11, powtag).
func equihashPersonal(n, k uint32) []byte {
	p := make([]byte, 16)
	copy(p, "ZcashPoW")
	binary.LittleEndian.PutUint32(p[8:], n)
	binary.LittleEndian.PutUint32(p[12:], k)
	return p
}

// indicesFromMinimal decodes the solution: 2^k indices, each I2BEBSP_{ℓ+1}, concatenated (§7.7.1).
func indicesFromMinimal(p equihashParams, soln []byte) ([]uint32, error) {
	if len(soln) != p.solutionLen {
		return nil, ErrEquihashParams
	}
	return decodeBits(soln, p.collisionBits+1), nil
}

// decodeBits reads b as consecutive big-endian width-bit integers (width ≤ 32); trailing bits that do not fill a
// whole integer are dropped.
func decodeBits(b []byte, width int) []uint32 {
	out := make([]uint32, 0, len(b)*8/width)
	var acc uint64
	accBits := 0
	for _, x := range b {
		acc = acc<<8 | uint64(x)
		accBits += 8
		if accBits >= width {
			accBits -= width
			out = append(out, uint32(acc>>accBits))
			acc &= 1<<accBits - 1
		}
	}
	return out
}

// minimalFromIndices is the inverse of indicesFromMinimal, for building vectors from index lists.
func minimalFromIndices(p equihashParams, indices []uint32) []byte {
	width := p.collisionBits + 1
	out := make([]byte, 0, len(indices)*width/8)
	var acc uint64
	accBits := 0
	for _, idx := range indices {
		acc = acc<<width | uint64(idx)&(1<<width-1)
		accBits += width
		for accBits >= 8 {
			accBits -= 8
			out = append(out, byte(acc>>accBits))
		}
		acc &= 1<<accBits - 1
	}
	return out
}

// VerifyEquihash reports whether soln is a valid Equihash (n, k) solution for the input ‖ nonce, with nil for a
// valid one. For a block header the input is its first 108 bytes and the nonce the next 32 (§7.7.1 powheader).
func VerifyEquihash(n, k uint32, input, nonce, soln []byte) error {
	p, err := newEquihashParams(n, k)
	if err != nil {
		return err
	}
	indices, err := indicesFromMinimal(p, soln)
	if err != nil {
		return err
	}
	base := newBlake2b(p.hashOutput, equihashPersonal(n, k))
	base.write(input)
	base.write(nonce)
	return verifyIndices(p, &base, indices)
}

func verifyIndices(p equihashParams, base *blake2b, indices []uint32) error {
	// Each leaf's n-bit string is split into k + 1 chunks of ℓ bits, most significant first. A subtree of 2^r
	// leaves is kept as the XOR of its leaves' chunks, in the row of its first leaf.
	rowLen := p.k + 1
	chunks := make([]uint32, len(indices)*rowLen)
	var le [4]byte
	out := make([]byte, 0, 64)
	strLen := p.n / 8
	mask := uint64(1)<<p.collisionBits - 1
	for i, idx := range indices {
		// EquihashGen: BLAKE2b(powtag, I ‖ nonce ‖ LE32(idx / indicesPerHash)), bytes (idx mod indicesPerHash)·n/8
		// onward (§5.4.1.11, with zero-based indices)
		binary.LittleEndian.PutUint32(le[:], idx/uint32(p.indicesPerHash))
		d := *base
		d.write(le[:])
		out = d.sum(out[:0])
		off := int(idx%uint32(p.indicesPerHash)) * strLen
		str := out[off : off+strLen]
		row := chunks[i*rowLen : (i+1)*rowLen]
		var acc uint64
		accBits, c := 0, 0
		for _, b := range str {
			acc = acc<<8 | uint64(b)
			accBits += 8
			if accBits >= p.collisionBits {
				accBits -= p.collisionBits
				row[c] = uint32(acc >> accBits & mask)
				c++
				acc &= 1<<accBits - 1
			}
		}
	}
	v := equihashTree{p: p, indices: indices, chunks: chunks, sorted: append([]uint32(nil), indices...),
		scratch: make([]uint32, len(indices))}
	if err := v.validate(0, len(indices), p.k); err != nil {
		return err
	}
	// the root: the last chunk of the XOR of all 2^k strings must be zero too, so the whole XOR is zero
	if chunks[p.k] != 0 {
		return ErrEquihashNonZero
	}
	return nil
}

type equihashTree struct {
	p       equihashParams
	indices []uint32 // in solution order
	chunks  []uint32 // len(indices) rows of k+1 chunks
	sorted  []uint32 // after validate(lo, hi, ·): sorted[lo:hi] holds that subtree's indices in ascending order
	scratch []uint32
}

// validate checks the subtree of leaves [lo, hi), 2^r of them. It leaves the XOR of their strings in row lo, with
// chunks 0..r-1 zero, and their indices sorted in sorted[lo:hi].
func (t *equihashTree) validate(lo, hi, r int) error {
	if r == 0 {
		return nil
	}
	mid := lo + (hi-lo)/2
	if err := t.validate(lo, mid, r-1); err != nil {
		return err
	}
	if err := t.validate(mid, hi, r-1); err != nil {
		return err
	}
	rowLen := t.p.k + 1
	a := t.chunks[lo*rowLen : (lo+1)*rowLen]
	b := t.chunks[mid*rowLen : (mid+1)*rowLen]
	// the binding conditions, in the reference's order: the two halves collide on chunk r-1 (their chunks
	// below it are already zero), the left half's first index is not above the right's, and no index repeats
	if a[r-1] != b[r-1] {
		return ErrEquihashCollision
	}
	if t.indices[mid] < t.indices[lo] {
		return ErrEquihashOrder
	}
	// merge the two sorted halves; an equal pair is a duplicate
	i, j, o := lo, mid, 0
	for i < mid && j < hi {
		switch x, y := t.sorted[i], t.sorted[j]; {
		case x == y:
			return ErrEquihashDuplicate
		case x < y:
			t.scratch[o] = x
			i++
		default:
			t.scratch[o] = y
			j++
		}
		o++
	}
	o += copy(t.scratch[o:], t.sorted[i:mid])
	copy(t.scratch[o:], t.sorted[j:hi])
	copy(t.sorted[lo:hi], t.scratch[:hi-lo])
	for c := range a {
		a[c] ^= b[c]
	}
	return nil
}
