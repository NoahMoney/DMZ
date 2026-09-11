package tests

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// board.bas v3 (docs/ATOMIC_SWAP_STUDY.md §2.2, Z-E; ADV-PRICE-07 in docs/audit/FINDINGS_2026-09-10.md): Accept
// stores the acceptance deadline ad_<id>, the accept height plus aw (200 blocks, docs/ZEC_SWAP.md §5's P_lock) — the
// height by which the maker must have locked the DERO leg. From it the maker may Cancel the accepted offer. So one
// Accept from anyone holds an offer for at most aw blocks, not until its expiry (up to 1,000,000 blocks), and the
// taker's companion and the app read an accept go dead from the chain rather than from anyone's word. The board holds
// no value, so the taker — whose protection is the escrow — loses nothing when the row is cancelled.

func newBoardV3(t *testing.T) (*harness.Chain, func(expiry uint64, who *rpc.Address) harness.Result,
	func(id uint64, pkh string, cltv uint64, who *rpc.Address) harness.Result,
	func(id uint64, who *rpc.Address) harness.Result, crypto.Hash, *rpc.Address) {
	t.Helper()
	c := harness.New()
	maker := c.NewAccount()
	code := readContract(t, "board.bas")
	scid, res := c.Install(code, nil, nil, maker, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	t.Logf("board.bas: %d bytes installed, install storage gas %d of %d", len(code), res.GasStorage, harness.MaxStorageGas)
	if res.GasStorage >= harness.MaxStorageGas {
		t.Fatalf("install storage gas %d does not fit the %d cap", res.GasStorage, harness.MaxStorageGas)
	}
	hash := strings.Repeat("ab", 32)
	post := func(expiry uint64, who *rpc.Address) harness.Result {
		return c.Call(scid, "Post", rpc.Arguments{harness.S("chain", "zcash-t"), harness.S("giveAsset", ""), harness.U("giveAmount", 500000),
			harness.U("wantAmount", 12345678), harness.S("wantAddr", "t1MakerTransparentAddress"), harness.S("hash", hash), harness.U("expiry", expiry)}, nil, who)
	}
	accept := func(id uint64, pkh string, cltv uint64, who *rpc.Address) harness.Result {
		return c.Call(scid, "Accept", rpc.Arguments{harness.U("id", id), harness.S("refundPkh", pkh), harness.U("cltv", cltv)}, nil, who)
	}
	cancel := func(id uint64, who *rpc.Address) harness.Result {
		return c.Call(scid, "Cancel", rpc.Arguments{harness.U("id", id)}, nil, who)
	}
	return c, post, accept, cancel, scid, maker
}

func TestBoardV3AcceptanceDeadline(t *testing.T) {
	c, post, accept, cancel, scid, maker := newBoardV3(t)
	taker, griefer, stranger := c.NewAccount(), c.NewAccount(), c.NewAccount()
	pkh := strings.Repeat("cd", 20)

	// ADV-PRICE-07, inverted: an offer at the longest expiry the board allows, bound by an accept that will never fund.
	far := c.Height + 1_000_000
	if r := post(far, maker); !r.OK() {
		t.Fatalf("post: %v", r.Err)
	}
	if r := accept(0, strings.Repeat("00", 20), 1, griefer); !r.OK() {
		t.Fatalf("accept: %v", r.Err)
	}
	at := c.Height
	// while the accept is live, the honest taker is locked out and the maker cannot cancel
	if r := accept(0, pkh, 2_500_000, taker); r.OK() {
		t.Fatal("a second Accept succeeded on a live accept")
	}
	if r := cancel(0, maker); r.OK() {
		t.Fatal("the maker cancelled an accepted offer in the block it was accepted")
	}
	c.Height = at + 199
	if r := cancel(0, maker); r.OK() {
		t.Fatal("the maker cancelled an accepted offer one block before its acceptance deadline")
	}
	c.Height = at + 200
	// The deadline does not re-open the row to another taker: the maker's Lock, if one was made in time, names the
	// first taker, and a second Accept would overwrite the refund key hash and cltv the maker's script was built from.
	if r := accept(0, pkh, 2_500_000, taker); r.OK() {
		t.Fatal("an Accept after the acceptance deadline re-bound the offer")
	}
	for _, who := range []*rpc.Address{griefer, stranger} {
		if r := cancel(0, who); r.OK() {
			t.Fatal("someone other than the maker cancelled")
		}
	}
	if r := cancel(0, maker); !r.OK() {
		t.Fatalf("the maker could not cancel an accepted offer at its acceptance deadline, %d blocks before its expiry: %v", far-c.Height, r.Err)
	}
	if c.U(scid, "st_0") != 2 {
		t.Fatalf("st_0 %d after cancel, want 2", c.U(scid, "st_0"))
	}
	if r := cancel(0, maker); r.OK() {
		t.Fatal("cancelled twice")
	}
	t.Logf("one Accept from anyone now holds an offer for %d blocks (%d min at 18 s), not the %d blocks to its expiry", c.Height-at, (c.Height-at)*18/60, far-at)

	// The deadline is on chain, where the taker's companion and the app read it.
	aw := c.U(scid, "aw")
	if aw != 200 {
		t.Fatalf("aw %d, want 200 (docs/ZEC_SWAP.md §5 P_lock)", aw)
	}
	if ad := c.U(scid, "ad_0"); ad != at+aw {
		t.Fatalf("ad_0 %d, want the accept height %d + %d", ad, at, aw)
	}

	// An accept near the offer's expiry: the expiry comes first, and the maker cancels from it (v2's exit, kept).
	exp := c.Height + 5
	if r := post(exp, maker); !r.OK() {
		t.Fatalf("post 1: %v", r.Err)
	}
	c.NextBlock()
	if r := accept(1, pkh, 2_500_000, taker); !r.OK() || c.U(scid, "ad_1") != c.Height+aw {
		t.Fatalf("accept 1: %v", r.Err)
	}
	if r := cancel(1, maker); r.OK() {
		t.Fatal("cancelled an accepted offer before both its expiry and its deadline")
	}
	c.Height = exp
	if r := cancel(1, maker); !r.OK() || c.U(scid, "st_1") != 2 {
		t.Fatalf("cancel at expiry, before the deadline: %v", r.Err)
	}

	// An open offer carries no deadline, and the maker cancels it at any time, as in v2.
	if r := post(c.Height+100, maker); !r.OK() {
		t.Fatalf("post 2: %v", r.Err)
	}
	if c.Exists(scid, "ad_2") {
		t.Fatal("an offer nobody accepted carries an acceptance deadline")
	}
	if r := cancel(2, maker); !r.OK() {
		t.Fatalf("cancel of an open offer: %v", r.Err)
	}

	if v := c.U(scid, "v"); v != 3 {
		t.Fatalf("board version %d, want 3", v)
	}
}

// cl_<id> is documented as a height. A transparent Zcash script's CHECKLOCKTIMEVERIFY reads any value from
// 500,000,000 (LOCKTIME_THRESHOLD, BIP 65) as a Unix time, and every such time is already past, so a refund
// "height" there is a refund the taker can make at once — a sale she can revoke (ZEC-3). The Zcash tip bound stays
// the companions' (the DVM cannot see Zcash); this constant one the board can enforce itself.
func TestBoardV3AcceptRefusesAUnixTimeRefundHeight(t *testing.T) {
	c, post, accept, _, scid, maker := newBoardV3(t)
	taker := c.NewAccount()
	pkh := strings.Repeat("cd", 20)
	if r := post(c.Height+100, maker); !r.OK() {
		t.Fatalf("post: %v", r.Err)
	}
	for _, cltv := range []uint64{500_000_000, 1_600_000_000, 1<<32 - 1, 1 << 63} {
		if r := accept(0, pkh, cltv, taker); r.OK() {
			t.Fatalf("Accept took cltv %d, which a Zcash script reads as a Unix time", cltv)
		}
	}
	if c.U(scid, "st_0") != 0 {
		t.Fatal("a refused Accept bound the offer")
	}
	if r := accept(0, pkh, 499_999_999, taker); !r.OK() || c.U(scid, "cl_0") != 499_999_999 {
		t.Fatalf("the largest height was refused: %v", r.Err)
	}
}

// GAP-027: the board holds nothing, but v2 refused only DERO. v3 also refuses the offer's give asset attached to
// Post, Accept or Cancel — the one asset the DVM can name there — and a refused call's value goes back to its ring-2
// signer, so nothing is kept. An asset the offer does not name cannot be seen: the DVM has no builtin that lists
// what a call carries.
func TestBoardV3RefusesTheGiveAssetAttached(t *testing.T) {
	c := harness.New()
	maker, taker := c.NewAccount(), c.NewAccount()
	scid, res := c.Install(readContract(t, "board.bas"), nil, nil, maker, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	var asset crypto.Hash
	asset[0] = 0x42
	extra := map[crypto.Hash]uint64{asset: 5}
	post := func(in map[crypto.Hash]uint64) harness.Result {
		return c.Call(scid, "Post", rpc.Arguments{harness.S("chain", "zcash-t"), harness.S("giveAsset", hex.EncodeToString(asset[:])), harness.U("giveAmount", 500),
			harness.U("wantAmount", 100000), harness.S("wantAddr", "t1MakerTransparentAddress"), harness.S("hash", strings.Repeat("ab", 32)), harness.U("expiry", c.Height+1000)}, in, maker)
	}
	accept := func(in map[crypto.Hash]uint64) harness.Result {
		return c.Call(scid, "Accept", rpc.Arguments{harness.U("id", 0), harness.S("refundPkh", strings.Repeat("cd", 20)), harness.U("cltv", 2_500_000)}, in, taker)
	}
	cancel := func(in map[crypto.Hash]uint64) harness.Result {
		return c.Call(scid, "Cancel", rpc.Arguments{harness.U("id", 0)}, in, maker)
	}
	if r := post(extra); r.OK() {
		t.Fatal("Post with the offer's give asset attached was accepted: the board would keep it for ever")
	}
	if c.U(scid, "n") != 0 {
		t.Fatal("the refused Post wrote a row")
	}
	if r := post(nil); !r.OK() {
		t.Fatalf("post: %v", r.Err)
	}
	if r := accept(extra); r.OK() {
		t.Fatal("Accept with the offer's give asset attached was accepted")
	}
	if c.U(scid, "st_0") != 0 {
		t.Fatal("the refused Accept bound the offer")
	}
	if r := accept(nil); !r.OK() {
		t.Fatalf("accept: %v", r.Err)
	}
	c.Height += c.U(scid, "aw")
	if r := cancel(extra); r.OK() {
		t.Fatal("Cancel with the offer's give asset attached was accepted")
	}
	if c.U(scid, "st_0") != 1 {
		t.Fatal("the refused Cancel moved the offer")
	}
	if r := cancel(nil); !r.OK() || c.U(scid, "st_0") != 2 {
		t.Fatalf("cancel at the deadline: %v", r.Err)
	}
	if b := c.SCBalance(scid, asset); b != 0 {
		t.Fatalf("the board kept %d of the asset", b)
	}
}
