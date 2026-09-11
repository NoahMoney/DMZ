package xmrswap

// Named regressions for what the real monero-wallet-rpc 0.18.5.1 showed on regtest (research/regtest/monero-smoke.log,
// tools/regtest/README.md): the fake now answers as the server does, and each test is the companion's answer to one
// difference.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func rawWalletCall(t *testing.T, url, method string, params interface{}) map[string]interface{} {
	t.Helper()
	b, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": "0", "method": method, "params": params})
	resp, err := http.Post(url+"/json_rpc", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var env map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&env)
	return env
}

// TestTheClientRefusesKeysThatDoNotDeriveTheAddress: the server makes a wallet from keys that do not derive the
// address it is given, and echoes the address back. The client refuses such keys itself, before the request, for the
// view-only wallet and the spend wallet alike.
func TestTheClientRefusesKeysThatDoNotDeriveTheAddress(t *testing.T) {
	ctx := context.Background()
	fw := NewFakeWallet(Stagenet)
	srv := httptest.NewServer(fw)
	defer srv.Close()
	w := NewWalletRPC(srv.URL, "", "")
	spend, _ := NewScalar(rand.Reader)
	view, _ := NewScalar(rand.Reader)
	wrong, _ := NewScalar(rand.Reader)
	addr, _ := Address(spend.Public(), view.Public(), Stagenet)

	// what the server does with them, asked directly: it accepts both, and the spend wallet cannot refresh
	if env := rawWalletCall(t, srv.URL, "generate_from_keys", map[string]interface{}{"filename": "raw-view", "address": addr, "viewkey": wrong.Hex(), "password": ""}); env["error"] != nil {
		t.Fatalf("the server-shaped fake refused a wrong view key: %v", env)
	}
	if env := rawWalletCall(t, srv.URL, "generate_from_keys", map[string]interface{}{"filename": "raw-spend", "address": addr, "viewkey": view.Hex(), "spendkey": wrong.Hex(), "password": ""}); env["error"] != nil {
		t.Fatalf("the server-shaped fake refused a wrong spend key: %v", env)
	}
	if env := rawWalletCall(t, srv.URL, "refresh", map[string]interface{}{"start_height": 0}); env["error"] == nil {
		t.Fatal("a wallet with the wrong spend key refreshed")
	}
	// what the client does: refuses them before anything is made
	if err := w.GenerateFromKeys(ctx, "view", addr, wrong.Hex(), "", "", 0); err == nil || !strings.Contains(err.Error(), "view key does not derive") {
		t.Fatalf("wrong view key: %v", err)
	}
	if err := w.GenerateFromKeys(ctx, "spend", addr, view.Hex(), wrong.Hex(), "", 0); err == nil || !strings.Contains(err.Error(), "spend key does not derive") {
		t.Fatalf("wrong spend key: %v", err)
	}
	if err := w.GenerateFromKeys(ctx, "right", addr, view.Hex(), spend.Hex(), "", 0); err != nil {
		t.Fatalf("the right keys: %v", err)
	}
}

// TestAViewOnlySendIsNotASend: a view-only wallet's transfer and sweep_all come back with a hash and an
// unsigned_txset, and nothing is broadcast. The client reports CodeNotSigned, which the swap treats as a refusal
// made before sending: the taker's lock stays to be made, and nothing is recorded as sent.
func TestAViewOnlySendIsNotASend(t *testing.T) {
	ctx := context.Background()
	fw := NewFakeWallet(Stagenet)
	srv := httptest.NewServer(fw)
	defer srv.Close()
	w := NewWalletRPC(srv.URL, "", "")
	spend, _ := NewScalar(rand.Reader)
	view, _ := NewScalar(rand.Reader)
	addr, _ := Address(spend.Public(), view.Public(), Stagenet)
	other, _ := Address(view.Public(), spend.Public(), Stagenet)
	if err := w.GenerateFromKeys(ctx, "watch", addr, view.Hex(), "", "", 0); err != nil {
		t.Fatal(err)
	}
	fw.Fund(addr, 5_000_000_000_000)
	fw.Mine(11)
	if _, err := w.Transfer(ctx, other, 1_000_000_000_000); !isRPC(err, CodeNotSigned) {
		t.Fatalf("a view-only transfer: %v", err)
	}
	if _, err := w.SweepAll(ctx, other); !isRPC(err, CodeNotSigned) {
		t.Fatalf("a view-only sweep: %v", err)
	}
	fw.mu.Lock()
	sent := 0
	for _, tx := range fw.txs {
		if tx.from == addr {
			sent++
		}
	}
	fw.mu.Unlock()
	if sent != 0 || !refusedBeforeSending(CodeNotSigned) {
		t.Fatalf("%d transactions left a view-only wallet; refusedBeforeSending(CodeNotSigned) = %v", sent, refusedBeforeSending(CodeNotSigned))
	}
	// the swap: a lock refused this way is not a lock
	r := newRun(t, plan{scenario: "happy", seed: 151})
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	driveUntil(r, "xmr_locking", 200)
	r.aliceW.Wallet = &refusingWallet{Wallet: r.aliceW.Wallet, err: &RPCError{Method: "transfer", Code: CodeNotSigned, Message: "unsigned"}}
	if _, err := r.alice.Step(r.ctx); err == nil || !strings.Contains(err.Error(), "nothing was sent") {
		t.Fatalf("the swap took an unsigned transfer for a lock: %v", err)
	}
	if a := r.loadState("alice"); a.Phase != "xmr_locking" || a.Xmr.LockUnknown || a.Xmr.LockTx != "" {
		t.Fatalf("alice %s, lockUnknown %v, lockTx %q", a.Phase, a.Xmr.LockUnknown, a.Xmr.LockTx)
	}
}

// TestAHashTheWalletDoesNotListIsNotASend: a transfer whose hash the wallet's own record (get_transfers out and
// pending) does not show is not taken as sent — and, since it might have been, it is not taken as refused either:
// the swap will not pay a second time.
func TestAHashTheWalletDoesNotListIsNotASend(t *testing.T) {
	ctx := context.Background()
	fw := NewFakeWallet(Stagenet)
	inner := httptest.NewServer(fw)
	defer inner.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
		}
		json.Unmarshal(body, &req)
		switch req.Method {
		case "transfer":
			w.Write([]byte(`{"jsonrpc":"2.0","id":"0","result":{"tx_hash":"` + strings.Repeat("ab", 32) + `"}}`))
			return
		case "get_transfers":
			w.Write([]byte(`{"jsonrpc":"2.0","id":"0","result":{}}`))
			return
		}
		resp, err := http.Post(inner.URL+"/json_rpc", "application/json", bytes.NewReader(body))
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer resp.Body.Close()
		io.Copy(w, resp.Body)
	}))
	defer srv.Close()
	spend, _ := NewScalar(rand.Reader)
	view, _ := NewScalar(rand.Reader)
	addr, _ := Address(spend.Public(), view.Public(), Stagenet)
	w := NewWalletRPC(srv.URL, "", "")
	if err := w.GenerateFromKeys(ctx, "me", addr, view.Hex(), spend.Hex(), "", 0); err != nil {
		t.Fatal(err)
	}
	_, err := w.Transfer(ctx, addr, 1)
	var rerr *RPCError
	if err == nil || !strings.Contains(err.Error(), "lists no outgoing transfer") || errors.As(err, &rerr) {
		t.Fatalf("an unlisted hash: %v", err)
	}
}

// TestMonerodIsNamedAfterEveryOpen (GAP-010 on the real server): set_daemon holds only for the wallet open when it is
// sent, so with -monerod the companion names it again after every open and every generate. Every wallet read and
// spend of a whole swap is then covered; without -monerod, the same swap makes many that are not.
func TestMonerodIsNamedAfterEveryOpen(t *testing.T) {
	run := func(monerod string) (unpinned, sets int, phase string) {
		r := newRun(t, plan{scenario: "happy", seed: 153})
		r.chain.flaky = nil
		with := func(c *Config) { c.Monerod = monerod }
		b := startSide(r, "bob", "bob.json", "channel", newMemDero(r.chain, bobAddr), r.w, with)
		a := startSide(r, "alice", "alice.json", "channel", newMemDero(r.chain, aliceAddr), r.w, with)
		before := r.fw.Unpinned()
		for i := 0; i < 600 && !(b.State().Done && a.State().Done); i++ {
			b.Step(r.ctx)
			a.Step(r.ctx)
			r.chain.Advance(1)
			r.fw.Mine(1)
			time.Sleep(time.Millisecond)
		}
		return r.fw.Unpinned() - before, r.fw.DaemonSets(), b.State().Phase
	}
	if unpinned, sets, phase := run("127.0.0.1:18081"); unpinned != 0 || sets == 0 || phase != "swept" {
		t.Fatalf("with -monerod: %d calls with no monerod named, %d set_daemon calls, bob %s", unpinned, sets, phase)
	}
	if unpinned, _, phase := run(""); unpinned == 0 || phase != "swept" {
		t.Fatalf("without -monerod the fake counted %d unpinned calls (bob %s): the test does not measure anything", unpinned, phase)
	}
}
