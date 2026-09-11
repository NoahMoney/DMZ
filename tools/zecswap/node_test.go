package zecswap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The node client against the fake node, whose answers are shaped as zcashd documents them.

func TestNodeReadsDocumentedShapes(t *testing.T) {
	ctx := context.Background()
	f := newFakeZcash(t, "test", startZec)
	c := f.client()
	ci, err := c.ChainInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ci.Chain != "test" || ci.Net != Testnet || ci.Height != startZec || ci.NextBranch != BranchNU63 || ci.TipBranch != BranchNU63 {
		t.Fatalf("chain info %+v", ci)
	}
	if h, err := c.BlockHash(ctx, startZec); err != nil || h != ci.BestHash {
		t.Fatalf("getblockhash %s %v, best %s", h, err, ci.BestHash)
	}
	key := testKey(takerKeyN)
	txid := f.Pay(P2PKHScript(key.PubKeyHash()), 123_456)
	us, err := c.AddressUTXOs(ctx, key.Address(Testnet))
	if err != nil || len(us) != 1 || us[0].TxID != txid || us[0].Value != 123_456 || us[0].Height != startZec+1 || us[0].Index != 0 {
		t.Fatalf("getaddressutxos %+v %v", us, err)
	}
	rt, err := c.RawTx(ctx, txid)
	if err != nil || !rt.Mined || rt.Height != startZec+1 || rt.Confirmations != 1 || !rt.Verified {
		t.Fatalf("getrawtransaction %+v %v", rt, err)
	}
	if out, err := c.TxOut(ctx, txid, 0); err != nil || out == nil || out.Confirmations != 1 {
		t.Fatalf("gettxout of an unspent output: %+v %v", out, err)
	}
	if out, err := c.TxOut(ctx, txid, 1); err != nil || out != nil {
		t.Fatalf("gettxout of an output that does not exist: %+v %v", out, err)
	}
	if ids, err := c.AddressTxIDs(ctx, key.Address(Testnet), startZec, startZec+1); err != nil || len(ids) != 1 || ids[0] != txid {
		t.Fatalf("getaddresstxids %v %v", ids, err)
	}
	if _, err := c.RawTx(ctx, strings.Repeat("ab", 32)); !errors.Is(err, ErrTxNotFound) {
		t.Fatalf("an unknown transaction: %v", err)
	}
}

// Every field is checked for shape and range before anything reads it; a malformed, contradictory or oversized
// answer is an error, never a guess.
func TestNodeRefusesMalformedAnswers(t *testing.T) {
	ctx := context.Background()
	body := func(result string) []byte {
		return []byte(`{"result":` + result + `,"error":null,"id":"zecswap"}`)
	}
	info := func(chain, blocks, best, consensus string) string {
		return fmt.Sprintf(`{"chain":%q,"blocks":%s,"bestblockhash":%q,"estimatedheight":%s,"upgrades":{},"consensus":%s}`, chain, blocks, best, blocks, consensus)
	}
	good := strings.Repeat("a", 64)
	cons := `{"chaintip":"37a5165b","nextblock":"37a5165b"}`
	other := strings.Repeat("cd", 32)
	for _, c := range []struct {
		name, method string
		body         []byte
		call         func(z *ZcashRPC) error
	}{
		{"an unknown chain", "getblockchaininfo", body(info("banana", "4200000", good, cons)), nil},
		{"a negative height", "getblockchaininfo", body(info("test", "-5", good, cons)), nil},
		{"a fractional height", "getblockchaininfo", body(info("test", "4200000.5", good, cons)), nil},
		{"a height past LOCKTIME_THRESHOLD", "getblockchaininfo", body(info("test", "600000000", good, cons)), nil},
		{"a short best hash", "getblockchaininfo", body(info("test", "4200000", "abcd", cons)), nil},
		{"no consensus object", "getblockchaininfo", body(`{"chain":"test","blocks":4200000,"bestblockhash":"` + good + `"}`), nil},
		{"a branch id that is not hex", "getblockchaininfo", body(info("test", "4200000", good, `{"chaintip":"37a5165b","nextblock":"xyz"}`)), nil},
		{"a UTXO above the supply", "getaddressutxos", body(`[{"address":"ADDR","txid":"` + other + `","outputIndex":0,"script":"76a9","satoshis":3000000000000000,"height":5}]`), nil},
		{"a UTXO with a negative index", "getaddressutxos", body(`[{"address":"ADDR","txid":"` + other + `","outputIndex":-1,"script":"76a9","satoshis":1,"height":5}]`), nil},
		{"a UTXO of another address", "getaddressutxos", body(`[{"address":"tmSomeoneElse","txid":"` + other + `","outputIndex":0,"script":"76a9","satoshis":1,"height":5}]`), nil},
		{"a UTXO at height 0", "getaddressutxos", body(`[{"address":"ADDR","txid":"` + other + `","outputIndex":0,"script":"76a9","satoshis":1,"height":0}]`), nil},
		{"a UTXO script that is not hex", "getaddressutxos", body(`[{"address":"ADDR","txid":"` + other + `","outputIndex":0,"script":"zz","satoshis":1,"height":5}]`), nil},
		{"another transaction's bytes", "getrawtransaction", nil, nil},
		{"hex that is not hex", "getrawtransaction", body(`{"hex":"zz","txid":"` + other + `"}`), nil},
		{"a height without confirmations", "getrawtransaction", body(`{"hex":"RAW","txid":"TXID","height":5,"blockhash":"` + good + `"}`), nil},
		{"not JSON-RPC at all", "getblockhash", []byte("<html>proxy error</html>"), nil},
		{"a txid list past the cap", "getaddresstxids", body(`["` + strings.Repeat(other+`","`, maxTxIDs) + other + `"]`), nil},
		{"a gettxout script that is not hex", "gettxout", body(`{"bestblock":"` + good + `","confirmations":3,"scriptPubKey":{"hex":"qq"}}`), nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeZcash(t, "test", startZec)
			key := testKey(takerKeyN)
			addr := key.Address(Testnet)
			txid := f.Pay(P2PKHScript(key.PubKeyHash()), 1000)
			f.Pay(P2PKHScript(key.PubKeyHash()), 2000)
			f.mu.Lock()
			var otherRaw string
			for id, z := range f.txs {
				if id != txid {
					otherRaw = fmt.Sprintf("%x", z.raw)
				}
			}
			ownRaw := fmt.Sprintf("%x", f.txs[txid].raw)
			f.mu.Unlock()
			b := c.body
			switch c.name {
			case "another transaction's bytes":
				b = body(`{"hex":"` + otherRaw + `","txid":"` + txid + `"}`)
			case "a height without confirmations":
				b = []byte(strings.Replace(strings.Replace(string(b), "RAW", ownRaw, 1), "TXID", txid, 1))
			}
			b = []byte(strings.ReplaceAll(string(b), "ADDR", addr))
			f.override = func(method string) ([]byte, int, bool) { return b, http.StatusOK, method == c.method }
			z := f.client()
			var err error
			switch c.method {
			case "getblockchaininfo":
				_, err = z.ChainInfo(ctx)
			case "getaddressutxos":
				_, err = z.AddressUTXOs(ctx, addr)
			case "getrawtransaction":
				_, err = z.RawTx(ctx, txid)
			case "getblockhash":
				_, err = z.BlockHash(ctx, startZec)
			case "getaddresstxids":
				_, err = z.AddressTxIDs(ctx, addr, startZec, startZec+2)
			case "gettxout":
				_, err = z.TxOut(ctx, txid, 0)
			}
			if err == nil {
				t.Fatalf("%s: accepted", c.name)
			}
		})
	}
}

// An answer larger than the cap is refused before it is parsed, whatever it contains.
func TestNodeAnswersAreSizeCapped(t *testing.T) {
	f := newFakeZcash(t, "test", startZec)
	c := f.client()
	c.MaxResponse = 1024
	f.override = func(method string) ([]byte, int, bool) {
		return []byte(`{"result":"` + strings.Repeat("a", 4096) + `","error":null,"id":1}`), 200, true
	}
	if _, err := c.BlockHash(context.Background(), startZec); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("an oversized answer: %v", err)
	}
}

func TestNodeTimeoutIsEnforced(t *testing.T) {
	f := newFakeZcash(t, "test", startZec)
	f.delay = 400 * time.Millisecond
	c := f.client()
	c.HTTP.Timeout = 100 * time.Millisecond
	start := time.Now()
	if _, err := c.ChainInfo(context.Background()); err == nil {
		t.Fatal("a node slower than the timeout was waited for")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("the timeout did not bound the wait")
	}
}

func TestNodeSendsBasicAuth(t *testing.T) {
	f := newFakeZcash(t, "test", startZec)
	f.user, f.pass = "__cookie__", "s3cret"
	if _, err := f.client().ChainInfo(context.Background()); err != nil {
		t.Fatalf("the right credentials were refused: %v", err)
	}
	bad := NewZcashRPC(f.srv.URL, "__cookie__", "wrong")
	if _, err := bad.ChainInfo(context.Background()); err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Fatalf("wrong credentials: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".cookie")
	os.WriteFile(path, []byte("__cookie__:s3cret\n"), 0o600)
	u, p, err := ReadCookie(path)
	if err != nil || u != "__cookie__" || p != "s3cret" {
		t.Fatalf("cookie %q %q %v", u, p, err)
	}
	os.WriteFile(path, []byte("nocolon"), 0o600)
	if _, _, err := ReadCookie(path); err == nil {
		t.Fatal("a file with no user:password was read as a cookie")
	}
}

// A transaction the node already holds, in its mempool or mined, is a success whatever the node says about it;
// an answer lost after the node took the transaction is a success once the node is asked; a real rejection is an
// error, and nothing is claimed about a transaction the node does not have.
func TestBroadcastIsIdempotent(t *testing.T) {
	ctx := context.Background()
	f := newFakeZcash(t, "test", startZec)
	c := f.client()
	key := testKey(takerKeyN)
	f.Pay(P2PKHScript(key.PubKeyHash()), 500_000)
	coins := coinsOf(t, c, key)
	h := HTLC{Hash: [32]byte{1}, ClaimPkh: testKey(makerKeyN).PubKeyHash(), RefundPkh: key.PubKeyHash(), CLTV: startZec + 50}
	tx, err := BuildFunding(FundingRequest{Key: key, HTLC: h, Amount: 200_000, Coins: coins, BranchID: BranchNU63, ExpiryHeight: f.Tip() + 20})
	if err != nil {
		t.Fatal(err)
	}
	raw := tx.Serialize()
	want := DisplayHex(tx.TxID())
	for i := 0; i < 2; i++ {
		if id, err := Broadcast(ctx, c, raw); err != nil || id != want {
			t.Fatalf("send %d: %s %v", i, id, err)
		}
	}
	f.Mine(1)
	if id, err := Broadcast(ctx, c, raw); err != nil || id != want {
		t.Fatalf("a mined transaction sent again (zcashd: -27): %s %v", id, err)
	}
	// the answer is lost after the node took it
	f2 := newFakeZcash(t, "test", startZec)
	f2.Pay(P2PKHScript(key.PubKeyHash()), 500_000)
	c2 := f2.client()
	tx2, _ := BuildFunding(FundingRequest{Key: key, HTLC: h, Amount: 200_000, Coins: coinsOf(t, c2, key), BranchID: BranchNU63, ExpiryHeight: f2.Tip() + 20})
	f2.dropReply = func(m string) bool { return m == "sendrawtransaction" }
	if _, err := Broadcast(ctx, c2, tx2.Serialize()); err != nil {
		t.Fatalf("a lost answer after the node took the transaction: %v", err)
	}
	// a transaction the node refuses: its signature no longer covers its outputs
	tx3, _ := BuildFunding(FundingRequest{Key: key, HTLC: h, Amount: 100_000, Coins: coinsOf(t, c2, key), BranchID: BranchNU63, ExpiryHeight: f2.Tip() + 20})
	tx3.Out[0].Value--
	f2.dropReply = nil
	if _, err := Broadcast(ctx, c2, tx3.Serialize()); err == nil {
		t.Fatal("a transaction with a broken signature was reported sent")
	}
}

// The chain-height sanity check: a node still syncing, one behind its own estimate, one below NU5, and one whose
// height fell further than any reorganisation, are all refused.
func TestChainHeightSanity(t *testing.T) {
	ctx := context.Background()
	f := newFakeZcash(t, "test", startZec)
	f.ibd = true
	if _, err := f.client().ChainInfo(ctx); err == nil {
		t.Error("a node in its initial download was believed")
	}
	f.ibd, f.ahead = false, 100
	if _, err := f.client().ChainInfo(ctx); err == nil {
		t.Error("a node 100 blocks behind its own estimate was believed")
	}
	low := newFakeZcash(t, "test", 1000)
	if _, err := low.client().ChainInfo(ctx); err == nil {
		t.Error("a testnet node below NU5 was believed")
	}
	reg := newFakeZcash(t, "regtest", 1000)
	if ci, err := reg.client().ChainInfo(ctx); err != nil || !ci.Regtest {
		t.Errorf("a regtest node at a low height: %+v %v", ci, err)
	}
	f.ahead = 0
	c := f.client()
	if _, err := c.ChainInfo(ctx); err != nil {
		t.Fatal(err)
	}
	f.Mine(200)
	if _, err := c.ChainInfo(ctx); err != nil {
		t.Fatal(err)
	}
	f.Reorg(150, false)
	if _, err := c.ChainInfo(ctx); err == nil {
		t.Error("a height drop of 150 blocks was believed")
	}
}

func coinsOf(t testing.TB, c *ZcashRPC, k *Key) []Coin {
	us, err := c.AddressUTXOs(context.Background(), k.Address(Testnet))
	if err != nil {
		t.Fatal(err)
	}
	var out []Coin
	for _, u := range us {
		cn, err := CoinOf(u)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, cn)
	}
	return out
}
