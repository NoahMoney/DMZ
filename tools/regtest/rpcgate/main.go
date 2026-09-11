// Command rpcgate is a JSON-RPC allow-list in front of one zebrad: it serves only the methods tools/zecswap calls,
// with a cookie of its own, and forwards those — and nothing else — to the zebrad behind it.
//
// zebrad 6.3.0 cannot restrict its RPC to a set of methods (zebra-rpc's config has listen_addr, cookie_dir,
// enable_cookie_auth and nothing per method), so the harness's testnet node keeps its own RPC on an internal loopback
// port with its cookie inside WSL's filesystem, and clients get this gate instead. A method outside the list is
// answered with JSON-RPC error -32601 and never reaches zebrad; a batch (a JSON array) is refused; a request without
// the gate's cookie is refused with HTTP 401. What reaches zebrad is a request rebuilt from the fields the gate
// checked, never the client's own bytes (main_test.go holds each of these).
//
// The gate forwards to one fixed upstream, so it can reach no other node; which network that node is on is
// checked by zcash-testnet-up.sh, which stops both when it is not testnet.
package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// zecswapMethods are the seven methods tools/zecswap/node.go calls (its METHODS comment and its c.call sites).
const zecswapMethods = "getblockchaininfo,getblockhash,getaddressutxos,getaddresstxids,getrawtransaction,sendrawtransaction,gettxout"

const (
	maxRequest  = 8 << 20  // sendrawtransaction of a 2 MB transaction is 4 MB of hex
	maxResponse = 64 << 20 // zebrad's own max_response_body_size is 50 MiB
)

// gate is the allow-list proxy. want is the client cookie ("__cookie__:<hex>"); upstream is zebrad's RPC URL; and
// upCookie is the file zebrad keeps its own cookie in, re-read on every request because zebrad rewrites it when it
// restarts.
type gate struct {
	allowed  map[string]bool
	allow    string
	want     string
	upstream string
	upCookie string
	client   *http.Client
}

func parseAllow(s string) map[string]bool {
	allowed := map[string]bool{}
	for _, m := range strings.Split(s, ",") {
		if m = strings.TrimSpace(m); m != "" {
			allowed[m] = true
		}
	}
	return allowed
}

func (g *gate) fail(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "1.0", "id": id, "result": nil,
		"error": map[string]interface{}{"code": code, "message": msg}})
}

func (g *gate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	user, pass, ok := r.BasicAuth()
	if !ok || subtle.ConstantTimeCompare([]byte(user+":"+pass), []byte(g.want)) != 1 {
		w.Header().Set("WWW-Authenticate", `Basic realm="rpcgate"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequest+1))
	if err != nil || len(body) > maxRequest {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	if t := bytes.TrimSpace(body); len(t) > 0 && t[0] == '[' {
		g.fail(w, nil, -32600, "rpcgate: batch requests are not served")
		return
	}
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		g.fail(w, nil, -32700, "rpcgate: not a JSON-RPC request")
		return
	}
	if !g.allowed[req.Method] {
		log.Printf("refused %q", req.Method)
		g.fail(w, req.ID, -32601, fmt.Sprintf("rpcgate: method %q is not served (only %s)", req.Method, g.allow))
		return
	}
	c, err := os.ReadFile(g.upCookie)
	if err != nil {
		g.fail(w, req.ID, -32603, "rpcgate: zebrad's cookie is not readable (is zebrad running?)")
		return
	}
	u, p, _ := strings.Cut(strings.TrimSpace(string(c)), ":")
	// Forward a request rebuilt from the fields checked above, never the client's own bytes. Go's decoder keeps the
	// LAST of a duplicated "method" key, and another parser may keep the first, so forwarding the raw body would let
	// {"method":"stop","method":"getblockhash"} pass this gate as getblockhash and reach zebrad as something else.
	// Extra top-level fields are dropped the same way.
	out := map[string]interface{}{"jsonrpc": "1.0", "method": req.Method}
	if req.JSONRPC != "" {
		out["jsonrpc"] = req.JSONRPC
	}
	if len(req.ID) > 0 {
		out["id"] = req.ID
	}
	if len(req.Params) > 0 {
		out["params"] = req.Params
	}
	fwdBody, err := json.Marshal(out)
	if err != nil {
		g.fail(w, req.ID, -32600, "rpcgate: the request cannot be re-encoded")
		return
	}
	fw, err := http.NewRequestWithContext(r.Context(), http.MethodPost, g.upstream, bytes.NewReader(fwdBody))
	if err != nil {
		g.fail(w, req.ID, -32603, "rpcgate: bad upstream: "+err.Error())
		return
	}
	fw.Header.Set("Content-Type", "application/json")
	fw.SetBasicAuth(u, p)
	resp, err := g.client.Do(fw)
	if err != nil {
		g.fail(w, req.ID, -32603, "rpcgate: zebrad did not answer: "+err.Error())
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, io.LimitReader(resp.Body, maxResponse))
}

func main() {
	listen := flag.String("listen", "127.0.0.1:18232", "where clients connect (loopback)")
	upstream := flag.String("upstream", "http://127.0.0.1:18242/", "zebrad's own RPC")
	upCookie := flag.String("upstream-cookie", "", "zebrad's .cookie file (re-read on every request: zebrad rewrites it when it restarts)")
	cookieOut := flag.String("cookie-out", "", "where to write this gate's own cookie (__cookie__:<random>) for clients")
	allow := flag.String("allow", zecswapMethods, "comma-separated methods to serve")
	flag.Parse()
	if !strings.HasPrefix(*listen, "127.0.0.1:") {
		log.Fatalf("rpcgate: -listen must be a 127.0.0.1 address, not %q", *listen)
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		log.Fatal(err)
	}
	want := "__cookie__:" + hex.EncodeToString(secret[:])
	if err := os.WriteFile(*cookieOut, []byte(want), 0o600); err != nil {
		log.Fatalf("rpcgate: write %s: %v", *cookieOut, err)
	}
	g := &gate{allowed: parseAllow(*allow), allow: *allow, want: want, upstream: *upstream, upCookie: *upCookie,
		client: &http.Client{Timeout: 5 * time.Minute}}
	log.Printf("rpcgate on %s -> %s, serving %s", *listen, *upstream, *allow)
	srv := &http.Server{Addr: *listen, Handler: g, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
