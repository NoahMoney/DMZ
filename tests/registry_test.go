package tests

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/dvm"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

func factoryArgs(treasury string, regfee uint64) rpc.Arguments {
	return rpc.Arguments{harness.S("treasury", treasury), harness.U("regfee", regfee)}
}

func TestFactory(t *testing.T) {
	c := harness.New()
	installer, treasury, alice := c.NewAccount(), c.NewAccount(), c.NewAccount()
	code := readContract(t, "factory.bas")
	scid, res := c.Install(code, nil, factoryArgs(treasury.String(), 10000), installer, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	t.Logf("factory.bas: %d bytes, install storage gas %d", len(code), res.GasStorage)
	if c.S(scid, "kind") != "factory" || c.U(scid, "v") != 2 || c.S(scid, "treasury") != string(treasury.Compressed()) ||
		c.S(scid, "tstr") != treasury.String() || c.U(scid, "regfee") != 10000 || c.U(scid, "initd") != 1 {
		t.Fatal("init")
	}
	// v2 stores no key naming anyone but the treasury: not the installer, and no owner of any kind
	for _, k := range []string{"owner", "tmpowner", "renounced"} {
		if c.Exists(scid, k) {
			t.Fatalf("factory.bas v2 stored %q", k)
		}
	}
	// Inverted from v1, which refused an install at ring size > 2 because it stored the signer as the owner. v2 stores
	// no signer, so an anonymous install is accepted and records nothing about who made it.
	anon, r := c.Install(code, nil, factoryArgs(treasury.String(), 0), nil, harness.MaxStorageGas)
	if !r.OK() || c.Exists(anon, "owner") || c.S(anon, "tstr") != treasury.String() || c.U(anon, "regfee") != 0 {
		t.Fatalf("anonymous install: %v", r.Err)
	}
	// Initialize still refuses a treasury that is not an address
	for _, bad := range []string{"dero1bad", "", "not an address"} {
		if _, r := c.Install(code, nil, factoryArgs(bad, 0), installer, harness.MaxStorageGas); r.OK() {
			t.Fatalf("an install with treasury %q was accepted", bad)
		}
	}
	var fake crypto.Hash
	fake[0] = 7
	// registration without the fee → rejected; with the fee → stored and fee forwarded to the treasury
	if r := c.Call(scid, "RegisterPool", rpc.Arguments{harness.H("scid", fake)}, map[crypto.Hash]uint64{zero: 9999}, alice); r.OK() {
		t.Fatal("underpaid registration accepted")
	}
	r = c.Call(scid, "RegisterPool", rpc.Arguments{harness.H("scid", fake)}, map[crypto.Hash]uint64{zero: 12000}, alice)
	if !r.OK() || r.Sent(zero, treasury) != 12000 || c.U(scid, "pc") != 1 || c.S(scid, "pool_0") != hex.EncodeToString(fake[:]) {
		t.Fatalf("register pool: %v sent=%d", r.Err, r.Sent(zero, treasury))
	}
	if c.SCBalance(scid, zero) != 0 {
		t.Fatal("factory kept fee")
	}
	// wrong scid length (string arg of 4 bytes) → rejected
	if r := c.Call(scid, "RegisterLaunch", rpc.Arguments{harness.S("scid", "abcd")}, map[crypto.Hash]uint64{zero: 12000}, alice); r.OK() {
		t.Fatal("bad scid accepted")
	}
	// anonymous registration is allowed (no SIGNER needed) when paying
	if r := c.Call(scid, "RegisterLaunch", rpc.Arguments{harness.H("scid", fake)}, map[crypto.Hash]uint64{zero: 10000}, nil); !r.OK() || c.U(scid, "lc") != 1 {
		t.Fatalf("anonymous paid registration: %v", r.Err)
	}
	if r := c.Call(scid, "RegisterNFT", rpc.Arguments{harness.H("scid", fake)}, map[crypto.Hash]uint64{zero: 10000}, alice); !r.OK() || c.U(scid, "nc") != 1 {
		t.Fatalf("register nft: %v", r.Err)
	}
	// a factory installed with regfee 0 registers for nothing, forever: there is no setter to make it free or not later
	if r := c.Call(anon, "RegisterPool", rpc.Arguments{harness.H("scid", fake)}, nil, alice); !r.OK() || c.U(anon, "pc") != 1 {
		t.Fatalf("free registration: %v", r.Err)
	}
	if c.SCBalance(anon, zero) != 0 {
		t.Fatal("factory kept a balance")
	}
	// the setters and the transfer are gone (TestFactoryFixedAtInstall has the full matrix)
	for _, entry := range []string{"SetRegFee", "SetTreasury", "Renounce", "TransferOwnership", "ClaimOwnership"} {
		if r := c.Call(scid, entry, rpc.Arguments{harness.U("regfee", 0), harness.S("treasury", alice.String()), harness.S("newowner", alice.String())}, nil, installer); r.OK() {
			t.Fatalf("%s is still an entrypoint", entry)
		}
	}
	if c.S(scid, "tstr") != treasury.String() || c.U(scid, "regfee") != 10000 || c.Exists(scid, "owner") || c.Exists(scid, "tmpowner") {
		t.Fatal("the factory's state moved")
	}
}

func TestSocial(t *testing.T) {
	c := harness.New()
	alice, bob, carol := c.NewAccount(), c.NewAccount(), c.NewAccount()
	code := readContract(t, "social.bas")
	scid, res := c.Install(code, nil, nil, alice, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	a, b, cc := alice.String(), bob.String(), carol.String()
	if r := c.Call(scid, "Profile", rpc.Arguments{harness.S("name", "alice"), harness.S("bio", "hi"), harness.S("avatar", "")}, nil, alice); !r.OK() {
		t.Fatalf("profile: %v", r.Err)
	}
	if c.S(scid, "pn_"+a) != "alice" || c.S(scid, "pb_"+a) != "hi" || c.S(scid, "pa_"+a) != "" {
		t.Fatal("profile keys")
	}
	long := make([]byte, 33)
	for i := range long {
		long[i] = 'x'
	}
	if r := c.Call(scid, "Profile", rpc.Arguments{harness.S("name", string(long)), harness.S("bio", ""), harness.S("avatar", "")}, nil, alice); r.OK() {
		t.Fatal("long name accepted")
	}
	if r := c.Call(scid, "Profile", rpc.Arguments{harness.S("name", "x"), harness.S("bio", ""), harness.S("avatar", "")}, nil, nil); r.OK() {
		t.Fatal("anonymous profile accepted")
	}
	// follow
	if r := c.Call(scid, "Follow", rpc.Arguments{harness.S("target", a)}, nil, alice); r.OK() {
		t.Fatal("self follow accepted")
	}
	if r := c.Call(scid, "Follow", rpc.Arguments{harness.S("target", "not-an-address")}, nil, alice); r.OK() {
		t.Fatal("bad target accepted")
	}
	if r := c.Call(scid, "Follow", rpc.Arguments{harness.S("target", b)}, nil, alice); !r.OK() {
		t.Fatalf("follow: %v", r.Err)
	}
	if r := c.Call(scid, "Follow", rpc.Arguments{harness.S("target", b)}, nil, alice); r.OK() {
		t.Fatal("double follow accepted")
	}
	if r := c.Call(scid, "Follow", rpc.Arguments{harness.S("target", cc)}, nil, alice); !r.OK() {
		t.Fatal("follow 2")
	}
	if r := c.Call(scid, "Follow", rpc.Arguments{harness.S("target", b)}, nil, carol); !r.OK() {
		t.Fatal("follow 3")
	}
	if c.U(scid, "f_"+a+"_"+b) != c.Height || c.U(scid, "fc_"+a) != 2 || c.S(scid, "fl_"+a+"_0") != b || c.S(scid, "fl_"+a+"_1") != cc || c.U(scid, "fr_"+b) != 2 || c.U(scid, "fr_"+cc) != 1 {
		t.Fatal("follow keys")
	}
	// unfollow
	if r := c.Call(scid, "Unfollow", rpc.Arguments{harness.S("target", cc)}, nil, bob); r.OK() {
		t.Fatal("unfollow without follow accepted")
	}
	if r := c.Call(scid, "Unfollow", rpc.Arguments{harness.S("target", b)}, nil, alice); !r.OK() {
		t.Fatal("unfollow")
	}
	if c.Exists(scid, "f_"+a+"_"+b) || c.U(scid, "fr_"+b) != 1 || c.U(scid, "fc_"+a) != 2 {
		t.Fatal("unfollow keys")
	}
	// re-follow appends a new list entry
	if r := c.Call(scid, "Follow", rpc.Arguments{harness.S("target", b)}, nil, alice); !r.OK() || c.U(scid, "fc_"+a) != 3 || c.S(scid, "fl_"+a+"_2") != b {
		t.Fatal("refollow")
	}
}

func TestNFT(t *testing.T) {
	c := harness.New()
	treasury, creator, alice, bob := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	token, res := c.Install(assetCode, nil, nil, creator, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatal(res.Err)
	}
	for _, u := range []*rpc.Address{alice, bob} {
		if r := c.Call(token, "Mint", rpc.Arguments{harness.U("amount", 1000000)}, nil, u); !r.OK() {
			t.Fatal(r.Err)
		}
	}
	code := readContract(t, "nft.bas")
	args := func(tok crypto.Hash, price uint64) rpc.Arguments {
		return rpc.Arguments{harness.S("name", "Veil Holder Badge"), harness.S("descr", "earned by holding VTC"),
			harness.S("icon", "https://example.invalid/i.png"), harness.S("file", "https://example.invalid/f.png"),
			harness.S("tags", "#veil"), harness.S("collection", "Veil Badges"), harness.H("token", tok),
			harness.U("minhold", 1000), harness.U("holdblocks", 3), harness.U("maxsupply", 2), harness.U("price", price),
			harness.S("treasury", treasury.String()), harness.U("fee", 50)}
	}
	scid, res := c.Install(code, nil, args(token, 100000), creator, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	t.Logf("nft.bas: %d bytes, install storage gas %d", len(code), res.GasStorage)
	if _, r := c.Install(code, nil, rpc.Arguments{harness.S("name", "x"), harness.S("descr", ""), harness.S("icon", ""), harness.S("file", ""), harness.S("tags", ""), harness.S("collection", ""), harness.S("token", "short"), harness.U("minhold", 1), harness.U("holdblocks", 1), harness.U("maxsupply", 1), harness.U("price", 0), harness.S("treasury", treasury.String()), harness.U("fee", 0)}, creator, harness.MaxStorageGas); r.OK() {
		t.Fatal("bad token length accepted")
	}
	in := func(d, tok uint64) map[crypto.Hash]uint64 {
		m := map[crypto.Hash]uint64{}
		if d > 0 {
			m[zero] = d
		}
		if tok > 0 {
			m[token] = tok
		}
		return m
	}
	a := alice.String()
	// lock below minhold → reject; mint before lock → reject
	if r := c.Call(scid, "Lock", nil, in(0, 999), alice); r.OK() {
		t.Fatal("under-min lock accepted")
	}
	if r := c.Call(scid, "Mint", nil, in(100000, 0), alice); r.OK() {
		t.Fatal("mint without lock accepted")
	}
	if r := c.Call(scid, "Lock", nil, in(0, 5000), alice); !r.OK() || c.U(scid, "lk_"+a) != 5000 || c.U(scid, "lh_"+a) != c.Height {
		t.Fatalf("lock: %v", r.Err)
	}
	if r := c.Call(scid, "Lock", nil, in(0, 5000), alice); r.OK() {
		t.Fatal("double lock accepted")
	}
	// too early
	if r := c.Call(scid, "Mint", nil, in(100000, 0), alice); r.OK() {
		t.Fatal("early mint accepted")
	}
	c.NextBlock()
	c.NextBlock()
	c.NextBlock()
	// underpaid
	if r := c.Call(scid, "Mint", nil, in(99999, 0), alice); r.OK() {
		t.Fatal("underpaid mint accepted")
	}
	// overpaid: refund of the excess, one edition, fee split
	r := c.Call(scid, "Mint", nil, in(150000, 0), alice)
	if !r.OK() || r.Sent(scid, alice) != 1 || r.Sent(zero, alice) != 50000 || c.U(scid, "minted") != 1 || c.U(scid, "fd") != 500 || c.U(scid, "cacc") != 99500 || c.S(scid, "ed_0") != a {
		t.Fatalf("mint: %v sent nft=%d refund=%d", r.Err, r.Sent(scid, alice), r.Sent(zero, alice))
	}
	if r := c.Call(scid, "Mint", nil, in(100000, 0), alice); r.OK() {
		t.Fatal("second mint on one lock accepted")
	}
	// unlock returns tokens and clears keys; a new lock restarts the clock
	r = c.Call(scid, "Unlock", nil, nil, alice)
	if !r.OK() || r.Sent(token, alice) != 5000 || c.Exists(scid, "lk_"+a) || c.Exists(scid, "lm_"+a) {
		t.Fatal("unlock")
	}
	if r := c.Call(scid, "Unlock", nil, nil, alice); r.OK() {
		t.Fatal("double unlock accepted")
	}
	// bob mints the last edition; supply exhausted afterwards
	if r := c.Call(scid, "Lock", nil, in(0, 1000), bob); !r.OK() {
		t.Fatal(r.Err)
	}
	for i := 0; i < 3; i++ {
		c.NextBlock()
	}
	if r := c.Call(scid, "Mint", nil, in(100000, 0), bob); !r.OK() || c.U(scid, "minted") != 2 {
		t.Fatal("bob mint")
	}
	if r := c.Call(scid, "Lock", nil, in(0, 1000), alice); !r.OK() {
		t.Fatal(r.Err)
	}
	for i := 0; i < 3; i++ {
		c.NextBlock()
	}
	if r := c.Call(scid, "Mint", nil, in(100000, 0), alice); r.OK() {
		t.Fatal("mint beyond max supply accepted")
	}
	// payouts
	if r := c.Call(scid, "ClaimCreator", nil, nil, alice); r.OK() {
		t.Fatal("non-creator claimed")
	}
	r = c.Call(scid, "ClaimCreator", nil, nil, creator)
	if !r.OK() || r.Sent(zero, creator) != 199000 || c.U(scid, "cacc") != 0 {
		t.Fatalf("creator claim %d", r.Sent(zero, creator))
	}
	r = c.Call(scid, "ClaimFees", nil, nil, treasury)
	if !r.OK() || r.Sent(zero, treasury) != 1000 || c.U(scid, "fd") != 0 {
		t.Fatalf("fee claim %d", r.Sent(zero, treasury))
	}
	// bob's 1000 and alice's second lock of 1000 are still locked; no DERO is left after both claims
	if c.SCBalance(scid, zero) != 0 || c.SCBalance(scid, token) != 2000 {
		t.Fatalf("balances dero=%d token=%d", c.SCBalance(scid, zero), c.SCBalance(scid, token))
	}
}

// A holder NFT is earned once per address: lock, mint, unlock, lock again must not mint a second edition (the
// unlock used to delete the per-lock mint flag). A hold period of zero blocks is refused at install.
func TestNFTMintOncePerAddress(t *testing.T) {
	c := harness.New()
	treasury, creator, alice := c.NewAccount(), c.NewAccount(), c.NewAccount()
	token, res := c.Install(assetCode, nil, nil, creator, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatal(res.Err)
	}
	if r := c.Call(token, "Mint", rpc.Arguments{harness.U("amount", 1000000)}, nil, alice); !r.OK() {
		t.Fatal(r.Err)
	}
	code := readContract(t, "nft.bas")
	args := func(holdblocks uint64) rpc.Arguments {
		return rpc.Arguments{harness.S("name", "Once"), harness.S("descr", ""), harness.S("icon", ""), harness.S("file", ""),
			harness.S("tags", ""), harness.S("collection", ""), harness.H("token", token),
			harness.U("minhold", 1000), harness.U("holdblocks", holdblocks), harness.U("maxsupply", 5), harness.U("price", 0),
			harness.S("treasury", treasury.String()), harness.U("fee", 50)}
	}
	if _, r := c.Install(code, nil, args(0), creator, harness.MaxStorageGas); r.OK() {
		t.Fatal("holdblocks 0 accepted: an instant mint needs no holding at all")
	}
	scid, res := c.Install(code, nil, args(2), creator, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	lock := map[crypto.Hash]uint64{token: 1000}
	if r := c.Call(scid, "Lock", nil, lock, alice); !r.OK() {
		t.Fatalf("lock: %v", r.Err)
	}
	c.NextBlock()
	c.NextBlock()
	if r := c.Call(scid, "Mint", nil, nil, alice); !r.OK() || r.Sent(scid, alice) != 1 || c.U(scid, "minted") != 1 {
		t.Fatalf("first mint: %v", r.Err)
	}
	if r := c.Call(scid, "Unlock", nil, nil, alice); !r.OK() || r.Sent(token, alice) != 1000 {
		t.Fatalf("unlock: %v", r.Err)
	}
	if r := c.Call(scid, "Lock", nil, lock, alice); !r.OK() {
		t.Fatalf("second lock: %v", r.Err)
	}
	c.NextBlock()
	c.NextBlock()
	if r := c.Call(scid, "Mint", nil, nil, alice); r.OK() || c.U(scid, "minted") != 1 {
		t.Fatal("second mint accepted: editions can be farmed by re-locking")
	}
	if r := c.Call(scid, "Unlock", nil, nil, alice); !r.OK() || r.Sent(token, alice) != 1000 {
		t.Fatalf("second unlock: %v", r.Err)
	}
}

// RegisterNFA takes the next slot of the badge counter: after a badge collection at nft_0 an NFA lands at nfa_1
// and nc reads 2, with nft_1 and nfa_0 absent — a slot holds exactly one spelling, and the app's registry walk
// (feed.js registry_) skips the one that is missing. The fee and length rules are reg()'s, as for every kind.
func TestFactoryRegisterNFA(t *testing.T) {
	c := harness.New()
	owner, treasury, alice := c.NewAccount(), c.NewAccount(), c.NewAccount()
	code := readContract(t, "factory.bas")
	scid, res := c.Install(code, nil, rpc.Arguments{harness.S("treasury", treasury.String()), harness.U("regfee", 10000)}, owner, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	var nft, nfa crypto.Hash
	nft[0], nfa[0] = 7, 9
	if r := c.Call(scid, "RegisterNFA", rpc.Arguments{harness.H("scid", nfa)}, map[crypto.Hash]uint64{zero: 9999}, alice); r.OK() || c.U(scid, "nc") != 0 {
		t.Fatal("underpaid NFA registration accepted")
	}
	if r := c.Call(scid, "RegisterNFA", rpc.Arguments{harness.S("scid", "abcd")}, map[crypto.Hash]uint64{zero: 10000}, alice); r.OK() || c.U(scid, "nc") != 0 {
		t.Fatal("bad scid accepted")
	}
	if r := c.Call(scid, "RegisterNFT", rpc.Arguments{harness.H("scid", nft)}, map[crypto.Hash]uint64{zero: 10000}, alice); !r.OK() || c.U(scid, "nc") != 1 {
		t.Fatalf("register nft: %v", r.Err)
	}
	r := c.Call(scid, "RegisterNFA", rpc.Arguments{harness.H("scid", nfa)}, map[crypto.Hash]uint64{zero: 12000}, alice)
	if !r.OK() || r.Sent(zero, treasury) != 12000 || c.U(scid, "nc") != 2 || c.S(scid, "nfa_1") != hex.EncodeToString(nfa[:]) {
		t.Fatalf("register nfa: %v sent=%d nc=%d", r.Err, r.Sent(zero, treasury), c.U(scid, "nc"))
	}
	if c.Exists(scid, "nft_1") || c.Exists(scid, "nfa_0") || c.S(scid, "nft_0") != hex.EncodeToString(nft[:]) {
		t.Fatal("a slot must hold exactly one of nft_<i> / nfa_<i>")
	}
	if c.SCBalance(scid, zero) != 0 {
		t.Fatal("factory kept fee")
	}
	// anonymous registration is allowed when paying, as for every other kind
	if r := c.Call(scid, "RegisterNFA", rpc.Arguments{harness.H("scid", nfa)}, map[crypto.Hash]uint64{zero: 10000}, nil); !r.OK() || c.U(scid, "nc") != 3 || c.S(scid, "nfa_2") != hex.EncodeToString(nfa[:]) {
		t.Fatalf("anonymous paid NFA registration: %v", r.Err)
	}
}

// MT-J15(b), replacing TestFactoryRenounce. factory.bas v2 has no owner: the treasury and the registration fee are
// fixed by Initialize for the life of the factory. From the first block after install and at later heights, every
// entrypoint name that was ever owner-gated in v1 (SetTreasury, SetRegFee, Renounce; TransferOwnership and
// ClaimOwnership, removed before v2) and Initialize itself, each with arguments that would move the treasury or the
// fee, is refused — the DVM discards the call, so it commits nothing — for the installer, a stranger, and an anonymous
// caller at ring size > 2, whose SIGNER() is 33 zero bytes; and the stored state stays exactly as Initialize left it.
// No owner, tmpowner or renounced key ever appears. Registration goes on, forwarding every fee in the call to the
// treasury fixed at install, so the factory holds nothing it could owe.
func TestFactoryFixedAtInstall(t *testing.T) {
	c := harness.New()
	installer, treasury, t2, alice := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	code := readContract(t, "factory.bas")
	scid, res := c.Install(code, nil, factoryArgs(treasury.String(), 10000), installer, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	state := func() [10]interface{} {
		return [10]interface{}{c.Var(scid, "owner"), c.Var(scid, "tmpowner"), c.Var(scid, "renounced"), c.Var(scid, "treasury"),
			c.Var(scid, "tstr"), c.Var(scid, "regfee"), c.Var(scid, "initd"), c.Var(scid, "kind"), c.Var(scid, "v"), c.Var(scid, "pc")}
	}
	installed := state()
	if installed[0] != nil || installed[1] != nil || installed[2] != nil {
		t.Fatalf("an owner key exists after install: owner %v, tmpowner %v, renounced %v", installed[0], installed[1], installed[2])
	}
	calls := []struct {
		entry string
		args  rpc.Arguments
	}{
		{"SetTreasury", rpc.Arguments{harness.S("treasury", t2.String())}},
		{"SetRegFee", rpc.Arguments{harness.U("regfee", 0)}},
		{"SetRegFee", rpc.Arguments{harness.U("regfee", ^uint64(0))}},
		{"Renounce", nil},
		{"Initialize", factoryArgs(t2.String(), 0)},
		{"Initialize", factoryArgs(treasury.String(), ^uint64(0))},
		{"TransferOwnership", rpc.Arguments{harness.S("newowner", alice.String())}},
		{"ClaimOwnership", nil},
	}
	names := map[*rpc.Address]string{installer: "the installer", alice: "a stranger", nil: "an anonymous caller"}
	seen := map[string]string{}
	for round := 0; round < 3; round++ {
		c.NextBlock()
		for _, who := range []*rpc.Address{installer, alice, nil} {
			for _, x := range calls {
				r := c.Call(scid, x.entry, x.args, nil, who)
				if r.OK() || r.Err == nil {
					t.Fatalf("round %d: %s by %s was accepted", round, x.entry, names[who])
				}
				if state() != installed {
					t.Fatalf("round %d: %s by %s moved the factory's state", round, x.entry, names[who])
				}
				seen[x.entry] = r.Err.Error()
			}
		}
	}
	for _, x := range []string{"SetTreasury", "SetRegFee", "Renounce", "Initialize", "TransferOwnership", "ClaimOwnership"} {
		t.Logf("%s from the installer, a stranger and a ring>2 caller, over three heights: refused (%.60s)", x, seen[x])
	}
	var fake crypto.Hash
	fake[0] = 7
	r := c.Call(scid, "RegisterPool", rpc.Arguments{harness.H("scid", fake)}, map[crypto.Hash]uint64{zero: 12000}, alice)
	if !r.OK() || r.Sent(zero, treasury) != 12000 || c.U(scid, "pc") != 1 || c.SCBalance(scid, zero) != 0 {
		t.Fatalf("registration: %v sent %d, factory holds %d", r.Err, r.Sent(zero, treasury), c.SCBalance(scid, zero))
	}
	if r := c.Call(scid, "RegisterNFA", rpc.Arguments{harness.H("scid", fake)}, map[crypto.Hash]uint64{zero: 10000}, nil); !r.OK() || c.SCBalance(scid, zero) != 0 {
		t.Fatalf("anonymous NFA registration: %v, factory holds %d", r.Err, c.SCBalance(scid, zero))
	}
	if c.S(scid, "tstr") != treasury.String() || c.U(scid, "regfee") != 10000 {
		t.Fatal("registration moved the treasury or the fee")
	}
	t.Log("no owner key at any point; every name that ever changed the treasury or the fee, and Initialize, refused to every caller over three heights; registration still pays the treasury fixed at install and the factory holds 0")
}

// The same property read from the source the DVM parses, so it holds for every caller and every state rather than
// the ones a test drives: the old entrypoint names do not exist in any spelling; treasury, tstr and regfee are stored
// by Initialize alone, which refuses a second run; nothing stores or deletes owner, tmpowner or renounced; and no
// function reads SIGNER() at all, so no key holds authority over the factory, nor calls UPDATE_SC_CODE.
func TestFactoryOldEntrypointsAreGone(t *testing.T) {
	code := readContract(t, "factory.bas")
	sc, _, err := dvm.ParseSmartContract(code)
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"SetTreasury", "SetRegFee", "Renounce", "TransferOwnership", "ClaimOwnership"} {
		for fn := range sc.Functions {
			if strings.EqualFold(fn, gone) {
				t.Fatalf("factory.bas defines %s", fn)
			}
		}
	}
	writers := map[string]map[string]bool{}
	fn := ""
	for _, line := range strings.Split(code, "\n") {
		tl := strings.TrimSpace(line)
		if strings.HasPrefix(tl, "Function ") {
			name := strings.Fields(tl)[1]
			fn = name[:strings.Index(name, "(")]
		}
		for _, op := range []string{`STORE("`, `DELETE("`} {
			for rest := tl; strings.Contains(rest, op); {
				rest = rest[strings.Index(rest, op)+len(op):]
				key := rest[:strings.Index(rest, `"`)]
				if writers[key] == nil {
					writers[key] = map[string]bool{}
				}
				writers[key][fn] = true
			}
		}
		for _, banned := range []string{"SIGNER()", "UPDATE_SC_CODE"} {
			if strings.Contains(tl, banned) {
				t.Fatalf("factory.bas %s reads %s: %q", fn, banned, tl)
			}
		}
	}
	for _, k := range []string{"treasury", "tstr", "regfee", "initd"} {
		if len(writers[k]) != 1 || !writers[k]["Initialize"] {
			t.Fatalf("%q is written by %v; factory.bas v2 lets only Initialize write it", k, writers[k])
		}
	}
	for _, k := range []string{"owner", "tmpowner", "renounced"} {
		if len(writers[k]) != 0 {
			t.Fatalf("%q is written by %v; factory.bas v2 has no owner", k, writers[k])
		}
	}
	if !strings.Contains(code, `5 IF EXISTS("initd") != 0 THEN GOTO 900`) {
		t.Fatal("Initialize no longer refuses a second run at line 5")
	}
	t.Logf("factory.bas entrypoints %d, none of them a setter; treasury, tstr, regfee written only by Initialize; no signer read", len(sc.Functions)-1)
}
