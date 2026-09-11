package xmrswap

// Named regressions for the readiness pass: the stipend (Path 0), the asset leg (U1), the gate deadline (U2), the
// Claim watched to stable depth (U3), the code pin and lower-case hashes (U4), the maker closing an abandoned lock
// under htlc.bas v2 (LIV-07), and the replay rule (a lock, or a Monero payment, is never served to two swaps).
// Each drives the real state machines over the fakes in fakes_test.go.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// startHooked is startWith with the Dero the caller wants for that side: the run's own state file, channel,
// counting wallet and invariant hooks.
func startHooked(r *run, role string, d Dero, mod func(*Config)) *Swap {
	st := r.loadState(role)
	ch, err := NewDirChannel(filepath.Join(r.dir, "channel"), role)
	if err != nil {
		r.t.Fatal(err)
	}
	ch.Poll = time.Millisecond
	spy := &spyChannel{Channel: ch, onSend: func(to string, m Message) error { return r.checkSend(role, to, m) }}
	cfg := r.config(role)
	if mod != nil {
		mod(&cfg)
	}
	var sw *Swap
	if role == "bob" {
		sw, err = NewBob(cfg, st, d, r.bobW, r.prover, spy)
	} else {
		sw, err = NewAlice(cfg, st, d, r.aliceW, r.prover, spy)
	}
	if err != nil {
		r.t.Fatal(err)
	}
	return sw
}

// startSide is a second swap in the run's directory: its own state file and channel, the shared chain, Monero node
// and used-secrets register, and none of the first swap's hooks.
func startSide(r *run, role, stateFile, channelDir string, d Dero, w Wallet, mod func(*Config)) *Swap {
	st, err := LoadState(filepath.Join(r.dir, stateFile), role, testHTLC)
	if err != nil {
		r.t.Fatal(err)
	}
	ch, err := NewDirChannel(filepath.Join(r.dir, channelDir), role)
	if err != nil {
		r.t.Fatal(err)
	}
	ch.Poll = time.Millisecond
	cfg := r.config(role)
	if mod != nil {
		mod(&cfg)
	}
	var sw *Swap
	if role == "bob" {
		sw, err = NewBob(cfg, st, d, w, r.prover, ch)
	} else {
		sw, err = NewAlice(cfg, st, d, w, r.prover, ch)
	}
	if err != nil {
		r.t.Fatal(err)
	}
	return sw
}

// until steps both sides a block and a Monero block at a time until stop says so; bobOn, when set, says whether the
// maker is online this round.
func (r *run) until(rounds int, stop func() bool, bobOn func() bool) {
	for i := 0; i < rounds; i++ {
		if stop() {
			return
		}
		if bobOn == nil || bobOn() {
			r.step("bob")
		}
		r.step("alice")
		r.chain.Advance(1)
		r.fw.Mine(1)
	}
	bp, _ := r.phase("bob")
	ap, _ := r.phase("alice")
	r.t.Fatalf("no end after %d rounds: bob %s, alice %s, height %d\n%s", rounds, bp, ap, r.chain.Height(), strings.Join(r.logs, "\n"))
}

func (r *run) bothDone() bool {
	_, b := r.phase("bob")
	_, a := r.phase("alice")
	return a && b
}

// --- Path 0: the stipend ---

// TestStipendIsPaidOnceBeforeTheXmrLock: the maker pays the taker's gas once, only after his own lock is stable,
// and her XMR leaves only after the stipend is in her wallet at stable depth (the run's transfer hook asserts the
// second at the moment of the payment).
func TestStipendIsPaidOnceBeforeTheXmrLock(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 101})
	r.drive()
	r.check()
	r.expectExit(LockClaimed)
	bob, alice := r.loadState("bob"), r.loadState("alice")
	ps := r.chain.paymentsTagged(bobAddr, aliceAddr, stipendPort(bob.HK))
	if len(ps) != 1 || ps[0].amount != testStipend || bob.Stipend.Tx != ps[0].txid || alice.Stipend.Tx != ps[0].txid {
		t.Fatalf("stipends %+v, bob recorded %q, alice %q", ps, bob.Stipend.Tx, alice.Stipend.Tx)
	}
	if ps[0].height < bob.Lock.LockedHeight+testStableDepth {
		t.Fatalf("the stipend was mined at %d, before the lock read at %d was stable", ps[0].height, bob.Lock.LockedHeight)
	}
}

// noPayDero is a maker whose wallet will not pay the stipend.
type noPayDero struct{ *memDero }

func (noPayDero) Pay(ctx context.Context, to string, amount, port uint64) (string, error) {
	return "", errors.New("the maker's wallet refuses the transfer")
}

// TestTakerLocksNoXmrWithoutTheStipend: a maker who locks and never pays the stipend gets no XMR locked against his
// lock. The taker waits while her budget lasts, then refuses; he refunds after t1.
func TestTakerLocksNoXmrWithoutTheStipend(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 103})
	r.bob = startHooked(r, "bob", noPayDero{newMemDero(r.chain, bobAddr)}, nil)
	r.alice = startWith(r, "alice", nil)
	r.until(600, r.bothDone, nil)
	alice := r.loadState("alice")
	if r.aliceW.transfers != 0 || alice.Phase != "refused" {
		t.Fatalf("alice %s with %d Monero transfers: %s", alice.Phase, r.aliceW.transfers, alice.Outcome)
	}
	if !hasLog(r, "waiting for the maker's stipend") {
		t.Fatalf("alice never said what she was waiting for:\n%s", strings.Join(r.logs, "\n"))
	}
	r.expectExit(LockRefunded)
}

// TestTakerRefusesAStipendBelowHerMinimum: the offer's stipend must cover what her Claim and a Punish may cost.
func TestTakerRefusesAStipendBelowHerMinimum(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 104})
	r.bob = startWith(r, "bob", func(c *Config) { c.Stipend = testStipend - 1 })
	r.alice = startWith(r, "alice", nil)
	driveUntil(r, "refused", 30)
	alice := r.loadState("alice")
	if !strings.Contains(alice.Outcome, "stipend") || len(r.chain.locks) != 0 {
		t.Fatalf("outcome %q, %d locks", alice.Outcome, len(r.chain.locks))
	}
}

// TestAnUnregisteredTakerIsRefusedBeforeAnyLock: a taker whose address never registered cannot receive the
// stipend; her own companion will not start, and a maker who finds her unregistered locks nothing.
func TestAnUnregisteredTakerIsRefusedBeforeAnyLock(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 105})
	r.chain.setUnregistered(aliceAddr, true)
	r.alice = startWith(r, "alice", nil)
	if _, err := r.alice.Step(r.ctx); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("an unregistered taker's companion started: %v", err)
	}
	if p, _ := r.phase("alice"); p != "" {
		t.Fatalf("an unregistered taker saved phase %q", p)
	}
	r.chain.setUnregistered(aliceAddr, false)
	r.bob = startWith(r, "bob", nil)
	for i := 0; i < 20; i++ {
		if p, _ := r.phase("bob"); p == "verified_pi_a" {
			break
		}
		r.step("bob")
		r.step("alice")
	}
	r.chain.setUnregistered(aliceAddr, true) // her account is gone from this chain by the time he would lock
	r.step("bob")
	bob := r.loadState("bob")
	if bob.Phase != "refused" || !strings.Contains(bob.Outcome, "not registered") || len(r.chain.locks) != 0 {
		t.Fatalf("bob %s (%s), %d locks", bob.Phase, bob.Outcome, len(r.chain.locks))
	}
}

// TestTheMakerChecksHisFundsBeforeLocking: a maker who cannot cover the lock, the stipend and his own fees locks
// nothing; once he can, he locks.
func TestTheMakerChecksHisFundsBeforeLocking(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 106})
	r.chain.setBalance(bobAddr, "", testDero+testStipend) // short of the fee reserve
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	for i := 0; i < 30; i++ {
		r.step("bob")
		r.step("alice")
		r.chain.Advance(1)
	}
	if bob := r.loadState("bob"); bob.Phase != "verified_pi_a" || len(r.chain.locks) != 0 || !hasLog(r, "nothing was locked") {
		t.Fatalf("bob %s, %d locks", bob.Phase, len(r.chain.locks))
	}
	r.chain.setBalance(bobAddr, "", 1<<40)
	driveUntil(r, "saw_dero_lock", 60)
	if len(r.chain.locks) != 1 {
		t.Fatalf("%d locks once the maker could pay", len(r.chain.locks))
	}
}

// --- U1: the asset leg ---

// TestAnAssetLegRunsEndToEnd: the DERO-side leg is an asset. The maker locks the asset (not DERO) once his wallet
// holds enough of it, the taker checks the asset in the lock against the terms, and the swap completes.
func TestAnAssetLegRunsEndToEnd(t *testing.T) {
	asset := strings.Repeat("9a", 32)
	withAsset := func(c *Config) { c.Asset, c.AssetDecimals = strings.ToUpper(asset), 5 } // capitals name the same asset
	r := newRun(t, plan{scenario: "happy", seed: 117})
	r.chain.setBalance(bobAddr, asset, testDero-1)
	r.bob = startWith(r, "bob", withAsset)
	r.alice = startWith(r, "alice", withAsset)
	for i := 0; i < 20; i++ {
		r.step("bob")
		r.step("alice")
		r.chain.Advance(1)
	}
	if len(r.chain.locks) != 0 || !hasLog(r, "of asset "+asset) {
		t.Fatalf("a maker short of the asset locked %d", len(r.chain.locks))
	}
	r.chain.setBalance(bobAddr, asset, testDero)
	r.drive()
	r.check()
	r.expectExit(LockClaimed)
	l, _ := r.chain.read(0)
	bob := r.loadState("bob")
	if l.Asset != asset || l.Amount != testDero || bob.Terms.Asset != asset || bob.Terms.Decimals != 5 || bob.Phase != "swept" {
		t.Fatalf("lock %+v, terms %+v, bob %s", l, bob.Terms, bob.Phase)
	}
}

// TestTheTakerRefusesAnotherAssetOrScale: the asset and its decimals are terms; an offer that differs from what the
// taker's companion was told is refused before anything is locked.
func TestTheTakerRefusesAnotherAssetOrScale(t *testing.T) {
	asset := strings.Repeat("9a", 32)
	for _, c := range []struct {
		name       string
		bob, alice func(*Config)
		want       string
	}{
		{"another asset", func(c *Config) { c.Asset = asset }, func(c *Config) { c.Asset = strings.Repeat("9b", 32) }, "offer refused"},
		{"DERO offered for an asset", nil, func(c *Config) { c.Asset = asset }, "offer refused"},
		{"another scale", func(c *Config) { c.Asset, c.AssetDecimals = asset, 5 }, func(c *Config) { c.Asset, c.AssetDecimals = asset, 8 }, "decimals"},
	} {
		r := newRun(t, plan{scenario: "happy", seed: 119})
		r.bob = startWith(r, "bob", c.bob)
		r.alice = startWith(r, "alice", c.alice)
		driveUntil(r, "refused", 30)
		if a := r.loadState("alice"); !strings.Contains(a.Outcome, c.want) || len(r.chain.locks) != 0 {
			t.Fatalf("%s: outcome %q, %d locks", c.name, a.Outcome, len(r.chain.locks))
		}
	}
}

// --- U2: the gate deadline ---

// TestAGateAfterTheDeadlineIsNotUsed: a maker who holds the gate back to t1 − G or later (the setup for suppressing
// her Claim until t1) gets no Claim at all. The taker waits for his refund, recovers her XMR with s_b, and he gets
// his DERO back and no XMR.
func TestAGateAfterTheDeadlineIsNotUsed(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 111})
	r.bob = startWith(r, "bob", nil)
	r.bob.cfg.GateMargin = 1 // a maker who releases the gate as late as he likes
	r.alice = startWith(r, "alice", nil)
	late := func() bool {
		t1 := r.loadState("bob").Lock.T1
		return !(r.stipendPaid() && r.chain.Height()+testGateDeadline < t1)
	}
	r.until(600, r.bothDone, late)
	bob, alice := r.loadState("bob"), r.loadState("alice")
	if n := r.alice.dero.(*memDero).calls["Claim"]; n != 0 || alice.ClaimTx != "" {
		t.Fatalf("alice sent %d Claims against a gate at or after t1 − G", n)
	}
	if !hasLog(r, "t1 − G") {
		t.Fatalf("alice did not say why she stopped:\n%s", strings.Join(r.logs, "\n"))
	}
	r.expectExit(LockRefunded)
	if bob.Phase != "refunded" || alice.Phase != "swept" || r.aliceW.transfers != 1 || alice.Revealed != bob.Own.Secret {
		t.Fatalf("bob %s, alice %s, transfers %d", bob.Phase, alice.Phase, r.aliceW.transfers)
	}
}

// --- U3: the Claim watched to stable depth ---

// TestTakerWatchesHerClaimUntilStable: after her Claim executes, the taker is not done until DERO can no longer
// replace its block. Here the block is replaced while she watches: the lock is open again, she claims again before
// the cutoff, and only the Claim that becomes stable ends her companion.
func TestTakerWatchesHerClaimUntilStable(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 113})
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	r.until(400, func() bool { p, _ := r.phase("alice"); return p == "claim_confirming" }, nil)
	alice := r.loadState("alice")
	if alice.Done || alice.Lock.ClaimSeenHeight == 0 || r.chain.StableHeight() >= alice.Lock.ClaimSeenHeight {
		t.Fatalf("alice done %v with her claim read at %d and the stable height %d", alice.Done, alice.Lock.ClaimSeenHeight, r.chain.StableHeight())
	}
	r.chain.unclaim(0) // the block holding her Claim is replaced before it is stable
	r.until(400, r.bothDone, nil)
	alice = r.loadState("alice")
	if n := r.alice.dero.(*memDero).calls["Claim"]; n != 2 || alice.Phase != "claimed" || !hasLog(r, "is not on the chain any more") {
		t.Fatalf("alice %s after %d Claims", alice.Phase, n)
	}
	r.expectExit(LockClaimed)
	r.check()
}

// --- U4: the code pin and lower-case hashes ---

// TestALookAlikeContractIsRefused: a contract whose code is not the pinned htlc.bas v2 gets nothing from either
// side — the maker locks nothing in it, the taker locks no XMR against it.
func TestALookAlikeContractIsRefused(t *testing.T) {
	t.Run("maker", func(t *testing.T) {
		r := newRun(t, plan{scenario: "happy", seed: 107})
		r.chain.mu.Lock()
		r.chain.codeHash = strings.Repeat("ee", 32)
		r.chain.mu.Unlock()
		r.bob = startWith(r, "bob", nil)
		r.alice = startWith(r, "alice", nil)
		for i := 0; i < 20; i++ {
			r.step("bob")
			r.step("alice")
			r.chain.Advance(1)
		}
		if bob := r.loadState("bob"); bob.Phase != "refused" || !strings.Contains(bob.Outcome, "not htlc.bas v2") || len(r.chain.locks) != 0 {
			t.Fatalf("bob %s (%s), %d locks", bob.Phase, bob.Outcome, len(r.chain.locks))
		}
	})
	t.Run("taker", func(t *testing.T) {
		r := newRun(t, plan{scenario: "happy", seed: 108})
		look := newMemDero(r.chain, aliceAddr)
		look.code = strings.Repeat("ee", 32) // what her node says the code at the SCID hashes to
		r.bob = startWith(r, "bob", nil)
		r.alice = startHooked(r, "alice", look, nil)
		r.until(600, r.bothDone, nil)
		alice := r.loadState("alice")
		if alice.Phase != "refused" || !strings.Contains(alice.Outcome, "not htlc.bas v2") || r.aliceW.transfers != 0 {
			t.Fatalf("alice %s (%s), %d transfers", alice.Phase, alice.Outcome, r.aliceW.transfers)
		}
		r.expectExit(LockRefunded)
	})
}

// TestUpperCaseHashesAreRefused (attack D): htlc.bas compares lower-case hex, so a hash in capitals can never be
// matched. The companion refuses one wherever it takes a hash, and the taker refuses a maker whose H_k is one.
func TestUpperCaseHashesAreRefused(t *testing.T) {
	low := SHA256Hex([]byte("x"))
	oneCapital := low
	for i, c := range low {
		if c >= 'a' && c <= 'f' { // a leading digit has no capital: change the first letter
			oneCapital = low[:i] + strings.ToUpper(string(c)) + low[i+1:]
			break
		}
	}
	for _, c := range []struct {
		h  string
		ok bool
	}{{low, true}, {strings.ToUpper(low), false}, {oneCapital, false}, {strings.Repeat("zz", 32), false}, {low[:63], false}} {
		if isHash(c.h) != c.ok {
			t.Fatalf("isHash(%q) = %v", c.h, !c.ok)
		}
	}
	r := newRun(t, plan{scenario: "happy", seed: 109})
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	for i := 0; i < 10; i++ {
		if p, _ := r.phase("alice"); p == "offer_checked" {
			break
		}
		r.step("bob")
		r.step("alice")
	}
	// a maker's setup whose proof verifies and whose H_k is in capitals
	prover := &stubProver{}
	sec, h, s, _ := prover.Keygen(r.ctx)
	_, _, proof, _ := prover.Prove(r.ctx, sec)
	body, _ := json.Marshal(bobSetupBody{S: s.Hex(), H: h, Proof: hex.EncodeToString(proof), HK: strings.ToUpper(SHA256Hex([]byte("k")))})
	ch, err := NewDirChannel(filepath.Join(r.dir, "channel"), "bob")
	if err != nil {
		t.Fatal(err)
	}
	if err := ch.Send(r.ctx, "alice", Message{Kind: KindBobSetup, Body: body}); err != nil {
		t.Fatal(err)
	}
	r.step("alice")
	if a := r.loadState("alice"); a.Phase != "refused" || !strings.Contains(a.Outcome, "lower-case") {
		t.Fatalf("alice %s: %s", a.Phase, a.Outcome)
	}
}

// --- LIV-07: an abandoned lock closes itself ---

// TestMakerClosesAnAbandonedLockWithNoHumanAction: the maker misses his whole refund window and the taker, who
// locked XMR, never comes back to punish. Under htlc.bas v1 that lock stayed open for ever and the maker's companion
// waited on it for ever. Now his companion, running unattended, closes it after t2 + CloseAfter with Punish, which
// pays the taker; the taker's companion, whenever it runs again, reads that she was paid.
func TestMakerClosesAnAbandonedLockWithNoHumanAction(t *testing.T) {
	r := newRun(t, plan{scenario: "punish", seed: 115})
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	r.until(400, func() bool { p, _ := r.phase("alice"); return p == "xmr_locked" }, func() bool { return !r.stipendPaid() })
	t2 := r.loadState("bob").Lock.T2
	for r.chain.Height() < t2+1 {
		r.chain.Advance(1) // nobody runs: the maker's window passes, and the taker is gone
	}
	for i := 0; i < 40; i++ {
		if _, done := r.phase("bob"); done {
			break
		}
		r.step("bob")
		r.chain.Advance(1)
	}
	bob := r.loadState("bob")
	l, _ := r.chain.read(bob.Lock.ID)
	if l.State != LockPunished || bob.Phase != "punished" || bob.PunishTx == "" || !strings.Contains(bob.Outcome, "closed the abandoned lock") {
		t.Fatalf("lock state %d, bob %s: %s", l.State, bob.Phase, bob.Outcome)
	}
	if n := r.bob.dero.(*memDero).calls["Punish"]; n != 1 {
		t.Fatalf("the maker sent %d Punish calls", n)
	}
	for i := 0; i < 5; i++ {
		r.step("alice")
	}
	if a := r.loadState("alice"); a.Phase != "punished" {
		t.Fatalf("the taker ended in %s", a.Phase)
	}
}

// --- the replay rule ---

// TestALockAlreadySeenFailsClosed: a lock already accepted is never accepted again. A second taker in the same
// directory, announced the first swap's lock, refuses it (its hashes are the first swap's); and a lock whose
// identity the register already holds for another swap is refused even where every field would pass.
func TestALockAlreadySeenFailsClosed(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 121})
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	driveUntil(r, "saw_dero_lock", 200) // swap A's taker has accepted lock 0
	b2 := startSide(r, "bob", "bob2.json", "channel2", newMemDero(r.chain, bobAddr), &countingWallet{Wallet: r.w}, nil)
	a2 := startSide(r, "alice", "alice2.json", "channel2", newMemDero(r.chain, aliceAddr), &countingWallet{Wallet: r.w}, nil)
	for i := 0; i < 20 && a2.Phase() != "verified_pi_b"; i++ {
		if b2.Phase() != "verified_pi_a" {
			b2.Step(r.ctx)
		}
		a2.Step(r.ctx)
	}
	if a2.Phase() != "verified_pi_b" {
		t.Fatalf("swap B's taker is in %s", a2.Phase())
	}
	ch, err := NewDirChannel(filepath.Join(r.dir, "channel2"), "bob")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(deroLockedBody{ID: 0})
	if err := ch.Send(r.ctx, "alice", Message{Kind: KindDeroLocked, Body: body}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3 && !a2.State().Done; i++ {
		a2.Step(r.ctx)
	}
	if st := a2.State(); st.Phase != "refused" || !strings.Contains(st.Outcome, "ha is not") {
		t.Fatalf("swap B's taker, announced swap A's lock: %s (%s)", st.Phase, st.Outcome)
	}

	// swap C: the register already holds, for another swap, the lock swap C's maker is about to make (id 1)
	used, err := LoadUsed(UsedPath(r.statePath("x")))
	if err != nil {
		t.Fatal(err)
	}
	if err := used.Record([]byte("lock|alice|"+testHTLC+"#1"), "feedfacefeedface", "alice lock"); err != nil {
		t.Fatal(err)
	}
	aw := &countingWallet{Wallet: r.w}
	b3 := startSide(r, "bob", "bob3.json", "channel3", newMemDero(r.chain, bobAddr), &countingWallet{Wallet: r.w}, nil)
	a3 := startSide(r, "alice", "alice3.json", "channel3", newMemDero(r.chain, aliceAddr), aw, nil)
	for i := 0; i < 200 && !a3.State().Done; i++ {
		b3.Step(r.ctx)
		a3.Step(r.ctx)
		r.chain.Advance(1)
		r.fw.Mine(1)
	}
	if st := a3.State(); st.Phase != "refused" || !strings.Contains(st.Outcome, "feedfacefeedface") || aw.transfers != 0 || b3.State().Lock.ID != 1 {
		t.Fatalf("swap C's taker: %s (%s), %d transfers, lock %d", st.Phase, st.Outcome, aw.transfers, b3.State().Lock.ID)
	}
}

// replayWallet is a taker's wallet that offers an old payment's txid for a new lock and pays nothing.
type replayWallet struct {
	Wallet
	txid string
}

func (w *replayWallet) Transfer(ctx context.Context, to string, amount uint64) (string, error) {
	return w.txid, nil
}

// TestOneMoneroPaymentOpensOneGate: one Monero payment opens at most one gate. The payment swap A's gate counted
// is swap A's maker's in the register, and no other swap's maker may count it; and a taker who offers the same
// payment for a second swap, paying nothing, gets no gate — the second maker reads his own joint address, finds
// nothing, and refunds.
func TestOneMoneroPaymentOpensOneGate(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 123})
	r.drive()
	r.check()
	r.expectExit(LockClaimed)
	bob, alice := r.loadState("bob"), r.loadState("alice")
	payment := alice.Xmr.LockTx
	used, err := LoadUsed(UsedPath(r.statePath("bob")))
	if err != nil {
		t.Fatal(err)
	}
	if err := used.Record([]byte("xmr-in|bob|"+payment+"|"+bob.Joint), "feedfacefeedface", "bob xmr-in"); !errors.Is(err, ErrSecretReused) {
		t.Fatalf("the payment swap A's gate counted was taken by another swap: %v", err)
	}
	b2 := startSide(r, "bob", "bob2.json", "channel2", newMemDero(r.chain, bobAddr), &countingWallet{Wallet: r.w}, nil)
	a2 := startSide(r, "alice", "alice2.json", "channel2", newMemDero(r.chain, aliceAddr), &replayWallet{Wallet: r.w, txid: payment}, nil)
	for i := 0; i < 600 && !(b2.State().Done && a2.State().Done); i++ {
		b2.Step(r.ctx)
		a2.Step(r.ctx)
		r.chain.Advance(1)
		r.fw.Mine(1)
	}
	b, a := b2.State(), a2.State()
	l, _ := r.chain.read(b.Lock.ID)
	if b.Phase != "refunded" || l.State != LockRefunded || a.Gate != "" || a.Xmr.LockTx != payment || b.Joint == bob.Joint {
		t.Fatalf("swap B: bob %s, lock %d, alice gate %q, offered %q", b.Phase, l.State, a.Gate, a.Xmr.LockTx)
	}
}
