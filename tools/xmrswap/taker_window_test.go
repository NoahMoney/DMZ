package xmrswap

// X-23: the taker refuses a t2 − t1 that leaves the maker no height to refund at (MT-B27).

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheTakerRefusesAWindowWithNoRefundHeight (X-23, MT-B27). The maker's Refund goes out only below t2 − 11: one
// built at h executes by h + 10. So a t2 − t1 of 11 or less leaves him no height to refund at, and leaves her XMR no
// way back but a Punish or his Reclaim. His own companion refuses to offer such a window. Hers refuses one offered by a
// maker whose companion does not, even under a T2Min set below it: at the offer, and in the lock read from the chain,
// where her floor is raised to 12. A window of 12 passes the offer.
func TestTheTakerRefusesAWindowWithNoRefundHeight(t *testing.T) {
	for _, c := range []struct {
		t2      uint64
		refused bool
	}{{DeroTxValidity, true}, {1, true}, {DeroTxValidity + 1, false}} {
		r := newRun(t, plan{scenario: "happy", seed: 191})
		r.alice = startWith(r, "alice", func(cf *Config) { cf.T2Min = 1 })
		if r.alice.cfg.T2Min != DeroTxValidity+1 {
			t.Fatalf("the taker's floor on t2 − t1 is %d, want %d", r.alice.cfg.T2Min, DeroTxValidity+1)
		}
		// a maker's offer as another program would send it: every term hers but the window
		b, err := json.Marshal(offerBody{AmountDero: testDero, AmountXmr: testXmr, Stipend: testStipend, DeroNet: "simulator", Net: Stagenet.String(),
			HTLC: testHTLC, T1Delta: testT1Delta, T2Delta: c.t2})
		if err != nil {
			t.Fatal(err)
		}
		ch, err := NewDirChannel(filepath.Join(r.dir, "channel"), "bob")
		if err != nil {
			t.Fatal(err)
		}
		if err := ch.Send(r.ctx, "alice", Message{Kind: KindOffer, Body: b}); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 4; i++ {
			r.step("alice")
		}
		alice := r.loadState("alice")
		switch {
		case c.refused && (alice.Phase != "refused" || !strings.Contains(alice.Outcome, "no height at which a Refund can go out")):
			t.Fatalf("t2 − t1 = %d: alice %s (%s)", c.t2, alice.Phase, alice.Outcome)
		case !c.refused && alice.Phase != "offer_checked":
			t.Fatalf("t2 − t1 = %d: alice %s (%s), want the offer accepted", c.t2, alice.Phase, alice.Outcome)
		}
		for _, m := range r.sent {
			if c.refused && m.Kind == KindAliceSetup {
				t.Fatalf("t2 − t1 = %d: alice answered an offer she refused", c.t2)
			}
		}
	}
	// the lock read from the chain: the raised floor refuses 11 whatever the offer said
	e := LockExpect{HA: "a", HK: "k", HB: "b", TakerRaw: "t", Amount: 1, T1Min: 1, T2Min: DeroTxValidity + 1}
	lock := Lock{HA: "a", HK: "k", HB: "b", Taker: "t", Amount: 1, T1: 1000, State: LockOpen}
	for t2, ok := range map[uint64]bool{1000 + DeroTxValidity: false, 1000 + DeroTxValidity + 1: true} {
		lock.T2 = t2
		if err := CheckLock(lock, 900, e); (err == nil) != ok {
			t.Fatalf("a lock with t2 − t1 = %d: %v", t2-1000, err)
		}
	}
}
