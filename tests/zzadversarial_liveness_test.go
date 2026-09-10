package tests

// Adversarial liveness: states a DMZ contract can be put into that no permissionless action can leave.
// Every case runs on the real DVM through the harness. Written for the mainnet audit, dimension
// "make funds stuck forever, or make a state need a human" (directive 5, 29).

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// unregisteredAddress builds a well-formed DERO address that has NEVER been registered on chain: the harness
// NewAccount registers, this deliberately does not. IS_ADDRESS_VALID and ADDRESS_RAW accept it (they only
// decompress the point); ProcessExternal panics when a transfer is addressed to it.
func unregisteredAddress() *rpc.Address {
	k := crypto.RandomScalarBNRed()
	return rpc.NewAddressFromKeys(crypto.GPoint.ScalarMult(k))
}

// L1. vault.bas Paying (438-448) is a one-shot mark ANY single signer may set on ANY open withdrawal at any
// height, and Reclaim (479) refuses for ever once it exists. The header (lines 4-9) claims "no state needs a
// human to leave it". This shows the opposite: one signer mark plus a federation that later cannot reach k
// leaves the burner units in the contract with no exit at all.
func TestPayingMarkStrandsABurnForeverWhenTheQuorumIsLost(t *testing.T) {
	c, scid, sg, user := sealedVault(t)
	mustOK(t, burn(c, scid, user, 500), "burn")

	// One signer marks it in the burn own block, long before it is even releasable. No vote, no quorum, no
	// w_<j>_rel check, and nothing in the contract can ever clear the mark.
	mustOK(t, c.Call(scid, "Paying", rpc.Arguments{harness.U("j", 0)}, nil, sg[0]), "paying")
	if c.U(scid, "w_0_p") != c.Height {
		t.Fatalf("mark not set at the burn height: %d", c.U(scid, "w_0_p"))
	}

	// The federation loses signers. k=2, na=3: one slash is allowed, and it takes na down to k, after which
	// Slash itself is refused (551) - the vault can never remove or replace another signer.
	for i := 0; i < 2; i++ {
		c.Call(scid, "Slash", rpc.Arguments{harness.S("target", sg[2].String())}, nil, sg[i])
	}
	if c.U(scid, "na") != 2 {
		t.Fatalf("slash: na %d", c.U(scid, "na"))
	}
	if r := c.Call(scid, "Slash", rpc.Arguments{harness.S("target", sg[1].String())}, nil, sg[0]); r.OK() {
		t.Fatal("slashed below the quorum floor")
	}
	// sg[1] key is now lost: only sg[0] can act. k=2 votes can never be reached again.

	c.Height = c.U(scid, "w_0_rel") + reclaimAfter + 1000000 // any height at all
	if r := c.Call(scid, "Reclaim", rpc.Arguments{harness.U("j", 0)}, nil, user); r.OK() {
		t.Fatal("marked burn reclaimed (expected: it cannot)")
	}
	c.Call(scid, "Veto", rpc.Arguments{harness.U("j", 0)}, nil, sg[0])
	c.Call(scid, "Released", rpc.Arguments{harness.U("j", 0), harness.S("ftx", "xmr-tx")}, nil, sg[0])
	if c.U(scid, "w_0_st") != 0 {
		t.Fatalf("one signer settled a k-of-n vote: st %d", c.U(scid, "w_0_st"))
	}
	// Nobody else can move it either.
	stranger := c.NewAccount()
	for _, e := range []string{"Reclaim", "Veto"} {
		if r := c.Call(scid, e, rpc.Arguments{harness.U("j", 0)}, nil, stranger); r.OK() {
			t.Fatalf("a stranger moved the burn with %s", e)
		}
	}
	// The chain keeps no balance for a contract's own asset, so the proof is that st is still 0 (open) and no
	// call has ever paid the burner back.
	if c.U(scid, "w_0_st") != 0 {
		t.Fatalf("st %d", c.U(scid, "w_0_st"))
	}
	if c.U(scid, "sup") != 2000 {
		t.Fatalf("sup %d: the claim is still counted against the foreign reserve", c.U(scid, "sup"))
	}
	t.Logf("STUCK: burn 0 st=%d (open) at height %d with 1 live signer and k=%d; Reclaim is refused for ever by the "+
		"Paying mark and Veto/Released can never reach quorum. 500 units are in the contract and sup is still %d.",
		c.U(scid, "w_0_st"), c.Height, c.U(scid, "k"), c.U(scid, "sup"))
}

// L2. vault.bas Burn (378-379) validates refund as a curve point only: IS_ADDRESS_VALID decompresses the point
// and says nothing about whether the account exists on chain. A well-formed but UNREGISTERED address is
// accepted and stored in w_<j>_by. Both return paths (Veto 465, Reclaim 482) then address their only transfer
// to it, so the DVM refuses the whole call for ever and the burn can never leave st 0 by any route that gives
// the units back. Released is the only entrypoint that still works, and it pays the burner nothing.
func TestBurnRefundToAnUnregisteredAddressCanNeverBeReturned(t *testing.T) {
	c, scid, sg, user := sealedVault(t)
	ghost := unregisteredAddress()
	mustOK(t, c.Call(scid, "Burn",
		rpc.Arguments{harness.S("dest", "4AxmrSomewhereFarAway"), harness.S("refund", ghost.String())},
		map[crypto.Hash]uint64{scid: 500}, user), "burn with an unregistered refund address")
	if c.S(scid, "w_0_by") != string(ghost.Compressed()) {
		t.Fatal("the refund address was not stored raw")
	}

	c.Height = c.U(scid, "w_0_rel") + reclaimAfter + 1
	r := c.Call(scid, "Reclaim", rpc.Arguments{harness.U("j", 0)}, nil, user)
	if r.OK() {
		t.Fatal("Reclaim to an unregistered address succeeded")
	}
	t.Logf("Reclaim refused by the chain, not by the contract: %v", r.Err)
	// The quorum cannot do it either: Veto addresses the same transfer.
	for i := 0; i < 2; i++ {
		if rv := c.Call(scid, "Veto", rpc.Arguments{harness.U("j", 0)}, nil, sg[i]); i == 1 && rv.OK() {
			t.Fatal("Veto to an unregistered address succeeded")
		}
	}
	if c.U(scid, "w_0_st") != 0 {
		t.Fatalf("st %d", c.U(scid, "w_0_st"))
	}
	// A stranger, a later height, a bigger quorum: nothing changes. The only entrypoint that still works is
	// Released, which sends nothing to the burner.
	c.Height += 5000000
	if rr := c.Call(scid, "Reclaim", rpc.Arguments{harness.U("j", 0)}, nil, c.NewAccount()); rr.OK() {
		t.Fatal("Reclaim eventually succeeded")
	}
	t.Logf("STUCK: burn 0 st=0 for ever. 500 units sit in the contract, sup is still %d, and the only exit the "+
		"contract still permits (Released) hands the burner nothing.", c.U(scid, "sup"))
}

// L5. The same unregistered-address hazard, but where the failing transfer is NOT the last one the call emits.
// SanityCheckExternalTransfers assigns its lookup result to the NAMED return err and treats ErrNotFound as
// "everything is okay" (simulator.go 227-238), so a later registered destination overwrites err with nil and
// the check passes. ProcessExternal then commits every state entry (simulator.go 306-326) and panics part way
// through the transfer loop (355-359); blockchain.process_transaction_sc recovers (transaction_execute.go
// 275-280) and the block goes on. nfa.bas Start takes charityDonateAddr from the owner with only
// IS_ADDRESS_VALID (line 175/195) and processDEROFinalPayment pays charity BEFORE protoFee and before the
// owner, so a sale settles with the buyer owning the asset and the seller paid nothing.
func TestNFAUnregisteredCharityAddressSettlesTheSaleAndDestroysTheSellersProceeds(t *testing.T) {
	e := newNFAEnv(t, false, 10, 0, 250) // DERO quote, 10% royalty, 2.5% protocol fee
	c := e.c
	ghost := unregisteredAddress()
	if r := e.start(e.creator, "sale", 24, 1000000, ghost.String(), 5, true); !r.OK() {
		t.Fatalf("Start accepted no unregistered charity address: %v", r.Err)
	}
	if c.S(e.nfa, "charityDonateAddr") != string(ghost.Compressed()) || c.U(e.nfa, "charityDonatePerc") != 5 {
		t.Fatal("charity not stored")
	}
	panicked := func() (p interface{}) {
		defer func() { p = recover() }()
		e.call("BuyItNow", nil, map[crypto.Hash]uint64{zero: 1000000}, e.bob)
		return nil
	}()
	if panicked == nil {
		t.Fatal("expected the chain to panic part way through the transfer loop")
	}
	t.Logf("ProcessExternal panicked exactly as the node does, after committing the state: %v", panicked)
	// The state the DVM had already written is durable: the sale completed.
	if c.S(e.nfa, "owner") != raw(e.bob) || c.U(e.nfa, "scBalance") != 0 || c.U(e.nfa, "previousSalePrice") != 1000000 {
		t.Fatalf("owner %s scBalance %d - the sale did not commit", e.who(c.S(e.nfa, "owner")), c.U(e.nfa, "scBalance"))
	}
	t.Logf("SETTLED AND UNPAID: owner is now the buyer, scBalance 0, previousSalePrice %d. The transfer loop stopped "+
		"at the charity send, so the protocol fee and the WHOLE seller payout (%d of 1000000) were never made.",
		c.U(e.nfa, "previousSalePrice"), 1000000-100000-50000-25000)
}

// L3. nfa.bas BuyItNow 500-540: on an ENDED auction any non-owner may call BuyItNow with nothing attached. That
// path returns the unit to the owner and resets the listing, but never touches currBidAddr/currBidAmt - only
// outbidReturns and processHighestBidder delete those, and neither can be reached any more. The winning
// bidder payment stays in the contract and only the owner, by relisting, can ever release it.
func TestAStrangerCanStrandAnAuctionWinnersPayment(t *testing.T) {
	e := newNFAEnv(t, true, 10, 0, 250) // wrapped quote: this is a wXMR-priced NFA
	c := e.c
	if r := e.start(e.creator, "auction", 24, 1000000, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := e.bid(e.alice, 5000000); !r.OK() {
		t.Fatalf("alice bid: %v", r.Err)
	}
	if c.SCBalance(e.nfa, e.quote) != 5000000 {
		t.Fatalf("the contract holds %d of the quote", c.SCBalance(e.nfa, e.quote))
	}
	c.Timestamp = c.U(e.nfa, "endBlockTime") // the auction has ended; alice has won

	// Carol has no stake at all. One call, no value attached, gas only.
	r := e.call("BuyItNow", nil, nil, e.carol)
	if !r.OK() {
		t.Fatalf("the grief call was refused: %v", r.Err)
	}
	if r.Sent(e.nfa, e.creator) != 1 || c.U(e.nfa, "scBalance") != 0 || c.S(e.nfa, "owner") != raw(e.creator) {
		t.Fatalf("the unit did not go back to the seller: sent %d scBalance %d", r.Sent(e.nfa, e.creator), c.U(e.nfa, "scBalance"))
	}
	if r.Sent(e.quote, e.alice) != 0 {
		t.Fatalf("alice was refunded %d", r.Sent(e.quote, e.alice))
	}
	if c.SCBalance(e.nfa, e.quote) != 5000000 || !c.Exists(e.nfa, "currBidAmt") {
		t.Fatalf("bid state after the grief: held %d, currBidAmt present %v", c.SCBalance(e.nfa, e.quote), c.Exists(e.nfa, "currBidAmt"))
	}

	// Alice cannot get it back. Nothing she can call reaches outbidReturns or processHighestBidder.
	for _, step := range []struct {
		name string
		run  func() harness.Result
	}{
		{"Bid", func() harness.Result { return e.call("Bid", nil, nil, e.alice) }},
		{"BuyItNow", func() harness.Result { return e.call("BuyItNow", nil, nil, e.alice) }},
		{"CloseListing", func() harness.Result { return e.call("CloseListing", nil, nil, e.alice) }},
		{"CancelListing", func() harness.Result { return e.call("CancelListing", nil, nil, e.alice) }},
	} {
		if r := step.run(); r.Sent(e.quote, e.alice) != 0 {
			t.Fatalf("%s refunded alice %d", step.name, r.Sent(e.quote, e.alice))
		}
	}
	// The seller cannot release it either without starting a whole new listing.
	for _, step := range []string{"CloseListing", "CancelListing"} {
		if r := e.call(step, nil, nil, e.creator); r.Sent(e.quote, e.alice) != 0 {
			t.Fatalf("%s by the owner refunded alice", step)
		}
	}
	if c.SCBalance(e.nfa, e.quote) != 5000000 {
		t.Fatalf("held %d", c.SCBalance(e.nfa, e.quote))
	}
	t.Logf("STRANDED: %d of the quote asset held by the NFA, owed to alice, with the seller holding the unit again. "+
		"The only release is the owner starting a NEW auction and someone bidding (outbidReturns), or an empty Bid on an "+
		"expired new auction, which would hand alice the asset at her old price.", c.SCBalance(e.nfa, e.quote))

	// Proof that the only exit runs through the owner: a fresh auction plus any bid refunds her.
	c.NextBlock()
	if r := e.start(e.creator, "auction", 1, 1, "", 0, true); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := e.bid(e.bob, 1); !r.OK() || r.Sent(e.quote, e.alice) != 5000000 {
		t.Fatalf("relist + bid did not refund alice: %v sent %d", r.Err, r.Sent(e.quote, e.alice))
	}
}

// L4. htlc.bas: past t2 the ONLY exit is Punish by the named taker (120-136); Refund window closes at t2
// (line 25). A taker who stops answering leaves the maker DERO in the contract with no permissionless exit.
func TestHtlcPastT2NeedsTheTakerOrTheFundsNeverMove(t *testing.T) {
	c := harness.New()
	maker, taker, stranger := c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid, res := c.Install(readContract(t, "htlc.bas"), nil, nil, maker, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatal(res.Err)
	}
	h := "0000000000000000000000000000000000000000000000000000000000000000"
	mustOK(t, c.Call(scid, "Lock", rpc.Arguments{harness.S("ha", h), harness.S("hk", h), harness.S("hb", h),
		harness.S("taker", taker.String()), harness.U("t1", c.Height+10), harness.U("t2", c.Height+20), harness.S("asset", "")},
		map[crypto.Hash]uint64{zero: 100000}, maker), "lock")

	c.Height += 25 // past t2
	for _, who := range []*rpc.Address{maker, stranger} {
		if r := c.Call(scid, "Punish", rpc.Arguments{harness.U("id", 0)}, nil, who); r.OK() {
			t.Fatal("Punish is not taker-only")
		}
		if r := c.Call(scid, "Refund", rpc.Arguments{harness.U("id", 0), harness.S("sb", h)}, nil, who); r.OK() {
			t.Fatal("Refund worked past t2")
		}
	}
	c.Height += 10000000
	if r := c.Call(scid, "Refund", rpc.Arguments{harness.U("id", 0), harness.S("sb", h)}, nil, maker); r.OK() {
		t.Fatal("the maker recovered after t2")
	}
	if c.U(scid, "st_0") != 0 || c.SCBalance(scid, zero) != 100000 {
		t.Fatalf("st %d balance %d", c.U(scid, "st_0"), c.SCBalance(scid, zero))
	}
	t.Logf("STUCK: %d DERO held at height %d, st=0, and only the taker own signature can ever move it",
		c.SCBalance(scid, zero), c.Height)
}

// L6. The same half-application, but with an address the creator can never change and never chose: nfa.bas init
// line 40 hard-codes the artificer address, and artificerFee is immutable after install. On any network where
// that account has not registered, EVERY sale of EVERY NFA with artificerFee > 0 transfers the asset and
// destroys the protocol fee and the whole seller payout. tests/nfa_test.go registers the address by hand
// (newNFAEnv, comment at line 19) which is exactly why the existing suite never sees this.
func TestNFAArtificerFeeOnAChainWhereTheArtificerNeverRegistered(t *testing.T) {
	c := harness.New()
	creator, buyer, treasury := c.NewAccount(), c.NewAccount(), c.NewAccount()
	// note: the artificer address is deliberately NOT registered here
	nfa, res := c.Install(readContract(t, "nfa.bas"), nil,
		nfaArgs("", 10, 5, 0, 250, treasury.String()), creator, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	if r := c.Call(nfa, "Start", rpc.Arguments{harness.S("listType", "sale"), harness.U("duration", 24),
		harness.U("startPrice", 1000000), harness.S("charityDonateAddr", ""), harness.U("charityDonatePerc", 0)},
		map[crypto.Hash]uint64{nfa: 1}, creator); !r.OK() {
		t.Fatalf("start: %v", r.Err)
	}
	panicked := func() (p interface{}) {
		defer func() { p = recover() }()
		c.Call(nfa, "BuyItNow", nil, map[crypto.Hash]uint64{zero: 1000000}, buyer)
		return nil
	}()
	if panicked == nil {
		t.Fatal("expected the transfer loop to stop at the artificer send")
	}
	t.Logf("panic: %v", panicked)
	if c.S(nfa, "owner") != string(buyer.Compressed()) || c.U(nfa, "scBalance") != 0 {
		t.Fatalf("the sale did not commit: owner set %v scBalance %d", c.S(nfa, "owner") == string(buyer.Compressed()), c.U(nfa, "scBalance"))
	}
	// SanityCheckExternalTransfers already decremented the contract's recorded balance by the FULL 1000000
	// (simulator.go 212-224) and ProcessExternal committed that write before the transfer loop failed, so the
	// undelivered 875000 is in nobody's wallet and not in the contract's accounting either.
	if b := c.SCBalance(nfa, zero); b != 0 {
		t.Fatalf("contract still accounts for %d DERO", b)
	}
	t.Logf("SETTLED AND UNPAID: buyer owns the NFA, previousSalePrice %d, the contract accounts for %d DERO, and "+
		"neither the 2.5%% protocol fee (25000) nor the seller's 850000 reached anyone. artificerFee is immutable, "+
		"so every later sale of this NFA does the same.", c.U(nfa, "previousSalePrice"), c.SCBalance(nfa, zero))
}

// L7. vault.bas Unbond line 226 refuses whenever an isg_ key EXISTS, and Slash line 40 sets isg_ to 0 rather
// than deleting it. A slashed signer is therefore permanently barred from Unbond, while AddSigner2 line 23
// refuses them for the same reason. Any DERO they Bond afterwards is theirs for ever and reachable by nobody.
func TestASlashedSignerWhoBondsAgainCanNeverRecoverThatDero(t *testing.T) {
	c, scid, sg, _ := sealedVault(t)
	for i := 0; i < 2; i++ {
		c.Call(scid, "Slash", rpc.Arguments{harness.S("target", sg[2].String())}, nil, sg[i])
	}
	if c.U(scid, "na") != 2 {
		t.Fatalf("na %d", c.U(scid, "na"))
	}
	mustOK(t, c.Call(scid, "Bond", nil, map[crypto.Hash]uint64{zero: 7777}, sg[2]), "a slashed signer bonds again")
	if c.U(scid, "bond_"+string(sg[2].Compressed())) != 7777 {
		t.Fatalf("bond %d", c.U(scid, "bond_"+string(sg[2].Compressed())))
	}
	if c.U(scid, "bondtot") != 2000 {
		t.Fatalf("a slashed signer's bond counted at stake: bondtot %d", c.U(scid, "bondtot"))
	}
	if r := c.Call(scid, "Unbond", nil, nil, sg[2]); r.OK() {
		t.Fatal("a slashed signer unbonded")
	}
	// and can never be reinstated, so no future Slash can move it either
	for i := 0; i < 2; i++ {
		c.Call(scid, "AddSigner2", rpc.Arguments{harness.S("addr", sg[2].String())}, nil, sg[i])
	}
	if c.U(scid, "na") != 2 {
		t.Fatalf("a slashed signer was reinstated: na %d", c.U(scid, "na"))
	}
	t.Logf("STUCK: 7777 DERO recorded under bond_<slashed signer>, not counted in bondtot, and no entrypoint "+
		"(Unbond, Slash, AddSigner2, ClaimFees) can ever move it.")
}

// L8. There is no dissolution. After Seal no signer, past or present, may ever Unbond (line 226) and the only
// path that moves a bond at all is Slash, which pays the TREASURY. An honest signer's bond is unconditionally
// unrecoverable for the life of the contract, and the contract has no end.
func TestAfterSealAnHonestSignerBondCanNeverComeBack(t *testing.T) {
	c, scid, sg, _ := sealedVault(t)
	for _, s := range sg {
		if r := c.Call(scid, "Unbond", nil, nil, s); r.OK() {
			t.Fatal("a sealed signer unbonded")
		}
	}
	// even a signer the quorum removes is paid to the treasury, never back
	before := c.U(scid, "bondtot")
	var paid uint64
	for i := 0; i < 2; i++ {
		r := c.Call(scid, "Slash", rpc.Arguments{harness.S("target", sg[2].String())}, nil, sg[i])
		paid += r.Sent(zero, sg[2])
	}
	if paid != 0 {
		t.Fatalf("a slash returned %d to the signer", paid)
	}
	t.Logf("STUCK: bondtot %d -> %d; every DERO a signer bonds is either locked for the life of the vault or "+
		"forfeited to the treasury. There is no wind-down entrypoint.", before, c.U(scid, "bondtot"))
}

// L9. launch.bas tmode 2 (buyback and lock): every phase-0 trade accrues tax into tacc, but Buyback line 20
// refuses unless phase == 1, ClaimTax line 21 refuses unless tmode == 1 and dist() only runs for tmode 3.
// A launch that never graduates - the normal outcome for a bonding curve - therefore holds every atom of tax
// its buyers paid for ever, and no party, privileged or not, has any entrypoint that can move it.
func TestLaunchTmode2TaxIsLockedForeverIfTheTokenNeverGraduates(t *testing.T) {
	e := newLaunchEnv(t, launchParams{supply: 1000000000000, grad: 100000000000, cbps: 0, tax: 1000, tmode: 2, fee: 100, pfee: 5000})
	c := e.c
	for i := 0; i < 5; i++ {
		e.buy(t, e.alice, 1000000000, 0, 0, 0)
		c.NextBlock()
	}
	tacc := c.U(e.scid, "tacc")
	if tacc == 0 {
		t.Fatal("no tax accrued")
	}
	if c.U(e.scid, "phase") != 0 {
		t.Fatal("the launch graduated; this case is about one that does not")
	}
	for _, call := range []struct {
		entry string
		who   *rpc.Address
	}{{"Buyback", e.alice}, {"Buyback", e.creator}, {"ClaimTax", e.creator}, {"Distribute", e.alice},
		{"Claim", e.alice}, {"ClaimFees", e.alice}} {
		r := c.Call(e.scid, call.entry, nil, nil, call.who)
		if c.U(e.scid, "tacc") != tacc {
			t.Fatalf("%s moved tacc", call.entry)
		}
		_ = r
	}
	// even at any later height, and from any ring size
	c.Height += 1000000
	if r := c.Call(e.scid, "Buyback", nil, nil, nil); r.OK() || c.U(e.scid, "tacc") != tacc {
		t.Fatal("Buyback worked in phase 0")
	}
	t.Logf("LOCKED: %d of the quote asset sits in tacc at phase 0 with tmode 2. Buyback needs phase 1, ClaimTax "+
		"needs tmode 1, dist() needs tmode 3. Only a full graduation can ever release it.", tacc)
}
