package tests

import (
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// board.bas v2 (docs/ZEC_SWAP.md §4): the maker's hash travels with the offer, the taker answers with the refund
// key hash and the refund height of the counter-chain script, and an accepted offer can be cancelled once it
// has expired — nothing on the board ever needs a human to leave it.
func TestSwapBoard(t *testing.T) {
	c := harness.New()
	alice, bob := c.NewAccount(), c.NewAccount()
	code := readContract(t, "board.bas")
	scid, res := c.Install(code, nil, nil, alice, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	t.Logf("board.bas: %d bytes, install storage gas %d", len(code), res.GasStorage)
	if c.U(scid, "v") != 2 {
		t.Fatalf("board version %d, want 2", c.U(scid, "v"))
	}
	exp := c.Height + 100
	hash := strings.Repeat("ab", 32)
	pkh := strings.Repeat("cd", 20)
	post := func(args rpc.Arguments, who *rpc.Address, dero uint64) harness.Result {
		var in map[crypto.Hash]uint64
		if dero > 0 {
			in = map[crypto.Hash]uint64{zero: dero}
		}
		return c.Call(scid, "Post", args, in, who)
	}
	offer := func(chain, asset string, give, want uint64, addr, h string, e uint64) rpc.Arguments {
		return rpc.Arguments{harness.S("chain", chain), harness.S("giveAsset", asset), harness.U("giveAmount", give), harness.U("wantAmount", want), harness.S("wantAddr", addr), harness.S("hash", h), harness.U("expiry", e)}
	}
	accept := func(id uint64, rp string, cltv uint64, who *rpc.Address) harness.Result {
		return c.Call(scid, "Accept", rpc.Arguments{harness.U("id", id), harness.S("refundPkh", rp), harness.U("cltv", cltv)}, nil, who)
	}
	cancel := func(id uint64, who *rpc.Address) harness.Result {
		return c.Call(scid, "Cancel", rpc.Arguments{harness.U("id", id)}, nil, who)
	}
	good := offer("zcash-t", "", 500000, 12345678, "t1MakerTransparentAddress", hash, exp)
	if r := post(offer("", "", 1, 1, "x", hash, exp), alice, 0); r.OK() {
		t.Fatal("empty chain accepted")
	}
	if r := post(offer("zcash-t", "abc", 1, 1, "x", hash, exp), alice, 0); r.OK() {
		t.Fatal("bad asset accepted")
	}
	if r := post(offer("zcash-t", "", 1, 1, "x", "abcd", exp), alice, 0); r.OK() {
		t.Fatal("short hash accepted at post")
	}
	if r := post(good, alice, 1); r.OK() {
		t.Fatal("post with DERO attached accepted (the board never holds funds)")
	}
	if r := post(good, nil, 0); r.OK() {
		t.Fatal("anonymous post accepted")
	}
	if r := post(good, alice, 0); !r.OK() || c.U(scid, "n") != 1 || c.U(scid, "st_0") != 0 || c.S(scid, "wa_0") != "t1MakerTransparentAddress" || c.S(scid, "h_0") != hash {
		t.Fatalf("post: %v", r.Err)
	}
	// the maker cannot accept their own offer; a short refund key hash and a zero refund height are refused
	if r := accept(0, pkh, 5000, alice); r.OK() {
		t.Fatal("maker accepted own offer")
	}
	if r := accept(0, "abcd", 5000, bob); r.OK() {
		t.Fatal("short refund key hash accepted")
	}
	if r := accept(0, pkh, 0, bob); r.OK() {
		t.Fatal("zero refund height accepted")
	}
	if r := accept(0, pkh, 5000, bob); !r.OK() || c.U(scid, "st_0") != 1 || c.S(scid, "rp_0") != pkh || c.U(scid, "cl_0") != 5000 || c.S(scid, "t_0") != string(bob.Compressed()) {
		t.Fatalf("accept: %v", r.Err)
	}
	if r := accept(0, pkh, 5000, bob); r.OK() {
		t.Fatal("double accept")
	}
	// an accepted offer cannot be cancelled while it is live
	if r := cancel(0, alice); r.OK() {
		t.Fatal("cancel of a live accepted offer")
	}
	// second offer: a stranger cannot cancel, the maker can, nobody can accept afterwards, nor cancel twice
	if r := post(good, alice, 0); !r.OK() {
		t.Fatalf("post 2: %v", r.Err)
	}
	if r := cancel(1, bob); r.OK() {
		t.Fatal("stranger cancelled")
	}
	if r := cancel(1, alice); !r.OK() || c.U(scid, "st_1") != 2 {
		t.Fatalf("cancel: %v", r.Err)
	}
	if r := accept(1, pkh, 5000, bob); r.OK() {
		t.Fatal("accepted a cancelled offer")
	}
	if r := cancel(1, alice); r.OK() {
		t.Fatal("cancelled twice")
	}
	// third offer expires in two blocks: after that nobody can accept it
	if r := post(offer("zcash-t", "", 1000, 1, "t1abc", hash, c.Height+2), alice, 0); !r.OK() {
		t.Fatalf("post 3: %v", r.Err)
	}
	// fourth offer, accepted, expires in two blocks: after that the maker may cancel it — the taker who accepted
	// and walked leaves nothing that needs anyone else
	if r := post(offer("zcash-t", "", 1000, 1, "t1abc", hash, c.Height+2), alice, 0); !r.OK() {
		t.Fatalf("post 4: %v", r.Err)
	}
	if r := accept(3, pkh, 5000, bob); !r.OK() || c.U(scid, "st_3") != 1 {
		t.Fatalf("accept 3: %v", r.Err)
	}
	if r := cancel(3, alice); r.OK() {
		t.Fatal("cancelled an accepted offer before its expiry")
	}
	c.NextBlock()
	c.NextBlock()
	if r := accept(2, pkh, 5000, bob); r.OK() {
		t.Fatal("accepted an expired offer")
	}
	if r := cancel(3, bob); r.OK() {
		t.Fatal("the taker cancelled the expired accepted offer")
	}
	if r := cancel(3, alice); !r.OK() || c.U(scid, "st_3") != 2 {
		t.Fatalf("cancel of the expired accepted offer: %v", r.Err)
	}
}
