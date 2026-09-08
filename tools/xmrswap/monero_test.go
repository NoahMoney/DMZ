package xmrswap

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// recorder answers each method with a canned result and keeps the request bodies it saw.
type recorder struct {
	t       *testing.T
	results map[string]string // method -> raw JSON result (or a whole {"error":...} envelope body)
	bodies  []map[string]interface{}
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/json_rpc" || req.Header.Get("Content-Type") != "application/json" {
		r.t.Errorf("request to %s with content type %q", req.URL.Path, req.Header.Get("Content-Type"))
	}
	var body map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		r.t.Errorf("body: %v", err)
	}
	r.bodies = append(r.bodies, body)
	method, _ := body["method"].(string)
	res, ok := r.results[method]
	if !ok {
		r.t.Errorf("unexpected method %q", method)
		res = "{}"
	}
	w.Header().Set("Content-Type", "application/json")
	if strings.HasPrefix(res, `{"error"`) {
		w.Write([]byte(`{"id":"0","jsonrpc":"2.0",` + res[1:]))
		return
	}
	w.Write([]byte(`{"id":"0","jsonrpc":"2.0","result":` + res + `}`))
}

func (r *recorder) last() map[string]interface{} { return r.bodies[len(r.bodies)-1] }

// jsonEqual compares two JSON documents structurally, so field order and number spelling do not matter.
func jsonEqual(t *testing.T, got map[string]interface{}, want string) {
	t.Helper()
	var w map[string]interface{}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("bad want %s: %v", want, err)
	}
	if !reflect.DeepEqual(got, w) {
		g, _ := json.Marshal(got)
		t.Errorf("request body\n got %s\nwant %s", g, want)
	}
}

// Every response below is the published reference's own example (https://docs.getmonero.org/rpc-library/wallet-rpc/)
// where it gives one, and the KV_SERIALIZE field names of wallet_rpc_server_commands_defs.h where it does not.
func TestWalletRPCRequestShapes(t *testing.T) {
	const stagenetAddr = "55LTR8KniP4LQGJSPtbYDacR7dz8RBFnsfAKMaMuwUNYX6aQbBcovzDPyrQF9KXF9tVU6Xk3K8no1BywnJX6GvZX8yJsXvt"
	const inExample = `{"in":[{"address":"77Vx9cs1VPicFndSVgYUvTdLCJEZw9h81hXLMYsjBCXSJfUehLa9TDW3Ffh45SQa7xb6dUs18mpNxfUhQGqfwXPSMrvKhVp",` +
		`"amount":200000000000,"amounts":[200000000000],"confirmations":1,"double_spend_seen":false,"fee":21650200000,` +
		`"height":153624,"locked":false,"note":"","payment_id":"0000000000000000","subaddr_index":{"major":1,"minor":0},` +
		`"subaddr_indices":[{"major":1,"minor":0}],"suggested_confirmations_threshold":1,"timestamp":1535918400,` +
		`"txid":"c36258a276018c3a4bc1f195a7fb530f50cd63a4fa765fb7c6f7f49fc051762a","type":"in","unlock_time":0}]}`
	rec := &recorder{t: t, results: map[string]string{
		"get_height":         `{"height":145545}`,
		"get_address":        `{"address":"` + stagenetAddr + `","addresses":[{"address":"` + stagenetAddr + `","address_index":0,"label":"Primary account","used":true}]}`,
		"transfer":           `{"amount":300000000000,"fee":1000000000,"multisig_txset":"","tx_blob":"","tx_hash":"7663438de4f72b25a0e395b770ea9ecf7108cd2f0c4b75be0b14a103d3362be9","tx_key":"","tx_metadata":"","unsigned_txset":""}`,
		"generate_from_keys": `{"address":"42gt8cXJSHAL4up8XoZh7fikVuswDU7itAoaCjSQyo6fFoeTQpAcAwrQ1cs8KvFynLFSBdabhmk7HEe3HS7UsAz4LYnVPYM","info":"Wallet has been generated successfully."}`,
		"open_wallet":        `{}`,
		"close_wallet":       `{}`,
		"get_transfers":      inExample,
		"get_balance":        `{"balance":157443303037455077,"blocks_to_unlock":59,"multisig_import_needed":false,"per_subaddress":[{"account_index":0,"address":"` + stagenetAddr + `","address_index":0,"balance":157360317826255077,"blocks_to_unlock":0,"label":"Primary account","num_unspent_outputs":5281,"time_to_unlock":0,"unlocked_balance":157360317826255077}],"time_to_unlock":0,"unlocked_balance":157360317826255077}`,
		"sweep_all":          `{"tx_hash_list":["c1c2c3"],"amount_list":[1],"fee_list":[2]}`,
	}}
	srv := httptest.NewServer(rec)
	defer srv.Close()
	w := NewWalletRPC(srv.URL, "", "")
	ctx := context.Background()

	h, err := w.Height(ctx)
	if err != nil || h != 145545 {
		t.Fatalf("Height: %d, %v", h, err)
	}
	jsonEqual(t, rec.last(), `{"jsonrpc":"2.0","id":"0","method":"get_height"}`)

	a, err := w.Address(ctx)
	if err != nil || a != stagenetAddr {
		t.Fatalf("Address: %q, %v", a, err)
	}
	jsonEqual(t, rec.last(), `{"jsonrpc":"2.0","id":"0","method":"get_address","params":{"account_index":0}}`)

	txid, err := w.Transfer(ctx, stagenetAddr, 300000000000)
	if err != nil || txid != "7663438de4f72b25a0e395b770ea9ecf7108cd2f0c4b75be0b14a103d3362be9" {
		t.Fatalf("Transfer: %q, %v", txid, err)
	}
	jsonEqual(t, rec.last(), `{"jsonrpc":"2.0","id":"0","method":"transfer","params":{"destinations":[{"amount":300000000000,"address":"`+stagenetAddr+`"}],"account_index":0}}`)

	const mainAddr = "42gt8cXJSHAL4up8XoZh7fikVuswDU7itAoaCjSQyo6fFoeTQpAcAwrQ1cs8KvFynLFSBdabhmk7HEe3HS7UsAz4LYnVPYM"
	if err := w.GenerateFromKeys(ctx, "wallet_name", mainAddr, "97cf64f2cd6c930242e9bed5f14f8f16a33047229aca3eababf4af7e8d113209", "", 1000); err != nil {
		t.Fatalf("GenerateFromKeys view-only: %v", err)
	}
	jsonEqual(t, rec.last(), `{"jsonrpc":"2.0","id":"0","method":"generate_from_keys","params":{"restore_height":1000,"filename":"wallet_name","address":"`+mainAddr+`","viewkey":"97cf64f2cd6c930242e9bed5f14f8f16a33047229aca3eababf4af7e8d113209","password":"","autosave_current":true}}`)
	if err := w.GenerateFromKeys(ctx, "wallet_name", mainAddr, "97cf64f2cd6c930242e9bed5f14f8f16a33047229aca3eababf4af7e8d113209", "11d3fd247672c4cb29b6e38791dcf07629cd2d68d868f0b78811ce584a6b0d01", 0); err != nil {
		t.Fatalf("GenerateFromKeys full: %v", err)
	}
	if p := rec.last()["params"].(map[string]interface{}); p["spendkey"] != "11d3fd247672c4cb29b6e38791dcf07629cd2d68d868f0b78811ce584a6b0d01" || p["restore_height"] != float64(0) {
		t.Errorf("full wallet params: %v", p)
	}
	if err := w.GenerateFromKeys(ctx, "w", "9tTLtauaEKSj7xoVXytVH32R1pLZBk4VV4mZFGEh4wkXhDWqw1soPyf3fGixf1kni31VznEZkWNEza9d5TvjWwq5PaohYHC", "97cf64f2cd6c930242e9bed5f14f8f16a33047229aca3eababf4af7e8d113209", "", 0); err == nil {
		t.Error("a wallet echoing a different address was accepted")
	}

	if err := w.OpenWallet(ctx, "wallet_name"); err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, rec.last(), `{"jsonrpc":"2.0","id":"0","method":"open_wallet","params":{"filename":"wallet_name","password":""}}`)
	if err := w.CloseWallet(ctx); err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, rec.last(), `{"jsonrpc":"2.0","id":"0","method":"close_wallet","params":{"autosave_current":true}}`)

	in, err := w.Incoming(ctx, 153624)
	if err != nil {
		t.Fatal(err)
	}
	want := []Incoming{{TxID: "c36258a276018c3a4bc1f195a7fb530f50cd63a4fa765fb7c6f7f49fc051762a", Amount: 200000000000, Height: 153624, Confirmations: 1, Unlocked: true}}
	if !reflect.DeepEqual(in, want) {
		t.Errorf("Incoming: %+v", in)
	}
	// min_height is exclusive in wallet2::get_payments, so 153624 is asked for as 153623
	jsonEqual(t, rec.last(), `{"jsonrpc":"2.0","id":"0","method":"get_transfers","params":{"in":true,"filter_by_height":true,"min_height":153623,"account_index":0}}`)
	if _, err := w.Incoming(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if p := rec.last()["params"].(map[string]interface{}); p["min_height"] != float64(0) {
		t.Errorf("Incoming(0) sent min_height %v", p["min_height"])
	}
	// a transfer the sender froze: the reference's entry with unlock_time set and locked true, as wallet2
	// reports one that is past the spendable age but not past its unlock_time
	rec.results["get_transfers"] = strings.Replace(strings.Replace(inExample, `"locked":false`, `"locked":true`, 1), `"unlock_time":0`, `"unlock_time":2000000`, 1)
	in, err = w.Incoming(ctx, 0)
	if err != nil || len(in) != 1 || in[0].Unlocked || in[0].UnlockTime != 2000000 {
		t.Errorf("Incoming with unlock_time: %+v %v", in, err)
	}

	bal, err := w.UnlockedBalance(ctx)
	if err != nil || bal != 157360317826255077 {
		t.Fatalf("UnlockedBalance: %d, %v", bal, err)
	}
	jsonEqual(t, rec.last(), `{"jsonrpc":"2.0","id":"0","method":"get_balance","params":{"account_index":0}}`)

	ids, err := w.SweepAll(ctx, stagenetAddr)
	if err != nil || !reflect.DeepEqual(ids, []string{"c1c2c3"}) {
		t.Fatalf("SweepAll: %v, %v", ids, err)
	}
	jsonEqual(t, rec.last(), `{"jsonrpc":"2.0","id":"0","method":"sweep_all","params":{"address":"`+stagenetAddr+`","account_index":0}}`)

	// the wallet's own refusal comes back with its code
	rec.results["get_address"] = `{"error":{"code":-13,"message":"No wallet file"}}`
	_, err = w.Address(ctx)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != -13 || rpcErr.Method != "get_address" {
		t.Errorf("rpc error: %v", err)
	}
	if !strings.Contains(err.Error(), "-13 No wallet file") {
		t.Errorf("rpc error text: %v", err)
	}
}

func TestWalletRPCEndpointSpellings(t *testing.T) {
	for _, in := range []string{"127.0.0.1:38083", "http://127.0.0.1:38083", "http://127.0.0.1:38083/", "http://127.0.0.1:38083/json_rpc"} {
		w := NewWalletRPC(in, "", "").(*walletRPC)
		if w.url != "http://127.0.0.1:38083/json_rpc" || w.uri != "/json_rpc" {
			t.Errorf("%q -> %q %q", in, w.url, w.uri)
		}
	}
	w := NewWalletRPC("https://host/wallet", "u", "p").(*walletRPC)
	if w.url != "https://host/wallet/json_rpc" || w.uri != "/wallet/json_rpc" {
		t.Errorf("prefixed path -> %q %q", w.url, w.uri)
	}
}

// One 401 for the first request, then credentials up front: the challenge is reused.
func TestWalletRPCDigestAuth(t *testing.T) {
	fw := NewFakeWallet(Stagenet)
	fw.RequireAuth("swap", "s3cret")
	var unauthorized int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		fw.ServeHTTP(rec, r)
		if rec.Code == http.StatusUnauthorized {
			atomic.AddInt32(&unauthorized, 1)
		}
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		w.Write(rec.Body.Bytes())
	}))
	defer srv.Close()
	ctx := context.Background()

	if _, err := NewWalletRPC(srv.URL, "", "").Height(ctx); err == nil || !strings.Contains(err.Error(), "requires --rpc-login") {
		t.Errorf("no credentials: %v", err)
	}
	if _, err := NewWalletRPC(srv.URL, "swap", "wrong").Height(ctx); err == nil || !strings.Contains(err.Error(), "authentication refused") {
		t.Errorf("wrong password: %v", err)
	}
	w := NewWalletRPC(srv.URL, "swap", "s3cret")
	atomic.StoreInt32(&unauthorized, 0)
	for i := 0; i < 3; i++ {
		if h, err := w.Height(ctx); err != nil || h != 1 {
			t.Fatalf("call %d: %d, %v", i, h, err)
		}
	}
	if n := atomic.LoadInt32(&unauthorized); n != 1 {
		t.Errorf("%d challenges for 3 calls, want 1", n)
	}
	// the parser, on epee's exact challenge text and on RFC 7616's example
	c, ok := parseDigestChallenge([]string{
		`Digest qop="auth",algorithm=MD5-sess,realm="monero-rpc",nonce="abc",stale=false`,
		`Digest qop="auth",algorithm=MD5,realm="monero-rpc",nonce="abc",stale=false`,
	})
	if !ok || c.algorithm != "MD5" || c.realm != "monero-rpc" || c.nonce != "abc" || c.qop != "auth" {
		t.Errorf("challenge: %+v %v", c, ok)
	}
	// RFC 7616 §3.9.1 (https://www.rfc-editor.org/rfc/rfc7616.html): user Mufasa, password "Circle of Life",
	// GET /dir/index.html, MD5, qop=auth, nc=00000001, the RFC's nonce and cnonce, response as printed there
	rfc := &digestChallenge{realm: "http-auth@example.org", nonce: "7ypf/xlj9XXwfDPEoM4URrv/xwf94BcCAzFZH4GiTo0v", qop: "auth", algorithm: "MD5"}
	if got := digestResponse("Mufasa", "Circle of Life", "GET", "/dir/index.html", rfc, "00000001", "f2/wE4q74E6zIJEtWaHKaf5wv/H5QzzpXusqGemxURZJ"); got != "8ca523f5e9506fed4657c9700eebdbec" {
		t.Errorf("RFC 7616 vector: %s", got)
	}
}

// The whole Monero leg against the fake, through the real client: Alice funds and pays the joint address,
// Bob's view-only wallet watches confirmations grow with Mine, the full wallet sweeps, and every refusal
// the reference makes comes back with the reference's code.
func TestFakeWalletSwapLeg(t *testing.T) {
	fw := NewFakeWallet(Stagenet)
	srv := httptest.NewServer(fw)
	defer srv.Close()
	w := NewWalletRPC(srv.URL, "", "")
	ctx := context.Background()

	newKeys := func() (spend, view Scalar, addr string) {
		spend, _ = NewScalar(rand.Reader)
		view, _ = NewScalar(rand.Reader)
		addr, _ = Address(spend.Public(), view.Public(), Stagenet)
		return
	}
	aliceSpend, aliceView, alice := newKeys()
	bobSpend, bobView, bob := newKeys()

	if _, err := w.Address(ctx); !isRPC(err, -13) {
		t.Fatalf("no wallet open: %v", err)
	}
	if err := w.GenerateFromKeys(ctx, "alice", alice, aliceView.Hex(), aliceSpend.Hex(), 0); err != nil {
		t.Fatal(err)
	}
	if a, err := w.Address(ctx); err != nil || a != alice {
		t.Fatalf("Address: %q %v", a, err)
	}
	fw.Fund(alice, 5_000_000_000_000)
	if in, err := w.Incoming(ctx, 0); err != nil || len(in) != 0 {
		t.Fatalf("pool credit visible as incoming: %+v %v", in, err)
	}
	fw.Mine(1)
	in, err := w.Incoming(ctx, 0)
	if err != nil || len(in) != 1 || in[0].Confirmations != 1 || in[0].Unlocked || in[0].Height != 1 || in[0].Amount != 5_000_000_000_000 {
		t.Fatalf("after one block: %+v %v", in, err)
	}
	// the joint address
	sa, _ := NewScalar(rand.Reader)
	sb, _ := NewScalar(rand.Reader)
	v, _ := NewScalar(rand.Reader)
	P, err := AddPoints(sa.Public(), sb.Public())
	if err != nil {
		t.Fatal(err)
	}
	joint, err := Address(P, v.Public(), Stagenet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Transfer(ctx, joint, 1_000_000_000_000); !isRPC(err, -37) {
		t.Fatalf("transfer from a locked balance: %v", err)
	}
	fw.Mine(9)
	if in, _ := w.Incoming(ctx, 0); in[0].Confirmations != 10 || !in[0].Unlocked {
		t.Fatalf("after ten blocks: %+v", in)
	}
	if _, err := w.Transfer(ctx, joint, 6_000_000_000_000); !isRPC(err, -17) {
		t.Fatalf("transfer above the balance: %v", err)
	}
	mainnetJoint, _ := Address(P, v.Public(), Mainnet)
	if _, err := w.Transfer(ctx, mainnetJoint, 1); !isRPC(err, -2) {
		t.Fatalf("transfer to another network: %v", err)
	}
	lockHeight, _ := w.Height(ctx)
	txid, err := w.Transfer(ctx, joint, 1_000_000_000_000)
	if err != nil {
		t.Fatal(err)
	}

	// Bob watches through a view-only wallet made from the joint address and the shared view key
	if err := w.GenerateFromKeys(ctx, "bob-view", joint, v.Hex(), "", lockHeight); err != nil {
		t.Fatal(err)
	}
	if in, err := w.Incoming(ctx, lockHeight); err != nil || len(in) != 0 {
		t.Fatalf("unmined lock visible: %+v %v", in, err)
	}
	fw.Mine(1)
	in, err = w.Incoming(ctx, lockHeight)
	if err != nil || len(in) != 1 || in[0].TxID != txid || in[0].Amount != 1_000_000_000_000 || in[0].Confirmations != 1 || in[0].Unlocked || in[0].Height != lockHeight {
		t.Fatalf("lock after one block: %+v %v", in, err)
	}
	for i := 2; i <= 10; i++ {
		fw.Mine(1)
		in, _ = w.Incoming(ctx, lockHeight)
		if in[0].Confirmations != uint64(i) || in[0].Unlocked != (i >= 10) {
			t.Fatalf("block %d: %+v", i, in)
		}
	}
	// minHeight is inclusive at the client, whatever the wire says
	if in, _ := w.Incoming(ctx, lockHeight+1); len(in) != 0 {
		t.Errorf("Incoming(lockHeight+1) returned the lock: %+v", in)
	}
	if _, err := w.SweepAll(ctx, bob); !isRPC(err, -29) {
		t.Fatalf("sweep from a view-only wallet: %v", err)
	}
	if err := w.GenerateFromKeys(ctx, "bob-view", joint, v.Hex(), "", 0); !isRPC(err, -1) || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate wallet name: %v", err)
	}
	if err := w.GenerateFromKeys(ctx, "late", joint, v.Hex(), "", lockHeight+1); err != nil {
		t.Fatal(err)
	}
	if in, _ := w.Incoming(ctx, 0); len(in) != 0 {
		t.Errorf("a wallet restored above the lock sees it: %+v", in)
	}

	// the full wallet, with s_a + s_b, and the sweep
	wrongView, _ := NewScalar(rand.Reader)
	if err := w.GenerateFromKeys(ctx, "bad-view", joint, wrongView.Hex(), "", 0); err == nil || !strings.Contains(err.Error(), "view secret key does not match") {
		t.Fatalf("wrong view key: %v", err)
	}
	if err := w.GenerateFromKeys(ctx, "bad-spend", joint, v.Hex(), sa.Hex(), 0); err == nil || !strings.Contains(err.Error(), "spend secret key does not match") {
		t.Fatalf("one share as the spend key: %v", err)
	}
	if err := w.GenerateFromKeys(ctx, "bad-net", mainnetJoint, v.Hex(), "", 0); err == nil || !strings.Contains(err.Error(), "Failed to parse public address") {
		t.Fatalf("mainnet address on stagenet: %v", err)
	}
	if err := w.GenerateFromKeys(ctx, "bob-full", joint, v.Hex(), AddScalars(sa, sb).Hex(), lockHeight); err != nil {
		t.Fatal(err)
	}
	ids, err := w.SweepAll(ctx, bob)
	if err != nil || len(ids) != 1 {
		t.Fatalf("SweepAll: %v %v", ids, err)
	}
	if _, err := w.SweepAll(ctx, bob); !isRPC(err, -37) {
		t.Fatalf("second sweep: %v", err)
	}
	fw.Mine(1)
	if err := w.GenerateFromKeys(ctx, "bob", bob, bobView.Hex(), bobSpend.Hex(), 0); err != nil {
		t.Fatal(err)
	}
	in, err = w.Incoming(ctx, 0)
	if err != nil || len(in) != 1 || in[0].TxID != ids[0] || in[0].Amount != 1_000_000_000_000-FakeFee {
		t.Fatalf("Bob's wallet after the sweep: %+v %v", in, err)
	}
	// Alice's balance: the fund, less the lock, less its fee
	if err := w.OpenWallet(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	fw.Mine(10)
	if _, err := w.Transfer(ctx, bob, 5_000_000_000_000-1_000_000_000_000-FakeFee-FakeFee); err != nil {
		t.Fatalf("Alice spending her exact remainder: %v", err)
	}
	if _, err := w.Transfer(ctx, bob, 1); !isRPC(err, -17) {
		t.Fatalf("Alice spending past her remainder: %v", err)
	}
	if err := w.OpenWallet(ctx, "nobody"); !isRPC(err, -1) {
		t.Fatalf("open a missing wallet: %v", err)
	}
	if err := w.CloseWallet(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.CloseWallet(ctx); !isRPC(err, -13) {
		t.Fatalf("close with nothing open: %v", err)
	}
	if h, err := w.Height(ctx); err != nil || h != fw.Height() {
		t.Fatalf("Height with nothing open: %d %v", h, err)
	}
}

// TestFakeWalletLockedAndBurntOutputs: the two shapes of a lock that confirms but cannot be spent (review R6
// and R5d), as the recipient's view-only wallet reports them. A transfer under an unlock_time is listed with it,
// stays locked past the spendable age, is outside the unlocked balance and cannot be swept until it passes;
// a burnt transfer is listed at its full amount and is never in any balance.
func TestFakeWalletLockedAndBurntOutputs(t *testing.T) {
	fw := NewFakeWallet(Stagenet)
	srv := httptest.NewServer(fw)
	defer srv.Close()
	w := NewWalletRPC(srv.URL, "", "")
	ctx := context.Background()
	spend, _ := NewScalar(rand.Reader)
	view, _ := NewScalar(rand.Reader)
	alice, _ := Address(spend.Public(), view.Public(), Stagenet)
	if err := w.GenerateFromKeys(ctx, "alice", alice, view.Hex(), spend.Hex(), 0); err != nil {
		t.Fatal(err)
	}
	fw.Fund(alice, 5_000_000_000_000)
	fw.Mine(11)
	jSpend, _ := NewScalar(rand.Reader)
	jView, _ := NewScalar(rand.Reader)
	joint, _ := Address(jSpend.Public(), jView.Public(), Stagenet)

	// the request's own unlock_time, then the wallet-wide one, then a burnt output
	fw.SetUnlockTime(fw.Height() + 30)
	lockedTx, err := w.Transfer(ctx, joint, 1_000_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	fw.SetUnlockTime(0)
	fw.BurnNextTransfer()
	burntTx, err := w.Transfer(ctx, joint, 1_000_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	goodTx, err := w.Transfer(ctx, joint, 1_000_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	fw.Mine(1)
	if err := w.GenerateFromKeys(ctx, "view", joint, jView.Hex(), "", 0); err != nil {
		t.Fatal(err)
	}
	fw.Mine(10)
	in, err := w.Incoming(ctx, 0)
	if err != nil || len(in) != 3 {
		t.Fatalf("Incoming: %+v %v", in, err)
	}
	byID := map[string]Incoming{}
	for _, e := range in {
		byID[e.TxID] = e
	}
	if e := byID[lockedTx]; e.Unlocked || e.UnlockTime == 0 || e.Confirmations != 11 {
		t.Errorf("the unlock_time transfer: %+v", e)
	}
	if e := byID[burntTx]; !e.Unlocked || e.UnlockTime != 0 || e.Amount != 1_000_000_000_000 {
		t.Errorf("the burnt transfer is not listed as an ordinary one: %+v", e)
	}
	if e := byID[goodTx]; !e.Unlocked || e.UnlockTime != 0 {
		t.Errorf("the plain transfer: %+v", e)
	}
	// three confirmed transfers of 1 XMR, one spendable
	if bal, err := w.UnlockedBalance(ctx); err != nil || bal != 1_000_000_000_000 {
		t.Fatalf("UnlockedBalance: %d %v", bal, err)
	}
	// the sweep takes the spendable one only; the frozen one waits for its unlock_time and then sweeps too
	if err := w.GenerateFromKeys(ctx, "full", joint, jView.Hex(), jSpend.Hex(), 0); err != nil {
		t.Fatal(err)
	}
	if ids, err := w.SweepAll(ctx, alice); err != nil || len(ids) != 1 {
		t.Fatalf("sweep with one spendable output: %v %v", ids, err)
	}
	fw.Mine(1)
	if _, err := w.SweepAll(ctx, alice); !isRPC(err, -37) {
		t.Fatalf("sweep of a frozen and a burnt output: %v", err)
	}
	fw.Mine(30)
	if in, _ := w.Incoming(ctx, 0); !byIDOf(in, lockedTx).Unlocked {
		t.Errorf("past its unlock_time the transfer is still locked: %+v", in)
	}
	if ids, err := w.SweepAll(ctx, alice); err != nil || len(ids) != 1 {
		t.Fatalf("sweep after the unlock_time: %v %v", ids, err)
	}
	// a unix-time unlock_time is a time, compared with the fake's block timestamps
	if err := w.OpenWallet(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	fw.Mine(10)
	fw.SetUnlockTime(1700000000 + 120*(fw.Height()+20))
	timedTx, err := w.Transfer(ctx, joint, 500_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	fw.Mine(11)
	if err := w.OpenWallet(ctx, "view"); err != nil {
		t.Fatal(err)
	}
	if e := byIDOf(mustIncoming(t, w, ctx), timedTx); e.Unlocked || e.UnlockTime < unlockTimeIsTime || e.Confirmations != 11 {
		t.Errorf("time-locked transfer: %+v", e)
	}
	fw.Mine(10)
	if e := byIDOf(mustIncoming(t, w, ctx), timedTx); !e.Unlocked {
		t.Errorf("time-locked transfer past its time: %+v", e)
	}
}

func byIDOf(in []Incoming, id string) Incoming {
	for _, e := range in {
		if e.TxID == id {
			return e
		}
	}
	return Incoming{}
}

func mustIncoming(t *testing.T, w Wallet, ctx context.Context) []Incoming {
	t.Helper()
	in, err := w.Incoming(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func isRPC(err error, code int) bool {
	var e *RPCError
	return errors.As(err, &e) && e.Code == code
}

func TestFakeWalletUnknownMethodAndPaths(t *testing.T) {
	fw := NewFakeWallet(Testnet)
	srv := httptest.NewServer(fw)
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/json_rpc", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":"7","method":"get_version"}`))
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		ID    string `json:"id"`
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&env)
	if env.ID != "7" || env.Error.Code != -32601 {
		t.Errorf("unknown method: %+v", env)
	}
	if resp, _ := http.Get(srv.URL + "/json_rpc"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET: %d", resp.StatusCode)
	}
	if resp, _ := http.Post(srv.URL+"/elsewhere", "application/json", strings.NewReader("{}")); resp.StatusCode != http.StatusNotFound {
		t.Errorf("other path: %d", resp.StatusCode)
	}
}

func TestWalletRPCContext(t *testing.T) {
	// The handler drains the body first: net/http only watches for the client going away once the body has
	// been read, and a handler that never reads it never sees the cancellation (and Close waits for it).
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := NewWalletRPC(srv.URL, "", "").Height(ctx); err == nil {
		t.Error("no error from a cancelled context")
	}
	if time.Since(start) > 5*time.Second {
		t.Error("the call outlived its context")
	}
}
