package zecswap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Test-only fakes. Nothing outside _test.go files can reach them: the production Zcash path is ZcashRPC and the
// production DERO path is DeroRPC, and both are driven here over HTTP against these fakes, or — for the state
// machines' in-process DERO — through the Dero interface.

// Two real simulator addresses (tools/xmrswap's tests use the same), so DeroRaw and DeroAddress run on the bytes
// the contracts would see. Neither is a live wallet in these tests.
const (
	makerAddr  = "deto1qyx6datstrsx8qlmctgpxdtze4t8mu2l4rue6ql29s7ec5j9hwnv5qgfywu80"
	takerAddr  = "deto1qy05unfm5l0aeqd5w7ht6ytgxuldzny668rdmaq5gujzmmn63qw2wqqzdnfce"
	makerKeyN  = 7
	takerKeyN  = 9
	startZec   = 4_200_000 // above testnet NU6.3 (4,134,000), so BranchNU63 is the schedule's answer
	startDero  = 1000
	testGive   = 50_000
	testWant   = 200_000
	takerCoins = 1_000_000
)

var (
	testBoard  = strings.Repeat("b0", 32)
	testEscrow = strings.Repeat("e5", 32)
)

func otherAddr(n byte) string {
	a, err := DeroAddress("02"+strings.Repeat(fmt.Sprintf("%02x", n), 32), false)
	if err != nil {
		panic(err)
	}
	return a
}

func testKey(n byte) *Key {
	var b [32]byte
	b[31] = n
	k, err := ParseKey(b)
	if err != nil {
		panic(err)
	}
	return k
}

func keySource(n byte) func(Network) (*Key, error) {
	return func(Network) (*Key, error) { return testKey(n), nil }
}

// testPolicy is §5 shrunk so a swap runs in a few hundred simulated blocks; Validate still holds.
func testPolicy() Policy {
	return Policy{ConfZec: 3, CLTVWindow: 32, CLTVTolerance: 12, ClaimCutoff: 5, TDeroWindow: 280, TakerMinDeroLeft: 220,
		PLock: 40, FundingExpiry: 10, RefundBump: 3, MinAmount: 100_000, OfferLife: 120, SettleBlocks: 2}
}

// ======================================================================================================
// The fake Zcash node.
//
// A chain of blocks, a mempool and consensus rules enough for this market: P2PKH spends and the two branches of the
// swap script are verified with ZIP 244 digests and real signatures, CHECKLOCKTIMEVERIFY and nLockTime finality,
// nExpiryHeight, the consensus branch id, the ZIP 317 fee. Its answers are shaped as the zcashd RPC documentation
// shapes them, method by method (the page is named at each), in the envelope zcashd uses: HTTP 200 with
// {"result": …, "error": null, "id": …}, HTTP 500 with {"result": null, "error": {"code", "message"}, "id": …}.
// ======================================================================================================

type ztx struct {
	id  string
	raw []byte
	tx  *Tx
}

type zblock struct {
	hash string
	txs  []string
}

type zout struct {
	out    TxOut
	height uint32
}

type fakeZcash struct {
	t      testing.TB
	mu     sync.Mutex
	chain  string
	base   uint32
	blocks []zblock
	txs    map[string]*ztx
	pool   []string
	nonce  uint64
	branch uint32
	// pending upgrades reported by getblockchaininfo, as a node that knows of a scheduled activation would
	pending []NodeUpgrade
	user    string
	pass    string
	ibd     bool
	ahead   uint32 // estimatedheight − blocks
	delay   time.Duration
	// dropReply, when it answers true, applies the call and then loses the answer (HTTP 502, no JSON-RPC body)
	dropReply func(method string) bool
	// onSend is called, under the node's lock, for every transaction sendrawtransaction is about to accept
	onSend func(f *fakeZcash, tx *Tx) error
	// override replaces the whole HTTP answer to a method: a crafted, malformed or oversized reply
	override func(method string) ([]byte, int, bool)
	sent     []string
	calls    map[string]int
	srv      *httptest.Server
}

func newFakeZcash(t testing.TB, chain string, tip uint32) *fakeZcash {
	f := &fakeZcash{t: t, chain: chain, base: tip - 20, txs: map[string]*ztx{}, branch: BranchNU63, calls: map[string]int{}}
	for i := 0; i < 21; i++ {
		f.appendBlockLocked(nil)
	}
	f.srv = httptest.NewServer(f)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeZcash) client() *ZcashRPC {
	c := NewZcashRPC(f.srv.URL, f.user, f.pass)
	c.HTTP = &http.Client{Timeout: 5 * time.Second}
	return c
}

func (f *fakeZcash) tipLocked() uint32 { return f.base + uint32(len(f.blocks)) - 1 }

func (f *fakeZcash) Tip() uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tipLocked()
}

func (f *fakeZcash) appendBlockLocked(ids []string) {
	f.nonce++
	h := f.base + uint32(len(f.blocks))
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|%v", f.chain, h, f.nonce, ids)))
	f.blocks = append(f.blocks, zblock{hash: hex.EncodeToString(sum[:]), txs: ids})
}

func (f *fakeZcash) addTxLocked(tx *Tx) string {
	raw := tx.Serialize()
	id := DisplayHex(tx.TxID())
	f.txs[id] = &ztx{id: id, raw: raw, tx: tx}
	return id
}

// Pay mints a coin to script in a new block: the taker's ZEC, arriving from somewhere else.
func (f *fakeZcash) Pay(script []byte, value uint64) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nonce++
	id := f.addTxLocked(&Tx{BranchID: f.branch, LockTime: uint32(f.nonce), Out: []TxOut{{Value: value, ScriptPubKey: script}}})
	f.appendBlockLocked([]string{id})
	return id
}

// MineTx puts a transaction in a new block without asking whether it is valid: what a lying node shows.
func (f *fakeZcash) MineTx(tx *Tx) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.addTxLocked(tx)
	f.appendBlockLocked([]string{id})
	return id
}

func (f *fakeZcash) utxoLocked() map[OutPoint]zout {
	u := map[OutPoint]zout{}
	for i, b := range f.blocks {
		h := f.base + uint32(i)
		for _, id := range b.txs {
			z := f.txs[id]
			for _, in := range z.tx.In {
				delete(u, in.PrevOut)
			}
			hash, _ := ParseDisplayHex(id)
			for j, o := range z.tx.Out {
				u[OutPoint{Hash: hash, Index: uint32(j)}] = zout{out: o, height: h}
			}
		}
	}
	return u
}

func (f *fakeZcash) minedAtLocked(id string) (uint32, bool) {
	for i, b := range f.blocks {
		for _, x := range b.txs {
			if x == id {
				return f.base + uint32(i), true
			}
		}
	}
	return 0, false
}

func (f *fakeZcash) inPoolLocked(id string) bool {
	for _, x := range f.pool {
		if x == id {
			return true
		}
	}
	return false
}

// Unspent says whether an output is unspent on the chain.
func (f *fakeZcash) Unspent(txid string, index uint32) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, _ := ParseDisplayHex(txid)
	_, ok := f.utxoLocked()[OutPoint{Hash: h, Index: index}]
	return ok
}

// SpenderOf is the mined transaction spending an output, if any.
func (f *fakeZcash) SpenderOf(txid string, index uint32) *Tx {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, _ := ParseDisplayHex(txid)
	op := OutPoint{Hash: h, Index: index}
	for _, b := range f.blocks {
		for _, id := range b.txs {
			for _, in := range f.txs[id].tx.In {
				if in.PrevOut == op {
					return f.txs[id].tx
				}
			}
		}
	}
	return nil
}

// OutputsTo counts the mined outputs that ever paid script.
func (f *fakeZcash) OutputsTo(script []byte) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, b := range f.blocks {
		for _, id := range b.txs {
			for _, o := range f.txs[id].tx.Out {
				if bytes.Equal(o.ScriptPubKey, script) {
					n++
				}
			}
		}
	}
	return n
}

func finalAt(tx *Tx, height uint32) bool {
	if tx.LockTime == 0 || tx.LockTime < height {
		return true
	}
	for _, in := range tx.In {
		if in.Sequence != SequenceFinal {
			return false
		}
	}
	return true
}

// Mine adds n blocks, each taking what the mempool holds that is final, unexpired and still spendable there.
func (f *fakeZcash) Mine(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k := 0; k < n; k++ {
		h := f.tipLocked() + 1
		u := f.utxoLocked()
		var keep, include []string
		for _, id := range f.pool {
			tx := f.txs[id].tx
			if tx.ExpiryHeight != 0 && h > tx.ExpiryHeight {
				continue // expired: dropped
			}
			if !finalAt(tx, h) {
				keep = append(keep, id)
				continue
			}
			ok := true
			for _, in := range tx.In {
				if _, has := u[in.PrevOut]; !has {
					ok = false
				}
			}
			if !ok {
				continue // its inputs are gone: dropped
			}
			for _, in := range tx.In {
				delete(u, in.PrevOut)
			}
			hash, _ := ParseDisplayHex(id)
			for j, o := range tx.Out {
				u[OutPoint{Hash: hash, Index: uint32(j)}] = zout{out: o, height: h}
			}
			include = append(include, id)
		}
		f.pool = keep
		f.appendBlockLocked(include)
	}
}

// Reorg removes the top depth blocks; their transactions go back to the mempool (keep) or vanish.
func (f *fakeZcash) Reorg(depth int, keep bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cut := len(f.blocks) - depth
	var back []string
	for _, b := range f.blocks[cut:] {
		back = append(back, b.txs...)
	}
	f.blocks = f.blocks[:cut]
	if keep {
		f.pool = append(back, f.pool...)
	}
}

// Submit is sendrawtransaction from the test itself (a third party broadcasting a refund file, say).
func (f *fakeZcash) Submit(raw []byte) (string, int, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sendLocked(raw)
}

// sendLocked is zcashd's sendrawtransaction (https://zcash.github.io/rpc/sendrawtransaction.html and
// src/rpc/rawtransaction.cpp): the txid for a new transaction or one already in the mempool, -27 for one already
// in a block, -26 for a verdict against it, -25 for missing inputs.
func (f *fakeZcash) sendLocked(raw []byte) (string, int, string) {
	tx, err := ParseTx(raw)
	if err != nil {
		return "", -22, "TX decode failed"
	}
	id := DisplayHex(tx.TxID())
	f.sent = append(f.sent, id)
	if _, mined := f.minedAtLocked(id); mined {
		return "", RPCTransactionAlreadyInChain, "transaction already in block chain"
	}
	if f.inPoolLocked(id) {
		return id, 0, ""
	}
	u := f.utxoLocked()
	poolSpent := map[OutPoint]bool{}
	for _, pid := range f.pool {
		for _, in := range f.txs[pid].tx.In {
			poolSpent[in.PrevOut] = true
		}
	}
	var in, out uint64
	for i := range tx.In {
		prev, ok := u[tx.In[i].PrevOut]
		if !ok {
			return "", RPCTransactionError, "Missing inputs"
		}
		if poolSpent[tx.In[i].PrevOut] {
			return "", RPCTransactionRejected, "258: txn-mempool-conflict"
		}
		tx.In[i].Value, tx.In[i].ScriptPubKey = prev.out.Value, prev.out.ScriptPubKey
		in += prev.out.Value
	}
	for _, o := range tx.Out {
		out += o.Value
	}
	next := f.tipLocked() + 1
	switch {
	case tx.BranchID != f.branch:
		return "", RPCTransactionRejected, "16: bad-tx-consensus-branch-id"
	case !finalAt(tx, next):
		return "", RPCTransactionRejected, "64: non-final"
	case tx.ExpiryHeight != 0 && tx.ExpiryHeight < next+3:
		return "", RPCTransactionRejected, "16: tx-expiring-soon"
	case out > in || in-out < tx.ConventionalFee():
		return "", RPCTransactionRejected, "66: min relay fee not met"
	}
	for i := range tx.In {
		if err := verifyInput(tx, i); err != nil {
			return "", RPCTransactionRejected, "16: mandatory-script-verify-flag-failed (" + err.Error() + ")"
		}
	}
	if f.onSend != nil {
		if err := f.onSend(f, tx); err != nil {
			f.t.Errorf("TEST INVARIANT at sendrawtransaction of %s: %v", id, err)
			return "", RPCTransactionRejected, "test invariant: " + err.Error()
		}
	}
	f.txs[id] = &ztx{id: id, raw: raw, tx: tx}
	f.pool = append(f.pool, id)
	return id, 0, ""
}

// verifyInput is the interpreter's verdict for the two script shapes of this market.
func verifyInput(tx *Tx, i int) error {
	in := tx.In[i]
	d, err := tx.SigDigest(i, SigHashAll)
	if err != nil {
		return err
	}
	pushes, err := scriptPushes(in.ScriptSig)
	if err != nil {
		return err
	}
	if pkh, ok := P2PKHHash(in.ScriptPubKey); ok {
		if len(pushes) != 2 || Hash160(pushes[1]) != pkh || !VerifySig(pushes[0], pushes[1], d) {
			return errors.New("P2PKH spend does not verify")
		}
		return nil
	}
	spk := in.ScriptPubKey
	if len(spk) != 23 || spk[0] != opHash160 || spk[1] != 20 || spk[22] != opEqual || len(pushes) < 2 {
		return errors.New("not a script this node spends")
	}
	redeem := pushes[len(pushes)-1]
	if h := Hash160(redeem); !bytes.Equal(h[:], spk[2:22]) {
		return errors.New("the redeem script does not hash to the P2SH")
	}
	h, ok := parseHTLCScript(redeem)
	if !ok {
		return errors.New("the redeem script is not the swap script")
	}
	if castToBool(pushes[len(pushes)-2]) {
		if len(pushes) != 5 || len(pushes[2]) != 32 || sha256.Sum256(pushes[2]) != h.Hash || Hash160(pushes[1]) != h.ClaimPkh || !VerifySig(pushes[0], pushes[1], d) {
			return errors.New("claim branch fails")
		}
		return nil
	}
	if len(pushes) != 4 || in.Sequence == SequenceFinal || tx.LockTime < h.CLTV || tx.LockTime >= LockTimeThreshold || Hash160(pushes[1]) != h.RefundPkh || !VerifySig(pushes[0], pushes[1], d) {
		return errors.New("refund branch fails: CHECKLOCKTIMEVERIFY or the key")
	}
	return nil
}

func parseHTLCScript(redeem []byte) (HTLC, bool) {
	var h HTLC
	if len(redeem) < 90 {
		return h, false
	}
	copy(h.Hash[:], redeem[7:39])
	copy(h.ClaimPkh[:], redeem[43:63])
	i := 66
	switch op := redeem[i]; {
	case op >= 0x51 && op <= 0x60:
		h.CLTV = uint32(op - 0x50)
		i++
	case op >= 1 && op <= 5:
		n := int(op)
		if i+1+n > len(redeem) {
			return h, false
		}
		var v uint64
		for k := n - 1; k >= 0; k-- {
			v = v<<8 | uint64(redeem[i+1+k])
		}
		h.CLTV = uint32(v)
		i += 1 + n
	default:
		return h, false
	}
	if i+25 > len(redeem) {
		return h, false
	}
	copy(h.RefundPkh[:], redeem[i+5:i+25])
	return h, bytes.Equal(h.RedeemScript(), redeem)
}

func (f *fakeZcash) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.user != "" {
		if u, p, ok := r.BasicAuth(); !ok || u != f.user || p != f.pass {
			w.WriteHeader(http.StatusUnauthorized) // zcashd: no body on a failed login
			return
		}
	}
	var req struct {
		ID     json.RawMessage   `json:"id"`
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.calls[req.Method]++
	if f.override != nil {
		if body, status, ok := f.override(req.Method); ok {
			f.mu.Unlock()
			w.WriteHeader(status)
			w.Write(body)
			return
		}
	}
	res, code, msg := f.handleLocked(req.Method, req.Params)
	drop := f.dropReply != nil && f.dropReply(req.Method)
	f.mu.Unlock()
	if drop {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("<html>502 bad gateway</html>"))
		return
	}
	env := map[string]interface{}{"id": req.ID}
	if code != 0 {
		w.WriteHeader(http.StatusInternalServerError)
		env["result"], env["error"] = nil, map[string]interface{}{"code": code, "message": msg}
	} else {
		env["result"], env["error"] = res, nil
	}
	json.NewEncoder(w).Encode(env)
}

func num0(p []json.RawMessage, i int) (json.Number, bool) {
	if i >= len(p) {
		return "", false
	}
	var n json.Number
	d := json.NewDecoder(bytes.NewReader(p[i]))
	d.UseNumber()
	if err := d.Decode(&n); err != nil {
		return "", false
	}
	return n, true
}

func str0(p []json.RawMessage, i int) string {
	if i >= len(p) {
		return ""
	}
	var s string
	json.Unmarshal(p[i], &s)
	return s
}

func addrScript(a string) ([]byte, bool) {
	_, kind, h, err := ParseAddress(a)
	if err != nil {
		return nil, false
	}
	if kind == P2PKH {
		return P2PKHScript(h), true
	}
	return append(append([]byte{opHash160}, push(h[:])...), opEqual), true
}

func (f *fakeZcash) handleLocked(method string, p []json.RawMessage) (interface{}, int, string) {
	tip := f.tipLocked()
	switch method {
	case "getblockchaininfo":
		// https://zcash.github.io/rpc/getblockchaininfo.html: chain, blocks, headers, bestblockhash, difficulty,
		// verificationprogress, estimatedheight, initial_block_download_complete, chainwork, size_on_disk,
		// commitments, upgrades {<branch id hex>: {name, activationheight, status, info}}, consensus {chaintip,
		// nextblock}
		net := Testnet
		if f.chain == "main" {
			net = Mainnet
		}
		ups := map[string]interface{}{}
		if f.chain == "regtest" {
			// zebrad on regtest reports its own table (research/regtest/zcash-smoke.log)
			for id, at := range regtestScheduleOf(f) {
				status, name := "active", ""
				if at > tip+1 {
					status = "pending"
				}
				for _, u := range Upgrades {
					if u.BranchID == id {
						name = u.Name
					}
				}
				ups[fmt.Sprintf("%08x", id)] = map[string]interface{}{"name": name, "activationheight": at, "status": status}
			}
		} else {
			for _, u := range Upgrades {
				status := "active"
				if u.height(net) > tip+1 {
					status = "pending"
				}
				ups[fmt.Sprintf("%08x", u.BranchID)] = map[string]interface{}{"name": u.Name, "activationheight": u.height(net), "status": status, "info": "See https://z.cash/upgrade for details."}
			}
		}
		for _, u := range f.pending {
			ups[fmt.Sprintf("%08x", u.BranchID)] = map[string]interface{}{"name": u.Name, "activationheight": u.Height, "status": "pending", "info": ""}
		}
		chain := f.chain
		if chain == "regtest" {
			chain = "test" // what zebrad 6.3.0 answers on regtest
		}
		return map[string]interface{}{"chain": chain, "blocks": tip, "headers": tip, "bestblockhash": f.blocks[len(f.blocks)-1].hash,
			"difficulty": 1.0, "verificationprogress": 1.0, "estimatedheight": tip + f.ahead, "initial_block_download_complete": !f.ibd,
			"chainwork": strings.Repeat("0", 64), "size_on_disk": 1, "commitments": 0, "upgrades": ups,
			"consensus": map[string]interface{}{"chaintip": fmt.Sprintf("%08x", f.branch), "nextblock": fmt.Sprintf("%08x", f.branch)}}, 0, ""

	case "getblockhash":
		// https://zcash.github.io/rpc/getblockhash.html: "hash" (string)
		n, ok := num0(p, 0)
		h, err := strconv.ParseInt(n.String(), 10, 64)
		if ok && err == nil && h == 0 {
			// block 0: regtest's genesis on a regtest node, another (testnet's) otherwise
			if f.chain == "regtest" {
				return RegtestGenesis, 0, ""
			}
			return "05a60a92d99d85997cce3b87616c089f6124d7342af37106edc76126334a2c38", 0, ""
		}
		if !ok || err != nil || h < int64(f.base) || h > int64(tip) {
			return nil, -8, "Block height out of range"
		}
		return f.blocks[uint32(h)-f.base].hash, 0, ""

	case "getblockheader":
		// The fake's blocks are not real headers, so it answers 1,487 bytes that no header check can pass: a companion
		// with the header gate on must refuse to reveal or to fund on this node (TestHeaderGateFailsClosed*).
		want := str0(p, 0)
		for _, b := range f.blocks {
			if b.hash == want {
				return hex.EncodeToString(make([]byte, 1487)), 0, ""
			}
		}
		return nil, -5, "Block not found"

	case "getblock":
		// verbosity 1: {hash, height, tx: [txid, …]}
		want := str0(p, 0)
		for i, b := range f.blocks {
			if b.hash == want {
				return map[string]interface{}{"hash": b.hash, "height": f.base + uint32(i), "tx": append([]string{}, b.txs...)}, 0, ""
			}
		}
		return nil, -5, "Block not found"

	case "getaddressutxos":
		// https://zcash.github.io/rpc/getaddressutxos.html: [{address, txid, outputIndex, script, satoshis, height}]
		var q struct {
			Addresses []string `json:"addresses"`
		}
		if len(p) == 0 || json.Unmarshal(p[0], &q) != nil || len(q.Addresses) != 1 {
			return nil, -8, "Invalid address"
		}
		script, ok := addrScript(q.Addresses[0])
		if !ok {
			return nil, -5, "Invalid address"
		}
		type row struct {
			op OutPoint
			o  zout
		}
		var rows []row
		for op, o := range f.utxoLocked() {
			if bytes.Equal(o.out.ScriptPubKey, script) {
				rows = append(rows, row{op, o})
			}
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].o.height != rows[j].o.height {
				return rows[i].o.height < rows[j].o.height
			}
			return DisplayHex(rows[i].op.Hash) < DisplayHex(rows[j].op.Hash)
		})
		out := []interface{}{}
		for _, r := range rows {
			out = append(out, map[string]interface{}{"address": q.Addresses[0], "txid": DisplayHex(r.op.Hash), "outputIndex": r.op.Index,
				"script": hex.EncodeToString(r.o.out.ScriptPubKey), "satoshis": r.o.out.Value, "height": r.o.height})
		}
		return out, 0, ""

	case "getaddresstxids":
		// https://zcash.github.io/rpc/getaddresstxids.html: ["transactionid", ...], the block range inclusive
		var q struct {
			Addresses []string `json:"addresses"`
			Start     uint32   `json:"start"`
			End       uint32   `json:"end"`
		}
		if len(p) == 0 || json.Unmarshal(p[0], &q) != nil || len(q.Addresses) != 1 {
			return nil, -8, "Invalid address"
		}
		script, ok := addrScript(q.Addresses[0])
		if !ok {
			return nil, -5, "Invalid address"
		}
		prevScript := map[OutPoint][]byte{}
		out := []string{}
		for i, b := range f.blocks {
			h := f.base + uint32(i)
			for _, id := range b.txs {
				tx := f.txs[id].tx
				touches := false
				for _, in := range tx.In {
					if bytes.Equal(prevScript[in.PrevOut], script) {
						touches = true
					}
				}
				hash, _ := ParseDisplayHex(id)
				for j, o := range tx.Out {
					prevScript[OutPoint{Hash: hash, Index: uint32(j)}] = o.ScriptPubKey
					if bytes.Equal(o.ScriptPubKey, script) {
						touches = true
					}
				}
				if touches && h >= q.Start && (q.End == 0 || h <= q.End) {
					out = append(out, id)
				}
			}
		}
		return out, 0, ""

	case "getrawtransaction":
		// https://zcash.github.io/rpc/getrawtransaction.html: verbose 0 the hex; verbose 1 {in_active_chain, hex,
		// txid, size, locktime, expiryheight, vin, vout, blockhash, height, confirmations} — the last three only for
		// a mined transaction (zcashd TxToJSON), and error -5 for one it does not know
		id := strings.ToLower(str0(p, 0))
		z, ok := f.txs[id]
		h, mined := f.minedAtLocked(id)
		if !ok || (!mined && !f.inPoolLocked(id)) {
			return nil, RPCInvalidAddressOrKey, "No such mempool or blockchain transaction. Use gettransaction for wallet transactions."
		}
		if v, _ := num0(p, 1); v.String() == "0" || v == "" {
			return hex.EncodeToString(z.raw), 0, ""
		}
		obj := map[string]interface{}{"hex": hex.EncodeToString(z.raw), "txid": id, "size": len(z.raw), "version": 5,
			"locktime": z.tx.LockTime, "expiryheight": z.tx.ExpiryHeight, "vin": []interface{}{}, "vout": []interface{}{}}
		if mined {
			obj["blockhash"], obj["height"], obj["confirmations"], obj["time"] = f.blocks[h-f.base].hash, h, tip-h+1, 1_700_000_000
		}
		return obj, 0, ""

	case "sendrawtransaction":
		raw, err := hex.DecodeString(str0(p, 0))
		if err != nil {
			return nil, -22, "TX decode failed"
		}
		id, code, msg := f.sendLocked(raw)
		if code != 0 {
			return nil, code, msg
		}
		return id, 0, ""

	case "gettxout":
		// https://zcash.github.io/rpc/gettxout.html: null for a spent or unknown output, else {bestblock,
		// confirmations, value, scriptPubKey {asm, hex, reqSigs, type, addresses}, version, coinbase}
		id := strings.ToLower(str0(p, 0))
		n, _ := num0(p, 1)
		idx, _ := strconv.ParseUint(n.String(), 10, 32)
		hash, err := ParseDisplayHex(id)
		if err != nil {
			return nil, -8, "txid must be hexadecimal"
		}
		o, ok := f.utxoLocked()[OutPoint{Hash: hash, Index: uint32(idx)}]
		if !ok {
			return nil, 0, ""
		}
		typ := "scripthash"
		if _, p2pkh := P2PKHHash(o.out.ScriptPubKey); p2pkh {
			typ = "pubkeyhash"
		}
		return map[string]interface{}{"bestblock": f.blocks[len(f.blocks)-1].hash, "confirmations": tip - o.height + 1,
			"value": float64(o.out.Value) / 1e8, "scriptPubKey": map[string]interface{}{"asm": "", "hex": hex.EncodeToString(o.out.ScriptPubKey), "reqSigs": 1, "type": typ, "addresses": []string{}},
			"version": 5, "coinbase": false}, 0, ""
	}
	return nil, -32601, "Method not found"
}

// ======================================================================================================
// The fake DERO chain: board.bas and swap.bas's rules in memory, at a height the test moves.
// ======================================================================================================

type fakeDero struct {
	mu      sync.Mutex
	height  uint64
	offers  []Offer
	escrows []Escrow
	flaky   func() bool
	txSeq   uint64
	// execDelay, when non-nil, is how many blocks after the height a call is built at the contract executes it: DERO
	// mines a transaction at most 10 blocks after its build height (blockchain.go:748), and board.bas and swap.bas read
	// the height it executes at. The chain moves on by that many blocks before the call runs (tools/xmrswap's
	// fakeChain.execDelay). Called under mu.
	execDelay func() uint64
	// refused counts, per call, the transactions mined and refused by the contract: each is a fee lost
	refused map[string]int
	// holdLock, when set, keeps the next Lock in the pool for that many blocks: it is sent, its answer is lost, and it
	// executes when the chain reaches its height (later). onHold runs as it is sent.
	holdLock uint64
	onHold   func()
	later    []pooled
}

// pooled is a call sent and not yet mined.
type pooled struct {
	at  uint64
	run func(h uint64)
}

func newFakeDero(height uint64) *fakeDero { return &fakeDero{height: height} }

func (c *fakeDero) Height() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.height
}

func (c *fakeDero) Advance(n uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.height += n
	c.mine()
}

// mine executes every pooled call whose height the chain has reached, at that height; called under mu.
func (c *fakeDero) mine() {
	keep := c.later[:0]
	for _, p := range c.later {
		if p.at <= c.height {
			p.run(p.at)
		} else {
			keep = append(keep, p)
		}
	}
	c.later = keep
}

// delay is how far the chain moves on before a contract call executes (execDelay); called under mu.
func (c *fakeDero) delay() {
	if c.execDelay != nil {
		c.height += c.execDelay()
		c.mine()
	}
}

// refuse counts a transaction mined and refused; called under mu.
func (c *fakeDero) refuse(name string) {
	if c.refused == nil {
		c.refused = map[string]int{}
	}
	c.refused[name]++
}

// Refused is how many name transactions were mined and refused by the contract.
func (c *fakeDero) Refused(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refused[name]
}

func (c *fakeDero) Offer(id uint64) Offer {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.offers[id]
}

func (c *fakeDero) Escrows() []Escrow {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Escrow(nil), c.escrows...)
}

func lowerHex64(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == s
}

// board.bas Post
func (c *fakeDero) post(h uint64, signer string, p PostArgs) error {
	switch {
	case len(p.Chain) == 0 || len(p.Chain) > 16 || len(p.WantAddr) == 0 || len(p.WantAddr) > 128:
		return errors.New("Post: strings")
	case len(p.GiveAsset) != 0 && len(p.GiveAsset) != 64:
		return errors.New("Post: asset")
	case p.GiveAmount == 0 || p.WantAmount == 0 || len(p.Hash) != 64:
		return errors.New("Post: amounts or hash")
	case p.Expiry <= h || p.Expiry > h+1_000_000:
		return errors.New("Post: expiry")
	case !lowerHex64(p.Hash):
		return errors.New("Post: hash is not lower-case hex")
	}
	c.offers = append(c.offers, Offer{ID: uint64(len(c.offers)), Maker: signer, Chain: p.Chain, GiveAsset: p.GiveAsset, GiveAmount: p.GiveAmount, WantAmount: p.WantAmount, WantAddr: p.WantAddr, Hash: p.Hash, Expiry: p.Expiry})
	return nil
}

// board.bas v3 Accept: a height, never a Unix time (line 25), and the acceptance deadline ad_<i> = height + aw
// (line 34). The board installs aw = 200, DefaultPolicy's P_lock; the fake uses the test policy's 40.
func (c *fakeDero) accept(h uint64, signer string, id uint64, rp string, cltv uint64) error {
	if id >= uint64(len(c.offers)) {
		return errors.New("Accept: no such offer")
	}
	o := &c.offers[id]
	if o.State != OfferOpen || h >= o.Expiry || len(rp) != 40 || cltv == 0 || cltv >= LockTimeThreshold || signer == o.Maker {
		return errors.New("Accept refused")
	}
	o.State, o.Taker, o.RefundPkh, o.CLTV, o.AcceptDeadline = OfferAccepted, signer, rp, cltv, h+fakeAcceptWindow
	return nil
}

// fakeAcceptWindow is the fake board's aw.
const fakeAcceptWindow = 40

// board.bas v3 Cancel: an open offer at any time; an accepted one from its acceptance deadline or its expiry,
// whichever comes first (line 26).
func (c *fakeDero) cancel(h uint64, signer string, id uint64) error {
	if id >= uint64(len(c.offers)) {
		return errors.New("Cancel: no such offer")
	}
	o := &c.offers[id]
	if signer != o.Maker || o.State == OfferCancelled || (o.State != OfferOpen && h < o.Expiry && h < o.AcceptDeadline) {
		return errors.New("Cancel refused")
	}
	o.State = OfferCancelled
	return nil
}

// swap.bas v2 Lock: at least wmin blocks wide (line 24), and always for a named taker (line 26).
func (c *fakeDero) lock(h uint64, signer, hash, takerRaw string, expiry uint64, asset string, amount uint64) (uint64, error) {
	if len(hash) != 64 || expiry <= h+1 || expiry < h+wminFor(c) || expiry > h+1_000_000 || !lowerHex64(hash) || amount == 0 || takerRaw == "" {
		return 0, errors.New("Lock refused")
	}
	if asset != "" && len(asset) != 64 {
		return 0, errors.New("Lock: asset")
	}
	a := ""
	if asset != "" {
		b, _ := hex.DecodeString(asset)
		a = hex.EncodeToString(b)
	}
	id := uint64(len(c.escrows))
	c.escrows = append(c.escrows, Escrow{ID: id, Maker: signer, Taker: takerRaw, Hash: hash, Asset: a, Amount: amount, Expiry: expiry})
	return id, nil
}

// swap.bas v2 Claim: the secret, before expiry, from any ring-2 signer; the funds go to the taker the lock named
// (lines 29-31), so who sends it decides nothing but who pays the fee.
func (c *fakeDero) claim(h uint64, signer string, id uint64, secret string) error {
	if id >= uint64(len(c.escrows)) {
		return errors.New("Claim: no such lock")
	}
	e := &c.escrows[id]
	raw, err := hex.DecodeString(secret)
	if e.State != EscrowOpen || h >= e.Expiry || len(secret) != 64 || err != nil || len(raw) != 32 || signer == "" {
		return errors.New("Claim refused")
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != e.Hash {
		return errors.New("Claim refused: the secret")
	}
	e.State, e.Secret = EscrowClaimed, secret
	return nil
}

// swap.bas Refund
func (c *fakeDero) refund(h uint64, signer string, id uint64) error {
	if id >= uint64(len(c.escrows)) {
		return errors.New("Refund: no such lock")
	}
	e := &c.escrows[id]
	if e.State != EscrowOpen || h < e.Expiry || signer != e.Maker {
		return errors.New("Refund refused")
	}
	e.State = EscrowRefunded
	return nil
}

// memDero is one party's in-process Dero over a fakeDero.
type memDero struct {
	c     *fakeDero
	addr  string
	me    string
	calls map[string]int
}

func newMemDero(c *fakeDero, addr string) *memDero {
	raw, err := DeroRaw(addr)
	if err != nil {
		panic(err)
	}
	return &memDero{c: c, addr: addr, me: raw, calls: map[string]int{}}
}

func (d *memDero) Height(ctx context.Context) (uint64, error) { return d.c.Height(), nil }
func (d *memDero) StableHeight(ctx context.Context) (uint64, error) {
	h := d.c.Height()
	if h < DeroStableDepth {
		return 0, nil
	}
	return h - DeroStableDepth, nil
}

func (d *memDero) ReadOffer(ctx context.Context, id uint64) (Offer, error) {
	d.c.mu.Lock()
	defer d.c.mu.Unlock()
	if id >= uint64(len(d.c.offers)) {
		return Offer{}, ErrNoSuchOffer
	}
	return d.c.offers[id], nil
}

func (d *memDero) ReadEscrow(ctx context.Context, id uint64) (Escrow, error) {
	d.c.mu.Lock()
	defer d.c.mu.Unlock()
	if id >= uint64(len(d.c.escrows)) {
		return Escrow{}, ErrNoSuchEscrow
	}
	return d.c.escrows[id], nil
}

func (d *memDero) FindOffer(ctx context.Context, makerRaw, hash string) (Offer, bool, error) {
	d.c.mu.Lock()
	defer d.c.mu.Unlock()
	for i := len(d.c.offers) - 1; i >= 0; i-- {
		if o := d.c.offers[i]; o.Maker == makerRaw && o.Hash == hash {
			return o, true, nil
		}
	}
	return Offer{}, false, nil
}

func (d *memDero) HashRows(ctx context.Context, hash string) ([]uint64, error) {
	d.c.mu.Lock()
	defer d.c.mu.Unlock()
	var ids []uint64
	for i := len(d.c.offers) - 1; i >= 0; i-- {
		if d.c.offers[i].Hash == hash {
			ids = append(ids, uint64(i))
		}
	}
	return ids, nil
}

func (d *memDero) FindEscrow(ctx context.Context, makerRaw, takerRaw, hash string) (Escrow, bool, error) {
	d.c.mu.Lock()
	defer d.c.mu.Unlock()
	for i := len(d.c.escrows) - 1; i >= 0; i-- {
		if e := d.c.escrows[i]; e.Maker == makerRaw && e.Taker == takerRaw && e.Hash == hash {
			return e, true, nil
		}
	}
	return Escrow{}, false, nil
}

// do runs one call. A call the estimator would refuse at the height it is built at sends nothing (ErrEstimate). The
// rest execute execDelay blocks later, where a refusal is a transaction mined and refused, its fee lost; a flaky
// answer is lost after the call took effect.
func (d *memDero) do(name string, estimated bool, f func(h uint64) error) (string, error) {
	c := d.c
	c.mu.Lock()
	defer c.mu.Unlock()
	d.calls[name]++
	c.txSeq++
	sum := sha256.Sum256([]byte("fake dero tx " + strconv.FormatUint(c.txSeq, 10)))
	txid := hex.EncodeToString(sum[:])
	if estimated {
		// the estimator judges the call and changes nothing
		offers, escrows := append([]Offer(nil), c.offers...), append([]Escrow(nil), c.escrows...)
		err := f(c.height)
		c.offers, c.escrows = offers, escrows
		if err != nil {
			return "", fmt.Errorf("%s %w: %v", name, ErrEstimate, err)
		}
	}
	c.delay()
	if err := f(c.height); err != nil {
		c.refuse(name)
		return txid, fmt.Errorf("%s %s was mined but the contract refused it: %v", name, txid, err)
	}
	if c.flaky != nil && c.flaky() {
		return "", errors.New("rpc timeout after the send")
	}
	return txid, nil
}

func (d *memDero) Post(ctx context.Context, p PostArgs) (string, uint64, error) {
	if o, ok, _ := d.FindOffer(ctx, d.me, p.Hash); ok {
		return "", o.ID, nil
	}
	var id uint64
	txid, err := d.do("Post", p.Expiry <= estimableExpiry, func(h uint64) error {
		id = uint64(len(d.c.offers))
		return d.c.post(h, d.me, p)
	})
	return txid, id, err
}

func (d *memDero) Accept(ctx context.Context, id uint64, rp string, cltv uint64) (string, error) {
	if o, err := d.ReadOffer(ctx, id); err == nil && o.State == OfferAccepted && o.Taker == d.me && o.RefundPkh == rp && o.CLTV == cltv {
		return "", nil
	}
	return d.do("Accept", true, func(h uint64) error { return d.c.accept(h, d.me, id, rp, cltv) })
}

func (d *memDero) Cancel(ctx context.Context, id uint64) (string, error) {
	return d.do("Cancel", false, func(h uint64) error { return d.c.cancel(h, d.me, id) })
}

func (d *memDero) Lock(ctx context.Context, hash, takerAddr string, expiry uint64, asset string, amount uint64) (string, uint64, error) {
	takerRaw, err := DeroRaw(takerAddr)
	if err != nil {
		return "", 0, err
	}
	if e, ok, _ := d.FindEscrow(ctx, d.me, takerRaw, hash); ok {
		return "", e.ID, nil
	}
	c := d.c
	c.mu.Lock()
	if hold := c.holdLock; hold > 0 {
		c.holdLock = 0
		d.calls["Lock"]++
		me := d.me
		c.later = append(c.later, pooled{at: c.height + hold, run: func(h uint64) {
			if _, err := c.lock(h, me, hash, takerRaw, expiry, asset, amount); err != nil {
				c.refuse("Lock")
			}
		}})
		onHold := c.onHold
		c.mu.Unlock()
		if onHold != nil {
			onHold()
		}
		return "", 0, errors.New("the Lock was sent and has not been seen mined")
	}
	c.mu.Unlock()
	var id uint64
	txid, err := d.do("Lock", expiry <= estimableExpiry, func(h uint64) error {
		var err error
		id, err = d.c.lock(h, d.me, hash, takerRaw, expiry, asset, amount)
		return err
	})
	return txid, id, err
}

func (d *memDero) Claim(ctx context.Context, id uint64, secret string) (string, error) {
	return d.do("Claim", true, func(h uint64) error { return d.c.claim(h, d.me, id, secret) })
}

func (d *memDero) Refund(ctx context.Context, id uint64) (string, error) {
	return d.do("Refund", false, func(h uint64) error { return d.c.refund(h, d.me, id) })
}

// ======================================================================================================
// The fake DERO daemon and wallet over HTTP, for DeroRPC: derod's request and answer shapes, with the estimator
// running the contract at height 0 (the fact the fixed fee exists for) and GetSC answering named keys.
// ======================================================================================================

type deroDaemon struct {
	c        *fakeDero
	addr     string
	mu       sync.Mutex
	requests []map[string]interface{}
	// extra are keys a later contract version stores; nothing here asks for them
	extra map[string]string
}

func (d *deroDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Method string                 `json:"method"`
		Params map[string]interface{} `json:"params"`
	}
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	d.mu.Lock()
	d.requests = append(d.requests, map[string]interface{}{"method": req.Method, "params": req.Params})
	d.mu.Unlock()
	res, rpcErr := d.handle(req.Method, req.Params)
	env := map[string]interface{}{"jsonrpc": "2.0", "id": "1"}
	if rpcErr != "" {
		env["error"] = map[string]interface{}{"code": -32000, "message": rpcErr}
	} else {
		env["result"] = res
	}
	json.NewEncoder(w).Encode(env)
}

func jsonU(v interface{}) uint64 {
	switch x := v.(type) {
	case json.Number:
		n, _ := strconv.ParseUint(x.String(), 10, 64)
		return n
	case float64:
		return uint64(x)
	}
	return 0
}

func jsonS(v interface{}) string { s, _ := v.(string); return s }

func scArgs(p map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	list, _ := p["sc_rpc"].([]interface{})
	for _, a := range list {
		m, _ := a.(map[string]interface{})
		out[jsonS(m["name"])] = m["value"]
	}
	return out
}

// run executes the call described by params at height h for the wallet's signer.
func (d *deroDaemon) run(h uint64, p map[string]interface{}) string {
	a := scArgs(p)
	signer, _ := DeroRaw(d.addr)
	c := d.c
	var err error
	switch jsonS(a["entrypoint"]) {
	case "Post":
		err = c.post(h, signer, PostArgs{Chain: jsonS(a["chain"]), GiveAsset: jsonS(a["giveAsset"]), GiveAmount: jsonU(a["giveAmount"]), WantAmount: jsonU(a["wantAmount"]), WantAddr: jsonS(a["wantAddr"]), Hash: jsonS(a["hash"]), Expiry: jsonU(a["expiry"])})
	case "Accept":
		err = c.accept(h, signer, jsonU(a["id"]), jsonS(a["refundPkh"]), jsonU(a["cltv"]))
	case "Cancel":
		err = c.cancel(h, signer, jsonU(a["id"]))
	case "Lock":
		var burn uint64
		asset := ""
		if xf, ok := p["transfers"].([]interface{}); ok {
			for _, t := range xf {
				m, _ := t.(map[string]interface{})
				burn += jsonU(m["burn"])
				if s := jsonS(m["scid"]); s != "" {
					asset = s
				}
			}
		}
		takerRaw, rerr := DeroRaw(jsonS(a["taker"]))
		if rerr != nil {
			return "Lock: taker"
		}
		if jsonS(a["asset"]) != asset {
			return "Lock: the asset argument and the transfer's scid differ"
		}
		_, err = c.lock(h, signer, jsonS(a["hash"]), takerRaw, jsonU(a["expiry"]), asset, burn)
	case "Claim":
		err = c.claim(h, signer, jsonU(a["id"]), jsonS(a["secret"]))
	case "Refund":
		err = c.refund(h, signer, jsonU(a["id"]))
	default:
		return "unknown entrypoint"
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

// scValue is one variable as derod's GetSC renders it under keysstring: a number as decimal text, a string as the
// hex of its bytes (upper case here, to prove the reader normalises), "NOT AVAILABLE err: …" for a key never set.
func (d *deroDaemon) scValue(scid, k string) string {
	c := d.c
	if v, ok := d.extra[k]; ok {
		return v
	}
	hx := func(s string) string { return strings.ToUpper(hex.EncodeToString([]byte(s))) }
	rawHex := func(s string) string { b, _ := hex.DecodeString(s); return strings.ToUpper(hex.EncodeToString(b)) }
	na := "NOT AVAILABLE err: leaf not found"
	if k == "n" {
		if scid == testBoard {
			return strconv.Itoa(len(c.offers))
		}
		return strconv.Itoa(len(c.escrows))
	}
	cut := strings.LastIndex(k, "_")
	if cut < 0 {
		return na
	}
	id, err := strconv.ParseUint(k[cut+1:], 10, 64)
	if err != nil {
		return na
	}
	u := func(v uint64) string { return strconv.FormatUint(v, 10) }
	if scid == testBoard {
		if id >= uint64(len(c.offers)) {
			return na
		}
		o := c.offers[id]
		switch k[:cut+1] {
		case "m_":
			return rawHex(o.Maker)
		case "c_":
			return hx(o.Chain)
		case "ga_":
			return hx(o.GiveAsset)
		case "gv_":
			return u(o.GiveAmount)
		case "wv_":
			return u(o.WantAmount)
		case "wa_":
			return hx(o.WantAddr)
		case "h_":
			return hx(o.Hash)
		case "e_":
			return u(o.Expiry)
		case "st_":
			return u(o.State)
		case "t_":
			if o.State == OfferOpen {
				return na
			}
			return rawHex(o.Taker)
		case "rp_":
			if o.State == OfferOpen {
				return na
			}
			return hx(o.RefundPkh)
		case "cl_":
			if o.State == OfferOpen {
				return na
			}
			return u(o.CLTV)
		}
		return na
	}
	if id >= uint64(len(c.escrows)) {
		return na
	}
	e := c.escrows[id]
	switch k[:cut+1] {
	case "m_":
		return rawHex(e.Maker)
	case "t_":
		return rawHex(e.Taker)
	case "h_":
		return hx(e.Hash)
	case "a_":
		return rawHex(e.Asset)
	case "v_":
		return u(e.Amount)
	case "e_":
		return u(e.Expiry)
	case "st_":
		return u(e.State)
	case "x_":
		if e.Secret == "" {
			return na
		}
		return hx(e.Secret)
	}
	return na
}

func (d *deroDaemon) handle(method string, p map[string]interface{}) (interface{}, string) {
	c := d.c
	switch method {
	case "DERO.GetHeight":
		h := c.Height()
		return map[string]interface{}{"height": h, "stableheight": h - DeroStableDepth, "topoheight": h}, ""
	case "DERO.GetInfo":
		return map[string]interface{}{"testnet": true, "network": "Simulator"}, ""
	case "DERO.GetRandomAddress":
		return map[string]interface{}{"address": []string{otherAddr(0x42)}}, ""
	case "DERO.GetGasEstimate":
		// the estimator runs the contract at height 0 and changes nothing
		c.mu.Lock()
		savedO, savedE := append([]Offer(nil), c.offers...), append([]Escrow(nil), c.escrows...)
		refused := d.run(0, p)
		c.offers, c.escrows = savedO, savedE
		c.mu.Unlock()
		if refused != "" {
			return nil, refused
		}
		return map[string]interface{}{"gascompute": 100, "gasstorage": 500}, ""
	case "DERO.GetTransaction":
		hashes, _ := p["txs_hashes"].([]interface{})
		var txs []map[string]interface{}
		for range hashes {
			txs = append(txs, map[string]interface{}{"valid_block": "block", "block_height": c.Height() - 1, "in_pool": false})
		}
		return map[string]interface{}{"txs": txs}, ""
	case "DERO.GetSC":
		c.mu.Lock()
		defer c.mu.Unlock()
		scid := jsonS(p["scid"])
		asked, _ := p["keysstring"].([]interface{})
		vals := make([]string, 0, len(asked))
		for _, k := range asked {
			vals = append(vals, d.scValue(scid, jsonS(k)))
		}
		return map[string]interface{}{"valuesstring": vals, "balance": 0}, ""
	case "GetAddress":
		return map[string]interface{}{"address": d.addr}, ""
	case "GetHeight":
		return map[string]interface{}{"height": c.Height()}, ""
	case "transfer":
		if jsonU(p["ringsize"]) != 2 {
			return nil, "ringsize must be 2: SIGNER() is real only there"
		}
		c.mu.Lock()
		d.run(c.height, p) // a refused call is still mined, and its fee spent
		c.txSeq++
		c.height++ // mined into this block; the next one follows, as waitTx wants
		sum := sha256.Sum256([]byte("fake dero http tx " + strconv.FormatUint(c.txSeq, 10)))
		c.mu.Unlock()
		return map[string]interface{}{"txid": hex.EncodeToString(sum[:])}, ""
	}
	return nil, "no such method: " + method
}

func (d *deroDaemon) last(method string) map[string]interface{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := len(d.requests) - 1; i >= 0; i-- {
		if d.requests[i]["method"] == method {
			p, _ := d.requests[i]["params"].(map[string]interface{})
			return p
		}
	}
	return nil
}

func newDeroClient(t *testing.T, c *fakeDero, addr string) (*DeroRPC, *deroDaemon) {
	d := &deroDaemon{c: c, addr: addr}
	srv := httptest.NewServer(d)
	t.Cleanup(srv.Close)
	rpc := NewDeroRPC(srv.URL, srv.URL, testBoard, testEscrow)
	rpc.Poll, rpc.Timeout = time.Millisecond, 5*time.Second
	return rpc, d
}
