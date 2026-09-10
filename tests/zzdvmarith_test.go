package tests

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/deroproject/derohe/rpc"
	ref "github.com/deroproject/derohe/veil/reference"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// tests/uint128_test.go checks reference/uint128.go (Go) against math/big. Nothing checks the DVM-BASIC that
// actually runs on chain: contracts/pool.bas's mulhi / nlz / divlu / muldiv / muldivup / bps / bpsup are only
// ever reached through an entrypoint, and the pool and launch fuzzes drive amounts of a shape that never
// exercises divlu's two correction loops. A transcription slip inside one of those branches would pay a wrong
// swap and every existing test would still be green.
//
// This installs the arithmetic block of contracts/pool.bas verbatim, behind entrypoints that only store their
// result, and drives it with the same edge and random triples math/big is checked on.

// arithSource takes the helper block of pool.bas as written (from "Function bps" to the end of the file) and
// adds entrypoints. Copying the text guarantees the probe is the shipped arithmetic, not a retyping of it.
func arithSource(t *testing.T) string {
	t.Helper()
	src := readContract(t, "pool.bas")
	i := strings.Index(src, "Function bps(")
	if i < 0 {
		t.Fatal("pool.bas: the arithmetic block moved")
	}
	return `Function InitializePrivate() Uint64
10 STORE("ok", 0)
20 RETURN 0
End Function
Function Mulhi(a Uint64, b Uint64) Uint64
10 STORE("r", mulhi(a, b))
20 RETURN 0
End Function
Function Muldiv(a Uint64, b Uint64, c Uint64) Uint64
10 STORE("r", muldiv(a, b, c))
20 RETURN 0
End Function
Function Muldivup(a Uint64, b Uint64, c Uint64) Uint64
10 STORE("r", muldivup(a, b, c))
20 RETURN 0
End Function
Function Bps(x Uint64, f Uint64) Uint64
10 STORE("r", bps(x, f))
20 RETURN 0
End Function
Function Bpsup(x Uint64, f Uint64) Uint64
10 STORE("r", bpsup(x, f))
20 RETURN 0
End Function
Function Nlz(x Uint64) Uint64
10 STORE("r", nlz(x))
20 RETURN 0
End Function
` + src[i:]
}

func TestDVMArithmeticMatchesMathBig(t *testing.T) {
	c := harness.New()
	who := c.NewAccount()
	scid, res := c.Install(arithSource(t), nil, nil, who, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("arith probe install: %v", res.Err)
	}
	t.Logf("arithmetic probe installed: storage gas %d, compute %d", res.GasStorage, res.GasCompute)

	call3 := func(entry string, a, b, cc uint64) (uint64, bool) {
		r := c.Call(scid, entry, rpc.Arguments{harness.U("a", a), harness.U("b", b), harness.U("c", cc)}, nil, who)
		if !r.OK() {
			return 0, false
		}
		return c.U(scid, "r"), true
	}
	call2 := func(entry, n1, n2 string, x, f uint64) (uint64, bool) {
		r := c.Call(scid, entry, rpc.Arguments{harness.U(n1, x), harness.U(n2, f)}, nil, who)
		if !r.OK() {
			return 0, false
		}
		return c.U(scid, "r"), true
	}

	bad := 0
	check := func(a, b, cc uint64) {
		if bad > 12 {
			return
		}
		if got, ok := call2("Mulhi", "a", "b", a, b); !ok || got != ref.MulhiBig(a, b) {
			bad++
			t.Errorf("DVM mulhi(%d,%d) = %d ok=%v; math/big says %d", a, b, got, ok, ref.MulhiBig(a, b))
		}
		wantQ, wantOK := ref.MulDivBig(a, b, cc)
		gotQ, gotOK := call3("Muldiv", a, b, cc)
		if gotOK != wantOK || (wantOK && gotQ != wantQ) {
			bad++
			t.Errorf("DVM muldiv(%d,%d,%d) = (%d,%v); math/big says (%d,%v)", a, b, cc, gotQ, gotOK, wantQ, wantOK)
		}
		wantU, wantUOK := ref.MulDivUpBig(a, b, cc)
		gotU, gotUOK := call3("Muldivup", a, b, cc)
		if gotUOK != wantUOK || (wantUOK && gotU != wantU) {
			bad++
			t.Errorf("DVM muldivup(%d,%d,%d) = (%d,%v); math/big says (%d,%v)", a, b, cc, gotU, gotUOK, wantU, wantUOK)
		}
		f := cc % 10001
		if got, ok := call2("Bps", "x", "f", a, f); !ok || got != ref.BpsBig(a, f) {
			bad++
			t.Errorf("DVM bps(%d,%d) = %d ok=%v; math/big says %d", a, f, got, ok, ref.BpsBig(a, f))
		}
		if got, ok := call2("Bpsup", "x", "f", a, f); !ok || got != ref.BpsUpBig(a, f) {
			bad++
			t.Errorf("DVM bpsup(%d,%d) = %d ok=%v; math/big says %d", a, f, got, ok, ref.BpsUpBig(a, f))
		}
	}

	// nlz over every power of two and every mask
	for i := uint(0); i < 64; i++ {
		x := uint64(1) << i
		for _, v := range []uint64{x, x | (x - 1)} {
			r := c.Call(scid, "Nlz", rpc.Arguments{harness.U("x", v)}, nil, who)
			if !r.OK() || c.U(scid, "r") != 63-uint64(i) {
				t.Fatalf("DVM nlz(%d) = %d ok=%v want %d", v, c.U(scid, "r"), r.OK(), 63-uint64(i))
			}
		}
	}

	// edge triples: the same table tests/uint128_test.go uses against math/big
	n := 0
	for _, a := range edges {
		for _, b := range edges {
			for _, cc := range edges {
				check(a, b, cc)
				n++
			}
		}
	}
	// the regime that reaches divlu at all (a*b >= 2^64 and the quotient still fits), plus plain random triples
	r := rand.New(rand.NewSource(20260910))
	for i := 0; i < 4000; i++ {
		a, b := rnd64(r), rnd64(r)
		hi := ref.MulhiBig(a, b)
		cc := hi + 1 + rnd64(r)
		if cc <= hi {
			continue
		}
		check(a, b, cc)
		n++
	}
	for i := 0; i < 4000; i++ {
		check(rnd64(r), rnd64(r), rnd64(r))
		n++
	}
	t.Logf("checked %d triples against math/big through the real DVM (%d disagreements)", n, bad)
}
