package xmrswap

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestFakeWalletModelsAWalletThatHasNotScanned pins what the fake had to learn before XMR-2's test could
// exist: monero-wallet-rpc's on_generate_from_keys sets the restore height, installs the wallet and returns —
// it never calls refresh — so the wallet it just made knows nothing about the chain, and every balance read
// out of it is zero until the `refresh` RPC or the server's idle loop reaches it.
func TestFakeWalletModelsAWalletThatHasNotScanned(t *testing.T) {
	ctx := context.Background()
	fw := NewFakeWallet(Stagenet)
	w := newTestWalletRPC(t, fw)

	spend, err := NewScalar(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	view, err := NewScalar(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	addr, err := Address(spend.Public(), view.Public(), Stagenet)
	if err != nil {
		t.Fatal(err)
	}
	fw.Fund(addr, 5_000_000_000_000)
	fw.Mine(20)

	fw.SetScanLag(true)
	if err := w.GenerateFromKeys(ctx, "made-from-keys", addr, view.Hex(), spend.Hex(), "", 0); err != nil {
		t.Fatal(err)
	}
	if got, err := w.UnlockedBalance(ctx); err != nil || got != 0 {
		t.Fatalf("a wallet that has not scanned reported %d piconero (err %v)", got, err)
	}
	if in, err := w.Incoming(ctx, 0); err != nil || len(in) != 0 {
		t.Fatalf("a wallet that has not scanned listed %d incoming transfers (err %v)", len(in), err)
	}
	if _, err := w.SweepAll(ctx, addr); err == nil {
		t.Fatal("a wallet that has not scanned swept something")
	}
	if err := w.Refresh(ctx, 0); err != nil {
		t.Fatal(err)
	}
	got, err := w.UnlockedBalance(ctx)
	if err != nil || got != 5_000_000_000_000 {
		t.Fatalf("after refresh: %d piconero (err %v)", got, err)
	}
	if fw.Refreshes() != 1 {
		t.Fatalf("%d refreshes recorded", fw.Refreshes())
	}
}

// TestSwapRefreshesTheJointWalletsItMakes is XMR-2 end to end: with the fake behaving as the real server does,
// a whole happy swap still reaches the gate and the sweep, because every joint wallet the companion opens is
// refreshed before anything is read out of it. On the old code the joint wallets saw the chain the instant
// they were made, which is the only reason the happy path was ever green.
func TestSwapRefreshesTheJointWalletsItMakes(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 71})
	r.fw.SetScanLag(true)
	r.drive()
	r.check()
	r.expectExit(LockClaimed)
	bob := r.loadState("bob")
	if bob.Phase != "swept" {
		t.Fatalf("bob ended in %s, not swept", bob.Phase)
	}
	if r.fw.Refreshes() == 0 {
		t.Fatal("no wallet was ever refreshed")
	}
	t.Logf("the swap claimed and swept against wallets that scan only when told to: %d refreshes", r.fw.Refreshes())
}

// refusingWallet is a wallet-rpc that turns a transfer down the way one on the wrong network does: with its
// own error code, before it builds anything.
type refusingWallet struct {
	Wallet
	err error
}

func (w *refusingWallet) Transfer(ctx context.Context, to string, amount uint64) (string, error) {
	if w.err != nil {
		return "", w.err
	}
	return w.Wallet.Transfer(ctx, to, amount)
}

// TestAliceStaysInXmrLockingWhenTheWalletRefusesBeforeSending is XMR-4's second half. A definitive refusal —
// -2 for an address the wallet cannot parse, which is what a joint address built for another network is —
// means nothing was sent. Recording it as "the fate of the transfer is unknown" and moving to xmr_locked
// reports a lock that does not exist; the phase must stand, the error must surface, and the transfer is still
// to make when the wallet is fixed.
func TestAliceStaysInXmrLockingWhenTheWalletRefusesBeforeSending(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 72})
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	driveUntil(r, "xmr_locking", 200)

	refusing := &refusingWallet{Wallet: r.aliceW.Wallet, err: &RPCError{Method: "transfer", Code: -2, Message: "Invalid destination address"}}
	r.aliceW.Wallet = refusing

	_, err := r.alice.Step(r.ctx)
	if err == nil {
		t.Fatal("the refusal did not surface")
	}
	if !strings.Contains(err.Error(), "nothing was sent") {
		t.Fatalf("the error does not say nothing was sent: %v", err)
	}
	t.Logf("surfaced: %v", err)
	alice := r.loadState("alice")
	if alice.Phase != "xmr_locking" || alice.Xmr.LockUnknown || alice.Xmr.LockTx != "" {
		t.Fatalf("phase %s, lockUnknown %v, lockTx %q: a refusal that sent nothing was recorded as a lock", alice.Phase, alice.Xmr.LockUnknown, alice.Xmr.LockTx)
	}
	if n, _ := r.paidTo(r.aliceXmr, alice.Joint); n != 0 {
		t.Fatalf("the joint address was paid %d times by a refused transfer", n)
	}

	// the operator fixes the wallet and the same companion carries on
	refusing.err = nil
	if _, err := r.alice.Step(r.ctx); err != nil {
		t.Fatalf("after the wallet was fixed: %v", err)
	}
	r.fw.Mine(1)
	alice = r.loadState("alice")
	if alice.Phase != "xmr_locked" || alice.Xmr.LockTx == "" {
		t.Fatalf("phase %s, lockTx %q", alice.Phase, alice.Xmr.LockTx)
	}
	if n, amt := r.paidTo(r.aliceXmr, alice.Joint); n != 1 || amt != testXmr {
		t.Fatalf("the joint address was paid %d times, %d piconero", n, amt)
	}
}

// newTestWalletRPC is a real client over a fake wallet-rpc.
func newTestWalletRPC(t *testing.T, fw *FakeWallet) Wallet {
	t.Helper()
	srv := httptest.NewServer(fw)
	t.Cleanup(srv.Close)
	return NewWalletRPC(srv.URL, "", "")
}

// TestKeyedReadsAgainstARealDaemon is opt-in evidence that the keyed reads are the shape derod actually
// answers, and not only the shape the fake was taught: XMRSWAP_SIM_SCID names an htlc.bas already installed on
// the daemon at XMRSWAP_SIM_DAEMON (the local simulator by default). It reads only; it signs nothing.
func TestKeyedReadsAgainstARealDaemon(t *testing.T) {
	scid := os.Getenv("XMRSWAP_SIM_SCID")
	if scid == "" {
		t.Skip("XMRSWAP_SIM_SCID is not set: no daemon to read")
	}
	daemon := os.Getenv("XMRSWAP_SIM_DAEMON")
	if daemon == "" {
		daemon = "127.0.0.1:20000"
	}
	ctx := context.Background()
	c := NewDeroRPC(daemon, daemon, scid)
	l, err := c.ReadLock(ctx, 0)
	if err != nil {
		t.Fatalf("ReadLock(0) on %s: %v", scid, err)
	}
	t.Logf("lock 0: maker %s taker %s ha %s amount %d t1 %d t2 %d st %d", l.Maker[:8], l.Taker[:8], l.HA[:8], l.Amount, l.T1, l.T2, l.State)
	if !is64hex(l.HA) || !is64hex(l.HK) || !is64hex(l.HB) || l.T1 == 0 {
		t.Fatalf("the keyed read decoded to %+v", l)
	}
	// an id the contract has not allocated is a missing key, not a decode failure
	if _, err := c.ReadLock(ctx, 1<<40); !errors.Is(err, ErrNoSuchLock) {
		t.Fatalf("an unallocated id answered %v, want ErrNoSuchLock", err)
	}
	got, found, err := c.FindLock(ctx, l.Maker, l.HA, l.HK, l.HB)
	if err != nil || !found || got.ID != l.ID {
		t.Fatalf("FindLock: id %d found %v err %v", got.ID, found, err)
	}
	if _, err := c.ContractBalance(ctx); err != nil {
		t.Fatalf("ContractBalance: %v", err)
	}
	t.Logf("FindLock walked the contract by key and found lock %d", got.ID)
}
