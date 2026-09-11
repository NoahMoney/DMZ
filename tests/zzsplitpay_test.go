package tests

// DVM semantics, dimension "make it lose money". One law of the chain, the two ways nfa.bas used to walk into it,
// and what it does now.
//
// THE LAW. dvm.SanityCheckExternalTransfers (vendor/derohe/dvm/simulator.go 227-238) is the only place a
// contract's transfer list is checked against the account registry, and its loop is
//
//	if _, err = balance_tree.Get([]byte(transfer.Address)); err == nil || xerrors.Is(err, graviton.ErrNotFound) {
//	    // everything is okay
//	} else { err = fmt.Errorf("account is unregistered"); return }
//
// so an UNREGISTERED destination takes the "everything is okay" branch, and `err` (the named return) is simply
// left holding whatever the LAST Get returned. One bad destination therefore fails the call only when it is the
// last transfer. Anywhere earlier, err comes back nil, process_transaction_sc runs dvm.ProcessExternal
// (simulator.go 301-382), which writes EVERY state entry first (307-326) and only then walks the transfers in
// emission order (328, appended by TX_Storage.SendExternal, dvm_store.go 138-144), panicking on the first
// unregistered one (356-359). blockchain.process_transaction_sc recovers from that panic
// (transaction_execute.go 277-282) and the block commits the trees regardless (blockchain.go 971-1012).
// SanityCheck has ALREADY debited the contract's own asset balance by the full total (simulator.go 212-224), so
// the dropped transfers are not refunded to anyone: that money stops existing.
//
// nfa.bas processDEROFinalPayment used to emit up to five transfers in one fixed order — royalty -> creatorAddr,
// artificerFee -> artificerAddr, charity -> charityDonateAddr, protoFee -> treasury, remainder -> owner — and two of
// those destinations had never been seen to sign: charityDonateAddr is a string the SELLER hands to Start(),
// checked as a curve point and nothing more, and artificerAddr is hard-coded. An unregistered one completed the
// sale and destroyed every share after it (FINDINGS_2026-09-10 DVM-1, LIV-01). Since 2026-09-11 a settlement sends
// only to the buyer, the creator and the owner, who have all signed a transaction on the contract; the other three
// shares are credited under ow_<address> and leave through PayOwed(payee), whose one transfer is the call's last,
// so an unregistered payee makes the DVM refuse that call whole and the share waits. The tests below were written
// to show the loss; each now asserts the repair, and gives its old name.

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// ghostAddress is a well-formed DERO address that has never been registered: harness.NewAccount registers, this
// deliberately does not. ADDRESS_RAW and IS_ADDRESS_VALID accept it (they only decompress the point).
func ghostAddress() *rpc.Address {
	k := crypto.RandomScalarBNRed()
	return rpc.NewAddressFromKeys(crypto.GPoint.ScalarMult(k))
}

// S1 (was TestNFASaleToAnUnregisteredCharityStripsTheSellerAndTheTreasury). A sale whose charity address has no
// account. No DVM builtin reads the registry, so Start still accepts the address; the sale now pays the seller and
// the creator in full in the call, the charity's share and the protocol fee wait in the contract, the treasury's
// leaves at once through PayOwed, and the charity's follows the day that account exists. Run for a DERO-quoted and
// for a wrapped-quote (wXMR) NFA.
func TestNFASaleToAnUnregisteredCharityPaysTheSellerAndTheTreasury(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		name := "dero"
		if wrapped {
			name = "wrapped"
		}
		t.Run(name, func(t *testing.T) {
			const royalty, protoFee, charityPerc = 10, 50, 20
			const amt = 1000000
			e := newNFAEnv(t, wrapped, royalty, 0, protoFee)
			c := e.c
			// alice takes the asset by private transfer, so the seller is not the creator and the royalty is
			// distinguishable from the remainder.
			e.claim(t, e.alice)
			ghost := ghostAddress()
			e.name(ghost, "ghost charity")
			if r := e.start(e.alice, "sale", 24, amt, ghost.String(), charityPerc, true); !r.OK() {
				t.Fatalf("start: %v", r.Err)
			}
			if c.S(e.nfa, "charityDonateAddr") != raw(ghost) {
				t.Fatal("the charity address was not stored; the case is not set up")
			}
			sellerBefore := c.AccountRecord(e.quote, e.alice)

			var r harness.Result
			p := func() (p interface{}) {
				defer func() { p = recover() }()
				r = e.buy(e.bob, amt)
				return nil
			}()
			if p != nil {
				t.Fatalf("ProcessExternal panicked: the settlement still names an address that may have no account: %v", p)
			}
			if !r.OK() {
				t.Fatalf("buy: %v", r.Err)
			}
			royaltyAmt, charity, proto := uint64(royalty*amt/100), uint64(charityPerc*amt/100), uint64(protoFee*amt/10000)
			payout := uint64(amt) - royaltyAmt - charity - proto
			if c.S(e.nfa, "owner") != raw(e.bob) || r.Sent(e.nfa, e.bob) != 1 || c.U(e.nfa, "scBalance") != 0 {
				t.Fatalf("the sale did not complete: owner %s", e.who(c.S(e.nfa, "owner")))
			}
			if r.Sent(e.quote, e.creator) != royaltyAmt || r.Sent(e.quote, e.alice) != payout {
				t.Fatalf("creator got %d (want %d), seller got %d (want %d)", r.Sent(e.quote, e.creator), royaltyAmt, r.Sent(e.quote, e.alice), payout)
			}
			if c.AccountRecord(e.quote, e.alice) == sellerBefore {
				t.Fatal("the seller's payout was listed but not delivered")
			}
			if r.Sent(e.quote, ghost) != 0 || r.Sent(e.quote, e.treasury) != 0 {
				t.Fatal("the settlement sent to a payee that may have no account")
			}
			if e.owedTo(ghost) != charity || e.owedTo(e.treasury) != proto || c.SCBalance(e.nfa, e.quote) != charity+proto {
				t.Fatalf("owed: charity %d (want %d), treasury %d (want %d), held %d", e.owedTo(ghost), charity, e.owedTo(e.treasury), proto, c.SCBalance(e.nfa, e.quote))
			}
			// the charity has no account: PayOwed to it is refused whole and changes nothing
			if rr := e.call("PayOwed", rpc.Arguments{harness.S("payee", ghost.String())}, nil, e.carol); rr.OK() {
				t.Fatal("PayOwed to an unregistered charity was accepted")
			}
			if e.owedTo(ghost) != charity || c.AccountRecord(e.quote, ghost) != "" {
				t.Fatal("a refused PayOwed moved the charity's share")
			}
			// the treasury has one: its fee leaves now; the charity's share the day its account exists
			treasuryBefore := c.AccountRecord(e.quote, e.treasury)
			e.payOwed(t, nil, e.treasury)
			if c.AccountRecord(e.quote, e.treasury) == treasuryBefore {
				t.Fatal("the protocol fee was listed but not delivered")
			}
			e.register(t, ghost)
			e.payOwed(t, e.carol, ghost)
			if c.AccountRecord(e.quote, ghost) == "" || c.SCBalance(e.nfa, e.quote) != 0 {
				t.Fatalf("after the charity registered: record %v, held %d", c.AccountRecord(e.quote, ghost) != "", c.SCBalance(e.nfa, e.quote))
			}
			t.Logf("PAID: sale of %d with an unregistered charity: seller %d and creator %d in the call; treasury %d by PayOwed; charity %d waited until its account existed",
				amt, payout, royaltyAmt, proto, charity)
		})
	}
}

// S1b. The control: the identical sale with a REGISTERED charity. The royalty and the seller's remainder arrive in
// the call; the charity's share and the protocol fee arrive through PayOwed, one transfer each, from any caller.
func TestNFASaleToARegisteredCharityPaysEveryone(t *testing.T) {
	const royalty, protoFee, charityPerc = 10, 50, 20
	const amt = 1000000
	e := newNFAEnv(t, false, royalty, 0, protoFee)
	e.claim(t, e.alice)
	if r := e.start(e.alice, "sale", 24, amt, e.charity.String(), charityPerc, true); !r.OK() {
		t.Fatalf("start: %v", r.Err)
	}
	e.checkSettlement(t, "sale", e.buy(e.bob, amt), amt, e.alice, e.bob, charityPerc, nil)
	e.payOwed(t, e.bob, e.charity)
	e.payOwed(t, e.bob, e.treasury)
	e.drain(t)
	t.Logf("control: royalty %d and seller %d in the call; charity %d and protoFee %d by PayOwed",
		royalty*amt/100, amt-royalty*amt/100-charityPerc*amt/100-protoFee*amt/10000, charityPerc*amt/100, protoFee*amt/10000)
}

// S2 (was TestPoisonedCharityShareMakesTheNFAUnsellableAndLocksABid, DVM-2). Start used to store the CLAMPED charity
// share before it validated the charity address, and the refusal path (400) returns the unit with RETURN 0, which
// commits: one refused Start left a share with no address, every later settlement reverted, and a bid taken
// meanwhile was locked in the contract. The share is now stored only after the address check (Start 170 -> 195),
// so a refused Start stores nothing and the next auction settles.
func TestARefusedStartCannotPoisonTheCharityShare(t *testing.T) {
	const royalty, protoFee = 5, 50
	e := newNFAEnv(t, true, royalty, 0, protoFee) // wXMR-quoted
	c := e.c
	room := uint64((10000 - protoFee - royalty*100) / 100) // Start 150: 94

	// One refused Start. The contract hands the unit back and returns 0, as the standard does, and stores nothing.
	r := e.start(e.creator, "sale", 24, 1000, "not-a-dero-address", room+1, true)
	if !r.OK() || r.Sent(e.nfa, e.creator) != 1 {
		t.Fatalf("the refusal path did not return the unit: %v sent %d", r.Err, r.Sent(e.nfa, e.creator))
	}
	if got := c.U(e.nfa, "charityDonatePerc"); got != 0 || c.Exists(e.nfa, "charityDonateAddr") || c.S(e.nfa, "listType") != "" {
		t.Fatalf("a REFUSED Start stored charityDonatePerc %d (address stored: %v)", got, c.Exists(e.nfa, "charityDonateAddr"))
	}
	// A clamped share with a good address is still stored, clamped, beside its address.
	if r := e.start(e.creator, "sale", 24, 1000, e.charity.String(), room+1, true); !r.OK() || c.U(e.nfa, "charityDonatePerc") != room || c.S(e.nfa, "charityDonateAddr") != raw(e.charity) {
		t.Fatalf("clamped start: %v, share %d", r.Err, c.U(e.nfa, "charityDonatePerc"))
	}
	if r := e.call("CancelListing", nil, nil, e.creator); !r.OK() || r.Sent(e.nfa, e.creator) != 1 {
		t.Fatalf("cancel: %v", r.Err)
	}
	// An ordinary auction afterwards: no charity asked for, none stored, and it settles to the winner.
	if r := e.start(e.creator, "auction", 24, 1000000, "", 0, true); !r.OK() || c.U(e.nfa, "charityDonatePerc") != 0 {
		t.Fatalf("auction start: %v, share %d", r.Err, c.U(e.nfa, "charityDonatePerc"))
	}
	if r := e.bid(e.alice, 5000000); !r.OK() {
		t.Fatalf("bid: %v", r.Err)
	}
	c.Timestamp = c.U(e.nfa, "endBlockTime")
	e.checkSettlement(t, "close", e.call("CloseListing", nil, nil, e.creator), 5000000, e.creator, e.alice, 0, nil)
	e.drain(t)
	t.Logf("a refused Start with share %d and a bad address stored nothing; the next auction settled to alice", room+1)
}

// S3 (was TestAResetLeavesAnEmptyCharityAddressBehindTheShare, DVM-2). The same one step further along: after any
// resetVars, charityDonateAddr exists and is "" (resetVars 110). A refused Start used to leave a share beside that
// empty address and every later sale reverted inside SEND_ASSET_TO_ADDRESS. Now the refused Start leaves the share
// at 0 and the next sale settles.
func TestAResetThenARefusedStartLeavesNoShare(t *testing.T) {
	const royalty, protoFee = 5, 50
	e := newNFAEnv(t, false, royalty, 0, protoFee)
	c := e.c
	room := uint64((10000 - protoFee - royalty*100) / 100)

	// A complete, ordinary sale first: it ends in resetVars, which stores charityDonateAddr = "".
	if r := e.start(e.creator, "sale", 24, 1000, "", 0, true); !r.OK() {
		t.Fatalf("start: %v", r.Err)
	}
	e.checkSettlement(t, "first sale", e.buy(e.alice, 1000), 1000, e.creator, e.alice, 0, nil)
	if got := c.S(e.nfa, "charityDonateAddr"); got != "" {
		t.Fatalf("charityDonateAddr %q after resetVars", got)
	}
	// alice is the owner now. One refused Start leaves the share at 0.
	if r := e.start(e.alice, "sale", 24, 1000, "not-a-dero-address", room+1, true); !r.OK() || r.Sent(e.nfa, e.alice) != 1 {
		t.Fatalf("refused start: %v", r.Err)
	}
	if c.U(e.nfa, "charityDonatePerc") != 0 || c.S(e.nfa, "charityDonateAddr") != "" {
		t.Fatalf("perc %d addr %q", c.U(e.nfa, "charityDonatePerc"), c.S(e.nfa, "charityDonateAddr"))
	}
	// And a normal sale completes.
	if r := e.start(e.alice, "sale", 24, 1000000, "", 0, true); !r.OK() {
		t.Fatalf("relist: %v", r.Err)
	}
	e.checkSettlement(t, "sale after the refused start", e.buy(e.bob, 1000000), 1000000, e.alice, e.bob, 0, nil)
	e.drain(t)
}

// S4 (was TestUnregisteredArtificerAddressDestroysEverySaleOnThatNetwork, LIV-01 (b)). nfa.bas init line 40
// hard-codes Civilware's MAINNET address as artificerAddr. On any network where that account has never been
// registered — a DMZ testnet, a private chain, or DERO mainnet itself if the key never transacted — every sale of an
// NFA whose creator set artificerFee > 0 used to half-commit: the buyer got the asset, the creator the royalty, and
// the protocol fee and the seller's whole remainder were destroyed. The artificer's share is now owed, not sent:
// the sale pays the seller and the creator in full, the buyer gets the unit, and the artificer's share waits for
// PayOwed until that account exists. newNFAEnv registers the address by hand; this case does not.
func TestUnregisteredArtificerAddressCostsTheSellerNothing(t *testing.T) {
	const royalty, artificerFee, protoFee = 10, 5, 50
	const amt = 1000000
	c := harness.New()
	creator, buyer, treasury := c.NewAccount(), c.NewAccount(), c.NewAccount()
	art, err := rpc.NewAddress(artificerAddr)
	if err != nil {
		t.Fatal(err)
	}
	// deliberately NOT registered, which is the whole point
	nfa, res := c.Install(readContract(t, "nfa.bas"), nil,
		nfaArgs("", royalty, artificerFee, 0, protoFee, treasury.String()), creator, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatal(res.Err)
	}
	if r := c.Call(nfa, "Start", rpc.Arguments{harness.S("listType", "sale"), harness.U("duration", 24),
		harness.U("startPrice", amt), harness.S("charityDonateAddr", ""), harness.U("charityDonatePerc", 0)},
		map[crypto.Hash]uint64{nfa: 1}, creator); !r.OK() {
		t.Fatalf("start: %v", r.Err)
	}
	var r harness.Result
	p := func() (p interface{}) {
		defer func() { p = recover() }()
		r = c.Call(nfa, "BuyItNow", nil, map[crypto.Hash]uint64{zero: amt}, buyer)
		return nil
	}()
	if p != nil {
		t.Fatalf("ProcessExternal panicked on the artificer transfer: the settlement still sends to it: %v", p)
	}
	if !r.OK() || c.S(nfa, "owner") != raw(buyer) || c.U(nfa, "scBalance") != 0 || r.Sent(nfa, buyer) != 1 {
		t.Fatalf("the sale did not complete: %v", r.Err)
	}
	royaltyAmt, artAmt, proto := uint64(royalty*amt/100), uint64(artificerFee*amt/100), uint64(protoFee*amt/10000)
	payout := uint64(amt) - royaltyAmt - artAmt - proto
	if got := r.Sent(zero, creator); got != royaltyAmt+payout { // the creator is the seller here
		t.Fatalf("the creator-seller received %d, want the royalty %d plus the remainder %d", got, royaltyAmt, payout)
	}
	owed := func(a *rpc.Address) uint64 { v, _ := c.Var(nfa, "ow_"+a.String()).(uint64); return v }
	if r.Sent(zero, art) != 0 || r.Sent(zero, treasury) != 0 || owed(art) != artAmt || owed(treasury) != proto || c.SCBalance(nfa, zero) != artAmt+proto {
		t.Fatalf("owed: artificer %d (want %d), treasury %d (want %d), held %d", owed(art), artAmt, owed(treasury), proto, c.SCBalance(nfa, zero))
	}
	payee := func(a *rpc.Address) rpc.Arguments { return rpc.Arguments{harness.S("payee", a.String())} }
	if rr := c.Call(nfa, "PayOwed", payee(art), nil, buyer); rr.OK() || owed(art) != artAmt || c.AccountRecord(zero, art) != "" {
		t.Fatalf("PayOwed to the unregistered artificer: ok %v, owed %d", rr.OK(), owed(art))
	}
	if rr := c.Call(nfa, "PayOwed", payee(treasury), nil, nil); !rr.OK() || rr.Sent(zero, treasury) != proto {
		t.Fatalf("PayOwed to the treasury: %v", rr.Err)
	}
	t.Logf("PAID: with %s… unregistered the seller got %d and the royalty %d in the call; the artificer's %d waits under ow_, the treasury's %d was paid by an anonymous PayOwed",
		artificerAddr[:16], payout, royaltyAmt, artAmt, proto)
}
