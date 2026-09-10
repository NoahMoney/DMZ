package tests

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// ADVERSARIAL: at the mainnet target of 20 editions the anti-sweep floor minhst = 1 + maxsupply/50 is 1,
// so the C9 guard that TestFoundersMintRegisterRewards relies on (minhst 3 at maxsupply 100) does not exist.
// One holder registering ONE unit takes every DERO deposited before anybody held the pass.
func TestFoundersMainnet20FirstRegistrantSweepsEverything(t *testing.T) {
	c := harness.New()
	creator, treasury, alice, bob := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid := installFounders(t, c, creator, treasury, 500000, 20, 0, 4, 5000, 250) // the directive's mainnet shape
	if got := c.U(scid, "minhst"); got != 1 {
		t.Fatalf("minhst %d, want 1 (1 + 20/50)", got)
	}
	// the treasury deposits a quarter of fees before the pass is registered by anyone
	if r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{zero: 10000000}, treasury); !r.OK() {
		t.Fatalf("deposit: %v", r.Err)
	}
	if c.U(scid, "und") != 10000000 {
		t.Fatalf("und %d", c.U(scid, "und"))
	}
	for _, who := range []*rpc.Address{alice, bob} {
		if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 8)}, map[crypto.Hash]uint64{zero: 8 * 500000}, who); !r.OK() {
			t.Fatalf("mint: %v", r.Err)
		}
	}
	if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 1}, alice); !r.OK() {
		t.Fatalf("register: %v", r.Err)
	}
	if c.U(scid, "und") != 0 || c.U(scid, "rpool") != 10000000 {
		t.Fatalf("und %d rpool %d: the floor should have held the pre-registration pool", c.U(scid, "und"), c.U(scid, "rpool"))
	}
	// bob now registers all 8 - too late, the whole pool is already alice's
	if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 8}, bob); !r.OK() {
		t.Fatalf("register bob: %v", r.Err)
	}
	r := c.Call(scid, "Claim", nil, nil, alice)
	if !r.OK() {
		t.Fatalf("claim: %v", r.Err)
	}
	t.Logf("alice held 1 of 9 registered units for one call and claimed %d of the 10000000 deposited", r.Sent(zero, alice))
	if r.Sent(zero, alice) != 10000000 {
		t.Fatalf("alice got %d, expected the whole pool (this test documents the defect)", r.Sent(zero, alice))
	}
	if rb := c.Call(scid, "Claim", nil, nil, bob); rb.Sent(zero, bob) != 0 {
		t.Fatalf("bob got %d", rb.Sent(zero, bob))
	}
	checkFounders(t, c, scid)
}

// ADVERSARIAL (P7-11): rewards are pro-rata at the instant of Deposit with no holding period, so a wallet that
// watches the treasury bot can Register in the block before a deposit, Claim, and Withdraw again.
func TestFoundersJustInTimeRegistrationTakesAFullShare(t *testing.T) {
	c := harness.New()
	creator, treasury, honest, sniper := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid := installFounders(t, c, creator, treasury, 0, 100, 0, 0, 5000, 250) // minhst 3
	if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 10)}, nil, honest); !r.OK() {
		t.Fatalf("mint honest: %v", r.Err)
	}
	if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 10)}, nil, sniper); !r.OK() {
		t.Fatalf("mint sniper: %v", r.Err)
	}
	if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 10}, honest); !r.OK() {
		t.Fatalf("register honest: %v", r.Err)
	}
	for i := 0; i < 5000; i++ { // the honest holder waits about 25 hours of blocks
		c.NextBlock()
	}
	// the sniper registers in the block before the treasury's deposit
	if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 10}, sniper); !r.OK() {
		t.Fatalf("register sniper: %v", r.Err)
	}
	if r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{zero: 2000000}, treasury); !r.OK() {
		t.Fatalf("deposit: %v", r.Err)
	}
	r := c.Call(scid, "Withdraw", rpc.Arguments{harness.U("u", 10)}, nil, sniper) // settles, then leaves
	if !r.OK() {
		t.Fatalf("withdraw: %v", r.Err)
	}
	t.Logf("sniper held for one call and took %d of the 2000000 deposited; the 5000-block holder gets the rest", r.Sent(zero, sniper))
	if r.Sent(zero, sniper) != 1000000 {
		t.Fatalf("sniper took %d, want 1000000 (half)", r.Sent(zero, sniper))
	}
	if r.Sent(scid, sniper) != 10 {
		t.Fatal("sniper did not get the passes back")
	}
	checkFounders(t, c, scid)
}

// ADVERSARIAL: after every registered holder has been paid, rpool still holds the floor dust of every settle and
// no path in the contract ever releases it. tools/treasurybot gates its PayBatch loop on rpool == 0 && und == 0
// (main.go:173), so from the first distribution onward that gate is never true again.
func TestFoundersRpoolNeverReachesZeroAfterAFullPayBatch(t *testing.T) {
	c := harness.New()
	creator, treasury := c.NewAccount(), c.NewAccount()
	scid := installFounders(t, c, creator, treasury, 0, 100, 0, 0, 5000, 250)
	for i := 0; i < 7; i++ {
		a := c.NewAccount()
		if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 3)}, nil, a); !r.OK() {
			t.Fatalf("mint: %v", r.Err)
		}
		if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 3}, a); !r.OK() {
			t.Fatalf("register: %v", r.Err)
		}
	}
	if r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{zero: 1000001}, treasury); !r.OK() {
		t.Fatalf("deposit: %v", r.Err)
	}
	r1 := c.Call(scid, "PayBatch", rpc.Arguments{harness.U("from", 0), harness.U("n", 15)}, nil, nil)
	if !r1.OK() {
		t.Fatalf("paybatch: %v", r1.Err)
	}
	r2 := c.Call(scid, "PayBatch", rpc.Arguments{harness.U("from", 0), harness.U("n", 15)}, nil, nil)
	if !r2.OK() {
		t.Fatalf("paybatch2: %v", r2.Err)
	}
	if len(r2.Transfers) != 0 {
		t.Fatalf("second PayBatch still paid %d transfers", len(r2.Transfers))
	}
	t.Logf("after two full PayBatch calls: rpool %d, und %d, transfers on the second call %d",
		c.U(scid, "rpool"), c.U(scid, "und"), len(r2.Transfers))
	if c.U(scid, "rpool") == 0 {
		t.Fatal("rpool reached zero: the treasurybot gate would work (this test documents that it does not)")
	}
	for i := 0; i < 5; i++ {
		c.Call(scid, "Distribute", nil, nil, nil)
		c.Call(scid, "PayBatch", rpc.Arguments{harness.U("from", 0), harness.U("n", 15)}, nil, nil)
	}
	if c.U(scid, "rpool") == 0 {
		t.Fatal("rpool cleared after all")
	}
	t.Logf("rpool after five more sweeps: %d - tools/treasurybot would send ceil(hn/15) PayBatch txs every -interval forever", c.U(scid, "rpool"))
	checkFounders(t, c, scid)
}

// ADVERSARIAL: a wrapped-asset protocol fee cannot be given to holders. Deposit() reads DEROVALUE(), so an
// asset-only deposit is refused, and an asset attached alongside DERO is absorbed with no exit.
func TestFoundersCannotTakeAWrappedFee(t *testing.T) {
	c := harness.New()
	creator, treasury, tre := c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid := installFounders(t, c, creator, treasury, 0, 100, 0, 0, 5000, 250)
	wxmr := crypto.Hash{7, 7, 7} // stands in for a vault SCID
	if r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{wxmr: 1000000000000}, tre); r.OK() {
		t.Fatal("a wXMR-only Deposit was accepted: it would credit holders with DERO nobody earned")
	}
	r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{zero: 500000, wxmr: 1000000000000}, tre)
	if !r.OK() {
		t.Fatalf("mixed deposit: %v", r.Err)
	}
	if got := c.SCBalance(scid, wxmr); got != 1000000000000 {
		t.Fatalf("wrapped balance %d", got)
	}
	t.Logf("founders.bas now holds %d units of a wrapped asset with no entrypoint that can ever send them", c.SCBalance(scid, wxmr))
	c.Call(scid, "Distribute", nil, nil, nil)
	if c.SCBalance(scid, wxmr) != 1000000000000 {
		t.Fatal("wrapped units moved")
	}
}

// ADVERSARIAL: Mint has no per-address cap, so one wallet can take every remaining edition in a single call the
// moment start is reached. At the mainnet shape that is 16 of 20, on top of the installer's 4 reserve editions.
func TestFoundersOneWalletTakesEveryEditionAtStart(t *testing.T) {
	c := harness.New()
	creator, treasury, whale := c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid := installFounders(t, c, creator, treasury, 500000, 20, 0, 4, 5000, 250)
	r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 16)}, map[crypto.Hash]uint64{zero: 16 * 500000}, whale)
	if !r.OK() || r.Sent(scid, whale) != 16 {
		t.Fatalf("mint: %v sent %d", r.Err, r.Sent(scid, whale))
	}
	if c.U(scid, "minted") != 20 {
		t.Fatalf("minted %d", c.U(scid, "minted"))
	}
	t.Logf("one wallet holds 16 of 20 editions; the installer holds the other 4")
}

// ADVERSARIAL: what the app and the treasury bot pay as a FIXED fee for PayBatch / Mint / Distribute must cover
// the storage gas the call actually charges, or the transaction is mined, reverted, and the fee is spent.
func TestFoundersFixedFeesCoverStorageGas(t *testing.T) {
	c := harness.New()
	creator, treasury := c.NewAccount(), c.NewAccount()
	scid := installFounders(t, c, creator, treasury, 500000, 100, 0, 0, 5000, 250)
	for i := 0; i < 15; i++ {
		a := c.NewAccount()
		if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 2)}, map[crypto.Hash]uint64{zero: 1000000}, a); !r.OK() {
			t.Fatalf("mint: %v", r.Err)
		} else if i == 0 {
			t.Logf("Mint(2) storage gas %d (app/nfts.js pins fee 4000)", r.GasStorage)
		}
		if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 2}, a); !r.OK() {
			t.Fatalf("register: %v", r.Err)
		} else if i == 0 {
			t.Logf("Register storage gas %d (app estimates)", r.GasStorage)
		}
	}
	if r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{zero: 9999999}, treasury); !r.OK() {
		t.Fatalf("deposit: %v", r.Err)
	}
	r := c.Call(scid, "PayBatch", rpc.Arguments{harness.U("from", 0), harness.U("n", 15)}, nil, nil)
	if !r.OK() {
		t.Fatalf("paybatch: %v", r.Err)
	}
	t.Logf("PayBatch(0,15) storage gas %d, compute %d (app/nfts.js pins fee 20000, tools/treasurybot pins 3000)", r.GasStorage, r.GasCompute)
	if r.GasStorage > 3000 {
		t.Logf("DEFECT: treasurybot main.go:178 sends PayBatch at a fixed fee of 3000 < %d storage gas", r.GasStorage)
	}
	if r.GasStorage > harness.MaxStorageGas {
		t.Fatalf("PayBatch(15) exceeds the per-tx storage gas cap")
	}
}

// ADVERSARIAL: `und` carries no time weighting. Whenever hst falls below minhst every deposit piles up
// undistributed, and whoever registers the stake that crosses the floor takes their pro-rata share of ALL of it,
// however long the holders below the floor waited.
func TestFoundersFrozenPoolIsSweptByWhoeverCrossesTheFloor(t *testing.T) {
	c := harness.New()
	creator, treasury, whale := c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid := installFounders(t, c, creator, treasury, 0, 500, 0, 0, 5000, 250) // minhst = 11
	small := make([]*rpc.Address, 5)
	for i := range small { // five small holders, 10 units registered in total: below the floor
		small[i] = c.NewAccount()
		if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 2)}, nil, small[i]); !r.OK() {
			t.Fatalf("mint: %v", r.Err)
		}
		if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 2}, small[i]); !r.OK() {
			t.Fatalf("register: %v", r.Err)
		}
	}
	if c.U(scid, "hst") != 10 || c.U(scid, "minhst") != 11 {
		t.Fatalf("hst %d minhst %d", c.U(scid, "hst"), c.U(scid, "minhst"))
	}
	// three months of fees arrive while the registered stake sits one unit under the floor
	for i := 0; i < 90; i++ {
		if r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{zero: 100000}, treasury); !r.OK() {
			t.Fatalf("deposit %d: %v", i, r.Err)
		}
		for b := 0; b < 720; b++ {
			c.NextBlock()
		}
	}
	if c.U(scid, "und") != 9000000 || c.U(scid, "rpool") != 0 {
		t.Fatalf("und %d rpool %d", c.U(scid, "und"), c.U(scid, "rpool"))
	}
	// a whale mints 90 units and registers them in one call: hst 100, floor crossed, the whole frozen pool released
	if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 90)}, nil, whale); !r.OK() {
		t.Fatalf("whale mint: %v", r.Err)
	}
	if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 90}, whale); !r.OK() {
		t.Fatalf("whale register: %v", r.Err)
	}
	r := c.Call(scid, "Claim", nil, nil, whale)
	if !r.OK() {
		t.Fatalf("whale claim: %v", r.Err)
	}
	t.Logf("whale registered after 90 deposits and claimed %d of the 9000000 that accrued while it held nothing", r.Sent(zero, whale))
	if r.Sent(zero, whale) != 8100000 {
		t.Fatalf("whale took %d, want 8100000 (90 of 100 units)", r.Sent(zero, whale))
	}
	got := uint64(0)
	for _, s := range small {
		got += c.Call(scid, "Claim", nil, nil, s).Sent(zero, s)
	}
	t.Logf("the five holders who waited three months share %d", got)
	checkFounders(t, c, scid)
}

// FINE (evidence): a pass registered after a deposit has already been distributed earns nothing from it -
// Register snapshots hd_ at the current accumulator, so history cannot be claimed by a fresh unit.
func TestFoundersLateRegistrantEarnsNoHistory(t *testing.T) {
	c := harness.New()
	creator, treasury, early, late := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid := installFounders(t, c, creator, treasury, 0, 100, 0, 0, 5000, 250) // minhst 3
	for _, a := range []*rpc.Address{early, late} {
		if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 5)}, nil, a); !r.OK() {
			t.Fatalf("mint: %v", r.Err)
		}
	}
	if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 5}, early); !r.OK() {
		t.Fatalf("register early: %v", r.Err)
	}
	if r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{zero: 1000000}, treasury); !r.OK() {
		t.Fatalf("deposit: %v", r.Err)
	}
	if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 5}, late); !r.OK() {
		t.Fatalf("register late: %v", r.Err)
	}
	if r := c.Call(scid, "Claim", nil, nil, late); r.Sent(zero, late) != 0 {
		t.Fatalf("late registrant claimed %d of a distributed deposit", r.Sent(zero, late))
	}
	if r := c.Call(scid, "Claim", nil, nil, early); r.Sent(zero, early) != 1000000 {
		t.Fatalf("early holder got %d, want the whole 1000000", r.Sent(zero, early))
	}
	// and no double claim: a second Claim pays nothing
	if r := c.Call(scid, "Claim", nil, nil, early); r.Sent(zero, early) != 0 {
		t.Fatalf("double claim paid %d", r.Sent(zero, early))
	}
	checkFounders(t, c, scid)
}

// ADVERSARIAL: Distribute() succeeds and moves nothing while 0 < hst < minhst, and app/nfts.js offers the
// "Release waiting DERO" button on exactly that condition (waiting > 0 && hst > 0), so the button burns a fee
// and changes nothing.
func TestFoundersDistributeIsANoOpBelowTheFloor(t *testing.T) {
	c := harness.New()
	creator, treasury, a := c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid := installFounders(t, c, creator, treasury, 0, 500, 0, 0, 5000, 250) // minhst 11
	if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 5)}, nil, a); !r.OK() {
		t.Fatalf("mint: %v", r.Err)
	}
	if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 5}, a); !r.OK() {
		t.Fatalf("register: %v", r.Err)
	}
	if r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{zero: 777777}, treasury); !r.OK() {
		t.Fatalf("deposit: %v", r.Err)
	}
	r := c.Call(scid, "Distribute", nil, nil, a)
	if !r.OK() {
		t.Fatalf("distribute: %v", r.Err)
	}
	if c.U(scid, "und") != 777777 || len(r.Transfers) != 0 {
		t.Fatalf("und %d transfers %d", c.U(scid, "und"), len(r.Transfers))
	}
	t.Logf("hst 5 of minhst 11: Distribute returns 0, moves nothing, and costs the caller a fee; und stays %d", c.U(scid, "und"))
}

// ADVERSARIAL: the app shows only the last 300 listing ids (app/founders.js:21). `ln` only ever grows and List
// costs nothing but gas, so one unit cycled through List/Unlist 300 times pushes every genuine listing out of
// the app's window while leaving it on chain.
func TestFoundersListingWindowIsPushedOutByOneUnit(t *testing.T) {
	c := harness.New()
	creator, treasury, seller, griefer := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid := installFounders(t, c, creator, treasury, 0, 500, 0, 0, 5000, 250)
	if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 2)}, nil, seller); !r.OK() {
		t.Fatalf("mint seller: %v", r.Err)
	}
	if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 1)}, nil, griefer); !r.OK() {
		t.Fatalf("mint griefer: %v", r.Err)
	}
	if r := c.Call(scid, "List", rpc.Arguments{harness.U("price", 100000)}, map[crypto.Hash]uint64{scid: 2}, seller); !r.OK() {
		t.Fatalf("list: %v", r.Err)
	}
	genuine := c.U(scid, "ln") - 1
	for i := 0; i < 300; i++ { // one unit, cycled
		id := c.U(scid, "ln")
		if r := c.Call(scid, "List", rpc.Arguments{harness.U("price", 10000000000000)}, map[crypto.Hash]uint64{scid: 1}, griefer); !r.OK() {
			t.Fatalf("spam list %d: %v", i, r.Err)
		}
		if r := c.Call(scid, "Unlist", rpc.Arguments{harness.U("id", id)}, nil, griefer); !r.OK() {
			t.Fatalf("spam unlist %d: %v", i, r.Err)
		}
	}
	ln := c.U(scid, "ln")
	windowFrom := uint64(0)
	if ln > 300 {
		windowFrom = ln - 300
	}
	t.Logf("ln %d, the app reads ids %d..%d; the genuine listing is id %d and the griefer's 300 ids are all deleted",
		ln, windowFrom, ln-1, genuine)
	if genuine >= windowFrom {
		t.Fatalf("genuine listing %d still inside the window %d..", genuine, windowFrom)
	}
	if !c.Exists(scid, "lu_"+itoa(genuine)) || c.U(scid, "la") != 1 {
		t.Fatal("the genuine listing should still be live on chain")
	}
	t.Logf("on chain la=%d (one live listing); in the app the marketplace renders empty", c.U(scid, "la"))
}
