package tests

// NFA-4 (FINDINGS_2026-09-10). contracts/nfa.bas used to refuse stray value on its market entrypoints only when the
// quote was not DERO, and ClaimOwnership and Update refused nothing, so value attached where a call takes none stayed
// in the contract for ever. Every entrypoint now refuses, with RETURN 1, any asset it does not take: the DVM discards
// the call and, at ring size 2, hands every attached asset back to the signer (nfa.bas header item 8). Each refused
// case below is one the old contract accepted and kept the value of. The one exception is kept on purpose: above
// ring 2 ClaimOwnership sends the unit to the recorded owner, as the standard does, because a refusal there could
// refund nobody and would destroy the asset's only unit; what else was attached stays. This file was
// TestNFAStrayValueOnClaimOwnershipAndUpdateIsStranded.

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

func TestNFAStrayValueIsRefused(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		tag := "dero"
		if wrapped {
			tag = "wrapped"
		}
		e := newNFAEnv(t, wrapped, 10, 0, 250)
		c := e.c
		with := func(parts ...map[crypto.Hash]uint64) map[crypto.Hash]uint64 {
			m := map[crypto.Hash]uint64{}
			for _, p := range parts {
				for k, v := range p {
					m[k] += v
				}
			}
			return m
		}
		unit, dero, quote := map[crypto.Hash]uint64{e.nfa: 1}, map[crypto.Hash]uint64{zero: 777}, e.money(6000) // on DERO the quote is DERO
		strays := map[string]map[crypto.Hash]uint64{"DERO": dero, "the unit": unit}
		if wrapped {
			strays["the quote"] = quote
		}
		updArgs := rpc.Arguments{harness.S("iconURL", ""), harness.S("coverURL", ""), harness.S("fileURL", "https://example.invalid/new.png"),
			harness.S("fileSignURL", ""), harness.S("tags", "")}
		startArgs := rpc.Arguments{harness.S("listType", "sale"), harness.U("duration", 1), harness.U("startPrice", 1_000_000),
			harness.S("charityDonateAddr", ""), harness.U("charityDonatePerc", 0)}
		refused := func(what, entry string, args rpc.Arguments, in map[crypto.Hash]uint64, who *rpc.Address) {
			t.Helper()
			owner, file, list := c.S(e.nfa, "owner"), c.S(e.nfa, "fileURL"), c.S(e.nfa, "listType")
			deroHeld, quoteHeld := c.SCBalance(e.nfa, zero), c.SCBalance(e.nfa, e.quote)
			if r := e.call(entry, args, in, who); r.OK() {
				t.Fatalf("%s: %s with %s attached was accepted: %+v", tag, entry, what, r.Transfers)
			}
			if c.S(e.nfa, "owner") != owner || c.S(e.nfa, "fileURL") != file || c.S(e.nfa, "listType") != list ||
				c.SCBalance(e.nfa, zero) != deroHeld || c.SCBalance(e.nfa, e.quote) != quoteHeld {
				t.Fatalf("%s: a refused %s (%s attached) changed the contract", tag, entry, what)
			}
			e.oneUnit(t, tag+" refused "+entry)
		}

		// ClaimOwnership and Start take the unit and nothing else; Update takes nothing
		refused("the unit and DERO", "ClaimOwnership", nil, with(unit, dero), e.creator)
		refused("the unit and DERO", "Start", startArgs, with(unit, dero), e.creator)
		if wrapped {
			refused("the unit and the quote", "ClaimOwnership", nil, with(unit, quote), e.creator)
			refused("the unit and the quote", "Start", startArgs, with(unit, quote), e.creator)
		}
		for what, in := range strays {
			refused(what, "Update", updArgs, in, e.creator)
		}
		// the same calls with nothing stray still work
		e.claim(t, e.creator)
		if r := e.call("Update", updArgs, nil, e.creator); !r.OK() || c.S(e.nfa, "fileURL") != "https://example.invalid/new.png" {
			t.Fatalf("%s: a clean Update: %v", tag, r.Err)
		}

		// a live sale, inside the cancel buffer: BuyItNow and Bid never take the unit, CancelListing takes nothing
		if r := e.start(e.creator, "sale", 1, 1_000_000, "", 0, true); !r.OK() {
			t.Fatalf("%s: start: %v", tag, r.Err)
		}
		refused("the unit and the price", "BuyItNow", nil, with(unit, e.money(1_000_000)), e.bob)
		refused("the unit", "Bid", nil, with(unit, e.money(1_000_000)), e.bob)
		for what, in := range strays {
			refused(what, "CancelListing", nil, in, e.creator)
		}
		e.checkSettlement(t, tag+" sale", e.buy(e.bob, 1_000_000), 1_000_000, e.creator, e.bob, 0, nil)

		// an ended sale: CloseListing takes nothing
		c.NextBlock()
		if r := e.start(e.bob, "sale", 1, 1_000_000, "", 0, true); !r.OK() {
			t.Fatalf("%s: relist: %v", tag, r.Err)
		}
		c.Timestamp = c.U(e.nfa, "endBlockTime")
		for what, in := range strays {
			refused(what, "CloseListing", nil, in, e.bob)
		}
		if r := e.call("CloseListing", nil, nil, e.bob); !r.OK() || r.Sent(e.nfa, e.bob) != 1 {
			t.Fatalf("%s: a clean close: %v", tag, r.Err)
		}
		// PayOwed takes nothing either
		for what, in := range strays {
			refused(what, "PayOwed", rpc.Arguments{harness.S("payee", e.treasury.String())}, in, e.carol)
		}
		e.drain(t)
		if c.SCBalance(e.nfa, zero) != 0 {
			t.Fatalf("%s: %d DERO left in the contract", tag, c.SCBalance(e.nfa, zero))
		}

		// the kept exception: ClaimOwnership above ring 2 sends the unit to the recorded owner, as the standard does,
		// and the DERO attached to it has nobody to go back to
		r := e.call("ClaimOwnership", nil, with(unit, dero), nil)
		if !r.OK() || r.Sent(e.nfa, e.bob) != 1 || c.S(e.nfa, "owner") != raw(e.bob) || c.SCBalance(e.nfa, zero) != 777 {
			t.Fatalf("%s: anonymous ClaimOwnership: %v, unit to the owner %d, DERO held %d", tag, r.Err, r.Sent(e.nfa, e.bob), c.SCBalance(e.nfa, zero))
		}
		e.oneUnit(t, tag+" anonymous claim")
		t.Logf("%s: every entrypoint refused value it does not take; above ring 2 ClaimOwnership returned the unit to its owner and kept the 777 DERO it could not refund", tag)
	}
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
