package main

import (
	"errors"
	"strings"
	"testing"

	"veil/tools/xmrswap"
)

// TestAFinishedSwapRemovesItsWake (MT-B15, MT-J13, MT-B28): when the swap is done, the companion removes both wake
// tasks its recovery card registered — the taker's first window's and t2's, the maker's t1's and t2 + g2's, that swap's
// and no other's — and says so for each the OS would not let it remove.
func TestAFinishedSwapRemovesItsWake(t *testing.T) {
	old := removeWake
	defer func() { removeWake = old }()
	for _, role := range []string{"alice", "bob"} {
		var removed []string
		removeWake = func(name string) error { removed = append(removed, name); return nil }
		st := &xmrswap.State{SwapID: "5ea1ed", Outcome: "done"}
		var logged []string
		logf := func(f string, a ...interface{}) { logged = append(logged, f) }
		endSwap(role, st, logf)
		want := xmrswap.WakeTaskNames(role, "5ea1ed")
		if len(removed) != 2 || removed[0] != want[0] || removed[1] != want[1] || want[0] != xmrswap.WakeTaskName("5ea1ed") || len(logged) != 0 {
			t.Fatalf("%s: removed %v, logged %v", role, removed, logged)
		}
		removeWake = func(string) error { return errors.New("access denied") }
		endSwap(role, st, logf)
		if len(logged) != 2 || !strings.Contains(logged[0], "was not removed") {
			t.Fatalf("%s: wakes that could not be removed were not said: %v", role, logged)
		}
	}
	// the maker's second wake is his Reclaim's, not the taker's t2
	if a, b := xmrswap.WakeTaskNames("alice", "5ea1ed"), xmrswap.WakeTaskNames("bob", "5ea1ed"); a[1] == b[1] || !strings.HasSuffix(b[1], "-reclaim") {
		t.Fatalf("the taker's wakes %v and the maker's %v", a, b)
	}
}
