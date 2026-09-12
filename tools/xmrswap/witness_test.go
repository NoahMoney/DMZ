package xmrswap

// MT-B09: the taker's check of the DERO lock is confirmed by every configured DERO node before any XMR moves.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// liarDero is a DERO node that answers for the lock with a lie of its own making, and for the install with depLie's
// when set; everything else it reads truly.
type liarDero struct {
	*memDero
	lie    func(Lock) (Lock, error)
	depLie func(Deployment) Deployment
}

func (d *liarDero) Deployment(ctx context.Context) (Deployment, error) {
	dep, err := d.memDero.Deployment(ctx)
	if err == nil && d.depLie != nil {
		dep = d.depLie(dep)
	}
	return dep, err
}

// TestAWitnessShowingAnotherInstallBlocksTheXmrLock (MT-B09 with MT-A03(a)): htlc.bas v3's code is the same for every
// install, so a witness must also show the install arguments her own node showed; one that shows another g2, or
// another w1min, keeps her XMR where it is, and the maker refunds at t1.
func TestAWitnessShowingAnotherInstallBlocksTheXmrLock(t *testing.T) {
	for _, lie := range []func(Deployment) Deployment{
		func(d Deployment) Deployment { d.G2++; return d },
		func(d Deployment) Deployment { d.W1Min--; return d },
	} {
		r := newRun(t, plan{scenario: "happy", seed: 173})
		witness := &liarDero{memDero: newMemDero(r.chain, aliceAddr), lie: func(l Lock) (Lock, error) { return l, nil }, depLie: lie}
		r.bob = startWith(r, "bob", nil)
		r.alice = startAliceOn(r, newMemDero(r.chain, aliceAddr), func(c *Config) { c.Witnesses = []LockReader{witness} })
		r.until(800, r.bothDone, nil)
		alice, bob := r.loadState("alice"), r.loadState("bob")
		if r.aliceW.transfers != 0 || alice.Phase != "refused" || !strings.Contains(alice.Outcome, "installed with") || bob.Phase != "refunded" {
			t.Fatalf("alice %s (%s), %d transfers; bob %s", alice.Phase, alice.Outcome, r.aliceW.transfers, bob.Phase)
		}
		t.Logf("alice: %s", alice.Outcome)
	}
}

func (d *liarDero) ReadLock(ctx context.Context, id uint64) (Lock, error) {
	l, err := d.memDero.ReadLock(ctx, id)
	if err != nil {
		return l, err
	}
	return d.lie(l)
}

// startAliceOn is alice on her own node d, with the run's hooks.
func startAliceOn(r *run, d Dero, mod func(*Config)) *Swap {
	st := r.loadState("alice")
	ch, err := NewDirChannel(filepath.Join(r.dir, "channel"), "alice")
	if err != nil {
		r.t.Fatal(err)
	}
	ch.Poll = time.Millisecond
	spy := &spyChannel{Channel: ch, onSend: func(to string, m Message) error { return r.checkSend("alice", to, m) }}
	cfg := r.config("alice")
	if mod != nil {
		mod(&cfg)
	}
	sw, err := NewAlice(cfg, st, d, r.aliceW, r.prover, spy)
	if err != nil {
		r.t.Fatal(err)
	}
	return sw
}

// TestALyingDeroNodeBlocksTheXmrLock (MT-B09): with a second DERO node configured, the taker locks XMR only when both
// show her the same lock. A witness that shows other terms, or no lock at all, keeps her XMR where it is; so does
// her own node lying while the witness tells the truth, which is the attack the witness is for (her own node shows
// her a lock that is not the chain's). The maker refunds at t1. An honest witness changes nothing.
func TestALyingDeroNodeBlocksTheXmrLock(t *testing.T) {
	truth := func(l Lock) (Lock, error) { return l, nil }
	for _, tc := range []struct {
		name         string
		own, witness func(Lock) (Lock, error) // each node's lie; nil for the truth
		blocked      bool
	}{
		{"a witness shows another amount", nil, func(l Lock) (Lock, error) { l.Amount++; return l, nil }, true},
		{"a witness shows another t2", nil, func(l Lock) (Lock, error) { l.T2++; return l, nil }, true},
		{"a witness shows no such lock", nil, func(Lock) (Lock, error) { return Lock{}, ErrNoSuchLock }, true},
		{"her own node lies and the witness shows the chain", func(l Lock) (Lock, error) { l.T2++; return l, nil }, nil, true},
		{"an honest witness", nil, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRun(t, plan{scenario: "happy", seed: 171})
			node := func(lie func(Lock) (Lock, error)) *liarDero {
				if lie == nil {
					lie = truth
				}
				return &liarDero{memDero: newMemDero(r.chain, aliceAddr), lie: lie}
			}
			witness := node(tc.witness)
			r.bob = startWith(r, "bob", nil)
			r.alice = startAliceOn(r, node(tc.own), func(c *Config) { c.Witnesses = []LockReader{witness} })
			r.until(800, r.bothDone, nil)
			alice, bob := r.loadState("alice"), r.loadState("bob")
			if tc.blocked {
				if r.aliceW.transfers != 0 || alice.Xmr.LockTx != "" {
					t.Fatalf("alice locked XMR (%d transfers, tx %q) while a DERO node disagreed; she ended %s, bob %s", r.aliceW.transfers, alice.Xmr.LockTx, alice.Phase, bob.Phase)
				}
				if alice.Phase != "refused" || bob.Phase != "refunded" {
					t.Fatalf("alice %s (%s), bob %s: want refused and refunded", alice.Phase, alice.Outcome, bob.Phase)
				}
				t.Logf("alice: %s", alice.Outcome)
			} else if alice.Phase != "claimed" || bob.Phase != "swept" || r.aliceW.transfers != 1 {
				t.Fatalf("with an honest witness: alice %s, bob %s, %d transfers", alice.Phase, bob.Phase, r.aliceW.transfers)
			}
			if witness.calls["ReadLock"] == 0 {
				t.Fatal("the witness node was never asked")
			}
		})
	}
}
