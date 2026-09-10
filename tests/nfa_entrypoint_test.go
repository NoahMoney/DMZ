package tests

// The DVM has no private functions: blockchain/transaction_execute.go 359-378 takes "entrypoint" verbatim from
// the transaction's SCDATA and dvm/sc.go 172 looks it up in sc_parsed.Functions with no filter, so every
// Function a contract declares is callable by anyone and its parameters come from SCDATA by name. These are the
// helper functions of contracts/nfa.bas and contracts/founders.bas, called directly.

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
	e.oneUnit(t, "after helper calls")
}

func TestFoundersHelpersAreCallableEntrypoints(t *testing.T) {
	c := harness.New()
	creator, treasury := c.NewAccount(), c.NewAccount()
	alice, bob := c.NewAccount(), c.NewAccount()
	scid := installFounders(t, c, creator, treasury, 1000, 100, 0, 10, 5000, 250)
	for _, a := range []*rpc.Address{alice, bob} {
		if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 5)}, map[crypto.Hash]uint64{zero: 5000}, a); !r.OK() {
			t.Fatal(r.Err)
		}
		if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 5}, a); !r.OK() {
			t.Fatal(r.Err)
		}
	}
	if r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{zero: 1_000_000}, treasury); !r.OK() {
		t.Fatal(r.Err)
	}
	rpoolBefore, deroBefore := c.U(scid, "rpool"), c.SCBalance(scid, zero)

	// settle(s String, a String): pay holder `a`'s pending to address `s`.
	r := c.Call(scid, "settle", rpc.Arguments{harness.S("s", string(bob.Compressed())), harness.S("a", alice.String())}, nil, bob)
	t.Logf("settle(bob, alice's key) by bob: ok=%v err=%v bob got %d", r.OK(), r.Err, r.Sent(zero, bob))
	if r.OK() && r.Sent(zero, bob) > 0 {
		t.Errorf("REWARD REDIRECTED: bob called settle() directly and was paid %d of alice's pending rewards", r.Sent(zero, bob))
	}

	// distribute(x Uint64): credit x to the accumulator without depositing it.
	r2 := c.Call(scid, "distribute", rpc.Arguments{harness.U("x", 1<<40)}, nil, bob)
	t.Logf("distribute(2^40) by a stranger: ok=%v err=%v rpool %d -> %d, contract DERO %d",
		r2.OK(), r2.Err, rpoolBefore, c.U(scid, "rpool"), c.SCBalance(scid, zero))
	if r2.OK() && c.U(scid, "rpool")+c.U(scid, "und") > c.SCBalance(scid, zero) {
		t.Errorf("PHANTOM REWARDS: rpool+und = %d but the contract holds only %d DERO — the invariant in founders.bas 9 is broken",
			c.U(scid, "rpool")+c.U(scid, "und"), c.SCBalance(scid, zero))
	}

	// remove(a String): drop a holder from the registry without returning their units.
	r3 := c.Call(scid, "remove", rpc.Arguments{harness.S("a", alice.String())}, nil, bob)
	t.Logf("remove(alice) by bob: ok=%v err=%v hn %d hst %d hu_alice? %v",
		r3.OK(), r3.Err, c.U(scid, "hn"), c.U(scid, "hst"), c.Exists(scid, "hu_"+alice.String()))
	if r3.OK() && !c.Exists(scid, "hu_"+alice.String()) {
		t.Errorf("HOLDER ERASED: bob called remove() directly and deleted alice's registration; hst still %d, so her 5 units are unrecoverable", c.U(scid, "hst"))
	}
	_ = deroBefore
}
