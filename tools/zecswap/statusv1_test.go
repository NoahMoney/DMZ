package zecswap

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"veil/tools/swapstatus"
)

// keyHex is the 32 secret bytes of a test key, as testKey makes them.
func keyHex(n byte) string {
	b := make([]byte, 32)
	b[31] = n
	return hex.EncodeToString(b)
}

// zSecrets is every secret value a state or a key holds, in both cases of hex.
func zSecrets(extra []string, sts ...*State) []string {
	var out []string
	for _, v := range extra {
		out = append(out, strings.ToLower(v), strings.ToUpper(v))
	}
	for _, st := range sts {
		if st == nil {
			continue
		}
		for _, v := range []string{st.Secret, st.SecretSeen} {
			if len(v) >= 16 {
				out = append(out, strings.ToLower(v), strings.ToUpper(v))
			}
		}
	}
	return out
}

type zCollector struct {
	t    *testing.T
	dir  string
	seen map[string]bool
}

func newZCollector(t *testing.T) *zCollector {
	dir := t.TempDir()
	if d := os.Getenv("SWAPSTATUS_OUT"); d != "" {
		dir = filepath.Join(d, "zecswap")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return &zCollector{t: t, dir: dir, seen: map[string]bool{}}
}

func (c *zCollector) check(name, role string, p swapstatus.Payload, secrets []string) {
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

func zAppCheck(t *testing.T, dir string) {
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

// TestStatusV1CarriesNoSecretInAnyPhase runs the crash fuzz's four scenarios, with and without process deaths, and
// builds the answer -status would serve at every save of either machine: each is valid by the contract, names no field
// the app reads as a secret's, and holds neither the maker's secret (before or after his claim makes it public), the
// taker's copy of it, nor either Zcash key. The answers are then read through the app's own parser.
func TestStatusV1CarriesNoSecretInAnyPhase(t *testing.T) {
	c := newZCollector(t)
	keys := []string{keyHex(makerKeyN), keyHex(takerKeyN)}
	seed := int64(7000)
	for _, scenario := range []string{"happy", "maker_absent", "taker_late", "maker_never_locks"} {
		for i := 0; i < 3; i++ {
			seed++
			w := newWorld(t, seed)
			w.offline = offlineFor(scenario)
			if i > 0 {
				w.randomCrash = 0.2
			}
			w.onSave = func(role, phase string) {
				other := map[string]string{RoleMaker: RoleTaker, RoleTaker: RoleMaker}[role]
				st := w.state(role)
				peer, _ := LoadState(w.path(other), other)
				c.check(scenario, role, statusV1(st, w.config(role), "testnet", w.dero.Height(), time.Now()), zSecrets(keys, st, peer))
			}
			if !w.drive(500) {
				t.Fatalf("%s run %d did not finish:\n%s", scenario, i, w.dump())
			}
		}
	}
	for _, want := range []string{"maker|posted", "maker|locked", "maker|claimed", "maker|swapped", "maker|refunded", "taker|funded", "taker|secret_seen", "taker|swapped", "taker|refunded", "taker|walked"} {
		if !c.seen[want] {
			t.Errorf("no answer was built at %s", want)
		}
	}
	zAppCheck(t, c.dir)
}

// TestStatusV1EveryPhaseOfBothMachines builds the answer for every phase of both machines' tables, from a state holding
// both secrets and every record, with the end phases' outcomes: each is valid, carries no secret, says what is next,
// and is set as ended only at an end.
func TestStatusV1EveryPhaseOfBothMachines(t *testing.T) {
	c := newZCollector(t)
	rnd := func() string {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(b)
	}
	for _, role := range []string{RoleMaker, RoleTaker} {
		for _, asset := range []string{"", strings.Repeat("7a", 32)} {
			for ph, info := range Phases(role) {
				f := &Funding{TxID: rnd(), Value: testWant, Height: 4_200_010, BlockHash: rnd()}
				st := &State{Format: StateFormat, Role: role, Phase: ph, Done: info.Terminal, Outcome: "a sentence that quotes " + rnd(), Note: "a note that quotes " + rnd(),
					ZcashChain: "test", Board: testBoard, Escrow: testEscrow, MyDero: makerAddr, HaveOffer: true, OfferID: 4, Hash: rnd(), Secret: rnd(), SecretSeen: rnd(),
					GiveAsset: asset, GiveAmount: testGive, WantAmount: testWant, OfferExpiry: 1400, Treasury: testTreasury, CLTV: 4_200_060, AcceptHeight: 1010,
					LockDeadline: 1040, HaveEscrow: true, EscrowID: 2, TDero: 1600, LockSeenHeight: 1020, DeroTxs: map[string]string{"post": rnd(), "accept": rnd(), "lock": rnd(), "claim": rnd(), "relay": rnd(), "refund": rnd(), "cancel": rnd()},
					FundingTxID: f.TxID, FundingExpiry: 4_200_030, Funding: f, ClaimTxID: rnd(), ClaimHeight: 4_200_020, ClaimBlock: rnd(), SpendTxID: rnd(), MaxZecTip: 4_200_025,
					Refunds: []SignedTx{{Hex: rnd(), TxID: rnd()}}}
				cfg := Config{Policy: testPolicy(), Board: testBoard, Escrow: testEscrow, Treasury: testTreasury, ZcashChain: "test"}
				p := statusV1(st, cfg, "testnet", 1100, time.Now())
				c.check("phase", role, p, zSecrets([]string{keyHex(makerKeyN), keyHex(takerKeyN)}, st))
				if p.Next == "" {
					t.Errorf("the %s in %q: no next sentence", role, ph)
				}
				if st.Done != (p.Outcome != "") {
					t.Errorf("the %s in %q (done %v): outcome %q", role, ph, st.Done, p.Outcome)
				}
				b, _ := json.Marshal(p)
				if strings.Contains(string(b), "quotes") {
					t.Errorf("the %s in %q: the state's outcome or note reached the answer", role, ph)
				}
				if p.Fee != fmt.Sprint(SwapFee(testWant)) {
					t.Errorf("the %s in %q: fee %q, want %d", role, ph, p.Fee, SwapFee(testWant))
				}
			}
		}
	}
	zAppCheck(t, c.dir)
}

// TestStatusV1SaysWhereTheSwapIs: a maker whose claim is confirming has it running at its depth against C_zec, with a
// Zcash deadline carrying the companion's estimate; on regtest, where blocks are mined on demand, there is none; and a
// taker's funding shows its confirmations.
func TestStatusV1SaysWhereTheSwapIs(t *testing.T) {
	st := &State{Format: StateFormat, Role: RoleMaker, Phase: "claimed", ZcashChain: "test", Escrow: testEscrow, HaveOffer: true, GiveAmount: 200000, WantAmount: 50000000,
		Treasury: testTreasury, CLTV: 4_200_100, HaveEscrow: true, EscrowID: 1, TDero: 7700, AcceptHeight: 1000, Funding: &Funding{TxID: strings.Repeat("10", 32), Value: 50000000, Height: 4_200_000},
		ClaimTxID: strings.Repeat("aa", 32), ClaimHeight: 4_200_012, MaxZecTip: 4_200_014}
	cfg := Config{Policy: DefaultPolicy(), Escrow: testEscrow, Treasury: testTreasury}
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	p := statusV1(st, cfg, "testnet", 7000, now)
	if p.Direction != "withdrawal" || p.Fee != "250000" || p.Lock == nil || *p.Lock != 1 || p.Outcome != "" {
		t.Fatalf("the maker's answer: %+v", p)
	}
	claim := p.Steps[4]
	if claim.ID != "zec_claim" || claim.State != "active" || *claim.Conf != 3 || *claim.Need != 10 || claim.Deadline == nil || claim.Deadline.Chain != "Zcash" ||
		claim.Deadline.Height != 4_200_100-20-1 || claim.Deadline.ETA == nil || *claim.Deadline.ETA != now.Add(time.Duration(4_200_079-4_200_014)*75*time.Second).Format(time.RFC3339) {
		t.Fatalf("the claim step: %+v %+v", claim, claim.Deadline)
	}
	if f := p.Steps[3]; f.State != "done" || *f.Conf != 15 || f.Deadline != nil {
		t.Fatalf("the funding step: %+v", f)
	}
	st.ZcashChain = "regtest"
	if d := statusV1(st, cfg, "simulator", 7000, now).Steps[4].Deadline; d == nil || d.ETA != nil {
		t.Fatalf("an estimate on regtest: %+v", d)
	}
	st.SpendTxID = st.ClaimTxID // deep: the DERO Claim is what runs now
	if p := statusV1(st, cfg, "testnet", 7000, now); p.Steps[4].State != "done" || p.Steps[5].State != "active" || p.Steps[5].Deadline.Height != 7700 {
		t.Fatalf("after the claim is deep: %+v", p.Steps[4:])
	}
}
