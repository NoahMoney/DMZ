package main

import (
	"errors"
	"strings"
	"testing"

	"veil/tools/xmrswap"
)

// TestAFinishedSwapRemovesItsWake (MT-B15): when the swap is done, the companion removes the wake task its recovery
// card registered — that swap's and no other — and says so if the OS would not let it.
func TestAFinishedSwapRemovesItsWake(t *testing.T) {
	old := removeWake
	defer func() { removeWake = old }()
	var removed []string
	removeWake = func(name string) error { removed = append(removed, name); return nil }
	st := &xmrswap.State{SwapID: "5ea1ed", Outcome: "claimed the DERO"}
	var logged []string
	logf := func(f string, a ...interface{}) { logged = append(logged, f) }
	endSwap("alice", st, logf)
	if len(removed) != 1 || removed[0] != xmrswap.WakeTaskName("5ea1ed") || len(logged) != 0 {
		t.Fatalf("removed %v, logged %v", removed, logged)
	}
	removeWake = func(string) error { return errors.New("access denied") }
	endSwap("alice", st, logf)
	if len(logged) != 1 || !strings.Contains(logged[0], "was not removed") {
		t.Fatalf("a wake that could not be removed was not said: %v", logged)
	}
}
