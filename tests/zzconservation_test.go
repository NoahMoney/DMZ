package tests

// MT-A13 (docs/MAINNET_TEST_PLAN.md) with GAP-022 and GAP-033 (docs/MAINNET_GAP_ANALYSIS.md): conservation checked
// after every call, accepted or refused, for pool, launch, nft, founders, swap and board, each driven by a seeded
// random sequence of calls from several wallets. Per contract and per asset, what the contract owes (reserves, fees
// owed, stakes, rewards, locks by state) must equal what it holds, which is what each design states. board holds
// nothing, so its balance must stay 0.
//
// Own tokens: the chain keeps no balance for a contract's own asset — StoreSCValue drops the write, "an scid can mint
// its assets infinitely" (vendor/derohe/dvm/sc.go 392-395) — so each own-token supply is bounded only by the
// contract's arithmetic. consLedger follows every unit the contract sent to a wallet and every unit a wallet sent
// back, and each test compares that with what the contract's keys say is out: pool LP == lp - 1000; launch tokens
// == alloc + sold - staked in phase 0 and supply - ra - fa - locked - staked in phase 1; founders units == minted -
// hst - listed; nft editions == minted.
//
// The harness does not check that a signer holds what it attaches, so the drivers attach own-token units only up to
// what consLedger says that wallet holds. DERO and the stand-in assets are unlimited, as in every other test here.
// VEIL_CONS_N sets the number of steps (default 400).

import (
	"crypto/sha256"
	"encoding/hex"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

func consSteps() int {
	if n, err := strconv.Atoi(os.Getenv("VEIL_CONS_N")); err == nil && n > 0 {
		return n
	}
	return 400
}

// consLedger follows a contract's own asset through the transfers the chain made.
type consLedger struct {
	scid crypto.Hash
	held map[string]uint64
}

func newConsLedger(scid crypto.Hash) *consLedger { return &consLedger{scid: scid, held: map[string]uint64{}} }

// apply books one call: on success what the signer attached of the own asset left its wallet and what the contract
// sent reached the addressee; a refused call moved nothing (the DVM discarded it and refunded the signer).
func (l *consLedger) apply(t *testing.T, r harness.Result, in map[crypto.Hash]uint64, who *rpc.Address) {
	t.Helper()
	if !r.OK() {
		return
	}
	if v := in[l.scid]; v > 0 {
		if who == nil || l.held[raw(who)] < v {
			t.Fatalf("the driver attached %d own units it does not hold", v)
		}
		l.held[raw(who)] -= v
	}
	for _, tr := range r.Transfers {
		if tr.Asset == l.scid {
			l.held[tr.Address] += tr.Amount
		}
	}
}

func (l *consLedger) of(a *rpc.Address) uint64 { return l.held[raw(a)] }

func (l *consLedger) total() uint64 {
	var s uint64
	for _, v := range l.held {
		s += v
	}
	return s
}

// consTally counts accepted and refused calls, so each test shows it drove both.
type consTally struct{ ok, refused int }

func (y *consTally) note(r harness.Result) {
	if r.OK() {
		y.ok++
	} else {
		y.refused++
	}
}

func (y *consTally) require(t *testing.T, name string) {
	t.Helper()
	if y.ok == 0 || y.refused == 0 {
		t.Fatalf("%s: %d accepted and %d refused calls; the driver must exercise both", name, y.ok, y.refused)
	}
	t.Logf("%s: conservation held after each of %d accepted and %d refused calls", name, y.ok, y.refused)
}

func consPick(rng *rand.Rand, us []*rpc.Address) *rpc.Address { return us[rng.Intn(len(us))] }

// consUpTo is uniform in 1..n, or 0 when n is 0.
func consUpTo(rng *rand.Rand, n uint64) uint64 {
	if n == 0 {
		return 0
	}
	return 1 + uint64(rng.Int63n(int64(n)))
}

func consDero(d uint64) map[crypto.Hash]uint64 {
	if d == 0 {
		return nil
	}
	return map[crypto.Hash]uint64{zero: d}
}

// ---- pool.bas: DERO == rd + fd, asset == ra + fa, LP in wallets == lp - 1000 ------------------------------------

func TestConservationPool(t *testing.T) {
	e := newPoolEnv(t, 50, 5000)
	c := e.c
	carol := c.NewAccount()
	if r := c.Call(e.asset, "Mint", rpc.Arguments{harness.U("amount", 1<<62)}, nil, carol); !r.OK() {
		t.Fatalf("mint: %v", r.Err)
	}
	users := []*rpc.Address{e.alice, e.bob, carol}
	lp := newConsLedger(e.pool)
	var y consTally
	rng := rand.New(rand.NewSource(20260911))
	step := 0
	check := func(what string) {
		t.Helper()
		if d, rd, fd := c.SCBalance(e.pool, zero), c.U(e.pool, "rd"), c.U(e.pool, "fd"); d != rd+fd {
			t.Fatalf("step %d %s: the pool holds %d DERO and owes rd %d + fd %d", step, what, d, rd, fd)
		}
		if a, ra, fa := c.SCBalance(e.pool, e.asset), c.U(e.pool, "ra"), c.U(e.pool, "fa"); a != ra+fa {
			t.Fatalf("step %d %s: the pool holds %d of the asset and owes ra %d + fa %d", step, what, a, ra, fa)
		}
		var want uint64
		if s := c.U(e.pool, "lp"); s > 0 {
			want = s - 1000
		}
		if got := lp.total(); got != want {
			t.Fatalf("step %d %s: %d LP in wallets, lp - 1000 = %d", step, what, got, want)
		}
	}
	call := func(what, entry string, args rpc.Arguments, in map[crypto.Hash]uint64, who *rpc.Address) {
		t.Helper()
		r := c.Call(e.pool, entry, args, in, who)
		lp.apply(t, r, in, who)
		y.note(r)
		check(what)
	}
	minOut := func() uint64 {
		if rng.Intn(5) == 0 {
			return 1 << 60
		}
		return 0
	}
	swap := func(min uint64) rpc.Arguments {
		return rpc.Arguments{harness.U("min_out", min), harness.U("publish", uint64(rng.Intn(2)))}
	}
	swapTo := func(dest *rpc.Address, min uint64) rpc.Arguments {
		return rpc.Arguments{harness.S("dest", dest.String()), harness.U("min_out", min), harness.U("publish", 0)}
	}
	remove := rpc.Arguments{harness.U("min_dero", 0), harness.U("min_asset", 0)}
	call("seed", "AddLiquidity", rpc.Arguments{harness.U("min_lp", 1)}, e.in(50_000_000, 50_000_000, 0), e.alice)
	for step = 0; step < consSteps(); step++ {
		who := consPick(rng, users)
		switch rng.Intn(10) {
		case 0:
			call("add", "AddLiquidity", rpc.Arguments{harness.U("min_lp", 1)}, e.in(consUpTo(rng, 5_000_000), consUpTo(rng, 5_000_000), 0), who)
		case 1:
			call("remove", "RemoveLiquidity", remove, e.in(0, 0, consUpTo(rng, lp.of(who))), who)
		case 2:
			call("buy", "SwapDeroForAsset", swap(minOut()), e.in(consUpTo(rng, 2_000_000), 0, 0), who)
		case 3:
			call("sell", "SwapAssetForDero", swap(minOut()), e.in(0, consUpTo(rng, 2_000_000), 0), who)
		case 4:
			call("buy for another", "SwapDeroForAssetTo", swapTo(consPick(rng, users), minOut()), e.in(consUpTo(rng, 2_000_000), 0, 0), who)
		case 5:
			call("sell for another", "SwapAssetForDeroTo", swapTo(consPick(rng, users), minOut()), e.in(0, consUpTo(rng, 2_000_000), 0), who)
		case 6:
			var by *rpc.Address
			if rng.Intn(2) == 0 {
				by = who
			}
			call("claim fees", "ClaimFees", nil, nil, by)
		case 7:
			call("remove with DERO attached", "RemoveLiquidity", remove, e.in(1000, 0, consUpTo(rng, lp.of(who))), who)
		case 8:
			var l uint64
			if lp.of(who) > 0 {
				l = 1
			}
			call("buy with LP attached", "SwapDeroForAsset", swap(0), e.in(100_000, 0, l), who)
		default:
			c.NextBlock()
		}
	}
	y.require(t, "pool.bas")
}

// ---- launch.bas: DERO == real|rd + fd + tacc + rpool; curve keys; tokens out == alloc + sold, or supply - held --

func TestConservationLaunch(t *testing.T) {
	for _, tmode := range []uint64{1, 2, 3} {
		name := "launch.bas tmode " + itoa(tmode)
		c := harness.New()
		treasury, creator, alice, bob := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
		users := []*rpc.Address{creator, alice, bob}
		p := launchParams{supply: 1_000_000 * 100000, grad: 3_000_000, cbps: 100, tax: 100, tmode: tmode, fee: 50, pfee: 5000}
		scid, res := c.Install(readContract(t, "launch.bas"), nil, launchArgs(p, treasury.String()), creator, harness.MaxStorageGas)
		if !res.OK() {
			t.Fatalf("%s install: %v", name, res.Err)
		}
		tok := newConsLedger(scid)
		tok.apply(t, res, nil, creator)
		alloc, supply, vd0, vt0 := tok.total(), c.U(scid, "S"), c.U(scid, "vd0"), c.U(scid, "ra")
		var y consTally
		rng := rand.New(rand.NewSource(int64(tmode)))
		step := 0
		check := func(what string) {
			t.Helper()
			dero, fd, tacc, rpool := c.SCBalance(scid, zero), c.U(scid, "fd"), c.U(scid, "tacc"), c.U(scid, "rpool")
			out, stot := tok.total(), c.U(scid, "stot")
			if c.U(scid, "phase") == 0 {
				real, sold := c.U(scid, "real"), c.U(scid, "sold")
				if dero != real+fd+tacc+rpool {
					t.Fatalf("%s step %d %s: holds %d DERO, owes real %d + fd %d + tacc %d + rpool %d", name, step, what, dero, real, fd, tacc, rpool)
				}
				if c.U(scid, "rd")-real != vd0 || c.U(scid, "ra")+sold != vt0 {
					t.Fatalf("%s step %d %s: the curve drifted: rd - real %d (vd0 %d), ra + sold %d (vt0 %d)", name, step, what, c.U(scid, "rd")-real, vd0, c.U(scid, "ra")+sold, vt0)
				}
				if out+stot != alloc+sold {
					t.Fatalf("%s step %d %s: %d tokens in wallets + %d staked, but alloc %d + sold %d", name, step, what, out, stot, alloc, sold)
				}
				return
			}
			if rd := c.U(scid, "rd"); dero != rd+fd+tacc+rpool {
				t.Fatalf("%s step %d %s: holds %d DERO, owes rd %d + fd %d + tacc %d + rpool %d", name, step, what, dero, rd, fd, tacc, rpool)
			}
			if ra, fa, locked := c.U(scid, "ra"), c.U(scid, "fa"), c.U(scid, "locked"); out+stot+ra+fa+locked != supply {
				t.Fatalf("%s step %d %s: wallets %d + staked %d + ra %d + fa %d + locked %d != supply %d", name, step, what, out, stot, ra, fa, locked, supply)
			}
		}
		in := func(d, a uint64) map[crypto.Hash]uint64 {
			m := map[crypto.Hash]uint64{}
			if d > 0 {
				m[zero] = d
			}
			if a > 0 {
				m[scid] = a
			}
			return m
		}
		call := func(what, entry string, args rpc.Arguments, m map[crypto.Hash]uint64, who *rpc.Address) {
			t.Helper()
			r := c.Call(scid, entry, args, m, who)
			tok.apply(t, r, m, who)
			y.note(r)
			check(what)
		}
		minOut := func() uint64 {
			if rng.Intn(5) == 0 {
				return 1 << 60
			}
			return 0
		}
		swap := func(min uint64) rpc.Arguments {
			return rpc.Arguments{harness.U("min_out", min), harness.U("publish", uint64(rng.Intn(2)))}
		}
		swapTo := func(dest *rpc.Address, min uint64) rpc.Arguments {
			return rpc.Arguments{harness.S("dest", dest.String()), harness.U("min_out", min), harness.U("publish", 0)}
		}
		staked := func(a *rpc.Address) uint64 { v, _ := c.Var(scid, "st_"+a.String()).(uint64); return v }
		check("install")
		randomStep := func() {
			who := consPick(rng, users)
			switch rng.Intn(12) {
			case 0, 1, 2:
				call("buy", "SwapDeroForAsset", swap(minOut()), in(consUpTo(rng, 400_000), 0), who)
			case 3:
				call("sell", "SwapAssetForDero", swap(minOut()), in(0, consUpTo(rng, tok.of(who))), who)
			case 4:
				call("buy for another", "SwapDeroForAssetTo", swapTo(consPick(rng, users), minOut()), in(consUpTo(rng, 400_000), 0), who)
			case 5:
				call("sell for another", "SwapAssetForDeroTo", swapTo(consPick(rng, users), minOut()), in(0, consUpTo(rng, tok.of(who))), who)
			case 6:
				var by *rpc.Address
				if rng.Intn(2) == 0 {
					by = who
				}
				call("claim fees", "ClaimFees", nil, nil, by)
			case 7:
				switch tmode {
				case 1:
					call("claim tax", "ClaimTax", nil, nil, who)
				case 2:
					call("buyback", "Buyback", nil, nil, who)
				default:
					call("stake", "Stake", nil, in(0, consUpTo(rng, tok.of(who))), who)
				}
			case 8:
				if tmode == 3 {
					amt := consUpTo(rng, staked(who))
					if rng.Intn(6) == 0 {
						amt = staked(who) + 1
					}
					call("unstake", "Unstake", rpc.Arguments{harness.U("amt", amt)}, nil, who)
				} else {
					call("stake outside tmode 3", "Stake", nil, in(0, consUpTo(rng, tok.of(who))), who)
				}
			case 9:
				switch {
				case tmode != 3:
					call("distribute outside tmode 3", "Distribute", nil, nil, who)
				case rng.Intn(2) == 0:
					call("claim", "Claim", nil, nil, who)
				default:
					call("distribute", "Distribute", nil, nil, who)
				}
			case 10:
				if rng.Intn(2) == 0 {
					call("sell with DERO attached", "SwapAssetForDero", swap(0), in(1000, consUpTo(rng, tok.of(who))), who)
				} else {
					call("buy with tokens attached", "SwapDeroForAsset", swap(0), in(100_000, consUpTo(rng, tok.of(who))), who)
				}
			default:
				for i := rng.Intn(25); i >= 0; i-- {
					c.NextBlock()
				}
			}
		}
		for step = 0; step < consSteps()/2; step++ {
			randomStep()
		}
		// drive the curve to graduation, so the phase-1 invariants are always exercised; each buy is checked like any call
		for i := 0; c.U(scid, "phase") == 0; i++ {
			if i > 2000 {
				t.Fatalf("%s: 2000 buys did not graduate the launch", name)
			}
			step++
			call("buy toward graduation", "SwapDeroForAsset", swap(0), in(400_000, 0), consPick(rng, users))
		}
		for ; step < consSteps()+consSteps()/2; step++ {
			randomStep()
		}
		y.require(t, name)
		if c.U(scid, "phase") != 1 {
			t.Fatalf("%s: the launch never graduated, so the phase-1 invariants were not exercised", name)
		}
	}
}

// ---- nft.bas: DERO == fd + cacc; token == sum of lk_; editions in wallets == minted ----------------------------

func TestConservationNFT(t *testing.T) {
	c := harness.New()
	treasury, creator := c.NewAccount(), c.NewAccount()
	users := []*rpc.Address{creator, c.NewAccount(), c.NewAccount(), c.NewAccount()}
	token, res := c.Install(assetCode, nil, nil, creator, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatal(res.Err)
	}
	for _, u := range users {
		if r := c.Call(token, "Mint", rpc.Arguments{harness.U("amount", 1<<40)}, nil, u); !r.OK() {
			t.Fatal(r.Err)
		}
	}
	const price = 100000
	scid, res := c.Install(readContract(t, "nft.bas"), nil, rpc.Arguments{harness.S("name", "Badge"), harness.S("descr", ""),
		harness.S("icon", ""), harness.S("file", ""), harness.S("tags", ""), harness.S("collection", "c"), harness.H("token", token),
		harness.U("minhold", 1000), harness.U("holdblocks", 2), harness.U("maxsupply", 30), harness.U("price", price),
		harness.S("treasury", treasury.String()), harness.U("fee", 50)}, creator, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	eds := newConsLedger(scid)
	var y consTally
	rng := rand.New(rand.NewSource(11))
	step := 0
	check := func(what string) {
		t.Helper()
		if d, fd, cacc := c.SCBalance(scid, zero), c.U(scid, "fd"), c.U(scid, "cacc"); d != fd+cacc {
			t.Fatalf("step %d %s: holds %d DERO and owes fd %d + cacc %d", step, what, d, fd, cacc)
		}
		var locked uint64
		for _, u := range users {
			if v, ok := c.Var(scid, "lk_"+u.String()).(uint64); ok {
				locked += v
			}
		}
		if h := c.SCBalance(scid, token); h != locked {
			t.Fatalf("step %d %s: holds %d of the token and owes the lockers %d", step, what, h, locked)
		}
		if out, minted := eds.total(), c.U(scid, "minted"); out != minted {
			t.Fatalf("step %d %s: %d editions in wallets, minted %d", step, what, out, minted)
		}
	}
	call := func(what, entry string, in map[crypto.Hash]uint64, who *rpc.Address) {
		t.Helper()
		r := c.Call(scid, entry, nil, in, who)
		eds.apply(t, r, in, who)
		y.note(r)
		check(what)
	}
	for step = 0; step < consSteps(); step++ {
		who := consPick(rng, users)
		switch rng.Intn(9) {
		case 0:
			call("lock", "Lock", map[crypto.Hash]uint64{token: 500 + uint64(rng.Intn(5500))}, who)
		case 1, 2:
			call("mint", "Mint", consDero(price-1000+uint64(rng.Intn(60000))), who)
		case 3:
			call("unlock", "Unlock", nil, who)
		case 4:
			call("claim creator", "ClaimCreator", nil, who)
		case 5:
			var by *rpc.Address
			if rng.Intn(2) == 0 {
				by = who
			}
			call("claim fees", "ClaimFees", nil, by)
		case 6:
			if rng.Intn(2) == 0 {
				call("unlock with DERO attached", "Unlock", consDero(1000), who)
			} else {
				call("mint with the token attached", "Mint", map[crypto.Hash]uint64{zero: price, token: 1}, who)
			}
		case 7:
			call("claim fees with DERO attached", "ClaimFees", consDero(1000), who)
		default:
			c.NextBlock()
		}
	}
	y.require(t, "nft.bas")
}

// ---- swap.bas: per asset, the contract holds exactly the open locks in it -----------------------------------------

func TestConservationSwap(t *testing.T) {
	c := harness.New()
	users := []*rpc.Address{c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()}
	asset, res := c.Install(assetCode, nil, nil, users[0], harness.MaxStorageGas)
	if !res.OK() {
		t.Fatal(res.Err)
	}
	for _, u := range users {
		if r := c.Call(asset, "Mint", rpc.Arguments{harness.U("amount", 1<<40)}, nil, u); !r.OK() {
			t.Fatal(r.Err)
		}
	}
	scid, res := c.Install(readContract(t, "swap.bas"), nil, swapInit(), users[0], harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	secrets := map[uint64]string{}
	var y consTally
	rng := rand.New(rand.NewSource(13))
	step := 0
	check := func(what string) {
		t.Helper()
		var dero, held uint64
		for i := uint64(0); i < c.U(scid, "n"); i++ {
			if c.U(scid, "st_"+itoa(i)) != 0 {
				continue
			}
			if v := c.U(scid, "v_"+itoa(i)); c.S(scid, "a_"+itoa(i)) == "" {
				dero += v
			} else {
				held += v
			}
		}
		if c.SCBalance(scid, zero) != dero || c.SCBalance(scid, asset) != held {
			t.Fatalf("step %d %s: holds %d DERO and %d of the asset; the open locks are %d and %d", step, what, c.SCBalance(scid, zero), c.SCBalance(scid, asset), dero, held)
		}
	}
	call := func(what, entry string, args rpc.Arguments, in map[crypto.Hash]uint64, who *rpc.Address) harness.Result {
		t.Helper()
		r := c.Call(scid, entry, args, in, who)
		y.note(r)
		check(what)
		return r
	}
	idOf := func() uint64 { return uint64(rng.Int63n(int64(c.U(scid, "n")) + 1)) }
	for step = 0; step < consSteps(); step++ {
		who := consPick(rng, users)
		switch rng.Intn(8) {
		case 0, 1:
			var sec [32]byte
			rng.Read(sec[:])
			sum := sha256.Sum256(sec[:])
			taker := consPick(rng, users).String() // swap.bas v2 refuses a lock that names no taker (GAP-024)
			v := consUpTo(rng, 1_000_000)
			assetArg, in := "", map[crypto.Hash]uint64{zero: v}
			if rng.Intn(2) == 0 {
				assetArg, in = hex.EncodeToString(asset[:]), map[crypto.Hash]uint64{asset: v}
				if rng.Intn(6) == 0 {
					in[zero] = 1 // refused: an asset lock with DERO attached
				}
			}
			id := c.U(scid, "n")
			r := call("lock", "Lock", rpc.Arguments{harness.S("hash", hex.EncodeToString(sum[:])), harness.S("taker", taker),
				harness.U("expiry", c.Height+swapTestWmin+uint64(rng.Intn(30))), harness.S("asset", assetArg)}, in, who)
			if r.OK() {
				secrets[id] = hex.EncodeToString(sec[:])
			}
		case 2, 3:
			id := idOf()
			secret := secrets[id]
			if rng.Intn(4) == 0 {
				secret = strings.Repeat("11", 32)
			}
			var in map[crypto.Hash]uint64
			if rng.Intn(8) == 0 {
				in = consDero(1000)
			}
			call("claim", "Claim", rpc.Arguments{harness.U("id", id), harness.S("secret", secret)}, in, who)
		case 4:
			id, by := idOf(), who
			if m, ok := c.Var(scid, "m_"+itoa(id)).(string); ok && rng.Intn(2) == 0 {
				for _, u := range users {
					if raw(u) == m {
						by = u
					}
				}
			}
			call("refund", "Refund", rpc.Arguments{harness.U("id", id)}, nil, by)
		case 5:
			call("claim of no lock", "Claim", rpc.Arguments{harness.U("id", c.U(scid, "n")+5), harness.S("secret", strings.Repeat("22", 32))}, nil, who)
		default:
			for i := rng.Intn(10); i >= 0; i-- {
				c.NextBlock()
			}
		}
	}
	y.require(t, "swap.bas")
}

// ---- board.bas: holds nothing, so its balance stays 0 ------------------------------------------------------------

func TestConservationBoard(t *testing.T) {
	c := harness.New()
	users := []*rpc.Address{c.NewAccount(), c.NewAccount(), c.NewAccount()}
	scid, res := c.Install(readContract(t, "board.bas"), nil, nil, users[0], harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	var y consTally
	rng := rand.New(rand.NewSource(17))
	step := 0
	call := func(what, entry string, args rpc.Arguments, in map[crypto.Hash]uint64, who *rpc.Address) {
		t.Helper()
		r := c.Call(scid, entry, args, in, who)
		y.note(r)
		if b := c.SCBalance(scid, zero); b != 0 {
			t.Fatalf("step %d %s: board.bas holds %d DERO", step, what, b)
		}
	}
	idOf := func() uint64 { return uint64(rng.Int63n(int64(c.U(scid, "n")) + 1)) }
	for step = 0; step < consSteps(); step++ {
		who := consPick(rng, users)
		switch rng.Intn(6) {
		case 0, 1:
			hash := strings.Repeat("ab", 32)
			if rng.Intn(8) == 0 {
				hash = strings.Repeat("AB", 32) // refused: not lower-case hex
			}
			var in map[crypto.Hash]uint64
			if rng.Intn(4) == 0 {
				in = consDero(1000) // refused: the board takes no value
			}
			call("post", "Post", rpc.Arguments{harness.S("chain", "zcash-t"), harness.S("giveAsset", ""), harness.U("giveAmount", consUpTo(rng, 1_000_000)),
				harness.U("wantAmount", 12345678), harness.S("wantAddr", "t1MakerTransparentAddress"), harness.S("hash", hash),
				harness.U("expiry", c.Height+1+uint64(rng.Intn(300)))}, in, who)
		case 2:
			var in map[crypto.Hash]uint64
			if rng.Intn(6) == 0 {
				in = consDero(1000)
			}
			call("accept", "Accept", rpc.Arguments{harness.U("id", idOf()), harness.S("refundPkh", strings.Repeat("cd", 20)), harness.U("cltv", 1000+uint64(rng.Intn(1000)))}, in, who)
		case 3:
			call("cancel", "Cancel", rpc.Arguments{harness.U("id", idOf())}, nil, who)
		default:
			for i := rng.Intn(50); i >= 0; i-- {
				c.NextBlock()
			}
		}
	}
	y.require(t, "board.bas")
}
