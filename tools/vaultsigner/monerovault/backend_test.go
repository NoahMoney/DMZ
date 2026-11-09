package monerovault

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	primary     = "55LTR8KniP4LQGJSPtbYDacR7dz8RBFnsfAKMaMuwUNYX6aQbBcovzDPyrQF9KXF9tVU6Xk3K8no1BywnJX6GvZX8yJsXvt"
	subaddr     = "7BnERTpvL5MbCLtj5n9No7J5oE5hHiB3tVCK5cjSvCsYWD2WRJLFuWeKTLiXo5QJqt2ZwUaLy2Vh1Ad51K7FNgqcHgjW85o"
	dest        = "55LTR8KniP4LQGJSPtbYDacR7dz8RBFnsfAKMaMuwUNYX6aQbBcovzDPyrQF9KXF9tVU6Xk3K8no1BywnJX6GvZX8yJsXvt"
	mainnetAddr = "44M2Xkd5z3ffTajB7y5e7yjmchoCMANgXa4ZP3FBU1TfM8YtenpowcyRpHKmi9mkcLc2cBP5TujtRJYPAzsbAA237Y78p4U"
)

type fakeWallet struct {
	mu       sync.Mutex
	calls    map[string]int
	incoming []Transfer
	outgoing []Transfer
	height   uint64
	created  bool
	byTx     *Transfer
	paid     string
	payment  string
	txset    string
}

func (f *fakeWallet) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Method string                 `json:"method"`
		Params map[string]interface{} `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	f.mu.Lock()
	f.calls[req.Method]++
	idx, _ := req.Params["address_index"].(float64)
	f.mu.Unlock()
	result := interface{}(nil)
	errObj := interface{}(nil)
	switch req.Method {
	case "get_height":
		f.mu.Lock()
		h := f.height
		f.mu.Unlock()
		result = map[string]interface{}{"height": h}
	case "get_version":
		result = map[string]interface{}{"version": 196610}
	case "get_balance":
		result = map[string]interface{}{"balance": 5000}
	case "get_address":
		if idx == 0 {
			result = map[string]interface{}{"address": primary}
		} else {
			f.mu.Lock()
			ok := f.created
			f.mu.Unlock()
			if !ok {
				errObj = map[string]interface{}{"code": -1, "message": "index out of range"}
			} else {
				result = map[string]interface{}{"address": subaddr}
			}
		}
	case "create_address":
		f.mu.Lock()
		f.created = true
		f.mu.Unlock()
		result = map[string]interface{}{"address": subaddr, "address_index": 1}
	case "get_transfers":
		f.mu.Lock()
		in, out := f.incoming, f.outgoing
		f.mu.Unlock()
		if v, _ := req.Params["in"].(bool); v {
			result = map[string]interface{}{"in": in}
		} else {
			result = map[string]interface{}{"out": out}
		}
	case "get_transfer_by_txid":
		f.mu.Lock()
		t := f.byTx
		f.mu.Unlock()
		if t == nil {
			errObj = map[string]interface{}{"code": -5, "message": "transaction not found"}
		} else {
			result = map[string]interface{}{"transfer": t}
		}
	case "transfer":
		f.mu.Lock()
		if f.txset != "" {
			txset := f.txset
			f.mu.Unlock()
			result = map[string]interface{}{"multisig_txset": txset}
			break
		}
		if p, ok := req.Params["payment_id"].(string); ok {
			f.payment = p
		}
		f.paid = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		f.outgoing = []Transfer{{TxID: f.paid, Height: 900, PaymentID: f.payment, Destinations: []Dest{{Address: dest, Amount: 100}}}}
		pay := f.paid
		f.mu.Unlock()
		result = map[string]interface{}{"tx_hash": pay}
	default:
		errObj = map[string]interface{}{"code": -32601, "message": "method not found"}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": "0", "result": result, "error": errObj})
}

func TestReleaseRefusesAMultisigTxset(t *testing.T) {
	f := &fakeWallet{calls: map[string]int{}, height: 1000, txset: "unsigned-multisig-txset"}
	b, _ := newTestBackend(t, f)
	if _, err := b.Release(7, dest, 100); err != ErrNeedsSignatures {
		t.Fatalf("Release error = %v, want ErrNeedsSignatures", err)
	}
	if f.calls["transfer"] != 1 {
		t.Fatalf("transfer calls = %d, want 1", f.calls["transfer"])
	}
}

func TestVerifyReReadsTheDeposit(t *testing.T) {
	tx := strings.Repeat("22", 32)
	f := &fakeWallet{calls: map[string]int{}, height: 1000, incoming: []Transfer{{TxID: tx, Amount: 150, Height: 900, SubaddrIndex: Subaddr{Minor: 1}}}}
	b, _ := newTestBackend(t, f)
	ds, err := b.Deposits()
	if err != nil || len(ds) != 1 {
		t.Fatalf("deposits = %+v, %v", ds, err)
	}
	if ok, err := b.Verify(ds[0]); err != nil || !ok {
		t.Fatalf("Verify = %v, %v", ok, err)
	}
	f.mu.Lock()
	f.incoming[0].Amount = 149
	f.mu.Unlock()
	if ok, _ := b.Verify(ds[0]); ok {
		t.Fatal("a changed deposit verified")
	}
}

func newTestBackend(t *testing.T, f *fakeWallet) (*Backend, *httptest.Server) {
	t.Helper()
	s := httptest.NewServer(f)
	t.Cleanup(s.Close)
	b, err := New(Config{Network: "stagenet", WalletRPC: s.URL, Address: primary, FinalityDepth: 10, TimeoutSeconds: 5, Retries: 1, RetryDelayMS: 5}, strings.Repeat("ab", 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	return b, s
}

func TestDepositsGroupSubaddressOutputsAndIgnoreLocked(t *testing.T) {
	tx := strings.Repeat("11", 32)
	f := &fakeWallet{calls: map[string]int{}, height: 1000, incoming: []Transfer{
		{TxID: tx, Amount: 100, Height: 900, SubaddrIndex: Subaddr{Minor: 1}},
		{TxID: tx, Amount: 50, Height: 900, SubaddrIndex: Subaddr{Minor: 1}},
		{TxID: tx, Amount: 70, Height: 900, Locked: true, SubaddrIndex: Subaddr{Minor: 2}},
		{TxID: tx, Amount: 80, Height: 900, UnlockTime: 99, SubaddrIndex: Subaddr{Minor: 1}},
	}}
	b, _ := newTestBackend(t, f)
	ds, err := b.Deposits()
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 1 || ds[0].TxID != tx || ds[0].Vout != 1 || ds[0].Index != 1 || ds[0].Amount != 150 || ds[0].Height != 900 {
		t.Fatalf("deposits = %+v", ds)
	}
}

func TestAddressCreatesAndThenReadsTheSubaddress(t *testing.T) {
	f := &fakeWallet{calls: map[string]int{}, height: 1000}
	b, _ := newTestBackend(t, f)
	got, err := b.Address(1)
	if err != nil || got != subaddr {
		t.Fatalf("Address(1) = %q, %v", got, err)
	}
	if _, err := b.Address(1); err != nil {
		t.Fatal(err)
	}
	if f.calls["create_address"] != 1 {
		t.Fatalf("create_address calls = %d, want 1 (the cached address must be reused)", f.calls["create_address"])
	}
}

func TestReleaseIsIdempotentByPaymentID(t *testing.T) {
	f := &fakeWallet{calls: map[string]int{}, height: 1000}
	b, _ := newTestBackend(t, f)
	txid, err := b.Release(7, dest, 100)
	if err != nil {
		t.Fatal(err)
	}
	if f.payment != b.paymentID(7) {
		t.Fatalf("payment id = %q, want %q", f.payment, b.paymentID(7))
	}
	again, err := b.Release(7, dest, 100)
	if err != nil || again != txid {
		t.Fatalf("second Release = %q, %v; want %q", again, err, txid)
	}
	if f.calls["transfer"] != 1 {
		t.Fatalf("transfer calls = %d, want 1", f.calls["transfer"])
	}
}

func TestVerifyReleaseChecksFinalityDestinationAndAmount(t *testing.T) {
	f := &fakeWallet{calls: map[string]int{}, height: 1000}
	b, _ := newTestBackend(t, f)
	f.byTx = &Transfer{TxID: strings.Repeat("aa", 32), Height: 900, PaymentID: b.paymentID(7), Destinations: []Dest{{Address: dest, Amount: 100}}}
	ok, err := b.VerifyRelease(7, f.byTx.TxID, dest, 100)
	if err != nil || !ok {
		t.Fatalf("VerifyRelease = %v, %v", ok, err)
	}
	if ok, _ := b.VerifyRelease(7, f.byTx.TxID, dest, 99); ok {
		t.Fatal("a different amount was accepted")
	}
	f.byTx.Height = 995
	if _, err := b.VerifyRelease(7, f.byTx.TxID, dest, 100); err == nil {
		t.Fatal("a payment above the finality depth was accepted")
	}
}

func TestClientRetriesTransientReads(t *testing.T) {
	fails := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fails < 2 {
			fails++
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": "0", "result": map[string]interface{}{"height": 1000}})
	}))
	defer s.Close()
	c, err := NewClient(s.URL, "", "", 2, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Height(t.Context())
	if err != nil || h != 1000 {
		t.Fatalf("Height = %d, %v", h, err)
	}
	if fails != 2 {
		t.Fatalf("failures = %d, want 2", fails)
	}
}

func TestClientAnswersADigestChallenge(t *testing.T) {
	var auth string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if auth == "" {
			w.Header().Set("WWW-Authenticate", `Digest realm="monero", nonce="abc", qop="auth", algorithm=MD5`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": "0", "result": map[string]interface{}{"height": 1000}})
	}))
	defer s.Close()
	c, err := NewClient(s.URL, "user", "pass", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Height(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !c.Challenged() || !strings.Contains(auth, `username="user"`) || !strings.Contains(auth, `response="`) {
		t.Fatalf("the wallet was not answered with a Digest authorization: %q", auth)
	}
}

func TestValidDestIsNetworkChecked(t *testing.T) {
	f := &fakeWallet{calls: map[string]int{}, height: 1000}
	b, _ := newTestBackend(t, f)
	if !b.ValidDest(dest) {
		t.Fatal("a mainnet address was refused")
	}
	if b.ValidDest(mainnetAddr) {
		t.Fatal("a mainnet address was accepted for a stagenet vault")
	}
}

func TestConfigNeedsThePrimaryAddress(t *testing.T) {
	_, err := New(Config{Network: "stagenet", WalletRPC: "127.0.0.1:18083", Address: subaddr, FinalityDepth: 10}, strings.Repeat("ab", 32), nil)
	if err == nil || !strings.Contains(err.Error(), "primary address") {
		t.Fatalf("a subaddress was accepted as the vault address: %v", err)
	}
	_, err = New(Config{Network: "mainnet", WalletRPC: "127.0.0.1:18083", Address: mainnetAddr, FinalityDepth: 9}, strings.Repeat("ab", 32), nil)
	if err == nil || !strings.Contains(err.Error(), "finality") {
		t.Fatalf("a finality depth below 10 was accepted: %v", err)
	}
}
