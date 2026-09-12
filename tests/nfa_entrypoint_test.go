package tests

// A transaction can call only a Function whose name starts with an upper-case ASCII letter: dvm/sc.go 262 hands the
// entrypoint to RunSmartContract (dvm/dvm.go 342-370), which refuses any other name before anything runs. So the
// lower-case helpers of contracts/nfa.bas and contracts/founders.bas are private to their contract. These tests call
// each helper directly, as a stranger's transaction would (and nfa's InitializePrivate, an entrypoint that refuses a
// second run), and fail if any such call moves ownership, pays out, mints, forges a credit or erases a record.
// This comment once said the DVM has no private functions, which is false (GAP-038); the test names date from then.

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

func TestNFAHelpersAreCallableEntrypoints(t *testing.T) {
	e := newNFAEnv(t, true, 10, 0, 250)
	c := e.c
	owner0 := c.S(e.nfa, "owner")

	// 1. transferOwnership(newOwner String) — the internal that BuyItNow and processHighestBidder use.
	r := c.Call(e.nfa, "transferOwnership", rpc.Arguments{harness.S("newOwner", raw(e.alice))}, nil, e.alice)
	t.Logf("transferOwnership by a stranger: ok=%v err=%v owner %s -> %s", r.OK(), r.Err, e.who(owner0), e.who(c.S(e.nfa, "owner")))
	if r.OK() && c.S(e.nfa, "owner") == raw(e.alice) {
		t.Errorf("OWNERSHIP TAKEN: any wallet called transferOwnership and became owner of the NFA")
	}

	// 2. processDEROFinalPayment(saleAmt Uint64) — the settlement, against whatever the contract holds.
	//    Park some quote in the contract first, the way a stranded bid does.
	e2 := newNFAEnv(t, true, 10, 0, 250)
	c2 := e2.c
	if rr := e2.start(e2.creator, "auction", 1, 1_000_000, "", 0, true); !rr.OK() {
		t.Fatal(rr.Err)
	}
	if rr := e2.bid(e2.alice, 5_000_000); !rr.OK() {
		t.Fatal(rr.Err)
	}
	held := c2.SCBalance(e2.nfa, e2.quote)
	r2 := c2.Call(e2.nfa, "processDEROFinalPayment", rpc.Arguments{harness.U("saleAmt", held)}, nil, e2.bob)
	t.Logf("processDEROFinalPayment(%d) by a stranger: ok=%v err=%v balance %d -> %d transfers %+v",
		held, r2.OK(), r2.Err, held, c2.SCBalance(e2.nfa, e2.quote), r2.Transfers)
	if r2.OK() && c2.SCBalance(e2.nfa, e2.quote) < held {
		t.Errorf("CONTRACT DRAINED: a stranger's direct call to processDEROFinalPayment paid out %d of the quote", held-c2.SCBalance(e2.nfa, e2.quote))
	}

	// 3. init(quote, protoFee, treasury) and InitializePrivate after install — the second-unit question.
	r3 := c.Call(e.nfa, "init", rpc.Arguments{harness.S("quote", ""), harness.U("protoFee", 0), harness.S("treasury", e.alice.String())}, nil, e.alice)
	t.Logf("init() after install: ok=%v err=%v", r3.OK(), r3.Err)
	if r3.OK() && r3.Sent(e.nfa, e.alice) > 0 {
		t.Errorf("SECOND UNIT MINTED by a direct init() call")
	}
	r4 := c.Call(e.nfa, "InitializePrivate", nfaArgs("", 0, 0, 0, 0, e.alice.String()), nil, e.alice)
	t.Logf("InitializePrivate() after install: ok=%v err=%v", r4.OK(), r4.Err)
	if r4.OK() && r4.Sent(e.nfa, e.alice) > 0 {
		t.Errorf("SECOND UNIT MINTED by a direct InitializePrivate call")
	}

	// 4. outbidReturns() and resetVars(forceReset) — listing state a stranger could clear
	r5 := c.Call(e.nfa, "resetVars", rpc.Arguments{harness.U("forceReset", 1)}, nil, e.alice)
	t.Logf("resetVars(1) by a stranger: ok=%v err=%v", r5.OK(), r5.Err)
	r6 := c.Call(e.nfa, "outbidReturns", nil, nil, e.alice)
	t.Logf("outbidReturns() by a stranger: ok=%v err=%v", r6.OK(), r6.Err)

	// 5. owe(payee, amt) — the credit a settlement makes to ow_<payee>. Called directly it would let anyone owe
	//    themselves the contract's whole balance and collect it with PayOwed.
	r7 := c.Call(e.nfa, "owe", rpc.Arguments{harness.S("payee", raw(e.alice)), harness.U("amt", 1<<40)}, nil, e.alice)
	t.Logf("owe(alice, 2^40) by alice: ok=%v err=%v", r7.OK(), r7.Err)
	if r7.OK() || c.Exists(e.nfa, "ow_"+e.alice.String()) || c.U(e.nfa, "owed") != 0 {
		t.Errorf("CREDIT FORGED: a direct owe() call wrote ow_ for alice")
	}
	e.oneUnit(t, "after helper calls")
}

// founders.bas v2's helpers: setp (stores a payout address), credit (credits a deposit), at (reads a record), newa (a
// new asset index), open (a new position) and bps. A stranger calls each directly with arguments that would forge
// value if it ran; the DVM refuses every lower-case entrypoint before it runs, so nothing may change.
func TestFoundersHelpersAreCallableEntrypoints(t *testing.T) {
	c := harness.New()
	creator, treasury := c.NewAccount(), c.NewAccount()
	alice, bob := c.NewAccount(), c.NewAccount()
	scid := installV2(t, c, creator, treasury, 1000, 100, 0, 10)
	for _, a := range []*rpc.Address{alice, bob} {
		if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 5)}, map[crypto.Hash]uint64{zero: 5000}, a); !r.OK() {
			t.Fatal(r.Err)
		}
		if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 5}, a); !r.OK() {
			t.Fatal(r.Err)
		}
	}
	if r := c.Call(scid, "Deposit", v2Deposit(zero), map[crypto.Hash]uint64{zero: 1_000_000}, treasury); !r.OK() {
		t.Fatal(r.Err)
	}
	keys := []string{"hst", "ev", "pn", "an", "ac_0", "au_0", "ap_0", "ad_0", "aw_0", "ar_0"}
	before := map[string]uint64{}
	for _, k := range keys {
		before[k] = c.U(scid, k)
	}
	dero := c.SCBalance(scid, zero)
	for _, x := range []struct {
		fn   string
		args rpc.Arguments
	}{
		{"setp", rpc.Arguments{harness.S("k", "xm_"), harness.S("x", "not an address"), harness.U("ok", 1)}},
		{"credit", rpc.Arguments{harness.U("i", 0), harness.U("d", 1<<40), harness.U("h", 1)}},
		{"at", rpc.Arguments{harness.U("i", 0), harness.U("t", 1), harness.U("k", 0)}},
		{"newa", rpc.Arguments{harness.S("a", string(make([]byte, 32)))}},
		{"open", rpc.Arguments{harness.S("s", string(bob.Compressed())), harness.U("u", 1000), harness.U("e", 1)}},
		{"bps", rpc.Arguments{harness.U("x", 1), harness.U("f", 1)}},
	} {
		if r := c.Call(scid, x.fn, x.args, nil, bob); r.OK() {
			t.Errorf("%s: a stranger's direct call to a lower-case helper was accepted", x.fn)
		}
	}
	for _, k := range keys {
		if got := c.U(scid, k); got != before[k] {
			t.Errorf("%s moved from %d to %d after the helper calls", k, before[k], got)
		}
	}
	if c.SCBalance(scid, zero) != dero || c.Exists(scid, "xm_"+bob.String()) {
		t.Errorf("a helper call moved DERO (%d -> %d) or stored an address", dero, c.SCBalance(scid, zero))
	}
}
