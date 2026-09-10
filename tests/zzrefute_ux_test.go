package tests

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// TestRefuteBurnRecordsRefundNotSigner pins the contract property that app/wrap.js:387 denies: Burn never reads
// SIGNER(); w_<j>_by is ADDRESS_RAW(refund), an address the burner names and which need not be their own.
func TestRefuteBurnRecordsRefundNotSigner(t *testing.T) {
	c := harness.New()
	owner, treasury, user, other := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	sg := []*rpc.Address{c.NewAccount(), c.NewAccount(), c.NewAccount()}
	code := readContract(t, "vault.bas")
	scid, res := c.Install(code, nil, rpc.Arguments{harness.U("k", 2), harness.S("treasury", treasury.String()),
		harness.U("cap", 9000), harness.U("minbond", 1000), harness.U("minburn", 100), harness.U("wfee", 10),
		harness.U("mindep", 10), harness.U("regfee", 0), harness.U("capratio", 3), harness.U("bigbps", 2000), harness.U("bigdelay", 0)}, owner, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	for _, s := range sg {
		if r := c.Call(scid, "AddSigner", rpc.Arguments{harness.S("addr", s.String())}, nil, owner); !r.OK() {
			t.Fatalf("add: %v", r.Err)
		}
		if r := c.Call(scid, "Bond", nil, map[crypto.Hash]uint64{zero: 1000}, s); !r.OK() {
			t.Fatalf("bond: %v", r.Err)
		}
	}
	if r := c.Call(scid, "Seal", nil, nil, owner); !r.OK() {
		t.Fatalf("seal: %v", r.Err)
	}
	if r := c.Call(scid, "Register", nil, nil, user); !r.OK() {
		t.Fatalf("register: %v", r.Err)
	}
	for i := 0; i < 2; i++ {
		if r := c.Call(scid, "Attest", rpc.Arguments{harness.U("j", 0), harness.S("addr", "4Axmr")}, nil, sg[i]); !r.OK() {
			t.Fatalf("attest %d: %v", i, r.Err)
		}
	}
	txid := harness.H("txid", hash(0xcd))
	for i := 0; i < 2; i++ {
		if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("j", 0), txid, harness.U("vout", 1), harness.U("amount", 500)}, nil, sg[i]); !r.OK() {
			t.Fatalf("mint %d: %v", i, r.Err)
		}
	}
	// user burns, naming `other` as the refund address — a wallet they do not sign from
	if r := c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "4AxmrDestinationAddr"), harness.S("refund", other.String())},
		map[crypto.Hash]uint64{scid: 200}, user); !r.OK() {
		t.Fatalf("burn with a foreign refund address was refused: %v", r.Err)
	}
	by := c.S(scid, "w_0_by")
	otherRaw := string(other.Compressed())
	userRaw := string(user.Compressed())
	t.Logf("w_0_by == other: %v   w_0_by == signer(user): %v", by == otherRaw, by == userRaw)
	if by == userRaw {
		t.Fatalf("Burn recorded the SIGNER: the app's copy would be true")
	}
	if by != otherRaw {
		t.Fatalf("w_0_by is neither the signer nor the named refund address")
	}
}

// TestRefuteMintRefusedAtCap: a deposit that overshoots the cap is refused at the k-th vote and the vote scope
// stays open — no Refunded, no wrapped units. This is what UX-01 says the deposit walkthrough never mentions.
func TestRefuteMintRefusedAtCap(t *testing.T) {
	c := harness.New()
	owner, treasury, user := c.NewAccount(), c.NewAccount(), c.NewAccount()
	sg := []*rpc.Address{c.NewAccount(), c.NewAccount(), c.NewAccount()}
	code := readContract(t, "vault.bas")
	scid, res := c.Install(code, nil, rpc.Arguments{harness.U("k", 2), harness.S("treasury", treasury.String()),
		harness.U("cap", 600), harness.U("minbond", 1000), harness.U("minburn", 100), harness.U("wfee", 10),
		harness.U("mindep", 10), harness.U("regfee", 0), harness.U("capratio", 3), harness.U("bigbps", 2000), harness.U("bigdelay", 0)}, owner, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	for _, s := range sg {
		c.Call(scid, "AddSigner", rpc.Arguments{harness.S("addr", s.String())}, nil, owner)
		c.Call(scid, "Bond", nil, map[crypto.Hash]uint64{zero: 1000}, s)
	}
	c.Call(scid, "Seal", nil, nil, owner)
	c.Call(scid, "Register", nil, nil, user)
	for i := 0; i < 2; i++ {
		c.Call(scid, "Attest", rpc.Arguments{harness.U("j", 0), harness.S("addr", "4Axmr")}, nil, sg[i])
	}
	// fill the cap
	tx1 := harness.H("txid", hash(0x11))
	for i := 0; i < 2; i++ {
		if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("j", 0), tx1, harness.U("vout", 1), harness.U("amount", 600)}, nil, sg[i]); !r.OK() {
			t.Fatalf("first mint %d: %v", i, r.Err)
		}
	}
	if c.U(scid, "sup") != 600 {
		t.Fatalf("sup %d, want 600", c.U(scid, "sup"))
	}
	// a second, honest deposit of 100 that the signers saw: refused at the k-th vote, forever
	tx2 := harness.H("txid", hash(0x22))
	var last harness.Result
	for i := 0; i < 2; i++ {
		last = c.Call(scid, "Mint", rpc.Arguments{harness.U("j", 0), tx2, harness.U("vout", 1), harness.U("amount", 100)}, nil, sg[i])
		t.Logf("over-cap mint vote %d ok=%v err=%v", i, last.OK(), last.Err)
	}
	if last.OK() {
		t.Fatalf("the k-th over-cap vote succeeded; cap is not enforced")
	}
	if c.U(scid, "sup") != 600 {
		t.Fatalf("sup moved past the cap: %d", c.U(scid, "sup"))
	}
	// the deposit is neither minted nor refunded: no mint_ record for it
	t.Logf("headroom is 0 and the second deposit holds no claim: sup=%d cap=%d", c.U(scid, "sup"), c.U(scid, "cap"))
}

// TestRefuteBurnWorksAtRingAbove2 proves the option app/wrap.js:387 tells the user does not exist: with
// signer == nil (the harness's ring > 2 emulation, SIGNER() = 33 zero bytes) Burn still executes and records
// the named refund address. The app hard-codes ring 2 for the burn; the contract does not require it.
func TestRefuteBurnWorksAtRingAbove2(t *testing.T) {
	c := harness.New()
	owner, treasury, user, other := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	sg := []*rpc.Address{c.NewAccount(), c.NewAccount(), c.NewAccount()}
	code := readContract(t, "vault.bas")
	scid, res := c.Install(code, nil, rpc.Arguments{harness.U("k", 2), harness.S("treasury", treasury.String()),
		harness.U("cap", 9000), harness.U("minbond", 1000), harness.U("minburn", 100), harness.U("wfee", 10),
		harness.U("mindep", 10), harness.U("regfee", 0), harness.U("capratio", 3), harness.U("bigbps", 2000), harness.U("bigdelay", 0)}, owner, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	for _, s := range sg {
		c.Call(scid, "AddSigner", rpc.Arguments{harness.S("addr", s.String())}, nil, owner)
		c.Call(scid, "Bond", nil, map[crypto.Hash]uint64{zero: 1000}, s)
	}
	c.Call(scid, "Seal", nil, nil, owner)
	c.Call(scid, "Register", nil, nil, user)
	for i := 0; i < 2; i++ {
		c.Call(scid, "Attest", rpc.Arguments{harness.U("j", 0), harness.S("addr", "4Axmr")}, nil, sg[i])
	}
	txid := harness.H("txid", hash(0xef))
	for i := 0; i < 2; i++ {
		c.Call(scid, "Mint", rpc.Arguments{harness.U("j", 0), txid, harness.U("vout", 1), harness.U("amount", 500)}, nil, sg[i])
	}
	// signer == nil: ring > 2, SIGNER() is blank
	r := c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "4AxmrPrivateWithdrawal"), harness.S("refund", other.String())},
		map[crypto.Hash]uint64{scid: 200}, nil)
	if !r.OK() {
		t.Fatalf("Burn at ring > 2 was refused: %v — the app's copy would then be defensible", r.Err)
	}
	if c.S(scid, "w_0_by") != string(other.Compressed()) {
		t.Fatalf("w_0_by is not the named refund address at ring > 2")
	}
	t.Logf("Burn EXECUTED with a blank SIGNER (ring > 2) and recorded the named refund address; wn=%d", c.U(scid, "wn"))
}
