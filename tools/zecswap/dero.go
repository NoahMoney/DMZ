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
	"strconv"
	"strings"
	"sync"
	"time"
)

// The DERO leg: contracts/board.bas (the offer, the accept) and contracts/swap.bas (the escrow), read by named keys
// and called through the party's own wallet at ring size 2 — SIGNER() is real only there, and both contracts ask
// who the signer is. The request shapes, the keyed read, the pending-transaction memory and the address codec are
// tools/xmrswap/dero.go's, copied rather than imported because that package is another engineer's and its lock is a
// different contract.
//
// TRUST. The DERO daemon is a trusted party for what the two contracts hold (docs/ZEC_SWAP.md §9 Z14): the taker
// funds ZEC against the lock it reports. The wallet signs every call; nothing here holds a DERO key.
//
// THE WALLET'S LOGIN. A DERO wallet RPC started without --rpc-login authenticates nobody, and its /ws endpoint
// accepts every Origin (derohe walletapi/rpcserver/rpc_websocket_server.go), so while a swap runs any web page the
// user opens can call transfer on it (GAP-008). WalletUser and WalletPass are sent as HTTP basic auth on every call
// to the wallet, and the command line refuses mainnet without them, and refuses a mainnet wallet that answers a
// call made without them.
//
// READS are by named key (DERO.GetSC keysstring): an added key — board.bas v3's acceptance deadline, a payee
// swap.bas may store — is simply not asked for unless this file names it, and a key the contract has not set is
// absent rather than an error. Uint64 values come back as decimal text and strings as the hex of their bytes
// (derod rpc_dero_getsc.go).
//
// FEES. DERO.GetGasEstimate runs the contract at height 0 (the estimator never sets dvm.Simulator.height), so a
// call whose checks depend on the height is judged there against the wrong height. Refund (height ≥ expiry) and
// Cancel of an accepted offer (height ≥ its deadline) would be refused at estimate while the real call succeeds;
// Post and Lock carry an expiry that must be at most height + 1,000,000, which at height 0 fails for any expiry
// above 1,000,000 — every expiry on DERO mainnet. Those calls are sent with a fixed fee and proved by reading the
// contract back; Accept and Claim, whose checks hold at height 0, are estimated. A call the contract refuses is
// still mined, so inclusion alone is never taken as success: every call is followed by a read of the key it was
// meant to change.

// Dero is the DERO leg as the state machines see it.
type Dero interface {
	DeroReader
	FindOffer(ctx context.Context, makerRaw, hash string) (Offer, bool, error)
	HashRows(ctx context.Context, hash string) ([]uint64, error)
	FindEscrow(ctx context.Context, makerRaw, takerRaw, hash string) (Escrow, bool, error)
	Post(ctx context.Context, p PostArgs) (txid string, id uint64, err error)
	Accept(ctx context.Context, id uint64, refundPkh string, cltv uint64) (string, error)
	Cancel(ctx context.Context, id uint64) (string, error)
	Lock(ctx context.Context, hash, takerAddr string, expiry uint64, asset string, amount uint64) (txid string, id uint64, err error)
	Claim(ctx context.Context, id uint64, secret string) (string, error)
	Refund(ctx context.Context, id uint64) (string, error)
}

// Offer is one board.bas row. Maker and Taker are the lower-case hex of the raw 33-byte keys the contract stores
// (SIGNER()); the text fields are what was posted, as text.
type Offer struct {
	ID         uint64
	Maker      string
	Chain      string
	GiveAsset  string // "" for DERO, else the 64-hex SCID as posted
	GiveAmount uint64
	WantAmount uint64 // zatoshi
	WantAddr   string // the maker's transparent address
	Hash       string
	Expiry     uint64
	State      uint64
	Taker      string // once accepted
	RefundPkh  string // once accepted: 40 hex as the taker wrote it
	CLTV       uint64 // once accepted
	// AcceptDeadline is board.bas v3's ad_<i>: the accept height plus the board's window, by which the maker must
	// have locked; 0 on a board that does not record it.
	AcceptDeadline uint64
}

// Board states (st_<i>).
const (
	OfferOpen      uint64 = 0
	OfferAccepted  uint64 = 1
	OfferCancelled uint64 = 2
)

// Escrow is one swap.bas lock. Asset is the hex of the 32 raw bytes the contract stores, "" for DERO.
type Escrow struct {
	ID     uint64
	Maker  string
	Taker  string // "" = anyone may claim
	Hash   string
	Asset  string
	Amount uint64
	Expiry uint64
	State  uint64
	Secret string // x_<i>, once claimed
}

// Escrow states (st_<i>).
const (
	EscrowOpen     uint64 = 0
	EscrowClaimed  uint64 = 1
	EscrowRefunded uint64 = 2
)

// PostArgs are board.bas Post's arguments.
type PostArgs struct {
	Chain      string
	GiveAsset  string
	GiveAmount uint64
	WantAmount uint64
	WantAddr   string
	Hash       string
	Expiry     uint64
}

// ZcashChainLabel is the board's chain label for this market (docs/ZEC_SWAP.md §3 step 1).
const ZcashChainLabel = "zcash-t"

var (
	ErrNoSuchOffer  = errors.New("zecswap: no such offer on the board")
	ErrNoSuchEscrow = errors.New("zecswap: no such escrow")
	// ErrEstimate wraps a call the daemon's estimator refused: nothing was sent, so nothing is in flight.
	ErrEstimate = errors.New("refused at estimate")
)

// estimableExpiry is the largest expiry a Post or Lock can carry and still pass the estimator, which runs the
// contract at height 0 against "expiry > height + 1000000".
const estimableExpiry = 1_000_000

// DeroRPC talks to one daemon and one wallet, for one board and one escrow contract. A witness is a DeroRPC with
// no wallet: it only reads.
type DeroRPC struct {
	Daemon string
	Wallet string
	// WalletUser and WalletPass are the wallet RPC's --rpc-login, sent as basic auth on every wallet call.
	WalletUser string
	WalletPass string
	Board      string
	Escrow     string
	// FixedFee pays the calls the estimator cannot judge: Refund, Cancel, and a Post or Lock above
	// estimableExpiry. It must cover the call's storage gas; a Post or Lock that runs out is still mined, and the
	// read-back after it says so.
	FixedFee uint64
	Timeout  time.Duration
	Poll     time.Duration
	HTTP     *http.Client

	mu      sync.Mutex
	pending map[string]string
	me      string
}

// NewDeroRPC returns a client with the simulator's and testnet's timing.
func NewDeroRPC(daemon, wallet, board, escrow string) *DeroRPC {
	return &DeroRPC{Daemon: daemon, Wallet: wallet, Board: board, Escrow: escrow, FixedFee: 5000, Timeout: 3 * time.Minute, Poll: 500 * time.Millisecond, HTTP: &http.Client{Timeout: 30 * time.Second}}
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

func deroEndpoint(host string) string {
	if strings.Contains(host, "://") {
		return strings.TrimSuffix(host, "/") + "/json_rpc"
	}
	return "http://" + host + "/json_rpc"
}

// ErrWalletAuth is a wallet that refused the credentials, or wanted some and was given none.
var ErrWalletAuth = errors.New("the DERO wallet refused the call: HTTP 401")

// call posts one JSON-RPC 2.0 request to the daemon or the wallet. Answers are read through a size cap.
func (c *DeroRPC) call(ctx context.Context, host, method string, params interface{}, out interface{}) error {
	body := map[string]interface{}{"jsonrpc": "2.0", "id": "1", "method": method}
	if params != nil {
		body["params"] = params
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deroEndpoint(host), bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if host == c.Wallet && (c.WalletUser != "" || c.WalletPass != "") {
		req.SetBasicAuth(c.WalletUser, c.WalletPass)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%s: %w (start the wallet RPC with --rpc-login and give the companion the same user:password)", method, ErrWalletAuth)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20+1))
	if err != nil {
		return fmt.Errorf("%s: read: %w", method, err)
	}
	if len(raw) > 8<<20 {
		return fmt.Errorf("%s: the answer is larger than 8 MiB", method)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("%s: HTTP %d, not a JSON-RPC answer: %w", method, resp.StatusCode, err)
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

type deroArg struct {
	Name     string      `json:"name"`
	DataType string      `json:"datatype"`
	Value    interface{} `json:"value"`
}

func argU(n string, v uint64) deroArg { return deroArg{n, "U", v} }
func argS(n string, v string) deroArg { return deroArg{n, "S", v} }
func argH(n string, v string) deroArg { return deroArg{n, "H", v} }

type deroTransfer struct {
	SCID        string `json:"scid,omitempty"`
	Destination string `json:"destination,omitempty"`
	Amount      uint64 `json:"amount"`
	Burn        uint64 `json:"burn,omitempty"`
}

// Height is DERO.GetHeight's height.
func (c *DeroRPC) Height(ctx context.Context) (uint64, error) {
	var r struct {
		Height uint64 `json:"height"`
	}
	err := c.call(ctx, c.Daemon, "DERO.GetHeight", nil, &r)
	return r.Height, err
}

// StableHeight is DERO.GetHeight's stableheight: a block at or below it is never replaced.
func (c *DeroRPC) StableHeight(ctx context.Context) (uint64, error) {
	var r struct {
		Stable uint64 `json:"stableheight"`
	}
	err := c.call(ctx, c.Daemon, "DERO.GetHeight", nil, &r)
	return r.Stable, err
}

// Info is DERO.GetInfo's network: testnet is true for every network but mainnet, network is "Simulator" on the
// simulator (derod rpc_dero_getinfo.go).
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

// WalletAnswersWithoutLogin asks the wallet for its address with no credentials at all. A wallet that answers
// is one any local program — any web page, through /ws — can spend from (GAP-008).
func (c *DeroRPC) WalletAnswersWithoutLogin(ctx context.Context) (bool, error) {
	anon := &DeroRPC{Daemon: c.Daemon, Wallet: c.Wallet, HTTP: c.HTTP}
	_, err := anon.Address(ctx)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrWalletAuth) {
		return false, nil
	}
	return false, err
}

func (c *DeroRPC) myRaw(ctx context.Context) (string, error) {
	c.mu.Lock()
	me := c.me
	c.mu.Unlock()
	if me != "" {
		return me, nil
	}
	a, err := c.Address(ctx)
	if err != nil {
		return "", err
	}
	raw, err := DeroRaw(a)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.me = raw
	c.mu.Unlock()
	return raw, nil
}

// CodeSHA256 reads a contract's code (DERO.GetSC with code and no variables) and hashes the bytes as stored: the
// same bytes reference.Canon makes of the source (pin.go).
func (c *DeroRPC) CodeSHA256(ctx context.Context, scid string) (string, error) {
	var r struct {
		Code string `json:"code"`
	}
	if err := c.call(ctx, c.Daemon, "DERO.GetSC", map[string]interface{}{"scid": scid, "code": true, "variables": false}, &r); err != nil {
		return "", err
	}
	if r.Code == "" {
		return "", fmt.Errorf("DERO.GetSC: no code at %s", scid)
	}
	sum := sha256.Sum256([]byte(r.Code))
	return hex.EncodeToString(sum[:]), nil
}

// EscrowWMin reads swap.bas v2's wmin, the minimum escrow width fixed at install.
func (c *DeroRPC) EscrowWMin(ctx context.Context) (uint64, error) {
	ks, err := c.getKeys(ctx, c.Escrow, []string{"wmin"})
	if err != nil {
		return 0, err
	}
	if !ks.has("wmin") {
		return 0, errors.New("the escrow stores no wmin: it is not swap.bas v2")
	}
	return ks.u("wmin")
}

// Registered asks the daemon for addr's encrypted balance at the tip (topoheight -1; 0 would ask about genesis).
// derod answers "Account Unregistered" (errormsg.ErrAccountUnregistered) for an address that has never registered,
// and any other error is an error, not a verdict. swap.bas v2's Claim pays the named taker, and a transfer to an
// unregistered address is refused and the whole Claim reverted: a lock for such a taker is one nobody can claim,
// which only T_dero ends. This is tools/xmrswap/dero.go's Registered.
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

// estimate mirrors what the wallet will submit: the daemon's estimator needs SC_ACTION and SC_ID added.
func (c *DeroRPC) estimate(ctx context.Context, scid string, xfers []deroTransfer, args []deroArg) (uint64, error) {
	me, err := c.Address(ctx)
	if err != nil {
		return 0, err
	}
	est := append(append([]deroArg{}, args...), argU("SC_ACTION", 0), argH("SC_ID", scid))
	var g struct {
		GasStorage uint64 `json:"gasstorage"`
	}
	params := map[string]interface{}{"transfers": xfers, "signer": me, "ringsize": 2, "scid": scid, "sc_rpc": est}
	if err := c.call(ctx, c.Daemon, "DERO.GetGasEstimate", params, &g); err != nil {
		return 0, err
	}
	return g.GasStorage + 200, nil
}

// submit hands one call to the wallet at ring size 2 and returns the txid.
func (c *DeroRPC) submit(ctx context.Context, scid string, xfers []deroTransfer, args []deroArg, fee uint64) (string, error) {
	params := map[string]interface{}{"transfers": xfers, "sc_rpc": args, "ringsize": 2, "fees": fee, "scid": scid}
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

// waitTx blocks until txid is in a block, one more block exists, and the wallet has synced past it: the point at
// which DERO.GetSC shows the call's effect. Inclusion is not execution; callers read the contract.
func (c *DeroRPC) waitTx(ctx context.Context, txid string) error {
	timeout, poll := c.Timeout, c.Poll
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
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
			return fmt.Errorf("DERO tx %s not mined within %v", txid, timeout)
		}
		if err := sleepCtx(ctx, poll); err != nil {
			return err
		}
	}
	for {
		h, err := c.Height(ctx)
		if err != nil {
			return err
		}
		var w struct {
			Height uint64 `json:"height"`
		}
		if err := c.call(ctx, c.Wallet, "GetHeight", nil, &w); err != nil {
			return err
		}
		if h > height && w.Height >= height+1 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("DERO tx %s: no block after it, or the wallet did not sync past it", txid)
		}
		if err := sleepCtx(ctx, poll); err != nil {
			return err
		}
	}
}

func (c *DeroRPC) txInPool(ctx context.Context, txid string) (bool, error) {
	var r struct {
		Txs []struct {
			ValidBlock string `json:"valid_block"`
			InPool     bool   `json:"in_pool"`
		} `json:"txs"`
	}
	if err := c.call(ctx, c.Daemon, "DERO.GetTransaction", map[string]interface{}{"txs_hashes": []string{txid}}, &r); err != nil {
		return false, err
	}
	return len(r.Txs) == 1 && r.Txs[0].InPool, nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ---- keyed reads ----

// scKeys is a set of a contract's variables: uint64 values as decimal text, strings as the hex of their bytes.
type scKeys map[string]string

// getKeys reads exactly the named keys of one contract. A key the contract has not set comes back as
// "NOT AVAILABLE err: ..." and is left out; a daemon that answers a different number of values than were asked
// for cannot be lined up with the request and is an error.
func (c *DeroRPC) getKeys(ctx context.Context, scid string, keys []string) (scKeys, error) {
	var r struct {
		Values []string `json:"valuesstring"`
	}
	params := map[string]interface{}{"scid": scid, "variables": false, "code": false, "keysstring": keys}
	if err := c.call(ctx, c.Daemon, "DERO.GetSC", params, &r); err != nil {
		return nil, err
	}
	if len(r.Values) != len(keys) {
		return nil, fmt.Errorf("DERO.GetSC: %d keys asked for, %d values answered", len(keys), len(r.Values))
	}
	out := make(scKeys, len(keys))
	for i, k := range keys {
		v := r.Values[i]
		if strings.HasPrefix(v, "NOT AVAILABLE") || v == "Unmarshal error" || v == "UNKNOWN Data type" {
			continue
		}
		out[k] = v
	}
	return out, nil
}

func (s scKeys) has(k string) bool { _, ok := s[k]; return ok }

func (s scKeys) u(k string) (uint64, error) {
	v, ok := s[k]
	if !ok {
		return 0, fmt.Errorf("key %s missing", k)
	}
	return strconv.ParseUint(v, 10, 64)
}

// raw is a string variable's bytes as lower-case hex.
func (s scKeys) raw(k string) (string, error) {
	v, ok := s[k]
	if !ok {
		return "", fmt.Errorf("key %s missing", k)
	}
	b, err := hex.DecodeString(v)
	if err != nil {
		return "", fmt.Errorf("key %s: %v", k, err)
	}
	return hex.EncodeToString(b), nil
}

// text is a string variable's bytes as text.
func (s scKeys) text(k string) (string, error) {
	h, err := s.raw(k)
	if err != nil {
		return "", err
	}
	b, _ := hex.DecodeString(h)
	return string(b), nil
}

func offerKeys(id uint64) []string {
	i := strconv.FormatUint(id, 10)
	return []string{"m_" + i, "c_" + i, "ga_" + i, "gv_" + i, "wv_" + i, "wa_" + i, "h_" + i, "e_" + i, "st_" + i, "t_" + i, "rp_" + i, "cl_" + i, "ad_" + i}
}

func escrowKeys(id uint64) []string {
	i := strconv.FormatUint(id, 10)
	return []string{"m_" + i, "t_" + i, "h_" + i, "a_" + i, "v_" + i, "e_" + i, "st_" + i, "x_" + i}
}

// ReadOffer reads one board row by its keys.
func (c *DeroRPC) ReadOffer(ctx context.Context, id uint64) (Offer, error) {
	ks, err := c.getKeys(ctx, c.Board, offerKeys(id))
	if err != nil {
		return Offer{}, err
	}
	return parseOffer(ks, id)
}

func parseOffer(ks scKeys, id uint64) (Offer, error) {
	i := strconv.FormatUint(id, 10)
	if !ks.has("st_" + i) {
		return Offer{}, ErrNoSuchOffer
	}
	o := Offer{ID: id}
	var err error
	for _, f := range []struct {
		dst  *string
		key  string
		text bool
	}{{&o.Maker, "m_", false}, {&o.Chain, "c_", true}, {&o.GiveAsset, "ga_", true}, {&o.WantAddr, "wa_", true}, {&o.Hash, "h_", true}} {
		if err == nil {
			if f.text {
				*f.dst, err = ks.text(f.key + i)
			} else {
				*f.dst, err = ks.raw(f.key + i)
			}
		}
	}
	for _, f := range []struct {
		dst *uint64
		key string
	}{{&o.GiveAmount, "gv_"}, {&o.WantAmount, "wv_"}, {&o.Expiry, "e_"}, {&o.State, "st_"}} {
		if err == nil {
			*f.dst, err = ks.u(f.key + i)
		}
	}
	if err == nil && ks.has("t_"+i) {
		o.Taker, err = ks.raw("t_" + i)
	}
	if err == nil && ks.has("rp_"+i) {
		o.RefundPkh, err = ks.text("rp_" + i)
	}
	if err == nil && ks.has("cl_"+i) {
		o.CLTV, err = ks.u("cl_" + i)
	}
	if err == nil && ks.has("ad_"+i) {
		o.AcceptDeadline, err = ks.u("ad_" + i)
	}
	if err != nil {
		return Offer{}, fmt.Errorf("offer %d: %w", id, err)
	}
	return o, nil
}

// ReadEscrow reads one swap.bas lock by its keys.
func (c *DeroRPC) ReadEscrow(ctx context.Context, id uint64) (Escrow, error) {
	ks, err := c.getKeys(ctx, c.Escrow, escrowKeys(id))
	if err != nil {
		return Escrow{}, err
	}
	return parseEscrow(ks, id)
}

func parseEscrow(ks scKeys, id uint64) (Escrow, error) {
	i := strconv.FormatUint(id, 10)
	if !ks.has("st_" + i) {
		return Escrow{}, ErrNoSuchEscrow
	}
	e := Escrow{ID: id}
	var err error
	if e.Maker, err = ks.raw("m_" + i); err == nil {
		if e.Taker, err = ks.raw("t_" + i); err == nil {
			if e.Hash, err = ks.text("h_" + i); err == nil {
				e.Asset, err = ks.raw("a_" + i)
			}
		}
	}
	for _, f := range []struct {
		dst *uint64
		key string
	}{{&e.Amount, "v_"}, {&e.Expiry, "e_"}, {&e.State, "st_"}} {
		if err == nil {
			*f.dst, err = ks.u(f.key + i)
		}
	}
	if err == nil && ks.has("x_"+i) {
		e.Secret, err = ks.text("x_" + i)
	}
	if err != nil {
		return Escrow{}, fmt.Errorf("escrow %d: %w", id, err)
	}
	return e, nil
}

// scanWindow is how many ids one request asks about while a scan walks a contract backwards: each id contributes
// one hash, so an answer stays tens of kilobytes whatever the contract has grown to.
const scanWindow = 256

// scanHashes walks a contract's h_<id> keys newest first and calls match for each id carrying hash; match returns
// true to stop.
func (c *DeroRPC) scanHashes(ctx context.Context, scid, hash string, match func(id uint64) (bool, error)) error {
	head, err := c.getKeys(ctx, scid, []string{"n"})
	if err != nil {
		return err
	}
	n, err := head.u("n")
	if err != nil {
		return err
	}
	for hi := n; hi > 0; {
		lo := uint64(0)
		if hi > scanWindow {
			lo = hi - scanWindow
		}
		keys := make([]string, 0, hi-lo)
		for id := hi; id > lo; id-- {
			keys = append(keys, "h_"+strconv.FormatUint(id-1, 10))
		}
		win, err := c.getKeys(ctx, scid, keys)
		if err != nil {
			return err
		}
		for id := hi; id > lo; id-- {
			got, err := win.text("h_" + strconv.FormatUint(id-1, 10))
			if err != nil || got != hash {
				continue
			}
			stop, err := match(id - 1)
			if err != nil || stop {
				return err
			}
		}
		hi = lo
	}
	return nil
}

// FindOffer is the newest board row this maker posted under hash.
func (c *DeroRPC) FindOffer(ctx context.Context, makerRaw, hash string) (Offer, bool, error) {
	var found Offer
	var ok bool
	err := c.scanHashes(ctx, c.Board, hash, func(id uint64) (bool, error) {
		o, err := c.ReadOffer(ctx, id)
		if err != nil {
			return false, err
		}
		if o.Maker == makerRaw {
			found, ok = o, true
			return true, nil
		}
		return false, nil
	})
	return found, ok, err
}

// HashRows is every board row carrying hash: a hash on more than one row is a secret that opens more than one
// swap (docs/ZEC_SWAP.md §9 Z8).
func (c *DeroRPC) HashRows(ctx context.Context, hash string) ([]uint64, error) {
	var ids []uint64
	err := c.scanHashes(ctx, c.Board, hash, func(id uint64) (bool, error) {
		ids = append(ids, id)
		return false, nil
	})
	return ids, err
}

// FindEscrow is the newest lock this maker made for this taker under hash.
func (c *DeroRPC) FindEscrow(ctx context.Context, makerRaw, takerRaw, hash string) (Escrow, bool, error) {
	var found Escrow
	var ok bool
	err := c.scanHashes(ctx, c.Escrow, hash, func(id uint64) (bool, error) {
		e, err := c.ReadEscrow(ctx, id)
		if err != nil {
			return false, err
		}
		if e.Maker == makerRaw && e.Taker == takerRaw {
			found, ok = e, true
			return true, nil
		}
		return false, nil
	})
	return found, ok, err
}

// ---- calls ----

// guardPending refuses to send a call while the transaction of an earlier attempt is still in the daemon's pool:
// the contract's state cannot show a pooled call, so a lookup before it would find nothing and send a second.
func (c *DeroRPC) guardPending(ctx context.Context, key string) error {
	txid := c.pendingTx(key)
	if txid == "" {
		return nil
	}
	inPool, err := c.txInPool(ctx, txid)
	if err != nil {
		return err
	}
	if inPool {
		return fmt.Errorf("the transaction %s of an earlier attempt is still in the pool: not sending another", txid)
	}
	c.clearPending(key)
	return nil
}

// Post puts an offer on the board, unless this maker already has one under the hash.
func (c *DeroRPC) Post(ctx context.Context, p PostArgs) (string, uint64, error) {
	me, err := c.myRaw(ctx)
	if err != nil {
		return "", 0, err
	}
	key := "post|" + p.Hash
	if err := c.guardPending(ctx, key); err != nil {
		return "", 0, err
	}
	if o, ok, err := c.FindOffer(ctx, me, p.Hash); err != nil {
		return "", 0, err
	} else if ok {
		return "", o.ID, nil
	}
	args := []deroArg{argS("entrypoint", "Post"), argS("chain", p.Chain), argS("giveAsset", p.GiveAsset), argU("giveAmount", p.GiveAmount),
		argU("wantAmount", p.WantAmount), argS("wantAddr", p.WantAddr), argS("hash", p.Hash), argU("expiry", p.Expiry)}
	fee := c.FixedFee
	if p.Expiry <= estimableExpiry {
		if fee, err = c.estimate(ctx, c.Board, nil, args); err != nil {
			return "", 0, fmt.Errorf("Post %w: %v", ErrEstimate, err)
		}
	}
	txid, err := c.submit(ctx, c.Board, nil, args, fee)
	if err != nil {
		return "", 0, err
	}
	c.setPending(key, txid)
	if err := c.waitTx(ctx, txid); err != nil {
		return txid, 0, err
	}
	c.clearPending(key)
	o, ok, err := c.FindOffer(ctx, me, p.Hash)
	if err != nil {
		return txid, 0, err
	}
	if !ok {
		return txid, 0, fmt.Errorf("Post %s was mined but the board recorded no offer", txid)
	}
	return txid, o.ID, nil
}

// Accept binds the offer to this wallet, unless it already is.
func (c *DeroRPC) Accept(ctx context.Context, id uint64, refundPkh string, cltv uint64) (string, error) {
	me, err := c.myRaw(ctx)
	if err != nil {
		return "", err
	}
	if o, err := c.ReadOffer(ctx, id); err != nil {
		return "", err
	} else if o.State == OfferAccepted && o.Taker == me && o.RefundPkh == refundPkh && o.CLTV == cltv {
		return "", nil
	}
	args := []deroArg{argS("entrypoint", "Accept"), argU("id", id), argS("refundPkh", refundPkh), argU("cltv", cltv)}
	fee, err := c.estimate(ctx, c.Board, nil, args)
	if err != nil {
		return "", fmt.Errorf("Accept %w: %v", ErrEstimate, err)
	}
	return c.sendAndCheck(ctx, c.Board, args, fee, func() (bool, error) {
		o, err := c.ReadOffer(ctx, id)
		return err == nil && o.State == OfferAccepted && o.Taker == me && o.RefundPkh == refundPkh && o.CLTV == cltv, err
	})
}

// Cancel withdraws the maker's offer: gated on the deadline or the expiry for an accepted one, so fee-fixed.
func (c *DeroRPC) Cancel(ctx context.Context, id uint64) (string, error) {
	args := []deroArg{argS("entrypoint", "Cancel"), argU("id", id)}
	return c.sendAndCheck(ctx, c.Board, args, c.FixedFee, func() (bool, error) {
		o, err := c.ReadOffer(ctx, id)
		return err == nil && o.State == OfferCancelled, err
	})
}

// Lock escrows the asset for the taker, unless this maker already has a lock for this taker under the hash.
func (c *DeroRPC) Lock(ctx context.Context, hash, takerAddr string, expiry uint64, asset string, amount uint64) (string, uint64, error) {
	me, err := c.myRaw(ctx)
	if err != nil {
		return "", 0, err
	}
	takerRaw, err := DeroRaw(takerAddr)
	if err != nil {
		return "", 0, err
	}
	key := "lock|" + hash + "|" + takerRaw
	if err := c.guardPending(ctx, key); err != nil {
		return "", 0, err
	}
	if e, ok, err := c.FindEscrow(ctx, me, takerRaw, hash); err != nil {
		return "", 0, err
	} else if ok {
		return "", e.ID, nil
	}
	dest, err := c.randomAddress(ctx)
	if err != nil {
		return "", 0, err
	}
	xfer := deroTransfer{Destination: dest, Burn: amount}
	if asset != "" {
		xfer.SCID = asset
	}
	xfers := []deroTransfer{xfer}
	args := []deroArg{argS("entrypoint", "Lock"), argS("hash", hash), argS("taker", takerAddr), argU("expiry", expiry), argS("asset", asset)}
	fee := c.FixedFee
	if expiry <= estimableExpiry {
		if fee, err = c.estimate(ctx, c.Escrow, xfers, args); err != nil {
			return "", 0, fmt.Errorf("Lock %w: %v", ErrEstimate, err)
		}
	}
	txid, err := c.submit(ctx, c.Escrow, xfers, args, fee)
	if err != nil {
		return "", 0, err
	}
	c.setPending(key, txid)
	if err := c.waitTx(ctx, txid); err != nil {
		return txid, 0, err
	}
	c.clearPending(key)
	e, ok, err := c.FindEscrow(ctx, me, takerRaw, hash)
	if err != nil {
		return txid, 0, err
	}
	if !ok {
		return txid, 0, fmt.Errorf("Lock %s was mined but the contract recorded no lock", txid)
	}
	return txid, e.ID, nil
}

// Claim opens the escrow with the secret. swap.bas v2 pays the taker the lock names, whoever sends it, so the
// maker's companion sends it too once its own claim is deep. Estimated: every check it makes holds at height 0.
func (c *DeroRPC) Claim(ctx context.Context, id uint64, secret string) (string, error) {
	args := []deroArg{argS("entrypoint", "Claim"), argU("id", id), argS("secret", secret)}
	fee, err := c.estimate(ctx, c.Escrow, nil, args)
	if err != nil {
		return "", fmt.Errorf("Claim %w: %v", ErrEstimate, err)
	}
	return c.sendAndCheck(ctx, c.Escrow, args, fee, func() (bool, error) {
		e, err := c.ReadEscrow(ctx, id)
		return err == nil && e.State == EscrowClaimed, err
	})
}

// Refund returns the escrow to the maker after its expiry: height-gated, so fee-fixed.
func (c *DeroRPC) Refund(ctx context.Context, id uint64) (string, error) {
	args := []deroArg{argS("entrypoint", "Refund"), argU("id", id)}
	return c.sendAndCheck(ctx, c.Escrow, args, c.FixedFee, func() (bool, error) {
		e, err := c.ReadEscrow(ctx, id)
		return err == nil && e.State == EscrowRefunded, err
	})
}

func (c *DeroRPC) sendAndCheck(ctx context.Context, scid string, args []deroArg, fee uint64, done func() (bool, error)) (string, error) {
	txid, err := c.submit(ctx, scid, nil, args, fee)
	if err != nil {
		return "", err
	}
	if err := c.waitTx(ctx, txid); err != nil {
		return txid, err
	}
	ok, err := done()
	if err != nil {
		return txid, err
	}
	if !ok {
		return txid, fmt.Errorf("%v %s was mined but the contract refused it", args[0].Value, txid)
	}
	return txid, nil
}

// ---- DERO addresses ----
//
// The contracts store addresses raw (SIGNER(), ADDRESS_RAW): the 33-byte compressed key. A wallet names the same
// account as bech32 with the network's prefix ("dero" on mainnet, "deto" elsewhere) over one version byte and the
// key (derohe rpc/address.go). swap.bas Lock takes the taker as an address string and runs ADDRESS_RAW on it, which
// returns "" for anything that is not one, so the maker must turn the board's raw t_<id> back into the string form.

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

// DeroRaw returns the lower-case hex of the 33 raw bytes a DERO address names — the form the contracts store.
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

// DeroAddress is the plain (not integrated) address of a raw 33-byte key, on mainnet or elsewhere.
func DeroAddress(rawHex string, mainnet bool) (string, error) {
	b, err := hex.DecodeString(rawHex)
	if err != nil || len(b) != 33 {
		return "", fmt.Errorf("not a raw 33-byte DERO key: %q", rawHex)
	}
	hrp := "deto"
	if mainnet {
		hrp = "dero"
	}
	payload := make([]int, 0, 34)
	payload = append(payload, 1)
	for _, c := range b {
		payload = append(payload, int(c))
	}
	data := bech32ConvertBits(payload, 8, 5, true)
	values := append(append(bech32HRPExpand(hrp), data...), 0, 0, 0, 0, 0, 0)
	mod := bech32Polymod(values) ^ 1
	var sb strings.Builder
	sb.WriteString(hrp)
	sb.WriteByte('1')
	for _, d := range data {
		sb.WriteByte(bech32Charset[d])
	}
	for i := 0; i < 6; i++ {
		sb.WriteByte(bech32Charset[(mod>>uint(5*(5-i)))&31])
	}
	return sb.String(), nil
}
