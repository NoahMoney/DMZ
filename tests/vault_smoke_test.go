package tests

import (
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// TestVaultSmoke installs vault v3 and walks one deposit and one withdrawal, so a parse error, a gas overrun or
// a broken vote helper fails fast before the long tests run.
func TestVaultSmoke(t *testing.T) {
	c := harness.New()
	owner, treasury, user := c.NewAccount(), c.NewAccount(), c.NewAccount()
	sg := []*rpc.Address{c.NewAccount(), c.NewAccount(), c.NewAccount()}
	code := readContract(t, "vault.bas")
	scid, res := c.Install(code, nil, rpc.Arguments{harness.U("k", 2), harness.S("treasury", treasury.String()),
		harness.U("cap", 9000), harness.U("minbond", 1000), harness.U("minburn", 100), harness.U("wfee", 10),
		harness.U("mindep", 10), harness.U("regfee", 0), harness.U("capratio", 3)}, owner, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	t.Logf("vault.bas: %d bytes, install storage gas %d of %d", len(code), res.GasStorage, harness.MaxStorageGas)
	if res.GasStorage >= harness.MaxStorageGas {
		t.Fatalf("install storage gas %d is at the cap", res.GasStorage)
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
	if r := c.Call(scid, "Register", nil, nil, user); !r.OK() || !c.Exists(scid, "ua_0") {
		t.Fatalf("register: %v", r.Err)
	}
	for i := 0; i < 2; i++ {
		if r := c.Call(scid, "Attest", rpc.Arguments{harness.U("j", 0), harness.S("addr", "4Axmr")}, nil, sg[i]); !r.OK() {
			t.Fatalf("attest %d: %v", i, r.Err)
		}
	}
	if c.S(scid, "da_0") != "4Axmr" || c.U(scid, "dver_0") != 1 {
		t.Fatalf("attest did not execute: %q ver %d", c.S(scid, "da_0"), c.U(scid, "dver_0"))
	}
	txid := harness.H("txid", hash(0xab))
	var last harness.Result
	for i := 0; i < 2; i++ {
		last = c.Call(scid, "Mint", rpc.Arguments{harness.U("j", 0), txid, harness.U("vout", 1), harness.U("amount", 500)}, nil, sg[i])
		if !last.OK() {
			t.Fatalf("mint %d: %v", i, last.Err)
		}
	}
	id := strings.Repeat("ab", 32) + ":1"
	if c.U(scid, "sup") != 500 || c.U(scid, "mint_"+id) != 1 || last.Sent(scid, user) != 500 {
		t.Fatalf("mint: sup %d mint_ %d sent %d", c.U(scid, "sup"), c.U(scid, "mint_"+id), last.Sent(scid, user))
	}
	// burn keeps the claim in sup until the release is recorded
	if r := c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "4AxmrDestinationAddress")}, map[crypto.Hash]uint64{scid: 200}, user); !r.OK() {
		t.Fatalf("burn: %v", r.Err)
	}
	if c.U(scid, "sup") != 500 || c.U(scid, "w_0_v") != 190 || c.U(scid, "w_0_f") != 10 {
		t.Fatalf("burn: sup %d v %d f %d", c.U(scid, "sup"), c.U(scid, "w_0_v"), c.U(scid, "w_0_f"))
	}
	for i := 0; i < 2; i++ {
		if r := c.Call(scid, "Released", rpc.Arguments{harness.U("j", 0), harness.S("ftx", "fabc")}, nil, sg[i]); !r.OK() {
			t.Fatalf("released %d: %v", i, r.Err)
		}
	}
	if c.U(scid, "w_0_st") != 1 || c.U(scid, "sup") != 310 || c.U(scid, "feepool") != 10 {
		t.Fatalf("release: st %d sup %d fee %d", c.U(scid, "w_0_st"), c.U(scid, "sup"), c.U(scid, "feepool"))
	}
	r := c.Call(scid, "ClaimFees", nil, nil, treasury)
	if !r.OK() || r.Sent(scid, treasury) != 10 || c.U(scid, "feepool") != 0 {
		t.Fatalf("claim fees: %v sent %d", r.Err, r.Sent(scid, treasury))
	}
}

func hash(b byte) crypto.Hash {
	var h crypto.Hash
	for i := range h {
		h[i] = b
	}
	return h
}
