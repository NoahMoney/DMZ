package main

import (
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Asset ids for the fakes: DERO, two tokens a pool trades against DERO, a launch's token, and a wrapped asset used as
// a quote (a wXMR-quoted pool).
var (
	tokA  = strings.Repeat("a1", 32)
	tokB  = strings.Repeat("b2", 32)
	tokC  = strings.Repeat("c3", 32)
	wxmr  = strings.Repeat("d6", 32)
	lnchB = tokB // a launch's asset leg is its own SCID
)

type fakeMarket struct {
	fd, fa, fc   uint64
	quote, asset string
	lateFD       uint64 // quote fees that arrive between the bot's read and the claim's execution
	lateFA       uint64
}

// fakeChain: markets with fee balances in two legs, a pass with a per-asset ad counter and a registered count, and
// crash injection on sends. A deposit with no pass registered is refused, as founders.bas v2 refuses it.
type fakeChain struct {
	m         map[string]*fakeMarket
	ad        map[string]uint64 // the pass's ad_<i>, per asset id
	hst       uint64
	crashNext string // "claim" or "deposit": the send lands on chain but the process dies before recording it
	claims    int
	deposits  map[string][]uint64
	received  map[string]uint64 // what ClaimFees sent the treasury, per asset
}

func newFakeChain(hst uint64) *fakeChain {
	return &fakeChain{m: map[string]*fakeMarket{}, ad: map[string]uint64{}, hst: hst, deposits: map[string][]uint64{}, received: map[string]uint64{}}
}

var errCrash = errors.New("process died after the send landed")

func (f *fakeChain) Fees(scid string) (uint64, uint64) { m := f.m[scid]; return m.fd, m.fa }
func (f *fakeChain) FC(scid string) uint64             { return f.m[scid].fc }
func (f *fakeChain) Legs(scid string, launch bool) (string, string) {
	m := f.m[scid]
	return m.quote, m.asset
}
func (f *fakeChain) PassDep(asset string) uint64 { return f.ad[asset] }
func (f *fakeChain) Registered() uint64          { return f.hst }
func (f *fakeChain) SendClaim(scid string) (string, error) {
	f.claims++
	m := f.m[scid]
	m.fd, m.fa = m.fd+m.lateFD, m.fa+m.lateFA
	m.lateFD, m.lateFA = 0, 0
	if m.fd == 0 && m.fa == 0 { // ClaimFees refuses a market with nothing in it
		return "claimtx", nil
	}
	m.fc += m.fd
	f.received[m.quote] += m.fd
	f.received[m.asset] += m.fa
	m.fd, m.fa = 0, 0
	if f.crashNext == "claim" {
		f.crashNext = ""
		return "", errCrash
	}
	return "claimtx", nil
}
func (f *fakeChain) SendDeposit(asset string, units uint64) (string, error) {
	if f.hst == 0 { // refused and refunded: nothing is credited
		return "deptx", nil
	}
	f.deposits[asset] = append(f.deposits[asset], units)
	f.ad[asset] += units
	if f.crashNext == "deposit" {
		f.crashNext = ""
		return "", errCrash
	}
	return "deptx", nil
}

// the markets the sweeps run over: a DERO/tokA pool, a DERO-quoted launch, and a wXMR-quoted pool of tokC
func threeMarkets(f *fakeChain) []market {
	f.m["pool1"] = &fakeMarket{quote: deroID, asset: tokA}
	f.m["launch1"] = &fakeMarket{quote: deroID, asset: lnchB}
	f.m["pool2"] = &fakeMarket{quote: wxmr, asset: tokC}
	return []market{{"pool1", deroID, tokA}, {"launch1", deroID, lnchB}, {"pool2", wxmr, tokC}}
}

// TestClaimDepositSurvivesCrashesPerAsset runs 150 sweeps over three markets whose fees accrue in both legs, with a
// crash after either send, the passes sometimes all withdrawn, and a fresh process from the journal on disk every
// time. At the end, in every asset, the pass received exactly share bps of what the treasury was sent, less at most
// one unit of rounding per deposit, and never more.
func TestClaimDepositSurvivesCrashesPerAsset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	rng := rand.New(rand.NewSource(9))
	ch := newFakeChain(3)
	markets := threeMarkets(ch)
	const share = 5000
	held := 0
	for sweep := 0; sweep < 150; sweep++ {
		for _, m := range markets {
			if rng.Intn(2) == 0 {
				ch.m[m.scid].fd += uint64(rng.Intn(1_000_000))
			}
			if rng.Intn(2) == 0 {
				ch.m[m.scid].fa += uint64(rng.Intn(3_000_000))
			}
		}
		if rng.Intn(6) == 0 {
			ch.hst = 0 // every pass withdrawn: the bot must hold
			held++
		} else if ch.hst == 0 && rng.Intn(2) == 0 {
			ch.hst = uint64(1 + rng.Intn(20))
		}
		j, err := loadJournal(path) // a fresh process
		if err != nil {
			t.Fatal(err)
		}
		if err := j.reconcile(ch); err != nil && !errors.Is(err, errCrash) {
			t.Fatal(err)
		}
		died := false
		for _, m := range markets {
			fd, _ := ch.Fees(m.scid)
			if fd == 0 {
				continue
			}
			switch rng.Intn(6) {
			case 0:
				ch.crashNext = "claim"
			case 1:
				ch.crashNext = "deposit"
			}
			_, _, err := j.claimMarket(ch, m.scid, m.quote, m.asset)
			if errors.Is(err, errCrash) {
				died = true // process died; the next sweep reconciles
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if died {
			continue
		}
		if err := j.depositShare(ch, share); err != nil && !errors.Is(err, errCrash) {
			t.Fatal(err)
		}
		if ch.hst == 0 && len(ch.deposits) > 0 {
			for a, d := range j.Deposits {
				if ch.ad[a] >= d.Before+d.Units {
					t.Fatalf("sweep %d: a deposit landed while no pass was registered", sweep)
				}
			}
		}
	}
	// drain: a pass is registered and a final clean run deposits whatever is still owed
	ch.hst = 2
	ch.crashNext = ""
	j, err := loadJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.reconcile(ch); err != nil {
		t.Fatal(err)
	}
	if err := j.depositShare(ch, share); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{deroID, tokA, lnchB, wxmr, tokC} {
		want := shareOf(ch.received[a], share)
		n := uint64(len(ch.deposits[a]))
		if ch.ad[a] > want || ch.ad[a]+n < want {
			t.Fatalf("asset %s…: the pass received %d, want %d minus at most %d (the treasury was sent %d)", a[:6], ch.ad[a], want, n, ch.received[a])
		}
		if ch.received[a] == 0 {
			t.Fatalf("asset %s… never received a fee: the test does not exercise it", a[:6])
		}
	}
	for a, o := range j.Owed {
		if shareOf(o, share) != 0 {
			t.Fatalf("journal not drained: %s… still owes %d", a[:6], o)
		}
	}
	if len(j.Claims) != 0 || len(j.Deposits) != 0 {
		t.Fatalf("journal not drained: %d claims, deposits %v", len(j.Claims), j.Deposits)
	}
	t.Logf("%d claims, %d sweeps with no pass registered; deposited DERO %d, tokA %d, launch %d, wXMR %d, tokC %d",
		ch.claims, held, ch.ad[deroID], ch.ad[tokA], ch.ad[lnchB], ch.ad[wxmr], ch.ad[tokC])
}

// While no pass is registered, v2 refuses every Deposit: nothing is sent, and what is owed is held in the journal and
// deposited in full, in each asset, once a pass registers.
func TestNoDepositWhileNoPassIsRegistered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	ch := newFakeChain(0)
	threeMarkets(ch)
	ch.m["pool1"].fd, ch.m["pool1"].fa = 10000, 40000
	ch.m["pool2"].fd, ch.m["pool2"].fa = 6000, 800
	j := newJournal(path)
	for _, m := range []market{{"pool1", deroID, tokA}, {"pool2", wxmr, tokC}} {
		if _, _, err := j.claimMarket(ch, m.scid, m.quote, m.asset); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		if err := j.depositShare(ch, 5000); err != nil {
			t.Fatal(err)
		}
	}
	if len(ch.deposits) != 0 || len(j.Deposits) != 0 {
		t.Fatalf("a deposit was sent with no pass registered: %v", ch.deposits)
	}
	j2, err := loadJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if j2.Owed[deroID] != 10000 || j2.Owed[tokA] != 40000 || j2.Owed[wxmr] != 6000 || j2.Owed[tokC] != 800 {
		t.Fatalf("held fees not persisted: %v", j2.Owed)
	}
	ch.hst = 1
	if err := j2.depositShare(ch, 5000); err != nil {
		t.Fatal(err)
	}
	if ch.ad[deroID] != 5000 || ch.ad[tokA] != 20000 || ch.ad[wxmr] != 3000 || ch.ad[tokC] != 400 {
		t.Fatalf("the first deposits after a registration: %v", ch.ad)
	}
	if len(j2.Owed) != 0 {
		t.Fatalf("still owed after the deposits: %v", j2.Owed)
	}
}

// TestUnlandedClaimIsDropped: a claim record whose market's fc never moved was never executed.
func TestUnlandedClaimIsDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	j := newJournal(path)
	j.Claims = []claim{{SCID: "m", Quote: deroID, Asset: tokA, FD: 500, FA: 90}}
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	ch := newFakeChain(1)
	ch.m["m"] = &fakeMarket{fd: 500, fa: 90, quote: deroID, asset: tokA}
	j2, err := loadJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := j2.reconcile(ch); err != nil {
		t.Fatal(err)
	}
	if len(j2.Claims) != 0 || len(j2.Owed) != 0 {
		t.Fatalf("unlanded claim kept: %+v owed %v", j2.Claims, j2.Owed)
	}
}

// inertChain: sends are "included" (a txid comes back) but nothing executes, so no counter moves.
type inertChain struct{ *fakeChain }

func (f inertChain) SendClaim(scid string) (string, error) { f.claims++; return "claimtx", nil }
func (f inertChain) SendDeposit(asset string, units uint64) (string, error) {
	f.deposits[asset] = append(f.deposits[asset], units)
	return "deptx", nil
}

// TestIncludedButNotExecuted: a claim whose tx landed without moving fc owes nothing and is not kept; a deposit
// whose tx landed without moving ad_<i> stays open (and is resent by the next reconcile), never closed.
func TestIncludedButNotExecuted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	f := newFakeChain(1)
	f.m["m"] = &fakeMarket{fd: 500, fa: 70, quote: deroID, asset: tokA}
	ch := inertChain{f}
	j := newJournal(path)
	if _, _, err := j.claimMarket(ch, "m", deroID, tokA); err == nil {
		t.Fatal("an unexecuted claim was counted")
	}
	if len(j.Claims) != 0 || len(j.Owed) != 0 {
		t.Fatalf("unexecuted claim kept: %+v %v", j.Claims, j.Owed)
	}
	// a confirmed claim, then a deposit that does not execute
	j.Owed = map[string]uint64{tokA: 500}
	if err := j.depositShare(ch, 5000); err == nil {
		t.Fatal("an unexecuted deposit was closed")
	}
	if d := j.Deposits[tokA]; d == nil || d.Units != 250 || j.Owed[tokA] != 500 {
		t.Fatalf("deposit record lost: %+v owed %v", d, j.Owed)
	}
	j2, err := loadJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if d := j2.Deposits[tokA]; d == nil || d.Units != 250 {
		t.Fatalf("deposit not persisted: %+v", d)
	}
	if err := j2.reconcile(ch); err == nil || len(f.deposits[tokA]) != 2 {
		t.Fatalf("reconcile should resend and fail again: err %v, sends %d", err, len(f.deposits[tokA]))
	}
}

// The quote leg is counted by the fc growth, so fees that arrive between the read and the claim's execution are owed
// too. The asset leg has no counter: it is counted at the read, and what arrived after it reaches the treasury
// uncounted (journal.go). Two sequential claims on one market never count the same growth twice.
func TestClaimedUnitsIsTheCounterGrowth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	ch := newFakeChain(1)
	ch.m["m"] = &fakeMarket{fd: 500, fa: 300, lateFD: 200, lateFA: 50, quote: deroID, asset: tokA}
	j := newJournal(path)
	qd, ad, err := j.claimMarket(ch, "m", deroID, tokA)
	if err != nil {
		t.Fatal(err)
	}
	if qd != 700 || j.Owed[deroID] != 700 {
		t.Fatalf("quote leg owed %d, want the 700 that moved", j.Owed[deroID])
	}
	if ad != 300 || j.Owed[tokA] != 300 || ch.received[tokA] != 350 {
		t.Fatalf("asset leg owed %d (received %d), want the 300 read before the send", j.Owed[tokA], ch.received[tokA])
	}
	j.Owed = map[string]uint64{}
	j.Claims = []claim{{SCID: "m", Quote: deroID, Asset: tokA, FD: 100, FA: 5, Before: 700}, {SCID: "m", Quote: deroID, Asset: tokA, FD: 300, FA: 7, Before: 800}}
	ch.m["m"].fc = 1100
	if err := j.reconcile(ch); err != nil {
		t.Fatal(err)
	}
	if j.Owed[deroID] != 400 || j.Owed[tokA] != 12 {
		t.Fatalf("growth double counted: %v", j.Owed)
	}
}

// A market whose quote leg is empty is not claimed: nothing on chain could prove the claim ran.
func TestClaimNeedsAQuoteLeg(t *testing.T) {
	ch := newFakeChain(1)
	ch.m["m"] = &fakeMarket{fd: 0, fa: 90000, quote: deroID, asset: tokA}
	j := newJournal(filepath.Join(t.TempDir(), "journal.json"))
	qd, ad, err := j.claimMarket(ch, "m", deroID, tokA)
	if err != nil || qd != 0 || ad != 0 || ch.claims != 0 || len(j.Claims) != 0 {
		t.Fatalf("claimed a market with no quote leg: %d %d %v, %d sends", qd, ad, err, ch.claims)
	}
}

// TestDustClaimsWait: owed units whose share rounds to zero stay until they add up to one.
func TestDustClaimsWait(t *testing.T) {
	ch := newFakeChain(1)
	j := newJournal(filepath.Join(t.TempDir(), "journal.json"))
	j.Owed = map[string]uint64{tokA: 1}
	if err := j.depositShare(ch, 5000); err != nil {
		t.Fatal(err)
	}
	if j.Owed[tokA] != 1 || len(ch.deposits) != 0 {
		t.Fatalf("dust dropped or deposited: %v %v", j.Owed, ch.deposits)
	}
	j.add(tokA, 1)
	if err := j.depositShare(ch, 5000); err != nil {
		t.Fatal(err)
	}
	if len(j.Owed) != 0 || ch.ad[tokA] != 1 {
		t.Fatalf("two dust units should deposit one: %v ad %d", j.Owed, ch.ad[tokA])
	}
}

// TestDryChainSendsNothing: reconcile, claim and deposit through the dry chain keep every record and send nothing.
func TestDryChainSendsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	real := newFakeChain(1)
	real.m["m"] = &fakeMarket{fd: 500, fa: 5, quote: deroID, asset: tokA}
	ch := dryChain{real}
	j := newJournal(path)
	j.Owed = map[string]uint64{deroID: 20}
	j.Deposits = map[string]*deposit{deroID: {Units: 10, Base: 20}}
	if err := j.reconcile(ch); !errors.Is(err, errDry) {
		t.Fatalf("dry reconcile: %v", err)
	}
	if _, _, err := j.claimMarket(ch, "m", deroID, tokA); !errors.Is(err, errDry) {
		t.Fatalf("dry claim: %v", err)
	}
	if real.claims != 0 || len(real.deposits) != 0 || j.Deposits[deroID] == nil {
		t.Fatalf("dry run sent something: claims %d deposits %v record %+v", real.claims, real.deposits, j.Deposits)
	}
}

// The v1 bot's journal holds DERO-only records reconciled against v1's dep counter. They mean nothing against v2's
// per-asset counters, so a journal carrying any is refused, not converted; an empty one is taken.
func TestV1JournalIsRefused(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"claimed":  `{"claims":[{"scid":"m","fd":5,"before":0,"state":"claimed"}]}`,
		"deposit":  `{"claims":[],"deposit":{"units":1,"base":2,"before":0}}`,
		"future v": `{"v":3,"claims":[]}`,
	} {
		p := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".json")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadJournal(p); err == nil || !strings.Contains(err.Error(), "refusing to run") {
			t.Fatalf("%s: a v1 journal was accepted: %v", name, err)
		}
	}
	p := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(p, []byte(`{"claims":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if j, err := loadJournal(p); err != nil || j.V != journalV {
		t.Fatalf("an empty journal was refused: %v", err)
	}
}

// Every quote is swept now: a wXMR-quoted pool's fees are owed and deposited as wXMR and as its token, never as DERO.
func TestForeignQuoteIsDepositedAsItself(t *testing.T) {
	ch := newFakeChain(4)
	ch.m["p"] = &fakeMarket{fd: 8000, fa: 2000, quote: wxmr, asset: tokC}
	j := newJournal(filepath.Join(t.TempDir(), "journal.json"))
	if _, _, err := j.claimMarket(ch, "p", wxmr, tokC); err != nil {
		t.Fatal(err)
	}
	if err := j.depositShare(ch, 5000); err != nil {
		t.Fatal(err)
	}
	if ch.ad[wxmr] != 4000 || ch.ad[tokC] != 1000 || ch.ad[deroID] != 0 {
		t.Fatalf("deposits %v: each leg must be deposited in its own asset", ch.ad)
	}
}

// NFA-3. docs/NFA.md §6.6 says an NFA's DERO protocol fee is swept to the pass. It is not: this bot walks the
// factory's pool_ and launch_ lists only, and nfa.bas keeps no claimable balance to walk to.
func TestSweeperWalksOnlyPoolAndLaunch(t *testing.T) {
	want := map[string]string{"pool_": "pc", "launch_": "lc"}
	if len(registryPrefixes) != len(want) {
		t.Fatalf("the sweeper walks %v; every list added here needs a fee balance a ClaimFees can move", registryPrefixes)
	}
	for _, p := range registryPrefixes {
		if want[p.prefix] != p.count {
			t.Fatalf("unexpected registry list %q/%q", p.prefix, p.count)
		}
	}
}
