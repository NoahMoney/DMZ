package main

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"veil/tools/xmrswap"
)

func testAddress(t *testing.T, net xmrswap.Network) string {
	t.Helper()
	spend, err := xmrswap.NewScalar(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	view, err := xmrswap.NewScalar(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	a, err := xmrswap.Address(spend.Public(), view.Public(), net)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// XMR-4, first half: -net is otherwise only used to BUILD the joint address and to compare the two parties'
// declared strings, so a wallet on another network was found out at generate_from_keys — for the maker, after
// his DERO was locked and irreversible until t1. The wallet's own primary address carries its network in the
// prefix, and one call before anything is locked says so.
func TestStartupRefusesAWalletOnAnotherNetwork(t *testing.T) {
	stagenet := testAddress(t, xmrswap.Stagenet)
	if err := checkWalletNetwork(stagenet, xmrswap.Stagenet); err != nil {
		t.Fatalf("a stagenet wallet under -net stagenet: %v", err)
	}
	err := checkWalletNetwork(stagenet, xmrswap.Mainnet)
	if err == nil {
		t.Fatal("a stagenet wallet was accepted under -net mainnet")
	}
	if !strings.Contains(err.Error(), "stagenet") || !strings.Contains(err.Error(), "mainnet") {
		t.Fatalf("the refusal names neither network: %v", err)
	}
	t.Logf("refused: %v", err)

	mainnet := testAddress(t, xmrswap.Mainnet)
	if err := checkWalletNetwork(mainnet, xmrswap.Stagenet); err == nil {
		t.Fatal("a mainnet wallet was accepted under -net stagenet")
	}
	if err := checkWalletNetwork("not an address", xmrswap.Stagenet); err == nil {
		t.Fatal("an address that does not decode was accepted")
	}
}

// XMR-4, second half: --restricted-rpc and a missing --wallet-dir make on_open_wallet and
// on_generate_from_keys refuse before they look at anything, and nothing said so until the first joint wallet.
func TestStartupRefusesAWalletRpcThatCannotMakeWallets(t *testing.T) {
	for _, c := range []struct {
		name  string
		probe error
		want  string
	}{
		{"restricted", &xmrswap.RPCError{Method: "open_wallet", Code: -1, Message: "Command unavailable in restricted mode."}, "--restricted-rpc"},
		{"no wallet dir", &xmrswap.RPCError{Method: "open_wallet", Code: -21, Message: "No wallet dir configured"}, "--wallet-dir"},
	} {
		err := walletManagement(c.probe)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: %v, want a refusal naming %s", c.name, err, c.want)
		}
		t.Logf("%s: %v", c.name, err)
	}
	// a configured wallet-dir answers "file not found" for a name nobody has made: that is the pass
	ok := &xmrswap.RPCError{Method: "open_wallet", Code: -1, Message: "Failed to open wallet : file not found"}
	if err := walletManagement(ok); err != nil {
		t.Fatalf("a healthy wallet-rpc was refused: %v", err)
	}
	if err := walletManagement(nil); err == nil {
		t.Fatal("a wallet-rpc that opened the probe wallet was accepted")
	}
}

// XMR-04: the lock file is what keeps a second companion from paying the joint address again, and a swap runs
// for hours. Stamped on every pass, a live companion never looks like a dead one; left alone past the
// staleness window, a dead one's litter is taken over.
func TestStateLockIsAHeartbeat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bob.json.lock")
	mine := &stateLock{path: path, stale: time.Minute}
	other := func() *stateLock { return &stateLock{path: path, stale: time.Minute} }
	if err := mine.take(); err != nil {
		t.Fatal(err)
	}
	if err := other().take(); err == nil {
		t.Fatal("a second companion took a lock that had just been made")
	}
	age := func(d time.Duration) {
		at := time.Now().Add(-d)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	// a companion held up for longer than the window, then stamping again on its next pass
	age(2 * time.Minute)
	if err := mine.touch(); err != nil {
		t.Fatal(err)
	}
	if err := other().take(); err == nil {
		t.Fatal("a live companion's lock was taken over after it had stamped it")
	}
	// and left alone, it is taken over
	age(2 * time.Minute)
	if err := other().take(); err != nil {
		t.Fatalf("a dead process's lock was not taken over: %v", err)
	}
	mine.release()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the lock file outlived the companion: %v", err)
	}
}

// staleAfter is measured in passes of the Step loop, not in a fixed ten minutes, so an operator who polls
// slowly does not get a lock that is stale between one pass and the next.
func TestStaleAfterIsSeveralPasses(t *testing.T) {
	for _, c := range []struct {
		interval, want time.Duration
	}{
		{time.Second, time.Minute},
		{5 * time.Second, time.Minute},
		{30 * time.Second, 5 * time.Minute},
		{2 * time.Minute, 20 * time.Minute},
	} {
		if got := staleAfter(c.interval); got != c.want {
			t.Fatalf("staleAfter(%s) = %s, want %s", c.interval, got, c.want)
		}
	}
}
