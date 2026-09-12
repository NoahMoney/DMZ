package zecswap

import (
	"regexp"
	"strings"
	"testing"
)

// The companion under reorganisations, on the fake node. TestRegtestReorg makes the same moves on a real zebrad
// (invalidateblock, a competing branch, reconsiderblock) and records them in research/regtest/zecswap-reorg.log.

// secondSpend is the taker spending, again, the coin her funding spent: once it is mined the funding can never be.
func (w *world) secondSpend() string {
	key := testKey(takerKeyN)
	h := HTLC{ClaimPkh: key.PubKeyHash(), RefundPkh: key.PubKeyHash(), CLTV: w.zc.Tip() + 100}
	h.Hash[0] = 0x5a
	tx, err := BuildFunding(FundingRequest{Key: key, HTLC: h, Amount: 100_000, Coins: coinsOf(w.t, w.zc.client(), key), BranchID: w.zc.branch, ExpiryHeight: w.zc.Tip() + 30})
	if err != nil {
		w.t.Fatal(err)
	}
	id, code, msg := w.zc.Submit(tx.Serialize())
	if code != 0 {
		w.t.Fatalf("the second spend: %s", msg)
	}
	return id
}

// untilClaimSeenMined drives the world until the maker has read his own claim in a block.
func (w *world) untilClaimSeenMined() *State {
	for i := 0; i < 300; i++ {
		if mk := w.state(RoleMaker); mk.Phase == "claimed" && mk.ClaimBlock != "" {
			return mk
		}
		w.round()
	}
	w.t.Fatalf("the maker never saw his claim mined: maker %s, taker %s\n%s", w.state(RoleMaker).Phase, w.state(RoleTaker).Phase, w.dump())
	return nil
}

// (a) A funding the maker read below C_zec, which the chain then dropped, is said with its block and forgotten; the
// secret stays with him.
func TestWatcherReportsAFundingTheChainDropped(t *testing.T) {
	w := newWorld(t, 21)
	st := w.makerLocked()
	fund := fundFor(t, w.zc, st)
	if _, code, msg := w.zc.Submit(fund.Serialize()); code != 0 {
		t.Fatal(msg)
	}
	w.zc.Mine(1)
	w.step(RoleMaker) // depth 1, below C_zec
	if s := w.state(RoleMaker).Sighted; s == nil || s.TxID != DisplayHex(fund.TxID()) {
		t.Fatalf("the maker kept no sight of the funding at depth 1: %+v", s)
	}
	w.zc.Reorg(1, false)
	w.zc.Mine(2)
	w.step(RoleMaker)
	st = w.state(RoleMaker)
	if st.Phase != "locked" || st.ClaimTxID != "" || st.Revealed || st.Sighted != nil {
		t.Fatalf("maker %s, claim %q, revealed %v, sighted %+v", st.Phase, st.ClaimTxID, st.Revealed, st.Sighted)
	}
	if !strings.Contains(w.dump(), "REORGANISATION: the funding "+DisplayHex(fund.TxID())) {
		t.Fatalf("the reorganisation was not reported:\n%s", w.dump())
	}
}

// (b2) The claim and the funding under it are reorganised off the chain before the taker read the claim, and nobody
// spends her coin again: the funding's bytes are public, so the maker sends them again, then — once the funding is
// C_zec deep again — the same claim, and the swap completes.
func TestClaimOffTheChainIsSentAgainWithItsFunding(t *testing.T) {
	w := newWorld(t, 22)
	hold := true
	w.offline = func(w *world, role string) bool {
		return hold && role == RoleTaker && w.state(RoleTaker).Phase == "funded"
	}
	mk := w.untilClaimSeenMined()
	depth := int(w.zc.Tip() - mk.Funding.Height + 1)
	w.zc.Reorg(depth, false)
	w.zc.Mine(2)
	if w.zc.Unspent(mk.Funding.TxID, FundingOutput) {
		t.Fatal("the reorganisation left the funding in place")
	}
	hold = false
	if !w.drive(400) {
		t.Fatalf("not finished: maker %s, taker %s\n%s", w.state(RoleMaker).Phase, w.state(RoleTaker).Phase, w.dump())
	}
	w.check("claim_back", []string{"swapped"}, []string{"swapped"})
	logs := w.dump()
	for _, want := range []string{"REORGANISATION: the claim " + mk.ClaimTxID, "REORGANISATION: the funding " + mk.Funding.TxID, "is sent again from its own bytes", "the claim " + mk.ClaimTxID + " is sent again"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("no %q in the log:\n%s", want, logs)
		}
	}
}

// (b, Z5) After the taker's DERO Claim, a reorganisation deeper than C_zec and a second spend of her coin take the
// funding away for good: the maker says so and ends lost, and status shows the claim the reorganisation removed.
func TestDeepReorgAfterTheClaimEndsLost(t *testing.T) {
	w := newWorld(t, 23)
	hold := true
	w.offline = func(w *world, role string) bool {
		if !hold || role != RoleMaker {
			return false
		}
		mk := w.state(RoleMaker)
		return mk.Phase == "claimed" && mk.ClaimBlock != ""
	}
	for i := 0; i < 400 && !w.done(RoleTaker); i++ {
		w.round()
	}
	mk, tk := w.state(RoleMaker), w.state(RoleTaker)
	if tk.Phase != "swapped" || mk.Phase != "claimed" {
		t.Fatalf("maker %s, taker %s\n%s", mk.Phase, tk.Phase, w.dump())
	}
	depth := int(w.zc.Tip() - mk.Funding.Height + 1)
	if uint32(depth) <= w.p.ConfZec {
		t.Fatalf("the reorganisation would be %d deep, not deeper than C_zec %d", depth, w.p.ConfZec)
	}
	w.zc.Reorg(depth, false)
	w.secondSpend()
	w.zc.Mine(depth + 1)
	hold = false
	for i := 0; i < 20 && !w.done(RoleMaker); i++ {
		w.round()
	}
	mk = w.state(RoleMaker)
	if mk.Phase != "lost" || !strings.Contains(mk.Outcome, "Z5") || !strings.Contains(w.dump(), "REORGANISATION: the funding") {
		t.Fatalf("maker %s: %s\n%s", mk.Phase, mk.Outcome, w.dump())
	}
	lines := strings.Join(Status(w.ctx, mk, []ZcashSource{{Name: "z", Node: w.zc.client()}}, []DeroSource{{Name: "d", Node: newMemDero(w.dero, makerAddr)}}), "\n")
	if !strings.Contains(lines, "DIVERGENCE") || !strings.Contains(lines, "a reorganisation") {
		t.Fatalf("status does not show the claim the reorganisation removed:\n%s", lines)
	}
}

// The same reorganisation while the escrow is still open (the taker never came back): the maker refunds the DERO at
// T_dero — not before — and the outcome says the secret was revealed.
func TestClaimOffTheChainRefundsTheDeroAtTDero(t *testing.T) {
	w := newWorld(t, 24)
	w.offline = func(w *world, role string) bool {
		return role == RoleTaker && w.state(RoleTaker).Phase == "funded"
	}
	mk := w.untilClaimSeenMined()
	depth := int(w.zc.Tip() - mk.Funding.Height + 1)
	w.zc.Reorg(depth, false)
	w.secondSpend()
	w.zc.Mine(2)
	for i := 0; i < 400 && !w.done(RoleMaker); i++ {
		if w.state(RoleMaker).Phase == "dero_refunding" && w.dero.Height() < mk.TDero {
			t.Fatalf("refunding the DERO at height %d, before T_dero %d", w.dero.Height(), mk.TDero)
		}
		w.round()
	}
	mk = w.state(RoleMaker)
	if mk.Phase != "refunded" || !strings.Contains(mk.Outcome, "revealed in claim "+mk.ClaimTxID) {
		t.Fatalf("maker %s: %s\n%s", mk.Phase, mk.Outcome, w.dump())
	}
	lines := strings.Join(Status(w.ctx, mk, []ZcashSource{{Name: "z", Node: w.zc.client()}}, []DeroSource{{Name: "d", Node: newMemDero(w.dero, makerAddr)}}), "\n")
	if !strings.Contains(lines, "past T_dero") || regexp.MustCompile(`-\d+ blocks?`).MatchString(lines) {
		t.Fatalf("status counts past T_dero wrongly:\n%s", lines)
	}
}

func TestCountdownNeverPrintsANegativeCount(t *testing.T) {
	for _, c := range []struct {
		deadline, now int64
		want          string
	}{{10, 7, "3 blocks to cltv"}, {10, 9, "1 block to cltv"}, {10, 10, "at cltv"}, {10, 11, "1 block past cltv"}, {10, 15, "5 blocks past cltv"}} {
		if got := countdown(c.deadline, c.now, "cltv"); got != c.want {
			t.Errorf("countdown(%d, %d) = %q, want %q", c.deadline, c.now, got, c.want)
		}
	}
}
