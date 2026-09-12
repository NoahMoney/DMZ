package tests

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/deroproject/derohe/rpc"
	ref "github.com/deroproject/derohe/veil/reference"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// S5 (launch staking): dist used to skip for good once tacc*2^32 >= stot (one staked unit and ~43,000 DERO of
// tax) and again once sacc would wrap. It now carries what one increment can and rolls the era for the rest.
func TestLaunchDistSaturation(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	e := newLaunchEnv(t, launchParams{supply: 1000000000 * 100000, grad: 500 * 100000, cbps: 300, tax: 1000, tmode: 3, fee: 50, pfee: 5000})
	e.graduate(t, r)
	e.stake(t, e.creator, 1, 0) // stot = 1: the smallest stake makes the accumulator wrap soonest
	e.buy(t, e.bob, 1<<36, 0, 0, 0)
	if e.m.TAcc < 1<<32 {
		t.Fatalf("tacc %d should exceed one increment (2^32)", e.m.TAcc)
	}
	e.claim(t, e.creator) // carries 2^32-1 units; the rest waits
	if e.m.SEra != 0 || e.m.TAcc == 0 {
		t.Fatalf("after the first claim: era %d tacc %d", e.m.SEra, e.m.TAcc)
	}
	e.claim(t, e.creator) // the remainder wraps sacc: era 1
	if e.m.SEra != 1 || e.c.U(e.scid, "sera") != 1 || e.m.TAcc != 0 || e.m.RPool > 1 {
		t.Fatalf("after the second claim: era %d/%d tacc %d rpool %d", e.m.SEra, e.c.U(e.scid, "sera"), e.m.TAcc, e.m.RPool)
	}
	e.stake(t, e.alice, 1, 0) // joins in era 1
	if e.c.U(e.scid, "se_"+e.alice.String()) != 1 {
		t.Fatal("a new stake starts in the current era")
	}
	e.buy(t, e.bob, 1<<30, 0, 0, 0)
	e.claim(t, e.alice)
	e.claim(t, e.creator)
	e.unstake(t, e.creator, 1, 0, 0)
	if e.c.Exists(e.scid, "se_"+e.creator.String()) {
		t.Fatal("se_ must go with the stake")
	}
	e.buy(t, e.bob, 1<<36, 0, 0, 0) // alice alone across further wraps (two: the carried part and the remainder)
	e.claim(t, e.alice)
	e.claim(t, e.alice)
	if e.m.SEra < 2 || e.c.U(e.scid, "sera") != e.m.SEra || e.m.TAcc != 0 || e.m.RPool > 1 {
		t.Fatalf("era %d/%d tacc %d rpool %d", e.m.SEra, e.c.U(e.scid, "sera"), e.m.TAcc, e.m.RPool)
	}
	t.Logf("eras %d, sacc %d, max gas %v", e.m.SEra, e.m.SAcc, e.gasMax)
}

// S6: a buy that overshoots the curve used to pay fee and tax on the whole deposit, refund included. The fee and
// the tax now apply to the DERO the curve consumes; the rest goes back untouched. Checked from chain state, not
// from the model.
func TestLaunchGraduationRefundFee(t *testing.T) {
	p := launchParams{supply: 1000000 * 100000, grad: 20 * 100000, cbps: 500, tax: 1000, tmode: 3, fee: 100, pfee: 5000}
	e := newLaunchEnv(t, p)
	e.buy(t, e.alice, p.grad/2, 0, 0, 0) // part way up the curve
	c := e.c
	rd, ra, rem := c.U(e.scid, "rd"), c.U(e.scid, "ra"), c.U(e.scid, "cs")-c.U(e.scid, "sold")
	cost, _ := ref.MulDivUp(rem, rd, ra-rem) // the exact price of the remainder, from chain state
	fd0, tacc0, real0 := c.U(e.scid, "fd"), c.U(e.scid, "tacc"), c.U(e.scid, "real")
	d := 10 * p.grad                                                                // a ten-fold overshoot
	sends, ok := e.m.Buy(ref.In{D: d}, 0, 1, e.bob.String(), c.Height, c.Timestamp) // published: the trade record must show the used amount
	r := c.Call(e.scid, "SwapDeroForAsset", rpc.Arguments{harness.U("min_out", 0), harness.U("publish", 1)}, e.in(d, 0), e.bob)
	if !ok || !r.OK() {
		t.Fatalf("graduating buy: model %v chain %v %v", ok, r.OK(), r.Err)
	}
	e.check(t, "graduating buy", r, e.bob, sends)
	refund := r.Sent(zero, e.bob)
	used := d - refund
	feeD, taxD, curveD := c.U(e.scid, "fd")-fd0, c.U(e.scid, "tacc")-tacc0, c.U(e.scid, "real")-real0
	if c.U(e.scid, "phase") != 1 || r.Sent(e.scid, e.bob) != rem {
		t.Fatalf("phase %d, tokens out %d want %d", c.U(e.scid, "phase"), r.Sent(e.scid, e.bob), rem)
	}
	if feeD != ref.BpsUp(used, p.fee) || taxD != ref.Bps(used, p.tax) {
		t.Fatalf("fee %d tax %d on used %d: want %d and %d", feeD, taxD, used, ref.BpsUp(used, p.fee), ref.Bps(used, p.tax))
	}
	if used != feeD+taxD+curveD {
		t.Fatalf("used %d != fee %d + tax %d + curve %d", used, feeD, taxD, curveD)
	}
	if curveD < cost || curveD > cost+2 {
		t.Fatalf("curve took %d for a remainder priced %d", curveD, cost)
	}
	if feeD >= ref.BpsUp(d, p.fee) {
		t.Fatal("fee was charged on the refund")
	}
	if g := c.S(e.scid, fmt.Sprintf("t_%d", e.m.TC-1)); !strings.HasPrefix(g, fmt.Sprintf("1|%s|%d|", e.bob.String(), used)) {
		t.Fatalf("trade record %q should carry the used amount %d", g, used)
	}
	t.Logf("d %d used %d refund %d fee %d tax %d curve %d (exact cost %d)", d, used, refund, feeD, taxD, curveD, cost)
}
