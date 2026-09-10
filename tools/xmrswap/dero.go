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
	// when the lock was found rather than sent.
	Lock(ctx context.Context, ha, hk, hb, taker string, t1, t2 uint64, amount uint64) (txid string, id uint64, err error)
	Claim(ctx context.Context, id uint64, sa, kb string) (string, error)
	Refund(ctx context.Context, id uint64, sb string) (string, error)
	Punish(ctx context.Context, id uint64) (string, error)
	ReadLock(ctx context.Context, id uint64) (Lock, error) // every field, including xa/xb when revealed and st
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
	State  uint64 // 0 open, 1 claimed, 2 refunded, 3 punished
	XA     string // s_a, present once claimed
	XB     string // s_b, present once refunded
}

// Lock states as htlc.bas stores them in st_<id>.
const (
	LockOpen     uint64 = 0
	LockClaimed  uint64 = 1
	LockRefunded uint64 = 2
	LockPunished uint64 = 3
)

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
	// call can still land usefully — a Claim while height + 8 + 1 < t1, a Refund while height < t2 — and after
	// that the caller moves to the exit that remains.
	Resends int
	// Log, when set, reports a re-send; the client is otherwise silent.
	Log func(format string, a ...interface{})

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
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("%s: read: %w", method, err)
	}
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
	dec := json.NewDecoder(bytes.NewReader(env.Result))
	dec.UseNumber()
	return dec.Decode(out)
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
// account (the daemon's random address), exactly as the app builds a Lock.
type Transfer struct {
	SCID        string `json:"scid,omitempty"`
	Destination string `json:"destination,omitempty"`
	Amount      uint64 `json:"amount"`
	Burn        uint64 `json:"burn,omitempty"`
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

// Balance is the wallet's DERO balance in atomic units.
func (c *DeroRPC) Balance(ctx context.Context) (uint64, error) {
	var r struct {
		Balance uint64 `json:"balance"`
	}
	err := c.call(ctx, c.Wallet, "GetBalance", map[string]interface{}{}, &r)
	return r.Balance, err
}

// Pay is a plain DERO transfer from this wallet, for a driver that tops a demo wallet up. It is not part of the
// swap, which moves DERO only through the contract.
func (c *DeroRPC) Pay(ctx context.Context, to string, amount uint64) (string, error) {
	var r struct {
		TXID string `json:"txid"`
	}
	params := map[string]interface{}{"transfers": []Transfer{{Destination: to, Amount: amount}}, "ringsize": 2}
	if err := c.call(ctx, c.Wallet, "transfer", params, &r); err != nil {
		return "", err
	}
	if _, err := c.waitTx(ctx, r.TXID); err != nil {
		return r.TXID, err
	}
	return r.TXID, nil
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

// Install puts htlc.bas (already passed through reference.Canon by the caller) on the chain and returns its
// scid, verifying the code landed. The estimator refuses some code with a graviton bucket panic (simrpc R-16);
// storage gas for an install is the code size plus a few hundred units, so that case is fee'd by size.
func (c *DeroRPC) Install(ctx context.Context, signer, code string) (scid string, storageGas uint64, err error) {
	g, err := c.estimate(ctx, signer, code, nil, nil)
	if err != nil && strings.Contains(err.Error(), "Bucket cannot start") {
		g, err = gas{GasStorage: uint64(len(code)) + 512}, nil
	}
	if err != nil {
		return "", 0, err
	}
	txid, err := c.submit(ctx, code, nil, nil, g.GasStorage+200)
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

func (c *DeroRPC) Lock(ctx context.Context, ha, hk, hb, taker string, t1, t2 uint64, amount uint64) (string, uint64, error) {
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
	dest, err := c.randomAddress(ctx)
	if err != nil {
		return "", 0, err
	}
	xfers := []Transfer{{Destination: dest, Burn: amount}}
	args := []Arg{argS("entrypoint", "Lock"), argS("ha", ha), argS("hk", hk), argS("hb", hb), argS("taker", taker), argU("t1", t1), argU("t2", t2), argS("asset", "")}
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

// exit sends one of the three exits and proves it executed by reading st_<id> back: a call the contract refuses
// is still mined, so inclusion alone would be a lie. A transaction that does not mine within Timeout is sent
// again at double the fee, Resends times at most, while its window is open (canResend).
func (c *DeroRPC) exit(ctx context.Context, id uint64, entry string, args []Arg, want uint64, estimated bool) (string, error) {
	all := append([]Arg{argS("entrypoint", entry), argU("id", id)}, args...)
	fee := c.FixedFee
	if fee == 0 {
		fee = 3000
	}
	if estimated {
		me, err := c.Address(ctx)
		if err != nil {
			return "", err
		}
		g, err := c.estimate(ctx, me, "", nil, all)
		if err != nil {
			return "", fmt.Errorf("%s refused at estimate: %w", entry, err)
		}
		fee = g.GasStorage + 200
	}
	var txid string
	for attempt := 0; ; attempt++ {
		var err error
		txid, err = c.submit(ctx, "", nil, all, fee)
		if err != nil {
			return "", err
		}
		_, err = c.waitTx(ctx, txid)
		if err == nil {
			break
		}
		if !errors.Is(err, errNotMined) || attempt >= c.Resends {
			return txid, err
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
		if !canResend(entry, h, l) {
			return txid, fmt.Errorf("%s %s not mined, and at height %d a re-send could no longer land in its window (t1 %d, t2 %d): %w", entry, txid, h, l.T1, l.T2, err)
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
// inside its window — a Claim while h + 8 + 1 < t1, a Refund while h < t2; Punish has no end.
func canResend(entry string, h uint64, l Lock) bool {
	switch entry {
	case "Claim":
		return h+DefaultStableBlocks+1 < l.T1
	case "Refund":
		return h < l.T2
	}
	return true
}

// Claim is estimated: every check it makes holds at height 0 (0 < t1), so a wrong preimage is refused by the
// daemon before any fee is spent.
func (c *DeroRPC) Claim(ctx context.Context, id uint64, sa, kb string) (string, error) {
	return c.exit(ctx, id, "Claim", []Arg{argS("sa", sa), argS("kb", kb)}, LockClaimed, true)
}

func (c *DeroRPC) Refund(ctx context.Context, id uint64, sb string) (string, error) {
	return c.exit(ctx, id, "Refund", []Arg{argS("sb", sb)}, LockRefunded, false)
}

func (c *DeroRPC) Punish(ctx context.Context, id uint64) (string, error) {
	return c.exit(ctx, id, "Punish", nil, LockPunished, false)
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
