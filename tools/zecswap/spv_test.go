package zecswap

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"veil/tools/zecspv"
)

// ---- the header gate (spv.go) ----

type recordedHeaders struct {
	FirstHeight uint32   `json:"first_height"`
	Headers     []string `json:"headers"`
	Blocks      []struct {
		Height uint32   `json:"height"`
		Hash   string   `json:"hash"`
		TxIDs  []string `json:"txids"`
	} `json:"blocks"`
}

// serveRecordedHeaders is a Zcash node that knows only a recorded run of real Mainnet headers (tools/zecspv/testdata)
// and answers getblockhash, getblockheader and getblock from it, as zebrad would. tamper, when set, alters the header
// it serves at a height (a node that lies about one header).
func serveRecordedHeaders(t *testing.T, file string, tamper func(height uint32, raw []byte)) (*ZcashRPC, *recordedHeaders) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "zecspv", "testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	var rec recordedHeaders
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	hashes, served, txids := map[uint32]string{}, map[string][]byte{}, map[string][]string{}
	for i, hx := range rec.Headers {
		raw, err := hex.DecodeString(hx)
		if err != nil {
			t.Fatal(err)
		}
		h, err := zecspv.ParseHeader(raw)
		if err != nil {
			t.Fatal(err)
		}
		height := rec.FirstHeight + uint32(i)
		hashes[height] = h.Hash().String()
		out := append([]byte(nil), raw...)
		if tamper != nil {
			tamper(height, out)
		}
		served[hashes[height]] = out
	}
	for _, blk := range rec.Blocks {
		txids[blk.Hash] = blk.TxIDs
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage   `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		var res, rpcErr interface{}
		missing := map[string]interface{}{"code": -5, "message": "Block not found"}
		switch req.Method {
		case "getblockhash":
			var h uint32
			json.Unmarshal(req.Params[0], &h)
			if s, ok := hashes[h]; ok {
				res = s
			} else {
				rpcErr = map[string]interface{}{"code": -8, "message": "Block height out of range"}
			}
		case "getblockheader":
			var hash string
			json.Unmarshal(req.Params[0], &hash)
			if raw, ok := served[hash]; ok {
				res = hex.EncodeToString(raw)
			} else {
				rpcErr = missing
			}
		case "getblock":
			var hash string
			json.Unmarshal(req.Params[0], &hash)
			if ids, ok := txids[hash]; ok {
				res = map[string]interface{}{"hash": hash, "tx": ids}
			} else {
				rpcErr = missing
			}
		default:
			rpcErr = map[string]interface{}{"code": -32601, "message": "not served"}
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"id": req.ID, "result": res, "error": rpcErr})
	}))
	t.Cleanup(srv.Close)
	return NewZcashRPC(srv.URL, "", ""), &rec
}

// The gate's reads and checks, on real Mainnet headers served as a node would serve them: GatherFundingProof and
// GatherHeaders read exactly what VerifyFunding and VerifyTip accept, and a node that lies — a transaction the block
// does not hold, a block that is not the one seen, one altered header — is refused.
func TestHeaderGatePlumbingOnRealMainnetHeaders(t *testing.T) {
	const file = "mainnet-3436131.json"
	ctx := context.Background()
	c, rec := serveRecordedHeaders(t, file, nil)
	var blockHash string
	var txids []string
	for _, b := range rec.Blocks {
		if b.Height == 3436162 {
			blockHash, txids = b.Hash, b.TxIDs
		}
	}
	leaf, err := zecspv.ParseHash(txids[3])
	if err != nil {
		t.Fatal(err)
	}
	raw0, _ := hex.DecodeString(rec.Headers[0])
	h0, _ := zecspv.ParseHeader(raw0)
	floor, err := zecspv.Mainnet.FloorFromBits(h0.Bits, 4)
	if err != nil {
		t.Fatal(err)
	}
	rawTip, _ := hex.DecodeString(rec.Headers[len(rec.Headers)-1])
	hTip, _ := zecspv.ParseHeader(rawTip)
	now := time.Unix(int64(hTip.Time), 0).Add(10 * time.Minute)
	pol := zecspv.Policy{Depth: 9, Below: 28, MaxTarget: floor, Now: now, MaxTipAge: 3 * time.Hour}

	// the maker's proof: the funding in block 3436162, 28 headers below it, 9 above
	proof, err := GatherFundingProof(ctx, c, 3436162, 3436171, 28, leaf, blockHash)
	if err != nil {
		t.Fatal(err)
	}
	f, err := zecspv.VerifyFunding(zecspv.Mainnet, proof, pol)
	if err != nil {
		t.Fatalf("real headers read through the gate did not verify: %v", err)
	}
	if f.BlockHeight != 3436162 || f.Confirmations != 10 || f.BlockHash.String() != blockHash {
		t.Fatalf("%+v", f)
	}

	// the taker's tip check through headerGateTip itself, with the recorded network's parameters
	sw := &Swap{cfg: Config{Policy: testPolicy(), Log: func(string, ...interface{}) {}, Headers: &HeaderCheck{Below: 28, MaxTipAge: 3 * time.Hour, Now: func() time.Time { return now }, Params: zecspv.Mainnet, Floor: floor}},
		st: &State{Role: RoleTaker}, zc: c, said: map[string]bool{}}
	tip, err := sw.headerGateTip(ctx, ChainInfo{Chain: "main", Net: Mainnet, Height: 3436171}, 3436171)
	if err != nil || tip != 3436171 {
		t.Fatalf("the taker's tip check on real headers: %d %v", tip, err)
	}
	// a witness that shows a tip well above what the headers prove stops the funding
	if _, err := sw.headerGateTip(ctx, ChainInfo{Chain: "main", Net: Mainnet, Height: 3436171}, 3436171+5); err == nil {
		t.Fatal("a tip five blocks above the proven one was taken")
	}

	// a transaction the block does not hold
	var other zecspv.Hash
	other[0] = 1
	if _, err := GatherFundingProof(ctx, c, 3436162, 3436171, 28, other, blockHash); err == nil {
		t.Fatal("a proof was built for a transaction the block does not hold")
	}
	// the funding seen in a block that is not the node's block at that height
	if _, err := GatherFundingProof(ctx, c, 3436162, 3436171, 28, leaf, strings.Repeat("0", 64)); !errors.Is(err, ErrReorged) {
		t.Fatalf("a changed block at the funding's height: %v", err)
	}
	// a node that serves one altered header, under the funding: its hash is no longer the block it names
	c2, _ := serveRecordedHeaders(t, file, func(h uint32, raw []byte) {
		if h == 3436150 {
			raw[104] ^= 1 // nBits
		}
	})
	if _, err := GatherFundingProof(ctx, c2, 3436162, 3436171, 28, leaf, blockHash); err == nil {
		t.Fatal("an altered header was read as the node's block")
	}
	// too few headers below the funding for the rule: refused by zecspv, not by luck
	short, err := GatherFundingProof(ctx, c, 3436162, 3436171, 10, leaf, blockHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zecspv.VerifyFunding(zecspv.Mainnet, short, zecspv.Policy{Depth: 9, Below: 10, MaxTarget: floor, Now: now}); !errors.Is(err, zecspv.ErrContext) {
		t.Fatalf("a proof with 10 headers below: %v", err)
	}
}

// With the gate on and a node whose headers carry no work, the maker never reveals: the claim cutoff passes, both
// refund.
func TestHeaderGateFailsClosedForTheMaker(t *testing.T) {
	w := newWorld(t, 41)
	w.cfgHook = func(role string, c *Config) {
		if role == RoleMaker {
			c.Headers = &HeaderCheck{Below: 2, Now: time.Now}
		}
	}
	if !w.drive(500) {
		t.Fatalf("not finished: maker %s, taker %s\n%s", w.state(RoleMaker).Phase, w.state(RoleTaker).Phase, w.dump())
	}
	w.check("header gate, maker", []string{"refunded"}, []string{"refunded"})
	w.zc.mu.Lock()
	reads := w.zc.calls["getblockheader"]
	w.zc.mu.Unlock()
	if len(w.claims) != 0 || w.state(RoleMaker).Revealed || reads == 0 || !strings.Contains(w.dump(), "not claiming: ") {
		t.Fatalf("claims %d, revealed %v, header reads %d\n%s", len(w.claims), w.state(RoleMaker).Revealed, reads, w.dump())
	}
}

// With the gate on and a node whose headers carry no work, the taker never funds; she declines once the lock is too
// short to fund, and the maker refunds his DERO.
func TestHeaderGateFailsClosedForTheTaker(t *testing.T) {
	w := newWorld(t, 42)
	w.cfgHook = func(role string, c *Config) {
		if role == RoleTaker {
			c.Headers = &HeaderCheck{Below: 2, Now: time.Now}
		}
	}
	if !w.drive(500) {
		t.Fatalf("not finished: maker %s, taker %s\n%s", w.state(RoleMaker).Phase, w.state(RoleTaker).Phase, w.dump())
	}
	// she retries the gate each pass and funds nothing; the lock's own minimum (T_dero − now) ends it
	tk := w.state(RoleTaker)
	if tk.Phase != "declined" || len(w.fundings) != 0 || tk.FundingTxID != "" || !strings.Contains(w.dump(), "reading the headers up to the tip") {
		t.Fatalf("taker %s: %s; fundings %d\n%s", tk.Phase, tk.Outcome, len(w.fundings), w.dump())
	}
	if mk := w.state(RoleMaker); mk.Phase != "refunded" {
		t.Fatalf("maker %s", mk.Phase)
	}
}

// On regtest the gate is skipped, explicitly: it reads no header, and says so.
func TestHeaderGateIsSkippedOnRegtestAndSaysSo(t *testing.T) {
	w := newWorld(t, 43)
	w.cfgHook = func(role string, c *Config) { c.Headers = DefaultHeaderCheck() }
	sw := w.start(RoleTaker)
	rt := ChainInfo{Chain: "regtest", Net: Testnet, Regtest: true, Height: 500}
	if tip, err := sw.headerGateTip(w.ctx, rt, 500); err != nil || tip != 500 {
		t.Fatalf("%d %v", tip, err)
	}
	if err := sw.headerGateFunding(w.ctx, rt, Funding{TxID: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	w.zc.mu.Lock()
	reads := w.zc.calls["getblockheader"] + w.zc.calls["getblock"]
	w.zc.mu.Unlock()
	if reads != 0 || !strings.Contains(w.dump(), "HEADER VERIFICATION SKIPPED") {
		t.Fatalf("%d header reads\n%s", reads, w.dump())
	}
	if !strings.Contains(HeaderGateNote("regtest"), "SKIPPED ON REGTEST") || !strings.Contains(HeaderGateNote("testnet"), "plumbing only") {
		t.Fatal("the start-up notes do not say what the gate does on regtest and testnet")
	}
}

// Off by default: a whole swap reads no header.
func TestHeaderGateIsOffByDefault(t *testing.T) {
	w := runScenario(t, "happy", 44)
	w.zc.mu.Lock()
	reads := w.zc.calls["getblockheader"] + w.zc.calls["getblock"]
	w.zc.mu.Unlock()
	if reads != 0 {
		t.Fatalf("%d header reads with the gate off", reads)
	}
}

// tools/regtest/rpcgate serves exactly the methods node.go calls, no more and no fewer.
func TestGateServesExactlyTheNodeClientsMethods(t *testing.T) {
	gate, err := os.ReadFile(filepath.Join("..", "regtest", "rpcgate", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`const zecswapMethods = "([^"]+)"`).FindSubmatch(gate)
	if m == nil {
		t.Fatal("rpcgate's zecswapMethods not found")
	}
	served := map[string]bool{}
	for _, s := range strings.Split(string(m[1]), ",") {
		served[s] = true
	}
	node, err := os.ReadFile("node.go")
	if err != nil {
		t.Fatal(err)
	}
	called := map[string]bool{}
	for _, c := range regexp.MustCompile(`c\.call\(ctx, "([a-z]+)"`).FindAllSubmatch(node, -1) {
		called[string(c[1])] = true
	}
	for s := range served {
		if !called[s] {
			t.Errorf("rpcgate serves %s, which node.go never calls", s)
		}
	}
	for c := range called {
		if !served[c] {
			t.Errorf("node.go calls %s, which rpcgate does not serve", c)
		}
	}
	if len(called) != 9 || !strings.Contains(string(node), "Exactly nine") {
		t.Fatalf("node.go calls %d methods; its METHODS comment must say how many", len(called))
	}
}

// ---- the board's cltv, checked against the companion's own Zcash node ----

// An accept made by this wallet elsewhere (the app computes cltv from a height typed by hand, +96, and board.bas
// stores it as given) is adopted only if its cltv holds against the taker's own node: too far and too soon are both
// refused before the taker waits on any lock.
func TestTakerRefusesAnAppAcceptWithCLTVOutsidePolicy(t *testing.T) {
	for _, c := range []struct {
		name  string
		ahead uint32
	}{{"too far: the taker's ZEC locked too long", 500}, {"too soon: no room for the maker to claim safely", 3}} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t, 51)
			w.stepN(RoleMaker, 2)
			pkh := testKey(takerKeyN).PubKeyHash()
			if _, err := newMemDero(w.dero, takerAddr).Accept(w.ctx, 0, hex.EncodeToString(pkh[:]), uint64(w.zc.Tip()+c.ahead)); err != nil {
				t.Fatal(err)
			}
			w.step(RoleTaker)
			tk := w.state(RoleTaker)
			if tk.Phase != "declined" || !strings.Contains(tk.Outcome, "does not hold against this companion's own Zcash node") {
				t.Fatalf("taker %s: %s", tk.Phase, tk.Outcome)
			}
		})
	}
}

// The chain moved between the accept and the funding (the maker took his time locking): at funding the taker checks
// cltv against her own node again, and declines rather than fund a script whose refund is now too close.
func TestTakerDeclinesFundingWhenCLTVHasLeftPolicy(t *testing.T) {
	w := newWorld(t, 52)
	for i := 0; i < 200 && w.state(RoleTaker).Phase != "lock_seen"; i++ {
		w.round()
	}
	if w.state(RoleTaker).Phase != "lock_seen" {
		t.Fatalf("taker %s\n%s", w.state(RoleTaker).Phase, w.dump())
	}
	w.zc.Mine(15)
	for i := 0; i < 60 && !w.done(RoleTaker); i++ {
		w.step(RoleTaker)
		w.dero.Advance(4)
	}
	tk := w.state(RoleTaker)
	if tk.Phase != "declined" || !strings.Contains(tk.Outcome, "policy") || len(w.fundings) != 0 {
		t.Fatalf("taker %s: %s; fundings %d\n%s", tk.Phase, tk.Outcome, len(w.fundings), w.dump())
	}
}

// ---- a resend nobody answered is not a verdict ----

// After the deep reorganisation of TestDeepReorgAfterTheClaimEndsLost, the node loses every answer to
// sendrawtransaction for a while: the maker must not take silence for a refusal and end the swap lost; once the node
// answers again, its verdict (the coin is spent) ends it.
func TestAResendWithNoAnswerIsNotALoss(t *testing.T) {
	w := newWorld(t, 25)
	hold := true
	w.offline = func(w *world, role string) bool {
		if !hold || role != RoleMaker {
			return false
		}
		mk := w.state(RoleMaker)
		return mk.Phase == "claimed" && mk.ClaimBlock != ""
	}
	for i := 0; i < 400 && !w.done(RoleTaker); i++ {
		w.round()
	}
	mk := w.state(RoleMaker)
	if mk.Phase != "claimed" {
		t.Fatalf("maker %s\n%s", mk.Phase, w.dump())
	}
	depth := int(w.zc.Tip() - mk.Funding.Height + 1)
	w.zc.Reorg(depth, false)
	w.secondSpend()
	w.zc.Mine(depth + 1)
	hold = false
	w.zc.mu.Lock()
	w.zc.dropReply = func(m string) bool { return m == "sendrawtransaction" }
	w.zc.mu.Unlock()
	for i := 0; i < 6; i++ {
		w.round()
	}
	if mk = w.state(RoleMaker); mk.Phase != "claimed" || !strings.Contains(w.dump(), "no node gave a verdict") {
		t.Fatalf("the maker ended %s on a resend no node answered\n%s", mk.Phase, w.dump())
	}
	w.zc.mu.Lock()
	w.zc.dropReply = nil
	w.zc.mu.Unlock()
	for i := 0; i < 20 && !w.done(RoleMaker); i++ {
		w.round()
	}
	if mk = w.state(RoleMaker); mk.Phase != "lost" {
		t.Fatalf("maker %s once the node answered\n%s", mk.Phase, w.dump())
	}
}
