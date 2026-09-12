package xmrswap

import (
	"context"
	"crypto/rand"
	"errors"
	"path/filepath"
	"testing"
)

// The four findings this file was written to reproduce — XMR-2's terminal sweep, XMR-3's gate on whatever
// wallet has the name, and the open_wallet miss the refuter found under them — are fixed, so every test here
// is the same case with its assertion turned round: what the companion must do now.

// zzRefW is a minimal Wallet: it answers whatever the test tells it to, and records the calls.
type zzRefW struct {
	addr      string // whatever wallet is "open" reports this
	opened    []string
	closes    int
	refreshes int
	sweeps    int
	sweepErr  []error // consumed one per SweepAll call
	unlocked  uint64
	incoming  []Incoming
	height    uint64
	openFails bool
}

func (s *zzRefW) Height(context.Context) (uint64, error)                   { return s.height, nil }
func (s *zzRefW) Address(context.Context) (string, error)                  { return s.addr, nil }
func (s *zzRefW) Transfer(context.Context, string, uint64) (string, error) { return "tx", nil }
func (s *zzRefW) GenerateFromKeys(_ context.Context, name, address, _, _, _ string, _ uint64) error {
	return errors.New("generate must not be reached: " + name + " " + address)
}
func (s *zzRefW) OpenWallet(_ context.Context, name, _ string) error {
	s.opened = append(s.opened, name)
	if s.openFails {
		return &RPCError{Method: "open_wallet", Code: -1, Message: "no such file"}
	}
	return nil
}
func (s *zzRefW) CloseWallet(context.Context) error { s.closes++; return nil }
func (s *zzRefW) Refresh(context.Context, uint64) error {
	s.refreshes++
	return nil
}
func (s *zzRefW) Incoming(context.Context, uint64) ([]Incoming, error) { return s.incoming, nil }
func (s *zzRefW) UnlockedBalance(context.Context) (uint64, error)      { return s.unlocked, nil }
func (s *zzRefW) Outgoing(context.Context) ([]string, error)           { return nil, nil }
func (s *zzRefW) SweepAll(context.Context, string) ([]string, error) {
	s.sweeps++
	if len(s.sweepErr) > 0 {
		e := s.sweepErr[0]
		s.sweepErr = s.sweepErr[1:]
		if e != nil {
			return nil, e
		}
	}
	return []string{"swepttx"}, nil
}

func zzRefSwap(t *testing.T, w Wallet) *Swap {
	t.Helper()
	st := NewState(filepath.Join(t.TempDir(), "s.json"), "bob", "ab")
	st.MyXmr = "bobsownaddress"
	st.Joint = "THE-JOINT-ADDRESS"
	// the sweep forms s_a + s_b before it opens anything, so both halves must be real scalars
	own, err := NewScalar(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := NewScalar(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	st.Own.Secret, st.Revealed = own.Hex(), peer.Hex()
	cfg := Config{Confirmations: 10, SweepWaits: 3, Log: func(f string, a ...interface{}) { t.Logf(f, a...) }}
	return &Swap{cfg: cfg, st: st, xmr: w, peer: "alice"}
}

// XMR-2, inverted: a SweepAll refusal with coins in the wallet is still terminal — the fate of a spend the
// wallet may have built is genuinely unknown — and nothing retries it.
func TestRefuterSweepTerminalOnOneRefusal(t *testing.T) {
	w := &zzRefW{addr: "THE-JOINT-ADDRESS", unlocked: 1_000_000_000_000,
		sweepErr: []error{&RPCError{Method: "sweep_all", Code: -37, Message: "not enough unlocked money"}}}
	s := zzRefSwap(t, w)
	s.st.Phase = "sweep_ready"
	err := s.sweep(context.Background())
	t.Logf("first sweep err=%v phase=%s done=%v", err, s.st.Phase, s.st.Done)
	if !s.st.Done || s.st.Phase != "sweep_unknown" {
		t.Fatalf("expected terminal sweep_unknown, got phase=%s done=%v", s.st.Phase, s.st.Done)
	}
	if w.closes == 0 {
		t.Fatal("the joint spend wallet was left open in the wallet-rpc")
	}
	before := w.sweeps
	_ = s.sweep(context.Background())
	if w.sweeps != before {
		t.Fatalf("unexpected retry")
	}
	t.Logf("a refusal with %d piconero unlocked stays terminal, the spend wallet is closed, nothing retries", 1_000_000_000_000)
}

// XMR-2, inverted: an unlocked balance of zero proves nothing was spent, so it is not the unknown the terminal
// state is for. The phase stays sweep_ready, SweepAll is never called, and the next pass sweeps once the
// wallet has scanned.
func TestRefuterSweepWaitsWhileTheWalletHasNothingSpendable(t *testing.T) {
	w := &zzRefW{addr: "THE-JOINT-ADDRESS", unlocked: 0}
	s := zzRefSwap(t, w)
	s.st.Phase = "sweep_ready"
	if err := s.sweep(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if s.st.Phase != "sweep_ready" || s.st.Done || s.st.Sweep.Unknown {
		t.Fatalf("phase=%s done=%v unknown=%v: an empty wallet must not end the swap", s.st.Phase, s.st.Done, s.st.Sweep.Unknown)
	}
	if w.sweeps != 0 {
		t.Fatalf("SweepAll was called %d times against a wallet with nothing in it", w.sweeps)
	}
	if w.refreshes == 0 {
		t.Fatal("the joint wallet was read without being refreshed: generate_from_keys never scans")
	}
	// the scan catches up
	w.unlocked = 1_000_000_000_000
	if err := s.sweep(context.Background()); err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if s.st.Phase != "swept" || w.sweeps != 1 {
		t.Fatalf("phase=%s after %d sweeps, want swept after exactly one", s.st.Phase, w.sweeps)
	}
	t.Logf("the sweep waited for the scan and then spent once; wallets opened %v", w.opened)
}

// XMR-3, inverted: confirmedAtJoint refuses to count anything out of a wallet whose address is not st.Joint.
func TestRefuterGateRefusesAWalletThatIsNotTheJointAddress(t *testing.T) {
	w := &zzRefW{
		addr:     "SOME-OTHER-ADDRESS", // the wallet under that name is NOT the joint address
		unlocked: 1_000_030_000_000,
		height:   500,
		incoming: []Incoming{{TxID: "d", Amount: 1_000_030_000_000, Height: 400, Confirmations: 100, Unlocked: true}},
	}
	s := zzRefSwap(t, w)
	sum, _, _, err := s.confirmedAtJoint(context.Background())
	if err == nil {
		t.Fatalf("the gate read %d piconero out of the wallet %q, which is not st.Joint %q", sum, w.addr, s.st.Joint)
	}
	if sum != 0 {
		t.Fatalf("a refused gate still returned %d", sum)
	}
	t.Logf("refused: %v", err)
}

// XMR-3 (b), inverted: openJoint refuses a pre-existing wallet of that name whose address is not st.Joint.
func TestRefuterOpenJointRefusesAnyWalletOfThatName(t *testing.T) {
	w := &zzRefW{addr: "SOME-OTHER-ADDRESS"}
	s := zzRefSwap(t, w)
	err := s.openJoint(context.Background(), s.walletName("view"), "")
	if err == nil {
		t.Fatalf("openJoint(%q) accepted a wallet holding %q; st.Joint is %q", s.walletName("view"), w.addr, s.st.Joint)
	}
	t.Logf("refused: %v", err)
}

// XMR-3 (c): with no pre-existing wallet the generate path still runs, and monero.go's own echo check is the
// second place the address is compared.
func TestRefuterGenerateIsStillReachedWhenNoWalletExists(t *testing.T) {
	w := &zzRefW{addr: "SOME-OTHER-ADDRESS", openFails: true}
	s := zzRefSwap(t, w)
	err := s.openJoint(context.Background(), s.walletName("view"), "")
	t.Logf("with no pre-existing wallet, openJoint reached GenerateFromKeys: err=%v", err)
	if err == nil {
		t.Fatalf("expected the generate path")
	}
}

// The refuter's own find, inverted: an open_wallet refusal cannot spend a piconero, so it must not end the
// swap. The durable "sweeping" intent is written after the wallet is open and the balance is read, so a
// wallet-rpc that comes back healthy between passes sweeps normally.
type zzRefOpenFailW struct {
	zzRefW
	openErr error
	opens   int
}

func (s *zzRefOpenFailW) OpenWallet(_ context.Context, name, _ string) error {
	s.opens++
	s.opened = append(s.opened, name)
	return s.openErr
}

func TestRefuterOpenWalletFailureDoesNotEndTheSwap(t *testing.T) {
	w := &zzRefOpenFailW{
		zzRefW:  zzRefW{addr: "THE-JOINT-ADDRESS", unlocked: 1_000_000_000_000},
		openErr: &RPCError{Method: "open_wallet", Code: -21, Message: "No wallet dir configured"},
	}
	s := zzRefSwap(t, w)
	s.st.Phase = "sweep_ready"

	err := s.sweep(context.Background())
	t.Logf("pass 1: err=%v phase=%s done=%v SweepAll calls=%d", err, s.st.Phase, s.st.Done, w.sweeps)
	if s.st.Phase != "sweep_ready" || s.st.Done {
		t.Fatalf("expected to stay in sweep_ready, got phase=%s done=%v", s.st.Phase, s.st.Done)
	}

	// the wallet-rpc is fixed between passes; the companion's loop calls Step again
	w.openErr = nil
	err = s.sweep(context.Background())
	t.Logf("pass 2 (wallet-rpc healthy again): err=%v phase=%s done=%v SweepAll calls=%d", err, s.st.Phase, s.st.Done, w.sweeps)
	if w.sweeps != 1 || s.st.Phase != "swept" {
		t.Fatalf("SweepAll called %d times, phase %s; want one sweep and swept", w.sweeps, s.st.Phase)
	}
}
