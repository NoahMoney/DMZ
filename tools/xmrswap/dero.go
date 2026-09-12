package xmrswap

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Dero is the DERO leg as the state machines see it: one htlc.bas contract, one wallet that signs for this
// party, one daemon that reads. Every method returns an error rather than exiting — tools/simrpc's policy is
// log.Fatal, which is fine for a throwaway script and wrong for a companion holding a key share mid-swap (the
// audit's V1), so this client copies simrpc's request shapes and none of its exits.
type Dero interface {
	Height(ctx context.Context) (uint64, error)
	// StableHeight is the daemon's own finality mark: a block at or below it is never replaced (STABLE_LIMIT = 8,
	// blockchain.go Get_Stable_Height). Bob sweeps only on a Claim read at or below it (Invariant 9).
	StableHeight(ctx context.Context) (uint64, error)
	// Lock is idempotent per (maker, ha, hk, hb): the hashes are fresh for one swap, so a lock that already
	// carries them is this swap's lock, and the client returns it instead of locking the DERO a second time.
	// That is the same rule vaultsigner holds its backends to (look the payment up before paying). txid is ""
	// when the lock was found rather than sent. asset is "" for DERO or the 64-hex SCID of the asset the lock
	// holds instead (U1); amount is in that asset's atomic units. cutoff is the first height at which no new Lock
	// transaction is built (lockCutoff): htlc.bas v3 measures t1 − w1min against the height the Lock executes at, up
	// to DeroTxValidity blocks after it was built, and the estimator cannot see that (it runs at height 0), so a
	// Lock built later could be refused on chain and its fee lost. The lookup for this swap's lock is made at any
	// height; at or past the cutoff with none found, nothing is sent and the error wraps ErrLockTooLate.
	Lock(ctx context.Context, ha, hk, hb, taker string, t1, t2 uint64, asset string, amount, cutoff uint64) (txid string, id uint64, err error)
	// Claim sends nothing, first time or re-send, at a height h with h + 1 >= cutoff: cutoff is the caller's
	// claimCutoff(t1, margin), so the client's re-send policy and the state machine's cutoff are one rule (XMR-06).
	Claim(ctx context.Context, id uint64, sa, kb string, cutoff uint64) (string, error)
	Refund(ctx context.Context, id uint64, sb string) (string, error)
	// Punish sends nothing, first time or re-send, at a height h >= cutoff: cutoff is the caller's punishCutoff(t2,
	// g2, margin), so that nothing it sends can execute at or after t2 + g2, where htlc.bas v3 refuses it.
	Punish(ctx context.Context, id uint64, cutoff uint64) (string, error)
	// Reclaim is htlc.bas v3's maker exit from t2 + g2, with Refund's arguments: it pays the maker and stores s_b in
	// xb_<id>, as Refund does.
	Reclaim(ctx context.Context, id uint64, sb string) (string, error)
	ReadLock(ctx context.Context, id uint64) (Lock, error) // every field, including xa/xb when revealed and st
	// Deployment is the contract's code hash and its three install arguments, read in one DERO.GetSC: what U4 and
	// checkDeployment compare with the htlc.bas this companion was built for and with its own policy, so a look-alike
	// contract, or the right code installed with windows outside the policy, is refused before any lock.
	Deployment(ctx context.Context) (Deployment, error)
	// Registered says whether addr has an account on this chain. A transfer to an unregistered address is refused,
	// so a maker cannot pay the stipend to one (Path 0).
	Registered(ctx context.Context, addr string) (bool, error)
	// Balance is this party's own spendable balance of asset ("" is DERO), from its own wallet.
	Balance(ctx context.Context, asset string) (uint64, error)
	// Pay is a plain DERO transfer from this party's wallet, tagged with a destination port so both wallets can
	// find it again by that tag alone: the stipend (Path 0). It returns the txid whenever the wallet gave one, even
	// with an error, because a transaction the client stopped waiting for may still be mined.
	Pay(ctx context.Context, to string, amount, port uint64) (string, error)
	// FindPayment looks through this party's own wallet history, incoming or outgoing, for a DERO transfer at or
	// above minHeight carrying the destination port; found is false when there is none.
	FindPayment(ctx context.Context, incoming bool, port, minHeight uint64) (p Payment, found bool, err error)
}

// Payment is one DERO transfer as this party's wallet records it.
type Payment struct {
	TxID   string
	Amount uint64
	Height uint64 // the block it was mined in
}

// Lock is one htlc.bas lock as the contract stores it. Maker and Taker are the lowercase hex of the raw 33-byte
// addresses (what SIGNER() and ADDRESS_RAW produce, and what DERO.GetSC returns for a raw string); compare them
// with DeroRaw. The hashes and revealed shares are the 64-hex strings the contract compares.
type Lock struct {
	ID     uint64
	Maker  string
	Taker  string
	HA     string
	HK     string
	HB     string
	Asset  string // "" for DERO, else the 64-hex SCID
	Amount uint64
	T1     uint64
	T2     uint64
	State  uint64 // 0 open, 1 claimed, 2 refunded, 3 punished, 4 reclaimed
	XA     string // s_a, present once claimed
	XB     string // s_b, present once refunded or reclaimed
}

// Lock states as htlc.bas stores them in st_<id>.
const (
	LockOpen     uint64 = 0
	LockClaimed  uint64 = 1
	LockRefunded uint64 = 2
	LockPunished uint64 = 3
	// LockReclaimed is htlc.bas v3's fourth exit: nobody punished in [t2, t2 + g2), and the maker took the lock back
	// with s_b, which the contract stores in xb_<id> exactly as a Refund does. For the taker it is a refund.
	LockReclaimed uint64 = 4
)

// DeroTxValidity is TX_VALIDITY_HEIGHT (vendor/derohe/blockchain/miner_block.go:43). A block takes a transaction only
// while the block's height minus the transaction's height — the wallet's tip when it built it — is below it
// (blockchain.go:748, miner_block.go:275), so a DERO transaction built at height h is mined by h + 10 or never. The
// companion counts 11, one block conservative: htlc.bas v3 measures a Lock's width and a Punish's deadline at the height
// the call executes, so it builds each only while its last possible execution height is inside the rule with a block
// to spare.
const DeroTxValidity uint64 = 11

// ErrLockTooLate is Lock's answer when no lock of this swap's was found and the height is at or past the cutoff: no
// transaction was sent.
var ErrLockTooLate = errors.New("too late to build the Lock: nothing was sent")

// lockCutoff is the first height at which the maker builds no Lock: one built at h executes by h + DeroTxValidity,
// and htlc.bas v3 refuses it unless t1 ≥ its execution height + w1min (line 26). So it goes out only while
// h + DeroTxValidity + w1min ≤ t1, and a maker who decides t1 = h + T1Delta needs T1Delta ≥ w1min + DeroTxValidity.
func lockCutoff(t1, w1min uint64) uint64 {
	if t1+1 <= DeroTxValidity+w1min {
		return 0
	}
	return t1 + 1 - DeroTxValidity - w1min
}

// punishCutoff is the first height at which the taker sends no Punish (MT-J13): htlc.bas v3 refuses one executing at
// or after t2 + g2, a transaction built at h executes by h + DeroTxValidity, and hers counts only once it is stable, 8
// blocks deep, with margin on top. From here she waits for the maker's Reclaim, which reveals s_b.
func punishCutoff(t2, g2, margin uint64) uint64 {
	if t2+g2 <= DeroTxValidity+DefaultStableBlocks+margin {
		return 0
	}
	return t2 + g2 - DeroTxValidity - DefaultStableBlocks - margin
}

// Deployment is what identifies an htlc.bas install (MT-A03(a), MT-J13): the code, hashed as stored, and the three
// values it was installed with. A value the contract does not store reads as 0, which no v3 install has.
type Deployment struct {
	CodeSHA256 string
	W1Min      uint64 // the least t1 − the Lock's execution height the contract accepts
	W2Min      uint64 // the least t2 − t1
	G2         uint64 // the Punish window: Punish in [t2, t2 + g2), Reclaim from t2 + g2
}

// HTLCInstallArgs are htlc.bas v3's Initialize arguments, for a driver that installs it.
func HTLCInstallArgs(w1min, w2min, g2 uint64) []Arg {
	return []Arg{argU("w1min", w1min), argU("w2min", w2min), argU("g2", g2)}
}

// ErrNoSuchLock is returned by ReadLock for an id the contract has not allocated.
var ErrNoSuchLock = errors.New("no such lock")

// DeroRPC talks to one daemon and one wallet-rpc, for one contract. Daemon and Wallet are "host:port" or a URL.
type DeroRPC struct {
	Daemon string
	Wallet string
	SCID   string
	// FixedFee pays the calls the daemon cannot estimate. The estimator runs a contract at height 0
	// (dvm.Simulator.height is never set), so Refund (t1 <= height) and Punish (height >= t2) "fail" at estimate
	// time while the real transaction succeeds; they go out with this fee, as vaultsigner sends Released.
	FixedFee uint64
	// Timeout bounds one transaction's wait for inclusion; Poll is how often the daemon is asked.
	Timeout time.Duration
	Poll    time.Duration
	HTTP    *http.Client
	// Resends is how many times an exit whose transaction did not mine within Timeout is sent again at double
	// the fee (review R24: a fee too low sits unmined into the window's end). Each re-send is a new transaction;
	// if two mine, the contract refuses the second and its fee is the cost. A re-send happens only while the
	// call can still land usefully — a Claim while height + 1 < the caller's claim cutoff (t1 − 8 − margin), a
	// Refund while height < t2 — and after that the caller moves to the exit that remains.
	Resends int
	// Log, when set, reports a re-send; the client is otherwise silent.
	Log func(format string, a ...interface{})
	// Login is "user:password" for a wallet started with --rpc-login, sent as HTTP basic auth on wallet calls and
	// never to the daemon (GAP-008). A wallet RPC with no login authenticates nobody, and its /ws endpoint takes
	// any Origin, so any web page open during a swap could call transfer on it.
	Login string

	// pending remembers, per (ha, hk, hb), a Lock transaction this client submitted and did not see mined.
	// FindLock reads executed contract state, so it cannot see a transaction still in the mempool; without
	// this memory the next attempt finds nothing, sends a second Lock carrying the same hashes and the same
	// windows, and both mine — one Monero payment against two locked amounts, either of which the taker can
	// claim with the one s_a and the one k_b.
	mu      sync.Mutex
	pending map[string]string
}

func (c *DeroRPC) pendingTx(key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pending[key]
}

func (c *DeroRPC) setPending(key, txid string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil {
		c.pending = map[string]string{}
	}
	c.pending[key] = txid
}

func (c *DeroRPC) clearPending(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pending, key)
}

// NewDeroRPC returns a client with the fee and timing the simulator and testnet need; callers adjust the fields.
func NewDeroRPC(daemon, wallet, scid string) *DeroRPC {
	return &DeroRPC{Daemon: daemon, Wallet: wallet, SCID: scid, FixedFee: 3000, Timeout: 3 * time.Minute, Poll: 500 * time.Millisecond, HTTP: &http.Client{Timeout: 30 * time.Second}, Resends: 2}
}

// txStatus is what the daemon knows about one transaction: mined into a block, or still in its mempool, or
// neither — a transaction it has forgotten, which a fee too low for the miners eventually becomes.
func (c *DeroRPC) txStatus(ctx context.Context, txid string) (mined, inPool bool, err error) {
	var r struct {
		Txs []struct {
			ValidBlock string `json:"valid_block"`
			InPool     bool   `json:"in_pool"`
		} `json:"txs"`
	}
	if err := c.call(ctx, c.Daemon, "DERO.GetTransaction", map[string]interface{}{"txs_hashes": []string{txid}}, &r); err != nil {
		return false, false, err
	}
	if len(r.Txs) != 1 {
		return false, false, nil
	}
	return r.Txs[0].ValidBlock != "" && !r.Txs[0].InPool, r.Txs[0].InPool, nil
}

// errNotMined is waitTx's verdict when the transaction is still not in a block at the deadline — the one
// failure a higher fee can answer.
var errNotMined = errors.New("not mined")

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func endpoint(host string) string {
	if strings.Contains(host, "://") {
		return strings.TrimSuffix(host, "/") + "/json_rpc"
	}
	return "http://" + host + "/json_rpc"
}

// call posts one JSON-RPC request. A uint64 above 2^53 (an amount, a height far ahead) must never pass through
// float64 on its way to a comparison, so results are decoded with json.Number; the plain structs below use
// uint64, which the decoder then fills exactly.
func (c *DeroRPC) call(ctx context.Context, host, method string, params interface{}, out interface{}) error {
	body := map[string]interface{}{"jsonrpc": "2.0", "id": "1", "method": method}
	if params != nil {
		body["params"] = params
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(host), bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if host == c.Wallet && c.Login != "" {
		user, pass, _ := strings.Cut(c.Login, ":")
		req.SetBasicAuth(user, pass)
	}
	hc := c.HTTP
	if hc == nil {
		hc = fallbackRPCClient // never http.DefaultClient: it has no timeout, and a node that never answers would hold the swap
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()
	raw, err := readCapped(resp.Body, maxDeroReply)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	// derod and the wallet answer a JSON-RPC error with 200; anything else — the wallet's 401 "Authorization
	// Required" (walletapi/rpcserver/rpc_websocket_server.go), a proxy's page — is not a reply to read
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: http %d", method, resp.StatusCode)
	}
	return decodeEnvelope(method, raw, out)
}

// WalletAcceptsAnonymous asks the wallet one question with no credentials at all. A wallet that answers is one any
// local process — and any web page, through its /ws endpoint — can drive (GAP-008); on mainnet the companion
// refuses to run against one. A 401 is the answer wanted; any other failure is an error, not a "no".
func (c *DeroRPC) WalletAcceptsAnonymous(ctx context.Context) (bool, error) {
	anon := &DeroRPC{Daemon: c.Daemon, Wallet: c.Wallet, SCID: c.SCID, HTTP: c.HTTP, Timeout: c.Timeout, Poll: c.Poll}
	_, err := anon.Address(ctx)
	switch {
	case err == nil:
		return true, nil
	case strings.Contains(err.Error(), "http 401"):
		return false, nil
	}
	return false, err
}

// maxDeroReply bounds one daemon or wallet answer. The companion asks only for named keys, one lock's worth or a
// window of ids (getKeys, FindLock), so a legitimate answer is kilobytes; 8 MB is far above any of them and far
// below what would hurt the machine.
const maxDeroReply = 8 << 20

// fallbackRPCClient is the client for a DeroRPC built without one: a bounded wait, as NewDeroRPC gives.
var fallbackRPCClient = &http.Client{Timeout: 30 * time.Second}

// readCapped reads a whole reply and refuses one longer than max rather than truncating it: a truncated document
// would fail to decode anyway, but "longer than the cap" is the answer that says what happened, the same way
// every time.
func readCapped(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("a reply longer than %d bytes was refused", max)
	}
	return b, nil
}

// decodeEnvelope is the one way a JSON-RPC 2.0 reply is read: a single JSON value and nothing after it
// (json.Unmarshal refuses trailing data), an error object or a result, and — where the caller wants a result — a
// result that is present and not null. A reply that is neither is malformed and is refused, never read as zeros.
// Numbers go through json.Number, so a uint64 above 2^53 is never rounded through float64.
func decodeEnvelope(method string, raw []byte, out interface{}) error {
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcErr         `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("%s: decode: %w", method, err)
	}
	if env.Error != nil {
		return fmt.Errorf("%s: rpc error %d: %s", method, env.Error.Code, env.Error.Message)
	}
	if out == nil {
		return nil
	}
	if len(env.Result) == 0 || string(env.Result) == "null" {
		return fmt.Errorf("%s: the reply carries neither a result nor an error", method)
	}
	dec := json.NewDecoder(bytes.NewReader(env.Result))
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%s: result: %w", method, err)
	}
	return nil
}

// Arg is one sc_rpc argument as the wallet and the estimator take it.
type Arg struct {
	Name     string      `json:"name"`
	DataType string      `json:"datatype"`
	Value    interface{} `json:"value"`
}

func argU(n string, v uint64) Arg { return Arg{n, "U", v} }
func argS(n string, v string) Arg { return Arg{n, "S", v} }
func argH(n string, v string) Arg { return Arg{n, "H", v} }

// Transfer is one wallet transfer entry: a burn into the contract needs a destination, which is any registered
// account (the daemon's random address), exactly as the app builds a Lock. SCID names the asset a transfer moves
// ("" is DERO); PayloadRPC is the encrypted payload only the receiver reads (the stipend's port tag).
type Transfer struct {
	SCID        string `json:"scid,omitempty"`
	Destination string `json:"destination,omitempty"`
	Amount      uint64 `json:"amount"`
	Burn        uint64 `json:"burn,omitempty"`
	PayloadRPC  []Arg  `json:"payload_rpc,omitempty"`
}

func (c *DeroRPC) Height(ctx context.Context) (uint64, error) {
	var r struct {
		Height uint64 `json:"height"`
	}
	err := c.call(ctx, c.Daemon, "DERO.GetHeight", nil, &r)
	return r.Height, err
}

// StableHeight is the daemon's own finality mark (DERO.GetHeight.stableheight).
func (c *DeroRPC) StableHeight(ctx context.Context) (uint64, error) {
	var r struct {
		Stable uint64 `json:"stableheight"`
	}
	err := c.call(ctx, c.Daemon, "DERO.GetHeight", nil, &r)
	return r.Stable, err
}

// Info reports the daemon's network: the companion refuses to lower the timelock policy anywhere but the simulator.
func (c *DeroRPC) Info(ctx context.Context) (testnet bool, network string, err error) {
	var r struct {
		Testnet bool   `json:"testnet"`
		Network string `json:"network"`
	}
	err = c.call(ctx, c.Daemon, "DERO.GetInfo", nil, &r)
	return r.Testnet, r.Network, err
}

// Address is the wallet's own address.
func (c *DeroRPC) Address(ctx context.Context) (string, error) {
	var r struct {
		Address string `json:"address"`
	}
	err := c.call(ctx, c.Wallet, "GetAddress", nil, &r)
	return r.Address, err
}

// Balance is the wallet's spendable balance of one asset ("" is DERO) in that asset's atomic units:
// GetBalance's unlocked_balance, the figure a transfer can draw on (walletapi/rpcserver/rpc_getbalance.go).
func (c *DeroRPC) Balance(ctx context.Context, asset string) (uint64, error) {
	params := map[string]interface{}{}
	if asset != "" {
		params["scid"] = asset
	}
	var r struct {
		Unlocked uint64 `json:"unlocked_balance"`
	}
	err := c.call(ctx, c.Wallet, "GetBalance", params, &r)
	return r.Unlocked, err
}

// Pay is a plain DERO transfer from this wallet at ring 2. A non-zero port rides in the encrypted payload as its
// destination port (rpc.RPC_DESTINATION_PORT, "D"); both the receiving wallet and the sending one decode it onto
// their own entry (walletapi daemon_communication.go, ProcessPayload), which is how FindPayment finds the stipend
// again on either side without anyone passing a txid around. A driver topping a demo wallet up passes port 0.
func (c *DeroRPC) Pay(ctx context.Context, to string, amount, port uint64) (string, error) {
	xf := Transfer{Destination: to, Amount: amount}
	if port != 0 {
		xf.PayloadRPC = []Arg{argU("D", port)}
	}
	var r struct {
		TXID string `json:"txid"`
	}
	params := map[string]interface{}{"transfers": []Transfer{xf}, "ringsize": 2}
	if err := c.call(ctx, c.Wallet, "transfer", params, &r); err != nil {
		return "", err
	}
	if len(r.TXID) != 64 {
		return "", fmt.Errorf("transfer: the wallet returned no txid (%q)", r.TXID)
	}
	if _, err := c.waitTx(ctx, r.TXID); err != nil {
		return r.TXID, err
	}
	return r.TXID, nil
}

// FindPayment reads this wallet's own transfers (GetTransfers, walletapi/rpcserver/rpc_gettransfers.go) and keeps
// the first carrying the port. The wallet takes a dstport filter and ignores it — Show_Transfers filters by
// direction and height only (walletapi/wallet.go) — so the match is made here, on each entry's decoded dstport.
func (c *DeroRPC) FindPayment(ctx context.Context, incoming bool, port, minHeight uint64) (Payment, bool, error) {
	var r struct {
		Entries []struct {
			Height   uint64 `json:"height"`
			TXID     string `json:"txid"`
			Amount   uint64 `json:"amount"`
			Incoming bool   `json:"incoming"`
			Coinbase bool   `json:"coinbase"`
			Port     uint64 `json:"dstport"`
		} `json:"entries"`
	}
	params := map[string]interface{}{"in": incoming, "out": !incoming, "min_height": minHeight}
	if err := c.call(ctx, c.Wallet, "GetTransfers", params, &r); err != nil {
		return Payment{}, false, err
	}
	for _, e := range r.Entries {
		if e.Coinbase || e.Incoming != incoming || e.Port != port || e.Height < minHeight {
			continue
		}
		return Payment{TxID: e.TXID, Amount: e.Amount, Height: e.Height}, true, nil
	}
	return Payment{}, false, nil
}

// Registered asks the daemon for addr's encrypted balance at the tip. topoheight is -1 because 0 asks about the
// genesis block (rpc_dero_getencryptedbalance.go uses any topoheight >= 0 it is given). derod answers
// errormsg.ErrAccountUnregistered, "Account Unregistered", for an address that has never registered; any other
// failure is an error, never a "no".
func (c *DeroRPC) Registered(ctx context.Context, addr string) (bool, error) {
	var r struct {
		Registration int64 `json:"registration"`
	}
	err := c.call(ctx, c.Daemon, "DERO.GetEncryptedBalance", map[string]interface{}{"address": addr, "topoheight": -1}, &r)
	if err != nil {
		if strings.Contains(err.Error(), "Account Unregistered") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// CodeSHA256 reads the contract's code (DERO.GetSC with code and no variables) and hashes the bytes as stored.
func (c *DeroRPC) CodeSHA256(ctx context.Context) (string, error) {
	var r struct {
		Code string `json:"code"`
	}
	if err := c.call(ctx, c.Daemon, "DERO.GetSC", map[string]interface{}{"scid": c.SCID, "code": true, "variables": false}, &r); err != nil {
		return "", err
	}
	if r.Code == "" {
		return "", fmt.Errorf("DERO.GetSC: no code at %s", c.SCID)
	}
	return SHA256Hex([]byte(r.Code)), nil
}

// Deployment reads the contract's code and its install arguments in one DERO.GetSC (code:true and keysstring; derod
// answers both in one reply, rpc_dero_getsc.go), so the hash and the values describe the same state. A key the
// contract does not store — a v2 install has none of the three — reads as 0.
func (c *DeroRPC) Deployment(ctx context.Context) (Deployment, error) {
	keys := []string{"w1min", "w2min", "g2"}
	var r struct {
		Code   string   `json:"code"`
		Values []string `json:"valuesstring"`
	}
	params := map[string]interface{}{"scid": c.SCID, "code": true, "variables": false, "keysstring": keys}
	if err := c.call(ctx, c.Daemon, "DERO.GetSC", params, &r); err != nil {
		return Deployment{}, err
	}
	if r.Code == "" {
		return Deployment{}, fmt.Errorf("DERO.GetSC: no code at %s", c.SCID)
	}
	if len(r.Values) != len(keys) {
		return Deployment{}, fmt.Errorf("DERO.GetSC: %d keys asked for, %d values answered", len(keys), len(r.Values))
	}
	d := Deployment{CodeSHA256: SHA256Hex([]byte(r.Code))}
	for i, dst := range []*uint64{&d.W1Min, &d.W2Min, &d.G2} {
		if v, err := strconv.ParseUint(r.Values[i], 10, 64); err == nil {
			*dst = v
		}
	}
	return d, nil
}

func (c *DeroRPC) randomAddress(ctx context.Context) (string, error) {
	var r struct {
		Address []string `json:"address"`
	}
	if err := c.call(ctx, c.Daemon, "DERO.GetRandomAddress", map[string]interface{}{}, &r); err != nil {
		return "", err
	}
	if len(r.Address) == 0 {
		return "", errors.New("DERO.GetRandomAddress: no address")
	}
	return r.Address[0], nil
}

type gas struct {
	GasCompute uint64 `json:"gascompute"`
	GasStorage uint64 `json:"gasstorage"`
}

// estimate mirrors what the wallet will submit: the daemon estimator needs SC_ACTION and SC_ID / SC_CODE added.
func (c *DeroRPC) estimate(ctx context.Context, signer, code string, xfers []Transfer, scRPC []Arg) (gas, error) {
	estRPC := append([]Arg{}, scRPC...)
	est := map[string]interface{}{"transfers": xfers, "signer": signer, "ringsize": 2}
	if code != "" {
		estRPC = append(estRPC, argU("SC_ACTION", 1), argS("SC_CODE", code))
		est["sc"] = code
	} else {
		estRPC = append(estRPC, argU("SC_ACTION", 0), argH("SC_ID", c.SCID))
		est["scid"] = c.SCID
	}
	est["sc_rpc"] = estRPC
	var g gas
	err := c.call(ctx, c.Daemon, "DERO.GetGasEstimate", est, &g)
	return g, err
}

// submit hands one transfer to the wallet at ring size 2 (SIGNER() is real only there) and returns the txid.
func (c *DeroRPC) submit(ctx context.Context, code string, xfers []Transfer, scRPC []Arg, fee uint64) (string, error) {
	params := map[string]interface{}{"transfers": xfers, "sc_rpc": scRPC, "ringsize": 2, "fees": fee}
	if code != "" {
		params["sc"] = code
	} else {
		params["scid"] = c.SCID
	}
	var r struct {
		TXID string `json:"txid"`
	}
	if err := c.call(ctx, c.Wallet, "transfer", params, &r); err != nil {
		return "", err
	}
	if len(r.TXID) != 64 {
		return "", fmt.Errorf("transfer: the wallet returned no txid (%q)", r.TXID)
	}
	return r.TXID, nil
}

// waitTx blocks until txid is in a valid block, one more block exists, and the wallet has synced past it — the
// point at which a DERO.GetSC read reflects the call. Inclusion is not execution: callers read the contract.
func (c *DeroRPC) waitTx(ctx context.Context, txid string) (uint64, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	poll := c.Poll
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	var height uint64
	for {
		var r struct {
			Txs []struct {
				ValidBlock  string `json:"valid_block"`
				BlockHeight uint64 `json:"block_height"`
				InPool      bool   `json:"in_pool"`
			} `json:"txs"`
		}
		err := c.call(ctx, c.Daemon, "DERO.GetTransaction", map[string]interface{}{"txs_hashes": []string{txid}}, &r)
		if err == nil && len(r.Txs) == 1 && r.Txs[0].ValidBlock != "" && !r.Txs[0].InPool {
			height = r.Txs[0].BlockHeight
			break
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("tx %s %w within %v", txid, errNotMined, timeout)
		}
		if err := sleep(ctx, poll); err != nil {
			return 0, err
		}
	}
	for {
		h, err := c.Height(ctx)
		if err != nil {
			return 0, err
		}
		if h > height {
			break
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("no new block after tx %s", txid)
		}
		if err := sleep(ctx, poll); err != nil {
			return 0, err
		}
	}
	for {
		var r struct {
			Height uint64 `json:"height"`
		}
		if err := c.call(ctx, c.Wallet, "GetHeight", nil, &r); err != nil {
			return 0, err
		}
		if r.Height >= height+1 {
			return height, nil
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("wallet did not sync to %d", height+1)
		}
		if err := sleep(ctx, poll); err != nil {
			return 0, err
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Install puts htlc.bas (already passed through reference.Canon by the caller) on the chain with its Initialize
// arguments (HTLCInstallArgs; htlc.bas v3 refuses an install without them) and returns its scid, verifying the code
// landed. The estimator refuses some code with a graviton bucket panic (simrpc R-16); storage gas for an install is
// the code size plus a few hundred units, so that case is fee'd by size.
func (c *DeroRPC) Install(ctx context.Context, signer, code string, args ...Arg) (scid string, storageGas uint64, err error) {
	g, err := c.estimate(ctx, signer, code, nil, args)
	if err != nil && strings.Contains(err.Error(), "Bucket cannot start") {
		g, err = gas{GasStorage: uint64(len(code)) + 512}, nil
	}
	if err != nil {
		return "", 0, err
	}
	txid, err := c.submit(ctx, code, nil, args, g.GasStorage+200)
	if err != nil {
		return "", 0, err
	}
	if _, err := c.waitTx(ctx, txid); err != nil {
		return "", 0, err
	}
	var r struct {
		Code string `json:"code"`
	}
	if err := c.call(ctx, c.Daemon, "DERO.GetSC", map[string]interface{}{"scid": txid, "code": true, "variables": false}, &r); err != nil {
		return "", 0, err
	}
	if r.Code != code {
		return "", 0, fmt.Errorf("install %s confirmed but the contract did not execute (fee %d)", txid, g.GasStorage)
	}
	return txid, g.GasStorage, nil
}

// scState is a set of the contract's string-keyed variables: uint64 values as their decimal text, string
// values as hex of their bytes. DERO.GetSC returns both encodings that way, whether the whole state was asked
// for (`stringkeys`, uint64 as a json.Number) or named keys were (`valuesstring`, uint64 as decimal text), and
// the accessors below take either.
type scState struct {
	StringKeys map[string]interface{} `json:"stringkeys"`
	Balance    uint64                 `json:"balance"`
}

// getKeys reads exactly the named string keys (DERO.GetSC's `keysstring`, vendor/derohe/rpc/daemon_rpc.go).
// Reading the WHOLE contract instead — `variables:true` — is what a padded contract kills: htlc.bas's Lock is
// permissionless, every lock adds ten keys and nothing ever deletes one, so past the response cap in call()
// every read fails for every companion on that scid, for ever, and a maker who cannot read cannot refund. A
// keyed read's size is set by what was asked for, so it does not grow with the contract.
//
// A key the contract has not set comes back as "NOT AVAILABLE err: ..." (rpc_dero_getsc.go) and is left out of
// the result. A daemon that answers a different number of values than were asked for is one whose answer
// cannot be lined up with the request, and that is an error rather than a set of absent keys.
func (c *DeroRPC) getKeys(ctx context.Context, keys []string) (map[string]string, error) {
	var r struct {
		Values []string `json:"valuesstring"`
	}
	params := map[string]interface{}{"scid": c.SCID, "variables": false, "code": false, "keysstring": keys}
	if err := c.call(ctx, c.Daemon, "DERO.GetSC", params, &r); err != nil {
		return nil, err
	}
	if len(r.Values) != len(keys) {
		return nil, fmt.Errorf("DERO.GetSC: %d keys asked for, %d values answered", len(keys), len(r.Values))
	}
	out := make(map[string]string, len(keys))
	for i, k := range keys {
		v := r.Values[i]
		if strings.HasPrefix(v, "NOT AVAILABLE") || v == "Unmarshal error" || v == "UNKNOWN Data type" {
			continue
		}
		out[k] = v
	}
	return out, nil
}

// keyState reads the named keys and returns them as an scState the accessors below can read.
func (c *DeroRPC) keyState(ctx context.Context, keys []string) (scState, error) {
	vals, err := c.getKeys(ctx, keys)
	if err != nil {
		return scState{}, err
	}
	st := scState{StringKeys: make(map[string]interface{}, len(vals))}
	for k, v := range vals {
		st.StringKeys[k] = v
	}
	return st, nil
}

// lockKeys is every key htlc.bas writes for one lock, including the two shares it reveals on an exit.
func lockKeys(id uint64) []string {
	i := strconv.FormatUint(id, 10)
	return []string{"m_" + i, "t_" + i, "ha_" + i, "hk_" + i, "hb_" + i, "a_" + i, "v_" + i, "t1_" + i, "t2_" + i, "st_" + i, "xa_" + i, "xb_" + i}
}

// ContractBalance is the DERO the contract itself holds: every exit must leave it at zero. The balance comes
// back whether or not variables were asked for, so this asks for none.
func (c *DeroRPC) ContractBalance(ctx context.Context) (uint64, error) {
	var st scState
	err := c.call(ctx, c.Daemon, "DERO.GetSC", map[string]interface{}{"scid": c.SCID, "variables": false, "code": false}, &st)
	return st.Balance, err
}

func (s scState) u(k string) (uint64, error) {
	v, ok := s.StringKeys[k]
	if !ok {
		return 0, fmt.Errorf("key %s missing", k)
	}
	switch x := v.(type) {
	case json.Number:
		return strconv.ParseUint(x.String(), 10, 64)
	case string:
		return strconv.ParseUint(x, 10, 64)
	}
	return 0, fmt.Errorf("key %s: unexpected %T", k, v)
}

// hexStr returns a string variable's raw bytes as lowercase hex (the RPC's own encoding, normalised).
func (s scState) hexStr(k string) (string, error) {
	v, ok := s.StringKeys[k]
	if !ok {
		return "", fmt.Errorf("key %s missing", k)
	}
	str, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("key %s: not a string (%T)", k, v)
	}
	b, err := hex.DecodeString(str)
	if err != nil {
		return "", fmt.Errorf("key %s: %v", k, err)
	}
	return hex.EncodeToString(b), nil
}

// str returns a string variable's bytes as text (for the hashes and shares, which the contract stores as hex text).
func (s scState) str(k string) (string, error) {
	h, err := s.hexStr(k)
	if err != nil {
		return "", err
	}
	b, _ := hex.DecodeString(h)
	return string(b), nil
}

func (s scState) has(k string) bool { _, ok := s.StringKeys[k]; return ok }

func (c *DeroRPC) ReadLock(ctx context.Context, id uint64) (Lock, error) {
	st, err := c.keyState(ctx, lockKeys(id))
	if err != nil {
		return Lock{}, err
	}
	return parseLock(st, id)
}

func parseLock(st scState, id uint64) (Lock, error) {
	i := strconv.FormatUint(id, 10)
	if !st.has("st_" + i) {
		return Lock{}, ErrNoSuchLock
	}
	l := Lock{ID: id}
	var err error
	get := func(dst *string, k string, text bool) {
		if err != nil {
			return
		}
		if text {
			*dst, err = st.str(k)
		} else {
			*dst, err = st.hexStr(k)
		}
	}
	getU := func(dst *uint64, k string) {
		if err != nil {
			return
		}
		*dst, err = st.u(k)
	}
	get(&l.Maker, "m_"+i, false)
	get(&l.Taker, "t_"+i, false)
	get(&l.HA, "ha_"+i, true)
	get(&l.HK, "hk_"+i, true)
	get(&l.HB, "hb_"+i, true)
	get(&l.Asset, "a_"+i, false)
	getU(&l.Amount, "v_"+i)
	getU(&l.T1, "t1_"+i)
	getU(&l.T2, "t2_"+i)
	getU(&l.State, "st_"+i)
	if err == nil && st.has("xa_"+i) {
		get(&l.XA, "xa_"+i, true)
	}
	if err == nil && st.has("xb_"+i) {
		get(&l.XB, "xb_"+i, true)
	}
	if err != nil {
		return Lock{}, fmt.Errorf("lock %d: %w", id, err)
	}
	return l, nil
}

// ReadKeys returns every stored key of lock id as the contract holds it, printable: numbers in decimal, hex
// text as itself, raw bytes (addresses, an asset id) as hex. It is what a driver prints as evidence.
func (c *DeroRPC) ReadKeys(ctx context.Context, id uint64) (map[string]string, error) {
	st, err := c.keyState(ctx, lockKeys(id))
	if err != nil {
		return nil, err
	}
	i := strconv.FormatUint(id, 10)
	if !st.has("st_" + i) {
		return nil, ErrNoSuchLock
	}
	out := map[string]string{}
	for _, k := range []string{"m_", "t_", "a_"} {
		if st.has(k + i) {
			if out[k+i], err = st.hexStr(k + i); err != nil {
				return nil, err
			}
		}
	}
	for _, k := range []string{"ha_", "hk_", "hb_", "xa_", "xb_"} {
		if st.has(k + i) {
			if out[k+i], err = st.str(k + i); err != nil {
				return nil, err
			}
		}
	}
	for _, k := range []string{"v_", "t1_", "t2_", "st_"} {
		if st.has(k + i) {
			n, err := st.u(k + i)
			if err != nil {
				return nil, err
			}
			out[k+i] = strconv.FormatUint(n, 10)
		}
	}
	return out, nil
}

// findLockWindow is how many ids one request asks about while FindLock walks the contract backwards. Each id
// contributes one hash, so a window's answer is tens of kilobytes whatever the contract has grown to.
const findLockWindow = 256

// FindLock scans the contract, newest first, for a lock carrying exactly these three hashes from this maker.
// It reads ha alone for a window of ids — ha is fresh for one swap, so at most one id in the contract carries
// it — and reads the whole lock only for an id whose ha matches.
func (c *DeroRPC) FindLock(ctx context.Context, makerRaw, ha, hk, hb string) (Lock, bool, error) {
	head, err := c.keyState(ctx, []string{"n"})
	if err != nil {
		return Lock{}, false, err
	}
	n, err := head.u("n")
	if err != nil {
		return Lock{}, false, err
	}
	for hi := n; hi > 0; {
		lo := uint64(0)
		if hi > findLockWindow {
			lo = hi - findLockWindow
		}
		keys := make([]string, 0, hi-lo)
		for id := hi; id > lo; id-- {
			keys = append(keys, "ha_"+strconv.FormatUint(id-1, 10))
		}
		win, err := c.keyState(ctx, keys)
		if err != nil {
			return Lock{}, false, err
		}
		for id := hi; id > lo; id-- {
			got, err := win.str("ha_" + strconv.FormatUint(id-1, 10))
			if err != nil || got != ha {
				continue
			}
			l, err := c.ReadLock(ctx, id-1)
			if err != nil {
				return Lock{}, false, err
			}
			if l.HA == ha && l.HK == hk && l.HB == hb && l.Maker == makerRaw {
				return l, true, nil
			}
		}
		hi = lo
	}
	return Lock{}, false, nil
}

func (c *DeroRPC) Lock(ctx context.Context, ha, hk, hb, taker string, t1, t2 uint64, asset string, amount, cutoff uint64) (string, uint64, error) {
	if asset != "" && !isHash(asset) {
		return "", 0, fmt.Errorf("asset %q is not a 64 lower-case hex SCID", asset)
	}
	me, err := c.Address(ctx)
	if err != nil {
		return "", 0, err
	}
	meRaw, err := DeroRaw(me)
	if err != nil {
		return "", 0, err
	}
	// A Lock this client already sent and did not see mined is invisible to the lookup below until it leaves
	// the mempool, so it is asked after by txid first: while the node still holds it, nothing new goes out.
	key := ha + "|" + hk + "|" + hb
	if txid := c.pendingTx(key); txid != "" {
		mined, inPool, err := c.txStatus(ctx, txid)
		if err != nil {
			return txid, 0, err
		}
		if inPool && !mined {
			return txid, 0, fmt.Errorf("the Lock transaction %s from an earlier attempt is still in the mempool, where the contract's state cannot show it: not sending another", txid)
		}
		// mined, or gone from the node altogether: from here the lookup tells the truth either way
		c.clearPending(key)
	}
	if l, found, err := c.FindLock(ctx, meRaw, ha, hk, hb); err != nil {
		return "", 0, err
	} else if found {
		return "", l.ID, nil
	}
	// MT-A03(a): the daemon's estimate below runs at height 0 and passes a Lock the chain will refuse for its width, so
	// this is the only check before the fee is spent
	h, err := c.Height(ctx)
	if err != nil {
		return "", 0, err
	}
	if h >= cutoff {
		return "", 0, fmt.Errorf("%w: at height %d a Lock with t1 %d could execute less than w1min before t1 (lock cutoff %d)", ErrLockTooLate, h, t1, cutoff)
	}
	dest, err := c.randomAddress(ctx)
	if err != nil {
		return "", 0, err
	}
	// An asset lock burns the asset alone into the contract. The wallet adds the zero-DERO payload that carries the
	// fee ("if we do not have base we can not detect fees", walletapi/wallet_transfer.go), and at the ring size
	// submit sets for every payload, 2, that payload is what names the signer (transaction_execute.go,
	// Extract_signer). At any other ring size the Lock is refused and what it carried is burned (Invariant 12).
	xfers := []Transfer{{SCID: asset, Destination: dest, Burn: amount}}
	args := []Arg{argS("entrypoint", "Lock"), argS("ha", ha), argS("hk", hk), argS("hb", hb), argS("taker", taker), argU("t1", t1), argU("t2", t2), argS("asset", asset)}
	g, err := c.estimate(ctx, me, "", xfers, args)
	if err != nil {
		return "", 0, fmt.Errorf("Lock refused at estimate: %w", err)
	}
	txid, err := c.submit(ctx, "", xfers, args, g.GasStorage+200)
	if err != nil {
		return "", 0, err
	}
	c.setPending(key, txid)
	if _, err := c.waitTx(ctx, txid); err != nil {
		return txid, 0, err
	}
	c.clearPending(key)
	l, found, err := c.FindLock(ctx, meRaw, ha, hk, hb)
	if err != nil {
		return txid, 0, err
	}
	if !found {
		return txid, 0, fmt.Errorf("Lock %s was included but the contract recorded no lock", txid)
	}
	return txid, l.ID, nil
}

// exit sends one of the four exits and proves it executed by reading st_<id> back: a call the contract refuses
// is still mined, so inclusion alone would be a lie. A transaction that does not mine within Timeout is sent
// again at double the fee, Resends times at most, while its window is open (canResend). A Claim is not sent at
// all, first time included, once height + 1 >= cutoff, and a Punish once height >= cutoff.
func (c *DeroRPC) exit(ctx context.Context, id uint64, entry string, args []Arg, want uint64, estimated bool, cutoff uint64) (string, error) {
	all := append([]Arg{argS("entrypoint", entry), argU("id", id)}, args...)
	fee := c.FixedFee
	if fee == 0 {
		fee = 3000
	}
	if entry == "Claim" || entry == "Punish" {
		h, err := c.Height(ctx)
		if err != nil {
			return "", err
		}
		if entry == "Claim" && h+1 >= cutoff {
			return "", fmt.Errorf("Claim not sent: at height %d it could be mined inside the last blocks before t1 (claim cutoff %d)", h, cutoff)
		}
		if entry == "Punish" && h >= cutoff {
			return "", fmt.Errorf("Punish not sent: at height %d it could execute, or become stable, at or after t2 + g2, where htlc.bas v3 refuses it (punish cutoff %d)", h, cutoff)
		}
	}
	if !estimated {
		// Refund, Punish and Reclaim cannot be estimated, so a call on a lock that has already moved would be mined, refused
		// and paid for. One read first saves that fee. A read that fails does not stop the send: a maker who cannot
		// read must still refund (refundBlind), and the contract refuses the call harmlessly if it is late.
		if l, err := c.ReadLock(ctx, id); err == nil && l.State != LockOpen {
			return "", fmt.Errorf("%s not sent: lock %d has already moved (st_%d = %d)", entry, id, id, l.State)
		}
	}
	if estimated {
		me, err := c.Address(ctx)
		if err != nil {
			return "", err
		}
		g, err := c.estimate(ctx, me, "", nil, all)
		if err != nil {
			return "", fmt.Errorf("%s refused at estimate: %w", entry, withholdArgs(err, all))
		}
		fee = g.GasStorage + 200
		if c.Log != nil {
			c.Log("%s estimated: storage gas %d, fee %d", entry, g.GasStorage, fee)
		}
	}
	var txid string
	for attempt := 0; ; attempt++ {
		var err error
		txid, err = c.submit(ctx, "", nil, all, fee)
		if err != nil {
			return "", withholdArgs(err, all)
		}
		_, err = c.waitTx(ctx, txid)
		if err == nil {
			break
		}
		if !errors.Is(err, errNotMined) || attempt >= c.Resends {
			return txid, withholdArgs(err, all)
		}
		h, herr := c.Height(ctx)
		if herr != nil {
			return txid, herr
		}
		l, lerr := c.ReadLock(ctx, id)
		if lerr != nil {
			return txid, lerr
		}
		if l.State != LockOpen {
			return txid, fmt.Errorf("%s %s not mined, and the lock has moved on (st_%d = %d)", entry, txid, id, l.State)
		}
		if !canResend(entry, h, l, cutoff) {
			return txid, fmt.Errorf("%s %s not mined, and at height %d a re-send could no longer land in its window (t1 %d, t2 %d): %w", entry, txid, h, l.T1, l.T2, withholdArgs(err, all))
		}
		fee *= 2
		if c.Log != nil {
			c.Log("%s %s not mined at fee %d; re-sending at %d (height %d, t1 %d, t2 %d)", entry, txid, fee/2, fee, h, l.T1, l.T2)
		}
	}
	l, err := c.ReadLock(ctx, id)
	if err != nil {
		return txid, err
	}
	if l.State != want {
		return txid, fmt.Errorf("%s %s was included but the contract refused it (st_%d = %d)", entry, txid, id, l.State)
	}
	return txid, nil
}

// canResend is the policy of §4: a height-gated call is re-sent only while it can still be mined and be stable
// inside its window — a Claim while h + 1 < cutoff, which is claimCutoff(t1, margin), the very height the state
// machine stops sending Claims at; a Refund while h < t2; a Punish while h < cutoff, which is punishCutoff(t2, g2,
// margin), since htlc.bas v3 refuses it from t2 + g2; Reclaim has no end. XMR-06: the Claim rule was once
// h + 8 + 1 < t1, which with the default margin let a re-send out at eight heights inside the band [t1 − 16, t1)
// that the margin exists to keep empty.
func canResend(entry string, h uint64, l Lock, cutoff uint64) bool {
	switch entry {
	case "Claim":
		return h+1 < cutoff
	case "Refund":
		return h < l.T2
	case "Punish":
		return h < cutoff
	}
	return true
}

// Claim is estimated: every check it makes holds at height 0 (0 < t1), so a wrong preimage is refused by the
// daemon before any fee is spent.
func (c *DeroRPC) Claim(ctx context.Context, id uint64, sa, kb string, cutoff uint64) (string, error) {
	return c.exit(ctx, id, "Claim", []Arg{argS("sa", sa), argS("kb", kb)}, LockClaimed, true, cutoff)
}

func (c *DeroRPC) Refund(ctx context.Context, id uint64, sb string) (string, error) {
	return c.exit(ctx, id, "Refund", []Arg{argS("sb", sb)}, LockRefunded, false, 0)
}

// Punish is the same call from whoever sends it: htlc.bas pays the stored taker whatever the signer (LIV-07), in
// [t2, t2 + g2) only since v3. Nothing is sent at or past cutoff.
func (c *DeroRPC) Punish(ctx context.Context, id uint64, cutoff uint64) (string, error) {
	return c.exit(ctx, id, "Punish", nil, LockPunished, false, cutoff)
}

// Reclaim is the maker's exit from t2 + g2 (htlc.bas v3), with Refund's arguments. The estimator runs at height 0,
// where it is always refused (0 < t2 + g2), so it goes out at FixedFee as Refund and Punish do.
func (c *DeroRPC) Reclaim(ctx context.Context, id uint64, sb string) (string, error) {
	return c.exit(ctx, id, "Reclaim", []Arg{argS("sb", sb)}, LockReclaimed, false, 0)
}

// --- DERO addresses ---
//
// The contract stores addresses raw (SIGNER(), ADDRESS_RAW): the 33-byte compressed key. A wallet reports the
// same account as bech32 with the network prefix ("dero" mainnet, "deto" testnet and simulator) over a payload of
// one version byte followed by the key. Comparing the taker the maker named with the address this companion owns
// therefore means decoding the bech32 and dropping the version byte. This is the same decoder as
// tools/simrpc/address.go (and app/rpc.js canonAddress); it is copied rather than imported because that package's
// policy is log.Fatal and nothing long-running may depend on it.

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

func bech32Polymod(values []int) int {
	gen := []int{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := 1
	for _, v := range values {
		b := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ v
		for i := 0; i < 5; i++ {
			if (b>>uint(i))&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}

func bech32HRPExpand(hrp string) []int {
	out := make([]int, 0, len(hrp)*2+1)
	for _, c := range hrp {
		out = append(out, int(c)>>5)
	}
	out = append(out, 0)
	for _, c := range hrp {
		out = append(out, int(c)&31)
	}
	return out
}

func bech32ConvertBits(data []int, from, to uint, pad bool) []int {
	acc, bits := 0, uint(0)
	max := (1 << to) - 1
	out := []int{}
	for _, v := range data {
		acc = acc<<from | v
		bits += from
		for bits >= to {
			bits -= to
			out = append(out, (acc>>bits)&max)
		}
	}
	if pad {
		if bits > 0 {
			out = append(out, (acc<<(to-bits))&max)
		}
	} else if bits >= from || (acc<<(to-bits))&max != 0 {
		return nil
	}
	return out
}

// DeroRaw returns the lowercase hex of the 33 raw bytes a DERO address string names — the form the contract
// stores in m_<id> and t_<id> — for a mainnet, testnet or simulator address, integrated or not.
func DeroRaw(addr string) (string, error) {
	s := strings.ToLower(addr)
	pos := strings.LastIndex(s, "1")
	if pos < 1 || pos+7 > len(s) {
		return "", fmt.Errorf("not a DERO address: %q", addr)
	}
	hrp := s[:pos]
	switch hrp {
	case "dero", "deto", "deroi", "detoi":
	default:
		return "", fmt.Errorf("not a DERO address (prefix %q): %q", hrp, addr)
	}
	data := make([]int, 0, len(s)-pos-1)
	for _, c := range s[pos+1:] {
		d := strings.IndexRune(bech32Charset, c)
		if d < 0 {
			return "", fmt.Errorf("not a DERO address (character %q): %q", c, addr)
		}
		data = append(data, d)
	}
	if bech32Polymod(append(bech32HRPExpand(hrp), data...)) != 1 {
		return "", fmt.Errorf("DERO address checksum failed: %q", addr)
	}
	raw := bech32ConvertBits(data[:len(data)-6], 5, 8, false)
	if len(raw) < 34 || raw[0] != 1 {
		return "", fmt.Errorf("DERO address payload is not version 1 with a 33-byte key: %q", addr)
	}
	b := make([]byte, 33)
	for i := range b {
		b[i] = byte(raw[1+i])
	}
	return hex.EncodeToString(b), nil
}
