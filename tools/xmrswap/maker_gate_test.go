package xmrswap

// The maker's gate: MT-B01 (U2, his side: the G + 2 margin) and MT-G05 (a lying monerod, the Monero maker's row of
// docs/CROSS_CHAIN_TRUST_MODEL.md §5). Each drives a maker's companion one step at a time from a state built here,
// over a view-only wallet that answers what the test says (zzRefW). The height he decides at, and every answer he
// reads, are exactly the test's.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
)

// sentChannel records what a companion sends and delivers nothing to it.
type sentChannel struct{ sent []Message }

func (c *sentChannel) Send(ctx context.Context, to string, m Message) error {
	c.sent = append(c.sent, m)
	return nil
}

func (c *sentChannel) Recv(ctx context.Context) (Message, error) {
	<-ctx.Done()
	return Message{}, ctx.Err()
}

func (c *sentChannel) gates() int {
	n := 0
	for _, m := range c.sent {
		if m.Kind == KindGate {
			n++
		}
	}
	return n
}

// gateTestT1 is the t1 of the maker's lock in these tests.
const gateTestT1 = 5000

// makerAtTheGate is a maker's companion in phase: his lock's t1 is gateTestT1, G is the mainnet profile's, his own gate
// margin is set as low as it goes, the DERO chain stands at height h, and w is his view-only wallet on the joint
// address.
func makerAtTheGate(t *testing.T, phase string, h uint64, w *zzRefW) (*Swap, *sentChannel) {
	t.Helper()
	s := zzRefSwap(t, w)
	s.cfg = Config{GateMargin: 1, Confirmations: 10, Log: func(f string, a ...interface{}) { t.Logf(f, a...) }}.withDefaults()
	s.st.Phase, s.st.SwapID = phase, "maker-gate-test"
	s.st.Lock.T1, s.st.Lock.T2 = gateTestT1, gateTestT1+400
	s.st.Terms.AmountXmr = testXmr
	var kb [32]byte
	if _, err := rand.Read(kb[:]); err != nil {
		t.Fatal(err)
	}
	s.st.Gate, s.st.HK = hex.EncodeToString(kb[:]), SHA256Hex(kb[:])
	used, err := LoadUsed(UsedPath(s.st.Path()))
	if err != nil {
		t.Fatal(err)
	}
	s.used = used
	s.dero = newMemDero(newFakeChain(h), bobAddr)
	ch := &sentChannel{}
	s.ch = ch
	return s, ch
}

// paidJoint is a view-only wallet at height 500 showing the agreed XMR at the joint address, 100 deep, unlocked and in
// its balance: the truth, or a lie that agrees with itself. mod changes the one payment.
func paidJoint(mod func(*Incoming)) *zzRefW {
	in := Incoming{TxID: strings.Repeat("d", 64), Amount: testXmr, Height: 400, Confirmations: 100, Unlocked: true}
	if mod != nil {
		mod(&in)
	}
	return &zzRefW{addr: "THE-JOINT-ADDRESS", unlocked: testXmr, height: 500, incoming: []Incoming{in}}
}

// TestTheMakersGateMarginIsGPlusTwo (MT-B01, U2, the maker's side). G is the mainnet profile's.
//   - A maker's gate margin is at least G + 2 whatever he set it to (withDefaults). The gate needs a block to cross
//     the channel and one for her to read the height, and one that reaches her at t1 − G or later is ignored.
//   - A t1 width at or below that margin leaves no block for the gate, and is refused at start (newSwap).
//   - At the moment of release, his companion sends k_b at t1 − G − 3 and not at t1 − G − 2, whatever margin he set.
func TestTheMakersGateMarginIsGPlusTwo(t *testing.T) {
	G := profileGateDeadline
	if DefaultGateDeadline != G {
		t.Fatalf("DefaultGateDeadline %d is not the profile's G %d", DefaultGateDeadline, G)
	}
	for _, set := range []uint64{0, 1, G, G + 1} {
		if c := (Config{GateMargin: set}).withDefaults(); c.GateDeadline != G || c.GateMargin != G+2 {
			t.Fatalf("a margin set to %d became %d with G %d; want G + 2 = %d", set, c.GateMargin, c.GateDeadline, G+2)
		}
	}
	if c := (Config{GateMargin: G + 50}).withDefaults(); c.GateMargin != G+50 {
		t.Fatalf("a wider margin was not kept: %d", c.GateMargin)
	}
	r := newRun(t, plan{scenario: "happy", seed: 131})
	for _, c := range []struct {
		width uint64
		ok    bool
	}{{G + 2, false}, {G + 3, true}} {
		cfg := r.config("bob")
		cfg.GateDeadline, cfg.GateMargin, cfg.T1Delta = 0, 1, c.width // G from the profile, the margin as low as it goes
		ch, err := NewDirChannel(filepath.Join(r.dir, "margin"), "bob")
		if err != nil {
			t.Fatal(err)
		}
		_, err = NewBob(cfg, r.loadState("bob"), newMemDero(r.chain, bobAddr), r.bobW, r.prover, ch)
		if (err == nil) != c.ok || (err != nil && !strings.Contains(err.Error(), "no block in which the gate may go out")) {
			t.Fatalf("a t1 width of %d with G %d: %v, want accepted=%v", c.width, G, err, c.ok)
		}
	}
	for _, c := range []struct {
		below   uint64 // t1 − height
		release bool
	}{{G + 3, true}, {G + 2, false}, {G, false}, {1, false}} {
		s, ch := makerAtTheGate(t, "xmr_confirmed", gateTestT1-c.below, paidJoint(nil))
		if _, err := s.Step(context.Background()); err != nil {
			t.Fatalf("t1 − %d: %v", c.below, err)
		}
		want := "refund_window"
		if c.release {
			want = "gate_sent"
		}
		if s.st.Phase != want || (ch.gates() == 1) != c.release || ch.gates() > 1 {
			t.Fatalf("at t1 − %d (margin %d): phase %s, %d gates; want %s", c.below, s.cfg.GateMargin, s.st.Phase, ch.gates(), want)
		}
	}
}

// TestALyingMonerodOpensNoGateWhereTheLieShows (MT-G05, the Monero maker's row of CROSS_CHAIN_TRUST_MODEL.md §5). The
// maker learns of the taker's XMR from one monerod, through his own wallet-rpc. A node colluding with her shows XMR at
// the joint address that is not on the chain: he releases the gate, she claims his DERO, and his sweep finds nothing.
// Wherever the lie shows in the answers themselves, because they do not hold together or break a rule of Monero's, the
// gate stays shut. Each row is one such answer; the companion reads it for three passes and releases nothing.
func TestALyingMonerodOpensNoGateWhereTheLieShows(t *testing.T) {
	half := func(i *Incoming) { i.Amount = testXmr / 2 }
	for _, c := range []struct {
		name    string
		w       *zzRefW
		restore uint64
	}{
		{"a payment the wallet's unlocked balance does not hold", func() *zzRefW { w := paidJoint(nil); w.unlocked = testXmr - 1; return w }(), 0},
		{"a payment frozen by its unlock_time", paidJoint(func(i *Incoming) { i.UnlockTime = 1 }), 0},
		{"a payment the wallet calls locked", paidJoint(func(i *Incoming) { i.Unlocked = false }), 0},
		{"more confirmations than the wallet's own heights give", paidJoint(func(i *Incoming) { i.Height = 498 }), 0},
		{"a payment above the wallet's own height", paidJoint(func(i *Incoming) { i.Height = 600 }), 0},
		{"a payment at height 0", paidJoint(func(i *Incoming) { i.Height, i.Confirmations = 0, 500 }), 0},
		{"a payment dated before the scan this swap asked for", paidJoint(func(i *Incoming) { i.Height, i.Confirmations = 40, 460 }), 300},
		{"one payment listed twice", func() *zzRefW { w := paidJoint(half); w.incoming = append(w.incoming, w.incoming[0]); return w }(), 0},
		{"a wallet under the joint name that holds another address", func() *zzRefW { w := paidJoint(nil); w.addr = "SOME-OTHER-ADDRESS"; return w }(), 0},
	} {
		s, ch := makerAtTheGate(t, "watching_xmr", gateTestT1-400, c.w)
		s.st.RestoreHeight = c.restore
		for i := 0; i < 3; i++ {
			_, _ = s.Step(context.Background())
		}
		if s.st.Phase != "watching_xmr" || ch.gates() != 0 {
			t.Fatalf("%s: phase %s, %d gates", c.name, s.st.Phase, ch.gates())
		}
	}
}

// TestALyingMonerodThatAgreesWithItselfOpensTheGate (MT-G05, T6) states the residual as a test. A node whose every
// answer agrees (a payment at the joint address, as deep as the gate asks, unlocked and in the balance) looks the same
// as the chain to a companion that reads one node, so the gate opens. That is T6. The companion does not detect it;
// its answer is the node it reads. On mainnet -monerod must be loopback, the party's own (GAP-010), and
// CROSS_CHAIN_TRUST_MODEL.md §5 discloses that a colluding source that is the only one costs the maker his DERO. No
// second Monero source is read: MT-B09 reads several for the taker's DERO lock, and nothing like it is built for
// Monero.
func TestALyingMonerodThatAgreesWithItselfOpensTheGate(t *testing.T) {
	s, ch := makerAtTheGate(t, "watching_xmr", gateTestT1-400, paidJoint(nil))
	for i := 0; i < 3 && s.st.Phase != "gate_sent"; i++ {
		if _, err := s.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if s.st.Phase != "gate_sent" || ch.gates() != 1 {
		t.Fatalf("the residual is not what the documents say it is: phase %s, %d gates", s.st.Phase, ch.gates())
	}
}
