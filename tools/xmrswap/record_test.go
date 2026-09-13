package xmrswap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestRecordedHTLCIsTheDeployRecord (MT-A18): the scids the command line defaults -htlc to are deploy/networks.json's,
// network for network, and the lookup refuses a network the record lacks or has no htlc.bas on.
func TestRecordedHTLCIsTheDeployRecord(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "networks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for k, v := range rec {
		if strings.HasPrefix(k, "_") {
			continue
		}
		var n struct {
			HTLC string `json:"htlc"`
		}
		if err := json.Unmarshal(v, &n); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		want[k] = n.HTLC
	}
	if !reflect.DeepEqual(want, recordedHTLC) {
		t.Fatalf("record_gen.go is not deploy/networks.json: run `go generate ./xmrswap` from tools/\ncompiled: %v\nrecord:   %v", recordedHTLC, want)
	}
	if _, err := RecordedHTLC("nowhere"); err == nil {
		t.Fatal("a network the record does not have gave a scid")
	}
	for net, scid := range want {
		got, err := RecordedHTLC(net)
		switch {
		case scid == "" && err == nil:
			t.Fatalf("%s: no htlc.bas recorded, yet %q was returned", net, got)
		case scid != "" && (err != nil || got != scid):
			t.Fatalf("%s: recorded %s, got %q (%v)", net, scid, got, err)
		}
		t.Logf("%s: %q", net, scid)
	}
}

// TestRecordedXmrTreasuryIsTheDeployRecord: the treasury the maker's sweep pays DMZ's swap fee to is deploy/networks.json's
// xmrTreasury, network for network, and each one recorded is one a swap on that DERO network can start with: mainnet's
// on Monero mainnet, testnet's on stagenet or testnet (the DERO test network pairs with those), the simulator's on regtest
// (mainnet's form) or a Monero test network.
func TestRecordedXmrTreasuryIsTheDeployRecord(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "networks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for k, v := range rec {
		if strings.HasPrefix(k, "_") {
			continue
		}
		var n struct {
			XmrTreasury string `json:"xmrTreasury"`
		}
		if err := json.Unmarshal(v, &n); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		want[k] = n.XmrTreasury
	}
	if !reflect.DeepEqual(want, recordedXmrTreasury) {
		t.Fatalf("record_gen.go is not deploy/networks.json: run `go generate ./xmrswap` from tools/\ncompiled: %v\nrecord:   %v", recordedXmrTreasury, want)
	}
	if _, err := RecordedXmrTreasury("nowhere"); err == nil {
		t.Fatal("a network the record does not have gave a treasury")
	}
	for net, addr := range want {
		got, err := RecordedXmrTreasury(net)
		if err != nil || got != addr {
			t.Fatalf("%s: recorded %q, got %q (%v)", net, addr, got, err)
		}
		if addr == "" {
			t.Logf("%s: no xmrTreasury recorded (on mainnet every swap is refused until one is; elsewhere no fee is taken)", net)
			continue
		}
		pa, err := ParseAddress(addr)
		if err != nil {
			t.Fatalf("%s: xmrTreasury %s: %v", net, addr, err)
		}
		if (net == "mainnet" && pa.Net != Mainnet) || (net == "testnet" && pa.Net == Mainnet) {
			t.Fatalf("%s records xmrTreasury %s, a %s address", net, addr, pa.Net)
		}
		if err := CheckTreasury(addr, pa.Net, net == "mainnet"); err != nil {
			t.Fatalf("%s: %v", net, err)
		}
		t.Logf("%s: xmrTreasury %s (%s, subaddress %v)", net, addr, pa.Net, pa.Subaddress)
	}
}
