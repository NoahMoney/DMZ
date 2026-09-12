package simrpc

// The review of 2026-09-12, finding 5: the wallet login must never reach a proxy. Go's default transport reads
// HTTP_PROXY from the environment and exempts only loopback, so simrpc's calls to a wallet elsewhere went through
// whatever proxy the environment named, the login in their basic auth. Its transport now has no proxy, and on mainnet
// simdeploy and treasurybot refuse a wallet that is not on this machine.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// TestMain runs the child of TestTheLoginNeverGoesThroughAProxy when asked: Go reads the proxy variables once per
// process, so the proxy must be in the environment the process starts with.
func TestMain(m *testing.M) {
	if target := os.Getenv("SIMRPC_TEST_PROXY_CHILD"); target != "" {
		os.Exit(proxyChild(target))
	}
	os.Exit(m.Run())
}

// proxyChild makes a wallet call, a daemon call and a GetSC to two hosts that are not this machine. Its dialer takes
// both names to target, the fake node: a call made directly reaches the node, and one made through a proxy dials the
// proxy instead and never does.
func proxyChild(target string) int {
	dial := rpcTransport.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	rpcTransport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == "wallet.example:18082" || addr == "daemon.example:20000" {
			addr = target
		}
		return dial(ctx, network, addr)
	}
	var r struct {
		Address string `json:"address"`
	}
	if err := WalletCall("wallet.example:18082", "GetAddress", nil, &r); err != nil || r.Address != "deto1qyfakewallet" {
		fmt.Fprintf(os.Stderr, "the wallet call did not reach the wallet (error set: %v)\n", err != nil)
		return 1
	}
	c := &Client{Daemon: "daemon.example:20000"}
	if h := c.Height(); h != 7 {
		fmt.Fprintf(os.Stderr, "daemon height %d\n", h)
		return 1
	}
	if st := c.GetSC(strings.Repeat("ab", 32), false); st.U0("n") != 1 {
		fmt.Fprintln(os.Stderr, "GetSC did not reach the daemon")
		return 1
	}
	return 0
}

// TestTheLoginNeverGoesThroughAProxy: with HTTP_PROXY, HTTPS_PROXY and their lower-case forms naming a proxy, and no
// NO_PROXY, a wallet call carrying VEIL_RPC_LOGIN, a daemon call and a GetSC to hosts that are not this machine all go
// to those hosts directly, and the proxy sees nothing.
func TestTheLoginNeverGoesThroughAProxy(t *testing.T) {
	if rpcTransport.Proxy != nil {
		t.Fatal("simrpc's transport consults a proxy")
	}
	var mu sync.Mutex
	var proxied []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		proxied = append(proxied, r.Method+" "+r.URL.Host)
		mu.Unlock()
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		switch req.Method {
		case "GetAddress":
			if u, p, ok := r.BasicAuth(); !ok || u != testUser || p != testPass {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			io.WriteString(w, `{"jsonrpc":"2.0","id":"1","result":{"address":"deto1qyfakewallet"}}`)
		case "DERO.GetHeight":
			io.WriteString(w, `{"jsonrpc":"2.0","id":"1","result":{"height":7,"stableheight":5}}`)
		case "DERO.GetSC":
			io.WriteString(w, `{"jsonrpc":"2.0","id":"1","result":{"stringkeys":{"n":1},"balance":0}}`)
		default:
			io.WriteString(w, `{"jsonrpc":"2.0","id":"1","error":{"code":-32601,"message":"method not found"}}`)
		}
	}))
	defer node.Close()
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "REQUEST_METHOD", LoginEnv:
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "HTTP_PROXY="+proxy.URL, "http_proxy="+proxy.URL, "HTTPS_PROXY="+proxy.URL, "https_proxy="+proxy.URL,
		LoginEnv+"="+testUser+":"+testPass, "SIMRPC_TEST_PROXY_CHILD="+strings.TrimPrefix(node.URL, "http://"))
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	noLogin(t, "the child's output", string(out))
	mu.Lock()
	defer mu.Unlock()
	if len(proxied) != 0 {
		t.Fatalf("%d request(s) went to the proxy named in the environment: %v", len(proxied), proxied)
	}
	if err != nil {
		t.Fatalf("the calls did not reach the node directly: %v\n%s", err, safe(string(out)))
	}
}

// TestRequireLoopbackWallets: the mainnet gate takes a wallet on this machine only, by address; a name that merely
// resolves to it is not taken on trust, and the refusal repeats no login.
func TestRequireLoopbackWallets(t *testing.T) {
	for _, ok := range []string{"127.0.0.1:10103", "localhost:10103", "LOCALHOST:1", "[::1]:10103", "127.0.0.2:1"} {
		if err := RequireLoopbackWallets(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"10.0.0.5:10103", "192.168.1.2:30002", "wallet.example:18082", "0.0.0.0:10103", "[2001:db8::1]:1", "localhost.example:1",
		wrongUser + ":" + wrongPass + "@10.0.0.5:10103"} {
		err := RequireLoopbackWallets("127.0.0.1:1", bad)
		if err == nil {
			t.Errorf("%s accepted", safe(bad))
			continue
		}
		noLogin(t, "the refusal", err.Error())
		if !strings.Contains(err.Error(), "not on this machine") || !strings.Contains(err.Error(), LoginEnv) {
			t.Errorf("the refusal does not say why: %q", safe(err.Error()))
		}
	}
}
