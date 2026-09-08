package main

import (
	"errors"
	"path/filepath"
	"testing"
)

// A backend that took the payment but can never show it: VerifyRelease fails every time.
type blindBackend struct {
	countingBackend
	verifyCalls int
}

func (b *blindBackend) VerifyRelease(j uint64, ftx, dest string, amount uint64) (bool, error) {
	b.verifyCalls++
	return false, errors.New("foreign chain does not show it")
}

// The retry path re-asks the backend to pay a withdrawal whose payment stays invisible. That is only safe while
// the on-chain Paying mark still blocks a Veto; under a lapsed mark a second payment is the one outcome worse
// than a slow one. So when markPaying reports the mark has lapsed, the intent must stay "paid" and the backend
// must not be asked again — the crash fuzz cannot reach this because its mark is always fresh.
func TestALapsedMarkKeepsTheIntentPaidRatherThanPayingAgain(t *testing.T) {
	old := *verifyTries
	*verifyTries = 2
	defer func() { *verifyTries = old }()

	be := &blindBackend{countingBackend: countingBackend{paid: map[uint64]int{}, ftx: map[uint64]string{}}}
	mem, err := loadMemory(filepath.Join(t.TempDir(), "state.json"), "vaultA")
	if err != nil {
		t.Fatal(err)
	}
	markFresh := true
	markPaying := func(uint64) bool { return markFresh }
	vote := func(uint64, string) bool { t.Fatal("voted Released on a payment that never verified"); return false }

	// pass 1: mark, pay, record the txid; verification fails (attempt 1 of 2)
	if err := processWithdrawal(mem, be, 4, "dest-4", 900, markPaying, vote, nil); err == nil {
		t.Fatal("expected a verify failure")
	}
	if be.paid[4] != 1 || mem.Intents["4"].State != "paid" {
		t.Fatalf("after pass 1: paid %d state %s", be.paid[4], mem.Intents["4"].State)
	}
	// the mark lapses before the retries run out
	markFresh = false
	// pass 2: attempt 2 of 2 exhausts the tries; the old behaviour reset to "paying" and re-paid on pass 3
	processWithdrawal(mem, be, 4, "dest-4", 900, markPaying, vote, nil)
	processWithdrawal(mem, be, 4, "dest-4", 900, markPaying, vote, nil)
	processWithdrawal(mem, be, 4, "dest-4", 900, markPaying, vote, nil)
	if be.paid[4] != 1 {
		t.Fatalf("the backend was asked to pay %d times under a lapsed mark", be.paid[4])
	}
	if it := mem.Intents["4"]; it.State != "paid" || it.FTx == "" {
		t.Fatalf("the record was discarded: state %s ftx %q", it.State, it.FTx)
	}
	if be.verifyCalls < 4 {
		t.Fatalf("stopped verifying after %d calls; it must keep checking every pass", be.verifyCalls)
	}
	// and with the mark fresh again, the documented retry is allowed exactly once
	markFresh = true
	processWithdrawal(mem, be, 4, "dest-4", 900, markPaying, vote, nil)
	if mem.Intents["4"].State != "paying" {
		t.Fatalf("with a fresh mark the retry should reopen the intent; state %s", mem.Intents["4"].State)
	}
}
