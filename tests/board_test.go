package tests

import (
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

func TestSwapBoard(t *testing.T) {
	c := harness.New()
	alice, bob := c.NewAccount(), c.NewAccount()
	code := readContract(t, "board.bas")
	scid, res := c.Install(code, nil, nil, alice, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	t.Logf("board.bas: %d bytes, install storage gas %d", len(code), res.GasStorage)
	exp := c.Height + 100
	post := func(args rpc.Arguments, who *rpc.Address, dero uint64) harness.Result {
		var in map[crypto.Hash]uint64
		if dero > 0 {
			in = map[crypto.Hash]uint64{zero: dero}
		}
		return c.Call(scid, "Post", args, in, who)
	}
	good := rpc.Arguments{harness.S("chain", "solana"), harness.S("giveAsset", ""), harness.U("giveAmount", 500000), harness.U("wantAmount", 12345678), harness.S("wantAddr", "So1anaAddressOfAlice"), harness.U("expiry", exp)}
	if r := post(rpc.Arguments{harness.S("chain", ""), harness.S("giveAsset", ""), harness.U("giveAmount", 1), harness.U("wantAmount", 1), harness.S("wantAddr", "x"), harness.U("expiry", exp)}, alice, 0); r.OK() {
		t.Fatal("empty chain accepted")
	}
	if r := post(rpc.Arguments{harness.S("chain", "solana"), harness.S("giveAsset", "abc"), harness.U("giveAmount", 1), harness.U("wantAmount", 1), harness.S("wantAddr", "x"), harness.U("expiry", exp)}, alice, 0); r.OK() {
		t.Fatal("bad asset accepted")
	}
	if r := post(good, alice, 1); r.OK() {
		t.Fatal("post with DERO attached accepted (the board never holds funds)")
	}
	if r := post(good, nil, 0); r.OK() {
		t.Fatal("anonymous post accepted")
	}
	if r := post(good, alice, 0); !r.OK() || c.U(scid, "n") != 1 || c.U(scid, "st_0") != 0 || c.S(scid, "wa_0") != "So1anaAddressOfAlice" {
		t.Fatalf("post: %v", r.Err)
	}
	hash := strings.Repeat("ab", 32)
	// maker cannot accept their own offer; short hash refused
	if r := c.Call(scid, "Accept", rpc.Arguments{harness.U("id", 0), harness.S("hash", hash), harness.S("lockRef", "soltx")}, nil, alice); r.OK() {
		t.Fatal("maker accepted own offer")
	}
	if r := c.Call(scid, "Accept", rpc.Arguments{harness.U("id", 0), harness.S("hash", "abcd"), harness.S("lockRef", "")}, nil, bob); r.OK() {
		t.Fatal("short hash accepted")
	}
	if r := c.Call(scid, "Accept", rpc.Arguments{harness.U("id", 0), harness.S("hash", hash), harness.S("lockRef", "soltx")}, nil, bob); !r.OK() || c.U(scid, "st_0") != 1 || c.S(scid, "h_0") != hash || c.S(scid, "t_0") != string(bob.Compressed()) {
		t.Fatalf("accept: %v", r.Err)
	}
	if r := c.Call(scid, "Accept", rpc.Arguments{harness.U("id", 0), harness.S("hash", hash), harness.S("lockRef", "")}, nil, bob); r.OK() {
		t.Fatal("double accept")
	}
	if r := c.Call(scid, "Cancel", rpc.Arguments{harness.U("id", 0)}, nil, alice); r.OK() {
		t.Fatal("cancel of an accepted offer")
	}
	// second offer: stranger cannot cancel, maker can, nobody can accept afterwards; expired offers cannot be accepted
	if r := post(good, alice, 0); !r.OK() {
		t.Fatalf("post 2: %v", r.Err)
	}
	if r := c.Call(scid, "Cancel", rpc.Arguments{harness.U("id", 1)}, nil, bob); r.OK() {
		t.Fatal("stranger cancelled")
	}
	if r := c.Call(scid, "Cancel", rpc.Arguments{harness.U("id", 1)}, nil, alice); !r.OK() || c.U(scid, "st_1") != 2 {
		t.Fatalf("cancel: %v", r.Err)
	}
	if r := c.Call(scid, "Accept", rpc.Arguments{harness.U("id", 1), harness.S("hash", hash), harness.S("lockRef", "")}, nil, bob); r.OK() {
		t.Fatal("accepted a cancelled offer")
	}
	if r := post(rpc.Arguments{harness.S("chain", "zcash"), harness.S("giveAsset", ""), harness.U("giveAmount", 1000), harness.U("wantAmount", 1), harness.S("wantAddr", "t1abc"), harness.U("expiry", c.Height+2)}, alice, 0); !r.OK() {
		t.Fatalf("post 3: %v", r.Err)
	}
	c.NextBlock()
	c.NextBlock()
	if r := c.Call(scid, "Accept", rpc.Arguments{harness.U("id", 2), harness.S("hash", hash), harness.S("lockRef", "")}, nil, bob); r.OK() {
		t.Fatal("accepted an expired offer")
	}
}
