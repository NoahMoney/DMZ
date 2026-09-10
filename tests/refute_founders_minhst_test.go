package tests

// REFUTER note on FOUND-1. The finder scoped the anti-sweep break to maxsupply 20 ("minhst degenerates to 1").
// The break is not specific to 20. minhst is 1 + maxsupply/50 (~2% of supply) while the install reserve is
// bounded only by maxsupply/5 (20%), and the shipped runbook (deploy/README.md:73, :233) and simdeploy's own
// defaults (tools/simdeploy/main.go:48,:50) use -pass-max 500 -pass-reserve 50. 50 >= minhst 11, so at the size
// that is actually shipped and documented the installer already clears the floor alone and takes 100% of every
// DERO deposited before anyone else registers. 20 makes it one pass instead of eleven; it does not create it.

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

func TestRefuteFoundersReserveSweepsAtTheShippedSize(t *testing.T) {
	c := harness.New()
	creator, treasury := c.NewAccount(), c.NewAccount()
	// exactly deploy/README.md:73 / tools/simdeploy defaults
	const max, reserve, price = 500, 50, 500000
	scid := installFounders(t, c, creator, treasury, price, max, 0, reserve, 5000, 250)
	if mh := c.U(scid, "minhst"); mh != 11 {
		t.Fatalf("minhst %d at maxsupply 500, want 1+500/50 = 11", mh)
	}
	if reserve < int(c.U(scid, "minhst")) {
		t.Fatalf("reserve %d below minhst — this test's premise", reserve)
	}

	const dep = 9_000_000
	if r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{zero: dep}, treasury); !r.OK() {
		t.Fatal(r.Err)
	}
	if c.U(scid, "und") != dep || c.U(scid, "rpool") != 0 {
		t.Fatalf("und %d rpool %d — the deposit should be parked", c.U(scid, "und"), c.U(scid, "rpool"))
	}
	// the installer registers the free reserve it was handed at install — no purchase, no other holder
	if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: reserve}, creator); !r.OK() {
		t.Fatal(r.Err)
	}
	if c.U(scid, "hst") != reserve {
		t.Fatalf("hst %d", c.U(scid, "hst"))
	}
	r := c.Call(scid, "Claim", nil, nil, creator)
	if !r.OK() {
		t.Fatal(r.Err)
	}
	got := r.Sent(zero, creator)
	if got != dep {
		t.Fatalf("the installer's free reserve claimed %d of the %d parked, want all of it", got, dep)
	}
	// and it can walk straight back out with the units
	if r := c.Call(scid, "Withdraw", rpc.Arguments{harness.U("u", reserve)}, nil, creator); !r.OK() {
		t.Fatal(r.Err)
	}
	t.Logf("SHIPPED SIZE: maxsupply 500 / reserve 50 / minhst 11 — the installer's free 10%% reserve cleared the floor alone and took 100%% (%d) of everything deposited before any purchaser registered, then withdrew the units", got)
	checkFounders(t, c, scid)
}

// MISSED (refuter): the app never reads minhst. app/founders.js:13's NUM list has no "minhst", so app/nfts.js:96
// labels `und` "Waiting for the first registration" — at the shipped maxsupply 500 it waits for ELEVEN registered
// passes — and app/nfts.js:93 offers "Release waiting DERO" on hst > 0, which below minhst is a transaction that
// costs gas, succeeds, and moves nothing, with no reason shown. This test pins the contract half of that.
func TestRefuteDistributeSucceedsAndMovesNothingBelowMinhst(t *testing.T) {
	c := harness.New()
	creator, treasury := c.NewAccount(), c.NewAccount()
	alice := c.NewAccount()
	const max, price = 500, 1000
	scid := installFounders(t, c, creator, treasury, price, max, 0, 0, 5000, 250)
	if mh := c.U(scid, "minhst"); mh != 11 {
		t.Fatalf("minhst %d", mh)
	}
	const dep = 500_000
	if r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{zero: dep}, treasury); !r.OK() {
		t.Fatal(r.Err)
	}
	// one holder registers: hst is 1, so the app's `release = waiting > 0 && hst > 0` is TRUE and it draws the button
	if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 1)}, map[crypto.Hash]uint64{zero: price}, alice); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 1}, alice); !r.OK() {
		t.Fatal(r.Err)
	}
	if c.U(scid, "hst") != 1 || c.U(scid, "und") != dep {
		t.Fatalf("hst %d und %d", c.U(scid, "hst"), c.U(scid, "und"))
	}
	r := c.Call(scid, "Distribute", nil, nil, alice)
	if !r.OK() {
		t.Fatalf("Distribute refused: %v — the finding is that it does NOT refuse", r.Err)
	}
	if c.U(scid, "und") != dep || c.U(scid, "rpool") != 0 || len(r.Transfers) != 0 {
		t.Fatalf("something moved: und %d rpool %d transfers %v", c.U(scid, "und"), c.U(scid, "rpool"), r.Transfers)
	}
	if rr := c.Call(scid, "Claim", nil, nil, alice); !rr.OK() || rr.Sent(zero, alice) != 0 {
		t.Fatalf("Claim after Distribute paid %d", rr.Sent(zero, alice))
	}
	t.Logf("Distribute below minhst (hst 1 < 11) RETURNs 0, moves nothing, and leaves und at %d — the app draws \"Release waiting DERO\" for this state and reads und as \"Waiting for the first registration\"", c.U(scid, "und"))
	checkFounders(t, c, scid)
}
