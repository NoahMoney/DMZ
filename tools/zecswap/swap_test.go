package zecswap

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	mrand "math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// The two state machines, against the fake Zcash node (through ZcashRPC, over HTTP) and the fake board and escrow.

// world is one swap's universe: both chains, both parties' state files, a clock the test moves.
type world struct {
	t           *testing.T
	ctx         context.Context
	dir         string
	p           Policy
	dero        *fakeDero
	zc          *fakeZcash
	rng         *mrand.Rand
	mu          sync.Mutex
	logs        []string
	sw          map[string]*Swap
	crashOnce   map[string]map[string]bool
	randomCrash float64
	offline     func(w *world, role string) bool
	restarts    map[string]int
	stepErrors  int
	fundings    map[string]bool
	claims      map[string]bool
	takerOn     bool
	firstFund   bool
	cfgHook     func(role string, c *Config)
	zcFor       map[string]*fakeZcash
}

func newWorld(t *testing.T, seed int64) *world {
	w := &world{t: t, ctx: context.Background(), dir: t.TempDir(), p: testPolicy(), rng: mrand.New(mrand.NewSource(seed)),
		sw: map[string]*Swap{}, crashOnce: map[string]map[string]bool{RoleMaker: {}, RoleTaker: {}}, restarts: map[string]int{},
		fundings: map[string]bool{}, claims: map[string]bool{}, zcFor: map[string]*fakeZcash{}}
	w.dero = newFakeDero(startDero)
	w.zc = newFakeZcash(t, "test", startZec)
	w.zc.Pay(P2PKHScript(testKey(takerKeyN).PubKeyHash()), takerCoins)
	w.zc.onSend = w.checkSend
	return w
}

func (w *world) logf(format string, a ...interface{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.logs = append(w.logs, fmt.Sprintf(format, a...))
}

func (w *world) dump() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Join(w.logs, "\n")
}

func (w *world) path(role string) string { return filepath.Join(w.dir, role+".json") }

func (w *world) state(role string) *State {
	st, err := LoadState(w.path(role), role)
	if err != nil {
		w.t.Fatalf("%s: %v", role, err)
	}
	return st
}

func (w *world) node(role string) *fakeZcash {
	if f := w.zcFor[role]; f != nil {
		return f
	}
	return w.zc
}

func (w *world) config(role string) Config {
	c := Config{Policy: w.p, Board: testBoard, Escrow: testEscrow, Log: w.logf}
	if role == RoleMaker {
		c.MyDero, c.Key, c.GiveAmount, c.WantAmount = makerAddr, keySource(makerKeyN), testGive, testWant
	} else {
		c.MyDero, c.Key, c.HaveOffer, c.OfferID = takerAddr, keySource(takerKeyN), true, 0
	}
	if w.cfgHook != nil {
		w.cfgHook(role, &c)
	}
	return c
}

func (w *world) crashFn(role string) func(string) bool {
	return func(phase string) bool {
		if w.crashOnce[role][phase] {
			delete(w.crashOnce[role], phase)
			return true
		}
		return w.randomCrash > 0 && w.rng.Float64() < w.randomCrash
	}
}

func (w *world) start(role string) *Swap {
	st := w.state(role)
	addr := makerAddr
	if role == RoleTaker {
		addr = takerAddr
	}
	var sw *Swap
	var err error
	if role == RoleMaker {
		sw, err = NewMaker(w.config(role), st, newMemDero(w.dero, addr), w.node(role).client())
	} else {
		sw, err = NewTaker(w.config(role), st, newMemDero(w.dero, addr), w.node(role).client())
	}
	if err != nil {
		w.t.Fatalf("%s: %v", role, err)
	}
	sw.crash = w.crashFn(role)
	w.restarts[role]++
	return sw
}

func (w *world) step(role string) {
	if w.sw[role] == nil {
		w.sw[role] = w.start(role)
	}
	_, err := w.sw[role].Step(w.ctx)
	if errors.Is(err, errCrashed) {
		w.sw[role] = nil
		return
	}
	if err != nil {
		w.stepErrors++
		w.logf("%s: step error: %v", role, err)
	}
}

func (w *world) done(role string) bool { return w.state(role).Done }

// round is one tick of the clock: both parties step (in a random order), DERO moves four blocks and Zcash one —
// 72 s against 75 s.
func (w *world) round() {
	roles := []string{RoleMaker, RoleTaker}
	if w.rng.Intn(2) == 0 {
		roles[0], roles[1] = roles[1], roles[0]
	}
	for _, role := range roles {
		if role == RoleTaker && !w.takerOn {
			w.dero.mu.Lock()
			has := len(w.dero.offers) > 0
			w.dero.mu.Unlock()
			if !has {
				continue
			}
			w.takerOn = true
		}
		if w.done(role) || (w.offline != nil && w.offline(w, role)) {
			continue
		}
		w.step(role)
	}
	w.dero.Advance(4)
	w.zc.Mine(1)
	for _, f := range w.zcFor {
		if f != w.zc {
			f.Mine(1)
		}
	}
}

func (w *world) drive(max int) bool {
	for i := 0; i < max; i++ {
		if w.done(RoleMaker) && w.takerOn && w.done(RoleTaker) {
			return true
		}
		w.round()
	}
	return w.done(RoleMaker) && w.takerOn && w.done(RoleTaker)
}

// checkSend is every invariant that must hold at the moment a transaction reaches the Zcash node. It runs under the
// node's lock, so it reads the node through its *Locked methods only.
func (w *world) checkSend(f *fakeZcash, tx *Tx) error {
	id := DisplayHex(tx.TxID())
	tk, err := LoadState(w.path(RoleTaker), RoleTaker)
	if err != nil || tk.Hash == "" || tk.RefundPkh == "" {
		return nil
	}
	hc, err := (&Swap{st: tk}).htlc()
	if err != nil {
		return nil
	}
	spk := hc.ScriptPubKey()
	for _, o := range tx.Out {
		if !bytes.Equal(o.ScriptPubKey, spk) {
			continue
		}
		w.fundings[id] = true
		if len(w.fundings) > 1 {
			return errors.New("a second, different funding of the same swap")
		}
		if tk.FundingTxID != id || tk.FundingHex != hex.EncodeToString(tx.Serialize()) {
			return errors.New("a funding that is not the one stored before it was sent")
		}
		if tk.Phase != "funding" && tk.Phase != "funded" && tk.Phase != "refunding" {
			return fmt.Errorf("a funding sent from phase %s", tk.Phase)
		}
		b, err := os.ReadFile(tk.RefundFile)
		if err != nil || len(tk.Refunds) == 0 || !strings.Contains(string(b), tk.Refunds[0].Hex) {
			return errors.New("the funding went out before the pre-signed refund was in the refund file")
		}
		if !w.firstFund {
			w.firstFund = true
			w.dero.mu.Lock()
			h := w.dero.height
			var e Escrow
			if tk.EscrowID < uint64(len(w.dero.escrows)) {
				e = w.dero.escrows[tk.EscrowID]
			}
			w.dero.mu.Unlock()
			if e.State != EscrowOpen || e.Hash != tk.Hash || e.Taker != tk.MyRaw {
				return errors.New("funded against an escrow that is not this swap's open lock")
			}
			if h < DeroStableDepth || h-DeroStableDepth < tk.LockSeenHeight {
				return errors.New("funded before the lock was past DERO's stable depth")
			}
			if e.Expiry-h < w.p.TakerMinDeroLeft {
				return fmt.Errorf("funded with T_dero − now = %d", e.Expiry-h)
			}
		}
	}
	if tk.FundingTxID == "" {
		return nil
	}
	fundHash, _ := ParseDisplayHex(tk.FundingTxID)
	for _, in := range tx.In {
		if in.PrevOut != (OutPoint{Hash: fundHash, Index: FundingOutput}) {
			continue
		}
		kind, _ := ClassifySpend(in.ScriptSig, hc)
		switch kind {
		case SpendClaim:
			w.claims[id] = true
			if len(w.claims) > 1 {
				return errors.New("a second, different claim")
			}
			mk, _ := LoadState(w.path(RoleMaker), RoleMaker)
			if mk.ClaimTxID != id || (mk.Phase != "claiming" && mk.Phase != "claimed") {
				return fmt.Errorf("a claim not stored before it was sent (maker in %s)", mk.Phase)
			}
			h, mined := f.minedAtLocked(tk.FundingTxID)
			if !mined || f.tipLocked()-h+1 < w.p.ConfZec {
				return fmt.Errorf("a claim of a funding %v mined, at depth %d", mined, f.tipLocked()-h+1)
			}
			if !w.p.ClaimOpen(f.tipLocked(), hc.CLTV) {
				return fmt.Errorf("a claim at tip %d past the cutoff for cltv %d", f.tipLocked(), hc.CLTV)
			}
		case SpendRefund:
			ok := false
			for _, r := range tk.Refunds {
				ok = ok || r.TxID == id
			}
			if !ok {
				return errors.New("a refund that is not one of the pre-signed ones")
			}
		}
	}
	return nil
}

// check is what holds at the end of every run, whatever died where.
func (w *world) check(name string, makerWant, takerWant []string) {
	t := w.t
	mk, tk := w.state(RoleMaker), w.state(RoleTaker)
	if !mk.Done || !tk.Done {
		t.Fatalf("%s: not finished: maker %s, taker %s\n%s", name, mk.Phase, tk.Phase, w.dump())
	}
	in := func(x string, xs []string) bool {
		for _, v := range xs {
			if v == x {
				return true
			}
		}
		return false
	}
	if !in(mk.Phase, makerWant) || !in(tk.Phase, takerWant) {
		t.Fatalf("%s: maker %s (%s), taker %s (%s); want %v / %v\n%s", name, mk.Phase, mk.Outcome, tk.Phase, tk.Outcome, makerWant, takerWant, w.dump())
	}
	// neither party ends with both legs while the other ends with neither
	escrowState := uint64(99)
	if tk.HaveEscrow {
		escrowState = w.dero.Escrows()[tk.EscrowID].State
	}
	var spender *Tx
	if tk.FundingTxID != "" {
		spender = w.zc.SpenderOf(tk.FundingTxID, FundingOutput)
	}
	if spender != nil {
		hc, _ := (&Swap{st: tk}).htlc()
		kind, _ := ClassifySpend(spender.In[0].ScriptSig, hc)
		if kind == SpendClaim && escrowState != EscrowClaimed {
			t.Fatalf("%s: the maker claimed the ZEC and the escrow is in state %d\n%s", name, escrowState, w.dump())
		}
		if kind == SpendRefund && escrowState == EscrowClaimed {
			t.Fatalf("%s: the taker refunded the ZEC and also claimed the DERO\n%s", name, w.dump())
		}
		if hc2 := hc.ScriptPubKey(); w.zc.OutputsTo(hc2) > 1 {
			t.Fatalf("%s: the script was funded more than once", name)
		}
	}
	if len(w.fundings) > 1 || len(w.claims) > 1 {
		t.Fatalf("%s: %d fundings, %d claims", name, len(w.fundings), len(w.claims))
	}
	// swap.bas takes a second Lock under the same hash as a second escrow, which the one secret opens as well
	if n := len(w.dero.Escrows()); n > 1 {
		t.Fatalf("%s: the maker locked %d times\n%s", name, n, w.dump())
	}
	// board.bas and swap.bas judge a call at the height it executes; none whose heights the companions set may be
	// refused there
	for _, call := range []string{"Post", "Accept", "Lock"} {
		if n := w.dero.Refused(call); n > 0 {
			t.Fatalf("%s: %d %s transaction(s) mined and refused by the contract, each a fee lost\n%s", name, n, call, w.dump())
		}
	}
	if mk.Secret != "" && strings.Contains(w.dump(), mk.Secret) {
		t.Fatalf("%s: the secret appears in a log line", name)
	}
	if m, _ := filepath.Glob(filepath.Join(w.dir, "*.tmp")); len(m) > 0 {
		t.Fatalf("%s: temporary files left: %v", name, m)
	}
}

// offlineFor is who is away when, per scenario.
func offlineFor(scenario string) func(w *world, role string) bool {
	switch scenario {
	case "maker_absent":
		// the maker is away from his lock until past cltv: the taker must refund, and he must never claim
		return func(w *world, role string) bool {
			if role != RoleMaker {
				return false
			}
			st := w.state(RoleMaker)
			return st.Phase == "locked" && st.CLTV != 0 && w.zc.Tip() < st.CLTV+2
		}
	case "taker_late":
		// the taker is away after funding until well after the maker's claim, but back before T_dero
		return func(w *world, role string) bool {
			if role != RoleTaker {
				return false
			}
			st := w.state(RoleTaker)
			return st.Phase == "funded" && st.Funding != nil && w.zc.Tip() < st.Funding.Height+w.p.ConfZec+6
		}
	case "maker_never_locks":
		// the maker is away after posting until the taker has walked
		return func(w *world, role string) bool {
			if role != RoleMaker {
				return false
			}
			return w.state(RoleMaker).Phase == "posted" && !w.state(RoleTaker).Done
		}
	}
	return nil
}

var scenarioOutcomes = map[string][2][]string{
	"happy":             {{"swapped"}, {"swapped"}},
	"taker_late":        {{"swapped"}, {"swapped"}},
	"maker_absent":      {{"refunded"}, {"refunded"}},
	"maker_never_locks": {{"refunded", "cancelled"}, {"walked"}},
}

func runScenario(t *testing.T, scenario string, seed int64) *world {
	w := newWorld(t, seed)
	w.offline = offlineFor(scenario)
	if !w.drive(400) {
		t.Fatalf("%s did not finish: maker %s, taker %s\n%s", scenario, w.state(RoleMaker).Phase, w.state(RoleTaker).Phase, w.dump())
	}
	o := scenarioOutcomes[scenario]
	w.check(scenario, o[0], o[1])
	return w
}

func TestHappyPath(t *testing.T) {
	w := runScenario(t, "happy", 1)
	tk := w.state(RoleTaker)
	if w.dero.Escrows()[tk.EscrowID].State != EscrowClaimed || len(w.claims) != 1 || len(w.fundings) != 1 {
		t.Fatal("the swap did not settle both legs")
	}
	// the refund file holds the three levels, and none of them was needed
	b, _ := os.ReadFile(tk.RefundFile)
	for _, r := range tk.Refunds {
		if !strings.Contains(string(b), r.Hex) {
			t.Fatal("a refund level is missing from the refund file")
		}
	}
	if len(tk.Refunds) != len(RefundFeeLevels) {
		t.Fatalf("%d refund levels", len(tk.Refunds))
	}
}

func TestTakerRefundsWhenMakerIsAbsent(t *testing.T) { runScenario(t, "maker_absent", 2) }
func TestTakerLateStillClaims(t *testing.T)          { runScenario(t, "taker_late", 3) }
func TestMakerNeverLocksTakerWalks(t *testing.T)     { runScenario(t, "maker_never_locks", 4) }

// ---- the attacks, each with its test ----

func (w *world) insertOffer(o Offer) {
	w.dero.mu.Lock()
	defer w.dero.mu.Unlock()
	o.ID = uint64(len(w.dero.offers))
	w.dero.offers = append(w.dero.offers, o)
}

func (w *world) makerOffer(hash string) Offer {
	raw, _ := DeroRaw(makerAddr)
	return Offer{Maker: raw, Chain: ZcashChainLabel, GiveAmount: testGive, WantAmount: testWant, WantAddr: testKey(makerKeyN).Address(Testnet), Hash: hash, Expiry: startDero + 400}
}

func (w *world) stepN(role string, n int) {
	for i := 0; i < n && !w.done(role); i++ {
		w.step(role)
	}
}

// ZEC-2: the board carries the hash in upper case (as board.bas did before cffdd29); the taker refuses to accept.
func TestTakerRefusesUpperCaseHash(t *testing.T) {
	w := newWorld(t, 5)
	h, _ := htlcFor(1)
	w.insertOffer(w.makerOffer(strings.ToUpper(hex.EncodeToString(h.Hash[:]))))
	w.stepN(RoleTaker, 1)
	st := w.state(RoleTaker)
	if st.Phase != "declined" || !strings.Contains(st.Outcome, "lower-case") || w.dero.Offer(0).State != OfferOpen {
		t.Fatalf("taker %s: %s", st.Phase, st.Outcome)
	}
}

// Z8: the same hash on two board rows is one secret opening two swaps; the taker refuses.
func TestTakerRefusesReusedHash(t *testing.T) {
	w := newWorld(t, 6)
	h, _ := htlcFor(1)
	hash := hex.EncodeToString(h.Hash[:])
	w.insertOffer(w.makerOffer(hash))
	w.insertOffer(w.makerOffer(hash))
	w.cfgHook = func(role string, c *Config) { c.OfferID = 1 }
	w.stepN(RoleTaker, 1)
	if st := w.state(RoleTaker); st.Phase != "declined" || !strings.Contains(st.Outcome, "Z8") {
		t.Fatalf("taker %s: %s", st.Phase, st.Outcome)
	}
}

// makerUntilAccepted drives the maker to a posted offer and accepts it by hand with cltv; it returns the accept.
func (w *world) makerUntilAccepted(cltv uint64) {
	w.stepN(RoleMaker, 2)
	if w.state(RoleMaker).Phase != "posted" {
		w.t.Fatalf("maker in %s", w.state(RoleMaker).Phase)
	}
	pkh := testKey(takerKeyN).PubKeyHash()
	if _, err := newMemDero(w.dero, takerAddr).Accept(w.ctx, 0, hex.EncodeToString(pkh[:]), cltv); err != nil {
		w.t.Fatal(err)
	}
}

// ZEC-3: a cltv far outside the window is never locked against; the offer is cancelled once it expires.
func TestMakerDeclinesCLTVOutsidePolicy(t *testing.T) {
	w := newWorld(t, 7)
	w.makerUntilAccepted(uint64(w.zc.Tip()) + 500)
	for i := 0; i < 200 && !w.done(RoleMaker); i++ {
		w.step(RoleMaker)
		w.dero.Advance(4)
		w.zc.Mine(1)
	}
	st := w.state(RoleMaker)
	if st.Phase != "cancelled" || len(w.dero.Escrows()) != 0 || !strings.Contains(st.Outcome, "policy") {
		t.Fatalf("maker %s: %s; %d escrows", st.Phase, st.Outcome, len(w.dero.Escrows()))
	}
}

// INT-04: board.bas v2 took any Uint64 as cltv; 2^63 must be refused by the companion, not panic its script builder.
// board.bas v3 now refuses it at Accept (line 25); the companion's own guard stays for any board that does not, so
// the accept is written straight into the fake board's storage here, as a v2 board would have stored it.
func TestMakerDeclinesUnboundedCLTV(t *testing.T) {
	w := newWorld(t, 8)
	w.stepN(RoleMaker, 2)
	pkh := testKey(takerKeyN).PubKeyHash()
	if _, err := newMemDero(w.dero, takerAddr).Accept(w.ctx, 0, hex.EncodeToString(pkh[:]), 1<<63); err == nil {
		t.Fatal("board.bas v3 took a cltv of 2^63")
	}
	takerRaw, _ := DeroRaw(takerAddr)
	w.dero.mu.Lock()
	o := &w.dero.offers[0]
	o.State, o.Taker, o.RefundPkh, o.CLTV, o.AcceptDeadline = OfferAccepted, takerRaw, hex.EncodeToString(pkh[:]), 1<<63, w.dero.height+fakeAcceptWindow
	w.dero.mu.Unlock()
	w.stepN(RoleMaker, 1)
	if st := w.state(RoleMaker); st.Phase != "cancel_wait" || len(w.dero.Escrows()) != 0 {
		t.Fatalf("maker %s", st.Phase)
	}
}

// fundFor builds the taker's funding of the maker's script on a node, from the taker's coins there.
func fundFor(t *testing.T, f *fakeZcash, st *State) *Tx {
	hc, err := (&Swap{st: st}).htlc()
	if err != nil {
		t.Fatal(err)
	}
	key := testKey(takerKeyN)
	tx, err := BuildFunding(FundingRequest{Key: key, HTLC: hc, Amount: st.WantAmount, Coins: coinsOf(t, f.client(), key), BranchID: f.branch, ExpiryHeight: f.Tip() + 30})
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

func (w *world) makerLocked() *State {
	w.makerUntilAccepted(uint64(w.zc.Tip()) + uint64(w.p.CLTVWindow))
	w.stepN(RoleMaker, 3)
	st := w.state(RoleMaker)
	if st.Phase != "locked" {
		w.t.Fatalf("maker in %s:\n%s", st.Phase, w.dump())
	}
	return st
}

// A funding the maker saw, shallow, is removed by a reorganisation. He must never claim on that stale sight; when
// the funding is mined again he counts its depth from its new block.
func TestStaleFundingBeforeReorgIsNotClaimed(t *testing.T) {
	w := newWorld(t, 9)
	st := w.makerLocked()
	fund := fundFor(t, w.zc, st)
	fundID := DisplayHex(fund.TxID())
	// no taker machine runs here, so this test watches the node itself: every spend of the funding that reaches
	// it is counted, and must find the funding at least C_zec deep in the chain the node has at that moment
	w.zc.onSend = func(f *fakeZcash, tx *Tx) error {
		for _, in := range tx.In {
			if DisplayHex(in.PrevOut.Hash) != fundID {
				continue
			}
			w.claims[DisplayHex(tx.TxID())] = true
			h, mined := f.minedAtLocked(fundID)
			if !mined || f.tipLocked()-h+1 < w.p.ConfZec {
				return fmt.Errorf("a claim of a funding mined %v at depth %d", mined, f.tipLocked()-h+1)
			}
		}
		return nil
	}
	if _, code, msg := w.zc.Submit(fund.Serialize()); code != 0 {
		t.Fatal(msg)
	}
	w.zc.Mine(1)
	w.step(RoleMaker) // depth 1
	w.zc.Mine(1)
	w.step(RoleMaker) // depth 2
	w.zc.Reorg(2, false)
	w.zc.Mine(4) // the funding is gone: a chain without it, as long as the old one was and longer
	for i := 0; i < 3; i++ {
		w.step(RoleMaker)
	}
	if len(w.claims) != 0 || w.state(RoleMaker).Phase != "locked" {
		t.Fatalf("claimed on a funding a reorganisation removed (phase %s)", w.state(RoleMaker).Phase)
	}
	// mined again, in a new block: the depth starts over, and the checkSend hook holds the claim to C_zec there
	if _, code, msg := w.zc.Submit(fund.Serialize()); code != 0 {
		t.Fatal(msg)
	}
	w.zc.Mine(1)
	newHeight := w.zc.Tip()
	for i := 0; i < 10 && len(w.claims) == 0; i++ {
		w.step(RoleMaker)
		w.zc.Mine(1)
	}
	if len(w.claims) != 1 {
		t.Fatalf("the funding mined again was never claimed:\n%s", w.dump())
	}
	if f := w.state(RoleMaker).Funding; f == nil || f.Height != newHeight {
		t.Fatalf("the claim was built against %+v, not the funding in block %d", f, newHeight)
	}
}

// The consumed-funding register: one funding outpoint on one network is credited to one swap, once.
func TestFundingCreditedTwiceIsRefused(t *testing.T) {
	dir := t.TempDir()
	c, err := LoadConsumed(ConsumedPath(filepath.Join(dir, "maker.json")))
	if err != nil {
		t.Fatal(err)
	}
	key := FundingKey("test", strings.Repeat("ab", 32), 0)
	if err := c.Record(key, "swapA", testBoard, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.Record(key, "swapA", testBoard, 0); err != nil {
		t.Fatalf("the same swap binding its own funding again: %v", err)
	}
	if err := c.Record(key, "swapB", testBoard, 1); !errors.Is(err, ErrFundingConsumed) {
		t.Fatalf("a second swap crediting the same funding: %v", err)
	}
	// the network is part of the identity: the same outpoint named on another chain is another funding
	if err := c.Record(FundingKey("main", strings.Repeat("ab", 32), 0), "swapB", testBoard, 1); err != nil {
		t.Fatalf("the same outpoint on another network: %v", err)
	}
	// and the register is read from disk every time: a second companion in the directory sees the first's entries
	c2, _ := LoadConsumed(ConsumedPath(filepath.Join(dir, "other.json")))
	if err := c2.Record(key, "swapC", testBoard, 2); !errors.Is(err, ErrFundingConsumed) {
		t.Fatalf("a second companion crediting the same funding: %v", err)
	}
}

// A funding already credited to another swap is never claimed by this one, even when it pays this swap's script.
func TestSameFundingForTwoSwapsIsRefused(t *testing.T) {
	w := newWorld(t, 10)
	st := w.makerLocked()
	fund := fundFor(t, w.zc, st)
	if _, code, msg := w.zc.Submit(fund.Serialize()); code != 0 {
		t.Fatal(msg)
	}
	c, _ := LoadConsumed(ConsumedPath(w.path(RoleMaker)))
	if err := c.Record(FundingKey("test", DisplayHex(fund.TxID()), FundingOutput), "an-earlier-swap", testBoard, 99); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 60 && w.state(RoleMaker).Phase == "locked"; i++ {
		w.zc.Mine(1)
		w.dero.Advance(4)
		w.step(RoleMaker)
	}
	if len(w.claims) != 0 || w.state(RoleMaker).Phase != "refund_wait" {
		t.Fatalf("claimed a funding bound to another swap (phase %s)", w.state(RoleMaker).Phase)
	}
	if !strings.Contains(w.dump(), "already credited to swap an-earlier-swap") {
		t.Fatal("the refusal was not said")
	}
}

// A swap bound to Zcash mainnet, read through a testnet node, stops; nothing is signed or sent.
func TestTestnetNodeOnMainnetSwapIsRefused(t *testing.T) {
	w := newWorld(t, 11)
	st := NewState(w.path(RoleMaker), RoleMaker)
	st.ZcashChain = "main"
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	w.step(RoleMaker)
	if !strings.Contains(w.dump(), "lives on Zcash main") || len(w.zc.sent) != 0 || len(w.dero.offers) != 0 {
		t.Fatalf("a mainnet swap ran against a testnet node:\n%s", w.dump())
	}
}

// The maker's Zcash node lies (it shows a funding that exists nowhere else). A second, honest node does not
// confirm it, so the secret never leaves the maker's machine; when the cutoff passes he refunds.
func TestOneLyingZcashNodeBlocksTheReveal(t *testing.T) {
	w := newWorld(t, 12)
	liar := newFakeZcash(t, "test", startZec)
	liar.Pay(P2PKHScript(testKey(takerKeyN).PubKeyHash()), takerCoins)
	w.zcFor[RoleMaker] = liar
	honest := w.zc
	w.cfgHook = func(role string, c *Config) {
		if role == RoleMaker {
			c.ZcashName = "liar"
			c.ZcashWitnesses = []ZcashSource{{Name: "honest", Node: honest.client()}}
		}
	}
	st := w.makerLocked()
	liar.MineTx(fundFor(t, liar, st))
	liar.Mine(int(w.p.ConfZec) + 1)
	for i := 0; i < 60 && !w.done(RoleMaker); i++ {
		w.step(RoleMaker)
		liar.Mine(1)
		honest.Mine(1)
		w.dero.Advance(4)
	}
	if len(liar.sent) != 0 || len(honest.sent) != 0 {
		t.Fatalf("a transaction was sent: liar %v, honest %v", liar.sent, honest.sent)
	}
	if !strings.Contains(w.dump(), "honest does NOT confirm the funding") || !strings.Contains(w.dump(), "liar confirms the funding") {
		t.Fatalf("each node's answer was not logged:\n%s", w.dump())
	}
	if ph := w.state(RoleMaker).Phase; ph != "refunded" && ph != "refund_wait" && ph != "dero_refunding" {
		t.Fatalf("maker in %s", ph)
	}
}

// The taker's DERO node lies (it shows a lock the chain does not have — here, a second node that has no lock at
// all disagrees with the first). She never funds.
func TestOneLyingDeroNodeBlocksTheFunding(t *testing.T) {
	w := newWorld(t, 13)
	other := newFakeDero(startDero)
	w.cfgHook = func(role string, c *Config) {
		if role == RoleTaker {
			c.DeroWitnesses = []DeroSource{{Name: "witness", Node: newMemDero(other, takerAddr)}}
		}
	}
	if !w.drive(400) {
		t.Fatalf("did not finish:\n%s", w.dump())
	}
	if len(w.fundings) != 0 || w.zc.OutputsTo(func() []byte { hc, _ := (&Swap{st: w.state(RoleTaker)}).htlc(); return hc.ScriptPubKey() }()) != 0 {
		t.Fatal("the taker funded against a lock one of her nodes does not show")
	}
	if !strings.Contains(w.dump(), "witness does NOT confirm escrow") {
		t.Fatalf("the witness's answer was not logged:\n%s", w.dump())
	}
	w.check("dero witness disagrees", []string{"refunded"}, []string{"declined"})
}

// The maker pads the script's address until getaddressutxos is refused. The taker's reads never list the address,
// so she still refunds at cltv.
func TestTakerRefundsEvenWhenTheAddressIsPadded(t *testing.T) {
	w := newWorld(t, 14)
	w.offline = offlineFor("maker_absent")
	for i := 0; i < 400 && !(w.done(RoleMaker) && w.done(RoleTaker)); i++ {
		if w.state(RoleTaker).Phase == "funded" && w.zc.override == nil {
			w.zc.mu.Lock()
			w.zc.override = func(method string) ([]byte, int, bool) {
				return []byte(`{"result":null,"error":{"code":-1,"message":"too many outputs"},"id":1}`), 500, method == "getaddressutxos"
			}
			w.zc.mu.Unlock()
		}
		w.round()
	}
	w.check("address padded", []string{"refunded"}, []string{"refunded"})
}

// Z-B: the taker disappears for good after funding. At cltv a third party broadcasts level 1 of the refund file;
// it can only pay her.
func TestPreSignedRefundCanBeBroadcastByAnyone(t *testing.T) {
	w := newWorld(t, 15)
	w.offline = func(w *world, role string) bool {
		if role == RoleTaker {
			ph := w.state(RoleTaker).Phase
			return ph == "funded" || ph == "refunding"
		}
		return offlineFor("maker_absent")(w, role)
	}
	var tk *State
	for i := 0; i < 400; i++ {
		w.round()
		tk = w.state(RoleTaker)
		if tk.Phase == "funded" && w.zc.Tip() >= tk.CLTV {
			break
		}
	}
	b, err := os.ReadFile(tk.RefundFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	var level1 string
	for i, l := range lines {
		if strings.HasPrefix(l, "level 1 ") {
			level1 = lines[i+1]
		}
	}
	raw, err := hex.DecodeString(level1)
	if err != nil {
		t.Fatalf("the refund file has no level 1 hex:\n%s", b)
	}
	if _, code, msg := w.zc.Submit(raw); code != 0 {
		t.Fatalf("a third party's broadcast of the refund at cltv: %s", msg)
	}
	w.zc.Mine(1)
	sp := w.zc.SpenderOf(tk.FundingTxID, FundingOutput)
	if sp == nil || !bytes.Equal(sp.Out[0].ScriptPubKey, P2PKHScript(testKey(takerKeyN).PubKeyHash())) || len(sp.Out) != 1 {
		t.Fatal("the refund did not pay the taker's key and nothing else")
	}
	if !strings.Contains(string(b), "WHAT IT CANNOT FIX") {
		t.Fatal("the refund file does not say what it cannot fix")
	}
}

// A network upgrade scheduled before T_dero would invalidate the refund while it is still needed: no funding.
func TestUpgradeBeforeTDeroDeclinesFunding(t *testing.T) {
	w := newWorld(t, 16)
	w.zc.pending = []NodeUpgrade{{Name: "NU7", BranchID: 0x77777777, Height: startZec + 40}}
	if !w.drive(400) {
		t.Fatalf("did not finish:\n%s", w.dump())
	}
	tk := w.state(RoleTaker)
	if tk.Phase != "declined" || !strings.Contains(tk.Outcome, "NU7") || len(w.fundings) != 0 {
		t.Fatalf("taker %s: %s", tk.Phase, tk.Outcome)
	}
	w.check("upgrade inside the swap", []string{"refunded"}, []string{"declined"})
}

// On regtest the chain moves to a new branch id after the funding: the stored refunds are signed again under it.
func TestBranchChangeResignsTheRefund(t *testing.T) {
	w := newWorld(t, 17)
	reg := newFakeZcash(t, "regtest", 5000)
	reg.branch = BranchNU62
	// the node's own table, which regtest's branch id is checked against: NU6.3 not yet scheduled
	setRegtestSchedule(reg, map[uint32]uint32{BranchNU5: 2, BranchNU6: 3, BranchNU61: 4, BranchNU62: 5, BranchNU63: 999_999})
	reg.Pay(P2PKHScript(testKey(takerKeyN).PubKeyHash()), takerCoins)
	reg.onSend = w.checkSend
	w.zc = reg
	w.offline = offlineFor("maker_absent")
	moved := false
	for i := 0; i < 400 && !(w.done(RoleMaker) && w.done(RoleTaker)); i++ {
		if st := w.state(RoleTaker); !moved && st.Phase == "funded" && st.Funding != nil {
			// NU6.3 activates at the next block, and the node's next block is signed under it
			setRegtestSchedule(reg, map[uint32]uint32{BranchNU5: 2, BranchNU6: 3, BranchNU61: 4, BranchNU62: 5, BranchNU63: reg.Tip() + 1})
			reg.mu.Lock()
			reg.branch = BranchNU63
			reg.mu.Unlock()
			moved = true
		}
		w.round()
	}
	tk := w.state(RoleTaker)
	if !moved || tk.RefundBranch != BranchNU63 || tk.Phase != "refunded" {
		t.Fatalf("taker %s, refunds under %08x:\n%s", tk.Phase, tk.RefundBranch, w.dump())
	}
	b, _ := os.ReadFile(tk.RefundFile)
	if !strings.Contains(string(b), tk.Refunds[0].Hex) || !strings.Contains(string(b), "37a5165b") {
		t.Fatal("the refund file was not rewritten with the new signatures")
	}
}

// ---- the machines' tables ----

func TestTransitionTableRefusesEveryUnlistedMove(t *testing.T) {
	for _, role := range []string{RoleMaker, RoleTaker} {
		table := Phases(role)
		var names []string
		for n := range table {
			names = append(names, n)
		}
		sort.Strings(names)
		// every successor is a phase; every phase is reachable from ""; every live phase can reach an end
		reach := map[string]bool{"": true}
		for changed := true; changed; {
			changed = false
			for from := range reach {
				for _, n := range table[from].Next {
					if _, ok := table[n]; !ok {
						t.Fatalf("%s: %q lists unknown successor %q", role, from, n)
					}
					if !reach[n] {
						reach[n], changed = true, true
					}
				}
			}
		}
		for _, n := range names {
			if !reach[n] {
				t.Errorf("%s: %q is unreachable", role, n)
			}
			if table[n].Terminal != (len(table[n].Next) == 0) {
				t.Errorf("%s: %q is terminal %v with successors %v", role, n, table[n].Terminal, table[n].Next)
			}
			if !table[n].Terminal && (table[n].Deadline == "" || table[n].IfStopped == "") {
				t.Errorf("%s: %q has no deadline or no if-stopped line", role, n)
			}
		}
		// every pair: the move is written exactly when the table lists it
		dir := t.TempDir()
		for _, from := range names {
			for _, to := range names {
				path := filepath.Join(dir, role+".json")
				st := NewState(path, role)
				st.Phase = from
				st.Done = table[from].Terminal
				if err := st.Save(); err != nil {
					t.Fatal(err)
				}
				s := &Swap{st: st, cfg: Config{Log: func(string, ...interface{}) {}}, said: map[string]bool{}}
				var err error
				if table[to].Terminal {
					st.Done = false
					err = s.finish(to, "test")
				} else {
					err = s.save(to)
				}
				listed := CanMove(role, from, to) && !table[from].Terminal
				if from == to && !table[from].Terminal {
					listed = true
				}
				if (err == nil) != listed {
					t.Errorf("%s: %q -> %q: err %v, listed %v", role, from, to, err, listed)
				}
				back, _ := LoadState(path, role)
				if err != nil && back.Phase != from {
					t.Errorf("%s: a refused move %q -> %q was written", role, from, to)
				}
			}
		}
	}
}

// ---- the crash fuzz ----

func fuzzN(t *testing.T) int {
	if v := os.Getenv("ZECSWAP_FUZZ_N"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	return 3
}

// TestCrashFuzz kills each side right after every durable write of every scenario — a phase save, a finish — and
// restarts it from its state file, with the DERO answers flaky, every DERO call executing up to 10 blocks after the
// height it was built at, chosen at random per call, as DERO allows (fakeDero.execDelay), and the Zcash node losing
// the answer to one send in five after taking it. At every broadcast it checks the invariants of checkSend (one funding, stored before it was
// sent, with the refund file written, against a stable lock; one claim, stored, deep enough, before the cutoff; no
// refund that was not pre-signed), and at the end that the scenario reached its outcome and nobody ended with both
// legs while the other ended with neither.
func TestCrashFuzz(t *testing.T) {
	seed := int64(100)
	scenarios := []string{"happy", "maker_absent", "taker_late", "maker_never_locks"}
	if v := os.Getenv("ZECSWAP_FUZZ_SCENARIOS"); v != "" {
		scenarios = strings.Split(v, ",")
	}
	phases := func(role string) []string {
		var out []string
		for n := range Phases(role) {
			if n != "" {
				out = append(out, n)
			}
		}
		sort.Strings(out)
		return out
	}
	mp, tp := phases(RoleMaker), phases(RoleTaker)
	run := func(scenario string, mcrash, tcrash string, random float64) *world {
		seed++
		w := newWorld(t, seed)
		w.offline = offlineFor(scenario)
		if mcrash != "" {
			w.crashOnce[RoleMaker][mcrash] = true
		}
		if tcrash != "" {
			w.crashOnce[RoleTaker][tcrash] = true
		}
		w.randomCrash = random
		drng := mrand.New(mrand.NewSource(seed * 7))
		w.dero.flaky = func() bool { return drng.Float64() < 0.1 }
		erng := mrand.New(mrand.NewSource(seed * 11))
		w.dero.execDelay = func() uint64 { return uint64(erng.Intn(DeroTxValidity)) }
		zrng := mrand.New(mrand.NewSource(seed * 13))
		w.zc.dropReply = func(m string) bool { return m == "sendrawtransaction" && zrng.Float64() < 0.2 }
		if !w.drive(500) {
			t.Fatalf("%s (crash maker %q taker %q, random %.1f) did not finish: maker %s, taker %s\n%s", scenario, mcrash, tcrash, random, w.state(RoleMaker).Phase, w.state(RoleTaker).Phase, w.dump())
		}
		return w
	}
	runs := 0
	for _, scenario := range scenarios {
		want := scenarioOutcomes[scenario]
		for i, ph := range mp {
			w := run(scenario, ph, tp[(i*7)%len(tp)], 0)
			w.check(fmt.Sprintf("%s/maker dies at %s", scenario, ph), want[0], want[1])
			runs++
		}
		for i, ph := range tp {
			w := run(scenario, mp[(i*5)%len(mp)], ph, 0)
			w.check(fmt.Sprintf("%s/taker dies at %s", scenario, ph), want[0], want[1])
			runs++
		}
		// random deaths may push a party past a deadline; then the safe exit — both refunded — is also an answer,
		// and the one thing never allowed is one side with both legs (check's own assertions)
		abort := [2][]string{append([]string{"refunded", "cancelled"}, want[0]...), append([]string{"refunded", "walked", "declined"}, want[1]...)}
		for i := 0; i < fuzzN(t); i++ {
			w := run(scenario, "", "", 0.2)
			w.check(fmt.Sprintf("%s/random %d", scenario, i), abort[0], abort[1])
			t.Logf("%s random run %d: %d maker and %d taker restarts, %d step errors retried", scenario, i, w.restarts[RoleMaker], w.restarts[RoleTaker], w.stepErrors)
			runs++
		}
	}
	t.Logf("%d runs", runs)
}
