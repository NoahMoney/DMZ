package tests

// docs/NFA.md §7 R4 second half ("the close that strands a bid") and FINDINGS_2026-09-10 NFA-1 / LIV-02 /
// ADV-PRICE-01. These were written to show the defect on the real DVM: an ended auction closed by a stranger's
// empty BuyItNow sent the unit back to the owner and left the winning bid in the contract under
// currBidAddr/currBidAmt, which resetVars did not clear; the stale record then settled a LATER listing, sale or
// auction, to the old bidder at the old price, on an empty Bid any wallet could send. nfa.bas now settles an ended
// auction to its winner from BuyItNow (512-516) as Bid and CloseListing do, so every way an auction ends settles or
// refunds its bid and a bid lives and dies with its listing. Each test asserts the repair and gives its old name.

import (
	"testing"

	"github.com/deroproject/derohe/rpc"
)

// noBid asserts that no bid record survives the listing that took it.
func (e *nfaEnv) noBid(t *testing.T, tag string) {
	t.Helper()
	if e.c.Exists(e.nfa, "currBidAddr") || e.c.Exists(e.nfa, "currBidAmt") {
		t.Fatalf("%s: a bid record outlived its listing", tag)
	}
}

// Was TestNFAStrandedBidByBuyItNow. An ended auction with a live top bid, closed by a stranger's empty BuyItNow:
// the stranger's call settles it to the highest bidder, by the split, exactly as the owner's CloseListing would.
func TestNFAEmptyBuyItNowSettlesAnEndedAuctionToItsWinner(t *testing.T) {
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
	e.checkSettlement(t, "stranger's empty BuyItNow", e.buy(e.bob, 0), 2_000_000, e.creator, e.alice, 0, nil)
	if c.U(e.nfa, "previousAuctionPrice") != 2_000_000 {
		t.Fatalf("previousAuctionPrice %d", c.U(e.nfa, "previousAuctionPrice"))
	}
	e.noBid(t, "after the settlement")
	// nothing is left to settle: the new owner's CloseListing finds no listing, a second empty BuyItNow moves nothing
	if rr := e.call("CloseListing", nil, nil, e.alice); rr.OK() {
		t.Fatal("CloseListing succeeded with nothing listed")
	}
	if rr := e.buy(e.bob, 0); !rr.OK() || len(rr.Transfers) != 0 {
		t.Fatalf("a second empty BuyItNow: %v %+v", rr.Err, rr.Transfers)
	}
	e.drain(t)
	t.Logf("SETTLED: the stranger's empty BuyItNow handed alice the unit for her 2,000,000 bid and paid the seller by the split")
}

// Was TestNFAStaleBidSettlesANewOwnersListing. The chain the note reasoned about: an ended auction, a change of
// owner, a new listing at a new price that nobody bids on. The stale record that used to settle the new listing
// to the first auction's bidder at the first auction's price no longer exists at any step.
func TestNFANoBidOutlivesItsListing(t *testing.T) {
	e := newNFAEnv(t, true, 10, 0, 250)
	c := e.c
	const first = 2_000_000

	// 1. auction, one bid, ended, closed by a stranger's empty BuyItNow: it settles to alice
	if r := e.start(e.creator, "auction", 1, 1_000_000, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := e.bid(e.alice, first); !r.OK() {
		t.Fatal(r.Err)
	}
	c.Timestamp = c.U(e.nfa, "endBlockTime")
	e.checkSettlement(t, "first auction", e.buy(e.bob, 0), first, e.creator, e.alice, 0, nil)
	e.noBid(t, "first auction")

	// 2. alice sells it to carol for 50,000,000 in a plain sale
	c.NextBlock()
	if r := e.start(e.alice, "sale", 1, 50_000_000, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	e.checkSettlement(t, "sale to carol", e.buy(e.carol, 50_000_000), 50_000_000, e.alice, e.carol, 0, nil)
	e.noBid(t, "sale to carol")

	// 3. carol, the new owner, lists an auction at 200,000,000. Nobody bids.
	c.NextBlock()
	if r := e.start(e.carol, "auction", 1, 200_000_000, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	c.Timestamp = c.U(e.nfa, "endBlockTime")

	// 4. bob — not the owner, not a bidder, nothing attached — calls Bid(): the unit goes back to carol and nobody
	//    is paid anything
	r := e.bid(e.bob, 0)
	if !r.OK() || len(r.Transfers) != 1 || r.Sent(e.nfa, e.carol) != 1 || c.S(e.nfa, "owner") != raw(e.carol) {
		t.Fatalf("empty Bid after an unbid auction: %v, transfers %+v, owner %s", r.Err, r.Transfers, e.who(c.S(e.nfa, "owner")))
	}
	if c.S(e.nfa, "listType") != "" || c.U(e.nfa, "scBalance") != 0 || c.U(e.nfa, "previousAuctionPrice") != first {
		t.Fatal("the unbid auction was not closed back to its owner")
	}
	e.oneUnit(t, "unbid auction closed")
	e.drain(t)
}

// Was TestNFAStaleBidAlsoSettlesASale. checkActive answers 111 for any ended listing before it compares the type, so
// an empty Bid() on an ended SALE reaches processHighestBidder. With no bid able to outlive its listing, that closes
// an ended sale only back to its owner: the unit returns and no quote moves.
func TestNFAAnEndedSaleSettlesOnlyToItsOwner(t *testing.T) {
	e := newNFAEnv(t, true, 0, 0, 250)
	c := e.c
	const first = 3_000_000
	if r := e.start(e.creator, "auction", 1, 1_000_000, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := e.bid(e.alice, first); !r.OK() {
		t.Fatal(r.Err)
	}
	c.Timestamp = c.U(e.nfa, "endBlockTime")
	e.checkSettlement(t, "auction", e.buy(e.bob, 0), first, e.creator, e.alice, 0, nil)
	c.NextBlock()
	if r := e.start(e.alice, "sale", 1, 900_000_000, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	c.Timestamp = c.U(e.nfa, "endBlockTime")
	r := e.bid(e.carol, 0)
	if !r.OK() || len(r.Transfers) != 1 || r.Sent(e.nfa, e.alice) != 1 || c.S(e.nfa, "owner") != raw(e.alice) {
		t.Fatalf("empty Bid on an ended sale: %v, transfers %+v, owner %s", r.Err, r.Transfers, e.who(c.S(e.nfa, "owner")))
	}
	e.oneUnit(t, "ended sale closed")
	e.drain(t)
}

var _ = rpc.Arguments{}
