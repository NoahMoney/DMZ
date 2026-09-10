package tests

import (
	"testing"
)

// REFUTER (fees), missed by the finder as a standalone: a DERO-quoted NFA sale pays protoFee straight to the
// treasury ADDRESS (contracts/nfa.bas, processDEROFinalPayment line 118) with no fd/fa/fc accrual and no
// ClaimFees. tools/treasurybot walks only pool_ and launch_ (main.go:124) and computes the pass deposit from
// what it claimed (jn.owed()), so an NFA's protocol fee — even in DERO — never contributes to the Founders Pass.
// app/nfts.js:95 tells a buyer the share of "the protocol fees claimed from every market is deposited here".
// docs/BACKLOG.md BL-64 scopes this gap to a WRAPPED quote only.
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
	fee := r.Sent(zero, e.treasury)
	if fee != price*500/10000 {
		t.Fatalf("treasury received %d, want %d", fee, price*500/10000)
	}
	for _, k := range []string{"fd", "fa", "fc"} { // nothing is left behind for a claimer to find
		if e.c.Exists(e.nfa, k) {
			t.Fatalf("nfa.bas has a %s key after a sale: it would be claimable", k)
		}
	}
	t.Logf("DERO-quoted NFA sale: %d atomic units of protocol fee went straight to the treasury WALLET; nfa.bas keeps no fd/fa/fc, has no ClaimFees, and tools/treasurybot walks only pool_/launch_ — so this fee can never reach a Founders Pass holder and never appears in the pass's `dep`", fee)
}
