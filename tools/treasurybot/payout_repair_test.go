package main

import (
	"encoding/hex"
	"strconv"
	"testing"

	rpc "veil/tools/simrpc"
)

// FEE-03. The payout loop used to run whenever the pass held anything at all:
//
//	if pass.U("rpool") == 0 && pass.U("und") == 0 { return }
//
// rpool never returns to zero. distribute() credits it ceil(inc·hst/2^32) while settle() pays each holder
// floor(hu·(acc-hd)/2^32), and the sum of the floors is short of the ceiling whenever any holder's share has a
// fractional part; no entrypoint can release the remainder, because every one of them pays through settle().
// So the gate was true for ever after the first distribution and the bot sent ceil(hn/15) PayBatch transactions
// every -interval that moved nothing, at 3000 atomic units of fee each.
//
// These tests fix the decision, not the fee: payoutBatches must name only the slices that pay somebody.

// oldGate is the condition this repair replaced, kept here so the tests state what changed rather than only
// what is now true.
func oldGate(pass rpc.SCState) bool { return pass.U0("rpool") != 0 || pass.U0("und") != 0 }

// pass builds an SCState the way DERO.GetSC returns one: numbers as decimal strings, strings as hex.
func pass(kv map[string]uint64, holders ...holder) rpc.SCState {
	st := rpc.SCState{StringKeys: map[string]interface{}{}}
	for k, v := range kv {
		st.StringKeys[k] = strconv.FormatUint(v, 10)
	}
	st.StringKeys["hn"] = strconv.FormatUint(uint64(len(holders)), 10)
	for i, h := range holders {
		st.StringKeys["h_"+strconv.Itoa(i)] = hex.EncodeToString([]byte(h.addr))
		st.StringKeys["hi_"+h.addr] = strconv.Itoa(i + 1)
		st.StringKeys["hu_"+h.addr] = strconv.FormatUint(h.hu, 10)
		st.StringKeys["hd_"+h.addr] = strconv.FormatUint(h.hd, 10)
		st.StringKeys["he_"+h.addr] = strconv.FormatUint(h.he, 10)
	}
	return st
}

type holder struct {
	addr       string
	hu, hd, he uint64
}

func holders(n int, hu, hd, he uint64) []holder {
	out := make([]holder, n)
	for i := range out {
		out[i] = holder{addr: "deto1holder" + strconv.Itoa(i), hu: hu, hd: hd, he: he}
	}
	return out
}

const prec = uint64(1) << 32

// TestPayoutBatchesSkipsSweepsThatPayNobody is the finding: dust in rpool, every holder settled to the current
// accumulator, and five sweeps in a row must send nothing.
func TestPayoutBatchesSkipsSweepsThatPayNobody(t *testing.T) {
	acc := 7 * prec
	st := pass(map[string]uint64{"acc": acc, "rpool": 2, "und": 0, "hst": 3, "minhst": 1, "era": 0}, holders(3, 1, acc, 0)...)
	if !oldGate(st) {
		t.Fatal("the state this test is about is one the old gate let through: rpool is 2, not 0")
	}
	for sweep := 0; sweep < 5; sweep++ {
		if b := payoutBatches(st); len(b) != 0 {
			t.Fatalf("sweep %d sent PayBatch for slices %v with nothing to pay (rpool %d is dust below one unit per holder)", sweep, b, st.U0("rpool"))
		}
	}
	t.Logf("rpool %d, 3 holders settled at acc %d: 5 sweeps, 0 transactions (the old gate would have sent 5)", st.U0("rpool"), acc)
}

// TestPayoutBatchesSendsWhatIsOwed: the repair must not stop the bot paying. One unit of acc per pass is one
// atomic unit each, so the slice pays 3.
func TestPayoutBatchesSendsWhatIsOwed(t *testing.T) {
	st := pass(map[string]uint64{"acc": prec, "rpool": 3, "und": 0, "hst": 3, "minhst": 1, "era": 0}, holders(3, 1, 0, 0)...)
	if got := payoutSlice(st, 0, 15); got != 3 {
		t.Fatalf("slice pays %d, want 3", got)
	}
	if b := payoutBatches(st); len(b) != 1 || b[0] != 0 {
		t.Fatalf("batches %v, want [0]", b)
	}
}

// TestPayoutBatchesSkipsOnlyTheEmptySlices: 31 holders, one of them owed. The bot used to send three
// transactions; it must now send the one that pays.
func TestPayoutBatchesSkipsOnlyTheEmptySlices(t *testing.T) {
	acc := 5 * prec
	hs := holders(31, 1, acc, 0)
	hs[20].hd = acc - prec // holder 20 is owed exactly one atomic unit
	st := pass(map[string]uint64{"acc": acc, "rpool": 9, "und": 0, "hst": 31, "minhst": 1, "era": 0}, hs...)
	b := payoutBatches(st)
	if len(b) != 1 || b[0] != 15 {
		t.Fatalf("batches %v, want [15] — the slice holding index 20", b)
	}
	old := 0
	for from := uint64(0); from < st.U0("hn"); from += 15 {
		old++
	}
	t.Logf("31 holders, one owed: 1 transaction where the old gate sent %d", old)
}

// TestPayoutSliceWalksClosedEras: settle() sums one term per era the holder has not been settled through
// (founders.bas:30-34). A gate that read only acc - hd would see nothing here and skip a real payment.
func TestPayoutSliceWalksClosedEras(t *testing.T) {
	st := pass(map[string]uint64{"acc": prec, "rpool": 1000, "und": 0, "hst": 1, "minhst": 1, "era": 1, "ea_0": 3 * prec},
		holder{addr: "deto1a", hu: 1, hd: prec, he: 0})
	if got := payoutSlice(st, 0, 15); got != 3 {
		t.Fatalf("slice pays %d, want 3 = (ea_0-hd)/2^32 + acc/2^32", got)
	}
	single := (1 * (st.U0("acc") - prec)) / prec // what one term alone would have said
	if single != 0 {
		t.Fatalf("the single-term reading should be 0 here, got %d", single)
	}
}

// TestPayoutSliceReleasesUndFirst: PayBatch runs distribute(0) before it settles anybody, so DERO waiting in und
// becomes payable inside the same call. The gate models that, or the bot would stop pushing a pass whose
// holders are owed only what is still waiting.
func TestPayoutSliceReleasesUndFirst(t *testing.T) {
	st := pass(map[string]uint64{"acc": 0, "rpool": 0, "und": 1000000, "hst": 2, "minhst": 1, "era": 0}, holders(2, 1, 0, 0)...)
	if got := payoutSlice(st, 0, 15); got != 1000000 {
		t.Fatalf("slice pays %d, want the 1000000 distribute(0) releases", got)
	}
	// but not while the registry is under the floor: distribute() leaves it in und
	st = pass(map[string]uint64{"acc": 0, "rpool": 0, "und": 1000000, "hst": 2, "minhst": 11, "era": 0}, holders(2, 1, 0, 0)...)
	if got := payoutSlice(st, 0, 15); got != 0 {
		t.Fatalf("slice pays %d under minhst, want 0", got)
	}
}

// NFA-3. docs/NFA.md §6.6 says an NFA's DERO protocol fee is swept to the pass. It is not: this bot walks the
// factory's pool_ and launch_ lists only, and nfa.bas keeps no claimable balance to walk to. The doc is not
// this fixer's file; the assertion that would have caught it belongs here either way.
func TestSweeperWalksOnlyPoolAndLaunch(t *testing.T) {
	want := map[string]string{"pool_": "pc", "launch_": "lc"}
	if len(registryPrefixes) != len(want) {
		t.Fatalf("the sweeper walks %v; every list added here needs a fee balance a ClaimFees can move", registryPrefixes)
	}
	for _, p := range registryPrefixes {
		if want[p.prefix] != p.count {
			t.Fatalf("unexpected registry list %q/%q", p.prefix, p.count)
		}
	}
}
