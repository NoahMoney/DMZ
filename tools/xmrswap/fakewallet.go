package xmrswap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
)

// FakeFee is the flat fee the fake wallet charges every transfer and sweep, in piconero (0.00003 XMR, the order
// of a real one). A fee that is not zero keeps a driver honest about amounts; a fee that is fixed keeps it
// deterministic.
const FakeFee uint64 = 30_000_000

// spendableAge is CRYPTONOTE_DEFAULT_TX_SPENDABLE_AGE (monero src/cryptonote_config.h): an output is spendable
// once block_height + 10 <= chain height (wallet2::is_transfer_unlocked).
const spendableAge = 10

// FakeWallet is a monero-wallet-rpc that serves the reference's request and response shapes from memory:
// get_height, get_address, transfer, generate_from_keys, open_wallet, close_wallet, get_transfers, sweep_all,
// with the wallet-RPC's own error codes. It is one node's view of one chain, so every wallet file it holds
// sees the same transfers: a view-only wallet made from the joint address sees what another wallet paid to it,
// and the full wallet made later from the joint spend key can sweep it.
//
// Time is Mine: a transfer goes to the pool; the first block Mine produces includes the pool, and
// confirmations are chain height minus block height, exactly as the wallet counts them (fill_transfer_entry
// in wallet_rpc_server.cpp); an output unlocks at 10 confirmations. Deterministic: transaction ids are a
// counter under SHA-256, timestamps are a function of height, and there is no randomness anywhere.
type FakeWallet struct {
	mu      sync.Mutex
	net     Network
	height  uint64 // chain height: the number of blocks; the next block's index
	seq     uint64
	wallets map[string]*fakeWalletFile
	open    string
	txs     []*fakeTx

	authUser, authPass string

	// unlockTime is the unlock_time every transfer from now on carries when the request names none (review R6:
	// a sender who wants the output frozen), and burnNext makes the next transfer's output a duplicate one-time
	// key (review R5d, the burning bug): listed by get_transfers at its full amount, never in any balance. Both
	// are how a test plays a dishonest Alice against Bob's gate.
	unlockTime uint64
	burnNext   bool

	// scanLag makes generate_from_keys behave as monero-wallet-rpc's does: on_generate_from_keys sets the
	// restore height and installs the wallet, and never calls refresh, so the new wallet has scanned nothing
	// and every balance it reports is zero until `refresh` is called. Off by default, because most drivers do
	// not want to model the scan; on, it is the real server's first answer to a sweep.
	scanLag bool
	// refreshes counts the `refresh` calls, so a test can assert the companion made them.
	refreshes int
}

type fakeWalletFile struct {
	address       string
	viewOnly      bool
	restoreHeight uint64
	// scanned is whether this wallet file has scanned the chain. A wallet made by generate_from_keys under
	// scanLag has not, and sees nothing at all until refresh.
	scanned bool
}

type fakeTx struct {
	id      string
	from    string // "" for Fund, which is a coinbase-like credit
	to      string
	amount  uint64
	fee     uint64
	height  uint64 // the block that includes it, once mined
	pending bool
	unlock  uint64 // the transaction's unlock_time: a height, or a unix time from 500,000,000 up; 0 for none
	burnt   bool   // a duplicate one-time key: the wallet lists it and ignores it
}

// unlockTimeIsTime is CRYPTONOTE_MAX_BLOCK_NUMBER (src/cryptonote_config.h): an unlock_time at or above it is
// a unix time, below it a height (cryptonote_basic.h, "spend this tx not early then block/time").
const unlockTimeIsTime = 500_000_000

// NewFakeWallet is an empty node on net at height 1 (the genesis block only), with no wallet open.
func NewFakeWallet(net Network) *FakeWallet {
	return &FakeWallet{net: net, height: 1, wallets: map[string]*fakeWalletFile{}}
}

// Mine produces n blocks. The first carries every pending transaction; the rest are empty.
func (f *FakeWallet) Mine(n uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n == 0 {
		return
	}
	for _, tx := range f.txs {
		if tx.pending {
			tx.pending, tx.height = false, f.height
		}
	}
	f.height += n
}

// Height is the chain height, for a driver that wants to reason about confirmations without an RPC.
func (f *FakeWallet) Height() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.height
}

// Fund credits amount piconero to address in the next block, the way a miner's reward would; it is how a
// driver gives Alice XMR to lock. Mine(10) after it makes the credit spendable.
func (f *FakeWallet) Fund(address string, amount uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.txs = append(f.txs, &fakeTx{id: f.nextTxID(), to: address, amount: amount, pending: true})
}

// RequireAuth makes the fake demand --rpc-login digest credentials, as epee does: a 401 with one
// WWW-Authenticate per algorithm, realm "monero-rpc", qop="auth". The nonce is fixed; a fake need not
// defend against replay.
func (f *FakeWallet) RequireAuth(user, pass string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authUser, f.authPass = user, pass
}

// SetUnlockTime makes every later transfer whose request names no unlock_time carry this one (0 restores the
// default). It is what a sender who wants the recipient's output frozen does, and what Bob's gate must refuse.
func (f *FakeWallet) SetUnlockTime(unlockTime uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unlockTime = unlockTime
}

// SetScanLag decides whether a wallet made by generate_from_keys must be refreshed before it sees anything,
// as monero-wallet-rpc's is. Off restores the default, where a new wallet sees the whole chain at once.
func (f *FakeWallet) SetScanLag(lag bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scanLag = lag
}

// Refreshes is how many times `refresh` has been called, for a driver that wants to prove the companion scans.
func (f *FakeWallet) Refreshes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshes
}

// BurnNextTransfer makes the next transfer's output a duplicate one-time key. The recipient's wallet lists the
// payment as get_transfers does, and never credits it: it is the burning bug as seen from the victim's side.
func (f *FakeWallet) BurnNextTransfer() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.burnNext = true
}

const fakeRealm, fakeNonce = "monero-rpc", "fake-wallet-nonce"

func (f *FakeWallet) nextTxID() string {
	f.seq++
	sum := sha256.Sum256([]byte("xmrswap fake tx " + strconv.FormatUint(f.seq, 10)))
	return hex.EncodeToString(sum[:])
}

// timestamp is the fake's block time: a function of height, so nothing here depends on the clock.
func (f *FakeWallet) timestamp(height uint64) uint64 { return 1700000000 + 120*height }

// unlocked is wallet2::is_transfer_unlocked for a mined credit: past the spendable age, and past the
// transaction's own unlock_time, read as a height below CRYPTONOTE_MAX_BLOCK_NUMBER and as a time above it.
func (f *FakeWallet) unlocked(tx *fakeTx) bool {
	if tx.pending || tx.height+spendableAge > f.height {
		return false
	}
	switch {
	case tx.unlock == 0:
		return true
	case tx.unlock < unlockTimeIsTime:
		return tx.unlock <= f.height
	}
	return tx.unlock <= f.timestamp(f.height)
}

// balance is (unlocked, total) for an address: mined credits (unlocked ones only, for the first) less every
// debit, pending or not. Change from a partial transfer stays spendable here, where a real wallet would lock
// it for ten blocks; the swap's own transfers never need the change. A burnt credit counts for nothing, as
// the wallet's scanner ignores it.
// walletBalance is what one wallet file reports: its address's balance, or nothing at all while the file has
// not scanned.
func (f *FakeWallet) walletBalance(wf *fakeWalletFile) (unlocked, total uint64) {
	if !wf.scanned {
		return 0, 0
	}
	return f.balance(wf.address)
}

func (f *FakeWallet) balance(address string) (unlocked, total uint64) {
	for _, tx := range f.txs {
		if tx.to == address && !tx.pending && !tx.burnt {
			total += tx.amount
			if f.unlocked(tx) {
				unlocked += tx.amount
			}
		}
		if tx.from == address {
			total -= tx.amount + tx.fee
			unlocked -= tx.amount + tx.fee
		}
	}
	return unlocked, total
}

type rpcFail struct {
	code int
	msg  string
}

func fail(code int, msg string) *rpcFail { return &rpcFail{code, msg} }

// ServeHTTP is the JSON-RPC 2.0 endpoint at /json_rpc.
func (f *FakeWallet) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/json_rpc" {
		http.NotFound(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.authUser != "" && !f.authorized(r) {
		for _, alg := range []string{"MD5", "MD5-sess"} {
			w.Header().Add("WWW-Authenticate", fmt.Sprintf(`Digest qop="auth",algorithm=%s,realm="%s",nonce="%s",stale=false`, alg, fakeRealm, fakeNonce))
		}
		http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	id := req.ID
	if len(id) == 0 {
		id = json.RawMessage(`"0"`)
	}
	env := map[string]interface{}{"id": id, "jsonrpc": "2.0"}
	result, rf := f.dispatch(req.Method, req.Params)
	if rf != nil {
		env["error"] = map[string]interface{}{"code": rf.code, "message": rf.msg}
	} else {
		env["result"] = result
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(env)
}

// authorized checks a Digest Authorization header against the fixed challenge, both qop forms and both
// algorithms, as epee's server does.
func (f *FakeWallet) authorized(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	if len(h) < 7 || h[:7] != "Digest " {
		return false
	}
	p := parseAuthParams(h[7:])
	if p["username"] != f.authUser || p["realm"] != fakeRealm || p["nonce"] != fakeNonce || p["uri"] == "" {
		return false
	}
	c := &digestChallenge{realm: fakeRealm, nonce: fakeNonce, qop: p["qop"], algorithm: p["algorithm"]}
	if c.algorithm == "" {
		c.algorithm = "MD5"
	}
	if c.qop != "" && c.qop != "auth" {
		return false
	}
	return digestResponse(f.authUser, f.authPass, r.Method, p["uri"], c, p["nc"], p["cnonce"]) == p["response"]
}

func (f *FakeWallet) openWallet() (*fakeWalletFile, *rpcFail) {
	if f.open == "" {
		return nil, fail(-13, "No wallet file") // WALLET_RPC_ERROR_CODE_NOT_OPEN
	}
	return f.wallets[f.open], nil
}

func (f *FakeWallet) dispatch(method string, raw json.RawMessage) (interface{}, *rpcFail) {
	params := func(v interface{}) *rpcFail {
		if len(raw) == 0 {
			return nil
		}
		if err := json.Unmarshal(raw, v); err != nil {
			return fail(-32602, "Invalid params")
		}
		return nil
	}
	switch method {
	case "get_height":
		return map[string]interface{}{"height": f.height}, nil

	case "get_address":
		wf, rf := f.openWallet()
		if rf != nil {
			return nil, rf
		}
		return map[string]interface{}{
			"address": wf.address,
			"addresses": []map[string]interface{}{
				{"address": wf.address, "address_index": 0, "label": "Primary account", "used": false},
			},
		}, nil

	case "transfer":
		var p struct {
			Destinations []struct {
				Amount  uint64 `json:"amount"`
				Address string `json:"address"`
			} `json:"destinations"`
			UnlockTime *uint64 `json:"unlock_time"` // the reference's own parameter; the companion never sends it
		}
		if rf := params(&p); rf != nil {
			return nil, rf
		}
		wf, rf := f.openWallet()
		if rf != nil {
			return nil, rf
		}
		if wf.viewOnly {
			return nil, fail(-29, "The wallet is watch-only. Cannot spend.") // WALLET_RPC_ERROR_CODE_WATCH_ONLY
		}
		if len(p.Destinations) != 1 {
			return nil, fail(-4, "the fake wallet pays exactly one destination")
		}
		d := p.Destinations[0]
		if d.Amount == 0 {
			return nil, fail(-46, "Destination amount is zero") // WALLET_RPC_ERROR_CODE_ZERO_AMOUNT
		}
		if rf := f.checkDest(d.Address); rf != nil {
			return nil, rf
		}
		unlocked, total := f.walletBalance(wf)
		if total < d.Amount+FakeFee {
			return nil, fail(-17, "not enough money") // WALLET_RPC_ERROR_CODE_NOT_ENOUGH_MONEY
		}
		if unlocked < d.Amount+FakeFee {
			return nil, fail(-37, "not enough unlocked money") // WALLET_RPC_ERROR_CODE_NOT_ENOUGH_UNLOCKED_MONEY
		}
		tx := &fakeTx{id: f.nextTxID(), from: wf.address, to: d.Address, amount: d.Amount, fee: FakeFee, pending: true, unlock: f.unlockTime, burnt: f.burnNext}
		if p.UnlockTime != nil {
			tx.unlock = *p.UnlockTime
		}
		f.burnNext = false
		f.txs = append(f.txs, tx)
		return map[string]interface{}{"tx_hash": tx.id, "amount": tx.amount, "fee": tx.fee, "tx_key": "", "tx_blob": "", "tx_metadata": ""}, nil

	case "sweep_all":
		var p struct {
			Address string `json:"address"`
		}
		if rf := params(&p); rf != nil {
			return nil, rf
		}
		wf, rf := f.openWallet()
		if rf != nil {
			return nil, rf
		}
		if wf.viewOnly {
			return nil, fail(-29, "The wallet is watch-only. Cannot spend.")
		}
		if rf := f.checkDest(p.Address); rf != nil {
			return nil, rf
		}
		unlocked, _ := f.walletBalance(wf)
		if unlocked <= FakeFee {
			return nil, fail(-37, "No unlocked balance in the specified account")
		}
		tx := &fakeTx{id: f.nextTxID(), from: wf.address, to: p.Address, amount: unlocked - FakeFee, fee: FakeFee, pending: true}
		f.txs = append(f.txs, tx)
		return map[string]interface{}{"tx_hash_list": []string{tx.id}, "amount_list": []uint64{tx.amount}, "fee_list": []uint64{tx.fee}}, nil

	case "generate_from_keys":
		var p struct {
			RestoreHeight uint64 `json:"restore_height"`
			Filename      string `json:"filename"`
			Address       string `json:"address"`
			Spendkey      string `json:"spendkey"`
			Viewkey       string `json:"viewkey"`
		}
		if rf := params(&p); rf != nil {
			return nil, rf
		}
		// the server's own order of refusals (wallet_rpc_server.cpp on_generate_from_keys), same messages
		if p.Viewkey == "" {
			return nil, fail(-1, "field 'viewkey' is mandatory. Please provide a view key you want to restore from.")
		}
		if p.Address == "" {
			return nil, fail(-1, "field 'address' is mandatory. Please provide a public address.")
		}
		if p.Filename == "" {
			return nil, fail(-1, "Invalid filename")
		}
		if _, exists := f.wallets[p.Filename]; exists {
			return nil, fail(-1, "Wallet already exists.")
		}
		pa, err := ParseAddress(p.Address)
		if err != nil || pa.Net != f.net || pa.Subaddress {
			return nil, fail(-1, "Failed to parse public address")
		}
		view, err := ParseScalar(p.Viewkey)
		if err != nil {
			return nil, fail(-1, "Failed to parse view key secret key")
		}
		if view.Public() != pa.View {
			return nil, fail(-1, "view secret key does not match main address")
		}
		if p.Spendkey != "" {
			spend, err := ParseScalar(p.Spendkey)
			if err != nil {
				return nil, fail(-1, "Failed to parse spend key secret key")
			}
			if spend.Public() != pa.Spend {
				return nil, fail(-1, "spend secret key does not match main address")
			}
		}
		f.wallets[p.Filename] = &fakeWalletFile{address: p.Address, viewOnly: p.Spendkey == "", restoreHeight: p.RestoreHeight, scanned: !f.scanLag}
		f.open = p.Filename
		return map[string]interface{}{"address": p.Address, "info": "Wallet has been generated successfully."}, nil

	case "open_wallet":
		var p struct {
			Filename string `json:"filename"`
		}
		if rf := params(&p); rf != nil {
			return nil, rf
		}
		if p.Filename == "" {
			return nil, fail(-1, "Invalid filename")
		}
		if _, ok := f.wallets[p.Filename]; !ok {
			return nil, fail(-1, "Failed to open wallet : file not found")
		}
		f.open = p.Filename
		return map[string]interface{}{}, nil

	case "refresh":
		// on_refresh: wallet2::refresh in the foreground, answering with what it fetched. Here it is the moment
		// a wallet made from keys starts seeing the chain.
		wf, rf := f.openWallet()
		if rf != nil {
			return nil, rf
		}
		f.refreshes++
		fetched := uint64(0)
		if !wf.scanned {
			fetched = f.height - wf.restoreHeight
			wf.scanned = true
		}
		return map[string]interface{}{"blocks_fetched": fetched, "received_money": fetched > 0}, nil

	case "close_wallet":
		if _, rf := f.openWallet(); rf != nil {
			return nil, rf
		}
		f.open = ""
		return map[string]interface{}{}, nil

	case "get_balance":
		wf, rf := f.openWallet()
		if rf != nil {
			return nil, rf
		}
		unlocked, total := f.walletBalance(wf)
		// the reference's shape (on_get_balance): the account's totals and one per_subaddress row for index 0
		return map[string]interface{}{
			"balance":                total,
			"unlocked_balance":       unlocked,
			"multisig_import_needed": false,
			"blocks_to_unlock":       0,
			"time_to_unlock":         0,
			"per_subaddress": []map[string]interface{}{{
				"account_index": 0, "address_index": 0, "address": wf.address, "balance": total, "unlocked_balance": unlocked,
				"label": "Primary account", "num_unspent_outputs": 0, "blocks_to_unlock": 0, "time_to_unlock": 0,
			}},
		}, nil

	case "get_transfers":
		var p struct {
			In             bool    `json:"in"`
			Pool           bool    `json:"pool"`
			FilterByHeight bool    `json:"filter_by_height"`
			MinHeight      uint64  `json:"min_height"`
			MaxHeight      *uint64 `json:"max_height"`
		}
		if rf := params(&p); rf != nil {
			return nil, rf
		}
		wf, rf := f.openWallet()
		if rf != nil {
			return nil, rf
		}
		// on_get_transfers: without filter_by_height the whole chain; with it, wallet2::get_payments keeps
		// min_height < block_height <= max_height — min_height is exclusive.
		var lo, hi uint64 = 0, ^uint64(0)
		if p.FilterByHeight {
			lo = p.MinHeight
			if p.MaxHeight != nil {
				hi = *p.MaxHeight
			}
		}
		in, pool := []map[string]interface{}{}, []map[string]interface{}{}
		if !wf.scanned {
			return map[string]interface{}{"in": in, "pool": pool}, nil
		}
		for _, tx := range f.txs {
			if tx.to != wf.address {
				continue
			}
			if tx.pending {
				if p.Pool {
					pool = append(pool, f.entry(tx, "pool"))
				}
				continue
			}
			if !p.In || tx.height < wf.restoreHeight || !(lo < tx.height && tx.height <= hi) {
				continue
			}
			in = append(in, f.entry(tx, "in"))
		}
		return map[string]interface{}{"in": in, "pool": pool}, nil
	}
	return nil, fail(-32601, "Method not found")
}

// checkDest is the wallet's validate_transfer: the destination must parse for this network.
func (f *FakeWallet) checkDest(address string) *rpcFail {
	pa, err := ParseAddress(address)
	if err != nil || pa.Net != f.net {
		return fail(-2, "Invalid destination address") // WALLET_RPC_ERROR_CODE_WRONG_ADDRESS
	}
	return nil
}

// entry is one transfer_entry of get_transfers, with every field the reference's example carries. A burnt
// credit is listed like any other — the list is what the scanner saw, the balance is what it kept.
func (f *FakeWallet) entry(tx *fakeTx, kind string) map[string]interface{} {
	var confirmations, height uint64
	if !tx.pending {
		height = tx.height
		confirmations = f.height - tx.height
	}
	locked := !f.unlocked(tx)
	return map[string]interface{}{
		"address":                           tx.to,
		"amount":                            tx.amount,
		"amounts":                           []uint64{tx.amount},
		"confirmations":                     confirmations,
		"double_spend_seen":                 false,
		"fee":                               tx.fee,
		"height":                            height,
		"locked":                            locked,
		"note":                              "",
		"payment_id":                        "0000000000000000",
		"subaddr_index":                     map[string]uint32{"major": 0, "minor": 0},
		"subaddr_indices":                   []map[string]uint32{{"major": 0, "minor": 0}},
		"suggested_confirmations_threshold": 1,
		"timestamp":                         f.timestamp(height),
		"txid":                              tx.id,
		"type":                              kind,
		"unlock_time":                       tx.unlock,
	}
}
