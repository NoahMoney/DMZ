package xmrswap

import (
	"strings"
	"testing"
)

func diverged(rep Report, want string) bool {
	for _, d := range rep.Diverged {
		if strings.Contains(d, want) {
			return true
		}
	}
	return false
}

// TestStatusFlagsDivergence (BL-26): the status report agrees with a swap whose state and chain agree, and names each
// way they can part: a state that says open where the chain says refunded, a lock whose fields are not the ones
// recorded, a joint address that has received less than the swap acted on (for either party), and a lock the
// contract does not have.
func TestStatusFlagsDivergence(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 125})
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	driveUntil(r, "gate_received", 300)
	bob, alice := r.loadState("bob"), r.loadState("alice")
	bd, ad := newMemDero(r.chain, bobAddr), newMemDero(r.chain, aliceAddr)

	for _, c := range []struct {
		name string
		st   *State
		d    Dero
	}{{"bob", bob, bd}, {"alice", alice, ad}} {
		if rep := Reconcile(r.ctx, c.st, c.d, r.w); len(rep.Diverged) != 0 {
			t.Fatalf("%s: a consistent swap diverged: %v\n%s", c.name, rep.Diverged, strings.Join(rep.Lines, "\n"))
		}
	}

	r.chain.mu.Lock()
	r.chain.locks[bob.Lock.ID].State = LockRefunded
	r.chain.mu.Unlock()
	if rep := Reconcile(r.ctx, bob, bd, nil); !diverged(rep, "the chain says lock 0 is refunded") {
		t.Fatalf("state open, chain refunded: %v", rep.Diverged)
	}
	r.chain.mu.Lock()
	r.chain.locks[bob.Lock.ID].State = LockOpen
	r.chain.mu.Unlock()

	moved := *bob
	moved.Lock.T1++
	if rep := Reconcile(r.ctx, &moved, bd, nil); !diverged(rep, "t1") {
		t.Fatalf("a lock with another t1: %v", rep.Diverged)
	}
	short := *bob
	short.Terms.AmountXmr = 2 * testXmr
	if rep := Reconcile(r.ctx, &short, bd, r.w); !diverged(rep, "has received") {
		t.Fatalf("the maker's joint address short of what he acted on: %v", rep.Diverged)
	}
	shortA := *alice
	shortA.Terms.AmountXmr = 2 * testXmr
	if rep := Reconcile(r.ctx, &shortA, ad, r.w); !diverged(rep, "has received") {
		t.Fatalf("the taker's joint address short of what she acted on: %v", rep.Diverged)
	}
	gone := *bob
	gone.Lock.ID = 99
	if rep := Reconcile(r.ctx, &gone, bd, nil); !diverged(rep, "no such lock") {
		t.Fatalf("a lock the contract does not have: %v", rep.Diverged)
	}
	t.Logf("status of the maker:\n  %s", strings.Join(Reconcile(r.ctx, bob, bd, r.w).Lines, "\n  "))
}
