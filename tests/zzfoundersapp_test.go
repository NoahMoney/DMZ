package tests

import (
	"strconv"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// app/founders.js myFounders computes a holder's pending reward the way contracts/founders.bas settle() pays it:
// one term per era the holder has not been settled through, read from the accumulator that era closed at
// (ea_<e>), then the term for the current era, capped at rpool. A single term against the live accumulator is
// right only until acc first wraps 2^64 — about 42,950 DERO of deposits per registered unit — after which the
// contract files the pre-wrap value under ea_<era> and restarts at zero, and a page that subtracted an old
// hd_ snapshot from the new accumulator would under-report what the contract owes. This test rolls the
// accumulator deliberately and then requires the page's number and the contract's payment to agree exactly.
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

	// INT-01, repaired. What app/founders.js prints, per holder, from exactly the keys it reads — transcribed
	// from myFounders() as it stands: one term per era the holder has not been settled through, taken from the
	// accumulator that era closed at (ea_<n>), then the term for the current era, capped at rpool. The single
	// term this helper used to compute is right only until the first roll: distribute() restarts acc at 0 when a
	// deposit would carry it past 2^64 and files the pre-wrap value under ea_<era>, so a hd_ snapshot taken in an
	// earlier era cannot be subtracted from this era's acc. The roll above is what makes the difference visible.
	appPending := func(who *rpc.Address) uint64 {
		hu := c.U(scid, "hu_" + who.String())
		if hu == 0 {
			return 0
		}
		var hd, he uint64
		if c.Exists(scid, "hd_" + who.String()) {
			hd = c.U(scid, "hd_" + who.String())
		}
		if c.Exists(scid, "he_" + who.String()) {
			he = c.U(scid, "he_" + who.String())
		}
		var p uint64
		for e := he; e < c.U(scid, "era"); e++ {
			var ea uint64
			if key := "ea_" + strconv.FormatUint(e, 10); c.Exists(scid, key) {
				ea = c.U(scid, key)
			}
			if ea > hd {
				p += hu * (ea - hd) / 4294967296
			}
			hd = 0
		}
		if acc > hd {
			p += hu * (acc - hd) / 4294967296
		}
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
