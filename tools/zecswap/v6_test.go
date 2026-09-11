package zecswap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
)

// v6 transactions (ZIP 229), regtest, and the answers of a real zebrad 6.3.0 — pinned by transactions captured from
// one (testdata/regtest_v6.json).

type regtestVectors struct {
	V6Coinbase struct {
		TxID string `json:"txid"`
		Hex  string `json:"hex"`
	} `json:"v6_coinbase"`
	V6Spend struct {
		TxID          string `json:"txid"`
		Hex           string `json:"hex"`
		PrevoutValue  uint64 `json:"prevout_value"`
		PrevoutScript string `json:"prevout_script"`
	} `json:"v6_p2pkh_spend"`
	V5Spend struct {
		TxID string `json:"txid"`
		Hex  string `json:"hex"`
	} `json:"v5_spend"`
}

func loadRegtestVectors(t *testing.T) regtestVectors {
	b, err := os.ReadFile("testdata/regtest_v6.json")
	if err != nil {
		t.Fatal(err)
	}
	var v regtestVectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func mustDecode(t *testing.T, s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The v6 transaction id and signature digest are ZIP 229's: this package's computation gives zebrad's txid for a
// real v6 coinbase and a real v6 P2PKH spend built by librustzcash, and verifies that spend's signature — which a
// v5-personalized digest does not.
func TestRealV6TransactionsFromZebra(t *testing.T) {
	v := loadRegtestVectors(t)
	for name, c := range map[string]struct{ id, hx string }{"v6 coinbase": {v.V6Coinbase.TxID, v.V6Coinbase.Hex}, "v6 spend": {v.V6Spend.TxID, v.V6Spend.Hex}, "v5 spend": {v.V5Spend.TxID, v.V5Spend.Hex}} {
		raw := mustDecode(t, c.hx)
		if id, ok := ComputeTxID(raw); !ok || id != c.id {
			t.Fatalf("%s: computed %q (%v), zebrad says %s", name, id, ok, c.id)
		}
		if _, _, err := transparentParts(raw); err != nil {
			t.Fatalf("%s: the transparent reader: %v", name, err)
		}
	}
	raw := mustDecode(t, v.V6Spend.Hex)
	tx, err := ParseV6(raw)
	if err != nil || tx.Version != 6 || len(tx.In) != 1 {
		t.Fatalf("ParseV6: %+v %v", tx, err)
	}
	if string(tx.Serialize()) != string(raw) {
		t.Fatal("the v6 transaction does not serialize back to its bytes")
	}
	tx.In[0].Value, tx.In[0].ScriptPubKey = v.V6Spend.PrevoutValue, mustDecode(t, v.V6Spend.PrevoutScript)
	pushes, err := scriptPushes(tx.In[0].ScriptSig)
	if err != nil || len(pushes) != 2 {
		t.Fatal("the spend's scriptSig is not <sig> <pubkey>")
	}
	d, err := tx.SigDigest(0, SigHashAll)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifySig(pushes[0], pushes[1], d) {
		t.Fatal("librustzcash's v6 signature does not verify against this package's v6 signature digest")
	}
	tx.Version = 5
	if d5, _ := tx.SigDigest(0, SigHashAll); VerifySig(pushes[0], pushes[1], d5) {
		t.Fatal("the v6 signature verified against the v5 digest: the two are not being told apart")
	}
}

// A claim built as v6 reads back as v6, verifies under the interpreter, and gives up its secret.
func TestV6ClaimIsBuiltAndRead(t *testing.T) {
	h, secret := htlcFor(startZec + 50)
	claim, err := BuildClaim(SpendRequest{Key: testKey(makerKeyN), HTLC: h, Prev: OutPoint{Hash: [32]byte{7}}, Value: 200_000, Payout: h.ClaimPkh, BranchID: BranchNU63, Version: 6}, secret)
	if err != nil {
		t.Fatal(err)
	}
	raw := claim.Serialize()
	if binaryHeader(raw) != V6Version {
		t.Fatalf("header %08x", binaryHeader(raw))
	}
	if err := verifyInput(claim, 0); err != nil {
		t.Fatal(err)
	}
	ins, _, err := transparentParts(raw)
	if err != nil {
		t.Fatal(err)
	}
	if kind, got := ClassifySpend(ins[0].ScriptSig, h); kind != SpendClaim || got != secret {
		t.Fatalf("read as %s", kind)
	}
	if id, ok := ComputeTxID(raw); !ok || id != DisplayHex(claim.TxID()) {
		t.Fatal("the v6 claim's id cannot be computed back")
	}
	refund, err := BuildRefund(SpendRequest{Key: testKey(takerKeyN), HTLC: h, Prev: OutPoint{Hash: [32]byte{7}}, Value: 200_000, Payout: h.RefundPkh, BranchID: BranchNU63, Version: 6})
	if err != nil || verifyInput(refund, 0) != nil {
		t.Fatalf("a v6 refund: %v", err)
	}
	if _, err := BuildClaim(SpendRequest{Key: testKey(makerKeyN), HTLC: h, Value: 200_000, BranchID: BranchNU63, Version: 7}, secret); err == nil {
		t.Fatal("a version-7 claim was built")
	}
}

// The watcher never passes over a transaction at the script's address it cannot read: it stops and says so. A
// spend it skipped might be the claim that carries the secret.
func TestFindSpendFailsClosedOnAnUnreadableTransaction(t *testing.T) {
	ctx := context.Background()
	f := newFakeZcash(t, "test", startZec)
	c := f.client()
	h, _ := htlcFor(startZec + 500)
	fundID := f.Pay(h.ScriptPubKey(), 200_000)
	fund := Funding{TxID: fundID, Value: 200_000, Height: f.Tip()}
	junk := f.Pay(h.ScriptPubKey(), 1)
	f.mu.Lock()
	raw := append([]byte(nil), f.txs[junk].raw...)
	raw[0] = 7 // a version this build has never heard of
	f.txs[junk].raw = raw
	f.mu.Unlock()
	cur := ScanCursor{Base: fund.Height}
	var err error
	for i := 0; i < 5 && err == nil; i++ {
		_, err = FindSpendStep(ctx, c, h, Testnet, fund, &cur, f.Tip(), SpendScanBudget)
	}
	if err == nil || !strings.Contains(err.Error(), "cannot be read") {
		t.Fatalf("an unreadable transaction at the address was passed over: %v", err)
	}
}

// ---- regtest ----

var (
	regtestSchedMu sync.Mutex
	regtestSched   = map[*fakeZcash]map[uint32]uint32{}
)

// regtestScheduleOf is the fake regtest node's upgrade table: zebrad 6.3.0's own (NU5 at 2 … NU6.3 at 6) unless a
// test sets another.
func regtestScheduleOf(f *fakeZcash) map[uint32]uint32 {
	regtestSchedMu.Lock()
	defer regtestSchedMu.Unlock()
	if m := regtestSched[f]; m != nil {
		return m
	}
	return map[uint32]uint32{BranchNU5: 2, BranchNU6: 3, BranchNU61: 4, BranchNU62: 5, BranchNU63: 6}
}

func setRegtestSchedule(f *fakeZcash, m map[uint32]uint32) {
	regtestSchedMu.Lock()
	defer regtestSchedMu.Unlock()
	regtestSched[f] = m
}

// zebrad says "chain": "test" on regtest. Regtest is told apart by its genesis block, and only by it; a node that
// calls itself regtest with any other genesis is refused.
func TestRegtestIsRecognisedByItsGenesis(t *testing.T) {
	ctx := context.Background()
	reg := newFakeZcash(t, "regtest", 5000)
	ci, err := reg.client().ChainInfo(ctx)
	if err != nil || !ci.Regtest || ci.Chain != "regtest" || ci.Net != Testnet || len(ci.Upgrades) < 5 {
		t.Fatalf("a regtest node: %+v %v", ci, err)
	}
	test := newFakeZcash(t, "test", startZec)
	if ci, err := test.client().ChainInfo(ctx); err != nil || ci.Regtest || ci.Chain != "test" {
		t.Fatalf("a testnet node: %+v %v", ci, err)
	}
	liar := newFakeZcash(t, "test", startZec)
	liar.override = func(method string) ([]byte, int, bool) {
		if method != "getblockchaininfo" {
			return nil, 0, false
		}
		return []byte(`{"result":{"chain":"regtest","blocks":4200000,"bestblockhash":"` + strings.Repeat("a", 64) + `","upgrades":{},"consensus":{"chaintip":"37a5165b","nextblock":"37a5165b"}},"error":null,"id":1}`), 200, true
	}
	if _, err := liar.client().ChainInfo(ctx); err == nil {
		t.Fatal("a node calling itself regtest with testnet's genesis was believed")
	}
	// regtest is reachable only with -network regtest
	for _, network := range []string{"mainnet", "testnet"} {
		if err := CheckNetworks(network, "regtest", true); err == nil {
			t.Fatalf("-network %s accepted a regtest node", network)
		}
	}
	if err := CheckNetworks("regtest", "regtest", true); err != nil {
		t.Fatal(err)
	}
	if PolicyFor("mainnet") != DefaultPolicy() || PolicyFor("testnet") != DefaultPolicy() || PolicyFor("regtest") != RegtestPolicy() {
		t.Fatal("the regtest policy is reachable from another network")
	}
	if err := RegtestPolicy().Validate(); err != nil {
		t.Fatal(err)
	}
}

// On regtest the branch id is checked against the node's own table; on mainnet and testnet against this build's.
func TestCheckBranchIDOnSchedule(t *testing.T) {
	zebra := []NodeUpgrade{{Name: "Canopy", BranchID: 0xe9ff75a6, Height: 1, Status: "active"}, {Name: "NU5", BranchID: BranchNU5, Height: 2}, {Name: "NU6", BranchID: BranchNU6, Height: 3},
		{Name: "NU6.1", BranchID: BranchNU61, Height: 4}, {Name: "NU6.2", BranchID: BranchNU62, Height: 5}, {Name: "NU6.3", BranchID: BranchNU63, Height: 6}}
	for _, c := range []struct {
		next, branch uint32
		ok           bool
	}{{347, BranchNU63, true}, {6, BranchNU63, true}, {5, BranchNU62, true}, {5, BranchNU63, false}, {347, BranchNU62, false}, {347, 0xDEADBEEF, false}, {1, 0xe9ff75a6, false}} {
		if err := CheckBranchIDOnSchedule(zebra, c.next, c.branch); (err == nil) != c.ok {
			t.Errorf("height %d branch %08x: %v", c.next, c.branch, err)
		}
	}
	// the testnet schedule would refuse the same answer at regtest heights
	if err := CheckBranchID(Testnet, 347, BranchNU63); err == nil {
		t.Fatal("testnet's schedule accepted NU6.3 at height 347")
	}
}

// zebrad answers a duplicate broadcast with -1 ("transaction already exists in mempool", then "... was committed to
// the best chain"), where zcashd answered the txid or -27. Broadcast asks the node whether it holds the transaction,
// so both are a success; a -1 about a transaction the node does not hold is not.
func TestBroadcastTreatsZebraDuplicateAsSent(t *testing.T) {
	ctx := context.Background()
	f := newFakeZcash(t, "test", startZec)
	c := f.client()
	key := testKey(takerKeyN)
	f.Pay(P2PKHScript(key.PubKeyHash()), 500_000)
	h, _ := htlcFor(startZec + 50)
	tx, err := BuildFunding(FundingRequest{Key: key, HTLC: h, Amount: 200_000, Coins: coinsOf(t, c, key), BranchID: BranchNU63, ExpiryHeight: f.Tip() + 20})
	if err != nil {
		t.Fatal(err)
	}
	raw := tx.Serialize()
	zebraSays := func(msg string) {
		f.mu.Lock()
		f.override = func(method string) ([]byte, int, bool) {
			return []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-1,"message":"` + msg + `"}}`), 200, method == "sendrawtransaction"
		}
		f.mu.Unlock()
	}
	zebraSays("transaction already exists in mempool")
	if _, err := Broadcast(ctx, c, raw); err == nil {
		t.Fatal("a -1 about a transaction the node does not hold was taken as sent")
	}
	if _, code, msg := f.Submit(raw); code != 0 {
		t.Fatal(msg)
	}
	if _, err := Broadcast(ctx, c, raw); err != nil {
		t.Fatalf("zebrad's duplicate answer for a pooled transaction: %v", err)
	}
	f.Mine(1)
	zebraSays("transaction was committed to the best chain")
	if _, err := Broadcast(ctx, c, raw); err != nil {
		t.Fatalf("zebrad's duplicate answer for a mined transaction: %v", err)
	}
}

// The v6 claim the regtest end-to-end run (TestRegtestEndToEnd) got mined on zebrad, built by this package and
// accepted by the node: its bytes hash to the node's txid, the taker's reader finds the secret in it, and the secret
// hashes to the swap's hash.
func TestRealV6ClaimFromZebra(t *testing.T) {
	b, err := os.ReadFile("testdata/regtest_v6_claim.json")
	if err != nil {
		t.Fatalf("testdata/regtest_v6_claim.json, captured by TestRegtestEndToEnd (ZECSWAP_REGTEST=1), is missing: %v", err)
	}
	var v struct {
		TxID   string `json:"txid"`
		Hex    string `json:"hex"`
		Hash   string `json:"hash"`
		Redeem string `json:"redeem"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	raw := mustDecode(t, v.Hex)
	if binaryHeader(raw) != V6Version {
		t.Fatalf("header %08x is not v6", binaryHeader(raw))
	}
	if id, ok := ComputeTxID(raw); !ok || id != v.TxID {
		t.Fatalf("computed %s, zebrad mined %s", id, v.TxID)
	}
	hc, ok := parseHTLCScript(mustDecode(t, v.Redeem))
	if !ok || hex.EncodeToString(hc.Hash[:]) != v.Hash {
		t.Fatal("the recorded redeem script is not the swap's")
	}
	ins, _, err := transparentParts(raw)
	if err != nil {
		t.Fatal(err)
	}
	kind, secret := ClassifySpend(ins[0].ScriptSig, hc)
	if kind != SpendClaim || sha256.Sum256(secret[:]) != hc.Hash {
		t.Fatalf("the real v6 claim reads as %s", kind)
	}
	if errors.Is(err, ErrShielded) {
		t.Fatal("unreachable")
	}
}
