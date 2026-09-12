package xmrswap

// MT-B14 (review R6): the gate opens on no output whose transaction carries an unlock_time.
//
// monero 0.18.5.1: wallet2 unlocks an output only once both its own unlock_time has passed (a height below
// CRYPTONOTE_MAX_BLOCK_NUMBER = 500,000,000, cryptonote_config.h:40, else a unix time) and it is
// CRYPTONOTE_DEFAULT_TX_SPENDABLE_AGE = 10 blocks deep (cryptonote_config.h:49; wallet2.cpp:7308-7344,
// is_transfer_unlocked and is_tx_spendtime_unlocked), and get_transfers reports that as "locked"
// (wallet_rpc_server.cpp:498, 517). An honest 0.18.5.1 wallet builds no transfer with an unlock_time at all
// (wallet2.cpp:7826; the RPC's -50, wallet_rpc_server.cpp:1250), so one at the joint address is another program's, or
// a coinbase's, whose unlock_time is its height + CRYPTONOTE_MINED_MONEY_UNLOCK_WINDOW = 60 (cryptonote_config.h:44).
// Whatever its value it opens no gate: Bob refunds at t1, and Alice recovers what is there once it unlocks — and when
// it will not unlock in time, her half of the key stays in her state file.

import "testing"

func TestAFarLockedOutputOpensNoGate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		unlock func(height uint64) uint64
		// the lock unlocks within the run, so alice's recovery after the refund finds it spendable
		recovers bool
	}{
		{"a coinbase's lock, its height + 60", func(h uint64) uint64 { return h + 60 }, false},
		{"an unlock_time already passed", func(uint64) uint64 { return 1 }, true},
		{"a far-future height", func(uint64) uint64 { return 1 << 20 }, false},
		{"a far-future time", func(uint64) uint64 { return 4_102_444_800 }, false}, // 2100-01-01
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRun(t, plan{scenario: "happy", seed: 241})
			check := r.aliceW.onTransfer
			r.aliceW.onTransfer = func(to string, amount uint64) error {
				r.fw.SetUnlockTime(tc.unlock(r.fw.Height())) // the lock alice's transfer carries
				return check(to, amount)
			}
			r.drive()
			r.check()
			r.expectExit(LockRefunded)
			bob, alice := r.loadState("bob"), r.loadState("alice")
			if r.gateSends != 0 || bob.Phase != "refunded" {
				t.Fatalf("gate sends %d, bob %s: the gate opened on a locked output", r.gateSends, bob.Phase)
			}
			if !hasLog(r, "carries unlock_time") {
				t.Fatal("bob did not say why the gate stayed shut")
			}
			switch {
			case tc.recovers && alice.Phase != "swept":
				t.Fatalf("alice ended %s (%s): the output was spendable after the refund", alice.Phase, alice.Outcome)
			case !tc.recovers && alice.Phase != "swept" && (alice.Phase != "sweep_unknown" || alice.Own.Secret == "" || alice.Revealed == ""):
				t.Fatalf("alice ended %s without her key and bob's share kept: %s", alice.Phase, alice.Outcome)
			}
			t.Logf("bob refunded; alice %s", alice.Phase)
		})
	}
}
