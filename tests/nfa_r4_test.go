package tests

// docs/NFA.md §7 R4 second half ("the close that strands a bid") says the case is OPEN and that "a §5.A test —
// three bidders, the end passes, a fourth wallet calls BuyItNow with nothing attached — decides it on the real
// DVM in minutes". These are those tests, on a wrapped quote, plus the chain the note reasons about but has
// never run: after the strand, the asset changes hands, the NEW owner lists at a NEW price, and any wallet can
// settle that listing to the OLD bidder at the OLD price.

import (
	"testing"

	"github.com/deroproject/derohe/rpc"
)

// TestNFAStrandedBidByBuyItNow: an ended auction with a live top bid, closed by a stranger's empty BuyItNow.
// The unit goes back to the owner; the bid is neither paid nor refunded and stays under currBidAddr/currBidAmt.
func TestNFAStrandedBidByBuyItNow(t *testing.T) {
	e := newNFAEnv(t, true, 10, 0, 250)
	c := e.c
	if r := e.start(e.creator, "auction", 1, 1_000_000, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := e.bid(e.alice, 2_000_000); !r.OK() {
		t.Fatal(r.Err)
	}
	if b := c.SCBalance(e.nfa, e.quote); b != 2_000_000 {
		t.Fatalf("contract holds %d of the quote after the bid, want 2000000", b)
	}
	c.Timestamp = c.U(e.nfa, "endBlockTime") // the auction has ended

	// bob is not the owner and attaches nothing at all
	r := e.buy(e.bob, 0)
	if !r.OK() {
		t.Fatalf("stranger's empty BuyItNow on an ended auction: %v", r.Err)
	}
	if r.Sent(e.nfa, e.creator) != 1 || c.U(e.nfa, "scBalance") != 0 || c.S(e.nfa, "listType") != "" {
		t.Fatalf("the unit did not go back to the owner: %v", r.Transfers)
	}
	if got := r.Sent(e.quote, e.alice); got != 0 {
		t.Fatalf("alice was refunded %d — the strand did not happen", got)
	}
	if amt := c.U(e.nfa, "currBidAmt"); amt != 2_000_000 || c.S(e.nfa, "currBidAddr") != raw(e.alice) {
		t.Fatalf("currBidAmt %d / currBidAddr %s after the close, want alice's 2000000", amt, e.who(c.S(e.nfa, "currBidAddr")))
	}
	if b := c.SCBalance(e.nfa, e.quote); b != 2_000_000 {
		t.Fatalf("contract holds %d of the quote, want alice's stranded 2000000", b)
	}
	e.oneUnit(t, "stranded")
	t.Logf("STRANDED: unit back to the owner, alice's 2,000,000 of the quote left in the contract with no call she can make to get it back")

	// and nothing alice can do alone recovers it: she is not the owner, so Cancel/Close refuse, and a Bid with
	// no live listing refuses too
	for _, entry := range []string{"CancelListing", "CloseListing"} {
		if rr := e.call(entry, nil, nil, e.alice); rr.OK() {
			t.Fatalf("%s by the stranded bidder succeeded", entry)
		}
	}
	if rr := e.bid(e.alice, 0); rr.OK() && rr.Sent(e.quote, e.alice) != 0 {
		t.Fatalf("an empty Bid refunded the stranded bid")
	}
	if b := c.SCBalance(e.nfa, e.quote); b != 2_000_000 {
		t.Fatalf("the stranded bid moved: %d", b)
	}
}

// TestNFAStaleBidSettlesANewOwnersListing is the consequence the note reasons about and nobody has run: the
// stranded bid survives a change of owner and settles a LATER listing, at the OLD price, to the OLD bidder, on a
// call any wallet can make.
func TestNFAStaleBidSettlesANewOwnersListing(t *testing.T) {
	e := newNFAEnv(t, true, 10, 0, 250)
	c := e.c
	const stale = 2_000_000

	// 1. auction, one bid, ended, closed by a stranger's empty BuyItNow -> stale bid parked
	if r := e.start(e.creator, "auction", 1, 1_000_000, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := e.bid(e.alice, stale); !r.OK() {
		t.Fatal(r.Err)
	}
	c.Timestamp = c.U(e.nfa, "endBlockTime")
	if r := e.buy(e.bob, 0); !r.OK() || c.U(e.nfa, "currBidAmt") != stale {
		t.Fatalf("strand: %v currBidAmt %d", r.Err, c.U(e.nfa, "currBidAmt"))
	}

	// 2. the creator sells it to carol for 50,000,000 in a plain sale — settlement works, the stale bid is untouched
	c.NextBlock()
	if r := e.start(e.creator, "sale", 1, 50_000_000, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := e.buy(e.carol, 50_000_000); !r.OK() || c.S(e.nfa, "owner") != raw(e.carol) {
		t.Fatalf("sale to carol: %v owner %s", r.Err, e.who(c.S(e.nfa, "owner")))
	}
	if c.U(e.nfa, "currBidAmt") != stale || c.SCBalance(e.nfa, e.quote) != stale {
		t.Fatalf("the stale bid did not survive the sale: currBidAmt %d balance %d", c.U(e.nfa, "currBidAmt"), c.SCBalance(e.nfa, e.quote))
	}

	// 3. carol, the new owner, lists an auction asking a hundred times the stale bid. Nobody bids.
	c.NextBlock()
	if r := e.start(e.carol, "auction", 1, 200_000_000, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	if c.U(e.nfa, "bidCount") != 0 {
		t.Fatal("bidCount was not reset")
	}
	c.Timestamp = c.U(e.nfa, "endBlockTime")

	// 4. bob — not the owner, not a bidder, nothing attached — calls Bid(). processHighestBidder finds the STALE
	//    record and settles carol's 200,000,000 listing to alice for 2,000,000.
	before := c.SCBalance(e.nfa, e.quote)
	r := e.bid(e.bob, 0)
	if !r.OK() {
		t.Fatalf("empty Bid after the end: %v", r.Err)
	}
	if r.Sent(e.nfa, e.alice) != 1 || c.S(e.nfa, "owner") != raw(e.alice) {
		t.Fatalf("the unit went to %v, owner %s — expected the stale bidder alice", r.Transfers, e.who(c.S(e.nfa, "owner")))
	}
	royalty, proto := e.royalty*stale/100, e.protoFee*stale/10000
	payout := stale - royalty - proto
	if got := r.Sent(e.quote, e.carol); got != payout {
		t.Fatalf("carol received %d, want the stale payout %d", got, payout)
	}
	if c.U(e.nfa, "previousAuctionPrice") != stale {
		t.Fatalf("previousAuctionPrice %d", c.U(e.nfa, "previousAuctionPrice"))
	}
	if b := c.SCBalance(e.nfa, e.quote); b != 0 || before != stale {
		t.Fatalf("contract balance before %d after %d", before, b)
	}
	e.oneUnit(t, "stale settlement")
	t.Logf("STALE SETTLEMENT: carol asked %d and received %d; alice, who bid %d on a listing two owners ago and never bid on this one, took the asset. The call was made by bob, who is neither party.",
		200_000_000, payout, stale)
}

// TestNFAStaleBidAlsoSettlesASale: the same, on a SALE listing — checkActive returns 111 for any ended listing
// before it looks at the type, so an empty Bid() settles an ended sale to the stale auction bidder too.
func TestNFAStaleBidAlsoSettlesASale(t *testing.T) {
	e := newNFAEnv(t, true, 0, 0, 250)
	c := e.c
	const stale = 3_000_000
	if r := e.start(e.creator, "auction", 1, 1_000_000, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := e.bid(e.alice, stale); !r.OK() {
		t.Fatal(r.Err)
	}
	c.Timestamp = c.U(e.nfa, "endBlockTime")
	if r := e.buy(e.bob, 0); !r.OK() {
		t.Fatal(r.Err)
	}
	c.NextBlock()
	if r := e.start(e.creator, "sale", 1, 900_000_000, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	c.Timestamp = c.U(e.nfa, "endBlockTime")
	r := e.bid(e.carol, 0)
	if !r.OK() {
		t.Fatalf("empty Bid on an ended sale: %v", r.Err)
	}
	if r.Sent(e.nfa, e.alice) != 1 || c.S(e.nfa, "owner") != raw(e.alice) {
		t.Fatalf("ended SALE at 900,000,000 was not taken by the stale bidder: %v owner %s", r.Transfers, e.who(c.S(e.nfa, "owner")))
	}
	t.Logf("an ended SALE asking 900,000,000 settled to the stale auction bidder for %d", stale)
	e.oneUnit(t, "stale sale settlement")
}

var _ = rpc.Arguments{}
