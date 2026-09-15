package main

import (
	"strings"
	"testing"
)

// XMR-1: the file backend was the flag default and the only backend that started, and nothing tied it to the
// simulator. A mainnet signer therefore ran on a JSON file: minting wrapped units against rows anyone with write
// access to that file could add, and burning a holder's units on withdrawals that paid nobody.
//
// The table is the whole contract of pickBackend. A Backend may come back for exactly one pair.
func TestPickBackendRefusesTheFileBackendOffTheSimulator(t *testing.T) {
	cases := []struct {
		network, backend string
		want             bool   // a usable Backend comes back
		errHas           string // a fragment the refusal must name, so the operator is told which rule bit
	}{
		{"simulator", "sim", true, ""},
		{"mainnet", "sim", false, "-network simulator"},
		{"testnet", "sim", false, "-network simulator"},
		{"simulator", "", false, "-backend is required"},
		{"mainnet", "", false, "-backend is required"},
		{"mainnet", "monero", false, "not implemented yet"},
		{"mainnet", "zcash", false, "-zcash-config"}, // built; it refuses to start without its configuration
		{"simulator", "zcash", false, "-zcash-config"},
		{"testnet", "monero", false, "not implemented yet"},
		{"simulator", "monero", false, "not implemented yet"},
		{"mainnet", "solana", false, "not implemented yet"},
		{"mainnet", "SIM", false, "unknown backend"},
		{"mainnet", "file", false, "unknown backend"},
		{"regtest", "sim", false, "unknown network"},
	}
	for _, c := range cases {
		confs := int64(0)
		be, err := pickBackend(c.network, c.backend, &confs, "sim-foreign.json")
		if c.want {
			if err != nil || be == nil {
				t.Errorf("-network %s -backend %s: want a backend, got %v (err %v)", c.network, c.backend, be, err)
			}
			continue
		}
		if err == nil || be != nil {
			t.Errorf("-network %s -backend %s: want a refusal, got backend %v (err %v)", c.network, c.backend, be, err)
			continue
		}
		if !strings.Contains(err.Error(), c.errHas) {
			t.Errorf("-network %s -backend %s: refusal does not say %q: %v", c.network, c.backend, c.errHas, err)
		}
	}
}

// The sim branch used to set *confs = 2 before anything checked the network, so a refused mainnet run had already
// overwritten the confirmation policy. Nothing a refusal returns may leave the caller's state changed.
func TestARefusedBackendLeavesConfsAlone(t *testing.T) {
	for _, network := range []string{"mainnet", "testnet"} {
		confs := int64(0)
		if _, err := pickBackend(network, "sim", &confs, "sim-foreign.json"); err == nil {
			t.Fatalf("%s: -backend sim was accepted", network)
		}
		if confs != 0 {
			t.Errorf("%s: a refused -backend sim set -confs to %d", network, confs)
		}
	}
}

// A real backend still settles the confirmation floor before it reports that it does not exist: when one is
// built, the mainnet floor from the network table must already be the number in force.
func TestARealBackendKeepsTheMainnetConfirmationFloor(t *testing.T) {
	confs := int64(1)
	if _, err := pickBackend("mainnet", "monero", &confs, ""); err == nil {
		t.Fatal("-backend monero was accepted")
	}
	if want := chainTable["mainnet"]["monero"].Confirmations; confs != want {
		t.Errorf("-confs 1 on mainnet monero settled at %d, want the table floor %d", confs, want)
	}
}

// The default must stay empty. A default is what made XMR-1 reachable by an operator who changed nothing.
func TestBackendFlagHasNoDefault(t *testing.T) {
	if *backend != "" {
		t.Errorf("-backend defaults to %q; it must have no default", *backend)
	}
}
