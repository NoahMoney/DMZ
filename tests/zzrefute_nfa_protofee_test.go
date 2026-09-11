package tests

import (
	"testing"
)

// REFUTER (fees), missed by the finder as a standalone: an NFA's protocol fee never reaches the Founders Pass.
// nfa.bas keeps no fd/fa/fc accrual and has no ClaimFees. Since 2026-09-11 the fee is owed to the treasury under
// ow_<treasury> and PayOwed (anyone, any ring size) sends it to the treasury WALLET — which is still not the pass:
// tools/treasurybot walks only pool_ and launch_ (main.go:44) and computes the pass deposit from what it claimed
// (jn.owed()), so an NFA's protocol fee, even in DERO, contributes nothing to the Founders Pass until the bot walks
// nfa_ and deposits what PayOwed brings in. docs/BACKLOG.md BL-64 scopes this gap to a WRAPPED quote only.
func TestNFAProtoFeeNeverReachesTheFoundersPass(t *testing.T) {
	e := newNFAEnv(t, false, 0, 0, 500) // DERO quote, protoFee 5%
	const price = 10000000              // 100 DERO
	if r := e.start(e.creator, "sale", 100, price, e.charity.String(), 0, true); !r.OK() {
		t.Fatalf("start: %v", r.Err)
	}
	r := e.buy(e.alice, price)
	if !r.OK() {
		t.Fatalf("buyitnow: %v", r.Err)
	}
	fee := uint64(price * 500 / 10000)
	if r.Sent(zero, e.treasury) != 0 || e.owedTo(e.treasury) != fee {
		t.Fatalf("treasury received %d in the sale and is owed %d, want 0 and %d", r.Sent(zero, e.treasury), e.owedTo(e.treasury), fee)
	}
	for _, k := range []string{"fd", "fa", "fc"} { // no pool-style accrual for a claimer that walks fd/fa/fc
		if e.c.Exists(e.nfa, k) {
			t.Fatalf("nfa.bas has a %s key after a sale", k)
		}
	}
	e.payOwed(t, nil, e.treasury) // anyone can send it on, and it goes to the treasury's wallet
	t.Logf("DERO-quoted NFA sale: %d atomic units of protocol fee owed to the treasury and paid to its WALLET by PayOwed; tools/treasurybot walks only pool_/launch_, so this fee still never reaches a Founders Pass holder and never appears in the pass's `dep`", fee)
}
