package tests

import (
	"math/rand"
	"testing"

	ref "github.com/deroproject/derohe/veil/reference"
)

// random uint64 with a random bit length so small, medium and huge operands all occur
func rnd64(r *rand.Rand) uint64 {
	bits := r.Intn(65)
	if bits == 0 {
		return 0
	}
	v := r.Uint64()
	if bits < 64 {
		v &= (1 << uint(bits)) - 1
	}
	return v
}

var edges = []uint64{0, 1, 2, 3, 999, 1000, 9999, 10000, 10001, 1<<32 - 1, 1 << 32, 1<<32 + 1, 1<<48 - 1, 1 << 48,
	1<<63 - 1, 1 << 63, 1<<63 + 1, 1<<64 - 2, 1<<64 - 1, 100000, 1e12, 1e15, 1e18, 18446744073709551557}

func checkTriple(t *testing.T, a, b, c uint64) {
	t.Helper()
	if hi := ref.Mulhi(a, b); hi != ref.MulhiBig(a, b) {
		t.Fatalf("Mulhi(%d,%d)=%d want %d", a, b, hi, ref.MulhiBig(a, b))
	}
	q, ok := ref.MulDiv(a, b, c)
	qb, okb := ref.MulDivBig(a, b, c)
	if ok != okb || (ok && q != qb) {
		t.Fatalf("MulDiv(%d,%d,%d)=(%d,%v) want (%d,%v)", a, b, c, q, ok, qb, okb)
	}
	u, ok := ref.MulDivUp(a, b, c)
	ub, okb := ref.MulDivUpBig(a, b, c)
	if ok != okb || (ok && u != ub) {
		t.Fatalf("MulDivUp(%d,%d,%d)=(%d,%v) want (%d,%v)", a, b, c, u, ok, ub, okb)
	}
	f := c % 10001
	if x := ref.Bps(a, f); x != ref.BpsBig(a, f) {
		t.Fatalf("Bps(%d,%d)=%d want %d", a, f, x, ref.BpsBig(a, f))
	}
	if x := ref.BpsUp(a, f); x != ref.BpsUpBig(a, f) {
		t.Fatalf("BpsUp(%d,%d)=%d want %d", a, f, x, ref.BpsUpBig(a, f))
	}
}

func TestUint128Edges(t *testing.T) {
	for _, a := range edges {
		for _, b := range edges {
			for _, c := range edges {
				checkTriple(t, a, b, c)
			}
		}
	}
}

func TestUint128Random(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 300000; i++ {
		checkTriple(t, rnd64(r), rnd64(r), rnd64(r))
	}
	// quotient-fits regime specifically (c large relative to a*b >> 64)
	for i := 0; i < 300000; i++ {
		a, b := rnd64(r), rnd64(r)
		hi := ref.MulhiBig(a, b)
		c := hi + 1 + rnd64(r)
		if c <= hi { // wrapped
			continue
		}
		checkTriple(t, a, b, c)
	}
}

func TestNlz(t *testing.T) {
	for i := uint(0); i < 64; i++ {
		x := uint64(1) << i
		if n := ref.Nlz(x); n != 63-uint64(i) {
			t.Fatalf("Nlz(1<<%d)=%d", i, n)
		}
		if n := ref.Nlz(x | (x - 1)); n != 63-uint64(i) {
			t.Fatalf("Nlz(mask %d)=%d", i, n)
		}
	}
}
