package zcashvault

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"veil/tools/zcashtx"
)

// Every party derives the same vault from public data: whatever order the account keys are listed in, and whether
// it is a signer (with its private key) or an auditor (with the descriptor alone), index j has one address, and that
// address's script names each signer's own child key.
func TestDescriptorEveryPartyDerivesTheSameAddresses(t *testing.T) {
	tv := newTestVault(t, 2, 3)
	x := tv.xpubs
	orders := [][]string{{x[0], x[1], x[2]}, {x[2], x[0], x[1]}, {x[1], x[2], x[0]}}
	var ref *Descriptor
	for _, o := range orders {
		d, err := NewDescriptor("regtest", 2, o, testScid)
		if err != nil {
			t.Fatal(err)
		}
		if ref == nil {
			ref = d
			continue
		}
		if d.Text() != ref.Text() {
			t.Fatal("the descriptor depends on the order its keys were given in")
		}
		for j := uint64(0); j < 25; j++ {
			a, _ := d.DepositAddress(j)
			b, _ := ref.DepositAddress(j)
			if a != b || !strings.HasPrefix(a, "t2") {
				t.Fatalf("index %d: %s and %s", j, a, b)
			}
		}
	}
	// an auditor with the published text derives the same thing
	aud, err := ParseDescriptorText(ref.ViewKey(), testScid)
	if err != nil {
		t.Fatalf("the published view key does not parse back: %v (%d characters)", err, len(ref.ViewKey()))
	}
	if len(ref.ViewKey()) > viewKeyMax {
		t.Fatalf("a 2-of-3 view key of %d characters does not fit SetViewKey's %d", len(ref.ViewKey()), viewKeyMax)
	}
	// every signer's private child key is in the script, at the position the descriptor says
	for j := uint32(0); j < 5; j++ {
		vs, _ := aud.Script(ChainDeposit, j)
		for i, seed := range tv.seeds {
			acct, err := accountKey(seed, NetRegtest, 0)
			if err != nil {
				t.Fatal(err)
			}
			who := -1
			for p, xp := range aud.XPubs {
				if xp == acct.XPub() {
					who = p
				}
			}
			child, err := acct.Path(aud.Branch(), ChainDeposit, j)
			if err != nil || who < 0 || !bytes.Equal(child.PubKey(), vs.Pubs[vs.signer[who]]) {
				t.Fatalf("signer %d's key at index %d is not where the descriptor puts it", i, j)
			}
		}
	}
	// the change address is not a deposit address, and another vault shares no address with this one
	ch, _ := ref.Change()
	other, _ := NewDescriptor("regtest", 2, x, "00"+testScid[2:])
	for j := uint64(0); j < 25; j++ {
		a, _ := ref.DepositAddress(j)
		b, _ := other.DepositAddress(j)
		if a == ch.Address || a == b {
			t.Fatalf("index %d collides (change %v, other vault %v)", j, a == ch.Address, a == b)
		}
	}
}

func TestDescriptorRefusals(t *testing.T) {
	tv := newTestVault(t, 2, 3)
	x := tv.xpubs
	m, _ := NewMaster(bytes.Repeat([]byte{7}, 32), true)
	xprv := m.serialize()
	mainnet, _ := NewMaster(bytes.Repeat([]byte{7}, 32), false)
	cases := map[string]struct {
		net   string
		k     int
		xpubs []string
		vault string
	}{
		"duplicate key":        {"regtest", 2, []string{x[0], x[0], x[1]}, testScid},
		"extended private key": {"regtest", 2, []string{x[0], x[1], xprv}, testScid},
		"mainnet key":          {"regtest", 2, []string{x[0], x[1], mainnet.XPub()}, testScid},
		"k of k":               {"regtest", 3, x, testScid},
		"1 of n":               {"regtest", 1, x, testScid},
		"unknown network":      {"regnet", 2, x, testScid},
		"bad scid":             {"regtest", 2, x, strings.ToUpper(testScid)},
		"garbage key":          {"regtest", 2, []string{x[0], x[1], "tpubnonsense"}, testScid},
	}
	for name, c := range cases {
		if _, err := NewDescriptor(c.net, c.k, c.xpubs, c.vault); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	var many []string
	for i := 0; i < 16; i++ {
		k, _ := NewMaster(bytes.Repeat([]byte{byte(i + 1)}, 32), true)
		many = append(many, k.XPub())
	}
	if _, err := NewDescriptor("regtest", 2, many, testScid); err == nil {
		t.Error("16 signers accepted; a standard P2SH multisig holds 15")
	}
	if _, err := NewDescriptor("regtest", 8, many[:15], testScid); err != nil {
		t.Errorf("8-of-15: %v", err)
	}
}

// The redeem script is exactly OP_k <33-byte key>… OP_n OP_CHECKMULTISIG, keys sorted; nothing else is accepted.
func TestRedeemScriptShape(t *testing.T) {
	var pubs [][]byte
	for i := 1; i <= 3; i++ {
		var kb [32]byte
		kb[31] = byte(i)
		k, _ := zcashtx.ParseKey(kb)
		pubs = append(pubs, k.PubKey())
	}
	sortPubs(pubs)
	r, err := RedeemScript(2, pubs)
	if err != nil {
		t.Fatal(err)
	}
	want := "52" + "21" + hex.EncodeToString(pubs[0]) + "21" + hex.EncodeToString(pubs[1]) + "21" + hex.EncodeToString(pubs[2]) + "53ae"
	if hex.EncodeToString(r) != want || len(r) != 105 {
		t.Fatalf("redeem script %x", r)
	}
	if _, err := RedeemScript(2, [][]byte{pubs[1], pubs[0], pubs[2]}); err == nil {
		t.Fatal("unsorted keys accepted")
	}
	if _, err := RedeemScript(2, [][]byte{pubs[0], pubs[0], pubs[2]}); err == nil {
		t.Fatal("a repeated key accepted")
	}
	if _, err := RedeemScript(4, pubs); err == nil {
		t.Fatal("4-of-3 accepted")
	}
	unc := append([]byte{4}, make([]byte, 64)...)
	if _, err := RedeemScript(1, [][]byte{unc}); err == nil {
		t.Fatal("an uncompressed key accepted")
	}
	var fifteen [][]byte
	for i := 1; i <= 15; i++ {
		var kb [32]byte
		kb[31] = byte(i)
		k, _ := zcashtx.ParseKey(kb)
		fifteen = append(fifteen, k.PubKey())
	}
	sortPubs(fifteen)
	if r, err := RedeemScript(8, fifteen); err != nil || len(r) != 513 {
		t.Fatalf("8-of-15: %d bytes, %v", len(r), err)
	}
}

func sortPubs(p [][]byte) {
	for i := range p {
		for j := i + 1; j < len(p); j++ {
			if bytes.Compare(p[j], p[i]) < 0 {
				p[i], p[j] = p[j], p[i]
			}
		}
	}
}

func TestValidDest(t *testing.T) {
	tv := newTestVault(t, 2, 3)
	b := tv.signers[0]
	ch, _ := tv.d.Change()
	var h [20]byte
	h[0] = 9
	good := []string{zcashtx.EncodeAddress(zcashtx.Testnet, zcashtx.P2PKH, h), zcashtx.EncodeAddress(zcashtx.Testnet, zcashtx.P2SH, h)}
	bad := []string{zcashtx.EncodeAddress(zcashtx.Mainnet, zcashtx.P2PKH, h), ch.Address, "utest1qqqqqqqqqqqqqqqqqqqqqqqqqqqqq", "ztestsapling1qqqqqqqqqqqqqqqqqq", "", "tm" + strings.Repeat("1", 33), fmt.Sprint(h)}
	for _, a := range good {
		if !b.ValidDest(a) {
			t.Errorf("%s refused", a)
		}
	}
	for _, a := range bad {
		if b.ValidDest(a) {
			t.Errorf("%q accepted", a)
		}
	}
}
