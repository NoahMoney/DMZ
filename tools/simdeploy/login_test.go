package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	rpc "veil/tools/simrpc"
)

// The wallets' login in these tests, and a wrong one. No output of simdeploy may contain any of them.
const (
	loginUser = "dmz-deployer"
	loginPass = "pw-Kq7vX2"
	badPass   = "pw-WRONG-39"
)

// TestMain runs simdeploy's main in a child process when a test asks for it (runSimdeploy): the refusals under test
// are exits.
func TestMain(m *testing.M) {
	if args, ok := os.LookupEnv("SIMDEPLOY_TEST_MAIN"); ok {
		os.Args = append([]string{"simdeploy"}, strings.Split(args, "\n")...)
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// loginNode is a fake daemon (login == "") or a fake wallet started with --rpc-login login. The daemon says it is
// mainnet; a wallet answers GetAddress. Any other method is answered with an RPC error, so the run stops at its
// first install's gas estimate and nothing reaches a transfer.
type loginNode struct {
	login, address string
	mu             sync.Mutex
	calls          []string // method, then "+auth" when the request carried an Authorization header
}

func (n *loginNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Method string `json:"method"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	call := req.Method
	if r.Header.Get("Authorization") != "" {
		call += "+auth"
	}
	n.mu.Lock()
	n.calls = append(n.calls, call)
	n.mu.Unlock()
	if u, p, ok := r.BasicAuth(); n.login != "" && (!ok || u+":"+p != n.login) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, "Authorization Required")
		return
	}
	switch req.Method {
	case "DERO.GetInfo":
		io.WriteString(w, `{"jsonrpc":"2.0","id":"1","result":{"testnet":false,"network":"mainnet"}}`)
	case "GetAddress":
		io.WriteString(w, `{"jsonrpc":"2.0","id":"1","result":{"address":"`+n.address+`"}}`)
	default:
		io.WriteString(w, `{"jsonrpc":"2.0","id":"1","error":{"code":-32000,"message":"fake node: stopped before `+req.Method+`"}}`)
	}
}

func (n *loginNode) seen() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.calls...)
}

func startNode(t *testing.T, login, address string) (*loginNode, string) {
	t.Helper()
	n := &loginNode{login: login, address: address}
	srv := httptest.NewServer(n)
	t.Cleanup(srv.Close)
	return n, strings.TrimPrefix(srv.URL, "http://")
}

// mainnetRun is a mainnet command line that passes every pre-install check, with its outputs in a scratch
// directory: the record is a copy, and the app directory is empty.
func mainnetRun(t *testing.T, daemon, w0, w2 string) []string {
	t.Helper()
	dir := t.TempDir()
	rec, err := os.ReadFile(filepath.Join("..", "..", "deploy", "networks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "networks.json"), rec, 0o644); err != nil {
		t.Fatal(err)
	}
	return []string{"-network", "mainnet", "-yes", "-daemon", daemon, "-w0", w0, "-w2", w2,
		"-contracts", filepath.Join("..", "..", "contracts"), "-app", filepath.Join(dir, "app"), "-networks", filepath.Join(dir, "networks.json"), "-timeout", "5s",
		"-pass-price", "100000", "-pass-max", "20", "-pass-reserve", "4",
		"-htlc-w1min", "400", "-htlc-w2min", "400", "-htlc-g2", "4800"}
}

// runSimdeploy runs main in a child with args, and with VEIL_RPC_LOGIN set to login, or absent when login is "".
func runSimdeploy(t *testing.T, login string, args []string) (int, string) {
	t.Helper()
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), rpc.LoginEnv+"=") {
			env = append(env, kv)
		}
	}
	env = append(env, "SIMDEPLOY_TEST_MAIN="+strings.Join(args, "\n"))
	if login != "" {
		env = append(env, rpc.LoginEnv+"="+login)
	}
	cmd := exec.Command(os.Args[0])
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, string(out)
	case errors.As(err, &ee):
		return ee.ExitCode(), string(out)
	}
	t.Fatalf("run simdeploy: %v", err)
	return 0, ""
}

// noLogin fails when simdeploy's output repeats any part of a login. It never prints the output.
func noLogin(t *testing.T, out string) {
	t.Helper()
	for _, s := range []string{loginUser, loginPass, badPass} {
		if strings.Contains(out, s) {
			t.Errorf("simdeploy's output repeats a login (%d bytes, not shown)", len(out))
			return
		}
	}
}

// shown is simdeploy's output for a failure message, or a placeholder when it repeats a login (noLogin reports that).
func shown(out string) string {
	for _, s := range []string{loginUser, loginPass, badPass} {
		if strings.Contains(out, s) {
			return "(output not shown: it repeats a login)"
		}
	}
	return out
}

// Without VEIL_RPC_LOGIN a mainnet run stops before its first call, and says which variable to set.
func TestMainnetRefusesWithoutTheWalletLogin(t *testing.T) {
	d, daemon := startNode(t, "", "")
	a, w0 := startNode(t, loginUser+":"+loginPass, "dero1qydeployer")
	b, w2 := startNode(t, loginUser+":"+loginPass, "dero1qytreasury")
	code, out := runSimdeploy(t, "", mainnetRun(t, daemon, w0, w2))
	if code != 2 || !strings.Contains(out, "mainnet:") || !strings.Contains(out, rpc.LoginEnv) {
		t.Fatalf("exit %d, want 2 naming %s:\n%s", code, rpc.LoginEnv, shown(out))
	}
	if strings.Contains(out, "-rpc-login") || strings.Contains(out, "-password") {
		t.Errorf("the refusal points at a command-line flag:\n%s", shown(out))
	}
	for _, n := range []*loginNode{d, a, b} {
		if c := n.seen(); len(c) != 0 {
			t.Errorf("calls were made before the refusal: %v", c)
		}
	}
}

// The rehearsal's workaround, the login in -w0 or -w2, is refused on mainnet: it puts the password in the process
// list and the shell history.
func TestMainnetRefusesALoginInTheWalletAddress(t *testing.T) {
	d, daemon := startNode(t, "", "")
	a, w0 := startNode(t, loginUser+":"+loginPass, "dero1qydeployer")
	_, w2 := startNode(t, loginUser+":"+loginPass, "dero1qytreasury")
	code, out := runSimdeploy(t, loginUser+":"+loginPass, mainnetRun(t, daemon, loginUser+":"+loginPass+"@"+w0, w2))
	noLogin(t, out)
	if code != 2 || !strings.Contains(out, "host:port") || !strings.Contains(out, rpc.LoginEnv) {
		t.Fatalf("exit %d, want 2 pointing at host:port and %s (%d bytes of output)", code, rpc.LoginEnv, len(out))
	}
	if len(d.seen())+len(a.seen()) != 0 {
		t.Errorf("calls were made before the refusal")
	}
}

// With VEIL_RPC_LOGIN set, both wallets are reached with it and the daemon never sees it; the run goes on to its
// first install, where the fake node stops it before any transfer.
func TestMainnetSendsTheLoginToTheWalletsOnly(t *testing.T) {
	d, daemon := startNode(t, "", "")
	a, w0 := startNode(t, loginUser+":"+loginPass, "dero1qydeployer")
	b, w2 := startNode(t, loginUser+":"+loginPass, "dero1qytreasury")
	code, out := runSimdeploy(t, loginUser+":"+loginPass, mainnetRun(t, daemon, w0, w2))
	noLogin(t, out)
	if code == 0 || !strings.Contains(out, "owner dero1qydeployer") || !strings.Contains(out, "treasury dero1qytreasury") {
		t.Fatalf("exit %d; the wallets were not read with the login:\n%s", code, shown(out))
	}
	if !strings.Contains(out, "stopped before DERO.GetGasEstimate") {
		t.Fatalf("the run did not reach its first install's estimate:\n%s", shown(out))
	}
	for _, c := range d.seen() {
		if strings.HasSuffix(c, "+auth") {
			t.Errorf("the daemon was sent a login: %v", d.seen())
		}
	}
	for _, n := range []*loginNode{a, b} {
		if c := n.seen(); strings.Join(c, ",") != "GetAddress+auth" {
			t.Errorf("wallet calls %v, want exactly one authenticated GetAddress", c)
		}
	}
}

// A wrong login stops the run at its first wallet call with the 401 message, before any install is estimated.
func TestMainnetStopsAtAWrongLogin(t *testing.T) {
	d, daemon := startNode(t, "", "")
	_, w0 := startNode(t, loginUser+":"+loginPass, "dero1qydeployer")
	_, w2 := startNode(t, loginUser+":"+loginPass, "dero1qytreasury")
	code, out := runSimdeploy(t, loginUser+":"+badPass, mainnetRun(t, daemon, w0, w2))
	noLogin(t, out)
	if code == 0 || !strings.Contains(out, "the wallet refused the login (401)") {
		t.Fatalf("exit %d, want the 401 message (%d bytes of output)", code, len(out))
	}
	if strings.Contains(out, "Authorization Required") || strings.Contains(out, "invalid character") {
		t.Errorf("the wallet's 401 page was read as a reply (%d bytes of output)", len(out))
	}
	for _, c := range d.seen() {
		if c != "DERO.GetInfo" {
			t.Errorf("the daemon got %s after the wallet refused the login", c)
		}
	}
}

// The gate is mainnet's alone: the simulator and the test networks run wallets with or without a login.
func TestTheWalletLoginIsRequiredOnMainnetOnly(t *testing.T) {
	saved := *network
	defer func() { *network = saved }()
	t.Setenv(rpc.LoginEnv, "")
	for _, n := range []string{"simulator", "testnet"} {
		*network = n
		if err := checkWalletLogin(); err != nil {
			t.Errorf("-network %s refused without a login: %v", n, err)
		}
	}
	*network = "mainnet"
	if err := checkWalletLogin(); err == nil {
		t.Error("-network mainnet accepted without a login")
	}
}
