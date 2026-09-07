package main

import (
	"encoding/json"
	"strconv"
	"testing"

	rpc "veil/tools/simrpc"
)

// state builds the contract state the daemon sees, the way the daemon sees it: uint64 values arrive as
// json.Number from DERO.GetSC.
func state(kv map[string]uint64) rpc.SCState {
	s := rpc.SCState{StringKeys: map[string]interface{}{}}
	for k, v := range kv {
		s.StringKeys[k] = json.Number(strconv.FormatUint(v, 10))
	}
	return s
}

// TestReserveShortfallIgnoresWithdrawalsInFlight is the regression test for the halt deadlock.
//
// A claim leaves the foreign reserve when the federation pays it and leaves `sup` when the Released quorum
// records that payment. Those are different moments by construction. A daemon that halts whenever
// `reserve < sup` therefore halts on every ordinary withdrawal — and because vault.bas Released refuses while
// halted, `sup` can never fall, the condition can never clear, and the vault freezes forever with no operator
// to lift it. Every honest deposit and every burned unit is lost.
//
// The check must be against `sup - inflight`, and inflight must come from contract state alone so that two
// signers at different tips never disagree about whether their own federation is solvent.
func TestReserveShortfallIgnoresWithdrawalsInFlight(t *testing.T) {
	// 50 000 minted, then 40 000 burned (39 900 payable after the 100 fee) and not yet released
	st := state(map[string]uint64{
		"sup": 50000, "wn": 1,
		"w_0_st": 0, "w_0_v": 39900,
	})
	if got := inflightUnits(st); got != 39900 {
		t.Fatalf("inflight %d, want 39900", got)
	}
	owed := owedUnits(st.U("sup"), inflightUnits(st))
	if owed != 10100 {
		t.Fatalf("owed %d, want 10100", owed)
	}
	// the federation has paid the 39 900 abroad: this is what a correct vault looks like mid-withdrawal
	if reserve := uint64(60100); reserve < owed {
		t.Fatalf("reserve %d treated as a shortfall against owed %d: this is the halt deadlock", reserve, owed)
	}
	// and a genuine shortfall is still caught: coins gone that no open withdrawal accounts for
	if reserve := uint64(10099); reserve >= owed {
		t.Fatalf("reserve %d not treated as a shortfall against owed %d", reserve, owed)
	}
}

// TestInflightCountsOnlyOpenWithdrawals: a closed withdrawal (released or vetoed) has already left `sup`, so
// counting it again would widen the blind spot and hide a real theft of exactly that size.
func TestInflightCountsOnlyOpenWithdrawals(t *testing.T) {
	st := state(map[string]uint64{
		"sup": 100000, "wn": 4,
		"w_0_st": 1, "w_0_v": 10000, // released: already out of sup
		"w_1_st": 0, "w_1_v": 5000, // open
		"w_2_st": 2, "w_2_v": 30000, // vetoed: never paid, units returned
		"w_3_st": 0, "w_3_v": 2500, // open
	})
	if got := inflightUnits(st); got != 7500 {
		t.Fatalf("inflight %d, want 7500 (only the open withdrawals)", got)
	}
	if got := owedUnits(100000, 7500); got != 92500 {
		t.Fatalf("owed %d, want 92500", got)
	}
}

// TestOwedNeverUnderflows: uint64 wraps silently, and an inflight total at or above the supply must read as
// "nothing is owed", not as a reserve requirement of eighteen quintillion that halts the vault instantly.
func TestOwedNeverUnderflows(t *testing.T) {
	for _, c := range []struct{ sup, inflight, want uint64 }{
		{100, 100, 0}, {100, 250, 0}, {0, 0, 0}, {100, 0, 100}, {100, 1, 99},
	} {
		if got := owedUnits(c.sup, c.inflight); got != c.want {
			t.Fatalf("owed(%d, %d) = %d, want %d", c.sup, c.inflight, got, c.want)
		}
	}
}

// reserveBackend is a countingBackend whose reserve actually falls when it pays, which is the only reason this
// class of bug is visible at all: a backend that reports a constant reserve can never reproduce it.
type reserveBackend struct {
	*countingBackend
	reserve uint64
}

func (b *reserveBackend) Reserve() (uint64, error) { return b.reserve, nil }
func (b *reserveBackend) Release(j uint64, dest string, amount uint64) (string, error) {
	ftx, err := b.countingBackend.Release(j, dest, amount)
	if err == nil && b.reserve >= amount {
		b.reserve -= amount // the coins leave when the federation pays, well before Released is voted
	}
	return ftx, err
}

// TestPayingDoesNotLookLikeTheft walks the sequence a real vault walks — mint, burn, pay abroad, then the
// Released vote — and asserts that at the moment the payment has landed and the vote has not, the daemon does
// not read its own correct federation as insolvent.
func TestPayingDoesNotLookLikeTheft(t *testing.T) {
	be := &reserveBackend{countingBackend: &countingBackend{paid: map[uint64]int{}, ftx: map[uint64]string{}}, reserve: 100000}
	mem := newMemory(t.TempDir()+"/state.json", "vaultA")

	// the contract state after a 40 000 burn against a 50 000 supply: sup is unchanged, the withdrawal is open
	st := state(map[string]uint64{"sup": 50000, "wn": 1, "w_0_st": 0, "w_0_v": 39900})

	voted := false
	if err := processWithdrawal(mem, be, 0, "dest-0", 39900, func(uint64, string) bool { voted = true; return true }, nil); err != nil {
		t.Fatal(err)
	}
	if !voted {
		t.Fatal("the withdrawal was paid but never voted")
	}
	// the payment has left the reserve; the contract has not yet been told, so sup is still 50 000
	if be.reserve != 60100 {
		t.Fatalf("reserve %d after paying 39900 of 100000", be.reserve)
	}
	owed := owedUnits(st.U("sup"), inflightUnits(st))
	if be.reserve < owed {
		t.Fatalf("a correct federation reads as short: reserve %d < owed %d", be.reserve, owed)
	}
}
