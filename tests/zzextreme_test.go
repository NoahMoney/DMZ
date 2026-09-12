package tests

import (
	"testing"

	ref "github.com/deroproject/derohe/veil/reference"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// Adversarial arithmetic pass: drive pool.bas and launch.bas at the top of the uint64 range with the maximum
// fee and tax, and print every result as a VECTOR line so tests/js can replay the same numbers through
// app/amm.js. A mirror that disagrees with the chain here is a sheet that promises a fill the chain refuses.

const maxU64 = ^uint64(0)

// TestPoolExtremeReserves: the largest reserves the contract can hold, dust trades, and inputs that make
// rd + deff approach 2^64.
func TestPoolExtremeReserves(t *testing.T) {
	e := newPoolEnv(t, 100, 10000) // fee at the 1% ceiling, whole fee to the protocol
	const bigD = uint64(1) << 62
	const bigA = uint64(1) << 61
	e.add(t, e.alice, bigD, bigA, 0, 0)
	t.Logf("first deposit rd=%d ra=%d lp=%d", e.model.RD, e.model.RA, e.model.LP)

	for _, d := range []uint64{1, 2, 99, 100, 101, 10000, 1 << 40, 1 << 61} {
		rd, ra := e.model.RD, e.model.RA
		r, ok := e.buy(t, e.bob, d, 0, 0, 0, 0)
		t.Logf(`VECTOR {"op":"poolBuy","fee":100,"rd":"%d","ra":"%d","in":"%d","minOut":"0","ok":%v,"out":"%d"}`, rd, ra, d, ok, r.Sent(e.asset, e.bob))
	}
	for _, a := range []uint64{1, 2, 100, 1 << 30, 1 << 59} {
		rd, ra := e.model.RD, e.model.RA
		r, ok := e.sell(t, e.bob, 0, a, 0, 0, 0)
		t.Logf(`VECTOR {"op":"poolSell","fee":100,"rd":"%d","ra":"%d","in":"%d","minOut":"0","ok":%v,"out":"%d"}`, rd, ra, a, ok, r.Sent(zero, e.bob))
	}
	t.Logf("after: rd=%d ra=%d fd=%d fa=%d", e.model.RD, e.model.RA, e.model.FD, e.model.FA)
}

// TestPoolQuoteBalanceNearWrap: the guard in buyto is the only thing between a legitimate reserve and a
// wrapped contract balance. Fill the quote side to exactly 2^64-1, then try one unit past it.
func TestPoolQuoteBalanceNearWrap(t *testing.T) {
	e := newPoolEnv(t, 0, 0)
	e.add(t, e.alice, 1<<63, 1<<40, 0, 0)
	room := maxU64 - e.model.RD - e.model.FD
	if _, ok := e.buy(t, e.bob, room, 0, 0, 0, 0); ok {
		t.Logf("filled the quote side to rd=%d fd=%d (sum %d)", e.model.RD, e.model.FD, e.model.RD+e.model.FD)
	} else {
		t.Logf("a buy filling the quote side to 2^64-1 was refused: a revert, not a loss")
	}
	e2 := newPoolEnv(t, 0, 0)
	e2.add(t, e2.alice, 1<<63, 1<<40, 0, 0)
	room2 := maxU64 - e2.model.RD - e2.model.FD
	if _, ok := e2.buy(t, e2.bob, room2+1, 0, 0, 0, 0); ok {
		t.Fatal("a buy that wraps the quote balance was accepted")
	}
	if _, ok := e2.buy(t, e2.bob, maxU64, 0, 0, 0, 0); ok {
		t.Fatal("a buy of 2^64-1 was accepted")
	}
}

// TestLaunchGradCeilingIsUnmodelled: contracts/launch.bas (its BASIC line 25) accepts grad up to 1e15, and
// reference/launch.go NewLaunch now models the same range (reference/launch.go:33). When this test was written the
// reference refused anything above 1e12, so the targets between them — 1 to 1000 XMR on a twelve-decimal quote, the
// product's own market — installed on chain with no model. The test keeps the two ceilings together: every target
// the contract installs must be one the reference models, or the differential fuzz cannot reach it. Its name dates
// from before the fix.
func TestLaunchGradCeilingIsUnmodelled(t *testing.T) {
	code := readContract(t, "launch.bas")
	for _, grad := range []uint64{1000000000001, 1e13, 1e14, 1000000000000000} {
		c := harness.New()
		treasury, creator := c.NewAccount(), c.NewAccount()
		p := launchParams{supply: 1e12, grad: grad, cbps: 0, tax: 0, tmode: 0, fee: 50, pfee: 5000}
		_, res := c.Install(code, nil, launchArgs(p, treasury.String()), creator, harness.MaxStorageGas)
		_, modelOK := ref.NewLaunch(p.supply, p.grad, p.cbps, p.tax, p.tmode, p.fee, p.pfee)
		t.Logf("grad=%d: contract installs=%v, reference models it=%v", grad, res.OK(), modelOK)
		if res.OK() && !modelOK {
			t.Errorf("grad=%d installs on chain but reference/launch.go refuses to model it: the differential fuzz cannot reach it", grad)
		}
	}
}

// TestLaunchExtremeCurve: the largest supply the contract accepts with the fee and tax at their ceilings,
// driven to graduation and beyond, at a graduation target of 1e12 (the reference's ceiling when this test was
// written; it now models up to 1e15).
func TestLaunchExtremeCurve(t *testing.T) {
	const supply = uint64(9223372036854775807)
	const grad = uint64(1000000000000)
	e := newLaunchEnv(t, launchParams{supply: supply, grad: grad, cbps: 500, tax: 1000, tmode: 1, fee: 100, pfee: 10000})
	t.Logf("cs=%d Lq=%d vd0=%d vt0=%d alloc=%d", e.m.C, e.m.Lq, e.m.VD0, e.m.VT0, e.m.Alloc)
	for _, d := range []uint64{1, 100, 10000, 1 << 20, 1 << 40, grad, grad * 2} {
		rd, ra, sold, phase, real := e.m.RD, e.m.RA, e.m.Sold, e.m.Phase, e.m.Real
		ok := e.buy(t, e.bob, d, 0, 0, 0)
		t.Logf(`VECTOR {"op":"launchBuy","fee":100,"tax":1000,"phase":%d,"rd":"%d","ra":"%d","cs":"%d","sold":"%d","real":"%d","in":"%d","ok":%v}`,
			phase, rd, ra, e.m.C, sold, real, d, ok)
	}
	t.Logf("phase=%d sold=%d real=%d rd=%d ra=%d tacc=%d fd=%d", e.m.Phase, e.m.Sold, e.m.Real, e.m.RD, e.m.RA, e.m.TAcc, e.m.FD)
}

// What the curve actually looks like at the contract's real ceiling, grad 1e15, read from the contract's own keys.
// The reference now models this range too (TestLaunchGradCeilingIsUnmodelled).
func TestLaunchCurveAtContractCeiling(t *testing.T) {
	code := readContract(t, "launch.bas")
	for _, sup := range []uint64{1000000, 1e12, 9223372036854775807} {
		c := harness.New()
		treasury, creator := c.NewAccount(), c.NewAccount()
		p := launchParams{supply: sup, grad: 1000000000000000, cbps: 500, tax: 1000, tmode: 1, fee: 100, pfee: 10000}
		scid, res := c.Install(code, nil, launchArgs(p, treasury.String()), creator, harness.MaxStorageGas)
		if !res.OK() {
			t.Fatalf("supply=%d grad=1e15 refused: %v", sup, res.Err)
		}
		cs, ra, vd0 := c.U(scid, "cs"), c.U(scid, "ra"), c.U(scid, "vd0")
		t.Logf("supply=%d grad=1e15: cs=%d ra(vt0)=%d vd0=%d  (ra > cs: %v)", sup, cs, ra, vd0, ra > cs)
		if ra <= cs {
			t.Errorf("supply=%d: ra=%d <= cs=%d, the curve cost muldivup(rem, rd, ra-rem) would divide by zero or wrap", sup, ra, cs)
		}
	}
}
