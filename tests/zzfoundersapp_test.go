package tests

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// app/founders.js myFounders computes a holder's pending reward as
//
//	pend = hu > 0 && acc > hd ? hu * (acc - hd) / 2^32 : 0
//
// It reads hu_ and hd_ only. contracts/founders.bas settle() (lines 263-288) also reads he_<addr> and walks
// every closed era through ea_<e>. Once acc has wrapped 2^64 even once — about 42,950 DERO of deposits per
// registered unit — the two disagree, and the page under-reports what the contract owes.
//
// This test measures both numbers against the real DVM.
func TestFoundersAppPendingAfterEraRoll(t *testing.T) {
	c := harness.New()
	creator, treasury := c.NewAccount(), c.NewAccount()
	scid := installFounders(t, c, creator, treasury, 0, 49, 0, 0, 5000, 250)
	a, b := c.NewAccount(), c.NewAccount()
	for _, who := range []*rpc.Address{a, b} {
		if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 1)}, nil, who); !r.OK() {
			t.Fatalf("mint: %v", r.Err)
		}
		if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 1}, who); !r.OK() {
			t.Fatalf("register: %v", r.Err)
		}
	}
	const d = 6_000_000_000 // hst = 2, so each deposit adds d*2^31 to acc: the second wraps 2^64
	deposit := func() {
		t.Helper()
		if r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{zero: d}, nil); !r.OK() {
			t.Fatalf("deposit: %v", r.Err)
		}
	}
	deposit()
	// a claims once, so its hd_ is the accumulator at that moment; b never claims, so its hd_ stays 0
	if r := c.Call(scid, "Claim", nil, nil, a); !r.OK() {
		t.Fatalf("claim: %v", r.Err)
	}
	deposit() // this one wraps: era 0 closes into ea_0

	if c.U(scid, "era") != 1 {
		t.Fatalf("expected the accumulator to have rolled once, era=%d", c.U(scid, "era"))
	}
	acc, rpool := c.U(scid, "acc"), c.U(scid, "rpool")
	t.Logf("after the roll: era=%d acc=%d ea_0=%d rpool=%d hst=%d", c.U(scid, "era"), acc, c.U(scid, "ea_0"), rpool, c.U(scid, "hst"))

	// what app/founders.js would print, per holder, from exactly the keys it reads
	appPending := func(who *rpc.Address) uint64 {
		hu := c.U(scid, "hu_"+who.String())
		var hd uint64
		if c.Exists(scid, "hd_"+who.String()) {
			hd = c.U(scid, "hd_"+who.String())
		}
		if hu == 0 || acc <= hd {
			return 0
		}
		p := hu * (acc - hd) / 4294967296
		if p > rpool {
			p = rpool
		}
		return p
	}

	bad := 0
	for name, who := range map[string]*rpc.Address{"a (claimed once, hd = the pre-roll accumulator)": a, "b (never claimed, hd = 0)": b} {
		shown := appPending(who)
		r := c.Call(scid, "Claim", nil, nil, who)
		if !r.OK() {
			t.Fatalf("claim: %v", r.Err)
		}
		paid := r.Sent(zero, who)
		t.Logf("%s: app/founders.js would show %d, the contract paid %d", name, shown, paid)
		if shown != paid {
			bad++
			t.Errorf("%s: the page under-reports the reward — shows %d, the contract owes %d (%d atomic units hidden)", name, shown, paid, paid-shown)
		}
	}
	if bad == 0 {
		t.Log("app formula agreed with the contract for every holder")
	}
}
