package xmrswap

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Wallet is one monero-wallet-rpc: the user's own, on their own machine. The companion opens it for the length
// of one swap. Every method fails rather than guesses, and none of them logs.
//
// Request and response shapes are monero src/wallet/wallet_rpc_server_commands_defs.h (the KV_SERIALIZE maps)
// and the published reference https://docs.getmonero.org/rpc-library/wallet-rpc/ ; the server behaviour they
// rely on is cited at each method.
type Wallet interface {
	Height(ctx context.Context) (uint64, error)
	// Address is the primary address of the currently open wallet.
	Address(ctx context.Context) (string, error)
	// Transfer pays amount piconero to one address from the open wallet and returns the transaction id.
	Transfer(ctx context.Context, to string, amount uint64) (txid string, err error)
	// GenerateFromKeys creates a wallet file from an address and its private view key — view-only when spendKey
	// is "" — scanning from restoreHeight, encrypted under password, and leaves it open. The spend key is the joint
	// key s_a + s_b at sweep time; this request is the one place it leaves the companion. monero-wallet-rpc does not
	// check the keys against the address (0.18.5.1, research/regtest/monero-smoke.log), so the client does, first.
	GenerateFromKeys(ctx context.Context, name, address, viewKey, spendKey, password string, restoreHeight uint64) error
	OpenWallet(ctx context.Context, name, password string) error
	CloseWallet(ctx context.Context) error
	// Refresh scans the open wallet from startHeight and returns when it has (the `refresh` RPC, on_refresh in
	// wallet_rpc_server.cpp). Nothing else in this interface makes the server scan on demand:
	// on_generate_from_keys ends by setting the restore height and installing the wallet, and never calls
	// refresh, so a wallet made from keys knows nothing about the chain until this is called or the server's
	// own idle loop reaches it. A balance read before it is a balance of zero, whatever is at the address.
	Refresh(ctx context.Context, startHeight uint64) error
	// Outgoing lists the txids of every transfer out of the open wallet, mined or still in the pool. A view-only
	// wallet cannot see its own spends, so it answers only for a wallet that holds its spend key.
	Outgoing(ctx context.Context) ([]string, error)
	// Incoming lists confirmed incoming transfers to the open wallet at height >= minHeight
	// (get_transfers{in:true, filter_by_height:true}).
	Incoming(ctx context.Context, minHeight uint64) ([]Incoming, error)
	// UnlockedBalance is the open wallet's spendable balance in piconero (get_balance.unlocked_balance): what a
	// sweep could move now. An output under an unlock_time is not in it, and neither is one the wallet found to
	// be a duplicate one-time key (the 2018 "burning bug": the wallet ignores the unspendable twin), which is
	// why Bob's gate reads this figure and not only the transfer list (Invariant 8).
	UnlockedBalance(ctx context.Context) (uint64, error)
	// SweepAll sends the open wallet's whole unlocked balance to one address and returns the transaction ids.
	SweepAll(ctx context.Context, to string) (txids []string, err error)
}

// Incoming is one confirmed incoming transfer. Confirmations is the wallet's count (chain height minus the
// block height); Unlocked is the wallet's own verdict on whether the output can be spent yet — false until
// CRYPTONOTE_DEFAULT_TX_SPENDABLE_AGE (10) blocks, which is also the count Bob waits for before the gate, and
// false for as long as the transaction's own unlock_time holds. UnlockTime is that field as the sender set it
// (a height below 500,000,000, else a unix time; 0 for none): a transfer whose UnlockTime is not 0 is one the
// sender chose to freeze, and Bob's gate never counts it (Invariant 8, review R6).
type Incoming struct {
	TxID          string
	Amount        uint64
	Height        uint64
	Confirmations uint64
	Unlocked      bool
	UnlockTime    uint64
}

// CodeNotSigned is the client's own code, outside monero-wallet-rpc's range, for a transfer or sweep the wallet
// answered with a transaction set to sign elsewhere (unsigned_txset or multisig_txset) instead of one it signed and
// sent. monero-wallet-rpc 0.18.5.1 answers a view-only wallet's transfer and sweep_all that way, with a tx_hash that
// was never broadcast, rather than refusing (research/regtest/monero-smoke.log): nothing was sent.
const CodeNotSigned = -9001

// RPCError is monero-wallet-rpc's own refusal, with its code (src/wallet/wallet_rpc_server_error_codes.h:
// -13 no wallet open, -17 not enough money, -29 watch-only, -37 not enough unlocked money, ...), or the client's
// CodeNotSigned.
type RPCError struct {
	Method  string
	Code    int
	Message string
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("monero-wallet-rpc %s: %d %s", e.Method, e.Code, e.Message)
}

// NewWalletRPC is a client for the monero-wallet-rpc at url (scheme and host, or host:port; "/json_rpc" is
// appended). user and pass are the --rpc-login credentials, sent as HTTP digest auth when the server asks
// for it; leave both empty for a server started without --rpc-login.
func NewWalletRPC(endpoint, user, pass string) Wallet {
	if !strings.Contains(endpoint, "://") {
		endpoint = "http://" + endpoint
	}
	endpoint = strings.TrimRight(endpoint, "/")
	if !strings.HasSuffix(endpoint, "/json_rpc") {
		endpoint += "/json_rpc"
	}
	uri := "/json_rpc" // the digest uri= field: the request path as sent
	if u, err := url.Parse(endpoint); err == nil {
		uri = u.RequestURI()
	}
	// A transfer can take the wallet a while (scanning, then building the ring signatures); the caller's ctx is
	// the real deadline and this is the backstop against a wallet that never answers at all.
	return &walletRPC{url: endpoint, uri: uri, user: user, pass: pass, http: &http.Client{Timeout: 10 * time.Minute}}
}

type walletRPC struct {
	url  string
	uri  string
	user string
	pass string
	http *http.Client

	mu   sync.Mutex
	chal *digestChallenge // the server's last challenge; nil until it has asked for auth
	nc   uint32           // requests answered under chal, the nc= field
	// challenged is whether the server has ever answered with a digest challenge: a wallet-rpc started with
	// --rpc-login does so on the first call, one started with --disable-rpc-login never does (GAP-009)
	challenged bool
}

// WalletAskedForCredentials says whether the monero-wallet-rpc behind w has ever demanded --rpc-login digest
// credentials. On mainnet the command line refuses a wallet-rpc that answered its first call without asking: that
// wallet holds the joint spend key at sweep time and can transfer and sweep, and any local process — possibly a web
// page — could drive it (GAP-009). A Wallet that is not this client is reported as not having asked.
func WalletAskedForCredentials(w Wallet) bool {
	c, ok := w.(*walletRPC)
	if !ok {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.challenged
}

// DaemonSetter is a wallet-rpc that can be told which monerod to use (set_daemon).
type DaemonSetter interface {
	SetDaemon(ctx context.Context, address string) error
}

// SetDaemon points the wallet-rpc at one monerod (set_daemon, wallet_rpc_server.cpp on_set_daemon), untrusted. The
// companion cannot ask a wallet-rpc which monerod it uses, and a monerod that lies shows Bob's view wallet a lock
// that is not there (GAP-010); so on mainnet it names the one it trusts — its own, on loopback — instead of taking
// whatever --daemon-address the wallet-rpc was started with.
func (w *walletRPC) SetDaemon(ctx context.Context, address string) error {
	return w.call(ctx, "set_daemon", map[string]interface{}{"address": address, "trusted": false}, nil)
}

// call posts one JSON-RPC 2.0 request and decodes its result. A JSON-RPC error becomes an *RPCError; a
// transport or HTTP failure is a plain error. Neither carries the request body, which for generate_from_keys
// holds the spend key.
func (w *walletRPC) call(ctx context.Context, method string, params, out interface{}) error {
	req := map[string]interface{}{"jsonrpc": "2.0", "id": "0", "method": method}
	if params != nil {
		req["params"] = params
	}
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("monero-wallet-rpc %s: encode: %w", method, err)
	}
	resp, err := w.post(ctx, body)
	if err != nil {
		return fmt.Errorf("monero-wallet-rpc %s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("monero-wallet-rpc %s: http %d", method, resp.StatusCode)
	}
	// the whole reply, bounded; one JSON value and nothing after it (json.Unmarshal refuses trailing data, where a
	// streaming decoder would stop at the first value and ignore the rest)
	raw, err := readCapped(resp.Body, maxWalletReply)
	if err != nil {
		return fmt.Errorf("monero-wallet-rpc %s: %w", method, err)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("monero-wallet-rpc %s: decode: %w", method, err)
	}
	if env.Error != nil {
		return &RPCError{Method: method, Code: env.Error.Code, Message: env.Error.Message}
	}
	if out == nil {
		return nil
	}
	if len(env.Result) == 0 || string(env.Result) == "null" {
		return fmt.Errorf("monero-wallet-rpc %s: the reply carries neither a result nor an error", method)
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("monero-wallet-rpc %s: result: %w", method, err)
	}
	return nil
}

// maxWalletReply bounds one monero-wallet-rpc answer. The largest the companion asks for is get_transfers on the
// personal wallet, whose history is the operator's; 64 MB covers any wallet a person runs and stops a server
// that streams without end. The client's own timeout (NewWalletRPC) bounds how long it may take.
const maxWalletReply = 64 << 20

// post sends the body, answering a 401 with digest credentials once. The server's challenge is kept, so a
// later request carries credentials up front and the round trip happens only on the first call and after a
// stale nonce.
func (w *walletRPC) post(ctx context.Context, body []byte) (*http.Response, error) {
	resp, err := w.send(ctx, body, w.authorization())
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	chal, ok := parseDigestChallenge(resp.Header.Values("WWW-Authenticate"))
	resp.Body.Close()
	if !ok {
		return nil, errors.New("401 without a Digest challenge")
	}
	if w.user == "" {
		return nil, errors.New("the wallet requires --rpc-login credentials and none were given")
	}
	w.mu.Lock()
	w.chal, w.nc, w.challenged = chal, 0, true
	w.mu.Unlock()
	resp, err = w.send(ctx, body, w.authorization())
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return nil, errors.New("authentication refused: wrong --rpc-login user or password")
	}
	return resp, nil
}

func (w *walletRPC) send(ctx context.Context, body []byte, auth string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	return w.http.Do(req)
}

// authorization is the Authorization header for the next request under the current challenge, or "".
func (w *walletRPC) authorization() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.chal == nil || w.user == "" {
		return ""
	}
	w.nc++
	var cn [8]byte
	if _, err := io.ReadFull(rand.Reader, cn[:]); err != nil {
		return ""
	}
	nc, cnonce := fmt.Sprintf("%08x", w.nc), hex.EncodeToString(cn[:])
	resp := digestResponse(w.user, w.pass, http.MethodPost, w.uri, w.chal, nc, cnonce)
	var b strings.Builder
	fmt.Fprintf(&b, `Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s", algorithm=%s`,
		w.user, w.chal.realm, w.chal.nonce, w.uri, resp, w.chal.algorithm)
	if w.chal.qop != "" {
		fmt.Fprintf(&b, `, qop=%s, nc=%s, cnonce="%s"`, w.chal.qop, nc, cnonce)
	}
	if w.chal.opaque != "" {
		fmt.Fprintf(&b, `, opaque="%s"`, w.chal.opaque)
	}
	return b.String()
}

// --- HTTP digest authentication (RFC 7616 §3.4; the MD5 profile of RFC 2617 §3.2.2) ---
//
// monero-wallet-rpc's server side is epee (contrib/epee/src/http_auth.cpp): realm "monero-rpc", one
// WWW-Authenticate per algorithm (MD5 and MD5-sess), qop="auth", and it also accepts a response computed
// without qop. The client sends MD5 with qop=auth, which is what `curl --digest` sends.

type digestChallenge struct {
	realm, nonce, qop, algorithm, opaque string
}

func parseDigestChallenge(values []string) (*digestChallenge, bool) {
	var fallback *digestChallenge
	for _, v := range values {
		if len(v) < 7 || !strings.EqualFold(v[:7], "Digest ") {
			continue
		}
		p := parseAuthParams(v[7:])
		c := &digestChallenge{realm: p["realm"], nonce: p["nonce"], algorithm: p["algorithm"], opaque: p["opaque"]}
		if c.algorithm == "" {
			c.algorithm = "MD5"
		}
		for _, q := range strings.Split(p["qop"], ",") {
			if strings.TrimSpace(q) == "auth" {
				c.qop = "auth"
			}
		}
		if strings.EqualFold(c.algorithm, "MD5") {
			return c, true
		}
		if fallback == nil && strings.EqualFold(c.algorithm, "MD5-sess") {
			fallback = c
		}
	}
	return fallback, fallback != nil
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// digestResponse is the response= value: MD5(HA1:nonce:nc:cnonce:qop:HA2) with qop, MD5(HA1:nonce:HA2) without;
// HA1 = MD5(user:realm:pass), folded with nonce and cnonce for MD5-sess; HA2 = MD5(method:uri).
func digestResponse(user, pass, method, uri string, c *digestChallenge, nc, cnonce string) string {
	ha1 := md5hex(user + ":" + c.realm + ":" + pass)
	if strings.EqualFold(c.algorithm, "MD5-sess") {
		ha1 = md5hex(ha1 + ":" + c.nonce + ":" + cnonce)
	}
	ha2 := md5hex(method + ":" + uri)
	if c.qop == "" {
		return md5hex(ha1 + ":" + c.nonce + ":" + ha2)
	}
	return md5hex(ha1 + ":" + c.nonce + ":" + nc + ":" + cnonce + ":" + c.qop + ":" + ha2)
}

// parseAuthParams reads the comma-separated key=value list of a Digest header, quoted or bare values.
func parseAuthParams(s string) map[string]string {
	out := map[string]string{}
	for len(s) > 0 {
		s = strings.TrimLeft(s, " ,")
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			break
		}
		key := strings.ToLower(strings.TrimSpace(s[:eq]))
		s = s[eq+1:]
		var val string
		if strings.HasPrefix(s, `"`) {
			end := strings.IndexByte(s[1:], '"')
			if end < 0 {
				val, s = s[1:], ""
			} else {
				val, s = s[1:1+end], s[2+end:]
			}
		} else {
			end := strings.IndexByte(s, ',')
			if end < 0 {
				val, s = strings.TrimSpace(s), ""
			} else {
				val, s = strings.TrimSpace(s[:end]), s[end:]
			}
		}
		out[key] = val
	}
	return out
}

// --- the methods ---

// Height is get_height: the OPEN WALLET's scanned height, not the chain's — 1 for a wallet made from keys until its
// first refresh, and -13 with no wallet open (0.18.5.1, research/regtest/monero-smoke.log). After a refresh it is the
// height of the chain its daemon reports. The companion reads it only for the restore height of the joint wallets,
// right after refreshing its own wallet, where a low reading costs a longer scan and never a missed payment;
// confirmations come from get_transfers, counted by the wallet against its refreshed chain.
func (w *walletRPC) Height(ctx context.Context) (uint64, error) {
	var r struct {
		Height uint64 `json:"height"`
	}
	if err := w.call(ctx, "get_height", nil, &r); err != nil {
		return 0, err
	}
	return r.Height, nil
}

func (w *walletRPC) Address(ctx context.Context) (string, error) {
	var r struct {
		Address string `json:"address"`
	}
	if err := w.call(ctx, "get_address", map[string]interface{}{"account_index": 0}, &r); err != nil {
		return "", err
	}
	if r.Address == "" {
		return "", errors.New("monero-wallet-rpc get_address: empty address")
	}
	return r.Address, nil
}

func (w *walletRPC) Transfer(ctx context.Context, to string, amount uint64) (string, error) {
	params := map[string]interface{}{
		"destinations":  []map[string]interface{}{{"amount": amount, "address": to}},
		"account_index": 0,
	}
	var r struct {
		TxHash        string `json:"tx_hash"`
		UnsignedTxset string `json:"unsigned_txset"`
		MultisigTxset string `json:"multisig_txset"`
	}
	if err := w.call(ctx, "transfer", params, &r); err != nil {
		return "", err
	}
	if r.UnsignedTxset != "" || r.MultisigTxset != "" {
		return "", &RPCError{Method: "transfer", Code: CodeNotSigned, Message: "the wallet answered with a transaction to sign elsewhere, not one it sent (a view-only or multisig wallet): nothing was broadcast"}
	}
	if r.TxHash == "" {
		return "", errors.New("monero-wallet-rpc transfer: no tx_hash in the result")
	}
	if err := w.confirmSent(ctx, "transfer", []string{r.TxHash}); err != nil {
		return r.TxHash, err
	}
	return r.TxHash, nil
}

// Outgoing is get_transfers out and pending: the open wallet's own record of what it has sent, mined or in the pool.
func (w *walletRPC) Outgoing(ctx context.Context) ([]string, error) {
	var r struct {
		Out []struct {
			TxID string `json:"txid"`
		} `json:"out"`
		Pending []struct {
			TxID string `json:"txid"`
		} `json:"pending"`
	}
	if err := w.call(ctx, "get_transfers", map[string]interface{}{"out": true, "pending": true, "account_index": 0}, &r); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(r.Out)+len(r.Pending))
	for _, e := range r.Out {
		ids = append(ids, e.TxID)
	}
	for _, e := range r.Pending {
		ids = append(ids, e.TxID)
	}
	return ids, nil
}

// confirmSent asks the wallet for its own record of what it says it sent — an outgoing transfer, pending or mined
// (get_transfers out and pending) — before a hash in a reply is taken as a send. A hash alone proves nothing:
// 0.18.5.1 hands one back for a view-only wallet's transfer it never broadcast. A hash the wallet does not list is an
// error that is not a refusal, so the caller treats the payment's fate as unknown and never pays twice.
func (w *walletRPC) confirmSent(ctx context.Context, method string, txids []string) error {
	ids, err := w.Outgoing(ctx)
	if err != nil {
		return fmt.Errorf("monero-wallet-rpc %s: reading the wallet's record of it: %w", method, err)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	for _, id := range txids {
		if !seen[id] {
			return fmt.Errorf("monero-wallet-rpc %s: the wallet answered %s and lists no outgoing transfer with that id: it may not have been sent", method, id)
		}
	}
	return nil
}

// GenerateFromKeys: the wallet file the wallet-rpc writes into its --wallet-dir is encrypted under password — for
// the joint wallets, a random password of this swap's, kept in the state file (GAP-020) — so the file alone does
// not give up the keys it holds. The companion closes the spend wallet when the sweep is over (swap.go closeJoint)
// and deletes both joint wallets' files when it knows the wallet-dir (-monero-wallet-dir).
//
// The server does NOT check the keys against the address. monero-wallet-rpc 0.18.5.1 accepts a random view key, or
// the right view key with a random spend key, and echoes the address it was given (research/regtest/monero-smoke.log;
// on_generate_from_keys calls wallet2::generate with no comparison). A wallet with the wrong view key then sees
// nothing, and one with the wrong spend key fails its refresh with -1 "Failed to generate key image". So the check is
// made here, before the request: v·G must be the address's view key and, when a spend key is given, s·G its spend
// key. The echo is compared too, and proves only that the server kept the address it was sent.
func (w *walletRPC) GenerateFromKeys(ctx context.Context, name, address, viewKey, spendKey, password string, restoreHeight uint64) error {
	pa, err := ParseAddress(address)
	if err != nil {
		return fmt.Errorf("monero-wallet-rpc generate_from_keys: %w", err)
	}
	view, err := ParseScalar(viewKey)
	if err != nil {
		return fmt.Errorf("monero-wallet-rpc generate_from_keys: view key: %w", err)
	}
	if view.Public() != pa.View {
		return fmt.Errorf("monero-wallet-rpc generate_from_keys: the view key does not derive the view key of %s: refused here, because the server would accept it and make a wallet that sees nothing", address)
	}
	if spendKey != "" {
		spend, err := ParseScalar(spendKey)
		if err != nil {
			return fmt.Errorf("monero-wallet-rpc generate_from_keys: spend key: %w", err)
		}
		if spend.Public() != pa.Spend {
			return fmt.Errorf("monero-wallet-rpc generate_from_keys: the spend key does not derive the spend key of %s: refused here, because the server would accept it and make a wallet that cannot spend", address)
		}
	}
	params := map[string]interface{}{
		"restore_height":   restoreHeight,
		"filename":         name,
		"address":          address,
		"viewkey":          viewKey,
		"password":         password,
		"autosave_current": true,
	}
	if spendKey != "" {
		params["spendkey"] = spendKey
	}
	var r struct {
		Address string `json:"address"`
		Info    string `json:"info"`
	}
	if err := w.call(ctx, "generate_from_keys", params, &r); err != nil {
		// MT-L02: the request carried the keys and the password; an error that echoes it must not print them
		return withhold(err, [2]string{"view key", viewKey}, [2]string{"spend key", spendKey}, [2]string{"wallet password", password})
	}
	if r.Address != address {
		return fmt.Errorf("monero-wallet-rpc generate_from_keys: the wallet reports address %q, expected %q", r.Address, address)
	}
	return nil
}

func (w *walletRPC) OpenWallet(ctx context.Context, name, password string) error {
	return w.call(ctx, "open_wallet", map[string]interface{}{"filename": name, "password": password}, nil)
}

func (w *walletRPC) CloseWallet(ctx context.Context) error {
	return w.call(ctx, "close_wallet", map[string]interface{}{"autosave_current": true}, nil)
}

// Refresh is on_refresh (wallet_rpc_server.cpp): it runs wallet2::refresh in the foreground and answers with
// the blocks it fetched, so when it returns the wallet has scanned. The companion calls it after every
// generate_from_keys and before every balance it acts on.
func (w *walletRPC) Refresh(ctx context.Context, startHeight uint64) error {
	return w.call(ctx, "refresh", map[string]interface{}{"start_height": startHeight}, nil)
}

// Incoming: wallet2::get_payments keeps a payment when `min_height < block_height` (src/wallet/wallet2.cpp),
// so the request's min_height is exclusive and the one sent is minHeight-1. minHeight 0 sends 0, which drops
// only block 0 — the genesis block, which pays nobody's wallet.
func (w *walletRPC) Incoming(ctx context.Context, minHeight uint64) ([]Incoming, error) {
	var exclusive uint64
	if minHeight > 0 {
		exclusive = minHeight - 1
	}
	params := map[string]interface{}{
		"in":               true,
		"filter_by_height": true,
		"min_height":       exclusive,
		"account_index":    0,
	}
	var r struct {
		In []struct {
			TxID          string `json:"txid"`
			Amount        uint64 `json:"amount"`
			Height        uint64 `json:"height"`
			Confirmations uint64 `json:"confirmations"`
			Locked        bool   `json:"locked"`
			UnlockTime    uint64 `json:"unlock_time"`
		} `json:"in"`
	}
	if err := w.call(ctx, "get_transfers", params, &r); err != nil {
		return nil, err
	}
	out := make([]Incoming, 0, len(r.In))
	for _, e := range r.In {
		if e.Height < minHeight {
			continue // never expected; the wallet's filter is the one above
		}
		out = append(out, Incoming{TxID: e.TxID, Amount: e.Amount, Height: e.Height, Confirmations: e.Confirmations, Unlocked: !e.Locked, UnlockTime: e.UnlockTime})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Height != out[j].Height {
			return out[i].Height < out[j].Height
		}
		return out[i].TxID < out[j].TxID
	})
	return out, nil
}

// UnlockedBalance: get_balance's unlocked_balance is wallet2::unlocked_balance, which counts an output only once
// is_transfer_unlocked says so — past the spendable age and past its unlock_time — and never counts an output
// the scanner ignored as a duplicate one-time key.
func (w *walletRPC) UnlockedBalance(ctx context.Context) (uint64, error) {
	var r struct {
		Unlocked uint64 `json:"unlocked_balance"`
	}
	if err := w.call(ctx, "get_balance", map[string]interface{}{"account_index": 0}, &r); err != nil {
		return 0, err
	}
	return r.Unlocked, nil
}

func (w *walletRPC) SweepAll(ctx context.Context, to string) ([]string, error) {
	params := map[string]interface{}{"address": to, "account_index": 0}
	var r struct {
		TxHashList    []string `json:"tx_hash_list"`
		UnsignedTxset string   `json:"unsigned_txset"`
		MultisigTxset string   `json:"multisig_txset"`
	}
	if err := w.call(ctx, "sweep_all", params, &r); err != nil {
		return nil, err
	}
	if r.UnsignedTxset != "" || r.MultisigTxset != "" {
		return nil, &RPCError{Method: "sweep_all", Code: CodeNotSigned, Message: "the wallet answered with a transaction to sign elsewhere, not one it sent (a view-only or multisig wallet): nothing was broadcast"}
	}
	if len(r.TxHashList) == 0 {
		return nil, errors.New("monero-wallet-rpc sweep_all: no tx_hash_list in the result")
	}
	if err := w.confirmSent(ctx, "sweep_all", r.TxHashList); err != nil {
		return r.TxHashList, err
	}
	return r.TxHashList, nil
}
