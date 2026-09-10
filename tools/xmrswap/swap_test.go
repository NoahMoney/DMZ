package xmrswap

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	mrand "math/rand"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Phases at which each side saves durable state, in protocol order: the crash fuzz kills a process right after
// each one and restarts it from the file.
var (
	bobPhases   = []string{"setup", "verified_pi_a", "dero_locking", "dero_locked", "watching_xmr", "xmr_confirmed", "gate_sending", "gate_sent", "claimed", "sweep_ready", "sweeping", "swept", "refund_window", "refunding", "refunded", "punish_window", "punished"}
	alicePhases = []string{"setup", "offer_checked", "verified_pi_b", "lock_announced", "saw_dero_lock", "xmr_locking", "xmr_locked", "gate_received", "claiming", "claimed", "await_refund", "refund_seen", "sweep_ready", "sweeping", "swept", "punish_window", "punishing", "punished"}
)

const (
	testHTLC    = "abababababababababababababababababababababababababababababababab"
	testDero    = uint64(5000)
	testXmr     = uint64(1_000_000_000_000)
	testT1Min   = uint64(10)
	testT2Min   = uint64(5)
	testT1Delta = uint64(50) // room for the lock, the stable wait, ten Monero blocks, the gate and a claim stable before t1
	testT2Delta = uint64(10)
	testStable  = uint64(2)
	testConfs   = uint64(10)
	// Alice sends no Claim once height + 1 ≥ t1 − 8 − testClaimMargin; Bob releases the gate only while she
	// still can, plus a block for the channel
	testClaimMargin = uint64(2)
	testGateMargin  = testStableDepth + testClaimMargin + 1
	// the passes a sweep gives a joint wallet with nothing spendable in it before it takes that for the answer
	testSweepWaits = uint64(6)
)

// plan is one fuzz run: the scenario (who goes offline when) and where each side dies.
type plan struct {
	scenario    string          // happy | refund | punish | nomine
	bobCrash    map[string]bool // crash once, the first time this phase is saved
	aliceCrash  map[string]bool
	randomCrash float64 // additionally, crash at any save with this probability
	seed        int64
}

// run is one swap's world: a DERO chain, a Monero node, two wallets, two state files, one channel.
type run struct {
	t                          *testing.T
	ctx                        context.Context
	plan                       plan
	dir                        string
	rng                        *mrand.Rand
	chain                      *fakeChain
	fw                         *FakeWallet
	srv                        *httptest.Server
	w                          Wallet
	bobXmr, aliceXmr           string
	bobW, aliceW               *countingWallet
	bob, alice                 *Swap
	prover                     Prover
	mu                         sync.Mutex
	logs                       []string
	sent                       []Message // every message either side put on the channel
	gateSends                  int
	sweepChecks                int // Bob's sweeps the Invariant 9 hook looked at
	bobRestarts, aliceRestarts int
	stepErrors                 int // non-fatal errors a Step returned (an rpc "timeout", a rename the OS refused): each is retried by the next Step
}

func newRun(t *testing.T, p plan) *run {
	r := &run{t: t, ctx: context.Background(), plan: p, dir: t.TempDir(), rng: mrand.New(mrand.NewSource(p.seed)), prover: &stubProver{}}
	r.chain = newFakeChain(1000)
	r.chain.flaky = func() bool { return r.rng.Float64() < 0.1 }
	r.fw = NewFakeWallet(Stagenet)
	r.srv = httptest.NewServer(r.fw)
	t.Cleanup(r.srv.Close)
	r.w = NewWalletRPC(r.srv.URL, "", "")
	r.bobXmr = r.newWallet("bob")
	r.aliceXmr = r.newWallet("alice")
	r.fw.Fund(r.aliceXmr, testXmr+FakeFee+1)
	r.fw.Mine(12)
	r.bobW = &countingWallet{Wallet: r.w, onSweep: r.checkSweep}
	r.aliceW = &countingWallet{Wallet: r.w, onTransfer: r.checkTransfer}
	return r
}

func (r *run) newWallet(name string) string {
	spend, err := NewScalar(rand.Reader)
	if err != nil {
		r.t.Fatal(err)
	}
	view, err := NewScalar(rand.Reader)
	if err != nil {
		r.t.Fatal(err)
	}
	addr, err := Address(spend.Public(), view.Public(), Stagenet)
	if err != nil {
		r.t.Fatal(err)
	}
	if err := r.w.GenerateFromKeys(r.ctx, name, addr, view.Hex(), spend.Hex(), 0); err != nil {
		r.t.Fatal(err)
	}
	return addr
}

func (r *run) logf(format string, a ...interface{}) {
	r.mu.Lock()
	r.logs = append(r.logs, fmt.Sprintf(format, a...))
	r.mu.Unlock()
}

func (r *run) statePath(role string) string { return filepath.Join(r.dir, role+".json") }

func (r *run) loadState(role string) *State {
	st, err := LoadState(r.statePath(role), role, testHTLC)
	if err != nil {
		r.t.Fatalf("%s: %v", role, err)
	}
	return st
}

func (r *run) config(role string) Config {
	addr, name := bobAddr, "bob"
	if role == "alice" {
		addr, name = aliceAddr, "alice"
	}
	return Config{HTLC: testHTLC, AmountDero: testDero, AmountXmr: testXmr, Net: Stagenet, MyDero: addr, MoneroWalletName: name,
		T1Min: testT1Min, T2Min: testT2Min, T1Delta: testT1Delta, T2Delta: testT2Delta, Confirmations: testConfs, StableBlocks: testStable,
		ClaimMargin: testClaimMargin, GateMargin: testGateMargin, LockSettleBlocks: 2, SweepWaits: testSweepWaits,
		Poll: 3 * time.Millisecond, ResendAfter: time.Second, Log: r.logf}
}

// crashFn is the process death: once at each planned phase, and at random when the plan says so.
func (r *run) crashFn(once map[string]bool) func(string) bool {
	return func(phase string) bool {
		if once[phase] {
			delete(once, phase)
			return true
		}
		return r.plan.randomCrash > 0 && r.rng.Float64() < r.plan.randomCrash
	}
}

// start is a fresh process for one side: state from disk, everything else rebuilt.
func (r *run) start(role string) *Swap {
	st := r.loadState(role)
	ch, err := NewDirChannel(filepath.Join(r.dir, "channel"), role)
	if err != nil {
		r.t.Fatal(err)
	}
	ch.Poll = time.Millisecond
	spy := &spyChannel{Channel: ch, onSend: func(to string, m Message) error { return r.checkSend(role, to, m) }}
	var sw *Swap
	if role == "bob" {
		sw, err = NewBob(r.config(role), st, newMemDero(r.chain, bobAddr), r.bobW, r.prover, spy)
		sw.crash = r.crashFn(r.plan.bobCrash)
		r.bobRestarts++
	} else {
		sw, err = NewAlice(r.config(role), st, newMemDero(r.chain, aliceAddr), r.aliceW, r.prover, spy)
		sw.crash = r.crashFn(r.plan.aliceCrash)
		r.aliceRestarts++
	}
	if err != nil {
		r.t.Fatal(err)
	}
	return sw
}

// step runs one side once; a crash drops the process, to be started again on the next step.
func (r *run) step(role string) {
	var sw **Swap
	if role == "bob" {
		sw = &r.bob
	} else {
		sw = &r.alice
	}
	if *sw == nil {
		*sw = r.start(role)
	}
	_, err := (*sw).Step(r.ctx)
	if errors.Is(err, errCrashed) {
		*sw = nil
		return
	}
	if err != nil && strings.Contains(err.Error(), "TEST INVARIANT") {
		r.t.Fatalf("%s: %v", role, err)
	}
	if err != nil {
		r.stepErrors++
		r.logf("%s: step error: %v", role, err)
	}
}

func (r *run) phase(role string) (string, bool) {
	st := r.loadState(role)
	return st.Phase, st.Done
}

// checkSend is Invariant 2 at the moment it matters: k_b goes out only while this node shows the joint
// address holding at least the amount with at least the confirmations — and Invariant 1: no share on the
// channel, ever; k_b only as "gate"; the view key only in alice_setup.
func (r *run) checkSend(from, to string, m Message) error {
	r.mu.Lock()
	r.sent = append(r.sent, m)
	r.mu.Unlock()
	if m.Kind != KindGate {
		return nil
	}
	r.gateSends++
	st := r.loadState("bob")
	var g gateBody
	if err := json.Unmarshal(m.Body, &g); err != nil || SHA256Hex(mustHex(g.KB)) != st.HK {
		return errors.New("the gate does not carry SHA256's preimage of hk")
	}
	if st.Phase != "gate_sending" && st.Phase != "gate_sent" {
		return fmt.Errorf("gate sent from phase %s", st.Phase)
	}
	if got := r.confirmedAt(st.Joint); got < testXmr {
		return fmt.Errorf("gate sent with %d confirmed piconero at the joint address, want ≥ %d", got, testXmr)
	}
	return nil
}

// checkSweep is Invariant 9 at the moment it matters: Bob spends the joint output only from the phase that
// recorded the intent, only against a lock the chain shows claimed, and only once the block that Claim was
// mined in — and the height he read it at, which bounds it — is at or below the stable height.
func (r *run) checkSweep(to string) error {
	r.sweepChecks++
	st := r.loadState("bob")
	if st.Phase != "sweeping" {
		return fmt.Errorf("sweep from phase %s", st.Phase)
	}
	if to != st.MyXmr {
		return fmt.Errorf("sweep to %s, not bob's own %s", to, st.MyXmr)
	}
	l, err := r.chain.read(st.Lock.ID)
	if err != nil || l.State != LockClaimed {
		return fmt.Errorf("sweep with the lock in state %d (%v)", l.State, err)
	}
	r.chain.mu.Lock()
	claimedAt, stable := r.chain.claimedAt[st.Lock.ID], r.chain.stable()
	r.chain.mu.Unlock()
	if st.Lock.ClaimSeenHeight == 0 || st.Lock.ClaimSeenHeight < claimedAt {
		return fmt.Errorf("bob recorded the claim at height %d, but it was mined at %d", st.Lock.ClaimSeenHeight, claimedAt)
	}
	if stable < st.Lock.ClaimSeenHeight {
		return fmt.Errorf("sweep with the claim read at height %d above the stable height %d", st.Lock.ClaimSeenHeight, stable)
	}
	return nil
}

// confirmedAt reads the fake node's own record: piconero paid to address with ≥ testConfs confirmations.
func (r *run) confirmedAt(address string) uint64 {
	r.fw.mu.Lock()
	defer r.fw.mu.Unlock()
	var sum uint64
	for _, tx := range r.fw.txs {
		if tx.to == address && !tx.pending && r.fw.height-tx.height >= testConfs {
			sum += tx.amount
		}
	}
	return sum
}

// paidTo is every mined payment from one address to another on the fake node.
func (r *run) paidTo(from, to string) (n int, amount uint64) {
	r.fw.mu.Lock()
	defer r.fw.mu.Unlock()
	for _, tx := range r.fw.txs {
		if tx.from == from && tx.to == to && !tx.pending {
			n++
			amount += tx.amount
		}
	}
	return
}

// checkTransfer is Invariant 3 at the moment it matters: the XMR leaves only after the DERO lock, as the
// chain holds it, passed every check — and from the phase that recorded the intent.
func (r *run) checkTransfer(to string, amount uint64) error {
	st := r.loadState("alice")
	if st.Phase != "xmr_locking" {
		return fmt.Errorf("transfer from phase %s", st.Phase)
	}
	if to != st.Joint || amount != testXmr {
		return fmt.Errorf("transfer of %d to %s, want %d to the joint address %s", amount, to, testXmr, st.Joint)
	}
	l, err := r.chain.read(st.Lock.ID)
	if err != nil {
		return fmt.Errorf("no lock on the chain: %v", err)
	}
	own, _ := ParseScalar(st.Own.Secret)
	e := LockExpect{HA: SHA256Hex(own[:]), HK: st.HK, HB: st.Peer.H, TakerRaw: mustRaw(aliceAddr), Amount: testDero, T1Min: 1, T2Min: testT2Min}
	if err := CheckLock(l, r.chain.Height(), e); err != nil {
		return fmt.Errorf("the lock on the chain fails a check: %v", err)
	}
	return nil
}

// drive runs the scenario to its end: both machines in lockstep, one DERO block and one Monero block per
// round, Bob offline as the scenario says.
func (r *run) drive() {
	var bobT1 uint64
	// Bob vanishes once he has locked AND told Alice the lock id (a maker who vanishes before announcing harms
	// only himself: nothing of Alice's is at stake yet, and his DERO waits for his own refund)
	announced := func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, m := range r.sent {
			if m.Kind == KindDeroLocked {
				return true
			}
		}
		return false
	}
	bobOffline := func() bool {
		st := r.loadState("bob")
		if st.Lock.T1 > 0 {
			bobT1 = st.Lock.T1
		}
		switch r.plan.scenario {
		case "refund":
			// Bob vanishes once his DERO is locked and comes back after t1: he never saw the XMR, so he refunds
			return announced() && r.chain.Height() <= bobT1
		case "punish":
			return announced()
		}
		return false
	}
	deadline := time.Now().Add(60 * time.Second)
	for round := 0; ; round++ {
		if time.Now().After(deadline) {
			bp, _ := r.phase("bob")
			ap, _ := r.phase("alice")
			r.t.Fatalf("%s: no end after %d rounds: bob %s, alice %s, height %d\n%s", r.plan.scenario, round, bp, ap, r.chain.Height(), strings.Join(r.logs, "\n"))
		}
		if !bobOffline() {
			r.step("bob")
		}
		r.step("alice")
		r.chain.Advance(1)
		if r.plan.scenario != "nomine" {
			r.fw.Mine(1)
		}
		_, bobDone := r.phase("bob")
		_, aliceDone := r.phase("alice")
		if r.plan.scenario == "punish" {
			// Alice punishes with Bob gone; then he comes back and learns it
			if aliceDone {
				for i := 0; i < 6 && !bobDone; i++ {
					r.step("bob")
					_, bobDone = r.phase("bob")
				}
				if bobDone {
					return
				}
			}
			continue
		}
		if bobDone && aliceDone {
			return
		}
	}
}

// check is every property the fuzz asserts once a run has ended.
func (r *run) check() {
	t := r.t
	bob, alice := r.loadState("bob"), r.loadState("alice")
	lock, err := r.chain.read(bob.Lock.ID)
	if err != nil {
		t.Fatalf("%s: no lock: %v", r.plan.scenario, err)
	}
	// payments: at most one of each, ever, across every restart
	if r.aliceW.transfers > 1 || r.bobW.transfers != 0 || r.bobW.sweeps > 1 || r.aliceW.sweeps > 1 {
		t.Fatalf("%s: payments: alice transfers %d sweeps %d, bob transfers %d sweeps %d", r.plan.scenario, r.aliceW.transfers, r.aliceW.sweeps, r.bobW.transfers, r.bobW.sweeps)
	}
	if lockN := r.chain.locks; len(lockN) != 1 {
		t.Fatalf("%s: %d DERO locks, want exactly one", r.plan.scenario, len(lockN))
	}
	// secrets: never in a log line; shares never on the channel; k_b only as gate; v only in alice_setup
	secrets := map[string]string{"s_b": bob.Own.Secret, "s_a": alice.Own.Secret, "k_b": bob.Gate, "v": alice.View}
	for name, sec := range secrets {
		if sec == "" {
			continue
		}
		for _, line := range r.logs {
			if strings.Contains(line, sec) {
				t.Fatalf("%s: %s in a log line: %s", r.plan.scenario, name, line)
			}
		}
		for _, m := range r.sent {
			body := string(m.Body)
			switch {
			case name == "k_b" && m.Kind == KindGate, name == "v" && m.Kind == KindAliceSetup:
				continue
			}
			if strings.Contains(body, sec) {
				t.Fatalf("%s: %s inside a %s message", r.plan.scenario, name, m.Kind)
			}
		}
	}
	// the lock never stays open: every scenario ends in one of the three exits
	switch lock.State {
	case LockClaimed:
		if r.aliceW.transfers != 1 || r.gateSends == 0 {
			t.Fatalf("%s: claimed with %d transfers and %d gate sends", r.plan.scenario, r.aliceW.transfers, r.gateSends)
		}
		if alice.Phase != "claimed" || (bob.Phase != "swept" && bob.Phase != "sweep_unknown") {
			t.Fatalf("%s: claimed but alice %s, bob %s", r.plan.scenario, alice.Phase, bob.Phase)
		}
		if bob.Phase == "swept" {
			if n, amt := r.paidTo(bob.Joint, r.bobXmr); n != 1 || amt != testXmr-FakeFee {
				t.Fatalf("%s: bob's sweep: %d payments of %d from the joint address to %s", r.plan.scenario, n, amt, r.bobXmr)
			}
		}
		if bob.Revealed != alice.Own.Secret {
			t.Fatalf("%s: bob read %q as s_a", r.plan.scenario, bob.Revealed)
		}
	case LockRefunded:
		if bob.Phase != "refunded" || r.gateSends != 0 {
			t.Fatalf("%s: refunded but bob %s, gate sends %d", r.plan.scenario, bob.Phase, r.gateSends)
		}
		switch {
		case r.aliceW.transfers == 1 && alice.Phase != "swept" && alice.Phase != "sweep_unknown":
			t.Fatalf("%s: alice locked XMR and ended in %s", r.plan.scenario, alice.Phase)
		case r.aliceW.transfers == 0 && alice.Phase != "aborted" && alice.Phase != "swept" && alice.Phase != "sweep_unknown" && alice.Phase != "refused":
			t.Fatalf("%s: alice locked nothing and ended in %s", r.plan.scenario, alice.Phase)
		}
		if alice.Phase == "swept" {
			if n, amt := r.paidTo(alice.Joint, r.aliceXmr); n != 1 || amt != testXmr-FakeFee {
				t.Fatalf("%s: alice's recovery: %d payments of %d", r.plan.scenario, n, amt)
			}
		}
		if alice.Revealed != "" && alice.Revealed != bob.Own.Secret {
			t.Fatalf("%s: alice read %q as s_b", r.plan.scenario, alice.Revealed)
		}
	case LockPunished:
		if alice.Phase != "punished" || bob.Phase != "punished" || r.gateSends != 0 {
			t.Fatalf("%s: punished but alice %s, bob %s, gate sends %d", r.plan.scenario, alice.Phase, bob.Phase, r.gateSends)
		}
	default:
		t.Fatalf("%s: the lock is still open (bob %s, alice %s)", r.plan.scenario, bob.Phase, alice.Phase)
	}
	// SEC-K-09: whoever swept held s_a + s_b in a wallet the rpc opened, and must have closed it again
	for _, side := range []struct {
		st *State
		w  *countingWallet
	}{{bob, r.bobW}, {alice, r.aliceW}} {
		if side.st.Phase == "swept" && side.w.closes == 0 {
			t.Fatalf("%s: %s swept and left the joint spend wallet open in the wallet-rpc", r.plan.scenario, side.st.Role)
		}
	}
	for _, role := range []string{"bob", "alice"} {
		if _, err := os.Stat(r.statePath(role) + ".tmp"); err == nil {
			t.Fatalf("%s: temp state file left behind", role)
		}
	}
}

func (r *run) expectExit(want uint64) {
	lock, _ := r.chain.read(0)
	if lock.State != want {
		bob, alice := r.loadState("bob"), r.loadState("alice")
		r.t.Fatalf("%s (crash bob %v alice %v): lock ended in state %d, want %d; bob %s, alice %s\n%s", r.plan.scenario, r.plan.bobCrash, r.plan.aliceCrash, lock.State, want, bob.Phase, alice.Phase, strings.Join(r.logs, "\n"))
	}
}

func fuzzN(t *testing.T) int {
	if v := os.Getenv("XMRSWAP_FUZZ_N"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	return 6
}

// TestCrashFuzz kills each side right after every durable step of every exit, restarts it from its state
// file, and checks the properties §4 and §5.D demand: at most one Monero payment and one sweep per side per
// swap, the gate only after this node's own confirmations, the XMR lock only after the chain's lock passed
// every check, no secret in any log line or in any message but its own, and the lock never left open.
func TestCrashFuzz(t *testing.T) {
	seed := int64(1)
	scenarios := []string{"happy", "refund", "punish"}
	if v := os.Getenv("XMRSWAP_FUZZ_SCENARIOS"); v != "" {
		scenarios = strings.Split(v, ",")
	}
	for _, scenario := range scenarios {
		for _, phase := range bobPhases {
			seed++
			r := newRun(t, plan{scenario: scenario, bobCrash: map[string]bool{phase: true}, aliceCrash: map[string]bool{alicePhases[int(seed)%len(alicePhases)]: true}, seed: seed})
			r.drive()
			r.check()
		}
		for _, phase := range alicePhases {
			seed++
			r := newRun(t, plan{scenario: scenario, aliceCrash: map[string]bool{phase: true}, bobCrash: map[string]bool{bobPhases[int(seed)%len(bobPhases)]: true}, seed: seed})
			r.drive()
			r.check()
			// the exits are decided by who is present, not by where a process died — except that a death
			// at the XMR lock intent means the XMR was never sent (and never sent twice), so the swap
			// degrades to a refund of the untouched DERO
			switch {
			case scenario == "happy" && phase != "xmr_locking":
				r.expectExit(LockClaimed)
			case scenario == "happy":
				r.expectExit(LockRefunded)
			case scenario == "refund":
				r.expectExit(LockRefunded)
			case scenario == "punish":
				r.expectExit(LockPunished)
			}
		}
		for i := 0; i < fuzzN(t); i++ {
			seed++
			r := newRun(t, plan{scenario: scenario, randomCrash: 0.2, seed: seed})
			r.drive()
			r.check()
			t.Logf("%s random run %d: %d restarts, %d transient step errors retried", scenario, i, r.bobRestarts+r.aliceRestarts, r.stepErrors)
		}
		t.Logf("%s: %d runs done", scenario, len(bobPhases)+len(alicePhases)+fuzzN(t))
	}
}

// TestHappyPathWithoutCrashes is the plain swap: one lock, one transfer, one gate, one claim, one sweep, and
// each side reads the other's share off the chain.
func TestHappyPathWithoutCrashes(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 7})
	r.drive()
	r.check()
	r.expectExit(LockClaimed)
	bob := r.loadState("bob")
	if bob.Phase != "swept" || len(bob.Sweep.Txs) != 1 || r.bobW.sweeps != 1 || r.sweepChecks != 1 || r.gateSends < 1 || r.bobRestarts != 1 || r.aliceRestarts != 1 {
		t.Fatalf("bob %s sweeps %v (%d calls, %d checked), gate sends %d, restarts %d/%d", bob.Phase, bob.Sweep.Txs, r.bobW.sweeps, r.sweepChecks, r.gateSends, r.bobRestarts, r.aliceRestarts)
	}
	// the gate went out from the phase that decided it and was not repeated after the claim (§5.D)
	for i, m := range r.sent {
		if m.Kind == KindGate && i > 0 && r.sent[i-1].Kind == KindGate {
			t.Fatalf("the gate was sent twice in a row")
		}
	}
	own, _ := ParseScalar(bob.Own.Secret)
	rev, _ := ParseScalar(bob.Revealed)
	joint, _ := Address(AddScalars(own, rev).Public(), mustPoint(bob.ViewPub), Stagenet)
	if joint != bob.Joint {
		t.Fatalf("s_a + s_b does not open the joint address: %s vs %s", joint, bob.Joint)
	}
}

// TestGateWaitsForConfirmations: with the Monero chain not moving, Alice's lock never reaches 10 confirmations
// and Bob never releases k_b; t1 passes and he refunds, and Alice recovers her (now confirmed) XMR with s_b.
func TestGateWaitsForConfirmations(t *testing.T) {
	r := newRun(t, plan{scenario: "nomine", seed: 11})
	r.drive()
	if r.gateSends != 0 {
		t.Fatalf("gate sent %d times without confirmations", r.gateSends)
	}
	r.expectExit(LockRefunded)
	bob, alice := r.loadState("bob"), r.loadState("alice")
	// her transfer sits unconfirmed on the fake node (nothing mined it), so her recovery sweep has nothing to
	// spend yet: the intent is recorded once and the key is kept
	if bob.Phase != "refunded" || (alice.Phase != "swept" && alice.Phase != "sweep_unknown") || r.aliceW.transfers != 1 {
		t.Fatalf("bob %s, alice %s, transfers %d", bob.Phase, alice.Phase, r.aliceW.transfers)
	}
}

// TestGateRefusesLockedTimeOutput (Invariant 8, review R6): Alice's lock carries an unlock_time. It confirms
// like any other and Bob's view-only wallet lists it, but he never releases k_b; t1 passes, he refunds with
// s_b, and her frozen XMR is hers to recover once the unlock_time is past — the fake's sweep refuses it until
// then, which is the one attempt the companion makes.
func TestGateRefusesLockedTimeOutput(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 29})
	r.fw.SetUnlockTime(1 << 20)
	r.drive()
	r.check()
	r.expectExit(LockRefunded)
	bob, alice := r.loadState("bob"), r.loadState("alice")
	if r.gateSends != 0 || bob.Phase != "refunded" || alice.Phase != "sweep_unknown" || r.aliceW.transfers != 1 {
		t.Fatalf("gate sends %d, bob %s, alice %s, transfers %d", r.gateSends, bob.Phase, alice.Phase, r.aliceW.transfers)
	}
	if !hasLog(r, "carries unlock_time") {
		t.Fatalf("the operator was not told why the gate stayed shut:\n%s", strings.Join(r.logs, "\n"))
	}
}

// TestGateRefusesBurntOutput (Invariant 8, review R5d): Alice's lock is a duplicate one-time key. The transfer
// list shows the agreed amount, confirmed and unlocked; the wallet's own balance does not, and the gate follows
// the balance.
func TestGateRefusesBurntOutput(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 31})
	r.fw.BurnNextTransfer()
	r.drive()
	r.check()
	r.expectExit(LockRefunded)
	bob := r.loadState("bob")
	if r.gateSends != 0 || bob.Phase != "refunded" || r.aliceW.transfers != 1 {
		t.Fatalf("gate sends %d, bob %s, transfers %d", r.gateSends, bob.Phase, r.aliceW.transfers)
	}
}

func hasLog(r *run, s string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.logs {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

// TestClaimCutoff: the height from which Alice sends no Claim is t1 − 8 − margin, and none at all for a t1
// that leaves no room.
func TestClaimCutoff(t *testing.T) {
	for _, c := range []struct{ t1, margin, want uint64 }{{100, 8, 84}, {100, 0, 92}, {17, 8, 1}, {16, 8, 0}, {5, 0, 0}} {
		if got := claimCutoff(c.t1, c.margin); got != c.want {
			t.Errorf("claimCutoff(%d, %d) = %d, want %d", c.t1, c.margin, got, c.want)
		}
	}
}

// TestAliceStopsClaimingBeforeCutoff (Invariants 9 and 11): a gate that reaches Alice once height + 1 ≥
// t1 − 8 − margin is not acted on — no Claim is sent, no fee spent — and the swap ends in Bob's refund with
// her XMR recovered through s_b. Bob is held offline after his lock until the cutoff is upon her, and his own
// gate margin is cut to nothing, as a careless maker's would be.
func TestAliceStopsClaimingBeforeCutoff(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 37})
	r.bob = r.start("bob")
	r.bob.cfg.GateMargin = 1
	announced := func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, m := range r.sent {
			if m.Kind == KindDeroLocked {
				return true
			}
		}
		return false
	}
	var gateAt uint64
	for round := 0; ; round++ {
		if round > 300 {
			bp, _ := r.phase("bob")
			ap, _ := r.phase("alice")
			t.Fatalf("no end: bob %s, alice %s, height %d\n%s", bp, ap, r.chain.Height(), strings.Join(r.logs, "\n"))
		}
		t1 := r.loadState("bob").Lock.T1
		offline := announced() && r.chain.Height()+1 < claimCutoff(t1, testClaimMargin)
		if !offline {
			r.step("bob")
		}
		if gateAt == 0 && r.gateSends > 0 {
			gateAt = r.chain.Height()
		}
		r.step("alice")
		r.chain.Advance(1)
		r.fw.Mine(1)
		_, bobDone := r.phase("bob")
		_, aliceDone := r.phase("alice")
		if bobDone && aliceDone {
			break
		}
	}
	bob, alice := r.loadState("bob"), r.loadState("alice")
	if gateAt == 0 || gateAt+1 < claimCutoff(bob.Lock.T1, testClaimMargin) {
		t.Fatalf("the gate went out at height %d, before the cutoff %d: the scenario did not happen", gateAt, claimCutoff(bob.Lock.T1, testClaimMargin))
	}
	if n := r.alice.dero.(*memDero).calls["Claim"]; n != 0 || alice.ClaimTx != "" {
		t.Fatalf("alice sent %d Claim calls past the cutoff (tx %q)", n, alice.ClaimTx)
	}
	r.expectExit(LockRefunded)
	if bob.Phase != "refunded" || alice.Phase != "swept" || r.aliceW.transfers != 1 || r.aliceW.sweeps != 1 || alice.Revealed != bob.Own.Secret {
		t.Fatalf("bob %s, alice %s, transfers %d, sweeps %d", bob.Phase, alice.Phase, r.aliceW.transfers, r.aliceW.sweeps)
	}
	if !hasLog(r, "past the claim cutoff") {
		t.Fatalf("alice did not say why she stopped:\n%s", strings.Join(r.logs, "\n"))
	}
}

// TestBobWaitsForStableClaim (Invariant 9): Bob reads the Claim and records the height; while the daemon's
// stable height is below it he does not form the spend key or sweep, and once it is not he does, exactly once.
func TestBobWaitsForStableClaim(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 41})
	for i := 0; ; i++ {
		if i > 300 {
			t.Fatalf("bob never read the claim\n%s", strings.Join(r.logs, "\n"))
		}
		r.step("bob")
		r.step("alice")
		if p, _ := r.phase("bob"); p == "claimed" {
			break
		}
		r.chain.Advance(1)
		r.fw.Mine(1)
	}
	bob := r.loadState("bob")
	seen := bob.Lock.ClaimSeenHeight
	if seen == 0 || seen < r.chain.claimedAt[0] || r.chain.StableHeight() >= seen {
		t.Fatalf("claim seen at %d, mined at %d, stable %d", seen, r.chain.claimedAt[0], r.chain.StableHeight())
	}
	if bob.LastSent != nil {
		t.Fatalf("bob still repeats %s after the claim", bob.LastSent.Kind)
	}
	// the chain stands still: nothing becomes stable, and Bob does not move
	for i := 0; i < 5; i++ {
		r.step("bob")
		if p, _ := r.phase("bob"); p != "claimed" {
			t.Fatalf("bob moved to %s with stable height %d below the claim at %d", p, r.chain.StableHeight(), seen)
		}
	}
	if r.bobW.sweeps != 0 {
		t.Fatalf("%d sweeps before the claim was stable", r.bobW.sweeps)
	}
	// the claim becomes stable, and the sweep follows, once
	for r.chain.StableHeight() < seen {
		r.chain.Advance(1)
	}
	for i := 0; i < 6; i++ {
		r.step("bob")
	}
	bob = r.loadState("bob")
	if bob.Phase != "swept" || r.bobW.sweeps != 1 || r.sweepChecks != 1 {
		t.Fatalf("bob %s, sweeps %d (%d checked)", bob.Phase, r.bobW.sweeps, r.sweepChecks)
	}
}

// TestRefusesReusedSecret (Invariant 13, reviews R20 and R23): every secret a companion generates is entered in
// the register beside its state file under its swap id, and a new swap whose fresh share, gate secret or view
// key is already there is refused before anything is said. The collision is injected through the prover and
// the random source, since nothing else could produce one.
func TestRefusesReusedSecret(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 43})
	fixed, _ := NewScalar(rand.Reader)
	r.prover = &stubProver{fixed: &fixed}
	r.step("bob")
	bob := r.loadState("bob")
	if bob.Phase != "setup" || bob.SwapID == "" || bob.Own.Secret != fixed.Hex() {
		t.Fatalf("first swap: phase %s, id %q", bob.Phase, bob.SwapID)
	}
	used, err := LoadUsed(UsedPath(r.statePath("bob")))
	if err != nil {
		t.Fatal(err)
	}
	share, gate := used.Secrets[SHA256Hex(fixed[:])], used.Secrets[SHA256Hex(mustHex(bob.Gate))]
	if share.Swap != bob.SwapID || share.Kind != "share" || gate.Swap != bob.SwapID || gate.Kind != "gate" || len(used.Secrets) != 2 {
		t.Fatalf("register after bob's setup: %+v", used.Secrets)
	}
	// the register holds hashes only: no secret, and no key share, is in the file
	raw, _ := os.ReadFile(UsedPath(r.statePath("bob")))
	if strings.Contains(string(raw), bob.Own.Secret) || strings.Contains(string(raw), bob.Gate) {
		t.Fatal("a secret is in the register")
	}
	second := func(role, file string, prover Prover, random string) *State {
		st, err := LoadState(filepath.Join(r.dir, file), role, testHTLC)
		if err != nil {
			t.Fatal(err)
		}
		ch, err := NewDirChannel(filepath.Join(r.dir, "channel-"+file), role)
		if err != nil {
			t.Fatal(err)
		}
		spy := &spyChannel{Channel: ch, onSend: func(to string, m Message) error { return r.checkSend(role, to, m) }}
		var sw *Swap
		if role == "bob" {
			sw, err = NewBob(r.config(role), st, newMemDero(r.chain, bobAddr), r.bobW, prover, spy)
		} else {
			sw, err = NewAlice(r.config(role), st, newMemDero(r.chain, aliceAddr), r.aliceW, prover, spy)
		}
		if err != nil {
			t.Fatal(err)
		}
		if random != "" {
			sw.rand = strings.NewReader(random)
		}
		if _, err := sw.Step(r.ctx); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		return st
	}
	// the same prover again, from the same directory: the share repeats, and the swap is refused
	st := second("bob", "bob-second.json", r.prover, "")
	if st.Phase != "refused" || !st.Done || !strings.Contains(st.Outcome, "share") || !strings.Contains(st.Outcome, bob.SwapID) || st.Own.Secret != "" {
		t.Fatalf("second swap on a repeated share: %s %q", st.Phase, st.Outcome)
	}
	// a fresh prover but a random source that repeats: the first such swap runs, the next repeats its k_b
	ones := strings.Repeat("k", 64)
	st = second("bob", "bob-third.json", &stubProver{}, "AAAAAAAA"+ones)
	if st.Phase != "setup" || st.SwapID != "4141414141414141" {
		t.Fatalf("third swap: %s %q", st.Phase, st.SwapID)
	}
	st = second("bob", "bob-fourth.json", &stubProver{}, "BBBBBBBB"+ones)
	if st.Phase != "refused" || !strings.Contains(st.Outcome, "gate") || !strings.Contains(st.Outcome, "4141414141414141") {
		t.Fatalf("fourth swap on a repeated gate secret: %s %q", st.Phase, st.Outcome)
	}
	// and Alice's view key, the same way
	views := strings.Repeat("v", 64)
	st = second("alice", "alice-second.json", &stubProver{}, "CCCCCCCC"+views)
	if st.Phase != "setup" {
		t.Fatalf("alice's second swap: %s %q", st.Phase, st.Outcome)
	}
	st = second("alice", "alice-third.json", &stubProver{}, "DDDDDDDD"+views)
	if st.Phase != "refused" || !strings.Contains(st.Outcome, "view") || !strings.Contains(st.Outcome, "4343434343434343") {
		t.Fatalf("alice's third swap on a repeated view key: %s %q", st.Phase, st.Outcome)
	}
	// nothing refused was said on any channel, and no secret reached a log line
	for _, m := range r.sent {
		if m.Kind != KindOffer {
			t.Fatalf("a refused swap sent %s", m.Kind)
		}
	}
	for _, line := range r.logs {
		if strings.Contains(line, fixed.Hex()) || strings.Contains(line, bob.Gate) {
			t.Fatalf("a secret in a log line: %s", line)
		}
	}
	// a register the companion cannot read stops it before any secret is made
	if err := os.WriteFile(UsedPath(r.statePath("bob")), []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	st5, _ := LoadState(filepath.Join(r.dir, "bob-fifth.json"), "bob", testHTLC)
	if _, err := NewBob(r.config("bob"), st5, newMemDero(r.chain, bobAddr), r.bobW, &stubProver{}, nil); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("corrupt register accepted: %v", err)
	}
}

// TestBobNeverLocksWithoutAliceProof (Invariant 4) and TestAliceNeverLocksXmrWithoutBobProof (Invariant 3):
// a proof that does not verify stops the side that would otherwise put coins under the other's hash.
func TestBobNeverLocksWithoutAliceProof(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 13})
	r.prover = &stubProver{bad: true}
	for i := 0; i < 20; i++ {
		r.step("bob")
		r.step("alice")
		r.chain.Advance(1)
	}
	bob := r.loadState("bob")
	if bob.Phase != "refused" || !bob.Done || len(r.chain.locks) != 0 {
		t.Fatalf("bob %s done %v, locks %d", bob.Phase, bob.Done, len(r.chain.locks))
	}
}

func TestAliceNeverLocksXmrWithoutBobProof(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 17})
	good, bad := &stubProver{}, &stubProver{bad: true}
	// Alice's own proof is fine; Bob's is not: the two sides see different provers
	r.prover = good
	r.bob = r.start("bob")
	r.bob.prover = bad
	for i := 0; i < 40; i++ {
		r.step("bob")
		r.step("alice")
		r.chain.Advance(1)
		r.fw.Mine(1)
	}
	alice := r.loadState("alice")
	if alice.Phase != "refused" || !alice.Done || r.aliceW.transfers != 0 || !strings.Contains(alice.Outcome, "proof") {
		t.Fatalf("alice %s done %v transfers %d: %s", alice.Phase, alice.Done, r.aliceW.transfers, alice.Outcome)
	}
}

// TestAliceRefusesTermsBelowPolicy: an offer whose timelock widths are below her minimums is refused before
// anything is sent; a lock read from the chain is refused on each field §4.3 names.
func TestAliceRefusesTermsBelowPolicy(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 19})
	r.alice = r.start("alice")
	r.alice.cfg.T1Min = testT1Delta + 1 // stricter than what Bob offers
	for i := 0; i < 10; i++ {
		r.step("bob")
		r.step("alice")
		r.chain.Advance(1)
	}
	alice := r.loadState("alice")
	if alice.Phase != "refused" || len(r.sent) != 1 || r.sent[0].Kind != KindOffer {
		t.Fatalf("alice %s; messages %d", alice.Phase, len(r.sent))
	}
}

func TestCheckLockEveryField(t *testing.T) {
	good := Lock{HA: "a", HK: "k", HB: "b", Taker: "t", Amount: 100, T1: 1000, T2: 1500, State: LockOpen}
	e := LockExpect{HA: "a", HK: "k", HB: "b", TakerRaw: "t", Amount: 100, T1Min: 400, T2Min: 400}
	if err := CheckLock(good, 600, e); err != nil {
		t.Fatal(err)
	}
	cases := map[string]Lock{
		"claimed":      {HA: "a", HK: "k", HB: "b", Taker: "t", Amount: 100, T1: 1000, T2: 1500, State: LockClaimed},
		"ha":           {HA: "x", HK: "k", HB: "b", Taker: "t", Amount: 100, T1: 1000, T2: 1500},
		"hk":           {HA: "a", HK: "x", HB: "b", Taker: "t", Amount: 100, T1: 1000, T2: 1500},
		"hb":           {HA: "a", HK: "k", HB: "x", Taker: "t", Amount: 100, T1: 1000, T2: 1500},
		"taker":        {HA: "a", HK: "k", HB: "b", Taker: "x", Amount: 100, T1: 1000, T2: 1500},
		"t1 too close": {HA: "a", HK: "k", HB: "b", Taker: "t", Amount: 100, T1: 999, T2: 1500},
		"t1 passed":    {HA: "a", HK: "k", HB: "b", Taker: "t", Amount: 100, T1: 500, T2: 1500},
		"t2 too close": {HA: "a", HK: "k", HB: "b", Taker: "t", Amount: 100, T1: 1000, T2: 1399},
		"t2 before t1": {HA: "a", HK: "k", HB: "b", Taker: "t", Amount: 100, T1: 1000, T2: 900},
		"amount":       {HA: "a", HK: "k", HB: "b", Taker: "t", Amount: 99, T1: 1000, T2: 1500},
		"asset":        {HA: "a", HK: "k", HB: "b", Taker: "t", Amount: 100, T1: 1000, T2: 1500, Asset: "ff"},
	}
	for name, l := range cases {
		if err := CheckLock(l, 600, e); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func mustPoint(h string) Point {
	p, err := ParsePoint(h)
	if err != nil {
		panic(err)
	}
	return p
}
