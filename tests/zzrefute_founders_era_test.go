package tests

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// REFUTER (fees): the "PayBatch cannot be DoSed" claim measures PayBatch(0,15) at 6,814,600 of the 10,000,000
// compute limit with zero era gaps. settle() walks EVERY era a holder missed (founders.bas:272-276), so a settle
// is not fixed-cost: it grows with the accumulator rollovers since that holder last settled. Both callers pin the
// slice at 15 (app/nfts.js:99, tools/treasurybot/main.go:177) and neither can lower it.
func TestFoundersPayBatchCostGrowsWithEraGaps(t *testing.T) {
	// build a pass with 15 registered holders, force exactly `gaps` era rollovers with nobody settling, and
	// report what PayBatch(0,15) then costs.
	run := func(gaps int) (uint64, bool, uint64) {
		c := harness.New()
		creator, treasury := c.NewAccount(), c.NewAccount()
		scid := installFounders(t, c, creator, treasury, 0, 100, 0, 0, 5000, 250)
		for i := 0; i < 15; i++ {
			a := c.NewAccount()
			if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 1)}, nil, a); !r.OK() {
				t.Fatalf("mint: %v", r.Err)
			}
			if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 1}, a); !r.OK() {
				t.Fatalf("register: %v", r.Err)
			}
		}
		const d = 15 * 4294967296 // hst * 2^32: one clamped increment, i.e. very nearly a whole era
		for c.U(scid, "era") < uint64(gaps) {
			if r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{zero: d}, treasury); !r.OK() {
				t.Fatalf("deposit: %v", r.Err)
			}
		}
		r := c.Call(scid, "PayBatch", rpc.Arguments{harness.U("from", 0), harness.U("n", 15)}, nil, nil)
		return c.U(scid, "era"), r.OK(), r.GasCompute
	}
	for _, gaps := range []int{0, 1, 2, 3, 4} {
		era, ok, gc := run(gaps)
		t.Logf("era %d (each holder %d eras behind): PayBatch(0,15) ok=%v compute %d of 10000000", era, era, ok, gc)
		if !ok {
			t.Logf("DEFECT: at %d era rollovers the fixed 15-holder slice no longer fits in a transaction", era)
			return
		}
	}
	t.Log("PayBatch(0,15) still fits at 4 era rollovers")
}
