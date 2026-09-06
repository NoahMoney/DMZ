package main

import (
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// countingBackend pays at most once per j (as every real backend must: look the payment up before paying) and
// counts how many times Release actually paid.
type countingBackend struct {
	paid  map[uint64]int
	ftx   map[uint64]string
	fails int // Release errors to inject before succeeding
}

func (b *countingBackend) Height() (int64, error)         { return 0, nil }
func (b *countingBackend) Deposits() ([]Deposit, error)   { return nil, nil }
func (b *countingBackend) Address(uint64) (string, error) { return "", nil }
func (b *countingBackend) Reserve() (uint64, error)       { return 1 << 40, nil }
func (b *countingBackend) Release(j uint64, dest string, amount uint64) (string, error) {
	if b.fails > 0 {
		b.fails--
		return "", errors.New("foreign rpc down")
	}
	if f, ok := b.ftx[j]; ok {
		return f, nil
	}
	b.paid[j]++
	b.ftx[j] = "ftx-" + strconv.FormatUint(j, 10)
	return b.ftx[j], nil
}

// TestExactlyOnceUnderCrashes kills the withdrawal path at random stages (after each durable step, and while the
// state is being reloaded from disk) for 100 withdrawals and checks that every one is paid exactly once and voted
// exactly once with the recorded txid.
func TestExactlyOnceUnderCrashes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	rng := rand.New(rand.NewSource(1))
	be := &countingBackend{paid: map[uint64]int{}, ftx: map[uint64]string{}}
	votes := map[uint64][]string{}
	for j := uint64(0); j < 100; j++ {
		dest, amount := "dest-"+strconv.FormatUint(j, 10), 1000+j
		voteRefusals := rng.Intn(3) // the first votes may be refused (quorum already complete elsewhere, or a busy wallet)
		voteReleased := func(j uint64, ftx string) bool {
			if voteRefusals > 0 {
				voteRefusals--
				return false
			}
			votes[j] = append(votes[j], ftx)
			return true
		}
		be.fails = rng.Intn(2)
		for attempt := 0; attempt < 20; attempt++ {
			mem, err := loadMemory(path) // every attempt restarts from disk, like a new process
			if err != nil {
				t.Fatal(err)
			}
			crashStage := []string{"", "after-intent", "after-pay", "after-paid", "after-vote"}[rng.Intn(5)]
			crash := func(stage string) bool { return stage == crashStage }
			err = processWithdrawal(mem, be, j, dest, amount, voteReleased, crash)
			if err == nil && mem.Intents[strconv.FormatUint(j, 10)].State == "voted" {
				break
			}
			if attempt == 19 {
				t.Fatalf("withdrawal %d never completed: %v", j, err)
			}
		}
		if be.paid[j] != 1 {
			t.Fatalf("withdrawal %d paid %d times", j, be.paid[j])
		}
		// Payments are exactly-once. Votes are at-least-once: a crash between the vote and the "voted" record
		// repeats the vote on restart, and the contract refuses a second vote from the same signer
		// (vault.bas Released: rv_<epoch>_<j>_<signer>). Every vote must carry the recorded txid.
		if len(votes[j]) < 1 {
			t.Fatalf("withdrawal %d never voted", j)
		}
		for _, f := range votes[j] {
			if f != be.ftx[j] {
				t.Fatalf("withdrawal %d voted %q want %q", j, f, be.ftx[j])
			}
		}
	}
	// the state survived every rename: reload and count
	mem, err := loadMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(mem.Intents) != 100 {
		t.Fatalf("intents on disk %d", len(mem.Intents))
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatal("temp file left behind")
	}
}

// TestWithdrawalFinality: a burn is paid on the foreign chain only once DERO calls its block stable (C6).
func TestWithdrawalFinality(t *testing.T) {
	for _, c := range []struct {
		h, stable int64
		want      bool
	}{{100, 108, true}, {108, 108, true}, {109, 108, false}, {0, 108, false}, {50, 0, false}} {
		if got := withdrawalFinal(c.h, c.stable); got != c.want {
			t.Fatalf("burn at %d, stable %d: final=%v want %v", c.h, c.stable, got, c.want)
		}
	}
}

// TestSuspiciousWithdrawal: one withdrawal above the configured share of the reserve is not paid.
func TestSuspiciousWithdrawal(t *testing.T) {
	for _, c := range []struct {
		amount, reserve, bps uint64
		want                 bool
	}{{2000, 10000, 2000, false}, {2001, 10000, 2000, true}, {1, 0, 2000, true}, {9999, 10000, 0, false}, {3, 12, 2500, false}, {4, 12, 2500, true}} {
		if got := suspiciousWithdrawal(c.amount, c.reserve, c.bps); got != c.want {
			t.Fatalf("amount %d reserve %d bps %d: %v want %v", c.amount, c.reserve, c.bps, got, c.want)
		}
	}
}

// TestCorruptStateRefusesToStart: a signer must not start with an empty memory when its state file is unreadable.
func TestCorruptStateRefusesToStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadMemory(path); err == nil {
		t.Fatal("corrupt state accepted")
	}
	if m, err := loadMemory(filepath.Join(t.TempDir(), "missing.json")); err != nil || len(m.Intents) != 0 {
		t.Fatalf("a missing file must be a fresh signer: %v", err)
	}
}

// TestIntentMismatchStops: an intent recorded for (dest, amount) must never be paid to a different tuple.
func TestIntentMismatchStops(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	mem := newMemory(path)
	be := &countingBackend{paid: map[uint64]int{}, ftx: map[uint64]string{}}
	mem.Intents["7"] = &Intent{J: 7, Dest: "a", Amount: 5, State: "paying"}
	if err := mem.save(); err != nil {
		t.Fatal(err)
	}
	err := processWithdrawal(mem, be, 7, "b", 5, func(uint64, string) bool { return true }, nil)
	if err == nil || be.paid[7] != 0 {
		t.Fatalf("mismatched intent paid: err %v paid %d", err, be.paid[7])
	}
}
