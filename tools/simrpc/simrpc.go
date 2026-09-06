// Package simrpc is a stdlib-only JSON-RPC client for driving a derohe daemon and its wallets the way the
// Veil app does: DERO.GetGasEstimate → wallet transfer → DERO.GetTransaction polling → DERO.GetSC reads.
package simrpc

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	Daemon   string
	Timeout  time.Duration
	RingSize int // 0 means 2; larger rings hide the payer among decoys and blank SIGNER() (research: tools/ringprobe)
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func Must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

// Call posts a JSON-RPC request to host (daemon or wallet) and decodes the result into out.
func Call(host, method string, params interface{}, out interface{}) error {
	body := map[string]interface{}{"jsonrpc": "2.0", "id": "1", "method": method}
	if params != nil {
		body["params"] = params
	}
	b, _ := json.Marshal(body)
	resp, err := http.Post("http://"+host+"/json_rpc", "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcErr         `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("%s: decode: %w", method, err)
	}
	if env.Error != nil {
		return fmt.Errorf("%s: rpc error %d: %s", method, env.Error.Code, env.Error.Message)
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

type Arg struct {
	Name     string      `json:"name"`
	DataType string      `json:"datatype"`
	Value    interface{} `json:"value"`
}

func U(n string, v uint64) Arg { return Arg{n, "U", v} }
func S(n string, v string) Arg { return Arg{n, "S", v} }
func H(n string, v string) Arg { return Arg{n, "H", v} }

type Transfer struct {
	SCID        string `json:"scid,omitempty"`
	Destination string `json:"destination,omitempty"`
	Amount      uint64 `json:"amount"`
	Burn        uint64 `json:"burn,omitempty"`
}

func Address(wallet string) string {
	var r struct {
		Address string `json:"address"`
	}
	Must(Call(wallet, "GetAddress", nil, &r))
	return r.Address
}

// Balance of DERO (scid == "") or of an asset in a wallet.
func Balance(wallet, scid string) uint64 {
	var r struct {
		Balance uint64 `json:"balance"`
	}
	params := map[string]interface{}{}
	if scid != "" {
		params["scid"] = scid
	}
	Must(Call(wallet, "GetBalance", params, &r))
	return r.Balance
}

func (c *Client) RandomAddress() string {
	var r struct {
		Address []string `json:"address"`
	}
	Must(Call(c.Daemon, "DERO.GetRandomAddress", map[string]interface{}{}, &r))
	if len(r.Address) == 0 {
		log.Fatal("no random address")
	}
	return r.Address[0]
}

func (c *Client) Height() int64 {
	var r struct {
		Height int64 `json:"height"`
	}
	Must(Call(c.Daemon, "DERO.GetHeight", nil, &r))
	return r.Height
}

// StableHeight is the daemon's own finality mark (DERO.GetHeight.stableheight): blocks at or below it are not
// reorganised by the node's rules. Anything that pays out on another chain waits for it.
func (c *Client) StableHeight() int64 {
	var r struct {
		Stable int64 `json:"stableheight"`
	}
	Must(Call(c.Daemon, "DERO.GetHeight", nil, &r))
	return r.Stable
}

func WalletHeight(wallet string) int64 {
	var r struct {
		Height int64 `json:"height"`
	}
	Must(Call(wallet, "GetHeight", nil, &r))
	return r.Height
}

// WaitBlocks blocks until n more blocks exist.
func (c *Client) WaitBlocks(n int64) {
	target := c.Height() + n
	deadline := time.Now().Add(c.Timeout * time.Duration(n+1))
	for c.Height() < target {
		if time.Now().After(deadline) {
			log.Fatalf("no %d new blocks within %v", n, c.Timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// WaitTx blocks until txid is in a valid block, one more block exists, and the wallets have synced past it.
func (c *Client) WaitTx(txid string, wallets ...string) int64 {
	deadline := time.Now().Add(c.Timeout)
	var height int64
	for {
		var r struct {
			Txs []struct {
				ValidBlock  string `json:"valid_block"`
				BlockHeight int64  `json:"block_height"`
				InPool      bool   `json:"in_pool"`
			} `json:"txs"`
		}
		err := Call(c.Daemon, "DERO.GetTransaction", map[string]interface{}{"txs_hashes": []string{txid}}, &r)
		if err == nil && len(r.Txs) == 1 && r.Txs[0].ValidBlock != "" && !r.Txs[0].InPool {
			height = r.Txs[0].BlockHeight
			break
		}
		if time.Now().After(deadline) {
			log.Fatalf("tx %s not confirmed within %v", txid, c.Timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
	for c.Height() <= height {
		if time.Now().After(deadline) {
			log.Fatal("no new block after tx")
		}
		time.Sleep(500 * time.Millisecond)
	}
	for _, w := range wallets {
		for WalletHeight(w) < height+1 {
			if time.Now().After(deadline) {
				log.Fatalf("wallet %s did not sync to %d", w, height+1)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	return height
}

type Gas struct {
	GasCompute uint64 `json:"gascompute"`
	GasStorage uint64 `json:"gasstorage"`
}

// Estimate mirrors what the wallet will submit: the daemon estimator needs SC_ACTION and SC_ID / SC_CODE added.
func (c *Client) Estimate(signer, code, scid string, xfers []Transfer, scRPC []Arg) (Gas, error) {
	estRPC := append([]Arg{}, scRPC...)
	if code != "" {
		estRPC = append(estRPC, U("SC_ACTION", 1), S("SC_CODE", code))
	} else {
		estRPC = append(estRPC, U("SC_ACTION", 0), H("SC_ID", scid))
	}
	est := map[string]interface{}{"transfers": xfers, "sc_rpc": estRPC, "signer": signer, "ringsize": c.ring()}
	if code != "" {
		est["sc"] = code
	}
	if scid != "" {
		est["scid"] = scid
	}
	var g Gas
	err := Call(c.Daemon, "DERO.GetGasEstimate", est, &g)
	return g, err
}

// Send builds an install (code != "") or call (scid != "") transfer, estimates gas, submits, waits, and for installs
// verifies the code landed. Returns txid, gas and the block height.
func (c *Client) Send(wallet, signer, code, scid string, xfers []Transfer, scRPC []Arg, wait ...string) (string, Gas, int64) {
	gas, err := c.Estimate(signer, code, scid, xfers, scRPC)
	if err != nil && code != "" && strings.Contains(err.Error(), "Bucket cannot start") {
		// R-16: the daemon simulates an install under an id derived from the code, and graviton refuses an id that
		// starts with ':' — for such code the estimate never works. Storage gas for an install is the code size
		// plus a few hundred units (vault.bas: 14,010 B → 14,276), so fee it by size.
		gas, err = Gas{GasStorage: uint64(len(code)) + 512}, nil
		fmt.Printf("estimate unavailable for this code (daemon bucket panic, R-16): sending the install with a size-based fee of %d\n", gas.GasStorage)
	}
	Must(err)
	params := map[string]interface{}{"transfers": xfers, "sc_rpc": scRPC, "ringsize": c.ring(), "fees": gas.GasStorage + 200}
	if code != "" {
		params["sc"] = code
	}
	if scid != "" {
		params["scid"] = scid
	}
	var r struct {
		TXID string `json:"txid"`
	}
	Must(Call(wallet, "transfer", params, &r))
	height := c.WaitTx(r.TXID, append([]string{wallet}, wait...)...)
	if code != "" {
		if st := c.GetSC(r.TXID, true); st.Code != code {
			log.Fatalf("install %s confirmed but the contract did not execute (fee %d)", r.TXID, gas.GasStorage)
		}
	}
	return r.TXID, gas, height
}

// SendWithFee submits a call without estimating first. Needed for entrypoints gated on BLOCK_HEIGHT(): the daemon
// estimator runs the contract with height 0 (dvm.Simulator.height is never set), so such calls "fail" at estimate
// time while the real TX succeeds.
func (c *Client) SendWithFee(wallet, signer, scid string, xfers []Transfer, scRPC []Arg, fee uint64, wait ...string) (string, int64) {
	params := map[string]interface{}{"transfers": xfers, "sc_rpc": scRPC, "ringsize": c.ring(), "fees": fee, "scid": scid}
	var r struct {
		TXID string `json:"txid"`
	}
	Must(Call(wallet, "transfer", params, &r))
	height := c.WaitTx(r.TXID, append([]string{wallet}, wait...)...)
	return r.TXID, height
}

type SCState struct {
	StringKeys map[string]interface{} `json:"stringkeys"`
	Balances   map[string]uint64      `json:"balances"`
	Balance    uint64                 `json:"balance"`
	Code       string                 `json:"code"`
}

// GetSC decodes the variables with json.Number so a uint64 above 2^53 (a Monero amount in piconero, a large
// supply) is never rounded through float64 on its way to a payment or a comparison.
func (c *Client) GetSC(scid string, code bool) SCState {
	body := map[string]interface{}{"jsonrpc": "2.0", "id": "1", "method": "DERO.GetSC", "params": map[string]interface{}{"scid": scid, "variables": true, "code": code}}
	b, _ := json.Marshal(body)
	resp, err := http.Post("http://"+c.Daemon+"/json_rpc", "application/json", bytes.NewReader(b))
	Must(err)
	defer resp.Body.Close()
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcErr         `json:"error"`
	}
	Must(json.NewDecoder(resp.Body).Decode(&env))
	if env.Error != nil {
		log.Fatalf("DERO.GetSC: rpc error %d: %s", env.Error.Code, env.Error.Message)
	}
	var r SCState
	dec := json.NewDecoder(bytes.NewReader(env.Result))
	dec.UseNumber()
	Must(dec.Decode(&r))
	return r
}

func (s SCState) Has(k string) bool { _, ok := s.StringKeys[k]; return ok }

// U reads a uint64 variable exactly; the process stops on a missing key or a value that is not an integer.
func (s SCState) U(k string) uint64 {
	v, ok := s.StringKeys[k]
	if !ok {
		log.Fatalf("key %s missing", k)
	}
	switch x := v.(type) {
	case json.Number:
		n, err := strconv.ParseUint(x.String(), 10, 64)
		if err != nil {
			log.Fatalf("key %s: not a uint64: %s", k, x.String())
		}
		return n
	case float64:
		if x != float64(uint64(x)) || x > 9007199254740992 {
			log.Fatalf("key %s: value %v cannot be represented exactly", k, x)
		}
		return uint64(x)
	case string:
		n, err := strconv.ParseUint(x, 10, 64)
		Must(err)
		return n
	}
	log.Fatalf("key %s: unexpected %T", k, v)
	return 0
}

// U0 is U for a key that may be absent (0 when missing).
func (s SCState) U0(k string) uint64 {
	if !s.Has(k) {
		return 0
	}
	return s.U(k)
}

// Info reports what the daemon says about its network.
func (c *Client) Info() (testnet bool, network string) {
	var r struct {
		Testnet bool   `json:"testnet"`
		Network string `json:"network"`
	}
	Must(Call(c.Daemon, "DERO.GetInfo", nil, &r))
	return r.Testnet, r.Network
}

// Str decodes a hex-encoded string variable.
func (s SCState) Str(k string) string {
	v, ok := s.StringKeys[k].(string)
	if !ok {
		log.Fatalf("key %s missing or not a string", k)
	}
	b, err := hex.DecodeString(v)
	Must(err)
	return string(b)
}

// ring is the ring size used for every transfer this client submits (2 unless set).
func (c *Client) ring() int {
	if c.RingSize < 2 {
		return 2
	}
	return c.RingSize
}
