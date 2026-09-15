package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestStatusFlagIsLoopbackOnly: -status is off unless given, and any address but 127.0.0.1:PORT is refused with the
// other flags, before any endpoint is contacted (docs/SWAP_STATUS.md §2).
func TestStatusFlagIsLoopbackOnly(t *testing.T) {
	prover, _ := filepath.Abs("proof-xmr")
	base := []string{"-net", "stagenet", "-daemon", "127.0.0.1:20000", "-dero", "127.0.0.1:30006", "-monero", "http://127.0.0.1:38082",
		"-prover", prover, "-amount-dero", "1", "-amount-xmr", "1"}
	env := func(string) string { return "" }
	o, err := parseOptions("bob", base, env)
	if err != nil || o.status != "" {
		t.Fatalf("without -status: %v, %q", err, o.status)
	}
	if o, err := parseOptions("bob", append(base, "-status", "127.0.0.1:18095"), env); err != nil || o.status != "127.0.0.1:18095" {
		t.Fatalf("-status 127.0.0.1:18095: %v", err)
	}
	for _, a := range []string{"0.0.0.0:18095", "localhost:18095", "[::1]:18095", "192.168.1.2:18095", "127.0.0.1:80", "127.0.0.1"} {
		if _, err := parseOptions("alice", append(base, "-status", a), env); err == nil || !strings.Contains(err.Error(), "-status") {
			t.Errorf("-status %s: %v", a, err)
		}
	}
}
