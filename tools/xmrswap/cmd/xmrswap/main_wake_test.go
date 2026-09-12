package main

import (
	"errors"
	"strings"
	"testing"

	"veil/tools/xmrswap"
)

// TestAFinishedSwapRemovesItsWake (MT-B15, MT-J13): when the swap is done, the companion removes both wake tasks its
// recovery card registered — the first window's and t2's, that swap's and no other's — and says so for each the OS
// would not let it remove.
func TestAFinishedSwapRemovesItsWake(t *testing.T) {
	old := removeWake
	defer func() { removeWake = old }()
	var removed []string
	removeWake = func(name string) error { removed = append(removed, name); return nil }
	st := &xmrswap.State{SwapID: "5ea1ed", Outcome: "claimed the DERO"}
	var logged []string
	logf := func(f string, a ...interface{}) { logged = append(logged, f) }
	endSwap("alice", st, logf)
	want := xmrswap.WakeTaskNames("5ea1ed")
	if len(removed) != 2 || removed[0] != want[0] || removed[1] != want[1] || want[0] != xmrswap.WakeTaskName("5ea1ed") || len(logged) != 0 {
		t.Fatalf("removed %v, logged %v", removed, logged)
	}
	removeWake = func(string) error { return errors.New("access denied") }
	endSwap("alice", st, logf)
	if len(logged) != 2 || !strings.Contains(logged[0], "was not removed") {
		t.Fatalf("wakes that could not be removed were not said: %v", logged)
	}
}
