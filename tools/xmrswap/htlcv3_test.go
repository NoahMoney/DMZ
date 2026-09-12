package xmrswap

// htlc.bas v3 in the companions (MT-A03(a), MT-J13, docs/HTLC_V3_COMPANION_NOTES.md): the install arguments are read
// and held to each party's policy; the maker builds no Lock the contract would refuse for its width; the taker's Punish
// has a deadline and counts only once stable; and a lock nobody punished is the maker's to Reclaim, after which the
// taker recovers her XMR as after a Refund. Each drives the real state machines over the fakes in fakes_test.go.

import (
	"strings"
	"testing"
)

// TestMakerRefusesAnInstallHisWidthsCannotMeet: an install whose w1min, with the DeroTxValidity blocks a Lock may wait
// to execute, is wider than the agreed t1 width, or whose w2min is wider than t2 − t1, gets no Lock. The daemon's
// estimate runs at height 0 and would pass the first; the chain would refuse it and keep the fee.
func TestMakerRefusesAnInstallHisWidthsCannotMeet(t *testing.T) {
	for _, c := range []struct {
		name         string
		w1min, w2min uint64
		want         string
	}{
		{"w1min + 11 above the t1 width", testT1Delta - DeroTxValidity + 1, testW2Min, "t1 width"},
		{"w2min above t2 − t1", testW1Min, testT2Delta + 1, "w2min"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRun(t, plan{scenario: "happy", seed: 301})
			r.chain.mu.Lock()
			r.chain.w1min, r.chain.w2min = c.w1min, c.w2min
			r.chain.mu.Unlock()
			r.bob = startWith(r, "bob", nil)
			r.alice = startWith(r, "alice", nil)
			for i := 0; i < 30; i++ {
				r.step("bob")
				r.step("alice")
				r.chain.Advance(1)
			}
			bob := r.loadState("bob")
			if bob.Phase != "refused" || !strings.Contains(bob.Outcome, c.want) || len(r.chain.locks) != 0 || r.aliceW.transfers != 0 {
				t.Fatalf("bob %s (%s), %d locks, %d XMR transfers", bob.Phase, bob.Outcome, len(r.chain.locks), r.aliceW.transfers)
			}
			t.Logf("bob: %s", bob.Outcome)
		})
	}
}

// TestTakerRefusesAShortPunishWindow (MT-J13): an install whose g2 is below her floor is refused before any XMR moves;
// the maker, whose own policy took it, refunds at t1.
func TestTakerRefusesAShortPunishWindow(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 302})
	r.chain.mu.Lock()
	r.chain.g2 = testG2 - 1
	r.chain.mu.Unlock()
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	r.until(600, r.bothDone, nil)
	alice := r.loadState("alice")
	if alice.Phase != "refused" || !strings.Contains(alice.Outcome, "g2") || r.aliceW.transfers != 0 {
		t.Fatalf("alice %s (%s), %d transfers", alice.Phase, alice.Outcome, r.aliceW.transfers)
	}
	r.expectExit(LockRefunded)
	t.Logf("alice: %s", alice.Outcome)
	// and a floor that leaves no height to Punish at is refused when the companion starts
	cfg := r.config("alice")
	cfg.G2Min = DeroTxValidity + DefaultStableBlocks + testClaimMargin
	if _, err := NewAlice(cfg, NewState(r.statePath("x"), "alice", testHTLC), newMemDero(r.chain, aliceAddr), r.aliceW, r.prover, nil); err == nil {
		t.Fatal("a g2 floor with no height to Punish at was accepted")
	}
}

// TestBobBuildsNoLockPastTheCutoff (MT-A03(a)): a maker whose wallet was away until the height at which a Lock could
// execute less than w1min before t1 sends none: htlc.bas v3 would refuse it and keep the fee, and the estimate would
// not say so. The state records every Lock attempt before its transaction leaves, so with none on record he refuses at
// once, after a restart too. With one on record, from a process that died after it, he first waits until every block
// that Lock could be mined in is stable, and refuses only once none can land.
func TestBobBuildsNoLockPastTheCutoff(t *testing.T) {
	for _, c := range []struct {
		name               string
		restart, attempted bool
	}{{"no restart", false, false}, {"a restart with no attempt on record", true, false}, {"a restart with an attempt on record", true, true}} {
		r := newRun(t, plan{scenario: "happy", seed: 303})
		r.chain.flaky = nil
		r.bob = startWith(r, "bob", nil)
		r.alice = startWith(r, "alice", nil)
		for i := 0; i < 40; i++ {
			if p, _ := r.phase("bob"); p == "dero_locking" {
				break
			}
			r.step("bob")
			r.step("alice")
		}
		if p, _ := r.phase("bob"); p != "dero_locking" {
			t.Fatalf("bob never reached dero_locking: %s", p)
		}
		cut := lockCutoff(r.loadState("bob").Lock.T1, testW1Min)
		for r.chain.Height() < cut {
			r.chain.Advance(1) // his wallet is away until the cutoff
		}
		d := r.bob.dero.(*memDero)
		if c.restart {
			if c.attempted {
				// the attempt a process recorded at the last height it could send at, before it died
				st := r.loadState("bob")
				st.Lock.AttemptHeight = cut - 1
				if err := st.Save(); err != nil {
					t.Fatal(err)
				}
			}
			d = newMemDero(r.chain, bobAddr)
			r.bob = startHooked(r, "bob", d, nil)
			if c.attempted {
				end := r.bob.lockWindowEnd(cut - 1)
				for r.chain.StableHeight() < end {
					r.step("bob")
					if p, _ := r.phase("bob"); p != "dero_locking" {
						t.Fatalf("%s: at stable height %d, below %d, the last block the recorded Lock could land in, bob moved to %s", c.name, r.chain.StableHeight(), end, p)
					}
					r.chain.Advance(1)
				}
			}
		}
		r.step("bob")
		bob := r.loadState("bob")
		if bob.Phase != "refused" || !strings.Contains(bob.Outcome, "w1min") || len(r.chain.locks) != 0 || d.calls["LockSent"] != 0 {
			t.Fatalf("%s: bob %s (%s), %d locks, %d Lock transactions sent", c.name, bob.Phase, bob.Outcome, len(r.chain.locks), d.calls["LockSent"])
		}
		t.Logf("%s: bob: %s", c.name, bob.Outcome)
	}
}

// TestTakerPastHerPunishCutoffWaitsForTheReclaim (MT-J13): the taker comes back after her punish cutoff with the lock
// still open. She sends no Punish — one could land only at or past t2 + g2, where the contract refuses it — and waits;
// the maker reclaims from t2 + g2, and she reads s_b from xb_<id> and sweeps her XMR back.
func TestTakerPastHerPunishCutoffWaitsForTheReclaim(t *testing.T) {
	r := newRun(t, plan{scenario: "punish", seed: 304})
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	r.until(400, func() bool { p, _ := r.phase("alice"); return p == "xmr_locked" }, func() bool { return !r.stipendPaid() })
	t2 := r.loadState("bob").Lock.T2
	cut := punishCutoff(t2, testG2, testClaimMargin)
	for r.chain.Height() < cut {
		r.chain.Advance(1) // both away until her cutoff
		r.fw.Mine(1)
	}
	for i := 0; i < 6; i++ {
		r.step("alice")
	}
	if p, _ := r.phase("alice"); p != "await_reclaim" || r.alice.dero.(*memDero).calls["Punish"] != 0 {
		t.Fatalf("past her cutoff alice is in %s having sent %d Punish calls", p, r.alice.dero.(*memDero).calls["Punish"])
	}
	r.until(200, r.bothDone, nil)
	bob, alice := r.loadState("bob"), r.loadState("alice")
	l, _ := r.chain.read(bob.Lock.ID)
	if l.State != LockReclaimed || bob.Phase != "reclaimed" || alice.Phase != "swept" || alice.Revealed != bob.Own.Secret {
		t.Fatalf("lock %d, bob %s, alice %s (%s)", l.State, bob.Phase, alice.Phase, alice.Outcome)
	}
	if r.alice.dero.(*memDero).calls["Punish"] != 0 || r.bob.dero.(*memDero).calls["Punish"] != 0 {
		t.Fatal("a Punish went out")
	}
	r.check()
}

// TestTakerWatchesHerPunishUntilStable (MT-J13): her Punish counts only once DERO can no longer replace its block. One
// undone before it is stable is sent again, while the cutoff allows, and only the one that becomes stable ends her
// companion.
func TestTakerWatchesHerPunishUntilStable(t *testing.T) {
	r := newRun(t, plan{scenario: "punish", seed: 305})
	r.chain.mu.Lock()
	r.chain.g2 = 60 // room for a second Punish after the first is undone
	r.chain.mu.Unlock()
	bobOn := func() bool { return !r.stipendPaid() }
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	r.until(600, func() bool { p, _ := r.phase("alice"); return p == "punish_confirming" }, bobOn)
	alice := r.loadState("alice")
	if alice.Done || alice.Lock.PunishSeenHeight == 0 || r.chain.StableHeight() >= alice.Lock.PunishSeenHeight {
		t.Fatalf("alice done %v with her punish read at %d and the stable height %d", alice.Done, alice.Lock.PunishSeenHeight, r.chain.StableHeight())
	}
	r.chain.unpunish(0) // the block holding her Punish is replaced before it is stable
	r.until(200, func() bool { _, done := r.phase("alice"); return done }, bobOn)
	alice = r.loadState("alice")
	if n := r.alice.dero.(*memDero).calls["Punish"]; n != 2 || alice.Phase != "punished" || !hasLog(r, "is not on the chain any more") {
		t.Fatalf("alice %s after %d Punish calls", alice.Phase, n)
	}
	r.expectExit(LockPunished)
}

// TestTakerPunishesAtHerLastHeight (MT-J13): at punishCutoff − 1, the last height her companion sends Punish at, it goes
// out and executes inside [t2, t2 + g2); at the cutoff itself nothing is sent (TestTakerPastHerPunishCutoffWaitsForTheReclaim).
func TestTakerPunishesAtHerLastHeight(t *testing.T) {
	r := newRun(t, plan{scenario: "punish", seed: 306})
	r.chain.flaky = nil
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	r.until(400, func() bool { p, _ := r.phase("alice"); return p == "xmr_locked" }, func() bool { return !r.stipendPaid() })
	t2 := r.loadState("bob").Lock.T2
	cut := punishCutoff(t2, testG2, testClaimMargin)
	for r.chain.Height() < cut-1 {
		r.chain.Advance(1) // both away until the last height she may send at
		r.fw.Mine(1)
	}
	d := r.alice.dero.(*memDero)
	for i := 0; i < 6 && d.calls["Punish"] == 0; i++ {
		if h := r.chain.Height(); h != cut-1 {
			t.Fatalf("the chain is at %d, not cut − 1 = %d, before her Punish", h, cut-1)
		}
		r.step("alice")
	}
	l, _ := r.chain.read(0)
	if p, _ := r.phase("alice"); d.calls["Punish"] != 1 || p != "punish_confirming" || l.State != LockPunished || cut-1 < t2 || cut-1 >= t2+testG2 {
		t.Fatalf("at cut − 1 = %d (t2 %d, g2 %d) alice is in %s after %d Punish calls, lock state %d", cut-1, t2, testG2, p, d.calls["Punish"], l.State)
	}
	r.until(60, func() bool { _, done := r.phase("alice"); return done }, func() bool { return false })
	if a := r.loadState("alice"); a.Phase != "punished" {
		t.Fatalf("alice ended in %s: %s", a.Phase, a.Outcome)
	}
}

// TestMakerWatchesHisReclaimUntilStable (MT-J13): his Reclaim counts only once DERO can no longer replace its block. One
// undone before it is stable, and not mined again, leaves the lock open with nobody but him able to move it: he sends it
// again, and only the Reclaim that becomes stable ends his companion. The taker then recovers her XMR with the s_b it
// reveals.
func TestMakerWatchesHisReclaimUntilStable(t *testing.T) {
	r := newRun(t, plan{scenario: "punish", seed: 307})
	r.chain.flaky = nil
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	r.until(400, func() bool { p, _ := r.phase("alice"); return p == "xmr_locked" }, func() bool { return !r.stipendPaid() })
	t2 := r.loadState("bob").Lock.T2
	for r.chain.Height() < t2+1 {
		r.chain.Advance(1) // both away: nobody refunds and nobody punishes
		r.fw.Mine(1)
	}
	bobUntil := func(stop func(*State) bool) *State {
		for i := 0; i < 80; i++ {
			if b := r.loadState("bob"); stop(b) {
				return b
			}
			r.step("bob")
			r.chain.Advance(1)
			r.fw.Mine(1)
		}
		b := r.loadState("bob")
		t.Fatalf("bob stuck in %s at height %d\n%s", b.Phase, r.chain.Height(), strings.Join(r.logs, "\n"))
		return nil
	}
	bob := bobUntil(func(b *State) bool { return b.Phase == "reclaim_confirming" })
	if bob.Done || bob.ReclaimTx == "" || bob.Lock.ReclaimSeenHeight == 0 || r.chain.StableHeight() >= bob.Lock.ReclaimSeenHeight {
		t.Fatalf("bob done %v (tx %q) with his reclaim read at %d and the stable height %d", bob.Done, bob.ReclaimTx, bob.Lock.ReclaimSeenHeight, r.chain.StableHeight())
	}
	r.chain.unreclaim(0) // the block holding his Reclaim is replaced before it is stable, and nobody mines it again
	bob = bobUntil(func(b *State) bool { return b.Done })
	if n := r.bob.dero.(*memDero).calls["Reclaim"]; n != 2 || bob.Phase != "reclaimed" || !hasLog(r, "the reclaim read at height") {
		t.Fatalf("bob %s after %d Reclaim calls: %s", bob.Phase, n, bob.Outcome)
	}
	if r.chain.StableHeight() < bob.Lock.ReclaimSeenHeight {
		t.Fatalf("bob done with his second reclaim read at %d, above the stable height %d", bob.Lock.ReclaimSeenHeight, r.chain.StableHeight())
	}
	for i := 0; i < 60; i++ {
		if _, done := r.phase("alice"); done {
			break
		}
		r.step("alice")
		r.chain.Advance(1)
		r.fw.Mine(1)
	}
	if a := r.loadState("alice"); a.Phase != "swept" || a.Revealed != bob.Own.Secret {
		t.Fatalf("the taker ended in %s (%s)", a.Phase, a.Outcome)
	}
	r.expectExit(LockReclaimed)
	r.check()
}

// --- MT-B27: the maker's Refund has a cutoff, t2 − DeroTxValidity, and counts only once stable ---

// bobAwayUntil runs both sides until the taker has locked XMR, with the maker away from the moment his stipend is paid,
// and then moves the chain on with nobody running until height h.
func bobAwayUntil(r *run, h uint64) {
	r.until(400, func() bool { p, _ := r.phase("alice"); return p == "xmr_locked" }, func() bool { return !r.stipendPaid() })
	for r.chain.Height() < h {
		r.chain.Advance(1)
		r.fw.Mine(1)
	}
}

// TestBobRefundsAtHisLastHeight (MT-B27): at refundCutoff − 1, the last height his companion sends a Refund at, it goes
// out and, executing as late as DERO allows (10 blocks after the height it was built at), still lands before t2; he
// counts it only once it is stable, and the taker recovers her XMR with the s_b it reveals.
func TestBobRefundsAtHisLastHeight(t *testing.T) {
	r := newRun(t, plan{scenario: "refund", seed: 309})
	r.chain.flaky = nil
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	r.until(400, func() bool { p, _ := r.phase("alice"); return p == "xmr_locked" }, func() bool { return !r.stipendPaid() })
	t2 := r.loadState("bob").Lock.T2
	cut := refundCutoff(t2)
	bobAwayUntil(r, cut-1)
	r.chain.mu.Lock()
	r.chain.execDelay = func() uint64 { return DeroTxValidity - 1 }
	r.chain.mu.Unlock()
	d := r.bob.dero.(*memDero)
	for i := 0; i < 6 && d.calls["Refund"] == 0; i++ {
		if h := r.chain.Height(); h != cut-1 {
			t.Fatalf("the chain is at %d, not cut − 1 = %d, before his Refund", h, cut-1)
		}
		r.step("bob")
	}
	r.chain.mu.Lock()
	r.chain.execDelay = nil
	r.chain.mu.Unlock()
	l, _ := r.chain.read(0)
	if p, _ := r.phase("bob"); d.calls["Refund"] != 1 || p != "refund_confirming" || l.State != LockRefunded || r.chain.Height() > t2 {
		t.Fatalf("at cut − 1 = %d (t2 %d) bob is in %s after %d Refund calls, lock state %d, height now %d", cut-1, t2, p, d.calls["Refund"], l.State, r.chain.Height())
	}
	r.until(200, r.bothDone, nil)
	bob, alice := r.loadState("bob"), r.loadState("alice")
	if bob.Phase != "refunded" || !strings.Contains(bob.Outcome, "stable") || r.chain.StableHeight() < bob.Lock.RefundSeenHeight || alice.Phase != "swept" {
		t.Fatalf("bob %s (%s), refund read at %d, stable height %d; alice %s", bob.Phase, bob.Outcome, bob.Lock.RefundSeenHeight, r.chain.StableHeight(), alice.Phase)
	}
	r.expectExit(LockRefunded)
	r.check()
}

// TestBobSendsNoRefundAtTheCutoff (MT-B27): at the refund cutoff itself nothing is sent — one built there could execute
// at t2, where htlc.bas refuses it and keeps the fee — and his companion says what that leaves him: the lock waits for
// anyone's Punish in [t2, t2 + g2), which pays the taker his DERO, or else for his Reclaim. Here the taker punishes.
func TestBobSendsNoRefundAtTheCutoff(t *testing.T) {
	r := newRun(t, plan{scenario: "refund", seed: 310})
	r.chain.flaky = nil
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	r.until(400, func() bool { p, _ := r.phase("alice"); return p == "xmr_locked" }, func() bool { return !r.stipendPaid() })
	cut := refundCutoff(r.loadState("bob").Lock.T2)
	bobAwayUntil(r, cut)
	d := r.bob.dero.(*memDero)
	for i := 0; i < 6; i++ {
		if p, _ := r.phase("bob"); p == "punish_window" {
			break
		}
		if h := r.chain.Height(); h != cut {
			t.Fatalf("the chain is at %d, not the cutoff %d", h, cut)
		}
		r.step("bob")
	}
	if p, _ := r.phase("bob"); p != "punish_window" || d.calls["Refund"] != 0 || !hasLog(r, "at or past the refund cutoff") || !hasLog(r, "anyone may Punish the lock, which pays alice the DERO") {
		t.Fatalf("at the cutoff %d bob is in %s after %d Refund calls\n%s", cut, p, d.calls["Refund"], strings.Join(r.logs, "\n"))
	}
	r.until(300, r.bothDone, nil)
	bob, alice := r.loadState("bob"), r.loadState("alice")
	if bob.Phase != "punished" || alice.Phase != "punished" || d.calls["Refund"] != 0 {
		t.Fatalf("bob %s, alice %s, %d Refund calls", bob.Phase, alice.Phase, d.calls["Refund"])
	}
	r.expectExit(LockPunished)
	r.check()
}

// TestMakerWatchesHisRefundUntilStable (MT-B27): his Refund counts only once DERO can no longer replace its block. One
// undone before it is stable, while the height is still before the refund cutoff, is sent again at once — the intent
// saved first, as before every send — and only the Refund that becomes stable ends his companion.
func TestMakerWatchesHisRefundUntilStable(t *testing.T) {
	r := newRun(t, plan{scenario: "refund", seed: 311})
	r.chain.flaky = nil
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	r.until(400, func() bool { p, _ := r.phase("alice"); return p == "xmr_locked" }, func() bool { return !r.stipendPaid() })
	bobAwayUntil(r, r.loadState("bob").Lock.T1)
	var saves []string
	r.bob.crash = func(phase string) bool { saves = append(saves, phase); return false }
	bobUntil := func(stop func(*State) bool) *State {
		for i := 0; i < 80; i++ {
			if b := r.loadState("bob"); stop(b) {
				return b
			}
			r.step("bob")
			r.chain.Advance(1)
			r.fw.Mine(1)
		}
		b := r.loadState("bob")
		t.Fatalf("bob stuck in %s at height %d\n%s", b.Phase, r.chain.Height(), strings.Join(r.logs, "\n"))
		return nil
	}
	bob := bobUntil(func(b *State) bool { return b.Phase == "refund_confirming" })
	if bob.Done || bob.RefundTx == "" || bob.Lock.RefundSeenHeight == 0 || r.chain.StableHeight() >= bob.Lock.RefundSeenHeight {
		t.Fatalf("bob done %v (tx %q) with his refund read at %d and the stable height %d", bob.Done, bob.RefundTx, bob.Lock.RefundSeenHeight, r.chain.StableHeight())
	}
	if allowed, expected := lockStatesFor(bob); !expected || !allowed[LockOpen] || !allowed[LockRefunded] {
		t.Fatalf("status for refund_confirming: %v", allowed)
	}
	r.chain.unrefund(0) // the block holding his Refund is replaced before it is stable, and nobody mines it again
	if h, cut := r.chain.Height(), refundCutoff(bob.Lock.T2); h >= cut {
		t.Fatalf("the test's own premise: height %d is not before the refund cutoff %d", h, cut)
	}
	bob = bobUntil(func(b *State) bool { return b.Done })
	if n := r.bob.dero.(*memDero).calls["Refund"]; n != 2 || bob.Phase != "refunded" || !hasLog(r, "the Refund goes out again") {
		t.Fatalf("bob %s after %d Refund calls: %s", bob.Phase, n, bob.Outcome)
	}
	if r.chain.StableHeight() < bob.Lock.RefundSeenHeight {
		t.Fatalf("bob done with his second refund read at %d, above the stable height %d", bob.Lock.RefundSeenHeight, r.chain.StableHeight())
	}
	// the second Refund's intent was on disk before it went out: refunding is saved twice, each time before a send
	if n := strings.Count(strings.Join(saves, " "), "refunding"); n != 2 {
		t.Fatalf("refunding saved %d times: %v", n, saves)
	}
	for i := 0; i < 60; i++ {
		if _, done := r.phase("alice"); done {
			break
		}
		r.step("alice")
		r.chain.Advance(1)
		r.fw.Mine(1)
	}
	if a := r.loadState("alice"); a.Phase != "swept" || a.Revealed != bob.Own.Secret {
		t.Fatalf("the taker ended in %s (%s)", a.Phase, a.Outcome)
	}
	r.expectExit(LockRefunded)
	r.check()
}

// TestARefundUndonePastItsCutoffIsLeftToPunishOrReclaim (MT-B27): a Refund undone by a replaced block once the height
// is at or past the refund cutoff is not sent again — it could execute only at or after t2 — and his companion says so
// and what it leaves him. Here nobody punishes in [t2, t2 + g2), so his Reclaim from t2 + g2 returns his DERO and puts
// s_b on the chain, from which the taker recovers her XMR; had anyone punished, the DERO would have gone to her
// (TestBobSendsNoRefundAtTheCutoff).
func TestARefundUndonePastItsCutoffIsLeftToPunishOrReclaim(t *testing.T) {
	r := newRun(t, plan{scenario: "refund", seed: 312})
	r.chain.flaky = nil
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	r.until(400, func() bool { p, _ := r.phase("alice"); return p == "xmr_locked" }, func() bool { return !r.stipendPaid() })
	st := r.loadState("bob")
	cut := refundCutoff(st.Lock.T2)
	bobAwayUntil(r, cut-1)
	d := r.bob.dero.(*memDero)
	for i := 0; i < 6 && d.calls["Refund"] == 0; i++ {
		r.step("bob") // at cut − 1, his last height: the Refund goes out and executes
	}
	if p, _ := r.phase("bob"); p != "refund_confirming" || r.chain.Height() < cut {
		t.Fatalf("bob in %s at height %d after his Refund at cut − 1 = %d", p, r.chain.Height(), cut-1)
	}
	r.chain.unrefund(0) // replaced before it is stable, with the height now at the cutoff
	for i := 0; i < 200; i++ {
		if _, done := r.phase("bob"); done {
			break
		}
		r.step("bob") // the taker is away for the whole Punish window: nobody punishes
		r.chain.Advance(1)
		r.fw.Mine(1)
	}
	bob := r.loadState("bob")
	if bob.Phase != "reclaimed" || d.calls["Refund"] != 1 || d.calls["Reclaim"] != 1 || !hasLog(r, "no Refund can execute before t2") || !hasLog(r, "at or past the refund cutoff") {
		t.Fatalf("bob %s (%s) after %d Refund and %d Reclaim calls", bob.Phase, bob.Outcome, d.calls["Refund"], d.calls["Reclaim"])
	}
	for i := 0; i < 60; i++ {
		if _, done := r.phase("alice"); done {
			break
		}
		r.step("alice")
		r.chain.Advance(1)
		r.fw.Mine(1)
	}
	if a := r.loadState("alice"); a.Phase != "swept" || a.Revealed != bob.Own.Secret || r.alice.dero.(*memDero).calls["Punish"] != 0 {
		t.Fatalf("the taker ended in %s (%s)", a.Phase, a.Outcome)
	}
	r.expectExit(LockReclaimed)
	r.check()
}

// TestAV2SwapInFlightIsRefusedByName: htlc.bas v2's maker saved "closing" while he closed an abandoned lock with his own
// Punish; v3 has no such phase. A state file left there is refused when the companion starts, naming v2, rather than
// stopping on an unknown phase, and status says the same and compares it with the chain as v2 would have.
func TestAV2SwapInFlightIsRefusedByName(t *testing.T) {
	r := newRun(t, plan{scenario: "punish", seed: 308})
	st := NewState(r.statePath("bob"), "bob", testHTLC)
	st.Phase, st.MyDero = "closing", bobAddr
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	_, err := NewBob(r.config("bob"), r.loadState("bob"), newMemDero(r.chain, bobAddr), r.bobW, r.prover, nil)
	if err == nil || !strings.Contains(err.Error(), "htlc.bas v2") || !strings.Contains(err.Error(), `"closing"`) {
		t.Fatalf("a v2 state in closing: %v", err)
	}
	if allowed, expected := lockStatesFor(st); !expected || !allowed[LockOpen] || !allowed[LockPunished] || allowed[LockReclaimed] {
		t.Fatalf("status for closing: %v, lock expected %v", allowed, expected)
	}
	if rep := Reconcile(r.ctx, st, newMemDero(r.chain, bobAddr), nil); !diverged(rep, "htlc.bas v2's") {
		t.Fatalf("status of a v2 state: %v", rep.Diverged)
	}
	t.Logf("refused: %v", err)
}
