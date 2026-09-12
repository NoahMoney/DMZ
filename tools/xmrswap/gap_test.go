package xmrswap

// Named regressions for the mainnet gap analysis (docs/MAINNET_GAP_ANALYSIS.md): each test is the closing of one row.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestTakerRefusesAnOfferForAnotherDeroNetwork (GAP-007): the DERO network is a term. A maker whose lock would be
// on another DERO network than the taker's daemon is refused before anything is locked, and a companion that was
// not told which network its daemon is on does not start.
func TestTakerRefusesAnOfferForAnotherDeroNetwork(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 131})
	// off the simulator the maker's width must carry the t1 rule at his depth (the review of 2026-09-12, finding 6)
	r.bob = startWith(r, "bob", func(c *Config) { c.DeroNet, c.T1Delta = "mainnet", T1Quote(c.Confirmations, c.GateDeadline) })
	r.alice = startWith(r, "alice", nil)
	driveUntil(r, "refused", 30)
	if a := r.loadState("alice"); !strings.Contains(a.Outcome, "DERO mainnet") || len(r.chain.locks) != 0 {
		t.Fatalf("outcome %q, %d locks", a.Outcome, len(r.chain.locks))
	}
	cfg := r.config("bob")
	cfg.DeroNet = ""
	if _, err := NewBob(cfg, NewState(filepath.Join(r.dir, "nonet.json"), "bob", testHTLC), newMemDero(r.chain, bobAddr), r.bobW, r.prover, nil); err == nil || !strings.Contains(err.Error(), "DERO network") {
		t.Fatalf("a companion with no DERO network: %v", err)
	}
}

// TestDeroClientSendsWalletCredentialsOnlyToTheWallet (GAP-008): the login goes to the wallet as basic auth and
// never to the daemon; a wallet that requires it refuses a call without it; and the probe tells a wallet anybody
// can drive from one that asks for credentials.
func TestDeroClientSendsWalletCredentialsOnlyToTheWallet(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	seen := map[string]string{}
	mk := func(name string, require bool) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen[name] = r.Header.Get("Authorization")
			mu.Unlock()
			if require {
				if u, p, ok := r.BasicAuth(); !ok || u != "alice" || p != "s3cret" {
					w.WriteHeader(http.StatusUnauthorized)
					io.WriteString(w, "Authorization Required") // what the derohe wallet answers
					return
				}
			}
			w.Write([]byte(`{"jsonrpc":"2.0","id":"1","result":{"address":"` + aliceAddr + `","height":7}}`))
		}))
	}
	daemon, wallet, open := mk("daemon", false), mk("wallet", true), mk("open", false)
	defer daemon.Close()
	defer wallet.Close()
	defer open.Close()

	c := NewDeroRPC(daemon.URL, wallet.URL, strings.Repeat("ab", 32))
	if _, err := c.Address(ctx); err == nil || !strings.Contains(err.Error(), "http 401") {
		t.Fatalf("a wallet that requires a login answered a call without one: %v", err)
	}
	c.Login = "alice:s3cret"
	if a, err := c.Address(ctx); err != nil || a != aliceAddr {
		t.Fatalf("with the login: %q %v", a, err)
	}
	if _, err := c.Height(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	daemonAuth, walletAuth := seen["daemon"], seen["wallet"]
	mu.Unlock()
	if daemonAuth != "" || !strings.HasPrefix(walletAuth, "Basic ") {
		t.Fatalf("credentials: daemon saw %q, wallet saw %q", daemonAuth, walletAuth)
	}
	if anon, err := c.WalletAcceptsAnonymous(ctx); err != nil || anon {
		t.Fatalf("a wallet that asks for credentials was reported open: %v %v", anon, err)
	}
	c2 := NewDeroRPC(daemon.URL, open.URL, strings.Repeat("ab", 32))
	c2.Login = "anything:atall"
	if anon, err := c2.WalletAcceptsAnonymous(ctx); err != nil || !anon {
		t.Fatalf("a wallet anybody can drive was not reported: %v %v", anon, err)
	}
}
