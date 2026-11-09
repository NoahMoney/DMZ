// Package monerovault is the Monero wallet-rpc side of a federation signer.
//
// It reads the wallet the operator already runs (deposits to the vault's per-index subaddresses, the reserve, the
// wallet's outgoing payments) and pays a withdrawal only through a payment id derived from the withdrawal index, so
// a retry after a crash finds the earlier payment instead of sending a second one. Every RPC failure is returned; a
// transport failure is retried a bounded number of times for reads. Nothing in this package exits the process.
//
// A wallet that is a Monero multisig wallet answers a transfer with a multisig txset rather than a sent transaction.
// This backend refuses that answer explicitly: it has no signing coordinator for Monero txesets, and pretending a
// txset was paid would let a signer vote Released for a payment nobody made. A single-key wallet (the simulator and
// a pilot) sends normally.
package monerovault

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
	"os"
	"strings"
	"sync"
	"time"
)

const (
	maxReply = 64 << 20
	// LoginEnv is read as user:password when the wallet-rpc was started with --rpc-login. It is read once per
	// request and never logged.
	LoginEnv = "VEIL_MONERO_RPC_LOGIN"
)

// ErrNeedsSignatures means the wallet returned a multisig/unsigned txset instead of a transaction it sent. Nothing
// was broadcast.
var ErrNeedsSignatures = errors.New("monerovault: the wallet returned a transaction to sign elsewhere; nothing was sent and this backend has no Monero multisig coordinator")

// RPCError is monero-wallet-rpc's own error answer.
type RPCError struct {
	Method  string
	Code    int
	Message string
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("monero-wallet-rpc %s: %d %s", e.Method, e.Code, e.Message)
}

// Client is one monero-wallet-rpc. endpoint may be host:port or a URL; /json_rpc is appended when absent.
type Client struct {
	url, uri string
	user     string
	pass     string
	http     *http.Client
	retries  int
	delay    time.Duration

	mu         sync.Mutex
	chal       *digestChallenge
	nc         uint32
	challenged bool
}

// NewClient builds a wallet-rpc client. A login written into the endpoint (user:password@host) is refused: it would
// put the password on the command line. Use LoginEnv instead.
func NewClient(endpoint, user, pass string, retries int, delay time.Duration) (*Client, error) {
	if strings.Contains(endpoint, "@") {
		return nil, errors.New("monerovault: the wallet-rpc endpoint carries a login; set " + LoginEnv + " instead of putting the password in the URL")
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "http://" + endpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("monerovault: %q is not a wallet-rpc URL", endpoint)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(u.Path, "/json_rpc") {
		u.Path += "/json_rpc"
	}
	if retries < 0 {
		retries = 0
	}
	return &Client{url: u.String(), uri: u.RequestURI(), user: user, pass: pass,
		http: &http.Client{Timeout: 10 * time.Minute}, retries: retries, delay: delay}, nil
}

// LoginFromEnv reads LoginEnv as user:password. Unset is no login.
func LoginFromEnv() (user, pass string, err error) {
	v := strings.TrimSpace(getenv(LoginEnv))
	if v == "" {
		return "", "", nil
	}
	u, p, ok := strings.Cut(v, ":")
	if !ok || u == "" || p == "" {
		return "", "", errors.New(LoginEnv + " must be user:password (its value is not shown)")
	}
	return u, p, nil
}

// getenv is a variable so a test can set a login without touching the process environment.
var getenv = os.Getenv

func (c *Client) Challenged() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.challenged
}

func (c *Client) call(ctx context.Context, method string, params, out interface{}) error {
	req := map[string]interface{}{"jsonrpc": "2.0", "id": "0", "method": method}
	if params != nil {
		req["params"] = params
	}
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("monero-wallet-rpc %s: encode: %w", method, err)
	}
	retryable := method != "transfer" && method != "create_address" && method != "open_wallet" && method != "close_wallet"
	for attempt := 0; ; attempt++ {
		err, retry := c.callOnce(ctx, method, body, out)
		if err == nil || !retry || !retryable || attempt >= c.retries {
			return err
		}
		delay := c.delay
		if delay <= 0 {
			delay = 500 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Duration(attempt+1) * delay):
		}
	}
}

func (c *Client) callOnce(ctx context.Context, method string, body []byte, out interface{}) (err error, retry bool) {
	resp, err := c.post(ctx, body)
	if err != nil {
		return fmt.Errorf("monero-wallet-rpc %s: %w", method, err), ctx.Err() == nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("monero-wallet-rpc %s: the wallet refused the login (HTTP %d)", method, resp.StatusCode), false
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return fmt.Errorf("monero-wallet-rpc %s: HTTP %d", method, resp.StatusCode), true
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("monero-wallet-rpc %s: HTTP %d", method, resp.StatusCode), false
	}
	raw, err := readCapped(resp.Body, maxReply)
	if err != nil {
		return fmt.Errorf("monero-wallet-rpc %s: %w", method, err), ctx.Err() == nil
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("monero-wallet-rpc %s: decode: %w", method, err), false
	}
	if env.Error != nil {
		return &RPCError{Method: method, Code: env.Error.Code, Message: env.Error.Message}, false
	}
	if out == nil {
		return nil, false
	}
	if len(env.Result) == 0 || string(env.Result) == "null" {
		return fmt.Errorf("monero-wallet-rpc %s: the reply carries neither a result nor an error", method), false
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("monero-wallet-rpc %s: result: %w", method, err), false
	}
	return nil, false
}

func readCapped(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("the answer is larger than %d bytes", max)
	}
	return b, nil
}

func (c *Client) post(ctx context.Context, body []byte) (*http.Response, error) {
	resp, err := c.send(ctx, body, c.authorization())
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
	if c.user == "" {
		return nil, errors.New("the wallet requires --rpc-login credentials and none were given; set " + LoginEnv)
	}
	c.mu.Lock()
	c.chal, c.nc, c.challenged = chal, 0, true
	c.mu.Unlock()
	resp, err = c.send(ctx, body, c.authorization())
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return nil, errors.New("authentication refused: wrong --rpc-login user or password")
	}
	return resp, nil
}

func (c *Client) send(ctx context.Context, body []byte, auth string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	return c.http.Do(req)
}

// --- HTTP digest authentication (RFC 7616, MD5 profile), the scheme monero-wallet-rpc uses for --rpc-login ---

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

func md5hex(s string) string { h := md5.Sum([]byte(s)); return hex.EncodeToString(h[:]) }

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

func (c *Client) authorization() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.chal == nil || c.user == "" {
		return ""
	}
	c.nc++
	var cn [8]byte
	if _, err := io.ReadFull(rand.Reader, cn[:]); err != nil {
		return ""
	}
	nc, cnonce := fmt.Sprintf("%08x", c.nc), hex.EncodeToString(cn[:])
	resp := digestResponse(c.user, c.pass, http.MethodPost, c.uri, c.chal, nc, cnonce)
	var b strings.Builder
	fmt.Fprintf(&b, `Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s", algorithm=%s`,
		c.user, c.chal.realm, c.chal.nonce, c.uri, resp, c.chal.algorithm)
	if c.chal.qop != "" {
		fmt.Fprintf(&b, `, qop=%s, nc=%s, cnonce="%s"`, c.chal.qop, nc, cnonce)
	}
	if c.chal.opaque != "" {
		fmt.Fprintf(&b, `, opaque="%s"`, c.chal.opaque)
	}
	return b.String()
}
