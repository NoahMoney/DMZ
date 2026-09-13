package xmrswap

// DMZ's swap fee (fee.go, docs/XMR_SWAP.md §2.2): 0.5% of the XMR, paid by the maker's sweep after a Claim, to the
// treasury deploy/networks.json names, and by nobody on any other exit.

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	mrand "math/rand"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// testMoneroAddress is a fresh standard address, or a subaddress, on net.
func testMoneroAddress(t *testing.T, net Network, sub bool) string {
	t.Helper()
	spend, err := NewScalar(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	view, err := NewScalar(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if !sub {
		a, err := Address(spend.Public(), view.Public(), net)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	_, subPrefix, err := net.prefixes()
	if err != nil {
		t.Fatal(err)
	}
	s, v := spend.Public(), view.Public()
	return encodeAddr(subPrefix, append(s[:], v[:]...))
}

// TestSwapFeeIsHalfAPercentRoundedDown: floor(amount × 50 / 10000), exactly, for every amount a uint64 holds.
func TestSwapFeeIsHalfAPercentRoundedDown(t *testing.T) {
	for _, c := range []struct{ amount, want uint64 }{
		{0, 0}, {1, 0}, {199, 0}, {200, 1}, {399, 1}, {400, 2}, {10_000, 50},
		{testXmr, 5_000_000_000}, {123_456_789_012, 617_283_945},
	} {
		if got := SwapFee(c.amount); got != c.want {
			t.Fatalf("SwapFee(%d) = %d, want %d", c.amount, got, c.want)
		}
	}
	ref := func(a uint64) uint64 {
		n := new(big.Int).SetUint64(a)
		return n.Mul(n, big.NewInt(XmrFeeBps)).Quo(n, big.NewInt(10000)).Uint64()
	}
	rng := mrand.New(mrand.NewSource(7))
	for i := 0; i < 20000; i++ {
		a := rng.Uint64() >> uint(rng.Intn(64))
		if got, want := SwapFee(a), ref(a); got != want {
			t.Fatalf("SwapFee(%d) = %d, want %d", a, got, want)
		}
	}
	if got, want := SwapFee(^uint64(0)), ref(^uint64(0)); got != want {
		t.Fatalf("SwapFee(max) = %d, want %d: the product overflowed", got, want)
	}
}

// TestCheckTreasury: a treasury must be a standard address or a subaddress on the swap's Monero network; an integrated
// address, a broken one or one on another network is refused everywhere, and an empty one on mainnet. Every refusal
// names the field.
func TestCheckTreasury(t *testing.T) {
	mainStd, mainSub := testMoneroAddress(t, Mainnet, false), testMoneroAddress(t, Mainnet, true)
	stageStd, stageSub := testMoneroAddress(t, Stagenet, false), testMoneroAddress(t, Stagenet, true)
	testStd := testMoneroAddress(t, Testnet, false)
	pa, err := ParseAddress(mainStd)
	if err != nil {
		t.Fatal(err)
	}
	integrated := encodeAddr(19, append(append(pa.Spend[:], pa.View[:]...), 1, 2, 3, 4, 5, 6, 7, 8))
	broken := mainStd[:len(mainStd)-1] + map[bool]string{true: "2", false: "1"}[mainStd[len(mainStd)-1] == '1']
	for _, c := range []struct {
		name    string
		addr    string
		net     Network
		mainnet bool
		ok      bool
	}{
		{"mainnet, a standard address", mainStd, Mainnet, true, true},
		{"mainnet, a subaddress", mainSub, Mainnet, true, true},
		{"stagenet, a standard address", stageStd, Stagenet, false, true},
		{"stagenet, a subaddress", stageSub, Stagenet, false, true},
		{"testnet, a standard address", testStd, Testnet, false, true},
		{"regtest (mainnet's form, against the simulator)", mainStd, Mainnet, false, true},
		{"mainnet, none", "", Mainnet, true, false},
		{"stagenet, none: no fee", "", Stagenet, false, true},
		{"regtest, none: no fee", "", Mainnet, false, true},
		{"mainnet, a stagenet address", stageStd, Mainnet, true, false},
		{"stagenet, a mainnet address", mainStd, Stagenet, false, false},
		{"stagenet, a testnet address", testStd, Stagenet, false, false},
		{"mainnet, an integrated address", integrated, Mainnet, true, false},
		{"mainnet, a checksum broken by one character", broken, Mainnet, true, false},
		{"mainnet, not an address", "treasury", Mainnet, true, false},
	} {
		err := CheckTreasury(c.addr, c.net, c.mainnet)
		if (err == nil) != c.ok {
			t.Fatalf("%s: %v", c.name, err)
		}
		if err != nil && !strings.Contains(err.Error(), "xmrTreasury") {
			t.Fatalf("%s: the refusal does not name the field: %v", c.name, err)
		}
	}
}

// TestASwapWithoutAValidTreasuryDoesNotStart: the library refuses the swap itself, for either role, whatever called it —
// on mainnet with no treasury or an invalid one, and anywhere with one on another network. Monero mainnet counts as
// mainnet against anything but the simulator, so a mix the command line refuses elsewhere still fails closed here.
func TestASwapWithoutAValidTreasuryDoesNotStart(t *testing.T) {
	mainSub := testMoneroAddress(t, Mainnet, true)
	stageStd := testMoneroAddress(t, Stagenet, false)
	for _, c := range []struct {
		name     string
		net      Network
		deroNet  string
		treasury string
		refused  bool
	}{
		{"mainnet, none recorded", Mainnet, "mainnet", "", true},
		{"mainnet, a stagenet address", Mainnet, "mainnet", stageStd, true},
		{"mainnet, not an address", Mainnet, "mainnet", "treasury", true},
		{"mainnet, a subaddress", Mainnet, "mainnet", mainSub, false},
		{"Monero mainnet against a DERO test network, none recorded", Mainnet, "testnet", "", true},
		{"regtest against the simulator, none recorded: no fee", Mainnet, "simulator", "", false},
		{"stagenet, a mainnet address", Stagenet, "testnet", mainSub, true},
		{"stagenet, none recorded: no fee", Stagenet, "testnet", "", false},
	} {
		for _, role := range []string{"bob", "alice"} {
			cfg := Config{HTLC: testHTLC, AmountDero: testDero, AmountXmr: testXmr, Net: c.net, DeroNet: c.deroNet, MyDero: bobAddr, XmrTreasury: c.treasury}
			st := NewState(filepath.Join(t.TempDir(), role+".json"), role, testHTLC)
			var err error
			if role == "bob" {
				_, err = NewBob(cfg, st, nil, nil, nil, nil)
			} else {
				_, err = NewAlice(cfg, st, nil, nil, nil, nil)
			}
			if named := err != nil && strings.Contains(err.Error(), "xmrTreasury"); named != c.refused {
				t.Fatalf("%s, %s: %v", c.name, role, err)
			}
		}
	}
}

// TestTheFeeIsTheMakersOnly: only the maker's sweep carries a fee, and only with a treasury; it rounds down, to no fee
// output at all below 200 piconero.
func TestTheFeeIsTheMakersOnly(t *testing.T) {
	tr := testMoneroAddress(t, Stagenet, false)
	for _, c := range []struct {
		role     string
		amount   uint64
		treasury string
		want     uint64
	}{
		{"bob", testXmr, tr, 5_000_000_000},
		{"bob", 199, tr, 0},
		{"bob", 200, tr, 1},
		{"alice", testXmr, tr, 0},
		{"bob", testXmr, "", 0},
	} {
		s := &Swap{st: &State{Role: c.role, Terms: Terms{AmountXmr: c.amount}}, cfg: Config{XmrTreasury: c.treasury}}
		if got := s.swapFee(); got != c.want {
			t.Fatalf("%s, %d piconero, treasury %q: fee %d, want %d", c.role, c.amount, c.treasury, got, c.want)
		}
	}
}

// TestTheFeeSweepIsOneTransactionThatLeavesNothingBehind: the client's fee-bearing sweep, against the wallet-rpc's own
// request shape — one transaction, exactly the fee to the treasury as the treasury's wallet sees it, the rest to the
// maker less the network fee, nothing left at the joint address — and a fee of 0 or one the balance cannot carry is
// refused before anything is sent.
func TestTheFeeSweepIsOneTransactionThatLeavesNothingBehind(t *testing.T) {
	ctx := context.Background()
	fw := NewFakeWallet(Stagenet)
	srv := httptest.NewServer(fw)
	defer srv.Close()
	w := NewWalletRPC(srv.URL, "", "")
	mk := func(name string) string {
		spend, _ := NewScalar(rand.Reader)
		view, _ := NewScalar(rand.Reader)
		addr, err := Address(spend.Public(), view.Public(), Stagenet)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.GenerateFromKeys(ctx, name, addr, view.Hex(), spend.Hex(), "", 0); err != nil {
			t.Fatal(err)
		}
		return addr
	}
	treasury, bob, joint := mk("treasury"), mk("bob"), mk("joint") // the joint wallet is left open
	fw.Fund(joint, testXmr)
	fw.Mine(12)
	fee := SwapFee(testXmr)
	for _, f := range []uint64{0, testXmr, testXmr + 1} {
		_, err := w.SweepWithFee(ctx, bob, testXmr, treasury, f)
		var rerr *RPCError
		if !errors.As(err, &rerr) || !refusedBeforeSending(rerr.Code) {
			t.Fatalf("a fee of %d out of %d: %v", f, testXmr, err)
		}
	}
	bal, err := w.UnlockedBalance(ctx)
	if err != nil || bal != testXmr {
		t.Fatalf("joint balance %d (%v)", bal, err)
	}
	txs, err := w.SweepWithFee(ctx, bob, bal, treasury, fee)
	if err != nil || len(txs) != 1 {
		t.Fatalf("the fee sweep: %v %v", txs, err)
	}
	if out, err := w.Outgoing(ctx); err != nil || len(out) != 1 || out[0] != txs[0] {
		t.Fatalf("the joint wallet lists %v (%v) as its sends, want the one transaction %s", out, err, txs[0])
	}
	fw.Mine(1)
	fw.mu.Lock()
	var toTreasury, toBob uint64
	for _, tx := range fw.txs {
		if tx.from != joint {
			continue
		}
		if tx.id != txs[0] {
			t.Errorf("a second transaction from the joint address: %s", tx.id)
		}
		switch tx.to {
		case treasury:
			toTreasury += tx.amount
		case bob:
			toBob += tx.amount
		default:
			t.Errorf("the sweep paid %s", tx.to)
		}
	}
	_, left := fw.balance(joint)
	fw.mu.Unlock()
	if toTreasury != fee || toBob != testXmr-fee-FakeFee || left != 0 {
		t.Fatalf("treasury %d (want %d), maker %d (want %d), left at the joint address %d", toTreasury, fee, toBob, testXmr-fee-FakeFee, left)
	}
	if err := w.OpenWallet(ctx, "treasury", ""); err != nil {
		t.Fatal(err)
	}
	if in, err := w.Incoming(ctx, 0); err != nil || len(in) != 1 || in[0].Amount != fee || in[0].TxID != txs[0] {
		t.Fatalf("the treasury's own wallet sees %+v (%v), want %d in %s", in, err, fee, txs[0])
	}
}

// TestTheSwapFeeIsPaidOnAClaimAndOnNothingElse: every exit, driven to its end with the fee in force. The treasury is paid
// exactly SwapFee once, by the maker's sweep, when the swap ends in a Claim, and nothing on a Refund, a Punish or a
// Reclaim; the taker's companion states the fee and locks the agreed amount (checkTransfer), and her recovery sweep is
// whole (check).
func TestTheSwapFeeIsPaidOnAClaimAndOnNothingElse(t *testing.T) {
	for _, scenario := range []string{"happy", "refund", "punish", "reclaim"} {
		t.Run(scenario, func(t *testing.T) {
			r := newRun(t, plan{scenario: scenario, seed: 301})
			r.drive()
			r.check()
			bob := r.loadState("bob")
			n, amt := r.paidTo(bob.Joint, r.treasury)
			switch {
			case scenario == "happy" && (n != 1 || amt != SwapFee(testXmr) || bob.Sweep.Fee != SwapFee(testXmr)):
				t.Fatalf("a Claim: %d payments of %d to the treasury, bob's state records %d", n, amt, bob.Sweep.Fee)
			case scenario != "happy" && (n != 0 || bob.Sweep.Fee != 0):
				t.Fatalf("%s: %d payments of %d to the treasury, bob's state records %d", scenario, n, amt, bob.Sweep.Fee)
			}
			said := false
			for _, l := range r.logs {
				said = said || (strings.Contains(l, "alice: DMZ's swap fee is the maker's") && strings.Contains(l, r.treasury))
			}
			if !said {
				t.Fatal("alice's companion never stated the fee the maker pays")
			}
			t.Logf("%s: bob %s, treasury paid %d", scenario, bob.Phase, amt)
		})
	}
}
