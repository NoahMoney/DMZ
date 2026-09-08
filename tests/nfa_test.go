package tests

// contracts/nfa.bas: ART-NFA-MS1 v1.1.1 with a quote asset and a protocol fee (docs/NFA_BUILD.md §5.A). Every
// case runs on the real DVM through the harness, once with the zero hash as the quote — DERO, where the contract
// must be the standard — and once with a plain asset contract standing in for a wrapped quote, as pool_test.go
// does. Settlement splits are asserted to the unit against the standard's own arithmetic plus the protoFee line.

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// The standard's donation address (init line 40). The harness knows only the accounts it created and
// ProcessExternal panics on a send to an address missing from the balance tree, so newNFAEnv registers it the
// one way the harness offers: as the signer of a burn.
const artificerAddr = "dero1qy0khp9s9yw2h0eu20xmy9lth3zp5cacmx3rwt6k45l568d2mmcf6qgcsevzx"

// BuyItNow 65 and Bid 85 refund anything above this: floor((2^64 - 1) / 10000), the largest amount every product
// in the contract survives without wrapping.
const nfaMaxAmt = 1844674407370955

type nfaEnv struct {
	c                                                  *harness.Chain
	creator, alice, bob, carol, treasury, charity, art *rpc.Address
	quote, wxmr, nfa                                   crypto.Hash
	royalty, artificerFee, protoFee                    uint64
	held                                               uint64 // units of the NFA in wallets, kept from the transfers the chain made
	names                                              map[string]string
}

func raw(a *rpc.Address) string { return string(a.Compressed()) }

// nfaArgs is InitializePrivate's argument list: the standard's metadata in the order it stores them, then the
// three DMZ additions. quote is "" for DERO or the 64-hex SCID of the asset a listing is priced in.
func nfaArgs(quote string, royalty, artificerFee, ownerCanUpdate, protoFee uint64, treasury string) rpc.Arguments {
	return rpc.Arguments{
		harness.U("artificerFee", artificerFee), harness.U("royalty", royalty), harness.U("ownerCanUpdate", ownerCanUpdate),
		harness.S("nameHdr", "DMZ test NFA"), harness.S("descrHdr", "one unit, one owner"), harness.S("typeHdr", "Image"),
		harness.S("iconURLHdr", "https://example.invalid/icon.png"), harness.S("tagsHdr", "#DMZ"),
		harness.S("fileCheckC", "c-header"), harness.S("fileCheckS", "s-header"),
		harness.S("fileURL", "https://example.invalid/file.png"), harness.S("fileSignURL", "https://example.invalid/file.png.sign"),
		harness.S("coverURL", "https://example.invalid/cover.png"), harness.S("collection", "DMZ tests"),
		harness.S("quote", quote), harness.U("protoFee", protoFee), harness.S("treasury", treasury),
	}
}

// newNFAEnv installs the stand-in quote asset and one NFA, DERO-quoted or wrapped-quoted, and asserts the
// install itself: gas under the cap, the single unit minted to the creator.
func newNFAEnv(t *testing.T, wrapped bool, royalty, artificerFee, protoFee uint64) *nfaEnv {
	t.Helper()
	e := &nfaEnv{c: harness.New(), royalty: royalty, artificerFee: artificerFee, protoFee: protoFee, names: map[string]string{}}
	e.creator, e.alice, e.bob, e.carol = e.c.NewAccount(), e.c.NewAccount(), e.c.NewAccount(), e.c.NewAccount()
	e.treasury, e.charity = e.c.NewAccount(), e.c.NewAccount()
	art, err := rpc.NewAddress(artificerAddr)
	if err != nil {
		t.Fatal(err)
	}
	e.art = art
	for n, a := range map[string]*rpc.Address{"creator": e.creator, "alice": e.alice, "bob": e.bob, "carol": e.carol, "treasury": e.treasury, "charity": e.charity, "artificer": e.art} {
		e.names[raw(a)] = n
	}
	var res harness.Result
	e.wxmr, res = e.c.Install(assetCode, nil, nil, e.creator, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("asset install: %v", res.Err)
	}
	for _, a := range []*rpc.Address{e.alice, e.bob, e.carol} { // an entry in the asset tree, as pool_test does
		if r := e.c.Call(e.wxmr, "Mint", rpc.Arguments{harness.U("amount", 1<<62)}, nil, a); !r.OK() {
			t.Fatalf("mint: %v", r.Err)
		}
	}
	// a DERO burn signed by the artificer address registers it in the balance tree (harness.exec), which is what a
	// send to it needs; the unit of DERO stranded in the asset contract is irrelevant to every assertion below
	if r := e.c.Call(e.wxmr, "Mint", rpc.Arguments{harness.U("amount", 1)}, map[crypto.Hash]uint64{zero: 1}, e.art); !r.OK() {
		t.Fatalf("registering the artificer address: %v", r.Err)
	}
	quote := ""
	if wrapped {
		e.quote, quote = e.wxmr, hex.EncodeToString(e.wxmr[:])
	}
	code := readContract(t, "nfa.bas")
	e.nfa, res = e.c.Install(code, nil, nfaArgs(quote, royalty, artificerFee, 0, protoFee, e.treasury.String()), e.creator, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("nfa install (wrapped=%v): %v", wrapped, res.Err)
	}
	t.Logf("nfa.bas install (wrapped=%v): %d bytes of code, storage gas %d, compute gas %d", wrapped, len(code), res.GasStorage, res.GasCompute)
	if res.GasStorage >= 20000 {
		t.Fatalf("install storage gas %d is not under the 20,000-unit cap", res.GasStorage)
	}
	if res.Sent(e.nfa, e.creator) != 1 {
		t.Fatalf("install minted %d units to the creator, want exactly 1", res.Sent(e.nfa, e.creator))
	}
	e.held = 1
	e.oneUnit(t, "install")
	return e
}

// call is every call to the NFA. On success it moves the unit ledger by what the chain did: units burned in
// leave a wallet, units the contract sent out reach one.
func (e *nfaEnv) call(entry string, args rpc.Arguments, in map[crypto.Hash]uint64, who *rpc.Address) harness.Result {
	r := e.c.Call(e.nfa, entry, args, in, who)
	if r.OK() {
		e.held -= in[e.nfa]
		for _, tr := range r.Transfers {
			if tr.Asset == e.nfa {
				e.held += tr.Amount
			}
		}
	}
	return r
}

func (e *nfaEnv) who(a string) string {
	if n, ok := e.names[a]; ok {
		return n
	}
	return "?"
}

// oneUnit is invariant §4.3. The chain keeps no balance for a contract's own asset (StoreSCValue drops the write:
// an SCID mints without limit), so the count comes from the transfers it made, kept by call: the units in
// wallets plus the one the contract records holding while listed (scBalance) must be exactly one.
func (e *nfaEnv) oneUnit(t *testing.T, tag string) {
	t.Helper()
	if sc := e.c.U(e.nfa, "scBalance"); e.held+sc != 1 {
		t.Fatalf("%s: %d units in wallets and %d in the contract — not exactly one unit", tag, e.held, sc)
	}
}

func (e *nfaEnv) money(v uint64) map[crypto.Hash]uint64 {
	if v == 0 {
		return nil
	}
	return map[crypto.Hash]uint64{e.quote: v}
}

func (e *nfaEnv) start(who *rpc.Address, listType string, hours, price uint64, charityAddr string, charityPerc uint64, unit bool) harness.Result {
	args := rpc.Arguments{harness.S("listType", listType), harness.U("duration", hours), harness.U("startPrice", price),
		harness.S("charityDonateAddr", charityAddr), harness.U("charityDonatePerc", charityPerc)}
	var in map[crypto.Hash]uint64
	if unit {
		in = map[crypto.Hash]uint64{e.nfa: 1}
	}
	return e.call("Start", args, in, who)
}

func (e *nfaEnv) buy(who *rpc.Address, v uint64) harness.Result {
	return e.call("BuyItNow", nil, e.money(v), who)
}
func (e *nfaEnv) bid(who *rpc.Address, v uint64) harness.Result {
	return e.call("Bid", nil, e.money(v), who)
}

// claim is the standard's private-transfer path: whoever holds the unit burns it and becomes owner. The harness
// does not check that the signer holds what it burns, so this is also how a test makes someone the owner.
func (e *nfaEnv) claim(t *testing.T, who *rpc.Address) {
	t.Helper()
	r := e.call("ClaimOwnership", nil, map[crypto.Hash]uint64{e.nfa: 1}, who)
	if !r.OK() || r.Sent(e.nfa, who) != 1 || e.c.S(e.nfa, "owner") != raw(who) {
		t.Fatalf("claim by %s: %v, unit back %d, owner %s", e.who(raw(who)), r.Err, r.Sent(e.nfa, who), e.who(e.c.S(e.nfa, "owner")))
	}
	e.oneUnit(t, "claim")
}

// checkSettlement asserts one sale or auction close to the unit: the standard's shares (processDEROFinalPayment
// 65, 85, 105, every one floored) and protoFee at 114, the owner's remainder, the unit to the buyer, nothing in
// any other asset, nothing left in the contract. extra names quote transfers the same call makes for other
// reasons (a refund to a late bidder).
func (e *nfaEnv) checkSettlement(t *testing.T, tag string, r harness.Result, amt uint64, seller, buyer *rpc.Address, charityPerc uint64, extra map[*rpc.Address]uint64) {
	t.Helper()
	if !r.OK() {
		t.Fatalf("%s: %v", tag, r.Err)
	}
	royalty, art, charity, proto := e.royalty*amt/100, e.artificerFee*amt/100, charityPerc*amt/100, e.protoFee*amt/10000
	payout := amt - royalty - art - charity - proto
	want := map[string]uint64{}
	add := func(a *rpc.Address, v uint64) { want[raw(a)] += v }
	add(e.creator, royalty)
	add(e.art, art)
	add(e.charity, charity)
	add(e.treasury, proto)
	add(seller, payout)
	total := amt
	for a, v := range extra {
		add(a, v)
		total += v
	}
	seen := map[string]bool{}
	for _, p := range []*rpc.Address{e.creator, e.art, e.charity, e.treasury, seller, buyer, e.alice, e.bob, e.carol} {
		if seen[raw(p)] {
			continue
		}
		seen[raw(p)] = true
		if got := r.Sent(e.quote, p); got != want[raw(p)] {
			t.Fatalf("%s: %s received %d of the quote, want %d (sale %d: royalty %d artificer %d charity %d proto %d owner %d)",
				tag, e.who(raw(p)), got, want[raw(p)], amt, royalty, art, charity, proto, payout)
		}
	}
	var sent uint64
	for _, tr := range r.Transfers {
		switch tr.Asset {
		case e.quote:
			sent += tr.Amount
		case e.nfa:
			if tr.Amount != 1 || tr.Address != raw(buyer) {
				t.Fatalf("%s: %d units of the NFA to %s, want 1 to the buyer", tag, tr.Amount, e.who(tr.Address))
			}
		default:
			t.Fatalf("%s: paid %d of %s to %s, which is neither the quote nor the unit", tag, tr.Amount, tr.Asset, e.who(tr.Address))
		}
	}
	if sent != total {
		t.Fatalf("%s: %d of the quote left the contract, want %d", tag, sent, total)
	}
	if r.Sent(e.nfa, buyer) != 1 || e.c.S(e.nfa, "owner") != raw(buyer) {
		t.Fatalf("%s: buyer got %d units, owner is %s", tag, r.Sent(e.nfa, buyer), e.who(e.c.S(e.nfa, "owner")))
	}
	if e.c.U(e.nfa, "scBalance") != 0 || e.c.S(e.nfa, "listType") != "" || e.c.U(e.nfa, "active") != 0 || e.c.U(e.nfa, "charityDonatePerc") != 0 || e.c.U(e.nfa, "startPrice") != 0 {
		t.Fatalf("%s: listing state not reset", tag)
	}
	e.oneUnit(t, tag)
	if b := e.c.SCBalance(e.nfa, e.quote); b != 0 {
		t.Fatalf("%s: %d of the quote stranded in the contract", tag, b)
	}
}

// ---- install --------------------------------------------------------------------------------------------------

func TestNFAInstall(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		e := newNFAEnv(t, wrapped, 10, 5, 250)
		c := e.c
		want := e.wxmr
		if !wrapped {
			want = zero
		}
		if c.S(e.nfa, "quote") != string(want[:]) {
			t.Fatalf("wrapped=%v: quote stored as %x", wrapped, c.S(e.nfa, "quote"))
		}
		if c.U(e.nfa, "protoFee") != 250 || c.S(e.nfa, "treasury") != raw(e.treasury) {
			t.Fatal("protoFee / treasury")
		}
		if c.S(e.nfa, "owner") != raw(e.creator) || c.S(e.nfa, "creatorAddr") != raw(e.creator) || c.S(e.nfa, "artificerAddr") != raw(e.art) {
			t.Fatal("owner / creatorAddr / artificerAddr")
		}
		if c.U(e.nfa, "royalty") != 10 || c.U(e.nfa, "artificerFee") != 5 || c.U(e.nfa, "ownerCanUpdate") != 0 {
			t.Fatal("royalty / artificerFee / ownerCanUpdate")
		}
		// the standard's constants and version, and every header by its exact key
		for k, v := range map[string]uint64{"active": 0, "scBalance": 0, "cancelBuffer": 300, "startBlockTime": 0, "endBlockTime": 0,
			"bidCount": 0, "staticBidIncr": 10000, "percentBidIncr": 1000, "charityDonatePerc": 0, "startPrice": 0, "currBidPrice": 0} {
			if c.U(e.nfa, k) != v {
				t.Fatalf("%s = %d, want %d", k, c.U(e.nfa, k), v)
			}
		}
		for k, v := range map[string]string{"version": "1.1.1", "listType": "", "nameHdr": "DMZ test NFA", "descrHdr": "one unit, one owner",
			"typeHdr": "Image", "iconURLHdr": "https://example.invalid/icon.png", "tagsHdr": "#DMZ", "fileCheckC": "c-header", "fileCheckS": "s-header",
			"fileURL": "https://example.invalid/file.png", "fileSignURL": "https://example.invalid/file.png.sign",
			"coverURL": "https://example.invalid/cover.png", "collection": "DMZ tests"} {
			if c.S(e.nfa, k) != v {
				t.Fatalf("%s = %q, want %q", k, c.S(e.nfa, k), v)
			}
		}
	}
	// what init refuses
	e := newNFAEnv(t, false, 0, 0, 0)
	code, tr := readContract(t, "nfa.bas"), e.treasury.String()
	good := nfaArgs("", 10, 5, 0, 250, tr)
	asHash := append(append(rpc.Arguments{}, good[:14]...), harness.H("quote", e.wxmr), harness.U("protoFee", 250), harness.S("treasury", tr))
	bad := []struct {
		why  string
		args rpc.Arguments
	}{
		{"quote of the wrong length", nfaArgs("abcd", 10, 5, 0, 250, tr)},
		{"quote of 64 characters that are not hex", nfaArgs(strings.Repeat("zz", 32), 10, 5, 0, 250, tr)},
		{"quote as 32 raw bytes (pool's H convention; §3.A takes hex)", asHash},
		{"protoFee over 10000", nfaArgs("", 10, 5, 0, 10001, tr)},
		{"treasury not an address", nfaArgs("", 10, 5, 0, 250, "dero1notanaddress")},
		{"royalty over 100", nfaArgs("", 101, 0, 0, 0, tr)},
		{"artificerFee over 100", nfaArgs("", 0, 101, 0, 0, tr)},
		{"ownerCanUpdate not 0 or 1", nfaArgs("", 0, 0, 2, 0, tr)},
		{"shares one bp past 100 %", nfaArgs("", 60, 40, 0, 1, tr)},
		{"a missing argument", good[:16]},
	}
	for _, b := range bad {
		if _, r := e.c.Install(code, nil, b.args, e.creator, harness.MaxStorageGas); r.OK() {
			t.Fatalf("an install with %s was accepted", b.why)
		}
	}
	if _, r := e.c.Install(code, nil, good, nil, harness.MaxStorageGas); r.OK() {
		t.Fatal("an install at ring size > 2 was accepted: the unit would have gone to nobody")
	}
	if _, r := e.c.Install(code, nil, nfaArgs("", 60, 40, 1, 0, tr), e.creator, harness.MaxStorageGas); !r.OK() {
		t.Fatalf("shares at exactly 100 %% refused: %v", r.Err)
	}
}

// ---- ownership ------------------------------------------------------------------------------------------------

func TestNFAClaimOwnership(t *testing.T) {
	e := newNFAEnv(t, false, 0, 0, 0)
	c := e.c
	if r := e.call("ClaimOwnership", nil, nil, e.alice); r.OK() {
		t.Fatal("a caller without the unit claimed ownership")
	}
	if c.S(e.nfa, "owner") != raw(e.creator) {
		t.Fatal("owner changed")
	}
	// the holder claims what is already theirs: the unit comes straight back, previousOwner is never written
	e.claim(t, e.creator)
	if c.Exists(e.nfa, "previousOwner") {
		t.Fatal("previousOwner written for a no-op claim")
	}
	// ring size > 2 with the unit: the standard returns it to the recorded owner rather than to nobody
	r := e.call("ClaimOwnership", nil, map[crypto.Hash]uint64{e.nfa: 1}, nil)
	if !r.OK() || r.Sent(e.nfa, e.creator) != 1 || c.S(e.nfa, "owner") != raw(e.creator) {
		t.Fatalf("anonymous claim: %v, unit to owner %d", r.Err, r.Sent(e.nfa, e.creator))
	}
	// a private transfer, then the new holder claims
	e.claim(t, e.alice)
	if c.S(e.nfa, "previousOwner") != raw(e.creator) {
		t.Fatal("previousOwner")
	}
	// Update stays the creator's unless ownerCanUpdate is 1 — it is 0 here, so the new owner is refused
	upd := rpc.Arguments{harness.S("iconURL", "https://example.invalid/icon2.png"), harness.S("coverURL", ""), harness.S("fileURL", ""), harness.S("fileSignURL", ""), harness.S("tags", "")}
	if r := e.call("Update", upd, nil, e.alice); r.OK() {
		t.Fatal("the owner updated headers with ownerCanUpdate = 0")
	}
	if r := e.call("Update", upd, nil, e.creator); !r.OK() || c.S(e.nfa, "iconURLHdr") != "https://example.invalid/icon2.png" || c.S(e.nfa, "coverURL") != "https://example.invalid/cover.png" {
		t.Fatalf("creator update: %v", r.Err)
	}
}

// ---- sale -----------------------------------------------------------------------------------------------------

func TestNFASale(t *testing.T) {
	for _, tc := range []struct {
		wrapped  bool
		protoFee uint64
	}{{false, 0}, {false, 250}, {true, 250}} {
		tag := "dero"
		if tc.wrapped {
			tag = "wrapped"
		}
		e := newNFAEnv(t, tc.wrapped, 10, 5, tc.protoFee)
		c := e.c
		e.claim(t, e.alice) // seller and creator differ, so royalty and the owner's remainder land on different accounts
		if r := e.start(e.alice, "sale", 24, 1_000_000, "", 0, false); r.OK() {
			t.Fatal("a listing started without the unit")
		}
		// the standard refunds the unit and returns 0 when the arguments are wrong: not the owner, an unknown type
		for _, bad := range []struct {
			who      *rpc.Address
			listType string
		}{{e.bob, "sale"}, {e.alice, "raffle"}} {
			r := e.start(bad.who, bad.listType, 24, 1_000_000, "", 0, true)
			if !r.OK() || r.Sent(e.nfa, bad.who) != 1 || c.S(e.nfa, "listType") != "" {
				t.Fatalf("%s: bad Start(%s by %s): %v, unit back %d", tag, bad.listType, e.who(raw(bad.who)), r.Err, r.Sent(e.nfa, bad.who))
			}
		}
		if r := e.start(e.alice, "sale", 24, 1_000_000, e.charity.String(), 20, true); !r.OK() {
			t.Fatalf("%s: Start: %v", tag, r.Err)
		}
		if c.S(e.nfa, "listType") != "sale" || c.U(e.nfa, "startPrice") != 1_000_000 || c.U(e.nfa, "scBalance") != 1 || c.U(e.nfa, "active") != 1 ||
			c.U(e.nfa, "startBlockTime") != c.Timestamp || c.U(e.nfa, "endBlockTime") != c.Timestamp+24*3600 ||
			c.U(e.nfa, "charityDonatePerc") != 20 || c.S(e.nfa, "charityDonateAddr") != raw(e.charity) {
			t.Fatalf("%s: listing state after Start", tag)
		}
		e.oneUnit(t, tag+" listed")
		if r := e.start(e.alice, "sale", 24, 1_000_000, "", 0, false); r.OK() {
			t.Fatalf("%s: a second listing started over an active one", tag)
		}
		if r := e.call("BuyItNow", nil, e.money(1_000_000), nil); r.OK() {
			t.Fatalf("%s: an anonymous buyer was accepted", tag)
		}
		// below the price: refunded in the same call, listing untouched
		r := e.buy(e.bob, 999_999)
		if !r.OK() || r.Sent(e.quote, e.bob) != 999_999 || r.Sent(e.nfa, e.bob) != 0 || c.S(e.nfa, "owner") != raw(e.alice) || c.U(e.nfa, "scBalance") != 1 {
			t.Fatalf("%s: buy below price: %v, refund %d", tag, r.Err, r.Sent(e.quote, e.bob))
		}
		if b := c.SCBalance(e.nfa, e.quote); b != 0 {
			t.Fatalf("%s: %d of the quote stranded by a refused buy", tag, b)
		}
		// the owner buying their own listing is a refund, not a sale
		if r := e.buy(e.alice, 1_000_000); !r.OK() || r.Sent(e.quote, e.alice) != 1_000_000 || c.U(e.nfa, "scBalance") != 1 {
			t.Fatalf("%s: owner self-buy: %v, refund %d", tag, r.Err, r.Sent(e.quote, e.alice))
		}
		// at the price
		e.checkSettlement(t, tag+" buy at price", e.buy(e.bob, 1_000_000), 1_000_000, e.alice, e.bob, 20, nil)
		if c.U(e.nfa, "previousSalePrice") != 1_000_000 || c.S(e.nfa, "previousOwner") != raw(e.alice) {
			t.Fatalf("%s: previousSalePrice / previousOwner", tag)
		}
		// bob relists: duration 0 is an hour, and a charity share past the room left is clamped to it
		room := (10000 - tc.protoFee - 500 - 1000) / 100
		if r := e.start(e.bob, "sale", 0, 1_000_000, e.charity.String(), 90, true); !r.OK() || c.U(e.nfa, "charityDonatePerc") != room || c.U(e.nfa, "endBlockTime") != c.Timestamp+3600 {
			t.Fatalf("%s: relist: %v, charity %d want %d", tag, r.Err, c.U(e.nfa, "charityDonatePerc"), room)
		}
		// above the price: the whole amount is the sale
		e.checkSettlement(t, tag+" buy above price", e.buy(e.carol, 1_500_000), 1_500_000, e.bob, e.carol, room, nil)
		// a clamped charity share still needs a charity address; without one the unit comes back and nothing starts
		if r := e.start(e.carol, "sale", 24, 1_000_000, "", 90, true); !r.OK() || r.Sent(e.nfa, e.carol) != 1 || c.S(e.nfa, "listType") != "" {
			t.Fatalf("%s: clamp without a charity address: %v", tag, r.Err)
		}
		// the 7-day cap
		if r := e.start(e.carol, "sale", 1000, 1_000_000, "", 0, true); !r.OK() || c.U(e.nfa, "endBlockTime") != c.Timestamp+604800 {
			t.Fatalf("%s: 1000 h listing: %v, end %d", tag, r.Err, c.U(e.nfa, "endBlockTime"))
		}
		if r := e.call("CancelListing", nil, nil, e.carol); !r.OK() || r.Sent(e.nfa, e.carol) != 1 {
			t.Fatalf("%s: cancel: %v", tag, r.Err)
		}
		e.oneUnit(t, tag+" end")
	}
}

func TestNFAExpiredSale(t *testing.T) {
	e := newNFAEnv(t, false, 10, 0, 250)
	c := e.c
	if r := e.start(e.creator, "sale", 1, 1_000_000, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	c.Timestamp = c.U(e.nfa, "endBlockTime")
	// a buy after the end returns the unit to the owner and the money to the buyer, in one call
	r := e.buy(e.bob, 1_000_000)
	if !r.OK() || r.Sent(e.quote, e.bob) != 1_000_000 || r.Sent(e.nfa, e.creator) != 1 || c.S(e.nfa, "owner") != raw(e.creator) || c.S(e.nfa, "listType") != "" || c.U(e.nfa, "scBalance") != 0 {
		t.Fatalf("expired buy: %v, refund %d, unit to owner %d", r.Err, r.Sent(e.quote, e.bob), r.Sent(e.nfa, e.creator))
	}
	e.oneUnit(t, "expired buy")
	// and the owner can close an expired sale nobody bought
	if r := e.start(e.creator, "sale", 1, 1_000_000, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := e.call("CloseListing", nil, nil, e.creator); r.OK() {
		t.Fatal("an active sale was closed")
	}
	c.Timestamp = c.U(e.nfa, "endBlockTime")
	if r := e.call("CloseListing", nil, nil, e.creator); !r.OK() || r.Sent(e.nfa, e.creator) != 1 || c.U(e.nfa, "scBalance") != 0 {
		t.Fatalf("close expired sale: %v", r.Err)
	}
	e.oneUnit(t, "closed")
}

// ---- auction --------------------------------------------------------------------------------------------------

func TestNFAAuction(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		tag := "dero"
		if wrapped {
			tag = "wrapped"
		}
		e := newNFAEnv(t, wrapped, 10, 0, 250)
		c := e.c
		e.claim(t, e.alice)
		if r := e.start(e.alice, "auction", 24, 1_000_000, "", 0, true); !r.OK() {
			t.Fatalf("%s: Start: %v", tag, r.Err)
		}
		end := c.Timestamp + 24*3600
		if c.U(e.nfa, "endBlockTime") != end {
			t.Fatalf("%s: end", tag)
		}
		if r := e.call("CloseListing", nil, nil, e.alice); r.OK() {
			t.Fatalf("%s: a running auction was closed", tag)
		}
		if r := e.call("CancelListing", nil, nil, e.bob); r.OK() {
			t.Fatalf("%s: a stranger cancelled", tag)
		}
		// below the start price: refunded, no bid recorded
		if r := e.bid(e.bob, 999_999); !r.OK() || r.Sent(e.quote, e.bob) != 999_999 || c.Exists(e.nfa, "currBidAddr") {
			t.Fatalf("%s: bid below start: %v", tag, r.Err)
		}
		expectBid := func(step string, r harness.Result, who *rpc.Address, amt, nextPrice, count uint64) {
			t.Helper()
			if !r.OK() {
				t.Fatalf("%s %s: %v", tag, step, r.Err)
			}
			if c.S(e.nfa, "currBidAddr") != raw(who) || c.U(e.nfa, "currBidAmt") != amt || c.U(e.nfa, "currBidPrice") != nextPrice || c.U(e.nfa, "bidCount") != count {
				t.Fatalf("%s %s: bidder %s amt %d next %d count %d", tag, step, e.who(c.S(e.nfa, "currBidAddr")), c.U(e.nfa, "currBidAmt"), c.U(e.nfa, "currBidPrice"), c.U(e.nfa, "bidCount"))
			}
			if c.U(e.nfa, raw(who)+"-bidDate") != c.Timestamp {
				t.Fatalf("%s %s: bidDate %d, want this block's %d", tag, step, c.U(e.nfa, raw(who)+"-bidDate"), c.Timestamp)
			}
			if b := c.SCBalance(e.nfa, e.quote); b != amt {
				t.Fatalf("%s %s: contract holds %d of the quote, want the winning bid %d", tag, step, b, amt)
			}
		}
		// bob 1.0: next price is the lesser of +10 % and +10000
		expectBid("bob 1.0", e.bid(e.bob, 1_000_000), e.bob, 1_000_000, 1_010_000, 1)
		// carol under the next price: refunded in the same call, bob still leads
		if r := e.bid(e.carol, 1_005_000); !r.OK() || r.Sent(e.quote, e.carol) != 1_005_000 || c.S(e.nfa, "currBidAddr") != raw(e.bob) || c.U(e.nfa, "currBidAmt") != 1_000_000 {
			t.Fatalf("%s: low bid: %v, refund %d", tag, r.Err, r.Sent(e.quote, e.carol))
		}
		// carol at the next price: bob is refunded in the same call, in the quote
		r := e.bid(e.carol, 1_010_000)
		expectBid("carol 1.01", r, e.carol, 1_010_000, 1_020_000, 2)
		if r.Sent(e.quote, e.bob) != 1_000_000 {
			t.Fatalf("%s: outbid bob refunded %d", tag, r.Sent(e.quote, e.bob))
		}
		// bob, whose bidDate the outbid deleted, may bid again in the same block
		r = e.bid(e.bob, 2_000_000)
		expectBid("bob 2.0", r, e.bob, 2_000_000, 2_010_000, 3)
		if r.Sent(e.quote, e.carol) != 1_010_000 {
			t.Fatalf("%s: outbid carol refunded %d", tag, r.Sent(e.quote, e.carol))
		}
		// the leader cannot bid twice in one block: refunded
		if r := e.bid(e.bob, 2_010_000); !r.OK() || r.Sent(e.quote, e.bob) != 2_010_000 || c.U(e.nfa, "currBidAmt") != 2_000_000 {
			t.Fatalf("%s: same-block rebid: %v, refund %d", tag, r.Err, r.Sent(e.quote, e.bob))
		}
		c.NextBlock()
		// raising one's own bid refunds the old one in the same call
		r = e.bid(e.bob, 2_010_000)
		expectBid("bob 2.01", r, e.bob, 2_010_000, 2_020_000, 4)
		if r.Sent(e.quote, e.bob) != 2_000_000 {
			t.Fatalf("%s: self-outbid refund %d", tag, r.Sent(e.quote, e.bob))
		}
		if c.U(e.nfa, "endBlockTime") != end {
			t.Fatalf("%s: end moved before the last 15 minutes", tag)
		}
		// anti-snipe: a bid inside the last 15 minutes moves the end to 15 minutes from now
		c.Timestamp = end - 600
		r = e.bid(e.carol, 2_100_000)
		expectBid("carol 2.1 in the last 10 min", r, e.carol, 2_100_000, 2_110_000, 5)
		if r.Sent(e.quote, e.bob) != 2_010_000 || c.U(e.nfa, "endBlockTime") != c.Timestamp+900 {
			t.Fatalf("%s: anti-snipe: bob refunded %d, end %d want %d", tag, r.Sent(e.quote, e.bob), c.U(e.nfa, "endBlockTime"), c.Timestamp+900)
		}
		end = c.Timestamp + 900
		c.Timestamp = end - 1
		if r := e.call("CloseListing", nil, nil, e.alice); r.OK() {
			t.Fatalf("%s: closed one second before the extended end", tag)
		}
		c.Timestamp = end
		if r := e.call("CloseListing", nil, nil, e.bob); r.OK() {
			t.Fatalf("%s: a non-owner closed", tag)
		}
		// CloseListing pays the winner's amount: split in the quote, unit to carol
		e.checkSettlement(t, tag+" close", e.call("CloseListing", nil, nil, e.alice), 2_100_000, e.alice, e.carol, 0, nil)
		if c.U(e.nfa, "previousAuctionPrice") != 2_100_000 || c.Exists(e.nfa, "currBidAddr") || c.Exists(e.nfa, "currBidAmt") || c.Exists(e.nfa, raw(e.carol)+"-bidDate") || c.U(e.nfa, "bidCount") != 0 {
			t.Fatalf("%s: auction state after close", tag)
		}
		// a bid after the end settles the auction to the leader and refunds the late bidder, in one call
		c.NextBlock()
		if r := e.start(e.carol, "auction", 1, 500_000, e.charity.String(), 3, true); !r.OK() {
			t.Fatalf("%s: second auction: %v", tag, r.Err)
		}
		expectBid("bob 0.5", e.bid(e.bob, 500_000), e.bob, 500_000, 510_000, 1)
		c.Timestamp = c.U(e.nfa, "endBlockTime")
		e.checkSettlement(t, tag+" late bid closes", e.bid(e.alice, 600_000), 500_000, e.carol, e.bob, 3, map[*rpc.Address]uint64{e.alice: 600_000})
		// an auction with no bids closes back to the owner
		c.NextBlock()
		if r := e.start(e.bob, "auction", 1, 500_000, "", 0, true); !r.OK() {
			t.Fatalf("%s: third auction: %v", tag, r.Err)
		}
		c.Timestamp = c.U(e.nfa, "endBlockTime")
		if r := e.call("CloseListing", nil, nil, e.bob); !r.OK() || r.Sent(e.nfa, e.bob) != 1 || len(r.Transfers) != 1 || c.S(e.nfa, "owner") != raw(e.bob) {
			t.Fatalf("%s: close without bids: %v", tag, r.Err)
		}
		e.oneUnit(t, tag+" end")
	}
}

func TestNFACancelListing(t *testing.T) {
	e := newNFAEnv(t, false, 10, 0, 250)
	c := e.c
	if r := e.call("CancelListing", nil, nil, e.creator); r.OK() {
		t.Fatal("cancelled with nothing listed")
	}
	if r := e.start(e.creator, "sale", 24, 1_000_000, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	c.NextBlock() // 18 s in, inside the 300 s buffer
	if r := e.call("CancelListing", nil, nil, e.bob); r.OK() {
		t.Fatal("a stranger cancelled")
	}
	if r := e.call("CancelListing", nil, nil, e.creator); !r.OK() || r.Sent(e.nfa, e.creator) != 1 || c.U(e.nfa, "scBalance") != 0 || c.S(e.nfa, "listType") != "" {
		t.Fatalf("cancel inside the buffer: %v", r.Err)
	}
	e.oneUnit(t, "cancelled sale")
	// cancelling an auction refunds the leading bid in the same call
	if r := e.start(e.creator, "auction", 24, 1_000_000, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := e.bid(e.bob, 1_000_000); !r.OK() {
		t.Fatal(r.Err)
	}
	c.NextBlock()
	r := e.call("CancelListing", nil, nil, e.creator)
	if !r.OK() || r.Sent(e.quote, e.bob) != 1_000_000 || r.Sent(e.nfa, e.creator) != 1 || c.Exists(e.nfa, "currBidAddr") || c.Exists(e.nfa, raw(e.bob)+"-bidDate") {
		t.Fatalf("cancel auction: %v, bob refunded %d", r.Err, r.Sent(e.quote, e.bob))
	}
	if b := c.SCBalance(e.nfa, e.quote); b != 0 {
		t.Fatalf("%d of the quote stranded by a cancel", b)
	}
	e.oneUnit(t, "cancelled auction")
	// outside the buffer the listing is binding: it runs to the end and closes to the bidder
	if r := e.start(e.creator, "auction", 24, 1_000_000, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := e.bid(e.bob, 1_000_000); !r.OK() {
		t.Fatal(r.Err)
	}
	c.Timestamp = c.U(e.nfa, "startBlockTime") + 300
	if r := e.call("CancelListing", nil, nil, e.creator); !r.OK() || r.Sent(e.quote, e.bob) != 1_000_000 {
		t.Fatalf("cancel at the buffer's last second: %v", r.Err)
	}
	if r := e.start(e.creator, "auction", 24, 1_000_000, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := e.bid(e.bob, 1_000_000); !r.OK() {
		t.Fatal(r.Err)
	}
	c.Timestamp = c.U(e.nfa, "startBlockTime") + 301
	if r := e.call("CancelListing", nil, nil, e.creator); r.OK() || c.U(e.nfa, "currBidAmt") != 1_000_000 {
		t.Fatal("cancelled outside the buffer")
	}
	c.Timestamp = c.U(e.nfa, "endBlockTime")
	e.checkSettlement(t, "close after failed cancel", e.call("CloseListing", nil, nil, e.creator), 1_000_000, e.creator, e.bob, 0, nil)
}

// ---- wrapped quote: stray DERO and the amount bound ------------------------------------------------------------

func TestNFAStrayDero(t *testing.T) {
	e := newNFAEnv(t, true, 10, 0, 250)
	c := e.c
	withDero := func(v uint64, unit bool) map[crypto.Hash]uint64 {
		m := map[crypto.Hash]uint64{zero: 1}
		if v > 0 {
			m[e.wxmr] = v
		}
		if unit {
			m[e.nfa] = 1
		}
		return m
	}
	startArgs := rpc.Arguments{harness.S("listType", "sale"), harness.U("duration", 24), harness.U("startPrice", 1_000_000), harness.S("charityDonateAddr", ""), harness.U("charityDonatePerc", 0)}
	if r := e.call("Start", startArgs, withDero(0, true), e.creator); r.OK() || c.S(e.nfa, "listType") != "" {
		t.Fatal("Start with stray DERO was accepted")
	}
	e.oneUnit(t, "refused start") // the revert gave the unit back
	if r := e.start(e.creator, "sale", 24, 1_000_000, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := e.call("BuyItNow", nil, withDero(1_000_000, false), e.bob); r.OK() || c.S(e.nfa, "owner") != raw(e.creator) || c.U(e.nfa, "scBalance") != 1 {
		t.Fatal("BuyItNow with stray DERO was accepted")
	}
	if r := e.call("CancelListing", nil, withDero(0, false), e.creator); r.OK() || c.U(e.nfa, "scBalance") != 1 {
		t.Fatal("CancelListing with stray DERO was accepted")
	}
	c.Timestamp = c.U(e.nfa, "endBlockTime")
	if r := e.call("CloseListing", nil, withDero(0, false), e.creator); r.OK() || c.U(e.nfa, "scBalance") != 1 {
		t.Fatal("CloseListing with stray DERO was accepted")
	}
	if r := e.call("CloseListing", nil, nil, e.creator); !r.OK() || r.Sent(e.nfa, e.creator) != 1 {
		t.Fatalf("close: %v", r.Err)
	}
	if r := e.start(e.creator, "auction", 24, 1_000_000, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := e.call("Bid", nil, withDero(1_000_000, false), e.bob); r.OK() || c.Exists(e.nfa, "currBidAddr") {
		t.Fatal("Bid with stray DERO was accepted")
	}
	// DERO alone is not a bid in a wrapped quote either
	if r := e.call("Bid", nil, map[crypto.Hash]uint64{zero: 1_000_000}, e.bob); r.OK() || c.Exists(e.nfa, "currBidAddr") {
		t.Fatal("a DERO-only Bid on a wrapped-quote NFA was accepted")
	}
	if b := c.SCBalance(e.nfa, zero); b != 0 {
		t.Fatalf("%d DERO stranded in a wrapped-quote NFA", b)
	}
	// the wrapped-quote path pays out only in the quote
	if r := e.bid(e.bob, 1_000_000); !r.OK() {
		t.Fatal(r.Err)
	}
	c.Timestamp = c.U(e.nfa, "endBlockTime")
	e.checkSettlement(t, "wrapped close", e.call("CloseListing", nil, nil, e.creator), 1_000_000, e.creator, e.bob, 0, nil)
}

func TestNFAAmountBound(t *testing.T) {
	// protoFee 10000 puts the largest multiplier on the largest accepted amount: 10000 * nfaMaxAmt is under 2^64
	// by 1615, and one unit more would wrap
	e := newNFAEnv(t, true, 0, 0, 10000)
	c := e.c
	if r := e.start(e.creator, "sale", 24, 1, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := e.buy(e.bob, nfaMaxAmt+1); !r.OK() || r.Sent(e.quote, e.bob) != nfaMaxAmt+1 || c.U(e.nfa, "scBalance") != 1 {
		t.Fatalf("buy above the bound: %v, refund %d", r.Err, r.Sent(e.quote, e.bob))
	}
	// at the bound the whole amount is the protocol's and the owner's remainder is 0, which sends nothing
	e.checkSettlement(t, "buy at the bound", e.buy(e.bob, nfaMaxAmt), nfaMaxAmt, e.creator, e.bob, 0, nil)
	if r := e.start(e.bob, "auction", 24, 1, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := e.bid(e.carol, nfaMaxAmt+1); !r.OK() || r.Sent(e.quote, e.carol) != nfaMaxAmt+1 || c.Exists(e.nfa, "currBidAddr") {
		t.Fatalf("bid above the bound: %v", r.Err)
	}
	if r := e.bid(e.carol, nfaMaxAmt); !r.OK() || c.U(e.nfa, "currBidAmt") != nfaMaxAmt || c.U(e.nfa, "currBidPrice") != nfaMaxAmt+10000 {
		t.Fatalf("bid at the bound: %v, next %d", r.Err, c.U(e.nfa, "currBidPrice"))
	}
	c.Timestamp = c.U(e.nfa, "endBlockTime")
	e.checkSettlement(t, "close at the bound", e.call("CloseListing", nil, nil, e.bob), nfaMaxAmt, e.bob, e.carol, 0, nil)
}
