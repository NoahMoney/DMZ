package tests

// Adversarial cases for the price / quote / liquidity / counterparty dimension. Every test here reproduces a
// claim in the audit report: it asserts the behaviour that is wrong, so it fails the day the behaviour changes.

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// R4 / BL-68 / ADV-PRICE-01 (was TestNFAEndedAuctionBuyItNowStrandsTheWinningBid). checkActive returns 111 for
// ANY ended listing before it looks at the type (40 precedes 50), so BuyItNow on an ended auction reaches its 111
// branch. That branch used to send the unit back to the owner and leave the winning bid in the contract, with no
// call the winner could make to get it back; it now settles the auction to the winner through processHighestBidder
// (BuyItNow 512-516), so a third party's empty BuyItNow does exactly what the owner's CloseListing does.
func TestNFAEndedAuctionBuyItNowSettlesTheWinningBid(t *testing.T) {
	e := newNFAEnv(t, true, 5, 0, 50)
	c := e.c
	const price = 1_000_000_000
	if r := e.start(e.creator, "auction", 1, price, "", 0, true); !r.OK() {
		t.Fatalf("start: %v", r.Err)
	}
	if r := e.bid(e.alice, price); !r.OK() || c.S(e.nfa, "currBidAddr") != raw(e.alice) {
		t.Fatalf("alice bid: %v", r.Err)
	}
	c.Timestamp = c.U(e.nfa, "endBlockTime") // the auction has ended; alice has won it

	// carol is neither the owner nor a bidder, and attaches nothing
	e.checkSettlement(t, "carol's empty BuyItNow on the ended auction", e.buy(e.carol, 0), price, e.creator, e.alice, 0, nil)

	// the listing is over: nothing is left for Bid, CloseListing or CancelListing to act on
	if rr := e.bid(e.alice, 0); !rr.OK() || len(rr.Transfers) != 0 {
		t.Fatalf("an empty Bid after the settlement: %v %+v", rr.Err, rr.Transfers)
	}
	if rr := e.call("CloseListing", nil, nil, e.alice); rr.OK() {
		t.Fatal("CloseListing succeeded with nothing listed")
	}
	if rr := e.call("CancelListing", nil, nil, e.alice); rr.OK() {
		t.Fatal("CancelListing succeeded with nothing listed")
	}
	e.drain(t)
	t.Logf("a stranger's empty BuyItNow settled the ended auction to its winner: alice holds the unit, the seller was paid %d", uint64(price)-uint64(price)*5/100-uint64(price)*50/10000)
}

// R4 / R11. generateEndBlock caps the INITIAL duration at 604,800 s; Bid 170-180 then resets endBlockTime to
// BLOCK_TIMESTAMP()+900 on every bid inside the last 900 s, with no reference to that cap. Two addresses take
// turns: each is refunded when the other outbids, so nothing is locked beyond one bid, and the price they must
// keep meeting rises only by staticBidIncr = 10,000 atomic units - a hundred-millionth of an XMR on a
// twelve-decimal quote.
func TestNFAAntiSnipeExtensionHasNoCap(t *testing.T) {
	e := newNFAEnv(t, true, 0, 0, 50)
	c := e.c
	const price = 1_000_000
	if r := e.start(e.creator, "auction", 1, price, "", 0, true); !r.OK() {
		t.Fatalf("start: %v", r.Err)
	}
	start := c.U(e.nfa, "startBlockTime")
	who := []*rpc.Address{e.alice, e.bob}
	var amt uint64
	const rounds = 700 // 700 * 900 s = 175 hours, well past the 7-day cap on the initial duration
	for i := 0; i < rounds; i++ {
		c.Timestamp = c.U(e.nfa, "endBlockTime") - 1 // the last second before the end
		if amt = c.U(e.nfa, "currBidPrice"); amt < price {
			amt = price
		}
		if r := e.bid(who[i%2], amt); !r.OK() || c.U(e.nfa, "bidCount") != uint64(i+1) {
			t.Fatalf("round %d bid %d: %v (bidCount %d)", i, amt, r.Err, c.U(e.nfa, "bidCount"))
		}
		if got := c.U(e.nfa, "endBlockTime"); got != c.Timestamp+900 {
			t.Fatalf("round %d: end %d, want %d", i, got, c.Timestamp+900)
		}
	}
	ran := c.U(e.nfa, "endBlockTime") - start
	if ran <= 604800 {
		t.Fatalf("the auction ran %d s, inside the 7-day cap - the extension is capped, update the report", ran)
	}
	if c.U(e.nfa, "scBalance") != 1 {
		t.Fatal("the unit left the contract")
	}
	t.Logf("auction held open for %d s (%.1f days) by %d alternating bids; the price to meet rose from %d to %d, i.e. %d units of the quote in total",
		ran, float64(ran)/86400, rounds, uint64(price), amt, amt-price)
	// the owner has no exit while it runs
	if r := e.call("CloseListing", nil, nil, e.creator); r.OK() {
		t.Fatal("CloseListing succeeded on a live auction - update the report")
	}
	if r := e.call("CancelListing", nil, nil, e.creator); r.OK() {
		t.Fatal("CancelListing succeeded outside the 300 s buffer - update the report")
	}
}

// ADV-PRICE-07, repaired by board.bas v3 (this was TestBoardAcceptByAnyoneFreezesAnOfferUntilExpiry). Accept is
// still permissionless and one-shot: any address that is not the maker binds an open offer with a 40-character
// string it never has to honour, and a second Accept is refused. v2's Cancel then refused the accepted offer until
// its expiry, which the maker may have set up to 1,000,000 blocks (~208 days) out. v3 records the acceptance
// deadline ad_<id> = the accept height + aw (200) and lets the maker Cancel from it, so the freeze lasts an hour.
// The residual — one Accept per offer per hour, repeatable — is GAP-026 in docs/MAINNET_GAP_ANALYSIS.md.
func TestBoardAcceptByAnyoneHoldsAnOfferOnlyUntilItsAcceptanceDeadline(t *testing.T) {
	c := harness.New()
	maker, griefer, taker := c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid, res := c.Install(readContract(t, "board.bas"), nil, nil, maker, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	expiry := c.Height + 1_000_000 // the maximum the contract allows
	post := rpc.Arguments{harness.S("chain", "zcash-t"), harness.S("giveAsset", ""), harness.U("giveAmount", 500000),
		harness.U("wantAmount", 12345678), harness.S("wantAddr", "t1MakerTransparentAddress"),
		harness.S("hash", strings.Repeat("ab", 32)), harness.U("expiry", expiry)}
	if r := c.Call(scid, "Post", post, nil, maker); !r.OK() {
		t.Fatalf("post: %v", r.Err)
	}
	junk := rpc.Arguments{harness.U("id", 0), harness.S("refundPkh", strings.Repeat("00", 20)), harness.U("cltv", 1)}
	if r := c.Call(scid, "Accept", junk, nil, griefer); !r.OK() {
		t.Fatalf("the griefer could not accept: %v", r.Err)
	}
	if c.U(scid, "st_0") != 1 {
		t.Fatal("Accept did not bind the offer")
	}
	// the honest taker is now locked out
	if r := c.Call(scid, "Accept", rpc.Arguments{harness.U("id", 0), harness.S("refundPkh", strings.Repeat("cd", 20)), harness.U("cltv", 2_500_000)}, nil, taker); r.OK() {
		t.Fatal("a second Accept succeeded - update the report")
	}
	// the maker cannot take the row back inside the acceptance window...
	at, aw := c.Height, c.U(scid, "aw")
	cancel := rpc.Arguments{harness.U("id", 0)}
	if r := c.Call(scid, "Cancel", cancel, nil, maker); r.OK() {
		t.Fatal("the maker cancelled an accepted offer in the block it was accepted")
	}
	c.Height = at + aw - 1
	if r := c.Call(scid, "Cancel", cancel, nil, maker); r.OK() {
		t.Fatal("the maker cancelled an accepted offer before its acceptance deadline")
	}
	// ...and from the deadline he can, nearly a million blocks before the offer's expiry
	c.Height = at + aw
	if c.U(scid, "ad_0") != c.Height {
		t.Fatalf("ad_0 %d, want the accept height %d + aw %d", c.U(scid, "ad_0"), at, aw)
	}
	if r := c.Call(scid, "Cancel", cancel, nil, maker); !r.OK() || c.U(scid, "st_0") != 2 {
		t.Fatalf("cancel at the acceptance deadline, %d blocks before expiry: %v", expiry-c.Height, r.Err)
	}
	t.Logf("one Accept transaction now holds the offer for %d blocks (~%.0f min at 18 s), not the %d to its expiry", aw, float64(aw)*18/60, expiry-at)
}

// launch.bas checks only STRLEN(quote) == 32: the quote may be any asset, including one the shipped config does
// not name. Nothing on chain records a quote's decimals, so a client with no record for the quote has no scale
// for any amount on that market.
func TestLaunchInstallsAndTradesInAQuoteNothingNames(t *testing.T) {
	c := harness.New()
	treasury, creator, alice := c.NewAccount(), c.NewAccount(), c.NewAccount()
	unk, res := c.Install(assetCode, nil, nil, creator, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("stand-in asset install: %v", res.Err)
	}
	if r := c.Call(unk, "Mint", rpc.Arguments{harness.U("amount", 1<<40)}, nil, alice); !r.OK() {
		t.Fatalf("mint: %v", r.Err)
	}
	args := launchArgs(launchParams{supply: 1_000_000 * 100000, grad: 1_000_000, cbps: 0, tax: 0, tmode: 0, fee: 50, pfee: 5000}, treasury.String())
	for i := range args {
		if args[i].Name == "quote" {
			args[i] = harness.H("quote", unk)
		}
	}
	scid, res := c.Install(readContract(t, "launch.bas"), nil, args, creator, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("a launch quoted in an unnamed asset was refused: %v", res.Err)
	}
	if got := c.S(scid, "qt"); got != string(unk[:]) {
		t.Fatalf("qt %x, want %x", got, unk[:])
	}
	r := c.Call(scid, "SwapDeroForAsset", rpc.Arguments{harness.U("min_out", 1), harness.U("publish", 0)}, map[crypto.Hash]uint64{unk: 100000}, alice)
	if !r.OK() || r.Sent(scid, alice) == 0 {
		t.Fatalf("buy in the unnamed quote: %v %v", r.Err, r.Transfers)
	}
	t.Logf("a launch quoted in %s… (an asset no deployment record names) installed and traded; the contract stores no decimals for it", hex.EncodeToString(unk[:8]))
}

// stats.js enrich(): m.hot = f1.blocks*4 + pub1.who*3 + min(50, log1p(vol/qscale)*8), and feed.trending() sorts
// Home, the price tape and the Trade hub by it. The anti-wash note beside it guards the publisher term (distinct
// addresses) and the volume term (a log), but not f1.blocks - the count of blocks in the last hour in which the
// reserves moved at all, weight 4, no cap below the ~200 blocks an hour holds. This measures what one such block
// costs: the smallest buy pool.bas will accept, and the storage gas the network charges for it.
func TestPoolTheCheapestReserveMoveCostsOnlyGas(t *testing.T) {
	e := newPoolEnv(t, 50, 5000) // the shipped fee: 0.5%, half of it to the treasury
	if r := e.c.Call(e.pool, "AddLiquidity", rpc.Arguments{harness.U("min_lp", 1)}, e.in(100_000_000, 100_000_000, 0), e.alice); !r.OK() {
		t.Fatalf("seed: %v", r.Err)
	}
	var min, gas uint64
	for d := uint64(1); d <= 1000; d++ {
		r := e.c.Call(e.pool, "SwapDeroForAsset", rpc.Arguments{harness.U("min_out", 1), harness.U("publish", 0)}, e.in(d, 0, 0), e.bob)
		if r.OK() {
			min, gas = d, r.GasStorage
			break
		}
	}
	if min == 0 {
		t.Fatal("no buy under 1000 atomic units was accepted")
	}
	before := e.c.U(e.pool, "rd")
	e.c.NextBlock()
	if r := e.c.Call(e.pool, "SwapDeroForAsset", rpc.Arguments{harness.U("min_out", 1), harness.U("publish", 0)}, e.in(min, 0, 0), e.bob); !r.OK() {
		t.Fatalf("second dust buy: %v", r.Err)
	}
	if e.c.U(e.pool, "rd") == before {
		t.Fatal("the dust buy did not move rd, so stats.js flow() would not count the block - update the report")
	}
	if e.c.U(e.pool, "pc") == 0 {
		t.Fatal("no price point was written, so the block would not be counted - update the report")
	}
	t.Logf("smallest accepted buy %d atomic DERO (%.5f DERO), storage gas %d, network fee gas+200 = %d atomic (%.5f DERO); an hour of 18 s blocks is ~200 such blocks, i.e. hot = 800 for about %.2f DERO",
		min, float64(min)/1e5, gas, gas+200, float64(gas+200)/1e5, float64(200*(gas+200+min))/1e5)
}
