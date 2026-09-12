package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestDaemonCanBeGivenOnceForEveryNode (MT-B09): -daemon may be repeated. The first is this party's own node, the one
// its wallet uses; every further one is a node the taker's lock check must agree with, and one that reports another
// DERO network is refused before anything is read from it.
func TestDaemonCanBeGivenOnceForEveryNode(t *testing.T) {
	args := []string{"-net", "stagenet", "-daemon", "127.0.0.1:40402", "-daemon", "node.example:40402", "-daemon", "10.0.0.2:40402",
		"-dero", "127.0.0.1:40403", "-monero", "http://127.0.0.1:38083", "-prover", filepath.Join(t.TempDir(), "proof-xmr"),
		"-htlc", strings.Repeat("ab", 32), "-amount-dero", "1", "-amount-xmr", "1"}
	o, err := parseOptions("alice", args, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if o.daemon != "127.0.0.1:40402" || len(o.daemons) != 3 || o.daemons[1] != "node.example:40402" || o.daemons[2] != "10.0.0.2:40402" {
		t.Fatalf("daemon %q, daemons %v", o.daemon, o.daemons)
	}
	if err := sameDeroNetwork(o.daemon, false, "mainnet", o.daemons[1], false, "mainnet"); err != nil {
		t.Fatalf("a node on the same network: %v", err)
	}
	for _, c := range []struct {
		testnet bool
		name    string
	}{{true, "testnet"}, {true, "simulator"}, {false, "other"}} {
		if err := sameDeroNetwork(o.daemon, false, "mainnet", o.daemons[1], c.testnet, c.name); err == nil {
			t.Fatalf("a node reporting %q (testnet=%v) was taken as a witness for mainnet", c.name, c.testnet)
		}
	}
}
