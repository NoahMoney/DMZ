package xmrswap

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"veil/tools/swapstatus"
)

// statusSecrets is every secret value the states hold — each party's key share, the gate secret, the joint view key,
// the joint wallets' password, the share read back from the chain — in both cases of hex.
func statusSecrets(sts ...*State) []string {
	var out []string
	for _, st := range sts {
		if st == nil {
			continue
		}
		for _, v := range []string{st.Own.Secret, st.Gate, st.View, st.JointPassword, st.Revealed} {
			if len(v) >= 16 {
				out = append(out, strings.ToLower(v), strings.ToUpper(v))
			}
		}
	}
	return out
}

// statusCollector builds the answer at every save either side makes, holds it to the contract and to the rule that no
// secret leaves, and writes one answer per role and phase for the app's own parser.
type statusCollector struct {
	t    *testing.T
	dir  string
	seen map[string]bool
}

func newStatusCollector(t *testing.T) *statusCollector {
	dir := t.TempDir()
	if d := os.Getenv("SWAPSTATUS_OUT"); d != "" {
		dir = filepath.Join(d, "xmrswap")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return &statusCollector{t: t, dir: dir, seen: map[string]bool{}}
}

func (c *statusCollector) check(name, role string, p swapstatus.Payload, secrets []string) {
	c.t.Helper()
	if err := p.Validate(); err != nil {
		c.t.Fatalf("%s, %s in phase %q: %v", name, role, p.Phase, err)
	}
	b, err := json.Marshal(p)
	if err != nil {
		c.t.Fatal(err)
	}
	for _, s := range secrets {
		if strings.Contains(string(b), s) {
			c.t.Fatalf("%s, %s in phase %q: the answer carries a secret of the swap's", name, role, p.Phase)
		}
	}
	if k := swapstatus.SecretField(b); k != "" {
		c.t.Fatalf("%s, %s in phase %q: a field named %q", name, role, p.Phase, k)
	}
	if (p.Outcome == "") == (strings.HasPrefix(p.Next, "Nothing")) && p.Outcome == "" {
		c.t.Fatalf("%s, %s in phase %q: a running swap says nothing is next", name, role, p.Phase)
	}
	key := role + "|" + p.Phase
	if !c.seen[key] {
		ph := p.Phase
		if ph == "" {
			ph = "new"
		}
		f := filepath.Join(c.dir, name+"-"+role+"-"+strings.ReplaceAll(ph, "_", "-")+".json")
		if err := os.WriteFile(f, b, 0o600); err != nil {
			c.t.Fatal(err)
		}
	}
	c.seen[key] = true
}

// appCheck runs the answers written through app/track.js with node (tests/js/statuscheck.mjs).
func appCheck(t *testing.T, dir string) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Logf("node is not on PATH: the answers in %s were not run through app/track.js", dir)
		return
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "tests", "js", "statuscheck.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, script, dir).CombinedOutput()
	if err != nil {
		t.Fatalf("app/track.js does not read the answers whole (%v):\n%s", err, out)
	}
	t.Logf("%s", strings.TrimSpace(string(out)))
}

// TestStatusV1CarriesNoSecretInAnyPhase runs the fake worlds of the crash fuzz — each exit of htlc.bas v3, with and
// without process deaths, and a refusal — and builds the answer -status would serve at every save of either side. Each
// is valid by the contract, names no field the app reads as a secret's, and holds none of the secrets either party's
// state holds at that moment: no key share, gate secret, view key, joint password or share read from the chain. Every
// phase the crash fuzz plans a death in is reached, and every answer is then read through the app's own parser.
func TestStatusV1CarriesNoSecretInAnyPhase(t *testing.T) {
	c := newStatusCollector(t)
	hook := func(name string, r *run) func(role, phase string) {
		return func(role, phase string) {
			other := map[string]string{"bob": "alice", "alice": "bob"}[role]
			st := r.loadState(role)
			peer, _ := LoadState(r.statePath(other), other, testHTLC)
			var confs *uint64
			if role == "bob" && r.bob != nil {
				confs = r.bob.xmrConfs
			}
			c.check(name, role, statusV1(st, r.config(role), r.chain.Height(), confs, time.Now()), statusSecrets(st, peer))
		}
	}
	seed := int64(9000)
	for _, scenario := range []string{"happy", "refund", "punish", "reclaim"} {
		for i := 0; i < 3; i++ {
			seed++
			p := plan{scenario: scenario, seed: seed}
			if i > 0 {
				p.randomCrash = 0.2
			}
			r := newRun(t, p)
			r.onSave = hook(scenario, r)
			r.drive()
		}
	}
	// a refusal: the taker turns down a maker whose t1 is beyond her ceiling, with nothing of hers locked
	r := newRun(t, plan{scenario: "happy", seed: 9100})
	r.onSave = hook("refused", r)
	r.bob = startWith(r, "bob", func(c *Config) { c.T1Delta = 900000 })
	r.bob.crash = r.crashFn("bob", map[string]bool{})
	r.alice = startWith(r, "alice", func(c *Config) { c.T1Max, c.T2Max = 200, 200 })
	r.alice.crash = r.crashFn("alice", map[string]bool{})
	driveUntil(r, "refused", 400)

	notPhases := map[string]bool{"lock_attempt": true, "lock_sent": true, "sweep_sent": true}
	for _, side := range []struct {
		role   string
		phases []string
	}{{"bob", bobPhases}, {"alice", append(alicePhases, "refused")}} {
		for _, ph := range side.phases {
			if !notPhases[ph] && !c.seen[side.role+"|"+ph] {
				t.Errorf("no answer was built in %s's phase %q", side.role, ph)
			}
		}
	}
	appCheck(t, c.dir)
}

// TestStatusV1EveryPhaseOfBothMachines builds the answer for every phase either machine has, from a state that holds
// every secret the file can hold, and the end phases with their outcomes: each is valid, carries none of them, and says
// what happens next.
func TestStatusV1EveryPhaseOfBothMachines(t *testing.T) {
	c := newStatusCollector(t)
	rnd := func() string {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(b)
	}
	ends := map[string]map[string]bool{
		"bob":   {"refused": true, "swept": true, "sweep_unknown": true, "punished": true, "refunded": true, "reclaimed": true},
		"alice": {"refused": true, "claimed": true, "aborted": true, "punished": true, "swept": true, "sweep_unknown": true},
	}
	phases := map[string][]string{
		"bob":   {"refund_window", "refunding", "refund_confirming", "refunded", "punish_window", "reclaiming", "reclaim_confirming", "reclaimed", "punished", "refused"},
		"alice": {"await_refund", "refund_seen", "sweep_ready", "sweeping", "swept", "sweep_unknown", "aborted", "punish_window", "punishing", "punish_confirming", "punished", "await_reclaim", "refused"},
	}
	for p := range bobStage {
		phases["bob"] = append(phases["bob"], p)
	}
	for p := range aliceStage {
		phases["alice"] = append(phases["alice"], p)
	}
	for _, role := range []string{"bob", "alice"} {
		for _, asset := range []string{"", strings.Repeat("7a", 32)} {
			for _, ph := range phases[role] {
				st := &State{Role: role, HTLC: testHTLC, Phase: ph, Done: ends[role][ph], Outcome: "a sentence that quotes " + rnd(),
					Terms: Terms{AmountDero: testDero, AmountXmr: testXmr, Asset: asset, Decimals: 2, DeroNet: "testnet", Net: "stagenet", Stipend: testStipend},
					Own:   Party{Secret: rnd(), H: rnd(), S: rnd()}, Peer: Party{H: rnd(), S: rnd()}, Gate: rnd(), HK: rnd(), View: rnd(), ViewPub: rnd(),
					JointPassword: rnd(), Revealed: rnd(), Contract: ContractRecord{W1Min: 400, W2Min: 400, G2: 4800},
					Lock:    LockRecord{ID: 0, Tx: rnd(), T1: 1420, T2: 1820, Amount: testDero, LockedHeight: 1002, SeenHeight: 1003, ClaimSeenHeight: 1200, RefundSeenHeight: 1500, PunishSeenHeight: 1830, ReclaimSeenHeight: 6700},
					Stipend: StipendRecord{Tx: rnd(), Amount: testStipend, Height: 1010}, Xmr: XmrRecord{LockTx: rnd(), ConfirmedAt: 300},
					ClaimTx: rnd(), RefundTx: rnd(), PunishTx: rnd(), ReclaimTx: "(reclaimed; txid not recorded)", Sweep: SweepRecord{Txs: []string{rnd()}, Fee: SwapFee(testXmr)},
					Consumed: map[string]bool{KindDeroLocked: true}}
				cfg := Config{HTLC: testHTLC, AmountDero: testDero, AmountXmr: testXmr, Asset: asset, AssetDecimals: 2, DeroNet: "testnet", Net: Stagenet, XmrTreasury: "set"}
				p := statusV1(st, cfg, 1300, swapstatus.U(4), time.Now())
				c.check("phase", role, p, statusSecrets(st))
				if p.Next == "" {
					t.Errorf("%s in %q: no next sentence", role, ph)
				}
				if st.Done != (p.Outcome != "") {
					t.Errorf("%s in %q (done %v): outcome %q", role, ph, st.Done, p.Outcome)
				}
				if strings.Contains(p.Next+strings.Join(stepLabels(p), ""), "a sentence that quotes") {
					t.Errorf("%s in %q: the state's outcome sentence reached the answer", role, ph)
				}
				if asset != "" && p.Asset != asset {
					t.Errorf("%s in %q: asset %q", role, ph, p.Asset)
				}
			}
		}
	}
	appCheck(t, c.dir)
}

func stepLabels(p swapstatus.Payload) []string {
	var out []string
	for _, s := range p.Steps {
		out = append(out, s.Label)
	}
	return out
}

// TestStatusV1SaysWhereTheSwapIs: the answer of a maker waiting for the taker's XMR tells the user that it is the XMR
// that is running, with the confirmations his gate counted against the ones it needs, and the gate's deadline as a
// DERO height with no estimate on the simulator; a taker with her Claim in has it at its depth, and the outcome is set
// only at the end.
func TestStatusV1SaysWhereTheSwapIs(t *testing.T) {
	bob := &State{Role: "bob", HTLC: testHTLC, Phase: "watching_xmr", Terms: Terms{AmountDero: 2000000, AmountXmr: testXmr, DeroNet: "simulator", Net: "stagenet"},
		Lock: LockRecord{ID: 3, Tx: strings.Repeat("9b", 32), T1: 1420, T2: 1820, LockedHeight: 1002}, Stipend: StipendRecord{Tx: strings.Repeat("b2", 32)}}
	cfg := Config{HTLC: testHTLC, AmountDero: 2000000, AmountXmr: testXmr, DeroNet: "simulator", Net: Stagenet, Confirmations: 10, GateDeadline: 100, XmrTreasury: "set"}
	p := statusV1(bob, cfg, 1020, swapstatus.U(3), time.Now())
	if p.Role != "maker" || p.Direction != "withdrawal" || p.Lock == nil || *p.Lock != 3 || p.Fee != "5000000000" || p.DeroAmount != "2000000" || p.Outcome != "" {
		t.Fatalf("the maker's answer: %+v", p)
	}
	want := map[string]string{"dero_lock": "done", "stipend": "done", "xmr_lock": "active", "gate": "waiting", "claim": "waiting", "sweep": "waiting"}
	for _, s := range p.Steps {
		if want[s.ID] != s.State {
			t.Errorf("step %s is %s, want %s", s.ID, s.State, want[s.ID])
		}
	}
	x := p.Steps[2]
	if x.Conf == nil || *x.Conf != 3 || x.Need == nil || *x.Need != 10 || x.TxID != "" || x.Deadline == nil || x.Deadline.Height != 1420-102 || x.Deadline.ETA != nil {
		t.Fatalf("the XMR step: %+v %+v", x, x.Deadline)
	}
	if l := p.Steps[0]; *l.Conf != 18 || *l.Need != 8 || l.Deadline != nil || l.Label != "You lock 20 DERO for the taker" {
		t.Fatalf("the lock step: %+v", l)
	}

	alice := &State{Role: "alice", HTLC: testHTLC, Phase: "claim_confirming", Terms: bob.Terms, Lock: LockRecord{ID: 3, T1: 1420, T2: 1820, SeenHeight: 1003, ClaimSeenHeight: 1300},
		Xmr: XmrRecord{LockTx: strings.Repeat("10", 32)}, Gate: strings.Repeat("ee", 32), ClaimTx: strings.Repeat("c1", 32)}
	p = statusV1(alice, cfg, 1305, nil, time.Now())
	if p.Role != "taker" || p.Direction != "deposit" || p.Steps[4].State != "active" || *p.Steps[4].Conf != 5 || p.Steps[4].TxID != strings.Repeat("c1", 32) {
		t.Fatalf("the taker's claim: %+v", p.Steps[4])
	}
	alice.Phase, alice.Done = "claimed", true
	if p = statusV1(alice, cfg, 1310, nil, time.Now()); p.Outcome != "swapped" || p.Next == "" {
		t.Fatalf("the taker at the end: %q %q", p.Outcome, p.Next)
	}
	for _, s := range p.Steps {
		if s.State != "done" || s.Deadline != nil {
			t.Fatalf("an ended swap's step %s: %s, %+v", s.ID, s.State, s.Deadline)
		}
	}
	// off the simulator a DERO deadline carries the companion's estimate, from the tip it read
	cfg.DeroNet = "testnet"
	bob.Terms.DeroNet = "testnet"
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	p = statusV1(bob, cfg, 1020, nil, now)
	if d := p.Steps[2].Deadline; d == nil || d.ETA == nil || *d.ETA != now.Add(time.Duration(1318-1020)*18*time.Second).Format(time.RFC3339) {
		t.Fatalf("the estimate: %+v", d)
	}
}

// TestStatusSaysWhatItRead is F-1, F-2 and F-3 (docs/MAINNET_TODO.md X-04): the status report gives the daemon's own
// height as the daemon's, not as the lock's; calls a regtest swap regtest; and says a maker's stipend height is not
// recorded rather than 0. F-4: a token leg's Claim ends saying the token was claimed, not DERO.
func TestStatusSaysWhatItRead(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 126})
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	driveUntil(r, "gate_received", 300)
	bob := r.loadState("bob")
	bob.Terms.Net = "mainnet" // what a -net regtest swap records: mainnet-form addresses, against the simulator
	lines := strings.Join(Reconcile(r.ctx, bob, newMemDero(r.chain, bobAddr), nil).Lines, "\n")
	for _, want := range []string{"on regtest (mainnet-form addresses, against the DERO simulator)", "; the daemon is at height ", "(mined height not recorded)"} {
		if !strings.Contains(lines, want) {
			t.Errorf("the report does not say %q:\n%s", want, lines)
		}
	}
	for _, not := range []string{"on mainnet;", " is open at height", "(height 0)"} {
		if strings.Contains(lines, not) {
			t.Errorf("the report still says %q:\n%s", not, lines)
		}
	}
	alice := r.loadState("alice")
	if strings.Contains(strings.Join(Reconcile(r.ctx, alice, newMemDero(r.chain, aliceAddr), nil).Lines, "\n"), "(mined height not recorded)") {
		t.Error("the taker's stipend, found in her wallet with its height, is reported without one")
	}
}
