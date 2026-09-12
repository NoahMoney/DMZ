package zecswap

import (
	"os"
	"strings"
	"testing"
)

// DERO mines a call up to 10 blocks after the height its wallet built it at (vendor/derohe/blockchain/blockchain.go:748),
// and board.bas and swap.bas read the height it executes at. These tests run the machines with the fake chain executing
// calls that late (fakeDero.execDelay), at each boundary where the companion sets a height or holds a call to one.

func callsLate(w *world) {
	w.dero.mu.Lock()
	defer w.dero.mu.Unlock()
	w.dero.execDelay = func() uint64 { return DeroTxValidity - 1 }
}

func callsOnTime(w *world) {
	w.dero.mu.Lock()
	defer w.dero.mu.Unlock()
	w.dero.execDelay = nil
}

// TestTDeroWidthLeavesWMinAtTheLatestExecution: T_dero is wmin + DeroTxValidity above the Lock's build height wherever
// that is wider than the policy's window, so swap.bas line 24 takes the Lock at every height DERO may execute it at.
func TestTDeroWidthLeavesWMinAtTheLatestExecution(t *testing.T) {
	p := DefaultPolicy()
	makerRaw, _ := DeroRaw(makerAddr)
	takerRaw, _ := DeroRaw(takerAddr)
	hash := strings.Repeat("ab", 32)
	const built = 5000
	for _, c := range []struct{ wmin, want uint64 }{{1, 1600}, {1200, 1600}, {1589, 1600}, {1590, 1601}, {1600, 1611}} {
		width, err := p.TDeroWidth(c.wmin)
		if err != nil || width != c.want {
			t.Fatalf("wmin %d: T_dero − the build height = %d (%v), want %d", c.wmin, width, err, c.want)
		}
		chain := newFakeDero(built)
		setWMin(chain, c.wmin)
		for exec := uint64(built + 1); exec < built+DeroTxValidity; exec++ {
			if _, err := chain.lock(exec, makerRaw, hash, takerRaw, built+width, "", 1); err != nil {
				t.Fatalf("wmin %d: a Lock built at %d with T_dero %d is refused executing at %d: %v", c.wmin, built, built+width, exec, err)
			}
		}
	}
	// the defect this replaces: T_dero was the build height + 1,603 (the window and SettleBlocks), which swap.bas refuses
	// against the mainnet wmin of 1,600 once the Lock executes 4 blocks late
	chain := newFakeDero(built)
	setWMin(chain, 1600)
	if _, err := chain.lock(built+4, makerRaw, hash, takerRaw, built+1603, "", 1); err == nil {
		t.Fatal("swap.bas's rule took an expiry 1,599 blocks above the executing height against wmin 1,600")
	}
	if _, err := p.TDeroWidth(MaxEscrowWidth - DeroTxValidity + 1); err == nil {
		t.Fatal("a width past swap.bas's million-block ceiling was not refused")
	}
}

// TestMakerLockAtTheWMinBoundaryExecutesTenBlocksLate: the fake escrow is installed with wmin = the policy's T_dero
// window, as a mainnet deployment is (1,600 each). The maker's Lock executes 10 blocks after the height he built it at,
// the most DERO allows, and swap.bas takes it, its fee not lost. The whole swap then completes with every call as late.
func TestMakerLockAtTheWMinBoundaryExecutesTenBlocksLate(t *testing.T) {
	w := newWorld(t, 61)
	wmin := wminFor(w.dero)
	if wmin != w.p.TDeroWindow {
		t.Fatalf("the fake escrow's wmin %d is not the policy's window %d: not the boundary", wmin, w.p.TDeroWindow)
	}
	callsLate(w)
	w.stepN(RoleMaker, 2)
	w.stepN(RoleTaker, 2)
	w.stepN(RoleMaker, 1)
	if ph := w.state(RoleMaker).Phase; ph != "locking" {
		t.Fatalf("maker in %s\n%s", ph, w.dump())
	}
	built := w.dero.Height()
	w.stepN(RoleMaker, 1)
	mk, es, exec := w.state(RoleMaker), w.dero.Escrows(), w.dero.Height()
	if mk.Phase != "locked" || len(es) != 1 || w.dero.Refused("Lock") != 0 {
		t.Fatalf("maker %s (%s) with %d escrows, %d Locks refused\n%s", mk.Phase, mk.Note, len(es), w.dero.Refused("Lock"), w.dump())
	}
	if exec != built+DeroTxValidity-1 || es[0].Expiry != built+wmin+DeroTxValidity || es[0].Expiry < exec+wmin {
		t.Fatalf("a Lock built at %d and executed at %d has T_dero %d against wmin %d", built, exec, es[0].Expiry, wmin)
	}
	if !w.drive(400) {
		t.Fatalf("the swap did not finish: maker %s, taker %s\n%s", w.state(RoleMaker).Phase, w.state(RoleTaker).Phase, w.dump())
	}
	w.check("a lock at the wmin boundary", []string{"swapped"}, []string{"swapped"})
}

// TestEveryScenarioWithTheLongestExecutionDelay: every DERO call executes 10 blocks after the height it was built at,
// and each scenario still ends in its outcome, with no Post, Accept or Lock refused on chain (check).
func TestEveryScenarioWithTheLongestExecutionDelay(t *testing.T) {
	for i, scenario := range []string{"happy", "maker_absent", "taker_late", "maker_never_locks"} {
		w := newWorld(t, int64(71+i))
		w.offline = offlineFor(scenario)
		callsLate(w)
		if !w.drive(500) {
			t.Fatalf("%s did not finish with every call 10 blocks late: maker %s, taker %s\n%s", scenario, w.state(RoleMaker).Phase, w.state(RoleTaker).Phase, w.dump())
		}
		o := scenarioOutcomes[scenario]
		w.check(scenario+", every call 10 blocks late", o[0], o[1])
	}
}

// TestNoAcceptThatCouldExecuteAtTheExpiry: board.bas refuses an Accept that executes at or after the offer's expiry,
// and one built at h may execute at h + 10. The taker sends none once h + DeroTxValidity reaches the expiry, and
// declines at the expiry with nothing spent.
func TestNoAcceptThatCouldExecuteAtTheExpiry(t *testing.T) {
	w := newWorld(t, 62)
	w.stepN(RoleMaker, 2)
	exp := w.dero.Offer(0).Expiry
	w.dero.mu.Lock()
	w.dero.height = exp - DeroTxValidity - 1
	w.dero.mu.Unlock()
	w.stepN(RoleTaker, 1)
	if st := w.state(RoleTaker); st.Phase != "accepting" {
		t.Fatalf("taker %s (%s) with the expiry %d blocks away", st.Phase, st.Outcome, DeroTxValidity+1)
	}
	w.dero.Advance(1)
	w.stepN(RoleTaker, 3)
	if n := w.sw[RoleTaker].dero.(*memDero).calls["Accept"]; n != 0 || w.state(RoleTaker).Phase != "accepting" || w.dero.Offer(0).State != OfferOpen {
		t.Fatalf("%d Accepts sent at height %d against the expiry %d", n, w.dero.Height(), exp)
	}
	w.dero.Advance(DeroTxValidity)
	w.stepN(RoleTaker, 1)
	if st := w.state(RoleTaker); st.Phase != "declined" || w.dero.Refused("Accept") != 0 {
		t.Fatalf("taker %s: %s", st.Phase, st.Outcome)
	}
}

// TestMakerLocksOnlyWhileTheLockLandsBeforeTheDeadline: at the last height lockOpen allows, the maker's Lock executes
// 10 blocks late and still lands before board.bas's ad_<i>, and the taker takes it; one block later he sends none.
func TestMakerLocksOnlyWhileTheLockLandsBeforeTheDeadline(t *testing.T) {
	margin := DeroTxValidity + testPolicy().SettleBlocks
	for _, c := range []struct {
		before uint64 // ad_<i> − the height the maker first reads the accept at
		locks  bool
	}{{margin + 1, true}, {margin, false}} {
		w := newWorld(t, 63)
		w.stepN(RoleMaker, 2)
		w.stepN(RoleTaker, 2)
		ad := w.dero.Offer(0).AcceptDeadline
		w.dero.mu.Lock()
		w.dero.height = ad - c.before
		w.dero.mu.Unlock()
		callsLate(w)
		w.stepN(RoleMaker, 2)
		es, mk := w.dero.Escrows(), w.state(RoleMaker)
		if !c.locks {
			if len(es) != 0 || mk.Phase != "cancel_wait" {
				t.Fatalf("%d blocks before the deadline: maker %s with %d escrows", c.before, mk.Phase, len(es))
			}
			continue
		}
		if len(es) != 1 || mk.Phase != "locked" || w.dero.Height() >= ad {
			t.Fatalf("%d blocks before the deadline %d: maker %s (%s) with %d escrows, at height %d\n%s", c.before, ad, mk.Phase, mk.Note, len(es), w.dero.Height(), w.dump())
		}
		w.stepN(RoleTaker, 1)
		if st := w.state(RoleTaker); st.Phase != "lock_seen" {
			t.Fatalf("taker %s: %s", st.Phase, st.Outcome)
		}
	}
}

// TestTakerCountsPLockFromHerAcceptsExecution: her Accept executes 10 blocks after she built it, so board.bas's ad_<i>
// is 10 blocks later than P_lock counted from her build height. The maker locks at the last height he may, and his
// Lock waits in the pool; she does not walk at P_lock after her build height, and funds against the Lock when it lands
// before ad_<i>.
func TestTakerCountsPLockFromHerAcceptsExecution(t *testing.T) {
	w := newWorld(t, 64)
	w.stepN(RoleMaker, 2)
	w.stepN(RoleTaker, 1)
	built := w.dero.Height()
	callsLate(w)
	w.stepN(RoleTaker, 1)
	callsOnTime(w)
	tk, ad := w.state(RoleTaker), w.dero.Offer(0).AcceptDeadline
	if tk.Phase != "accepted" || tk.AcceptHeight != built || ad != built+DeroTxValidity-1+fakeAcceptWindow {
		t.Fatalf("taker %s, accept height %d (built at %d), ad %d", tk.Phase, tk.AcceptHeight, built, ad)
	}
	sent := ad - DeroTxValidity - w.p.SettleBlocks - 1
	w.dero.mu.Lock()
	w.dero.height, w.dero.holdLock = sent, 8
	w.dero.mu.Unlock()
	w.stepN(RoleMaker, 2)
	if n := len(w.dero.Escrows()); n != 0 || w.state(RoleMaker).Phase != "locking" {
		t.Fatalf("maker %s with %d escrows: the Lock was not held in the pool", w.state(RoleMaker).Phase, n)
	}
	w.dero.Advance(built + w.p.PLock - sent)
	w.stepN(RoleTaker, 1)
	if st := w.state(RoleTaker); st.Phase != "accepted" {
		t.Fatalf("at DERO height %d, P_lock after the height her Accept was built at and before the board's deadline %d, the taker is in %s: %s", w.dero.Height(), ad, st.Phase, st.Outcome)
	}
	w.dero.Advance(sent + 8 - w.dero.Height())
	if n := len(w.dero.Escrows()); n != 1 || w.dero.Height() >= ad {
		t.Fatalf("%d escrows at height %d, deadline %d", n, w.dero.Height(), ad)
	}
	w.stepN(RoleTaker, 1)
	if st := w.state(RoleTaker); st.Phase != "lock_seen" {
		t.Fatalf("taker %s: %s", st.Phase, st.Outcome)
	}
}

// TestRestartWhileTheLockIsInThePoolSendsNoSecondLock: the maker's companion dies while its Lock waits in the pool,
// before it has learned anything about it. swap.bas would take a second Lock under the same hash as a second escrow,
// which the one secret opens too. The state on disk as the Lock left already records the attempt, so the restarted
// companion waits DeroTxValidity blocks from it, finds the first Lock mined, and sends none.
func TestRestartWhileTheLockIsInThePoolSendsNoSecondLock(t *testing.T) {
	w := newWorld(t, 65)
	w.stepN(RoleMaker, 2)
	w.stepN(RoleTaker, 2)
	w.stepN(RoleMaker, 1)
	if ph := w.state(RoleMaker).Phase; ph != "locking" {
		t.Fatalf("maker in %s", ph)
	}
	var onDisk []byte
	w.dero.mu.Lock()
	w.dero.holdLock = DeroTxValidity - 1
	w.dero.onHold = func() {
		var err error
		if onDisk, err = os.ReadFile(w.path(RoleMaker)); err != nil {
			t.Error(err)
		}
	}
	w.dero.mu.Unlock()
	w.stepN(RoleMaker, 1)
	if err := os.WriteFile(w.path(RoleMaker), onDisk, 0o600); err != nil {
		t.Fatal(err)
	}
	w.sw[RoleMaker] = nil
	for i := 0; i < 12 && w.state(RoleMaker).Phase == "locking"; i++ {
		w.step(RoleMaker)
		w.dero.Advance(2)
	}
	es, st := w.dero.Escrows(), w.state(RoleMaker)
	if len(es) != 1 || st.Phase != "locked" || st.EscrowID != es[0].ID {
		t.Fatalf("maker %s with %d escrows: a second Lock went out while the first was in the pool\n%s", st.Phase, len(es), w.dump())
	}
	if n := w.sw[RoleMaker].dero.(*memDero).calls["Lock"]; n != 0 {
		t.Fatalf("the restarted maker sent %d Lock(s)", n)
	}
}

// TestTakerCountsHerClaimOnlyOnceItsLastBlockIsStable: her Claim executes 10 blocks after the height she sent it at. She
// calls the swap done only once the last block it could execute in is stable, not when the block she sent it at is.
func TestTakerCountsHerClaimOnlyOnceItsLastBlockIsStable(t *testing.T) {
	w := newWorld(t, 66)
	// the maker stays away once he has claimed the ZEC, so the DERO Claim is hers and not his relay
	w.offline = func(w *world, role string) bool { return role == RoleMaker && w.state(RoleMaker).Phase == "claimed" }
	for i := 0; i < 300 && w.state(RoleTaker).Phase != "secret_seen"; i++ {
		w.round()
	}
	if ph := w.state(RoleTaker).Phase; ph != "secret_seen" {
		t.Fatalf("taker in %s\n%s", ph, w.dump())
	}
	built := w.dero.Height()
	callsLate(w)
	w.stepN(RoleTaker, 1)
	tk := w.state(RoleTaker)
	if tk.Phase != "dero_claimed" || tk.DeroClaimHeight != built || w.dero.Escrows()[tk.EscrowID].State != EscrowClaimed {
		t.Fatalf("taker %s, Claim height %d (built at %d)", tk.Phase, tk.DeroClaimHeight, built)
	}
	w.stepN(RoleTaker, 1)
	if ph := w.state(RoleTaker).Phase; ph != "dero_claimed" {
		t.Fatalf("taker %s with her Claim, executed at %d, at the tip %d", ph, built+DeroTxValidity-1, w.dero.Height())
	}
	w.dero.Advance(built + DeroTxValidity + DeroStableDepth - w.dero.Height())
	w.stepN(RoleTaker, 1)
	if st := w.state(RoleTaker); st.Phase != "swapped" {
		t.Fatalf("taker %s: %s", st.Phase, st.Outcome)
	}
}
