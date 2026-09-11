package tests

import (
	"encoding/hex"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

func TestFactory(t *testing.T) {
	c := harness.New()
	owner, treasury, t2, alice := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	code := readContract(t, "factory.bas")
	scid, res := c.Install(code, nil, rpc.Arguments{harness.S("treasury", treasury.String()), harness.U("regfee", 10000)}, owner, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	t.Logf("factory.bas: %d bytes, install storage gas %d", len(code), res.GasStorage)
	if c.S(scid, "owner") != string(owner.Compressed()) || c.S(scid, "treasury") != string(treasury.Compressed()) || c.S(scid, "tstr") != treasury.String() {
		t.Fatal("init")
	}
	// anonymous install must fail
	if _, r := c.Install(code, nil, rpc.Arguments{harness.S("treasury", treasury.String()), harness.U("regfee", 0)}, nil, harness.MaxStorageGas); r.OK() {
		t.Fatal("anonymous factory install accepted")
	}
	var fake crypto.Hash
	fake[0] = 7
	// registration without the fee → rejected; with the fee → stored and fee forwarded to the treasury
	if r := c.Call(scid, "RegisterPool", rpc.Arguments{harness.H("scid", fake)}, map[crypto.Hash]uint64{zero: 9999}, alice); r.OK() {
		t.Fatal("underpaid registration accepted")
	}
	r := c.Call(scid, "RegisterPool", rpc.Arguments{harness.H("scid", fake)}, map[crypto.Hash]uint64{zero: 12000}, alice)
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
	// owner-only setters
	if r := c.Call(scid, "SetRegFee", rpc.Arguments{harness.U("regfee", 0)}, nil, alice); r.OK() {
		t.Fatal("non-owner set fee")
	}
	if r := c.Call(scid, "SetRegFee", rpc.Arguments{harness.U("regfee", 0)}, nil, owner); !r.OK() || c.U(scid, "regfee") != 0 {
		t.Fatal("owner set fee")
	}
	if r := c.Call(scid, "RegisterPool", rpc.Arguments{harness.H("scid", fake)}, nil, alice); !r.OK() || c.U(scid, "pc") != 2 {
		t.Fatal("free registration")
	}
	if r := c.Call(scid, "SetTreasury", rpc.Arguments{harness.S("treasury", t2.String())}, nil, alice); r.OK() {
		t.Fatal("non-owner set treasury")
	}
	if r := c.Call(scid, "SetTreasury", rpc.Arguments{harness.S("treasury", "dero1bad")}, nil, owner); r.OK() {
		t.Fatal("bad treasury accepted")
	}
	if r := c.Call(scid, "SetTreasury", rpc.Arguments{harness.S("treasury", t2.String())}, nil, owner); !r.OK() || c.S(scid, "tstr") != t2.String() {
		t.Fatal("owner set treasury")
	}
	// ownership cannot be handed on: the two-step transfer is gone and Renounce is the only way it ends
	// (TestFactoryRenounce)
	for _, entry := range []string{"TransferOwnership", "ClaimOwnership"} {
		if r := c.Call(scid, entry, rpc.Arguments{harness.S("newowner", alice.String())}, nil, owner); r.OK() {
			t.Fatalf("%s is still an entrypoint", entry)
		}
		if r := c.Call(scid, entry, nil, nil, alice); r.OK() {
			t.Fatalf("%s is still an entrypoint", entry)
		}
	}
	if c.S(scid, "owner") != string(owner.Compressed()) || c.Exists(scid, "tmpowner") {
		t.Fatal("owner moved")
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

// SEC-K-02 / LIV-08. The owner can end its own powers by its own act, and nothing brings them back. After Renounce
// every function that was ever owner-gated (SetTreasury, SetRegFee, Renounce; TransferOwnership and ClaimOwnership,
// which no longer exist) and the one function that ever stored owner (Initialize) is refused — the DVM discards the
// call, so it commits nothing — for the old owner, a stranger, and an anonymous caller at ring size > 2, whose
// SIGNER() is 33 zero bytes (the reason owner is deleted rather than overwritten with a zero key, which that caller
// would match), at this height and later ones, and the stored state stays exactly as Renounce left it. Registration
// goes on, forwarding every fee in the call, so the factory holds nothing it could owe.
func TestFactoryRenounce(t *testing.T) {
	c := harness.New()
	owner, treasury, t2, alice := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	code := readContract(t, "factory.bas")
	scid, res := c.Install(code, nil, rpc.Arguments{harness.S("treasury", treasury.String()), harness.U("regfee", 10000)}, owner, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	for _, who := range []*rpc.Address{alice, nil} {
		if r := c.Call(scid, "Renounce", nil, nil, who); r.OK() {
			t.Fatal("Renounce by a non-owner was accepted")
		}
	}
	c.NextBlock()
	if r := c.Call(scid, "Renounce", nil, nil, owner); !r.OK() {
		t.Fatalf("renounce: %v", r.Err)
	}
	if c.Exists(scid, "owner") || c.U(scid, "renounced") != c.Height {
		t.Fatalf("after Renounce: owner present %v, renounced %v", c.Exists(scid, "owner"), c.Var(scid, "renounced"))
	}
	state := func() [7]interface{} {
		return [7]interface{}{c.Var(scid, "owner"), c.Var(scid, "tmpowner"), c.Var(scid, "treasury"), c.Var(scid, "tstr"),
			c.Var(scid, "regfee"), c.Var(scid, "renounced"), c.Var(scid, "initd")}
	}
	after := state()
	calls := []struct {
		entry string
		args  rpc.Arguments
	}{
		{"SetTreasury", rpc.Arguments{harness.S("treasury", t2.String())}},
		{"SetRegFee", rpc.Arguments{harness.U("regfee", 0)}},
		{"Renounce", nil},
		{"Initialize", rpc.Arguments{harness.S("treasury", t2.String()), harness.U("regfee", 0)}},
		{"TransferOwnership", rpc.Arguments{harness.S("newowner", alice.String())}},
		{"ClaimOwnership", nil},
	}
	seen := map[string]string{}
	for round := 0; round < 3; round++ {
		for _, who := range []*rpc.Address{owner, alice, nil} {
			name := "an anonymous caller"
			if who != nil {
				name = e2name(who, owner)
			}
			for _, x := range calls {
				r := c.Call(scid, x.entry, x.args, nil, who)
				if r.OK() || r.Err == nil {
					t.Fatalf("round %d: %s by %s was accepted after Renounce", round, x.entry, name)
				}
				if state() != after {
					t.Fatalf("round %d: %s by %s moved the factory's state after Renounce", round, x.entry, name)
				}
				seen[x.entry] = r.Err.Error()
			}
		}
		c.NextBlock()
	}
	for _, x := range calls {
		t.Logf("after Renounce, %s from the old owner, a stranger and a ring>2 caller: refused (%.60s)", x.entry, seen[x.entry])
	}
	// registration is untouched and forwards the whole fee to the treasury fixed at the renunciation: the factory
	// never holds a balance, so there is nothing it owes that it could fail to hold
	var fake crypto.Hash
	fake[0] = 7
	r := c.Call(scid, "RegisterPool", rpc.Arguments{harness.H("scid", fake)}, map[crypto.Hash]uint64{zero: 12000}, alice)
	if !r.OK() || r.Sent(zero, treasury) != 12000 || c.U(scid, "pc") != 1 || c.SCBalance(scid, zero) != 0 {
		t.Fatalf("registration after Renounce: %v sent %d, factory holds %d", r.Err, r.Sent(zero, treasury), c.SCBalance(scid, zero))
	}
	if r := c.Call(scid, "RegisterNFA", rpc.Arguments{harness.H("scid", fake)}, map[crypto.Hash]uint64{zero: 10000}, nil); !r.OK() || c.SCBalance(scid, zero) != 0 {
		t.Fatalf("anonymous NFA registration after Renounce: %v, factory holds %d", r.Err, c.SCBalance(scid, zero))
	}
	t.Logf("factory renounced at height %d: owner absent, every owner power refused to every caller over three heights; registration still pays the treasury and the factory holds 0", c.U(scid, "renounced"))
}

func e2name(who, owner *rpc.Address) string {
	if who.String() == owner.String() {
		return "the old owner"
	}
	return "a stranger"
}
