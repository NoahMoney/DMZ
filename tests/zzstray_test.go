package tests

// GAP-027 (docs/MAINNET_GAP_ANALYSIS.md). social.bas checked no attached value anywhere, and nft.bas's Unlock,
// ClaimCreator and ClaimFees took DERO they do not use; what they took stayed in the contract for ever. Each
// entrypoint now refuses value it does not take with RETURN 1, which the DVM discards and, at ring size 2, refunds to
// the signer. Every refused case below is one the old contract accepted. The DVM has no builtin that lists what a
// call carries, so a contract can refuse only the assets it can name (DERO, its own unit, a token it stores); the last
// case in each test pins that limit: an unrelated asset attached is still kept.

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

func TestSocialRefusesStrayValue(t *testing.T) {
	c := harness.New()
	alice, bob := c.NewAccount(), c.NewAccount()
	scid, res := c.Install(readContract(t, "social.bas"), nil, nil, alice, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	other, res := c.Install(assetCode, nil, nil, alice, harness.MaxStorageGas) // an asset social.bas knows nothing of
	if !res.OK() {
		t.Fatalf("asset install: %v", res.Err)
	}
	if r := c.Call(other, "Mint", rpc.Arguments{harness.U("amount", 1<<40)}, nil, alice); !r.OK() {
		t.Fatalf("mint: %v", r.Err)
	}
	a, b := alice.String(), bob.String()
	dero := map[crypto.Hash]uint64{zero: 500}
	for _, x := range []struct {
		entry string
		args  rpc.Arguments
		key   string // a key the clean call writes
	}{
		{"Profile", rpc.Arguments{harness.S("name", "alice"), harness.S("bio", ""), harness.S("avatar", "")}, "pn_" + a},
		{"Follow", rpc.Arguments{harness.S("target", b)}, "f_" + a + "_" + b},
		{"Unfollow", rpc.Arguments{harness.S("target", b)}, ""},
	} {
		before := c.Var(scid, "fr_"+b)
		if r := c.Call(scid, x.entry, x.args, dero, alice); r.OK() {
			t.Fatalf("%s with DERO attached was accepted", x.entry)
		}
		if c.SCBalance(scid, zero) != 0 || c.Var(scid, "fr_"+b) != before || (x.key != "" && c.Exists(scid, x.key)) {
			t.Fatalf("a refused %s changed the contract", x.entry)
		}
		if r := c.Call(scid, x.entry, x.args, nil, alice); !r.OK() {
			t.Fatalf("a clean %s: %v", x.entry, r.Err)
		}
	}
	if c.Exists(scid, "f_"+a+"_"+b) || c.U(scid, "fr_"+b) != 0 {
		t.Fatal("the clean Unfollow did not take effect")
	}
	// the limit: an asset the contract cannot name is accepted, and kept
	if r := c.Call(scid, "Profile", rpc.Arguments{harness.S("name", "alice"), harness.S("bio", "b"), harness.S("avatar", "")}, map[crypto.Hash]uint64{other: 7}, alice); !r.OK() || c.SCBalance(scid, other) != 7 {
		t.Fatalf("unrelated asset: %v, held %d", r.Err, c.SCBalance(scid, other))
	}
	t.Log("social.bas refuses DERO on Profile, Follow and Unfollow; an asset it cannot name is still kept (no DVM builtin lists a call's assets)")
}

func TestNFTRefusesStrayValue(t *testing.T) {
	c := harness.New()
	treasury, creator, alice := c.NewAccount(), c.NewAccount(), c.NewAccount()
	token, res := c.Install(assetCode, nil, nil, creator, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatal(res.Err)
	}
	other, res := c.Install(assetCode, nil, nil, creator, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatal(res.Err)
	}
	for _, u := range []*rpc.Address{alice, creator} {
		for _, asset := range []crypto.Hash{token, other} {
			if r := c.Call(asset, "Mint", rpc.Arguments{harness.U("amount", 1000000)}, nil, u); !r.OK() {
				t.Fatal(r.Err)
			}
		}
	}
	const price = 100000
	scid, res := c.Install(readContract(t, "nft.bas"), nil, rpc.Arguments{harness.S("name", "Badge"), harness.S("descr", ""),
		harness.S("icon", ""), harness.S("file", ""), harness.S("tags", ""), harness.S("collection", "c"), harness.H("token", token),
		harness.U("minhold", 1000), harness.U("holdblocks", 1), harness.U("maxsupply", 5), harness.U("price", price),
		harness.S("treasury", treasury.String()), harness.U("fee", 50)}, creator, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	in := func(d, tok, unit uint64) map[crypto.Hash]uint64 {
		m := map[crypto.Hash]uint64{}
		if d > 0 {
			m[zero] = d
		}
		if tok > 0 {
			m[token] = tok
		}
		if unit > 0 {
			m[scid] = unit
		}
		return m
	}
	state := func() [6]interface{} {
		a := alice.String()
		return [6]interface{}{c.Var(scid, "lk_"+a), c.Var(scid, "minted"), c.Var(scid, "fd"), c.Var(scid, "cacc"), c.SCBalance(scid, zero), c.SCBalance(scid, token)}
	}
	refused := func(what, entry string, m map[crypto.Hash]uint64, who *rpc.Address) {
		t.Helper()
		before := state()
		if r := c.Call(scid, entry, nil, m, who); r.OK() {
			t.Fatalf("%s with %s attached was accepted", entry, what)
		}
		if state() != before {
			t.Fatalf("a refused %s (%s attached) changed the contract", entry, what)
		}
	}
	// Lock takes the token and nothing else
	refused("the token and DERO", "Lock", in(1, 5000, 0), alice)
	if r := c.Call(scid, "Lock", nil, in(0, 5000, 0), alice); !r.OK() {
		t.Fatalf("lock: %v", r.Err)
	}
	c.NextBlock()
	// Mint takes DERO and nothing else
	refused("the price and the token", "Mint", in(price, 1, 0), alice)
	r := c.Call(scid, "Mint", nil, in(price, 0, 0), alice)
	if !r.OK() || r.Sent(scid, alice) != 1 {
		t.Fatalf("mint: %v", r.Err)
	}
	refused("the price and an edition", "Lock", in(0, 5000, 1), creator)
	// Unlock, ClaimCreator and ClaimFees take nothing
	for _, x := range []struct {
		entry string
		who   *rpc.Address
	}{{"Unlock", alice}, {"ClaimCreator", creator}, {"ClaimFees", treasury}} {
		refused("DERO", x.entry, in(1, 0, 0), x.who)
		refused("the token", x.entry, in(0, 1, 0), x.who)
		refused("an edition", x.entry, in(0, 0, 1), x.who)
	}
	for _, x := range []struct {
		entry string
		who   *rpc.Address
	}{{"ClaimFees", treasury}, {"ClaimCreator", creator}, {"Unlock", alice}} {
		if r := c.Call(scid, x.entry, nil, nil, x.who); !r.OK() {
			t.Fatalf("a clean %s: %v", x.entry, r.Err)
		}
	}
	if c.SCBalance(scid, zero) != 0 || c.SCBalance(scid, token) != 0 {
		t.Fatalf("after every claim and the unlock: %d DERO, %d token", c.SCBalance(scid, zero), c.SCBalance(scid, token))
	}
	// the limit: an asset the contract cannot name is accepted, and kept
	if r := c.Call(scid, "ClaimCreator", nil, map[crypto.Hash]uint64{other: 9}, creator); !r.OK() || c.SCBalance(scid, other) != 9 {
		t.Fatalf("unrelated asset: %v, held %d", r.Err, c.SCBalance(scid, other))
	}
	t.Log("nft.bas refuses DERO, the token and editions wherever they are not taken; an asset it cannot name is still kept")
}
