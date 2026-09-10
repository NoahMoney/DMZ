package tests

// DVM semantics, dimension "make it lose money". One law of the chain and two ways nfa.bas walks into it.
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
// tests/sendorder_test.go checks statement order (no external send before a failure exit). It says nothing
// about destinations, and no DMZ test asserts that a contract may only address an unverified account with its
// LAST transfer.
//
// nfa.bas processDEROFinalPayment (329-354) emits up to five transfers in one fixed order:
//   royalty -> creatorAddr, artificerFee -> artificerAddr, charity -> charityDonateAddr,
//   protoFee -> treasury, remainder -> owner
// charityDonateAddr is a string the SELLER hands to Start(), checked as a curve point and nothing more
// (nfa.bas 175, 195); app/nfas.js startSheet does the same format-only check. protoFee is 50 bps on every DMZ
// NFA, so there is always a transfer after the charity one.

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

// S1. A sale whose charity address is unregistered. The buyer keeps the NFA, the creator keeps the royalty and
// everything from the charity transfer on - the charity share, the protocol fee and the SELLER'S ENTIRE
// REMAINDER - is destroyed. Run for a DERO-quoted and for a wrapped-quote (wXMR) NFA.
func TestNFASaleToAnUnregisteredCharityStripsTheSellerAndTheTreasury(t *testing.T) {
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
			if r := e.start(e.alice, "sale", 24, amt, ghost.String(), charityPerc, true); !r.OK() {
				t.Fatalf("start: %v", r.Err)
			}
			if c.S(e.nfa, "charityDonateAddr") != raw(ghost) {
				t.Fatal("the contract accepted an unregistered address as the charity destination")
			}

			creatorBefore := c.AccountRecord(e.quote, e.creator)
			sellerBefore := c.AccountRecord(e.quote, e.alice)
			treasuryBefore := c.AccountRecord(e.quote, e.treasury)
			buyerUnitBefore := c.AccountRecord(e.nfa, e.bob)

			p := func() (p interface{}) {
				defer func() { p = recover() }()
				e.call("BuyItNow", nil, e.money(amt), e.bob)
				return nil
			}()
			if p == nil {
				t.Fatal("BuyItNow succeeded; expected ProcessExternal to panic on the charity transfer")
			}
			t.Logf("ProcessExternal panicked exactly as the chain does, and the chain recovers: %v", p)

			// Everything the contract stored is final: the sale happened.
			if got := c.S(e.nfa, "owner"); got != raw(e.bob) {
				t.Fatalf("owner %s, want bob - the ownership write must have committed", e.who(got))
			}
			if c.U(e.nfa, "scBalance") != 0 || c.S(e.nfa, "listType") != "" || c.U(e.nfa, "previousSalePrice") != amt {
				t.Fatalf("listing state after the panic: scBalance %d listType %q previousSalePrice %d",
					c.U(e.nfa, "scBalance"), c.S(e.nfa, "listType"), c.U(e.nfa, "previousSalePrice"))
			}
			// Transfers before the bad one landed.
			if c.AccountRecord(e.nfa, e.bob) == buyerUnitBefore {
				t.Fatal("the buyer did not receive the unit")
			}
			if c.AccountRecord(e.quote, e.creator) == creatorBefore {
				t.Fatal("the creator did not receive the royalty")
			}
			// The bad one and everything after it did not.
			if rec := c.AccountRecord(e.quote, ghost); rec != "" {
				t.Fatal("the charity account exists after all; the case is not set up")
			}
			if c.AccountRecord(e.quote, e.treasury) != treasuryBefore {
				t.Logf("treasury was not paid the %d protocol fee", protoFee*amt/10000)
			} else {
				t.Logf("treasury balance record unchanged: the %d-unit protocol fee was never delivered", protoFee*amt/10000)
			}
			if c.AccountRecord(e.quote, e.alice) != sellerBefore {
				t.Fatalf("the seller was paid; the drop did not reach the remainder")
			}
			// And the contract's own books say it paid everything out.
			if held := c.SCBalance(e.nfa, e.quote); held != 0 {
				t.Fatalf("contract still holds %d of the quote; SanityCheck should have debited the whole sale", held)
			}
			payout := amt - royalty*amt/100 - charityPerc*amt/100 - protoFee*amt/10000
			t.Logf("LOST: sale of %d settled. buyer holds the NFA, creator took %d royalty; charity %d, treasury %d "+
				"and the seller's %d remainder were never credited to anyone and the contract's balance for the quote is 0. "+
				"%d of %d destroyed.",
				amt, royalty*amt/100, charityPerc*amt/100, protoFee*amt/10000, payout,
				charityPerc*amt/100+protoFee*amt/10000+payout, amt)
		})
	}
}

// S1b. The control: the identical sale with a REGISTERED charity pays all five shares and returns cleanly. The
// only difference between this and S1 is whether the charity account exists on chain.
func TestNFASaleToARegisteredCharityPaysEveryone(t *testing.T) {
	const royalty, protoFee, charityPerc = 10, 50, 20
	const amt = 1000000
	e := newNFAEnv(t, false, royalty, 0, protoFee)
	e.claim(t, e.alice)
	if r := e.start(e.alice, "sale", 24, amt, e.charity.String(), charityPerc, true); !r.OK() {
		t.Fatalf("start: %v", r.Err)
	}
	r := e.buy(e.bob, amt)
	if !r.OK() {
		t.Fatalf("buy: %v", r.Err)
	}
	payout := uint64(amt - royalty*amt/100 - charityPerc*amt/100 - protoFee*amt/10000)
	for _, w := range []struct {
		who  *rpc.Address
		want uint64
	}{{e.creator, uint64(royalty * amt / 100)}, {e.charity, uint64(charityPerc * amt / 100)},
		{e.treasury, uint64(protoFee * amt / 10000)}, {e.alice, payout}} {
		if got := r.Sent(e.quote, w.who); got != w.want {
			t.Fatalf("%s received %d, want %d", e.who(raw(w.who)), got, w.want)
		}
	}
	t.Logf("control: every share delivered (royalty %d, charity %d, protoFee %d, seller %d)",
		royalty*amt/100, charityPerc*amt/100, protoFee*amt/10000, payout)
}

// S2. nfa.bas Start 165-175 stores the CLAMPED charity share before it validates the charity address, and the
// refusal path (400) returns the unit with RETURN 0, which commits. A seller who asks for a charity share above
// the room left and mistypes the address gets their unit back and leaves charityDonatePerc permanently non-zero
// with no charityDonateAddr stored (init never stores one). Every later listing keeps that share, because
// Start 190 tests the ARGUMENT, not the stored value - and every settlement then reverts on LOAD of the missing
// key. The asset cannot be sold, and any bid taken meanwhile is locked in the contract.
func TestPoisonedCharityShareMakesTheNFAUnsellableAndLocksABid(t *testing.T) {
	const royalty, protoFee = 5, 50
	e := newNFAEnv(t, true, royalty, 0, protoFee) // wXMR-quoted
	c := e.c
	room := uint64((10000 - protoFee - royalty*100) / 100) // Start 150: 94

	// One refused Start. The contract hands the unit back and returns 0.
	r := e.start(e.creator, "sale", 24, 1000, "not-a-dero-address", room+1, true)
	if !r.OK() || r.Sent(e.nfa, e.creator) != 1 {
		t.Fatalf("the refusal path did not return the unit: %v sent %d", r.Err, r.Sent(e.nfa, e.creator))
	}
	if got := c.U(e.nfa, "charityDonatePerc"); got != room {
		t.Fatalf("charityDonatePerc %d after a REFUSED Start, want the clamped %d", got, room)
	}
	if c.Exists(e.nfa, "charityDonateAddr") {
		t.Fatal("an address was stored; the case is not set up")
	}
	t.Logf("poisoned: charityDonatePerc=%d with no charityDonateAddr, from a Start that was refused", room)

	// A perfectly ordinary later listing: no charity asked for, and none shown by any client reading the
	// arguments of this call.
	if r := e.start(e.creator, "auction", 24, 1000000, "", 0, true); !r.OK() {
		t.Fatalf("auction start: %v", r.Err)
	}
	if got := c.U(e.nfa, "charityDonatePerc"); got != room {
		t.Fatalf("the new listing cleared the share: %d", got)
	}
	if r := e.bid(e.alice, 5000000); !r.OK() {
		t.Fatalf("bid: %v", r.Err)
	}
	if c.SCBalance(e.nfa, e.quote) != 5000000 {
		t.Fatalf("the contract holds %d of the quote", c.SCBalance(e.nfa, e.quote))
	}
	c.Timestamp = c.U(e.nfa, "endBlockTime") // the auction has ended and alice has won

	// No settlement is possible: processDEROFinalPayment LOADs a key that does not exist.
	for _, s := range []struct {
		name string
		run  func() harness.Result
	}{
		{"CloseListing by the owner", func() harness.Result { return e.call("CloseListing", nil, nil, e.creator) }},
		{"empty Bid by the winner", func() harness.Result { return e.call("Bid", nil, nil, e.alice) }},
		{"empty Bid by a stranger", func() harness.Result { return e.call("Bid", nil, nil, e.carol) }},
	} {
		if got := s.run(); got.OK() {
			t.Fatalf("%s settled a poisoned listing", s.name)
		} else {
			t.Logf("%s refused by the DVM: %v", s.name, got.Err)
		}
	}
	if c.S(e.nfa, "owner") != raw(e.creator) || c.U(e.nfa, "scBalance") != 1 {
		t.Fatalf("state moved: owner %s scBalance %d", e.who(c.S(e.nfa, "owner")), c.U(e.nfa, "scBalance"))
	}
	// CancelListing is out of its 300-second window, so the seller cannot undo it either.
	if got := e.call("CancelListing", nil, nil, e.creator); got.OK() {
		t.Fatal("CancelListing worked after the auction ended")
	}
	t.Logf("STUCK: the NFA cannot be sold and alice's %d units of the quote are locked in the contract; "+
		"the only exit left is the BuyItNow-on-an-expired-auction path, which strands the bid instead",
		c.SCBalance(e.nfa, e.quote))

	// That exit does clear the poison, at the cost of stranding the bid (the known R4 shape).
	if got := e.call("BuyItNow", nil, nil, e.carol); !got.OK() {
		t.Fatalf("the expired-listing path: %v", got.Err)
	}
	if got := c.U(e.nfa, "charityDonatePerc"); got != 0 {
		t.Fatalf("resetVars left charityDonatePerc at %d", got)
	}
	if c.SCBalance(e.nfa, e.quote) != 5000000 {
		t.Fatalf("alice was refunded after all: held %d", c.SCBalance(e.nfa, e.quote))
	}
	t.Logf("resetVars cleared the share, and alice's %d is still stranded", c.SCBalance(e.nfa, e.quote))
}

// S3. The same poison one step further along: once ANY listing has been through resetVars, charityDonateAddr
// exists and is the empty string (nfa.bas 110), so the settlement no longer fails on a missing key - it fails
// inside SEND_ASSET_TO_ADDRESS, which panics on an address that does not decompress (dvm_functions.go 398-401).
// Same outcome, different line, and it proves the contract can hold a charity share with a stored address that
// can never be paid.
func TestAResetLeavesAnEmptyCharityAddressBehindTheShare(t *testing.T) {
	const royalty, protoFee = 5, 50
	e := newNFAEnv(t, false, royalty, 0, protoFee)
	c := e.c
	room := uint64((10000 - protoFee - royalty*100) / 100)

	// A complete, ordinary sale first: it ends in resetVars, which stores charityDonateAddr = "".
	if r := e.start(e.creator, "sale", 24, 1000, "", 0, true); !r.OK() {
		t.Fatalf("start: %v", r.Err)
	}
	if r := e.buy(e.alice, 1000); !r.OK() {
		t.Fatalf("buy: %v", r.Err)
	}
	if got := c.S(e.nfa, "charityDonateAddr"); got != "" {
		t.Fatalf("charityDonateAddr %q after resetVars", got)
	}
	// alice is the owner now. She poisons the share with one refused Start.
	if r := e.start(e.alice, "sale", 24, 1000, "not-a-dero-address", room+1, true); !r.OK() {
		t.Fatalf("refused start: %v", r.Err)
	}
	if c.U(e.nfa, "charityDonatePerc") != room || c.S(e.nfa, "charityDonateAddr") != "" {
		t.Fatalf("perc %d addr %q", c.U(e.nfa, "charityDonatePerc"), c.S(e.nfa, "charityDonateAddr"))
	}
	// Now a normal sale cannot complete.
	if r := e.start(e.alice, "sale", 24, 1000000, "", 0, true); !r.OK() {
		t.Fatalf("relist: %v", r.Err)
	}
	r := e.buy(e.bob, 1000000)
	if r.OK() {
		t.Fatal("the sale settled with an empty charity address")
	}
	t.Logf("every buy reverts inside SEND_ASSET_TO_ADDRESS: %v", r.Err)
	if c.S(e.nfa, "owner") != raw(e.alice) || c.U(e.nfa, "scBalance") != 1 {
		t.Fatalf("state moved: owner %s scBalance %d", e.who(c.S(e.nfa, "owner")), c.U(e.nfa, "scBalance"))
	}
}

// S4. No user error at all. nfa.bas init line 40 hard-codes Civilware's MAINNET address as artificerAddr and
// line 90 pays it artificerFee% of every sale, as transfer 2 of 5. On any network where that account has never
// been registered - a DMZ testnet, a private chain, or DERO mainnet itself if the key never transacted - every
// sale of every NFA whose creator set artificerFee > 0 half-commits: the buyer gets the asset, the creator gets
// the royalty, and the protocol fee and the seller's whole remainder are destroyed. newNFAEnv registers that
// address by hand before every other NFA test (nfa_test.go 76-80), which is why no existing test sees this.
func TestUnregisteredArtificerAddressDestroysEverySaleOnThatNetwork(t *testing.T) {
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
	creatorBefore, treasuryBefore := c.AccountRecord(zero, creator), c.AccountRecord(zero, treasury)
	p := func() (p interface{}) {
		defer func() { p = recover() }()
		c.Call(nfa, "BuyItNow", nil, map[crypto.Hash]uint64{zero: amt}, buyer)
		return nil
	}()
	if p == nil {
		t.Fatal("the sale completed; the artificer address must be unregistered for this case")
	}
	t.Logf("ProcessExternal panicked on the artificer transfer: %v", p)
	if c.S(nfa, "owner") != raw(buyer) || c.U(nfa, "scBalance") != 0 {
		t.Fatalf("the sale did not commit: owner %v scBalance %d", c.S(nfa, "owner") == raw(buyer), c.U(nfa, "scBalance"))
	}
	if c.AccountRecord(zero, creator) == creatorBefore {
		t.Fatal("the royalty (transfer 2) did not land")
	}
	if c.AccountRecord(zero, treasury) != treasuryBefore {
		t.Fatal("the treasury was paid; the drop did not reach transfer 4")
	}
	if c.AccountRecord(zero, art) != "" {
		t.Fatal("the artificer account exists")
	}
	if held := c.SCBalance(nfa, zero); held != 0 {
		t.Fatalf("contract still holds %d", held)
	}
	t.Logf("LOST: buyer owns the NFA, creator took %d royalty, and %d (artificer) + %d (protocol fee) + %d "+
		"(the seller's remainder) were destroyed - with no bad input from anyone, on any network where "+
		"%s is not registered", royalty*amt/100, artificerFee*amt/100, protoFee*amt/10000,
		amt-royalty*amt/100-artificerFee*amt/100-protoFee*amt/10000, artificerAddr[:16]+"...")
}
