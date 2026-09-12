package xmrswap

// MT-B12 / XMR-R1: a companion killed during its sweep resumes it. Before, a restart that found the durable intent
// "sweeping" ended the swap in sweep_unknown, whether or not anything had gone out, and left the XMR for the
// operator to recover by hand from the spend key in the state file.

import "testing"

func TestASweepInterruptedByAKillIsResumed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		scenario string
		role     string
		at       string
	}{
		{"bob killed after the intent, before anything went out", "happy", "bob", "sweeping"},
		{"bob killed after the sweep went out, before it was recorded", "happy", "bob", "sweep_sent"},
		{"alice killed after her recovery sweep went out, before it was recorded", "refund", "alice", "sweep_sent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := plan{scenario: tc.scenario, seed: 191}
			if tc.role == "bob" {
				p.bobCrash = map[string]bool{tc.at: true}
			} else {
				p.aliceCrash = map[string]bool{tc.at: true}
			}
			r := newRun(t, p)
			r.drive()
			r.check()
			st := r.loadState(tc.role)
			w, to := r.bobW, r.bobXmr
			if tc.role == "alice" {
				w, to = r.aliceW, r.aliceXmr
			}
			if st.Phase != "swept" {
				t.Fatalf("%s ended %s: %s", tc.role, st.Phase, st.Outcome)
			}
			// one sweep reached the chain, and the companion called sweep_all exactly once: the resume found the
			// sweep that went out instead of making a second, and made the one that had not
			if n, amt := r.paidTo(st.Joint, to); n != 1 || amt != testXmr-FakeFee {
				t.Fatalf("%d payments of %d from the joint address to %s's own", n, amt, tc.role)
			}
			if w.sweeps != 1 || len(st.Sweep.Txs) != 1 {
				t.Fatalf("sweep_all called %d times, %v recorded", w.sweeps, st.Sweep.Txs)
			}
			restarts := r.bobRestarts
			if tc.role == "alice" {
				restarts = r.aliceRestarts
			}
			if restarts < 2 {
				t.Fatalf("%s was never killed at %s (%d starts)", tc.role, tc.at, restarts)
			}
			t.Logf("%s: %s", tc.role, st.Outcome)
		})
	}
}
