package tests

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	ref "github.com/deroproject/derohe/veil/reference"
	"github.com/deroproject/derohe/veil/tests/harness"
)

type launchEnv struct {
	c                             *harness.Chain
	treasury, creator, alice, bob *rpc.Address
	scid                          crypto.Hash
	m                             *ref.Launch
	gasMax                        map[string]uint64
	users                         []*rpc.Address
}

type launchParams struct {
	supply, grad, cbps, tax, tmode, fee, pfee uint64
}

func launchArgs(p launchParams, treasury string) rpc.Arguments {
	return rpc.Arguments{
		harness.S("name", "Veil Test Coin"), harness.S("ticker", "VTC"), harness.S("descr", "a test memecoin"),
		harness.S("icon", "https://example.invalid/icon.png"), harness.S("tags", "#test"),
		harness.U("dec", 5), harness.U("supply", p.supply), harness.U("grad", p.grad), harness.U("cbps", p.cbps),
		harness.U("tax", p.tax), harness.U("tmode", p.tmode), harness.S("treasury", treasury),
		harness.U("fee", p.fee), harness.U("pfee", p.pfee),
	}
}

func newLaunchEnv(t *testing.T, p launchParams) *launchEnv {
	t.Helper()
	e := &launchEnv{c: harness.New(), gasMax: map[string]uint64{}}
	e.treasury, e.creator, e.alice, e.bob = e.c.NewAccount(), e.c.NewAccount(), e.c.NewAccount(), e.c.NewAccount()
	e.users = []*rpc.Address{e.alice, e.bob, e.creator}
	code := readContract(t, "launch.bas")
	var res harness.Result
	e.scid, res = e.c.Install(code, nil, launchArgs(p, e.treasury.String()), e.creator, harness.MaxStorageGas)
	m, ok := ref.NewLaunch(p.supply, p.grad, p.cbps, p.tax, p.tmode, p.fee, p.pfee)
	if res.OK() != ok {
		t.Fatalf("install ok=%v (%v) model ok=%v", res.OK(), res.Err, ok)
	}
	if !ok {
		return nil
	}
	e.note("Install", res)
	if res.GasStorage >= harness.MaxStorageGas {
		t.Fatalf("install storage gas %d over cap", res.GasStorage)
	}
	if res.Sent(e.scid, e.creator) != m.Alloc {
		t.Fatalf("creator allocation sent %d want %d", res.Sent(e.scid, e.creator), m.Alloc)
	}
	e.m = m
	t.Logf("launch.bas: %d bytes, install storage gas %d, compute %d; C=%d Lq=%d vd0=%d vt0=%d", len(code), res.GasStorage, res.GasCompute, m.C, m.Lq, m.VD0, m.VT0)
	e.check(t, "install", harness.Result{}, nil, nil)
	return e
}

func (e *launchEnv) note(entry string, r harness.Result) {
	if r.GasCompute > e.gasMax[entry] {
		e.gasMax[entry] = r.GasCompute
	}
}

func (e *launchEnv) in(d, a uint64) map[crypto.Hash]uint64 {
	m := map[crypto.Hash]uint64{}
	if d > 0 {
		m[zero] = d
	}
	if a > 0 {
		m[e.scid] = a
	}
	return m
}

func (e *launchEnv) check(t *testing.T, tag string, r harness.Result, who *rpc.Address, sends []ref.Send) {
	t.Helper()
	m, c := e.m, e.c
	got := map[string]uint64{}
	want := map[string]uint64{"phase": m.Phase, "sold": m.Sold, "real": m.Real, "rd": m.RD, "ra": m.RA, "fd": m.FD, "fa": m.FA, "fc": m.FC,
		"tacc": m.TAcc, "rpool": m.RPool, "locked": m.Locked, "stot": m.STot, "sacc": m.SAcc, "sera": m.SEra, "tc": m.TC, "pc": m.PC, "lh": m.LH, "lt": m.LT}
	for k := range want {
		got[k] = c.U(e.scid, k)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s: %s = %d, model %d\n got %v\nwant %v", tag, k, got[k], v, got, want)
		}
	}
	if m.Phase == 1 {
		if c.U(e.scid, "gh") != m.GH {
			t.Fatalf("%s: gh", tag)
		}
	}
	if b := c.SCBalance(e.scid, zero); b != m.DeroBalance() {
		t.Fatalf("%s: contract DERO balance %d != model %d", tag, b, m.DeroBalance())
	}
	for addr, s := range m.Stakes {
		if c.U(e.scid, "st_"+addr) != s.ST || c.U(e.scid, "sd_"+addr) != s.SD || c.U(e.scid, "se_"+addr) != s.SE {
			t.Fatalf("%s: stake %s: st %d/%d sd %d/%d se %d/%d", tag, addr, c.U(e.scid, "st_"+addr), s.ST, c.U(e.scid, "sd_"+addr), s.SD, c.U(e.scid, "se_"+addr), s.SE)
		}
	}
	for _, u := range e.users {
		if _, ok := m.Stakes[u.String()]; !ok && c.Exists(e.scid, "st_"+u.String()) {
			t.Fatalf("%s: stale stake key for %s", tag, u.String())
		}
	}
	if who != nil {
		wantSend := map[string]uint64{}
		for _, s := range sends {
			wantSend[s.Kind] += s.Amount
		}
		if g := r.Sent(zero, who); g != wantSend["dero"] {
			t.Fatalf("%s: sent dero %d, model %d", tag, g, wantSend["dero"])
		}
		if g := r.Sent(e.scid, who); g != wantSend["asset"] {
			t.Fatalf("%s: sent asset %d, model %d", tag, g, wantSend["asset"])
		}
	}
	if m.TC > 0 {
		tr := m.Trades[m.TC-1]
		want := fmt.Sprintf("%d|%s|%d|%d|%d|%d", tr.Side, tr.Addr, tr.In, tr.Out, tr.H, tr.T)
		if g := c.S(e.scid, fmt.Sprintf("t_%d", m.TC-1)); g != want {
			t.Fatalf("%s: t_%d = %q want %q", tag, m.TC-1, g, want)
		}
	}
	if m.PC > 0 {
		p := m.Points[m.PC-1]
		want := fmt.Sprintf("%d|%d|%d|%d", p.H, p.T, p.RD, p.RA)
		if g := c.S(e.scid, fmt.Sprintf("p_%d", m.PC-1)); g != want {
			t.Fatalf("%s: p_%d = %q want %q", tag, m.PC-1, g, want)
		}
	}
}

func (e *launchEnv) buy(t *testing.T, who *rpc.Address, d, stray, minOut, publish uint64) bool {
	t.Helper()
	sends, ok := e.m.Buy(ref.In{D: d, A: stray}, minOut, publish, who.String(), e.c.Height, e.c.Timestamp)
	r := e.c.Call(e.scid, "SwapDeroForAsset", rpc.Arguments{harness.U("min_out", minOut), harness.U("publish", publish)}, e.in(d, stray), who)
	e.note("Buy", r)
	if r.OK() != ok {
		t.Fatalf("Buy(d=%d min=%d phase=%d): contract ok=%v (%v), model ok=%v", d, minOut, e.m.Phase, r.OK(), r.Err, ok)
	}
	e.check(t, "Buy", r, who, sends)
	return ok
}

func (e *launchEnv) sell(t *testing.T, who *rpc.Address, a, stray, minOut, publish uint64) bool {
	t.Helper()
	sends, ok := e.m.Sell(ref.In{D: stray, A: a}, minOut, publish, who.String(), e.c.Height, e.c.Timestamp)
	r := e.c.Call(e.scid, "SwapAssetForDero", rpc.Arguments{harness.U("min_out", minOut), harness.U("publish", publish)}, e.in(stray, a), who)
	e.note("Sell", r)
	if r.OK() != ok {
		t.Fatalf("Sell(a=%d min=%d phase=%d): contract ok=%v (%v), model ok=%v", a, minOut, e.m.Phase, r.OK(), r.Err, ok)
	}
	e.check(t, "Sell", r, who, sends)
	return ok
}

func (e *launchEnv) stake(t *testing.T, who *rpc.Address, a, stray uint64) bool {
	t.Helper()
	sends, ok := e.m.StakeOp(who.String(), ref.In{D: stray, A: a})
	r := e.c.Call(e.scid, "Stake", nil, e.in(stray, a), who)
	e.note("Stake", r)
	if r.OK() != ok {
		t.Fatalf("Stake(a=%d): contract ok=%v (%v), model ok=%v", a, r.OK(), r.Err, ok)
	}
	e.check(t, "Stake", r, who, sends)
	return ok
}

func (e *launchEnv) unstake(t *testing.T, who *rpc.Address, amt, strayD, strayA uint64) bool {
	t.Helper()
	sends, ok := e.m.Unstake(who.String(), amt, ref.In{D: strayD, A: strayA})
	r := e.c.Call(e.scid, "Unstake", rpc.Arguments{harness.U("amt", amt)}, e.in(strayD, strayA), who)
	e.note("Unstake", r)
	if r.OK() != ok {
		t.Fatalf("Unstake(%d): contract ok=%v (%v), model ok=%v", amt, r.OK(), r.Err, ok)
	}
	e.check(t, "Unstake", r, who, sends)
	return ok
}

func (e *launchEnv) claim(t *testing.T, who *rpc.Address) {
	t.Helper()
	sends, ok := e.m.Claim(who.String())
	r := e.c.Call(e.scid, "Claim", nil, nil, who)
	e.note("Claim", r)
	if r.OK() != ok {
		t.Fatalf("Claim: contract ok=%v (%v), model ok=%v", r.OK(), r.Err, ok)
	}
	e.check(t, "Claim", r, who, sends)
}

func (e *launchEnv) buyback(t *testing.T) {
	t.Helper()
	ok := e.m.Buyback(e.c.Height, e.c.Timestamp)
	r := e.c.Call(e.scid, "Buyback", nil, nil, nil) // anonymous caller is fine here
	e.note("Buyback", r)
	if r.OK() != ok {
		t.Fatalf("Buyback: contract ok=%v (%v), model ok=%v", r.OK(), r.Err, ok)
	}
	e.check(t, "Buyback", r, nil, nil)
}

func (e *launchEnv) claimTax(t *testing.T, who *rpc.Address, expectOK bool) {
	t.Helper()
	r := e.c.Call(e.scid, "ClaimTax", nil, nil, who)
	e.note("ClaimTax", r)
	if r.OK() != expectOK {
		t.Fatalf("ClaimTax by %s: ok=%v (%v) want %v", who.String(), r.OK(), r.Err, expectOK)
	}
	if expectOK {
		sends, _ := e.m.ClaimTax()
		e.check(t, "ClaimTax", r, who, sends)
	}
}

func (e *launchEnv) claimFees(t *testing.T) {
	t.Helper()
	r := e.c.Call(e.scid, "ClaimFees", nil, nil, e.treasury)
	e.note("ClaimFees", r)
	if !r.OK() {
		t.Fatalf("ClaimFees: %v", r.Err)
	}
	sends := e.m.ClaimFees()
	e.check(t, "ClaimFees", r, e.treasury, sends)
}

// graduate buys until the curve is sold out; returns the number of buys.
func (e *launchEnv) graduate(t *testing.T, r *rand.Rand) int {
	n := 0
	for e.m.Phase == 0 {
		d := uint64(r.Int63n(int64(e.m.G/4))) + 100000
		if r.Intn(5) == 0 {
			d = e.m.G * 2 // overshoot: refund path
		}
		e.buy(t, e.users[r.Intn(2)], d, 0, 0, uint64(r.Intn(2)))
		n++
		if n > 10000 {
			t.Fatal("did not graduate")
		}
	}
	return n
}

func TestLaunchInstallValidation(t *testing.T) {
	good := launchParams{supply: 1000000000 * 100000, grad: 500 * 100000, cbps: 300, tax: 200, tmode: 1, fee: 50, pfee: 10000}
	newLaunchEnv(t, good)
	bad := []launchParams{
		{supply: 999999, grad: 500 * 100000, fee: 50},
		{supply: 1 << 63, grad: 500 * 100000, fee: 50},
		{supply: 1e14, grad: 999999, fee: 50},
		{supply: 1e14, grad: 1000000000001, fee: 50},
		{supply: 1e14, grad: 500 * 100000, cbps: 501, fee: 50},
		{supply: 1e14, grad: 500 * 100000, tax: 1001, tmode: 1, fee: 50},
		{supply: 1e14, grad: 500 * 100000, tax: 100, tmode: 0, fee: 50},
		{supply: 1e14, grad: 500 * 100000, tax: 0, tmode: 2, fee: 50},
		{supply: 1e14, grad: 500 * 100000, tmode: 4, tax: 1, fee: 50},
		{supply: 1e14, grad: 500 * 100000, fee: 101},
		{supply: 1e14, grad: 500 * 100000, fee: 50, pfee: 10001},
	}
	for i, p := range bad {
		if e := newLaunchEnv(t, p); e != nil {
			t.Fatalf("bad params %d accepted", i)
		}
	}
	// the curve end price equals the AMM start price up to rounding: (vd0+G)/(vt0-C) vs G/Lq
	m, _ := ref.NewLaunch(good.supply, good.grad, good.cbps, good.tax, good.tmode, good.fee, good.pfee)
	endNum, endDen := m.VD0+m.G, m.VT0-m.C
	ammNum, ammDen := m.G, m.Lq
	lhs, _ := ref.MulDivBig(endNum, ammDen, 1)
	rhs, _ := ref.MulDivBig(ammNum, endDen, 1)
	diff := lhs - rhs
	if lhs < rhs {
		diff = rhs - lhs
	}
	if diff*1000 > lhs { // within 0.1 %
		t.Fatalf("price discontinuity at graduation: %d vs %d", lhs, rhs)
	}
}

func TestLaunchLifecycleCreatorTax(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	e := newLaunchEnv(t, launchParams{supply: 1000000000 * 100000, grad: 500 * 100000, cbps: 300, tax: 200, tmode: 1, fee: 50, pfee: 10000})
	// anonymous caller rejected
	if res := e.c.Call(e.scid, "SwapDeroForAsset", rpc.Arguments{harness.U("min_out", 0), harness.U("publish", 0)}, e.in(100000, 0), nil); res.OK() {
		t.Fatal("anonymous buy accepted")
	}
	e.buy(t, e.alice, 100000, 0, 0, 1)
	e.buy(t, e.alice, 100000, 5, 0, 1) // stray own asset
	e.c.NextBlock()
	// sell back part of what alice bought; creator cannot sell allocation beyond sold
	e.sell(t, e.alice, 1000, 0, 0, 0)
	e.sell(t, e.creator, e.m.Sold+1, 0, 0, 0)
	e.sell(t, e.creator, e.m.Sold, 0, 0, 1)
	// slippage exact / +1
	q, _ := e.m.QuoteBuy(300000)
	e.buy(t, e.bob, 300000, 0, q+1, 0)
	e.buy(t, e.bob, 300000, 0, q, 0)
	e.c.NextBlock()
	n := e.graduate(t, r)
	if e.m.Phase != 1 || e.m.Sold != e.m.C || e.m.RA != e.m.Lq || e.m.RD != e.m.Real {
		t.Fatalf("graduation state phase=%d sold=%d C=%d", e.m.Phase, e.m.Sold, e.m.C)
	}
	t.Logf("graduated after %d buys; rd=%d ra=%d fd=%d tacc=%d", n, e.m.RD, e.m.RA, e.m.FD, e.m.TAcc)
	// AMM phase
	e.c.NextBlock()
	e.buy(t, e.bob, 250000, 0, 0, 1)
	e.sell(t, e.bob, 100000000, 0, 0, 1)
	e.sell(t, e.alice, 1, 0, 0, 0) // dust: out == 0 → reject
	// tax: only creator, mode 1
	e.claimTax(t, e.alice, false)
	e.claimTax(t, e.creator, true)
	e.claimFees(t)
	// Buyback / staking must be rejected in mode 1
	if res := e.c.Call(e.scid, "Buyback", nil, nil, nil); res.OK() {
		t.Fatal("Buyback accepted in mode 1")
	}
	if res := e.c.Call(e.scid, "Stake", nil, e.in(0, 100), e.alice); res.OK() {
		t.Fatal("Stake accepted in mode 1")
	}
	t.Logf("max gas: %v", e.gasMax)
}

func TestLaunchBuyback(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	e := newLaunchEnv(t, launchParams{supply: 21000000 * 100000, grad: 10 * 100000, cbps: 0, tax: 500, tmode: 2, fee: 50, pfee: 5000})
	e.buyback(t) // phase 0: rejected
	e.graduate(t, r)
	e.c.NextBlock()
	e.buy(t, e.alice, 50000, 0, 0, 0)
	if e.m.TAcc == 0 {
		t.Fatal("no tax accrued")
	}
	// one call spends at most 1 % of the DERO reserve (the phase-0 tax accrued to more than that here) and the
	// next call needs 20 blocks: a buy → Buyback → sell sandwich cannot beat the fees
	before, cap := e.m.TAcc, e.m.RD/100
	e.buyback(t)
	if e.m.Locked == 0 || e.m.TAcc != before-minU(before, cap) {
		t.Fatalf("buyback: locked %d, tacc %d → %d, cap %d", e.m.Locked, before, e.m.TAcc, cap)
	}
	e.buyback(t) // same block: refused (spacing), model and contract alike
	if e.m.TAcc != before-minU(before, cap) {
		t.Fatal("a second buyback in the same block spent tax")
	}
	e.sell(t, e.alice, 100000, 0, 0, 0)
	for i := 0; i < 20 && e.m.TAcc > 0; i++ { // drain the rest, one capped call per 20 blocks
		for j := 0; j < 20; j++ {
			e.c.NextBlock()
		}
		e.buyback(t)
	}
	if e.m.TAcc != 0 {
		t.Fatalf("tax not drained after 20 capped buybacks: %d left", e.m.TAcc)
	}
	e.buyback(t) // nothing accrued: rejected
	t.Logf("locked %d tokens; max gas %v", e.m.Locked, e.gasMax)
}

func TestLaunchStaking(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	e := newLaunchEnv(t, launchParams{supply: 1000000 * 100000, grad: 20 * 100000, cbps: 500, tax: 1000, tmode: 3, fee: 50, pfee: 5000})
	// creator stakes part of the allocation before any trade
	e.stake(t, e.creator, 1000000, 0)
	e.stake(t, e.creator, 0, 0)        // zero
	e.stake(t, e.creator, 100, 1)      // stray DERO
	e.buy(t, e.alice, 500000, 0, 0, 0) // tax accrues
	e.claim(t, e.creator)              // creator gets the whole distribution
	if e.m.RPool != 0 && e.m.RPool > 1 {
		t.Fatalf("rpool dust %d", e.m.RPool)
	}
	e.stake(t, e.alice, 100000, 0)
	e.buy(t, e.bob, 500000, 0, 0, 0)
	e.claim(t, e.alice)
	e.claim(t, e.creator)
	e.unstake(t, e.alice, 100001, 0, 0) // more than staked
	e.unstake(t, e.alice, 50000, 0, 0)
	e.unstake(t, e.alice, 50000, 0, 0) // full exit deletes keys
	e.claim(t, e.alice)                // nothing pending
	e.graduate(t, r)
	e.buy(t, e.bob, 100000, 0, 0, 0)
	e.unstake(t, e.creator, 1000000, 0, 0)
	if e.m.STot != 0 {
		t.Fatal("stot")
	}
	// tax with nobody staking accrues in tacc until someone stakes
	e.buy(t, e.bob, 100000, 0, 0, 0)
	if e.m.TAcc == 0 {
		t.Fatal("tacc should accrue with no stakers")
	}
	e.stake(t, e.bob, 10, 0)
	e.claim(t, e.bob)
	t.Logf("max gas %v", e.gasMax)
}

func TestLaunchFuzz(t *testing.T) {
	n := 6000
	if s := os.Getenv("VEIL_FUZZ_N"); s != "" {
		n, _ = strconv.Atoi(s)
	}
	seed := int64(11)
	if s := os.Getenv("VEIL_FUZZ_SEED"); s != "" {
		seed, _ = strconv.ParseInt(s, 10, 64)
	}
	r := rand.New(rand.NewSource(seed))
	for _, p := range []launchParams{
		{supply: 1000000000 * 100000, grad: 500 * 100000, cbps: 300, tax: 200, tmode: 3, fee: 50, pfee: 5000},
		{supply: 1000000, grad: 1000000, cbps: 500, tax: 1000, tmode: 2, fee: 100, pfee: 0},
		{supply: 1 << 62, grad: 1000000000000, cbps: 0, tax: 0, tmode: 0, fee: 50, pfee: 10000},
	} {
		e := newLaunchEnv(t, p)
		held := map[string]uint64{} // tokens each user holds according to the model (creator starts with alloc)
		held[e.creator.String()] = e.m.Alloc
		for i := 0; i < n; i++ {
			if r.Intn(3) == 0 {
				e.c.NextBlock()
			}
			who := e.users[r.Intn(3)]
			switch r.Intn(12) {
			case 0, 1, 2, 3, 4:
				d := amount(r, 1<<40)
				if r.Intn(3) == 0 {
					d = uint64(r.Int63n(int64(e.m.G))) + 1
				}
				var minOut uint64
				if q, ok := e.m.QuoteBuy(d); ok && r.Intn(4) == 0 {
					minOut = q + uint64(r.Intn(2))
				}
				var stray uint64
				if r.Intn(40) == 0 {
					stray = 1
				}
				before := e.m.Sold
				if e.buy(t, who, d, stray, minOut, uint64(r.Intn(2))) {
					if e.m.Phase == 0 || before != e.m.Sold {
						held[who.String()] += e.m.Sold - before
					} else { // amm buy: tokens out are the last trade's out? recompute via sends is simpler
						held[who.String()] += 0
					}
				}
			case 5, 6, 7:
				h := held[who.String()]
				if h == 0 {
					continue
				}
				a := uint64(r.Int63n(int64(h%(1<<62)))) + 1
				if r.Intn(10) == 0 {
					a = h + 1 + uint64(r.Intn(1000)) // more than held: fine for the harness, exercises a > sold
				}
				var stray uint64
				if r.Intn(40) == 0 {
					stray = 1
				}
				if e.sell(t, who, a, stray, 0, uint64(r.Intn(2))) && a <= h {
					held[who.String()] -= a
				}
			case 8:
				if e.m.TMode == 3 {
					a := amount(r, 1<<40)
					if e.stake(t, who, a, 0) {
						// staked tokens come from anywhere in the harness
					}
				} else if e.m.TMode == 2 {
					e.buyback(t)
				} else if e.m.TMode == 1 {
					e.claimTax(t, e.creator, true)
				}
			case 9:
				if e.m.TMode == 3 {
					if s, ok := e.m.Stakes[who.String()]; ok {
						amt := uint64(r.Int63n(int64(s.ST))) + 1
						if r.Intn(5) == 0 {
							amt = s.ST
						}
						e.unstake(t, who, amt, 0, 0)
					} else {
						e.unstake(t, who, 1, 0, 0)
					}
				}
			case 10:
				if e.m.TMode == 3 {
					e.claim(t, who)
				}
			default:
				if e.m.FD > 0 && r.Intn(4) == 0 {
					e.claimFees(t)
				}
			}
		}
		t.Logf("fuzz ok: %d ops, phase=%d rd=%d ra=%d sold=%d/%d tc=%d pc=%d locked=%d stot=%d; max gas %v",
			n, e.m.Phase, e.m.RD, e.m.RA, e.m.Sold, e.m.C, e.m.TC, e.m.PC, e.m.Locked, e.m.STot, e.gasMax)
		for k, g := range e.gasMax {
			if g > 10000000 {
				t.Fatalf("%s compute gas %d over the 10M limit", k, g)
			}
		}
	}
}

func minU(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
