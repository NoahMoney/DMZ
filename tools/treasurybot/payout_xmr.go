package main

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
	"sort"
	"strings"
	"time"

	"veil/tools/xmrswap"
)

// The Monero side. The treasury's own monero-wallet-rpc, with the treasury wallet open, holds the fees and pays the
// passes. xmrswap's client pays one destination per call and cannot build without relaying, so the bot speaks the
// six methods it needs itself: get_address_index, refresh, get_transfers, transfer (do_not_relay, get_tx_metadata),
// relay_tx, get_transfer_by_txid. Addresses are checked with xmrswap.ParseAddress.

// moneroRPC is one monero-wallet-rpc. Its --rpc-login, when set, is answered as HTTP digest (MD5, qop=auth), the
// scheme epee serves and xmrswap's client speaks. No error repeats a request body.
type moneroRPC struct {
	url, user, pass string
	http            *http.Client
}

type xmrError struct {
	Method  string
	Code    int
	Message string
}

func (e *xmrError) Error() string {
	return fmt.Sprintf("monero-wallet-rpc %s: %d %s", e.Method, e.Code, e.Message)
}

func newMoneroRPC(hostport, login string) *moneroRPC {
	u, p, _ := strings.Cut(login, ":")
	return &moneroRPC{url: "http://" + hostport + "/json_rpc", user: u, pass: p,
		http: &http.Client{Timeout: 10 * time.Minute, Transport: &http.Transport{Proxy: nil}}}
}

func (w *moneroRPC) call(ctx context.Context, method string, params, out interface{}) error {
	body, err := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": "0", "method": method, "params": params})
	if err != nil {
		return err
	}
	resp, err := w.post(ctx, body, "")
	if err != nil {
		return fmt.Errorf("monero-wallet-rpc %s: %v", method, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		chal := resp.Header.Values("WWW-Authenticate")
		resp.Body.Close()
		if w.user == "" {
			return fmt.Errorf("monero-wallet-rpc %s: the wallet asks for its --rpc-login and %s is not set", method, xmrLoginEnv)
		}
		auth, err := w.digest(chal)
		if err != nil {
			return fmt.Errorf("monero-wallet-rpc %s: %v", method, err)
		}
		if resp, err = w.post(ctx, body, auth); err != nil {
			return fmt.Errorf("monero-wallet-rpc %s: %v", method, err)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			resp.Body.Close()
			return fmt.Errorf("monero-wallet-rpc %s: the wallet refused the login in %s", method, xmrLoginEnv)
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("monero-wallet-rpc %s: http %d", method, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("monero-wallet-rpc %s: %v", method, err)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("monero-wallet-rpc %s: decode: %v", method, err)
	}
	if env.Error != nil {
		return &xmrError{Method: method, Code: env.Error.Code, Message: env.Error.Message}
	}
	if out == nil {
		return nil
	}
	if len(env.Result) == 0 || string(env.Result) == "null" {
		return fmt.Errorf("monero-wallet-rpc %s: the reply has neither a result nor an error", method)
	}
	return json.Unmarshal(env.Result, out)
}

func (w *moneroRPC) post(ctx context.Context, body []byte, auth string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("the wallet address is not host:port")
	}
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	return w.http.Do(req)
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// digest answers the server's MD5 Digest challenge (RFC 2617 §3.2.2), with qop=auth when it offers it.
func (w *moneroRPC) digest(values []string) (string, error) {
	for _, v := range values {
		if len(v) < 7 || !strings.EqualFold(v[:7], "Digest ") {
			continue
		}
		p := authParams(v[7:])
		if a := p["algorithm"]; a != "" && !strings.EqualFold(a, "MD5") {
			continue
		}
		var cn [8]byte
		if _, err := rand.Read(cn[:]); err != nil {
			return "", err
		}
		cnonce, uri := hex.EncodeToString(cn[:]), "/json_rpc"
		ha1, ha2 := md5hex(w.user+":"+p["realm"]+":"+w.pass), md5hex("POST:"+uri)
		h := fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", algorithm=MD5`, w.user, p["realm"], p["nonce"], uri)
		if strings.Contains(p["qop"], "auth") {
			h += fmt.Sprintf(`, response="%s", qop=auth, nc=00000001, cnonce="%s"`, md5hex(ha1+":"+p["nonce"]+":00000001:"+cnonce+":auth:"+ha2), cnonce)
		} else {
			h += fmt.Sprintf(`, response="%s"`, md5hex(ha1+":"+p["nonce"]+":"+ha2))
		}
		if o := p["opaque"]; o != "" {
			h += fmt.Sprintf(`, opaque="%s"`, o)
		}
		return h, nil
	}
	return "", errors.New("401 without an MD5 Digest challenge")
}

// authParams reads a Digest header's key=value list, quoted or bare.
func authParams(s string) map[string]string {
	out := map[string]string{}
	for len(s) > 0 {
		s = strings.TrimLeft(s, " ,")
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			break
		}
		k := strings.ToLower(strings.TrimSpace(s[:eq]))
		s = s[eq+1:]
		var v string
		if strings.HasPrefix(s, `"`) {
			end := strings.IndexByte(s[1:], '"')
			if end < 0 {
				v, s = s[1:], ""
			} else {
				v, s = s[1:1+end], s[2+end:]
			}
		} else if end := strings.IndexByte(s, ','); end < 0 {
			v, s = strings.TrimSpace(s), ""
		} else {
			v, s = strings.TrimSpace(s[:end]), s[end:]
		}
		out[k] = v
	}
	return out
}

// xmrNet pays from the account holding xmrTreasury. Fees are the incoming transfers to that one subaddress; the
// wallet's change goes to the account's primary address, and the bot's own payouts are excluded by txid in any case.
type xmrNet struct {
	w            *moneroRPC
	treasury     string
	major, minor uint64
	want         xmrswap.Network
	minPart      uint64
}

func (x *xmrNet) Name() string    { return "XMR" }
func (x *xmrNet) MinPart() uint64 { return x.minPart }

// MaxDests: a Monero transaction has at most 16 outputs, and one is the change.
func (x *xmrNet) MaxDests() int { return 15 }

func (x *xmrNet) Check(addr string) error { return checkXmr(addr, x.want) }

// checkXmr is the full check the contract cannot make: base58 blocks, the Keccak checksum, the network prefix, and
// both keys on the curve (xmrswap.ParseAddress). An integrated address is refused: a transfer carries one payment
// id at most, and the form asks for a subaddress.
func checkXmr(addr string, want xmrswap.Network) error {
	pa, err := xmrswap.ParseAddress(addr)
	if err != nil {
		return err
	}
	if pa.Net != want {
		return fmt.Errorf("a Monero %s address; this bot pays on %s", pa.Net, want)
	}
	return nil
}

// locate finds xmrTreasury in the open wallet: an address that is not this wallet's own is refused.
func (x *xmrNet) locate(ctx context.Context) error {
	var r struct {
		Index struct {
			Major uint64 `json:"major"`
			Minor uint64 `json:"minor"`
		} `json:"index"`
	}
	if err := x.w.call(ctx, "get_address_index", map[string]interface{}{"address": x.treasury}, &r); err != nil {
		return fmt.Errorf("xmrTreasury is not an address of the wallet open at -xmr-wallet: %v", err)
	}
	x.major, x.minor = r.Index.Major, r.Index.Minor
	return nil
}

func (x *xmrNet) Income(ctx context.Context) ([]income, error) {
	if err := x.w.call(ctx, "refresh", map[string]interface{}{}, nil); err != nil {
		return nil, err
	}
	var in struct {
		In []struct {
			TxID          string `json:"txid"`
			Amount        uint64 `json:"amount"`
			Confirmations uint64 `json:"confirmations"`
			Locked        bool   `json:"locked"`
			Address       string `json:"address"`
			Sub           struct {
				Major uint64 `json:"major"`
				Minor uint64 `json:"minor"`
			} `json:"subaddr_index"`
		} `json:"in"`
	}
	if err := x.w.call(ctx, "get_transfers", map[string]interface{}{"in": true, "account_index": x.major, "subaddr_indices": []uint64{x.minor}}, &in); err != nil {
		return nil, err
	}
	var out struct {
		Out     []struct{ TxID string } `json:"out"`
		Pending []struct{ TxID string } `json:"pending"`
	}
	if err := x.w.call(ctx, "get_transfers", map[string]interface{}{"out": true, "pending": true, "account_index": x.major}, &out); err != nil {
		return nil, err
	}
	mine := map[string]bool{}
	for _, t := range append(out.Out, out.Pending...) {
		mine[t.TxID] = true
	}
	var got []income
	for _, t := range in.In {
		if t.Sub.Major != x.major || t.Sub.Minor != x.minor || (t.Address != "" && t.Address != x.treasury) {
			continue
		}
		if t.Locked || t.Confirmations < incomeConf || mine[t.TxID] || t.Amount == 0 || len(t.TxID) != 64 {
			continue
		}
		got = append(got, income{ID: t.TxID, Amt: t.Amount})
	}
	sort.Slice(got, func(i, j int) bool { return got[i].ID < got[j].ID })
	return got, nil
}

// Build asks the wallet for one transfer to every destination, signed and NOT relayed, with its tx_metadata: the
// bytes relay_tx takes to broadcast exactly that transaction, now or after a crash. The fee is the treasury's.
func (x *xmrNet) Build(ctx context.Context, dests []dest, _ func(string) bool) (built, error) {
	ds := make([]map[string]interface{}, len(dests))
	for i, d := range dests {
		ds[i] = map[string]interface{}{"amount": d.Amt, "address": d.Addr}
	}
	var r struct {
		TxHash   string `json:"tx_hash"`
		Meta     string `json:"tx_metadata"`
		Fee      uint64 `json:"fee"`
		Unsigned string `json:"unsigned_txset"`
		Multisig string `json:"multisig_txset"`
	}
	params := map[string]interface{}{"destinations": ds, "account_index": x.major, "do_not_relay": true, "get_tx_metadata": true}
	if err := x.w.call(ctx, "transfer", params, &r); err != nil {
		return built{}, err
	}
	if r.Unsigned != "" || r.Multisig != "" {
		return built{}, errors.New("the wallet answered with a transaction to sign elsewhere (a view-only or multisig wallet): nothing was built")
	}
	if len(r.TxHash) != 64 || r.Meta == "" {
		return built{}, errors.New("the wallet's transfer carries no tx_hash or no tx_metadata")
	}
	return built{TxID: r.TxHash, Blob: r.Meta, Fee: r.Fee}, nil
}

// Broadcast relays the recorded transaction unless the wallet already has it.
func (x *xmrNet) Broadcast(ctx context.Context, p *payout) error {
	if st, err := x.State(ctx, p); err == nil && st != txUnknown {
		return nil
	}
	var r struct {
		TxHash string `json:"tx_hash"`
	}
	if err := x.w.call(ctx, "relay_tx", map[string]interface{}{"hex": p.Blob}, &r); err != nil {
		var xe *xmrError
		if errors.As(err, &xe) {
			return fmt.Errorf("relay_tx refused: code %d", xe.Code) // the message is not repeated: it could quote the metadata
		}
		return err
	}
	if r.TxHash != p.TxID {
		return fmt.Errorf("the wallet relayed %s, the payout recorded %s", r.TxHash, p.TxID)
	}
	st, err := x.State(ctx, p)
	if err != nil {
		return err
	}
	if st == txUnknown {
		return errors.New("relay_tx answered, but the wallet lists no such transfer: its fate is unknown, and it will be looked up again")
	}
	return nil
}

// State is the wallet's own record: get_transfer_by_txid. "Not found" (-8) is unknown; "failed" (dropped by the
// pool) is unknown too, so the same transaction is relayed again, and a new one is never built for its parts.
func (x *xmrNet) State(ctx context.Context, p *payout) (txState, error) {
	if err := x.w.call(ctx, "refresh", map[string]interface{}{}, nil); err != nil {
		return 0, err
	}
	var r struct {
		T struct {
			Type string `json:"type"`
			Conf uint64 `json:"confirmations"`
		} `json:"transfer"`
	}
	err := x.w.call(ctx, "get_transfer_by_txid", map[string]interface{}{"txid": p.TxID, "account_index": x.major}, &r)
	var xe *xmrError
	if errors.As(err, &xe) && xe.Code == -8 {
		return txUnknown, nil
	}
	if err != nil {
		return 0, err
	}
	switch r.T.Type {
	case "out":
		if r.T.Conf >= incomeConf {
			return txMined, nil
		}
		return txPending, nil
	case "pending", "pool":
		return txPending, nil
	case "failed":
		return txUnknown, nil
	}
	return 0, fmt.Errorf("the wallet lists %s as %q", p.TxID, r.T.Type)
}
