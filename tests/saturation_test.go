package tests

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	ref "github.com/deroproject/derohe/veil/reference"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// S5 (founders): the reward accumulator used to stop paying for good once acc + inc wrapped 2^64; every later
// deposit fell into und and stayed there. Eras turn the wrap into bookkeeping: the closed era keeps its final
// accumulator under ea_<era>, and settle walks every era a holder missed.
func TestFoundersAccSaturation(t *testing.T) {
	c := harness.New()
	creator, treasury := c.NewAccount(), c.NewAccount()
	scid := installFounders(t, c, creator, treasury, 0, 49, 0, 0, 5000, 250) // maxsupply 49: minhst = 1, so two registered units already distribute
	a, b := c.NewAccount(), c.NewAccount()
	for _, who := range []*rpc.Address{a, b} {
		if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 1)}, nil, who); !r.OK() {
			t.Fatalf("mint: %v", r.Err)
		}
		if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 1}, who); !r.OK() {
			t.Fatalf("register: %v", r.Err)
		}
	}
	const d = 6_000_000_000 // hst = 2: each deposit adds d*2^31 to acc, so the second one wraps 2^64
	deposit := func() {
		t.Helper()
		if r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{zero: d}, nil); !r.OK() {
			t.Fatalf("deposit: %v", r.Err)
		}
	}
	claim := func(who *rpc.Address) uint64 {
		t.Helper()
		r := c.Call(scid, "Claim", nil, nil, who)
		if !r.OK() {
			t.Fatalf("claim: %v", r.Err)
		}
		return r.Sent(zero, who)
	}
	deposit()
	if got := claim(a); got != d/2 {
		t.Fatalf("first claim %d want %d", got, d/2)
	}
	if c.U(scid, "era") != 0 {
		t.Fatal("no era should have closed yet")
	}
	deposit()
	if c.U(scid, "era") != 1 || !c.Exists(scid, "ea_0") {
		t.Fatalf("second deposit must close era 0: era %d acc %d und %d", c.U(scid, "era"), c.U(scid, "acc"), c.U(scid, "und"))
	}
	if c.U(scid, "und") != 0 {
		t.Fatalf("und %d: the deposit was not distributed", c.U(scid, "und"))
	}
	if got := claim(a); got != d/2 {
		t.Fatalf("claim across the era boundary %d want %d", got, d/2)
	}
	if got := claim(b); got != d {
		t.Fatalf("claim spanning both eras %d want %d", got, d)
	}
	if c.U(scid, "rpool") != 0 || c.U(scid, "paid") != 2*d {
		t.Fatalf("rpool %d paid %d", c.U(scid, "rpool"), c.U(scid, "paid"))
	}
	// era 1 keeps paying; a full withdrawal settles first; a re-registration starts in the current era
	deposit()
	r := c.Call(scid, "Withdraw", rpc.Arguments{harness.U("u", 1)}, nil, b)
	if !r.OK() || r.Sent(zero, b) != d/2 || r.Sent(scid, b) != 1 {
		t.Fatalf("withdraw: ok=%v dero %d asset %d", r.OK(), r.Sent(zero, b), r.Sent(scid, b))
	}
	if c.Exists(scid, "he_"+b.String()) {
		t.Fatal("he_ must go with the holder")
	}
	if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 1}, b); !r.OK() {
		t.Fatalf("re-register: %v", r.Err)
	}
	// deposit three wrapped the accumulator again (every deposit adds 0.7 x 2^64), so this is era 2
	if c.U(scid, "era") != 2 || c.U(scid, "he_"+b.String()) != 2 {
		t.Fatalf("a new holder starts in the current era: era %d he %d", c.U(scid, "era"), c.U(scid, "he_"+b.String()))
	}
	deposit()
	if got := claim(b); got != d/2 {
		t.Fatalf("re-registered claim %d want %d", got, d/2)
	}
	if got := claim(a); got != d {
		t.Fatalf("a over deposits three and four: %d want %d", got, d)
	}
	checkFounders(t, c, scid)
	t.Logf("eras %d, acc %d, paid %d", c.U(scid, "era"), c.U(scid, "acc"), c.U(scid, "paid"))
}

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
