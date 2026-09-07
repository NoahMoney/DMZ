package tests

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	ref "github.com/deroproject/derohe/veil/reference"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// The quote side of a pool is an SCID fixed at install. The zero hash is DERO, and the DVM makes that exact:
// ASSETVALUE(zero) is DEROVALUE() and SEND_ASSET_TO_ADDRESS(a,n,zero) is SEND_DERO_TO_ADDRESS(a,n), so a
// zero-quoted pool behaves byte for byte as every test here was written against.
var zeroHash crypto.Hash // the zero SCID: in the DVM this quote IS DERO

const assetCode = `Function InitializePrivate() Uint64
10 RETURN 0
End Function
Function Mint(amount Uint64) Uint64
10 SEND_ASSET_TO_ADDRESS(SIGNER(), amount, SCID())
20 RETURN 0
End Function
`

var zero crypto.Hash

func readContract(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../contracts/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type poolEnv struct {
	c                    *harness.Chain
	treasury, alice, bob *rpc.Address
	asset, pool          crypto.Hash
	model                *ref.Pool
	fee, pfee            uint64
	gasMax               map[string]uint64
	storeMax             map[string]uint64
}

func newPoolEnv(t *testing.T, fee, pfee uint64) *poolEnv {
	t.Helper()
	e := &poolEnv{c: harness.New(), fee: fee, pfee: pfee, gasMax: map[string]uint64{}, storeMax: map[string]uint64{}}
	e.treasury = e.c.NewAccount()
	e.alice = e.c.NewAccount()
	e.bob = e.c.NewAccount()
	var res harness.Result
	e.asset, res = e.c.Install(assetCode, nil, nil, e.alice, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("asset install: %v", res.Err)
	}
	for _, a := range []*rpc.Address{e.alice, e.bob} { // give them an entry in the asset tree
		if r := e.c.Call(e.asset, "Mint", rpc.Arguments{harness.U("amount", 1<<62)}, nil, a); !r.OK() {
			t.Fatalf("mint: %v", r.Err)
		}
	}
	code := readContract(t, "pool.bas")
	e.pool, res = e.c.Install(code, nil, rpc.Arguments{
		harness.H("asset", e.asset), harness.H("quote", zeroHash), harness.S("treasury", e.treasury.String()), harness.U("fee", fee), harness.U("pfee", pfee),
	}, e.alice, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("pool install: %v", res.Err)
	}
	e.note("Install", res)
	e.model = ref.NewPool(fee, pfee)
	return e
}

func (e *poolEnv) note(entry string, r harness.Result) {
	if r.GasCompute > e.gasMax[entry] {
		e.gasMax[entry] = r.GasCompute
	}
	if r.GasStorage > e.storeMax[entry] {
		e.storeMax[entry] = r.GasStorage
	}
}

func (e *poolEnv) in(d, a, l uint64) map[crypto.Hash]uint64 {
	m := map[crypto.Hash]uint64{}
	if d > 0 {
		m[zero] = d
	}
	if a > 0 {
		m[e.asset] = a
	}
	if l > 0 {
		m[e.pool] = l
	}
	return m
}

func (e *poolEnv) kindHash(kind string) crypto.Hash {
	switch kind {
	case "dero":
		return zero
	case "asset":
		return e.asset
	case "lp":
		return e.pool
	}
	panic(kind)
}

// checkState compares every model field with the contract and the model sends with the emitted transfers.
func (e *poolEnv) checkState(t *testing.T, tag string, r harness.Result, who *rpc.Address, sends []ref.Send) {
	t.Helper()
	m := e.model
	c := e.c
	got := map[string]uint64{"rd": c.U(e.pool, "rd"), "ra": c.U(e.pool, "ra"), "lp": c.U(e.pool, "lp"), "fd": c.U(e.pool, "fd"),
		"fa": c.U(e.pool, "fa"), "fc": c.U(e.pool, "fc"), "tc": c.U(e.pool, "tc"), "pc": c.U(e.pool, "pc"), "lh": c.U(e.pool, "lh"), "lt": c.U(e.pool, "lt")}
	want := map[string]uint64{"rd": m.RD, "ra": m.RA, "lp": m.LP, "fd": m.FD, "fa": m.FA, "fc": m.FC, "tc": m.TC, "pc": m.PC, "lh": m.LH, "lt": m.LT}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s: %s = %d, model %d (got %v want %v)", tag, k, got[k], v, got, want)
		}
	}
	// the contract must hold exactly reserves + accrued protocol fees
	if b := c.SCBalance(e.pool, zero); b != m.RD+m.FD {
		t.Fatalf("%s: pool DERO balance %d != rd+fd %d", tag, b, m.RD+m.FD)
	}
	if b := c.SCBalance(e.pool, e.asset); b != m.RA+m.FA {
		t.Fatalf("%s: pool asset balance %d != ra+fa %d", tag, b, m.RA+m.FA)
	}
	if who == nil {
		return
	}
	wantSend := map[string]uint64{}
	for _, s := range sends {
		wantSend[s.Kind] += s.Amount
	}
	for _, kind := range []string{"dero", "asset", "lp"} {
		if g := r.Sent(e.kindHash(kind), who); g != wantSend[kind] {
			t.Fatalf("%s: sent %s %d, model %d", tag, kind, g, wantSend[kind])
		}
	}
	// published trades and price points, latest entries
	if m.TC > 0 {
		tr := m.Trades[m.TC-1]
		want := fmt.Sprintf("%d|%s|%d|%d|%d|%d", tr.Side, tr.Addr, tr.In, tr.Out, tr.H, tr.T)
		if g := c.S(e.pool, fmt.Sprintf("t_%d", m.TC-1)); g != want {
			t.Fatalf("%s: t_%d = %q want %q", tag, m.TC-1, g, want)
		}
	}
	if m.PC > 0 {
		p := m.Points[m.PC-1]
		want := fmt.Sprintf("%d|%d|%d|%d", p.H, p.T, p.RD, p.RA)
		if g := c.S(e.pool, fmt.Sprintf("p_%d", m.PC-1)); g != want {
			t.Fatalf("%s: p_%d = %q want %q", tag, m.PC-1, g, want)
		}
	}
}

func (e *poolEnv) add(t *testing.T, who *rpc.Address, d, a, l, minLP uint64) (harness.Result, bool) {
	t.Helper()
	sends, ok := e.model.AddLiquidity(ref.In{D: d, A: a, L: l}, minLP, e.c.Height, e.c.Timestamp)
	r := e.c.Call(e.pool, "AddLiquidity", rpc.Arguments{harness.U("min_lp", minLP)}, e.in(d, a, l), who)
	e.note("AddLiquidity", r)
	if r.OK() != ok {
		t.Fatalf("AddLiquidity(d=%d a=%d l=%d min=%d): contract ok=%v (%v), model ok=%v", d, a, l, minLP, r.OK(), r.Err, ok)
	}
	e.checkState(t, "AddLiquidity", r, who, sends)
	return r, ok
}

func (e *poolEnv) remove(t *testing.T, who *rpc.Address, d, a, l, minD, minA uint64) (harness.Result, bool) {
	t.Helper()
	sends, ok := e.model.RemoveLiquidity(ref.In{D: d, A: a, L: l}, minD, minA, e.c.Height, e.c.Timestamp)
	r := e.c.Call(e.pool, "RemoveLiquidity", rpc.Arguments{harness.U("min_dero", minD), harness.U("min_asset", minA)}, e.in(d, a, l), who)
	e.note("RemoveLiquidity", r)
	if r.OK() != ok {
		t.Fatalf("RemoveLiquidity(l=%d): contract ok=%v (%v), model ok=%v", l, r.OK(), r.Err, ok)
	}
	e.checkState(t, "RemoveLiquidity", r, who, sends)
	return r, ok
}

func (e *poolEnv) buy(t *testing.T, who *rpc.Address, d, a, l, minOut, publish uint64) (harness.Result, bool) {
	t.Helper()
	sends, ok := e.model.SwapDeroForAsset(ref.In{D: d, A: a, L: l}, minOut, publish, who.String(), e.c.Height, e.c.Timestamp)
	r := e.c.Call(e.pool, "SwapDeroForAsset", rpc.Arguments{harness.U("min_out", minOut), harness.U("publish", publish)}, e.in(d, a, l), who)
	e.note("SwapDeroForAsset", r)
	if r.OK() != ok {
		t.Fatalf("SwapDeroForAsset(d=%d min=%d): contract ok=%v (%v), model ok=%v rd=%d ra=%d", d, minOut, r.OK(), r.Err, ok, e.model.RD, e.model.RA)
	}
	e.checkState(t, "SwapDeroForAsset", r, who, sends)
	return r, ok
}

func (e *poolEnv) sell(t *testing.T, who *rpc.Address, d, a, l, minOut, publish uint64) (harness.Result, bool) {
	t.Helper()
	sends, ok := e.model.SwapAssetForDero(ref.In{D: d, A: a, L: l}, minOut, publish, who.String(), e.c.Height, e.c.Timestamp)
	r := e.c.Call(e.pool, "SwapAssetForDero", rpc.Arguments{harness.U("min_out", minOut), harness.U("publish", publish)}, e.in(d, a, l), who)
	e.note("SwapAssetForDero", r)
	if r.OK() != ok {
		t.Fatalf("SwapAssetForDero(a=%d min=%d): contract ok=%v (%v), model ok=%v rd=%d ra=%d", a, minOut, r.OK(), r.Err, ok, e.model.RD, e.model.RA)
	}
	e.checkState(t, "SwapAssetForDero", r, who, sends)
	return r, ok
}

func (e *poolEnv) report(t *testing.T) {
	keys := []string{"Install", "AddLiquidity", "RemoveLiquidity", "SwapDeroForAsset", "SwapAssetForDero", "ClaimFees"}
	var sb strings.Builder
	sb.WriteString("max gas per entrypoint (compute / storage):")
	for _, k := range keys {
		if g, ok := e.gasMax[k]; ok {
			sb.WriteString(fmt.Sprintf(" %s=%d/%d", k, g, e.storeMax[k]))
		}
	}
	t.Log(sb.String())
}

// ---- scenarios ----------------------------------------------------------------------------------------------------

func TestPoolInstallValidation(t *testing.T) {
	e := newPoolEnv(t, 50, 5000)
	if e.c.S(e.pool, "kind") != "pool" || e.c.U(e.pool, "fee") != 50 || e.c.U(e.pool, "pfee") != 5000 {
		t.Fatal("init values")
	}
	if e.c.S(e.pool, "asset") != string(e.asset[:]) || e.c.S(e.pool, "treasury") != string(e.treasury.Compressed()) {
		t.Fatal("asset/treasury not stored raw")
	}
	code := readContract(t, "pool.bas")
	bad := []rpc.Arguments{
		{harness.H("asset", e.asset), harness.H("quote", zeroHash), harness.S("treasury", e.treasury.String()), harness.U("fee", 101), harness.U("pfee", 5000)},
		{harness.H("asset", e.asset), harness.H("quote", zeroHash), harness.S("treasury", e.treasury.String()), harness.U("fee", 50), harness.U("pfee", 10001)},
		{harness.H("asset", e.asset), harness.H("quote", zeroHash), harness.S("treasury", "dero1notanaddress"), harness.U("fee", 50), harness.U("pfee", 5000)},
		{harness.H("asset", zero), harness.H("quote", zeroHash), harness.S("treasury", e.treasury.String()), harness.U("fee", 50), harness.U("pfee", 5000)},
		{harness.S("asset", "abcd"), harness.S("treasury", e.treasury.String()), harness.U("fee", 50), harness.U("pfee", 5000)},
	}
	for i, args := range bad {
		if _, r := e.c.Install(code, nil, args, e.alice, harness.MaxStorageGas); r.OK() {
			t.Fatalf("bad install %d accepted", i)
		}
	}
	if s := e.storeMax["Install"]; s >= harness.MaxStorageGas {
		t.Fatalf("install storage gas %d exceeds per-TX cap %d", s, harness.MaxStorageGas)
	}
	t.Logf("pool.bas install: %d bytes of code, storage gas %d, compute gas %d", len(code), e.storeMax["Install"], e.gasMax["Install"])
}

func TestPoolSmoke(t *testing.T) {
	e := newPoolEnv(t, 50, 5000)
	// first deposit 100 DERO : 1,000,000 tokens
	e.add(t, e.alice, 100*100000, 1000000, 0, 0)
	if e.model.LP != 100*100000 || e.model.RD != 100*100000 || e.model.RA != 1000000 {
		t.Fatal("first deposit state")
	}
	e.c.NextBlock()
	// buy with 1 DERO, publish
	e.buy(t, e.bob, 100000, 0, 0, 0, 1)
	e.c.NextBlock()
	// sell 5000 tokens, unpublished
	e.sell(t, e.bob, 0, 5000, 0, 0, 0)
	e.c.NextBlock()
	// second LP, unbalanced (excess asset refunded)
	e.add(t, e.bob, 10*100000, 1000000, 0, 0)
	// remove half of alice LP
	e.remove(t, e.alice, 0, 0, 50*100000, 0, 0)
	if e.model.TC != 1 || e.model.PC != 3 {
		t.Fatalf("tc=%d pc=%d", e.model.TC, e.model.PC)
	}
	// ClaimFees is permissionless: anyone may trigger it, and it always pays the treasury recorded at install.
	// A fee path only one key can move stops the moment that key does, and this protocol has no operator.
	fd, fa := e.model.FD, e.model.FA
	r := e.c.Call(e.pool, "ClaimFees", nil, nil, e.bob)
	if !r.OK() || r.Sent(zero, e.treasury) != fd || r.Sent(zero, e.bob) != 0 {
		t.Fatalf("a stranger could not trigger the payout to the treasury: %v (treasury got %d, caller got %d)", r.Err, r.Sent(zero, e.treasury), r.Sent(zero, e.bob))
	}
	e.model.ClaimFees()
	fd, fa = e.model.FD, e.model.FA
	if fd != 0 || fa != 0 {
		t.Fatalf("model not drained: fd %d fa %d", fd, fa)
	}
	// and with nothing to claim the call is refused rather than burning a fee for nothing
	if r := e.c.Call(e.pool, "ClaimFees", nil, nil, e.bob); r.OK() {
		t.Fatal("an empty fee pool was claimed")
	}
	e.buy(t, e.alice, 3*100000, 0, 0, 0, 0)
	e.sell(t, e.alice, 0, 3*100000, 0, 0, 0)
	fd, fa = e.model.FD, e.model.FA
	r = e.c.Call(e.pool, "ClaimFees", nil, nil, e.treasury)
	e.note("ClaimFees", r)
	if !r.OK() {
		t.Fatalf("claim: %v", r.Err)
	}
	e.model.ClaimFees()
	if r.Sent(zero, e.treasury) != fd || r.Sent(e.asset, e.treasury) != fa || fd == 0 || fa == 0 {
		t.Fatalf("claim amounts dero %d/%d asset %d/%d", r.Sent(zero, e.treasury), fd, r.Sent(e.asset, e.treasury), fa)
	}
	e.checkState(t, "ClaimFees", r, nil, nil)
	e.report(t)
}

func TestPoolRejects(t *testing.T) {
	e := newPoolEnv(t, 50, 5000)
	e.add(t, e.alice, 100*100000, 1000000, 0, 0)
	// ringsize > 2: signer unknown → every entrypoint returns 1
	for _, entry := range []string{"AddLiquidity", "RemoveLiquidity", "SwapDeroForAsset", "SwapAssetForDero", "ClaimFees"} {
		args := rpc.Arguments{harness.U("min_lp", 0), harness.U("min_dero", 0), harness.U("min_asset", 0), harness.U("min_out", 0), harness.U("publish", 0)}
		if r := e.c.Call(e.pool, entry, args, e.in(100000, 1000, 0), nil); r.OK() {
			t.Fatalf("%s accepted an anonymous caller", entry)
		}
	}
	e.checkState(t, "anon", harness.Result{}, nil, nil)
	// slippage: exact quote passes, quote+1 fails
	out, _ := e.model.Quote(100000)
	e.buy(t, e.bob, 100000, 0, 0, out+1, 0)
	e.buy(t, e.bob, 100000, 0, 0, out, 0)
	// stray assets
	e.buy(t, e.bob, 100000, 5, 0, 0, 0)               // asset along with a DERO swap
	e.sell(t, e.bob, 5, 1000, 0, 0, 0)                // DERO along with an asset swap
	e.remove(t, e.alice, 1, 0, 1000, 0, 0)            // DERO with LP burn
	e.add(t, e.bob, 100000, 1000, 7, 0)               // LP with a deposit
	e.remove(t, e.alice, 0, 0, e.model.LP, 0, 0)      // cannot remove MIN_LIQ
	e.remove(t, e.alice, 0, 0, e.model.LP-1000, 0, 0) // can remove everything else
	e.report(t)
}

// ---- fuzz --------------------------------------------------------------------------------------------------------

func amount(r *rand.Rand, cap uint64) uint64 {
	switch r.Intn(10) {
	case 0:
		return uint64(r.Intn(3)) + 1
	case 1, 2, 3:
		return uint64(r.Int63n(1000000)) + 1
	case 4, 5, 6:
		return uint64(r.Int63n(1000000000)) + 1
	case 7, 8:
		return uint64(r.Int63n(1000000000000)) + 1
	default:
		v := r.Uint64()
		if cap != 0 {
			v %= cap
		}
		return v + 1
	}
}

func TestPoolFuzz(t *testing.T) {
	n := 10000
	if s := os.Getenv("VEIL_FUZZ_N"); s != "" {
		n, _ = strconv.Atoi(s)
	}
	seed := int64(42)
	if s := os.Getenv("VEIL_FUZZ_SEED"); s != "" {
		seed, _ = strconv.ParseInt(s, 10, 64)
	}
	r := rand.New(rand.NewSource(seed))
	e := newPoolEnv(t, 50, 5000)
	users := []*rpc.Address{e.alice, e.bob}
	var lpHeld uint64 // total LP in circulation (alice + bob), tracked from model
	for i := 0; i < n; i++ {
		if r.Intn(3) == 0 {
			e.c.NextBlock()
		}
		who := users[r.Intn(2)]
		switch r.Intn(10) {
		case 0, 1:
			d := amount(r, 1<<40)
			a := amount(r, 1<<62)
			if e.model.LP == 0 && r.Intn(4) != 0 {
				d = 100000 + uint64(r.Int63n(1e10))
			}
			var minLP uint64
			if r.Intn(5) == 0 {
				minLP = amount(r, 1<<40)
			}
			var stray uint64
			if r.Intn(50) == 0 {
				stray = 1
			}
			before := e.model.LP
			_, ok := e.add(t, who, d, a, stray, minLP)
			if ok {
				lpHeld += e.model.LP - before
				if before == 0 {
					lpHeld -= ref.MinLiq
				}
			}
		case 2:
			if lpHeld == 0 {
				continue
			}
			l := uint64(r.Int63n(int64(lpHeld))) + 1
			if r.Intn(20) == 0 {
				l = lpHeld + uint64(r.Intn(2000))
			}
			var minD, minA uint64
			if r.Intn(5) == 0 {
				minD = amount(r, 1<<40)
			}
			var stray uint64
			if r.Intn(50) == 0 {
				stray = 1
			}
			before := e.model.LP
			_, ok := e.remove(t, who, stray, 0, l, minD, minA)
			if ok {
				lpHeld -= before - e.model.LP
			}
		case 3, 4, 5, 6:
			d := amount(r, 1<<40)
			var minOut uint64
			if q, ok := e.model.Quote(d); ok && r.Intn(4) == 0 {
				minOut = q + uint64(r.Intn(2)) // exact or +1
			}
			var stray uint64
			if r.Intn(50) == 0 {
				stray = 1
			}
			e.buy(t, who, d, stray, 0, minOut, uint64(r.Intn(2)))
		default:
			a := amount(r, 1<<62)
			var minOut uint64
			if r.Intn(5) == 0 {
				minOut = amount(r, 1<<40)
			}
			var stray uint64
			if r.Intn(50) == 0 {
				stray = 1
			}
			e.sell(t, who, stray, a, 0, minOut, uint64(r.Intn(2)))
		}
		if i%1000 == 0 && e.model.FD > 0 && r.Intn(2) == 0 {
			res := e.c.Call(e.pool, "ClaimFees", nil, nil, e.treasury)
			if !res.OK() {
				t.Fatalf("claim: %v", res.Err)
			}
			e.note("ClaimFees", res)
			e.model.ClaimFees()
			e.checkState(t, "ClaimFees", res, nil, nil)
		}
	}
	t.Logf("fuzz ok: %d ops, final rd=%d ra=%d lp=%d tc=%d pc=%d", n, e.model.RD, e.model.RA, e.model.LP, e.model.TC, e.model.PC)
	e.report(t)
	for k, g := range e.gasMax {
		if g > 10000000 {
			t.Fatalf("%s compute gas %d over the 10M limit", k, g)
		}
	}
}
