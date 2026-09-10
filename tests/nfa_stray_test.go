package tests

// contracts/nfa.bas refuses stray DERO on the five market entrypoints of a wrapped-quote NFA (Start 25,
// BuyItNow 17, Bid 21, CloseListing 5, CancelListing 20) because RETURN 1 refunds the whole call. ClaimOwnership
// and Update carry no such guard, and on a DERO-quoted NFA the guard cannot fire at all. This asserts both
// halves, and that nothing the contract can do ever gets the stranded value out again.

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

func TestNFAStrayValueOnClaimOwnershipAndUpdateIsStranded(t *testing.T) {
	e := newNFAEnv(t, true, 10, 0, 250) // wrapped quote: DEROVALUE() != 0 is refused on market calls
	c := e.c
	updArgs := rpc.Arguments{harness.S("iconURL", ""), harness.S("coverURL", ""), harness.S("fileURL", "https://example.invalid/new.png"),
		harness.S("fileSignURL", ""), harness.S("tags", "")}

	// the guard works where it exists
	if r := e.call("Start", rpc.Arguments{harness.S("listType", "sale"), harness.U("duration", 1), harness.U("startPrice", 1000),
		harness.S("charityDonateAddr", ""), harness.U("charityDonatePerc", 0)},
		map[crypto.Hash]uint64{e.nfa: 1, zero: 777}, e.creator); r.OK() {
		t.Fatal("Start accepted stray DERO on a wrapped-quote NFA")
	}
	if b := c.SCBalance(e.nfa, zero); b != 0 {
		t.Fatalf("the refused Start left %d DERO in the contract", b)
	}

	// ClaimOwnership: no DEROVALUE guard, and the quote is not the unit, so both strand
	r := e.call("ClaimOwnership", nil, map[crypto.Hash]uint64{e.nfa: 1, zero: 12345}, e.creator)
	if !r.OK() {
		t.Fatalf("ClaimOwnership with stray DERO: %v", r.Err)
	}
	if r.Sent(zero, e.creator) != 0 {
		t.Fatalf("the stray DERO came back: %d", r.Sent(zero, e.creator))
	}
	// Update: same, and by the creator, who can call it forever
	r2 := e.call("Update", updArgs, map[crypto.Hash]uint64{zero: 500, e.quote: 6000}, e.creator)
	if !r2.OK() {
		t.Fatalf("Update with stray value: %v", r2.Err)
	}
	dero, quote := c.SCBalance(e.nfa, zero), c.SCBalance(e.nfa, e.quote)
	if dero != 12845 || quote != 6000 {
		t.Fatalf("stranded: %d DERO, %d quote — expected 12845 and 6000", dero, quote)
	}
	t.Logf("STRANDED: %d DERO and %d of the quote sit in the NFA with no entrypoint that sends them anywhere", dero, quote)

	// and a full sale afterwards pays out only what the buyer attached: the stranded value never leaves
	if rr := e.start(e.creator, "sale", 1, 1_000_000, "", 0, true); !rr.OK() {
		t.Fatal(rr.Err)
	}
	rs := e.buy(e.alice, 1_000_000)
	if !rs.OK() {
		t.Fatal(rs.Err)
	}
	var out uint64
	for _, tr := range rs.Transfers {
		if tr.Asset == e.quote {
			out += tr.Amount
		}
	}
	if out != 1_000_000 {
		t.Fatalf("the settlement moved %d of the quote, want exactly the buyer's 1000000", out)
	}
	if c.SCBalance(e.nfa, zero) != dero || c.SCBalance(e.nfa, e.quote) != quote+0 {
		t.Fatalf("stranded value changed across a settlement: %d DERO %d quote", c.SCBalance(e.nfa, zero), c.SCBalance(e.nfa, e.quote))
	}
	t.Logf("after a complete sale the stranded %d DERO / %d quote are still there: no path releases them", c.SCBalance(e.nfa, zero), c.SCBalance(e.nfa, e.quote))
}

// R12, on the DVM: the creator rewrites the file a live auction's bidders are bidding on.
func TestNFAUpdateRewritesTheFileUnderALiveBid(t *testing.T) {
	e := newNFAEnv(t, true, 10, 0, 250)
	c := e.c
	if r := e.start(e.creator, "auction", 1, 1_000_000, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := e.bid(e.alice, 2_000_000); !r.OK() {
		t.Fatal(r.Err)
	}
	before := c.S(e.nfa, "fileURL")
	r := e.call("Update", rpc.Arguments{harness.S("iconURL", ""), harness.S("coverURL", ""),
		harness.S("fileURL", "https://attacker.invalid/other.png"), harness.S("fileSignURL", ""), harness.S("tags", "")}, nil, e.creator)
	if !r.OK() {
		t.Fatalf("Update during a live auction: %v", r.Err)
	}
	if c.S(e.nfa, "fileURL") == before {
		t.Fatal("fileURL unchanged")
	}
	t.Logf("R12 confirmed: the creator changed fileURL from %q to %q while alice's 2,000,000 bid stood; ownerCanUpdate is 0 and it made no difference",
		before, c.S(e.nfa, "fileURL"))
	// and the bid is still live and still settles
	c.Timestamp = c.U(e.nfa, "endBlockTime")
	if rr := e.call("CloseListing", nil, nil, e.creator); !rr.OK() || rr.Sent(e.nfa, e.alice) != 1 {
		t.Fatalf("close: %v", rr.Err)
	}
}
