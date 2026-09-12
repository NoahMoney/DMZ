package xmrswap

// The review of 2026-09-12, finding 1: htlc.bas takes a second Lock under the same hashes as a second lock, which the one
// s_a and k_b open, so the maker must never send one while DERO could still mine the first. Each test here holds the
// maker's Lock in the fake chain's mempool (fakeChain.poolDelay), where no lookup can see it.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// oneLockAtMost fails the test the moment the chain holds, or may still mine, a second lock for the swap.
func oneLockAtMost(t *testing.T, r *run) {
	t.Helper()
	r.chain.mu.Lock()
	locks, sent := len(r.chain.locks), len(r.chain.sentAt)
	r.chain.mu.Unlock()
	if pooled := r.chain.inPool(); locks+pooled > 1 {
		t.Fatalf("%d locks on the chain and %d in the pool after %d Lock transactions: a second lock under the same hashes\n%s", locks, pooled, sent, strings.Join(r.logs, "\n"))
	}
}

// TestARestartWithTheLockInThePoolSendsNoSecond is the review's scenario: the intent at H, the Lock sent at H + 1, the
// process killed while it waits for the Lock to be mined, and a restart three blocks later with the Lock still in the
// pool. The old wait, LockSettleBlocks from the intent, is over by then, and the old companion sent a second Lock. This
// one finds its attempt on disk, only looks at the chain until the first Lock lands, and sends nothing more.
func TestARestartWithTheLockInThePoolSendsNoSecond(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 501, bobCrash: map[string]bool{"lock_sent": true}})
	r.chain.flaky = nil
	r.chain.mu.Lock()
	r.chain.poolDelay = func() uint64 { return DeroTxValidity - 1 } // mined in the last block DERO allows
	r.chain.mu.Unlock()
	for i := 0; ; i++ {
		if i > 100 {
			t.Fatalf("bob never sent his Lock\n%s", strings.Join(r.logs, "\n"))
		}
		r.step("bob")
		if r.bob == nil {
			break // killed at lock_sent
		}
		r.step("alice")
		r.chain.Advance(1)
	}
	st := r.loadState("bob")
	a := st.Lock.AttemptHeight
	if st.Phase != "dero_locking" || a == 0 || r.chain.inPool() != 1 || len(r.chain.locks) != 0 {
		t.Fatalf("killed in %s with attempt %d, %d Locks in the pool and %d on the chain", st.Phase, a, r.chain.inPool(), len(r.chain.locks))
	}
	for r.chain.Height() < a+3 {
		r.chain.Advance(1)
	}
	if old := st.Lock.IntentHeight + r.config("bob").LockSettleBlocks; r.chain.Height() < old {
		t.Fatalf("not the review's scenario: the restart at %d is inside the old wait, which ended at %d", r.chain.Height(), old)
	}
	r.until(600, func() bool { oneLockAtMost(t, r); return r.bothDone() }, nil)
	r.check()
	r.expectExit(LockClaimed)
	if n := len(r.chain.sentAt); n != 1 || !hasLog(r, "DERO could still mine it") {
		t.Fatalf("%d Lock transactions sent across the restart, want the one", n)
	}
}

// TestALockLeftInTheMempoolIsNotResentInsideItsWindow: no restart. The client stops waiting for its Lock and returns an
// error, as it does when derod drops the transaction from its own pool while another node still holds it, and the Lock
// is mined in the last block DERO allows. Until then the maker only looks, and the swap ends on the one lock.
func TestALockLeftInTheMempoolIsNotResentInsideItsWindow(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 502})
	r.chain.flaky = nil
	r.chain.mu.Lock()
	r.chain.poolDelay = func() uint64 { return DeroTxValidity - 1 }
	r.chain.mu.Unlock()
	r.until(600, func() bool { oneLockAtMost(t, r); return r.bothDone() }, nil)
	r.check()
	r.expectExit(LockClaimed)
	if n := len(r.chain.sentAt); n != 1 {
		t.Fatalf("%d Lock transactions sent, want one", n)
	}
}

// TestALockThatNeverLandsIsResentOnlyAfterItsWindow: the first Lock is dropped from every mempool and never mined. The
// maker cannot know that until DERO could no longer mine it anywhere: only once every block it could land in is stable,
// with no lock of the swap's on the chain, does a second go out, and the swap ends on that one.
func TestALockThatNeverLandsIsResentOnlyAfterItsWindow(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 503})
	r.chain.flaky = nil
	sends := 0
	r.chain.mu.Lock()
	r.chain.dropPooled = true
	r.chain.poolDelay = func() uint64 {
		sends++
		if sends == 1 {
			return DeroTxValidity - 1 // pooled, and dropped
		}
		return 0
	}
	r.chain.mu.Unlock()
	r.until(800, func() bool { oneLockAtMost(t, r); return r.bothDone() }, nil)
	r.check()
	r.expectExit(LockClaimed)
	r.chain.mu.Lock()
	sentAt := append([]uint64(nil), r.chain.sentAt...)
	r.chain.mu.Unlock()
	if len(sentAt) != 2 {
		t.Fatalf("Lock transactions sent at %v, want the dropped one and one more", sentAt)
	}
	end := sentAt[0] + r.config("bob").LockSettleBlocks + DeroTxValidity
	if stableAtResend := sentAt[1] - testStableDepth; stableAtResend < end {
		t.Fatalf("the second Lock went out at height %d, with the stable height %d below %d, the last block the first could be mined in", sentAt[1], stableAtResend, end)
	}
	t.Logf("first Lock sent at %d and dropped; the second at %d, once %d was stable", sentAt[0], sentAt[1], end)
}

// TestAStateFromAnEarlierBuildWaitsOutItsLock: a state an earlier build saved in dero_locking records no attempt, because
// that build recorded one only after a Lock call returned an error, and its process died while its Lock sat in the pool.
// Restarted three blocks later, this build takes its own height as the attempt's and sends nothing while that Lock can
// still land; it lands, and the swap goes on with it.
func TestAStateFromAnEarlierBuildWaitsOutItsLock(t *testing.T) {
	ctx := context.Background()
	r := newRun(t, plan{scenario: "happy", seed: 504})
	r.chain.flaky = nil
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	for i := 0; i < 60; i++ {
		if p, _ := r.phase("bob"); p == "dero_locking" {
			break
		}
		r.step("bob")
		r.step("alice")
		r.chain.Advance(1)
	}
	st := r.loadState("bob")
	if st.Phase != "dero_locking" {
		t.Fatalf("bob never reached dero_locking: %s", st.Phase)
	}
	// the earlier build's Lock, in the pool, and its state, which recorded no attempt
	r.chain.mu.Lock()
	r.chain.poolDelay = func() uint64 { return DeroTxValidity - 1 }
	r.chain.mu.Unlock()
	if _, _, err := newMemDero(r.chain, bobAddr).Lock(ctx, st.Peer.H, st.HK, st.Own.H, st.PeerDero, st.Lock.T1, st.Lock.T2, st.Terms.Asset, st.Lock.Amount, noCutoff); !errors.Is(err, errNotMined) {
		t.Fatalf("the earlier build's Lock: %v", err)
	}
	r.chain.mu.Lock()
	r.chain.poolDelay = nil
	r.chain.mu.Unlock()
	st.Lock.AttemptHeight, st.Lock.AttemptsRecorded = 0, false
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		r.chain.Advance(1)
	}
	r.bob = startWith(r, "bob", nil)
	r.until(600, func() bool { oneLockAtMost(t, r); return r.bothDone() }, nil)
	r.check()
	r.expectExit(LockClaimed)
	if n := len(r.chain.sentAt); n != 1 {
		t.Fatalf("%d Lock transactions, want the earlier build's one", n)
	}
}

// TestTheDeroClientSendsNoSecondLockWhileTheFirstCanStillBeMined: the client's own guard, under the state machine's. A
// Lock stuck in the daemon's pool stops every later call from sending; once the daemon drops it, the client still sends
// nothing until every block it could have been mined in elsewhere is stable, and then one Lock goes out. A call refused
// at the estimate says it sent nothing (ErrLockNotSent), so the state machine withdraws its attempt.
func TestTheDeroClientSendsNoSecondLockWhileTheFirstCanStillBeMined(t *testing.T) {
	ctx := context.Background()
	chain := newFakeChain(1000)
	bob := &fakeDaemon{chain: chain, addr: bobAddr}
	cb := newClient(t, bob)
	cb.Timeout = 50 * time.Millisecond
	_, ha := secretPair(t)
	hk, hb := SHA256Hex(mustHex(strings.Repeat("11", 32))), SHA256Hex(mustHex(strings.Repeat("22", 32)))
	lock := func() (string, uint64, error) {
		return cb.Lock(ctx, ha, hk, hb, aliceAddr, 1050, 1100, "", 5000, noCutoff)
	}
	sends := func() int { return bob.count("transfer") }
	chain.mu.Lock()
	chain.stuckBelowFee = 1 << 40
	sentAt := chain.height
	chain.mu.Unlock()
	txid, _, err := lock()
	if err == nil || txid == "" || !errors.Is(err, errNotMined) || lockNotSent(err) || sends() != 1 {
		t.Fatalf("the first Lock: tx %q, %v, %d transfers", txid, err, sends())
	}
	if _, _, err := lock(); err == nil || !strings.Contains(err.Error(), "still in the mempool") || lockNotSent(err) || sends() != 1 {
		t.Fatalf("with the first in the pool: %v, %d transfers", err, sends())
	}
	// derod drops it from its own pool; another node may still mine it until its validity runs out
	chain.mu.Lock()
	delete(chain.pool, txid)
	chain.stuckBelowFee = 0
	chain.mu.Unlock()
	for chain.StableHeight() < sentAt+DeroTxValidity {
		if _, _, err := lock(); err == nil || !strings.Contains(err.Error(), "may still mine it") || lockNotSent(err) || sends() != 1 {
			t.Fatalf("at stable height %d, with the dropped Lock still mineable until %d: %v, %d transfers", chain.StableHeight(), sentAt+DeroTxValidity, err, sends())
		}
		chain.Advance(1)
	}
	tx2, id, err := lock()
	if err != nil || tx2 == "" || tx2 == txid || sends() != 2 || len(chain.locks) != 1 || id != chain.locks[0].ID {
		t.Fatalf("once the dropped Lock can no longer land: tx %q id %d, %v, %d transfers, %d locks", tx2, id, err, sends(), len(chain.locks))
	}
	if _, again, err := lock(); err != nil || again != id || sends() != 2 {
		t.Fatalf("the lookup after the lock: id %d, %v, %d transfers", again, err, sends())
	}
	// an estimate the daemon refuses (t1 inside w1min) sent nothing, and says so
	_, ha2 := secretPair(t)
	ha2 = SHA256Hex([]byte(ha2))
	if _, _, err := cb.Lock(ctx, ha2, hk, hb, aliceAddr, 5, 100, "", 5000, noCutoff); !errors.Is(err, ErrLockNotSent) || sends() != 2 {
		t.Fatalf("a Lock refused at the estimate: %v, %d transfers", err, sends())
	}
}
