package main

import (
	"errors"
	"math/rand"
	"path/filepath"
	"testing"
)

// fakeChain: markets with fee balances, a pass with a cumulative dep counter, and crash injection on sends.
type fakeChain struct {
	fd, fc    map[string]uint64
	dep       uint64
	crashNext string // "claim" or "deposit": the send lands on chain but the process dies before recording it
	claims    int
	deposits  []uint64
}

var errCrash = errors.New("process died after the send landed")

func (f *fakeChain) FD(scid string) uint64 { return f.fd[scid] }
func (f *fakeChain) FC(scid string) uint64 { return f.fc[scid] }
func (f *fakeChain) PassDep() uint64       { return f.dep }
func (f *fakeChain) SendClaim(scid string) (string, error) {
	f.claims++
	f.fc[scid] += f.fd[scid]
	f.fd[scid] = 0
	if f.crashNext == "claim" {
		f.crashNext = ""
		return "", errCrash
	}
	return "claimtx", nil
}
func (f *fakeChain) SendDeposit(units uint64) (string, error) {
	f.deposits = append(f.deposits, units)
	f.dep += units
	if f.crashNext == "deposit" {
		f.crashNext = ""
		return "", errCrash
	}
	return "deptx", nil
}

// TestClaimDepositSurvivesCrashes runs 100 sweeps over random markets with crashes after either send, restarting
// from the journal on disk each time, and checks that the pass received exactly share bps of every unit claimed.
func TestClaimDepositSurvivesCrashes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	rng := rand.New(rand.NewSource(9))
	ch := &fakeChain{fd: map[string]uint64{}, fc: map[string]uint64{}}
	const share = 5000
	var claimedTotal uint64
	for sweep := 0; sweep < 100; sweep++ {
		// fees accrue on a few markets
		for m := 0; m < 3; m++ {
			if rng.Intn(2) == 0 {
				ch.fd["market"+string(rune('a'+m))] += uint64(rng.Intn(1_000_000))
			}
		}
		j, err := loadJournal(path) // a fresh process
		if err != nil {
			t.Fatal(err)
		}
		if err := j.reconcile(ch); err != nil && !errors.Is(err, errCrash) {
			t.Fatal(err)
		}
		if j.Deposit != nil { // reconcile itself crashed on the resend: next sweep finishes it
			continue
		}
		died := false
		for _, scid := range []string{"marketa", "marketb", "marketc"} {
			fd := ch.FD(scid)
			if fd == 0 {
				continue
			}
			switch rng.Intn(6) {
			case 0:
				ch.crashNext = "claim"
			case 1:
				ch.crashNext = "deposit"
			}
			err := j.claimMarket(ch, scid, fd)
			claimedTotal += fd // the send always lands in this fake, crash or not
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
	}
	// drain: a final clean run deposits whatever is still open
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
	want := (claimedTotal/10000)*share + ((claimedTotal%10000)*share)/10000 // the exact share of everything claimed
	// the share is floored per deposit batch, so the total can fall short by at most one unit per batch, never exceed
	if ch.dep > want || ch.dep+uint64(len(ch.deposits)) < want {
		t.Fatalf("pass received %d, want %d minus at most %d (claimed %d over %d deposits)", ch.dep, want, len(ch.deposits), claimedTotal, len(ch.deposits))
	}
	if len(j.Claims) != 0 || j.Deposit != nil {
		t.Fatalf("journal not drained: %d claims, deposit %v", len(j.Claims), j.Deposit)
	}
	t.Logf("claimed %d units in %d claims, deposited %d in %d deposits (share %d bps)", claimedTotal, ch.claims, ch.dep, len(ch.deposits), share)
}

// TestUnlandedClaimIsDropped: a "claiming" record whose market still holds the fees was never sent.
func TestUnlandedClaimIsDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	j := &journal{path: path, Claims: []claim{{SCID: "m", FD: 500, State: "claiming"}}}
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	ch := &fakeChain{fd: map[string]uint64{"m": 500}, fc: map[string]uint64{}} // fc never moved: the claim never landed
	j2, err := loadJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := j2.reconcile(ch); err != nil {
		t.Fatal(err)
	}
	if len(j2.Claims) != 0 || j2.owed() != 0 {
		t.Fatalf("unlanded claim kept: %+v", j2.Claims)
	}
}

// inertChain: sends are "included" (a txid comes back) but nothing executes, so no counter moves.
type inertChain struct{ fakeChain }

func (f *inertChain) SendClaim(scid string) (string, error) { f.claims++; return "claimtx", nil }
func (f *inertChain) SendDeposit(units uint64) (string, error) {
	f.deposits = append(f.deposits, units)
	return "deptx", nil
}

// TestIncludedButNotExecuted: a claim whose tx landed without moving fc owes nothing and is not kept; a deposit
// whose tx landed without moving dep stays open (and is resent by the next reconcile), never closed.
func TestIncludedButNotExecuted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	ch := &inertChain{fakeChain{fd: map[string]uint64{"m": 500}, fc: map[string]uint64{}}}
	j := &journal{path: path}
	if err := j.claimMarket(ch, "m", 500); err == nil {
		t.Fatal("an unexecuted claim was counted")
	}
	if len(j.Claims) != 0 || j.owed() != 0 {
		t.Fatalf("unexecuted claim kept: %+v", j.Claims)
	}
	// a confirmed claim, then a deposit that does not execute
	j.Claims = []claim{{SCID: "m", FD: 500, State: "claimed"}}
	if err := j.depositShare(ch, 5000); err == nil {
		t.Fatal("an unexecuted deposit was closed")
	}
	if j.Deposit == nil || j.Deposit.Units != 250 || j.owed() != 500 {
		t.Fatalf("deposit record lost: %+v owed %d", j.Deposit, j.owed())
	}
	j2, err := loadJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if j2.Deposit == nil || j2.Deposit.Units != 250 {
		t.Fatalf("deposit not persisted: %+v", j2.Deposit)
	}
	if err := j2.reconcile(ch); err == nil || len(ch.deposits) != 2 {
		t.Fatalf("reconcile should resend and fail again: err %v, sends %d", err, len(ch.deposits))
	}
}

// TestClaimedUnitsIsTheCounterGrowth: fees that accrue between the fd read and the claim's execution are claimed
// too, and the journal owes the share on what actually moved, not on the earlier read.
func TestClaimedUnitsIsTheCounterGrowth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	ch := &fakeChain{fd: map[string]uint64{"m": 700}, fc: map[string]uint64{}}
	j := &journal{path: path}
	if err := j.claimMarket(ch, "m", 500); err != nil { // 200 more arrived after the read
		t.Fatal(err)
	}
	if j.owed() != 700 {
		t.Fatalf("owed %d, want the 700 that moved", j.owed())
	}
	// two sequential claims on one market: the first record's growth stops at the second's Before
	j.Claims = []claim{{SCID: "m", FD: 100, Before: 0, State: "claiming"}, {SCID: "m", FD: 300, Before: 100, State: "claiming"}}
	ch.fc["m"] = 400
	if err := j.reconcile(ch); err != nil {
		t.Fatal(err)
	}
	if len(j.Claims) != 2 || j.Claims[0].FD != 100 || j.Claims[1].FD != 300 {
		t.Fatalf("growth double counted: %+v", j.Claims)
	}
}

// TestDustClaimsWait: claims whose share rounds to zero units stay until they add up to one.
func TestDustClaimsWait(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	ch := &fakeChain{fd: map[string]uint64{}, fc: map[string]uint64{}}
	j := &journal{path: path, Claims: []claim{{SCID: "m", FD: 1, State: "claimed"}}}
	if err := j.depositShare(ch, 5000); err != nil {
		t.Fatal(err)
	}
	if len(j.Claims) != 1 || len(ch.deposits) != 0 {
		t.Fatalf("dust claim dropped or deposited: %+v %v", j.Claims, ch.deposits)
	}
	j.Claims = append(j.Claims, claim{SCID: "n", FD: 1, State: "claimed"})
	if err := j.depositShare(ch, 5000); err != nil {
		t.Fatal(err)
	}
	if len(j.Claims) != 0 || ch.dep != 1 {
		t.Fatalf("two dust claims should deposit one unit: %+v dep %d", j.Claims, ch.dep)
	}
}

// TestDryChainSendsNothing: reconcile and deposit through the dry chain keep every record and send nothing.
func TestDryChainSendsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.json")
	real := &fakeChain{fd: map[string]uint64{"m": 500}, fc: map[string]uint64{}}
	ch := dryChain{real}
	j := &journal{path: path, Deposit: &deposit{Units: 10, Base: 20}}
	if err := j.reconcile(ch); !errors.Is(err, errDry) {
		t.Fatalf("dry reconcile: %v", err)
	}
	if err := j.claimMarket(ch, "m", 500); !errors.Is(err, errDry) {
		t.Fatalf("dry claim: %v", err)
	}
	if real.claims != 0 || len(real.deposits) != 0 || j.Deposit == nil {
		t.Fatalf("dry run sent something: claims %d deposits %v record %+v", real.claims, real.deposits, j.Deposit)
	}
}
