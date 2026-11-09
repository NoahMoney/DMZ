// Package simrpc is a stdlib-only JSON-RPC client for driving a derohe daemon and its wallets the way the
// Veil app does: DERO.GetGasEstimate → wallet transfer → DERO.GetTransaction polling → DERO.GetSC reads.
//
// A wallet started with --rpc-login (netwallet -rpc-login, or VEIL_RPC_LOGIN; required on mainnet) answers 401 to
// any call without HTTP basic auth. WalletCall, and every wallet helper here (Address, Balance, WalletHeight, the
// transfer in Send and SendWithFee), sends the login in VEIL_RPC_LOGIN, the variable netwallet reads, so one
// user:password serves the wallet and the tool that drives it. It is sent to wallets only, never to the daemon, and
// never appears in an error or a log line.
package simrpc

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// LoginEnv holds the wallet RPC login, user:password: the same variable netwallet passes to the wallet as
// --rpc-login, so the tools and the wallet they drive read one value from one place.
const LoginEnv = "VEIL_RPC_LOGIN"

// ErrLoginRefused is a wallet's 401: the call carried no login, or not the one the wallet was started with.
var ErrLoginRefused = errors.New("the wallet refused the login (401)")

// walletLogin reads LoginEnv. set is false when it is unset or empty; a value without a colon is an error that does
// not repeat the value.
func walletLogin() (user, pass string, set bool, err error) {
	v := os.Getenv(LoginEnv)
	if v == "" {
		return "", "", false, nil
	}
	user, pass, ok := strings.Cut(v, ":")
	if !ok {
		return "", "", true, fmt.Errorf("%s must be user:password (its value is not shown)", LoginEnv)
	}
	return user, pass, true, nil
}

// RequireWalletLogin is for a network whose wallets must ask for a login (mainnet): it fails, before any call is
// made, when LoginEnv is unset or is not a non-empty user and password, and when one of the wallet addresses carries
// a login of its own (user:password@host), which would put the password on the command line. The error names the
// variable and never a value.
func RequireWalletLogin(wallets ...string) error {
	for _, w := range wallets {
		if AddressCarriesLogin(w) {
			return errors.New("a wallet address carries a login (user:password@host), which puts the password on the command line, readable by every local process: give the wallet as host:port; its login comes from " + LoginEnv)
		}
	}
	user, pass, set, err := walletLogin()
	switch {
	case err != nil:
		return err
	case !set:
		return errors.New("the wallets' RPC asks for a login and " + LoginEnv + " is not set: set it in this console's environment to the user:password the wallets were started with (netwallet reads the same variable)")
	case user == "" || pass == "":
		return errors.New(LoginEnv + " must be user:password with both parts non-empty (its value is not shown): a wallet started with an empty user checks no login at all")
	}
	return nil
}

// AddressCarriesLogin says whether a wallet address has the user:password@host form. Go's HTTP client sends such a
// login as basic auth, and a call to one still works, but the login is then on the command line.
func AddressCarriesLogin(host string) bool { return strings.Contains(host, "@") }

// RequireLoopbackWallets is the other half of the mainnet gate: every wallet address must name this machine. The login
// rides in each wallet request as HTTP basic auth over plain HTTP, so a wallet anywhere else is one whose login anyone
// on the way can read. The error names the host without any login it carries.
func RequireLoopbackWallets(wallets ...string) error {
	for _, w := range wallets {
		if !IsLoopback(w) {
			return fmt.Errorf("the wallet address %s is not on this machine: its login (%s) would travel to it as HTTP basic auth over plain HTTP, readable on every network in between; run the wallet here and give it as 127.0.0.1:<port>", redact(w), LoginEnv)
		}
	}
	return nil
}

// IsLoopback says whether a host:port address names this machine: localhost, or an IP in 127.0.0.0/8 or ::1. A name
// that only resolves to one is not taken on trust.
func IsLoopback(hostport string) bool {
	host := redact(hostport)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// rpcTransport carries every call this package makes, to a daemon or a wallet, and it never goes through a proxy. Go's
// default transport reads HTTP_PROXY from the environment and exempts only loopback, so a wallet on another host was
// reached through whatever proxy the environment named, with the login from VEIL_RPC_LOGIN in the request.
var rpcTransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return t
}()

// rpcClient is the client for one call: rpcTransport, with http.DefaultClient's timeout, which verifydeploy and
// testnetcheck set for every simrpc call they make.
func rpcClient() *http.Client {
	return &http.Client{Transport: rpcTransport, Timeout: http.DefaultClient.Timeout}
}

// redact drops the user:password@ of an address, for any message that names the host.
func redact(host string) string {
	if i := strings.LastIndex(host, "@"); i >= 0 {
		return host[i+1:]
	}
	return host
}

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

// Call posts a JSON-RPC request to host and decodes the result into out. It sends no login of its own: use it for
// the daemon, and WalletCall for a wallet.
func Call(host, method string, params interface{}, out interface{}) error {
	return call(host, method, params, out, false)
}

// WalletCall is Call for a wallet: it sends the login in VEIL_RPC_LOGIN as HTTP basic auth when the variable is set.
// A host written user:password@host keeps its own login, which Go's client sends instead.
func WalletCall(host, method string, params interface{}, out interface{}) error {
	return call(host, method, params, out, true)
}

func call(host, method string, params interface{}, out interface{}, wallet bool) error {
	body := map[string]interface{}{"jsonrpc": "2.0", "id": "1", "method": method}
	if params != nil {
		body["params"] = params
	}
	b, _ := json.Marshal(body)
	// url.Parse's error quotes the whole URL, login included, so it is not passed on
	req, err := http.NewRequest(http.MethodPost, "http://"+host+"/json_rpc", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("%s: %s is not a host:port address", method, redact(host))
	}
	req.Header.Set("Content-Type", "application/json")
	loginFrom := ""
	if req.URL.User != nil {
		loginFrom = "the wallet address" // the client sends it itself
	} else if wallet {
		user, pass, set, err := walletLogin()
		if err != nil {
			return fmt.Errorf("%s: %w", method, err)
		}
		if set {
			req.SetBasicAuth(user, pass)
			loginFrom = LoginEnv
		}
	}
	resp, err := rpcClient().Do(req) // no proxy, whatever the environment says; the default client's timeout
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the url.Error repeats the URL; the host is named once, without its login
		}
		return fmt.Errorf("%s: %s: %v", method, redact(host), err)
	}
	defer resp.Body.Close()
	// derod and the wallet answer a JSON-RPC error with 200 (jrpc2's jhttp bridge). A 401 is the wallet's
	// "Authorization Required" (walletapi/rpcserver/rpc_websocket_server.go); any other status is not a reply to
	// read, and its body is not repeated.
	switch {
	case resp.StatusCode == http.StatusUnauthorized && loginFrom == "":
		return fmt.Errorf("%s at %s: %w: no login was sent, and the wallet asks for one; set %s", method, redact(host), ErrLoginRefused, LoginEnv)
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("%s at %s: %w: the login in %s is not the one the wallet was started with", method, redact(host), ErrLoginRefused, loginFrom)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%s at %s: HTTP %d, not a JSON-RPC reply", method, redact(host), resp.StatusCode)
	}
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
	a, err := AddressE(wallet)
	Must(err)
	return a
}

// AddressE is Address with its error returned instead of the process ending. A signer uses it so a wallet that is
// briefly unreachable does not take the whole daemon down; the caller retries on its next pass.
func AddressE(wallet string) (string, error) {
	var r struct {
		Address string `json:"address"`
	}
	if err := WalletCall(wallet, "GetAddress", nil, &r); err != nil {
		return "", err
	}
	if r.Address == "" {
		return "", errors.New("GetAddress: the wallet returned an empty address")
	}
	return r.Address, nil
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
	Must(WalletCall(wallet, "GetBalance", params, &r))
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
	h, err := c.HeightE()
	Must(err)
	return h
}

// HeightE is Height with its error returned.
func (c *Client) HeightE() (int64, error) {
	var r struct {
		Height int64 `json:"height"`
	}
	if err := Call(c.Daemon, "DERO.GetHeight", nil, &r); err != nil {
		return 0, err
	}
	return r.Height, nil
}

// StableHeight is the daemon's own finality mark (DERO.GetHeight.stableheight): blocks at or below it are not
// reorganised by the node's rules. Anything that pays out on another chain waits for it.
func (c *Client) StableHeight() int64 {
	h, err := c.StableHeightE()
	Must(err)
	return h
}

// StableHeightE is StableHeight with its error returned.
func (c *Client) StableHeightE() (int64, error) {
	var r struct {
		Stable int64 `json:"stableheight"`
	}
	if err := Call(c.Daemon, "DERO.GetHeight", nil, &r); err != nil {
		return 0, err
	}
	return r.Stable, nil
}

func WalletHeight(wallet string) int64 {
	h, err := WalletHeightE(wallet)
	Must(err)
	return h
}

// WalletHeightE is WalletHeight with its error returned.
func WalletHeightE(wallet string) (int64, error) {
	var r struct {
		Height int64 `json:"height"`
	}
	if err := WalletCall(wallet, "GetHeight", nil, &r); err != nil {
		return 0, err
	}
	return r.Height, nil
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
	h, err := c.WaitTxE(txid, wallets...)
	Must(err)
	return h
}

// WaitTxE is WaitTx with its error returned. A caller that must keep running (the federation signer) uses this:
// a node that is briefly unreachable is a failed pass, not a reason to exit with a lock file and no signer.
func (c *Client) WaitTxE(txid string, wallets ...string) (int64, error) {
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
			if err != nil {
				return 0, fmt.Errorf("tx %s not confirmed within %v: %w", txid, c.Timeout, err)
			}
			return 0, fmt.Errorf("tx %s not confirmed within %v", txid, c.Timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
	for {
		h, err := c.HeightE()
		if err != nil {
			return 0, err
		}
		if h > height {
			break
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("no new block after tx %s within %v", txid, c.Timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
	for _, w := range wallets {
		for {
			wh, err := WalletHeightE(w)
			if err != nil {
				return 0, fmt.Errorf("wallet %s: %w", redact(w), err)
			}
			if wh >= height+1 {
				break
			}
			if time.Now().After(deadline) {
				return 0, fmt.Errorf("wallet %s did not sync to %d", redact(w), height+1)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	return height, nil
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
	Must(WalletCall(wallet, "transfer", params, &r))
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
	txid, height, err := c.SendWithFeeE(wallet, signer, scid, xfers, scRPC, fee, wait...)
	Must(err)
	return txid, height
}

// SendWithFeeE is SendWithFee with its error returned.
func (c *Client) SendWithFeeE(wallet, signer, scid string, xfers []Transfer, scRPC []Arg, fee uint64, wait ...string) (string, int64, error) {
	params := map[string]interface{}{"transfers": xfers, "sc_rpc": scRPC, "ringsize": c.ring(), "fees": fee, "scid": scid}
	var r struct {
		TXID string `json:"txid"`
	}
	if err := WalletCall(wallet, "transfer", params, &r); err != nil {
		return "", 0, err
	}
	if r.TXID == "" {
		return "", 0, errors.New("transfer: the wallet returned no txid")
	}
	height, err := c.WaitTxE(r.TXID, append([]string{wallet}, wait...)...)
	if err != nil {
		return r.TXID, 0, err
	}
	return r.TXID, height, nil
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
	st, err := c.GetSCE(scid, code)
	Must(err)
	return st
}

// GetSCE is GetSC with its error returned. The state is decoded with json.Number exactly as GetSC does, so a
// caller switching to this method cannot be handed a rounded uint64.
func (c *Client) GetSCE(scid string, code bool) (SCState, error) {
	body := map[string]interface{}{"jsonrpc": "2.0", "id": "1", "method": "DERO.GetSC", "params": map[string]interface{}{"scid": scid, "variables": true, "code": code}}
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, "http://"+c.Daemon+"/json_rpc", bytes.NewReader(b))
	if err != nil {
		return SCState{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := rpcClient().Do(req)
	if err != nil {
		return SCState{}, err
	}
	defer resp.Body.Close()
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcErr         `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return SCState{}, err
	}
	if env.Error != nil {
		return SCState{}, fmt.Errorf("DERO.GetSC: rpc error %d: %s", env.Error.Code, env.Error.Message)
	}
	var r SCState
	dec := json.NewDecoder(bytes.NewReader(env.Result))
	dec.UseNumber()
	if err := dec.Decode(&r); err != nil {
		return SCState{}, err
	}
	return r, nil
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
	testnet, network, err := c.InfoE()
	Must(err)
	return testnet, network
}

// InfoE is Info with its error returned.
func (c *Client) InfoE() (testnet bool, network string, err error) {
	var r struct {
		Testnet bool   `json:"testnet"`
		Network string `json:"network"`
	}
	if err := Call(c.Daemon, "DERO.GetInfo", nil, &r); err != nil {
		return false, "", err
	}
	return r.Testnet, r.Network, nil
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
