package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"veil/tools/netcfg"
	"veil/tools/xmrswap"
	"veil/tools/zecswap"
)

// ---- the pass, as founders.bas stores it ----

// fakePasses answers every snapshot with what it holds when read: hu_<holder> units and key+<holder> addresses.
type fakePasses struct {
	stable uint64
	units  map[string]uint64
	addrs  map[string]string // "xm_<holder>" / "zc_<holder>" → address
}

func newFakePasses() *fakePasses {
	return &fakePasses{stable: 100, units: map[string]uint64{}, addrs: map[string]string{}}
}

func (f *fakePasses) Stable() (uint64, error) { return f.stable, nil }
func (f *fakePasses) Snapshot(h uint64, key string, extra []string) (snapshot, error) {
	s := snapshot{Height: h, Topo: int64(h), Units: map[string]uint64{}, Addrs: map[string]string{}}
	names := append([]string{}, extra...)
	for a, u := range f.units {
		if u > 0 {
			s.Units[a], s.Hst = u, s.Hst+u
			names = append(names, a)
		}
	}
	for _, a := range names {
		if v, ok := f.addrs[key+a]; ok {
			s.Addrs[a] = v
		}
	}
	return s, nil
}

// ---- a coin in memory, for the rules ----

// memNet: addresses starting "good" are payable; a relayed transaction pays its destinations once.
type memNet struct {
	inc      []income
	minPart  uint64
	maxDests int
	txs      map[string]*memTx
	byHolder map[string]uint64 // what each holder received, over every transaction
	seq      int
}

type memTx struct {
	dests   []dest
	relayed bool
	conf    int
}

func newMemNet() *memNet {
	return &memNet{minPart: 1, maxDests: 3, txs: map[string]*memTx{}, byHolder: map[string]uint64{}}
}

func (m *memNet) Name() string { return "MEM" }
func (m *memNet) Income(context.Context) ([]income, error) {
	return append([]income(nil), m.inc...), nil
}
func (m *memNet) MinPart() uint64 { return m.minPart }
func (m *memNet) MaxDests() int   { return m.maxDests }
func (m *memNet) Check(a string) error {
	if strings.HasPrefix(a, "good") {
		return nil
	}
	return errors.New("not payable")
}
func (m *memNet) Build(_ context.Context, ds []dest, _ func(string) bool) (built, error) {
	m.seq++
	id := fmt.Sprintf("%064x", m.seq)
	m.txs[id] = &memTx{dests: ds}
	return built{TxID: id, Blob: "b" + id}, nil
}
func (m *memNet) Broadcast(_ context.Context, p *payout) error {
	t := m.txs[p.TxID]
	if !t.relayed {
		t.relayed = true
		for _, d := range t.dests {
			m.byHolder[d.Holder] += d.Amt
		}
	}
	return nil
}
func (m *memNet) State(_ context.Context, p *payout) (txState, error) {
	t := m.txs[p.TxID]
	switch {
	case t == nil || !t.relayed:
		return txUnknown, nil
	case t.conf >= incomeConf:
		return txMined, nil
	}
	return txPending, nil
}
func (m *memNet) mine() {
	for _, t := range m.txs {
		if t.relayed {
			t.conf += incomeConf
		}
	}
}

// run is one process lifetime: replay the journal, run one round, close it.
func run(t *testing.T, n payNet, pr passReader, key, path string, share uint64) (*ledger, error) {
	t.Helper()
	l, err := openLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.close()
	p := &assetPayer{net: n, pr: pr, l: l, key: key, share: share, every: 10, out: io.Discard}
	return l, p.round(context.Background())
}

// conserved: paid + in flight + owed + carried + the treasury's part = income.
func conserved(t *testing.T, l *ledger, n payNet) {
	t.Helper()
	owed, carried := l.balances(n)
	if got := l.Paid + l.inFlight() + owed + carried + l.Treasury; got != l.Income {
		t.Fatalf("paid %d + in flight %d + owed %d + carried %d + treasury %d = %d, income %d", l.Paid, l.inFlight(), owed, carried, l.Treasury, got, l.Income)
	}
}

// TestPayoutConservationPerAsset runs 80 periods for each asset with random fees, registrations, withdrawals and
// addresses (some missing, some not payable), each round in a fresh process, and checks after every one that nothing
// is created or lost, and at the end that every holder was paid exactly what the period rule, worked here on its own,
// gives it.
func TestPayoutConservationPerAsset(t *testing.T) {
	for _, key := range []string{"xm_", "zc_"} {
		rng := rand.New(rand.NewSource(int64(len(key)) + 7))
		n, pr := newMemNet(), newFakePasses()
		n.minPart = 50
		path := filepath.Join(t.TempDir(), key+"journal.jsonl")
		const share = 3333
		holders := []string{"deroA", "deroB", "deroC", "deroD", "deroE"}
		model := map[string]uint64{} // every holder's parts, by the rule
		var carry uint64
		for period := 1; period <= 80; period++ {
			for i := rng.Intn(3); i > 0; i-- {
				n.inc = append(n.inc, income{ID: fmt.Sprintf("fee-%d-%d", period, i), Amt: uint64(rng.Intn(50_000))})
			}
			for _, h := range holders {
				switch rng.Intn(6) {
				case 0:
					delete(pr.units, h) // withdrawn; what it is owed stays owed
				case 1:
					pr.units[h] = uint64(1 + rng.Intn(4))
				case 2:
					pr.addrs[key+h] = "good-" + h
				case 3:
					pr.addrs[key+h] = "bad-" + h
				}
			}
			if rng.Intn(5) == 0 {
				pr.units = map[string]uint64{} // no pass registered
			}
			// the rule, worked by the test, from what the pass and the coin hold at the period's end
			var fresh uint64
			for _, in := range n.inc {
				if strings.HasPrefix(in.ID, fmt.Sprintf("fee-%d-", period)) {
					fresh += in.Amt
				}
			}
			var hst uint64
			for _, u := range pr.units {
				hst += u
			}
			pool := carry + shareOf(fresh, share)
			if hst == 0 {
				carry = pool
			} else {
				for h, u := range pr.units {
					model[h] += pool / hst * u
				}
				carry = pool % hst
			}
			pr.stable = uint64(period) * 10
			l, err := run(t, n, pr, key, path, share)
			if err != nil {
				t.Fatalf("%s period %d: %v", key, period, err)
			}
			conserved(t, l, n)
			if l.Carry != carry {
				t.Fatalf("%s period %d: carried %d, the rule gives %d", key, period, l.Carry, carry)
			}
			n.mine()
		}
		for _, h := range holders {
			pr.addrs[key+h] = "good-" + h
		}
		for i := 0; i < 6; i++ { // the last addresses are read at a new period, then paid in batches of MaxDests
			pr.stable += 10
			if _, err := run(t, n, pr, key, path, share); err != nil {
				t.Fatal(err)
			}
			n.mine()
		}
		l, _ := run(t, n, pr, key, path, share)
		conserved(t, l, n)
		for _, h := range holders {
			if n.byHolder[h] != model[h] || l.Due[h] != 0 {
				t.Errorf("%s %s received %d (still due %d), the rule gives %d", key, h, n.byHolder[h], l.Due[h], model[h])
			}
		}
	}
}

// While no pass is registered the passes' part is held, and the first period with a registered pass divides all of
// it among the passes registered then.
func TestPayoutHoldsWhileNoPassIsRegistered(t *testing.T) {
	n, pr := newMemNet(), newFakePasses()
	path := filepath.Join(t.TempDir(), "j.jsonl")
	n.inc = []income{{ID: "f1", Amt: 1000}}
	l, err := run(t, n, pr, "xm_", path, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if l.Carry != 500 || l.Treasury != 500 || len(l.Due) != 0 || len(n.txs) != 0 {
		t.Fatalf("no pass registered: carried %d, treasury %d, due %v, %d payouts; want 500, 500, none, none", l.Carry, l.Treasury, l.Due, len(n.txs))
	}
	pr.units = map[string]uint64{"deroA": 1, "deroB": 3}
	pr.addrs = map[string]string{"xm_deroA": "good-a", "xm_deroB": "good-b"}
	pr.stable = 110 // the next period; no new fee
	if _, err := run(t, n, pr, "xm_", path, 5000); err != nil {
		t.Fatal(err)
	}
	n.mine()
	l, _ = run(t, n, pr, "xm_", path, 5000)
	if n.byHolder["deroA"] != 125 || n.byHolder["deroB"] != 375 || l.Carry != 0 || l.Paid != 500 {
		t.Fatalf("the held part went A %d, B %d (carry %d, paid %d); want 125, 375, 0, 500", n.byHolder["deroA"], n.byHolder["deroB"], l.Carry, l.Paid)
	}
	conserved(t, l, n)
}

// A holder with no address, or one this network cannot pay, keeps its part owed; stored later, it is paid all of it.
// A holder who withdrew is still paid what it earned.
func TestPayoutKeepsAPartOwedWithoutAnAddress(t *testing.T) {
	n, pr := newMemNet(), newFakePasses()
	path := filepath.Join(t.TempDir(), "j.jsonl")
	pr.units = map[string]uint64{"deroA": 1, "deroB": 1, "deroC": 1}
	pr.addrs = map[string]string{"zc_deroA": "good-a", "zc_deroC": "t1-mainnet-on-a-testnet"}
	n.inc = []income{{ID: "f1", Amt: 600}}
	if _, err := run(t, n, pr, "zc_", path, 10000); err != nil {
		t.Fatal(err)
	}
	n.mine()
	l, _ := run(t, n, pr, "zc_", path, 10000)
	owed, _ := l.balances(n)
	if n.byHolder["deroA"] != 200 || n.byHolder["deroB"]+n.byHolder["deroC"] != 0 || owed != 400 {
		t.Fatalf("A received %d, B+C %d, owed %d; want 200, 0, 400", n.byHolder["deroA"], n.byHolder["deroB"]+n.byHolder["deroC"], owed)
	}
	delete(pr.units, "deroB") // B withdraws, then stores an address
	pr.addrs["zc_deroB"] = "good-b"
	pr.stable = 110
	if _, err := run(t, n, pr, "zc_", path, 10000); err != nil {
		t.Fatal(err)
	}
	n.mine()
	l, _ = run(t, n, pr, "zc_", path, 10000)
	owed, _ = l.balances(n)
	if n.byHolder["deroB"] != 200 || n.byHolder["deroC"] != 0 || owed != 200 {
		t.Fatalf("after B stored an address: B %d, C %d, owed %d; want 200, 0, 200", n.byHolder["deroB"], n.byHolder["deroC"], owed)
	}
	conserved(t, l, n)
}

// A part below the network's minimum is carried, not paid, and joins the holder's next part; the undivided
// remainder of a period (less than hst units) is carried to the next.
func TestPayoutCarriesDust(t *testing.T) {
	n, pr := newMemNet(), newFakePasses()
	n.minPart = 1000
	path := filepath.Join(t.TempDir(), "j.jsonl")
	pr.units = map[string]uint64{"deroA": 1, "deroB": 2}
	pr.addrs = map[string]string{"xm_deroA": "good-a", "xm_deroB": "good-b"}
	n.inc = []income{{ID: "f1", Amt: 2000}} // 1000 to the passes: 333 per unit, 1 carried
	l, err := run(t, n, pr, "xm_", path, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if l.Due["deroA"] != 333 || l.Due["deroB"] != 666 || l.Carry != 1 || len(n.txs) != 0 {
		t.Fatalf("due A %d, B %d, carry %d, %d payouts; want 333, 666, 1 and nothing paid below 1000", l.Due["deroA"], l.Due["deroB"], l.Carry, len(n.txs))
	}
	n.inc = append(n.inc, income{ID: "f2", Amt: 4000}) // 2000 + 1 carried = 2001: 667 per unit
	pr.stable = 110
	if _, err := run(t, n, pr, "xm_", path, 5000); err != nil {
		t.Fatal(err)
	}
	n.mine()
	l, _ = run(t, n, pr, "xm_", path, 5000)
	if n.byHolder["deroA"] != 1000 || n.byHolder["deroB"] != 2000 || l.Carry != 0 {
		t.Fatalf("A received %d, B %d, carry %d; want 1000 (333 + 667), 2000, 0", n.byHolder["deroA"], n.byHolder["deroB"], l.Carry)
	}
	conserved(t, l, n)
}

// ---- a monero-wallet-rpc over HTTP, with --rpc-login ----

type fwTx struct {
	id      string
	dests   []dest
	relayed bool
	conf    uint64
}

type fakeWallet struct {
	mu       sync.Mutex
	user     string
	pass     string
	treasury string // subaddress 0/1
	in       []income
	inConf   uint64
	built    map[string]*fwTx // tx_metadata → tx
	txs      map[string]*fwTx // relayed, by id
	received map[string]uint64
	relays   int
	seq      int
}

func newFakeWallet(treasury string) *fakeWallet {
	return &fakeWallet{user: "treasury", pass: "pw-xmr-7Qd", treasury: treasury, built: map[string]*fwTx{}, txs: map[string]*fwTx{}, received: map[string]uint64{}}
}

func (f *fakeWallet) authorized(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Digest ") {
		return false
	}
	p := authParams(h[7:])
	ha1, ha2 := md5hex(f.user+":monero-rpc:"+f.pass), md5hex("POST:"+p["uri"])
	return p["username"] == f.user && p["nonce"] == "n0nce" && p["response"] == md5hex(ha1+":n0nce:"+p["nc"]+":"+p["cnonce"]+":auth:"+ha2)
}

func (f *fakeWallet) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !f.authorized(r) {
		w.Header().Add("WWW-Authenticate", `Digest qop="auth",algorithm=MD5,realm="monero-rpc",nonce="n0nce",stale=false`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var req struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	f.mu.Lock()
	defer f.mu.Unlock()
	res, code, msg := f.handle(req.Method, req.Params)
	if code != 0 {
		json.NewEncoder(w).Encode(map[string]interface{}{"id": "0", "jsonrpc": "2.0", "error": map[string]interface{}{"code": code, "message": msg}})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"id": "0", "jsonrpc": "2.0", "result": res})
}

func (f *fakeWallet) handle(method string, raw json.RawMessage) (interface{}, int, string) {
	var p struct {
		Address      string                   `json:"address"`
		In           bool                     `json:"in"`
		Out          bool                     `json:"out"`
		Destinations []map[string]interface{} `json:"destinations"`
		DoNotRelay   bool                     `json:"do_not_relay"`
		GetMeta      bool                     `json:"get_tx_metadata"`
		Hex          string                   `json:"hex"`
		TxID         string                   `json:"txid"`
	}
	json.Unmarshal(raw, &p)
	switch method {
	case "refresh":
		return map[string]interface{}{"blocks_fetched": 0}, 0, ""
	case "get_address_index":
		if p.Address != f.treasury {
			return nil, -2, "Address doesn't belong to the wallet"
		}
		return map[string]interface{}{"index": map[string]uint64{"major": 0, "minor": 1}}, 0, ""
	case "get_transfers":
		res := map[string]interface{}{}
		if p.In {
			var in []map[string]interface{}
			for _, t := range f.in {
				in = append(in, map[string]interface{}{"txid": t.ID, "amount": t.Amt, "confirmations": f.inConf, "locked": f.inConf < 10,
					"address": f.treasury, "subaddr_index": map[string]uint64{"major": 0, "minor": 1}})
			}
			res["in"] = in
		}
		if p.Out {
			var out, pend []map[string]interface{}
			for _, t := range f.txs {
				if t.conf == 0 {
					pend = append(pend, map[string]interface{}{"txid": t.id})
				} else {
					out = append(out, map[string]interface{}{"txid": t.id})
				}
			}
			res["out"], res["pending"] = out, pend
		}
		return res, 0, ""
	case "transfer":
		if !p.DoNotRelay || !p.GetMeta {
			return nil, -1, "this fake builds only do_not_relay transfers"
		}
		f.seq++
		t := &fwTx{id: fmt.Sprintf("%064x", 0xabc0000+f.seq)}
		for _, d := range p.Destinations {
			t.dests = append(t.dests, dest{Addr: d["address"].(string), Amt: uint64(d["amount"].(float64))})
		}
		meta := hex.EncodeToString([]byte("meta:" + t.id))
		f.built[meta] = t
		return map[string]interface{}{"tx_hash": t.id, "tx_metadata": meta, "fee": 30_000_000}, 0, ""
	case "relay_tx":
		t := f.built[p.Hex]
		if t == nil {
			return nil, -1, "Failed to parse tx metadata."
		}
		if t.relayed {
			return nil, -4, "Failed to commit tx (double spend)" // what a daemon says to the same key images twice
		}
		t.relayed = true
		f.txs[t.id] = t
		f.relays++
		for _, d := range t.dests {
			f.received[d.Addr] += d.Amt
		}
		return map[string]interface{}{"tx_hash": t.id}, 0, ""
	case "get_transfer_by_txid":
		t := f.txs[p.TxID]
		if t == nil {
			return nil, -8, "Transaction not found."
		}
		typ := "out"
		if t.conf == 0 {
			typ = "pending"
		}
		return map[string]interface{}{"transfer": map[string]interface{}{"type": typ, "confirmations": t.conf, "txid": t.id}}, 0, ""
	}
	return nil, -32601, "Method not found"
}

func (f *fakeWallet) mine(n uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inConf += n
	for _, t := range f.txs {
		t.conf += n
	}
}

// xmrAddr is a valid Monero address of net made from the scalar s (both keys s·G), or its subaddress form.
func xmrAddr(t *testing.T, s byte, net xmrswap.Network) string {
	t.Helper()
	sc, err := xmrswap.ParseScalar(hex.EncodeToString(append([]byte{s}, make([]byte, 31)...)))
	if err != nil {
		t.Fatal(err)
	}
	a, err := xmrswap.Address(sc.Public(), sc.Public(), net)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// ---- a Zcash node in process: it checks every signature and fee it is sent ----

type fzOut struct {
	addr   string
	script []byte
	value  uint64
	height uint32
}

type fakeZcash struct {
	mu       sync.Mutex
	height   uint32
	utxos    map[string]fzOut
	mempool  map[string][]byte
	mined    map[string]uint32
	raws     map[string][]byte
	received map[string]uint64
	accepted int
	drop     bool // refuse every broadcast
	seq      int
}

func newFakeZcash() *fakeZcash {
	return &fakeZcash{height: 200, utxos: map[string]fzOut{}, mempool: map[string][]byte{}, mined: map[string]uint32{}, raws: map[string][]byte{}, received: map[string]uint64{}}
}

func (z *fakeZcash) ChainInfo(context.Context) (zecswap.ChainInfo, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	return zecswap.ChainInfo{Chain: "test", Net: zecswap.Testnet, Regtest: true, Height: z.height, NextBranch: zecswap.BranchNU63, TipBranch: zecswap.BranchNU63,
		Upgrades: []zecswap.NodeUpgrade{{Name: "NU6.3", BranchID: zecswap.BranchNU63, Height: 6, Status: "active"}}}, nil
}
func (z *fakeZcash) BlockHash(context.Context, uint32) (string, error) {
	return "", errors.New("not served")
}
func (z *fakeZcash) AddressTxIDs(context.Context, string, uint32, uint32) ([]string, error) {
	return nil, errors.New("not served")
}
func (z *fakeZcash) AddressUTXOs(_ context.Context, addr string) ([]zecswap.UTXO, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	var out []zecswap.UTXO
	for id, o := range z.utxos {
		if o.addr == addr {
			txid, idx, _ := strings.Cut(id, ":")
			var i uint32
			fmt.Sscan(idx, &i)
			out = append(out, zecswap.UTXO{Address: addr, TxID: txid, Index: i, Script: o.script, Value: o.value, Height: o.height})
		}
	}
	return out, nil
}
func (z *fakeZcash) RawTx(_ context.Context, id string) (zecswap.RawTx, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if raw, ok := z.mempool[id]; ok {
		return zecswap.RawTx{TxID: id, Raw: raw, Verified: true}, nil
	}
	if h, ok := z.mined[id]; ok {
		return zecswap.RawTx{TxID: id, Raw: z.raws[id], Mined: true, Height: h, Confirmations: z.height - h + 1, Verified: true}, nil
	}
	return zecswap.RawTx{}, zecswap.ErrTxNotFound
}
func (z *fakeZcash) TxOut(_ context.Context, id string, i uint32) (*zecswap.TxOutInfo, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if o, ok := z.utxos[outpoint(id, i)]; ok {
		return &zecswap.TxOutInfo{Script: o.script, Confirmations: z.height - o.height + 1}, nil
	}
	return nil, nil
}
func (z *fakeZcash) SendRawTx(_ context.Context, raw []byte) (string, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	id, _ := zecswap.ComputeTxID(raw)
	if z.drop {
		return "", &zecswap.RPCError{Method: "sendrawtransaction", Code: -26, Message: "dropped by the test"}
	}
	if _, ok := z.mempool[id]; ok {
		return "", &zecswap.RPCError{Method: "sendrawtransaction", Code: -1, Message: "transaction already exists in mempool"}
	}
	if _, ok := z.mined[id]; ok {
		return "", &zecswap.RPCError{Method: "sendrawtransaction", Code: -1, Message: "transaction was committed to the best chain"}
	}
	tx, err := zecswap.ParseTx(raw)
	if err != nil {
		return "", err
	}
	if tx.ExpiryHeight != 0 && z.height+1 > tx.ExpiryHeight {
		return "", &zecswap.RPCError{Method: "sendrawtransaction", Code: -25, Message: "expired"}
	}
	for i := range tx.In {
		op := outpoint(zecswap.DisplayHex(tx.In[i].PrevOut.Hash), tx.In[i].PrevOut.Index)
		o, ok := z.utxos[op]
		if !ok {
			return "", &zecswap.RPCError{Method: "sendrawtransaction", Code: -25, Message: "missing input " + op}
		}
		for mid, mraw := range z.mempool {
			mtx, _ := zecswap.ParseTx(mraw)
			for _, in := range mtx.In {
				if in.PrevOut == tx.In[i].PrevOut {
					return "", &zecswap.RPCError{Method: "sendrawtransaction", Code: -25, Message: "double spend of " + op + " by " + mid}
				}
			}
		}
		tx.In[i].Value, tx.In[i].ScriptPubKey = o.value, o.script
	}
	for i, in := range tx.In {
		ss := in.ScriptSig
		sig, pub := ss[1:1+ss[0]], ss[2+ss[0]:]
		d, err := tx.SigDigest(i, zecswap.SigHashAll)
		pkh, _ := zecswap.P2PKHHash(in.ScriptPubKey)
		if err != nil || !zecswap.VerifySig(sig, pub, d) || zecswap.Hash160(pub) != pkh {
			return "", &zecswap.RPCError{Method: "sendrawtransaction", Code: -26, Message: "bad signature"}
		}
	}
	if err := tx.CheckFee(1); err != nil {
		return "", &zecswap.RPCError{Method: "sendrawtransaction", Code: -26, Message: err.Error()}
	}
	z.mempool[id] = raw
	z.accepted++
	return id, nil
}

// fund pays addr value in a new mined transaction: a fee reaching the treasury.
func (z *fakeZcash) fund(addr string, value uint64) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.seq++
	_, _, h, _ := zecswap.ParseAddress(addr)
	z.utxos[outpoint(fmt.Sprintf("%064x", 0xfee000+z.seq), 0)] = fzOut{addr: addr, script: zecswap.P2PKHScript(h), value: value, height: z.height}
}

func (z *fakeZcash) mine(n uint32) {
	z.mu.Lock()
	defer z.mu.Unlock()
	for id, raw := range z.mempool {
		tx, _ := zecswap.ParseTx(raw)
		for _, in := range tx.In {
			delete(z.utxos, outpoint(zecswap.DisplayHex(in.PrevOut.Hash), in.PrevOut.Index))
		}
		for i, o := range tx.Out {
			var addr string
			if h, ok := zecswap.P2PKHHash(o.ScriptPubKey); ok {
				addr = zecswap.EncodeAddress(zecswap.Testnet, zecswap.P2PKH, h)
			} else {
				var h [20]byte
				copy(h[:], o.ScriptPubKey[2:22])
				addr = zecswap.EncodeAddress(zecswap.Testnet, zecswap.P2SH, h)
			}
			z.utxos[outpoint(id, uint32(i))] = fzOut{addr: addr, script: o.ScriptPubKey, value: o.Value, height: z.height + 1}
			z.received[addr] += o.Value
		}
		z.mined[id], z.raws[id] = z.height+1, raw
		delete(z.mempool, id)
	}
	z.height += n
}

func zecKey(t *testing.T, b byte) *zecswap.Key {
	t.Helper()
	var raw [32]byte
	raw[31] = b
	k, err := zecswap.ParseKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// ---- crashes ----

var steps = []string{"after-period", "after-intent", "after-build", "after-signed", "after-broadcast", "after-sent", "after-paid"}

// coin is one real payNet over its fake, with its own way to mine and to count what each address received.
type coin struct {
	name     string
	key      string
	fresh    func() payNet // a new process's view of the coin
	mine     func()
	received func(addr string) uint64
	sends    func() int // transactions the coin accepted
}

func xmrCoin(t *testing.T) (coin, map[string]string) {
	treasury := xmrAddr(t, 3, xmrswap.Mainnet)
	fw := newFakeWallet(treasury)
	fw.in, fw.inConf = []income{{ID: strings.Repeat("1f", 32), Amt: 1_000_000_000_000}}, 12
	srv := httptest.NewServer(fw)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	addrs := map[string]string{"deroA": xmrAddr(t, 5, xmrswap.Mainnet), "deroB": xmrAddr(t, 6, xmrswap.Mainnet), "deroC": xmrAddr(t, 7, xmrswap.Mainnet)}
	return coin{name: "XMR", key: "xm_",
		fresh: func() payNet {
			n := &xmrNet{w: newMoneroRPC(host, fw.user+":"+fw.pass), treasury: treasury, want: xmrswap.Mainnet, minPart: 100_000_000}
			if err := n.locate(context.Background()); err != nil {
				t.Fatal(err)
			}
			return n
		},
		mine:     func() { fw.mine(incomeConf) },
		received: func(a string) uint64 { fw.mu.Lock(); defer fw.mu.Unlock(); return fw.received[a] },
		sends:    func() int { fw.mu.Lock(); defer fw.mu.Unlock(); return fw.relays },
	}, addrs
}

func zecCoin(t *testing.T) (coin, map[string]string, *fakeZcash) {
	fz := newFakeZcash()
	k := zecKey(t, 7)
	treasury := k.Address(zecswap.Testnet)
	fz.fund(treasury, 625_000_000)
	fz.height += incomeConf
	p2sh := zecswap.EncodeAddress(zecswap.Testnet, zecswap.P2SH, [20]byte{9, 9, 9})
	addrs := map[string]string{"deroA": zecKey(t, 8).Address(zecswap.Testnet), "deroB": zecKey(t, 9).Address(zecswap.Testnet), "deroC": p2sh}
	return coin{name: "ZEC", key: "zc_",
		fresh: func() payNet {
			return &zecNet{nodes: []zecswap.ZcashSource{{Name: "fake", Node: fz}}, chain: "regtest", net: zecswap.Testnet, treasury: treasury, key: k, minPart: 10_000}
		},
		mine:     func() { fz.mine(incomeConf) },
		received: func(a string) uint64 { fz.mu.Lock(); defer fz.mu.Unlock(); return fz.received[a] },
		sends:    func() int { fz.mu.Lock(); defer fz.mu.Unlock(); return fz.accepted },
	}, addrs, fz
}

// TestPayoutCrashAtEveryStepNeverPaysTwice stops the process right after each step of a payout, for each coin,
// against the real XMR client (a wallet-rpc with --rpc-login, over HTTP) and the real ZEC builder and signer (a
// node that checks every signature and fee). A new process then replays the journal and carries on. Every holder
// must end with exactly its part, from exactly one transaction.
func TestPayoutCrashAtEveryStepNeverPaysTwice(t *testing.T) {
	t.Cleanup(func() { crashHook = nil })
	for _, mk := range []string{"XMR", "ZEC"} {
		for _, step := range steps {
			var c coin
			var addrs map[string]string
			if mk == "XMR" {
				c, addrs = xmrCoin(t)
			} else {
				c, addrs, _ = zecCoin(t)
			}
			pr := newFakePasses()
			pr.units = map[string]uint64{"deroA": 1, "deroB": 2, "deroC": 3}
			for h, a := range addrs {
				pr.addrs[c.key+h] = a
			}
			path := filepath.Join(t.TempDir(), "j.jsonl")
			fired := false
			crashHook = func(s string) error {
				if s == step && !fired {
					fired = true
					return errCrash
				}
				return nil
			}
			var l *ledger
			for life := 0; life < 8; life++ {
				var err error
				l, err = run(t, c.fresh(), pr, c.key, path, 5000)
				if err != nil && !errors.Is(err, errCrash) {
					t.Fatalf("%s crash %s, life %d: %v", mk, step, life, err)
				}
				c.mine()
			}
			if !fired {
				t.Fatalf("%s: step %s never happened", mk, step)
			}
			n := c.fresh()
			conserved(t, l, n)
			var pass uint64
			if mk == "XMR" {
				pass = 500_000_000_000
			} else {
				pass = 312_500_000
			}
			for h, u := range pr.units {
				if got, want := c.received(addrs[h]), pass/6*u; got != want {
					t.Errorf("%s crash %s: %s received %d, its part is %d", mk, step, h, got, want)
				}
			}
			if c.sends() != 1 || l.open != nil || l.Paid != pass/6*6 {
				t.Errorf("%s crash %s: %d transactions accepted, open %v, paid %d; want 1, none, %d", mk, step, c.sends(), l.open, l.Paid, pass/6*6)
			}
		}
	}
}

// A signed ZEC payout that no node takes is broadcast again, never rebuilt, until it is past its expiry height; only
// then is it voided, and its parts paid by a new transaction.
func TestZecPayoutIsVoidedOnlyOnceItCanNeverBeMined(t *testing.T) {
	c, addrs, fz := zecCoin(t)
	pr := newFakePasses()
	pr.units = map[string]uint64{"deroA": 1}
	pr.addrs["zc_deroA"] = addrs["deroA"]
	path := filepath.Join(t.TempDir(), "j.jsonl")
	fz.drop = true
	if _, err := run(t, c.fresh(), pr, "zc_", path, 5000); err == nil {
		t.Fatal("a refused broadcast was not reported")
	}
	l, _ := run(t, c.fresh(), pr, "zc_", path, 5000)
	first := l.open
	if first == nil || first.TxID == "" {
		t.Fatal("the signed payout is not open after a refused broadcast")
	}
	fz.height = first.Expiry - 1 // the last height at which it could still be mined
	if l, _ = run(t, c.fresh(), pr, "zc_", path, 5000); l.open == nil || l.open.TxID != first.TxID {
		t.Fatal("the payout was given up before its expiry height")
	}
	fz.height, fz.drop = first.Expiry, false
	l, _ = run(t, c.fresh(), pr, "zc_", path, 5000)
	if l.open == nil || l.open.TxID == first.TxID {
		t.Fatalf("past its expiry the dead payout was not replaced (open %v)", l.open)
	}
	fz.mine(incomeConf)
	l, _ = run(t, c.fresh(), pr, "zc_", path, 5000)
	if got := c.received(addrs["deroA"]); got != 312_500_000 || l.open != nil {
		t.Fatalf("deroA received %d (open %v), want 312500000 once", got, l.open)
	}
}

// ---- the journal ----

// A line cut short by a crash is dropped and the file repaired; any other unreadable line stops the asset.
func TestPayoutJournalDropsATornLastLine(t *testing.T) {
	n, pr := newMemNet(), newFakePasses()
	n.inc = []income{{ID: "f1", Amt: 100}}
	path := filepath.Join(t.TempDir(), "j.jsonl")
	if _, err := run(t, n, pr, "xm_", path, 5000); err != nil {
		t.Fatal(err)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"t":"intent","n":1,"dests":[{"hol`)
	f.Close()
	l, err := openLedger(path)
	if err != nil {
		t.Fatalf("a torn last line stopped the journal: %v", err)
	}
	l.close()
	b, _ := os.ReadFile(path)
	if l.open != nil || l.Income != 100 || !strings.HasSuffix(string(b), "}\n") {
		t.Fatalf("the torn line was acted on or left in place (open %v, income %d)", l.open, l.Income)
	}
	os.WriteFile(path, append([]byte("{not json}\n"), b...), 0o600)
	if _, err := openLedger(path); err == nil {
		t.Fatal("a corrupt line was accepted")
	}
	os.WriteFile(path, append(b, b...), 0o600) // the same period twice: its income would count twice
	if _, err := openLedger(path); err == nil {
		t.Fatal("a period recorded twice was accepted")
	}
}

// ---- addresses ----

func TestPayoutAddressChecks(t *testing.T) {
	if err := checkXmr(xmrAddr(t, 5, xmrswap.Mainnet), xmrswap.Mainnet); err != nil {
		t.Errorf("a mainnet address was refused: %v", err)
	}
	for _, bad := range []string{xmrAddr(t, 5, xmrswap.Stagenet), xmrAddr(t, 5, xmrswap.Mainnet)[:94] + "1", "4" + strings.Repeat("1", 94)} {
		if checkXmr(bad, xmrswap.Mainnet) == nil {
			t.Errorf("%s was accepted on mainnet", bad)
		}
	}
	k := zecKey(t, 3)
	for addr, ok := range map[string]bool{
		k.Address(zecswap.Testnet):                                        true,
		zecswap.EncodeAddress(zecswap.Testnet, zecswap.P2SH, [20]byte{1}): true,
		k.Address(zecswap.Mainnet):                                        false, // another network
		"zs1" + strings.Repeat("q", 75):                                   false, // shielded
		k.Address(zecswap.Testnet)[:34] + "x":                             false,
	} {
		if err := (&zecNet{net: zecswap.Testnet}).Check(addr); (err == nil) != ok {
			t.Errorf("%s: payable %v, want %v (%v)", addr, err == nil, ok, err)
		}
	}
}

// ---- configuration ----

// On a mainnet config each asset refuses to pay, and says why, unless every check passes; one asset's refusal
// leaves the other alone; no reason repeats a key or a login.
func TestPayoutsRefuseOnMainnetWithoutEveryCheck(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	os.MkdirAll(repo, 0o755)
	keyHex := hex.EncodeToString(append(make([]byte, 31), 11))
	writeKey := func(p, h string) string { os.WriteFile(p, []byte(h), 0o600); return p }
	keyFile := writeKey(filepath.Join(dir, "zec.key"), keyHex)
	inRepo := writeKey(filepath.Join(repo, "zec.key"), keyHex)
	otherKey := writeKey(filepath.Join(dir, "other.key"), hex.EncodeToString(append(make([]byte, 31), 12)))
	xaddr, zaddr := xmrAddr(t, 5, xmrswap.Mainnet), zecKey(t, 11).Address(zecswap.Mainnet)
	good := func() payoutFlags {
		return payoutFlags{network: "mainnet", root: repo, nw: &netcfg.Network{XmrTreasury: xaddr, ZecTreasury: zaddr},
			xmrWallet: "127.0.0.1:18083", xmrNet: "mainnet", xmrLogin: "treasury:pw-main-1", xmrMin: 100_000_000,
			zecNodes: []string{"127.0.0.1:8232"}, zecNet: "mainnet", zecKey: keyFile, zecLogin: "node:pw-main-2", zecMin: 10_000}
	}
	if x, z, notes := planPayouts(good()); x == nil || z == nil {
		t.Fatalf("a complete mainnet configuration was refused: %v", notes)
	}
	cases := []struct {
		asset, want string
		mod         func(*payoutFlags)
	}{
		{"XMR", "no -xmr-wallet", func(f *payoutFlags) { f.xmrWallet = "" }},
		{"XMR", "no xmrTreasury", func(f *payoutFlags) { f.nw.XmrTreasury = "" }},
		{"XMR", "not accepted on mainnet", func(f *payoutFlags) { f.xmrTreasury = xaddr }},
		{"XMR", "-xmr-net must be", func(f *payoutFlags) { f.xmrNet = "" }},
		{"XMR", "-xmr-net regtest", func(f *payoutFlags) { f.xmrNet = "regtest" }},
		{"XMR", "-xmr-net stagenet", func(f *payoutFlags) { f.xmrNet = "stagenet" }},
		{"XMR", "xmrTreasury", func(f *payoutFlags) { f.nw.XmrTreasury = xmrAddr(t, 5, xmrswap.Stagenet) }},
		{"XMR", "xmrTreasury", func(f *payoutFlags) { f.nw.XmrTreasury = xaddr[:94] + "1" }},
		{"XMR", xmrLoginEnv, func(f *payoutFlags) { f.xmrLogin = "" }},
		{"XMR", "not on this machine", func(f *payoutFlags) { f.xmrWallet = "10.0.0.5:18083" }},
		{"XMR", "carries a login", func(f *payoutFlags) { f.xmrWallet = "treasury:pw-main-1@127.0.0.1:18083" }},
		{"XMR", "could not be read", func(f *payoutFlags) { f.nw, f.nwErr = nil, errors.New("no file") }},
		{"ZEC", "no -zec-node", func(f *payoutFlags) { f.zecNodes = nil }},
		{"ZEC", "no zecTreasury", func(f *payoutFlags) { f.nw.ZecTreasury = "" }},
		{"ZEC", "not accepted on mainnet", func(f *payoutFlags) { f.zecTreasury = zaddr }},
		{"ZEC", "-zec-net testnet", func(f *payoutFlags) { f.zecNet = "testnet" }},
		{"ZEC", "not a transparent P2PKH", func(f *payoutFlags) { f.nw.ZecTreasury = zecKey(t, 11).Address(zecswap.Testnet) }},
		{"ZEC", "not a transparent P2PKH", func(f *payoutFlags) {
			f.nw.ZecTreasury = zecswap.EncodeAddress(zecswap.Mainnet, zecswap.P2SH, [20]byte{1})
		}},
		{"ZEC", "not a transparent P2PKH", func(f *payoutFlags) { f.nw.ZecTreasury = "zs1" + strings.Repeat("q", 75) }},
		{"ZEC", "no -zec-key", func(f *payoutFlags) { f.zecKey = "" }},
		{"ZEC", "inside the repository", func(f *payoutFlags) { f.zecKey = inRepo }},
		{"ZEC", "does not control", func(f *payoutFlags) { f.zecKey = otherKey }},
		{"ZEC", "could not be read", func(f *payoutFlags) { f.zecKey = filepath.Join(dir, "missing.key") }},
		{"ZEC", "no node login", func(f *payoutFlags) { f.zecLogin = "" }},
		{"ZEC", "below the smallest output", func(f *payoutFlags) { f.zecMin = 1 }},
	}
	for _, c := range cases {
		f := good()
		c.mod(&f)
		x, z, notes := planPayouts(f)
		all := strings.Join(notes, "\n")
		refused := (c.asset == "XMR" && x == nil) || (c.asset == "ZEC" && z == nil)
		if !refused || !strings.Contains(all, c.asset+" payouts refused") || !strings.Contains(all, c.want) {
			t.Errorf("%s (%s): not refused with the reason: %q", c.asset, c.want, all)
		}
		if other := (c.asset == "XMR" && z == nil) || (c.asset == "ZEC" && x == nil); other && f.nw != nil {
			t.Errorf("%s (%s): the other asset was refused too: %q", c.asset, c.want, all)
		}
		for _, secret := range []string{keyHex, "pw-main-1", "pw-main-2"} {
			if strings.Contains(all, secret) {
				t.Errorf("%s (%s): a reason repeats a secret", c.asset, c.want)
			}
		}
	}
	// a test deployment never pays real coins
	f := good()
	f.network = "simulator"
	if x, z, notes := planPayouts(f); x != nil || z != nil || len(notes) != 2 {
		t.Errorf("a simulator config accepted mainnet Monero or Zcash: %v", notes)
	}
}

// Off mainnet an unset asset is simply off; the DERO side is unaffected either way.
func TestPayoutsAreOffWhenNotConfigured(t *testing.T) {
	x, z, notes := planPayouts(payoutFlags{network: "simulator"})
	if x != nil || z != nil || strings.Join(notes, "|") != "XMR payouts off: no -xmr-wallet|ZEC payouts off: no -zec-node" {
		t.Fatalf("unexpected: %v", notes)
	}
}

// The mainnet bot says, before its first call, that it will pay no XMR and no ZEC and why.
func TestMainnetBotRefusesPayoutsItCannotCheck(t *testing.T) {
	d, daemon := startBotNode(t, "")
	_, wallet := startBotNode(t, botUser+":"+botPass)
	code, out := runBot(t, botUser+":"+botPass, append(mainnetBot(t, daemon, wallet), "-networks", filepath.Join(t.TempDir(), "none.json")))
	botNoLogin(t, out)
	if code == 0 || !strings.Contains(out, "XMR payouts refused: no -xmr-wallet") || !strings.Contains(out, "ZEC payouts refused: no -zec-node") {
		t.Fatalf("exit %d, want both refusals:\n%s", code, botShown(out))
	}
	if i := strings.Index(out, "payouts refused"); i < 0 || len(d.seen()) == 0 {
		t.Fatalf("the bot did not go on to its DERO side")
	}
}

// ---- the snapshot ----

// The snapshot is the pass at the last topoheight of its height (a side block at the same height counts), with
// every holder's units and the addresses of every holder and of every name still owed.
func TestSnapshotReadsThePassAtTheLastTopoheightOfItsHeight(t *testing.T) {
	heightOf := func(topo int64) int64 { // topo 6 and 13 share a height with the block before
		h := topo
		if topo > 5 {
			h--
		}
		if topo > 12 {
			h--
		}
		return h
	}
	var readAt int64 = -1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string                 `json:"method"`
			Params map[string]interface{} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		var res interface{}
		switch req.Method {
		case "DERO.GetHeight":
			res = map[string]int64{"height": 18, "stableheight": 10, "topoheight": 20}
		case "DERO.GetBlockHeaderByTopoHeight":
			tp := int64(req.Params["topoheight"].(float64))
			res = map[string]interface{}{"block_header": map[string]int64{"height": heightOf(tp), "topoheight": tp}}
		case "DERO.GetSC":
			readAt = int64(req.Params["topoheight"].(float64))
			hx := func(s string) string { return hex.EncodeToString([]byte(s)) }
			res = map[string]interface{}{"stringkeys": map[string]interface{}{"v": 2, "hst": 3, "hu_deroA": 1, "hu_deroB": 2,
				"xm_deroA": hx("4aaa"), "xm_deroGone": hx("4ggg"), "zc_deroA": hx("tmzzz")}}
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": "1", "result": res})
	}))
	defer srv.Close()
	d := deroPasses{daemon: strings.TrimPrefix(srv.URL, "http://"), founders: strings.Repeat("f", 64)}
	for h, topo := range map[uint64]int64{5: 6, 10: 11, 3: 3} {
		s, err := d.Snapshot(h, "xm_", []string{"deroGone"})
		if err != nil {
			t.Fatal(err)
		}
		if s.Topo != topo || readAt != topo {
			t.Errorf("height %d read at topoheight %d (asked %d), want %d", h, s.Topo, readAt, topo)
		}
		want := map[string]string{"deroA": "4aaa", "deroGone": "4ggg"}
		if s.Hst != 3 || s.Units["deroA"] != 1 || s.Units["deroB"] != 2 || fmt.Sprint(s.Addrs) != fmt.Sprint(want) {
			t.Errorf("snapshot %+v", s)
		}
	}
}
