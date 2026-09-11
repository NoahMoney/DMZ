package main

import (
	"strings"
	"testing"

	"veil/tools/xmrswap"
)

// TestNetworkSelectionFailsClosed: mainnet is never reached by default and never mixed with anything else.
// -mainnet is required whenever either chain is mainnet and refused unless both are; a DERO wallet written for
// another network than its daemon is refused; regtest runs only against the DERO simulator.
func TestNetworkSelectionFailsClosed(t *testing.T) {
	const (
		deroMainAddr = "dero1qy2nxgts7wdn28ckc4l2tewphjcppqjfj69ddkxjn0ay8hlsjx73jqgpyhv40"
		deroTestAddr = "deto1qy2nxgts7wdn28ckc4l2tewphjcppqjfj69ddkxjn0ay8hlsjx73jqgmat5s8"
	)
	type netcase struct {
		name        string
		deroTestnet bool
		deroNetwork string
		addr        string
		monero      xmrswap.Network
		regtest     bool
		mainnet     bool
		want        string // "" = accepted
	}
	for _, c := range []netcase{
		{"simulator and stagenet", true, "Simulator", deroTestAddr, xmrswap.Stagenet, false, false, ""},
		{"DERO testnet and Monero testnet", true, "", deroTestAddr, xmrswap.Testnet, false, false, ""},
		{"mainnet on both, named", false, "", deroMainAddr, xmrswap.Mainnet, false, true, ""},
		{"mainnet on both, not named", false, "", deroMainAddr, xmrswap.Mainnet, false, false, "without -mainnet"},
		{"Monero mainnet against the simulator", true, "Simulator", deroTestAddr, xmrswap.Mainnet, false, false, "without -mainnet"},
		{"Monero mainnet against the simulator, named", true, "Simulator", deroTestAddr, xmrswap.Mainnet, false, true, "mix"},
		{"DERO mainnet with stagenet", false, "", deroMainAddr, xmrswap.Stagenet, false, false, "without -mainnet"},
		{"DERO mainnet with stagenet, named", false, "", deroMainAddr, xmrswap.Stagenet, false, true, "mix"},
		{"-mainnet on test networks", true, "Simulator", deroTestAddr, xmrswap.Stagenet, false, true, "-mainnet was given"},
		{"a testnet wallet on a mainnet daemon", false, "", deroTestAddr, xmrswap.Mainnet, false, true, "not a mainnet address"},
		{"a mainnet wallet on the simulator", true, "Simulator", deroMainAddr, xmrswap.Stagenet, false, false, "not a test-network address"},
		{"regtest against the simulator", true, "Simulator", deroTestAddr, xmrswap.Mainnet, true, false, ""},
		{"regtest against DERO testnet", true, "", deroTestAddr, xmrswap.Mainnet, true, false, "only against the DERO simulator"},
		{"regtest against DERO mainnet", false, "", deroMainAddr, xmrswap.Mainnet, true, false, "only against the DERO simulator"},
		{"regtest named as mainnet", true, "Simulator", deroTestAddr, xmrswap.Mainnet, true, true, "two different networks"},
	} {
		err := checkNetworks(c.deroTestnet, c.deroNetwork, c.addr, c.monero, c.regtest, c.mainnet)
		switch {
		case c.want == "" && err != nil:
			t.Fatalf("%s: refused: %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Fatalf("%s: %v, want a refusal naming %q", c.name, err, c.want)
		}
	}
	if n, regtest, err := parseNet("regtest"); err != nil || !regtest || n != xmrswap.Mainnet {
		t.Fatalf("parseNet(regtest) = %v %v %v", n, regtest, err)
	}
	if _, regtest, err := parseNet("stagenet"); err != nil || regtest {
		t.Fatalf("parseNet(stagenet): %v %v", regtest, err)
	}
}
