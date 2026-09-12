package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestConfsIsTheTradesTierUnlessTyped (the review of 2026-09-12, finding 6): -confs left out is 0, which the companion
// takes as the trade's confirmation tier's depth; typed, it is taken as given, and never below Monero's spendable age.
func TestConfsIsTheTradesTierUnlessTyped(t *testing.T) {
	env := func(string) string { return "" }
	base := []string{"-net", "stagenet", "-daemon", "127.0.0.1:20000", "-dero", "127.0.0.1:30006", "-monero", "http://127.0.0.1:38082",
		"-prover", filepath.Join(t.TempDir(), "proof-xmr"), "-amount-dero", "1", "-amount-xmr", "1"}
	o, err := parseOptions("bob", base, env)
	if err != nil || o.confs != 0 {
		t.Fatalf("-confs left out: %d, %v; want 0, the trade's tier's", o.confs, err)
	}
	for _, low := range []string{"0", "9"} {
		if _, err := parseOptions("bob", append(append([]string{}, base...), "-confs", low), env); err == nil || !strings.Contains(err.Error(), "10") {
			t.Fatalf("-confs %s: %v", low, err)
		}
	}
	if o, err := parseOptions("bob", append(append([]string{}, base...), "-confs", "12"), env); err != nil || o.confs != 12 {
		t.Fatalf("-confs 12: %v", err)
	}
}
