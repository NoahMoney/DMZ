package main

import (
	"reflect"
	"strings"
	"testing"

	"veil/tools/netcfg"
)

// Every contract id the per-network record can hold must be one the verifier checks. The record's single-instance
// contracts are its string fields other than the daemon address and the legacy single-vault spelling (vaults are
// checked from `vaults`). When the swap contracts were added to the record the verifier went on checking only
// factory, social, founders and chat, so an escrow running foreign code would have passed: this test is what stops
// that happening again. The treasury fields are foreign-chain addresses, not contracts: each companion validates its
// own (zecswap's CheckTreasury, xmrswap's equivalent) and refuses mainnet without one.
func TestRecordContractsCoverTheRecord(t *testing.T) {
	listed := map[string]bool{}
	for _, c := range recordContracts(&netcfg.Network{}) {
		listed[c.label] = true
	}
	typ := reflect.TypeOf(netcfg.Network{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Type.Kind() != reflect.String {
			continue
		}
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if tag == "daemon" || tag == "vault" || tag == "zecTreasury" || tag == "xmrTreasury" {
			continue
		}
		if !listed[tag] {
			t.Errorf("the record's %q field (netcfg.Network.%s) names a contract the verifier never checks; add it to recordContracts", tag, f.Name)
		}
	}
}

// The ids themselves must come from the record, not be fixed.
func TestRecordContractsReadTheRecord(t *testing.T) {
	n := &netcfg.Network{Factory: "f", Social: "s", Founders: "o", Chat: "c", Htlc: "h", Swap: "w", Board: "b", Rate: "r",
		Promo: "p"}
	want := map[string]string{"factory": "f", "social": "s", "founders": "o", "chat": "c", "htlc": "h", "swap": "w", "board": "b", "rate": "r",
		"promo": "p"}
	got := recordContracts(n)
	if len(got) != len(want) {
		t.Fatalf("recordContracts lists %d contracts, want %d", len(got), len(want))
	}
	for _, c := range got {
		if want[c.label] != c.scid {
			t.Errorf("%s: scid %q, the record has %q", c.label, c.scid, want[c.label])
		}
	}
}
