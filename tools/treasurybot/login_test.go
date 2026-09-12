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

// The treasury wallet's login in these tests, and a wrong one. No output of the bot may contain any of them.
const (
	botUser    = "dmz-treasury"
	botPass    = "pw-Kq7vX2"
	botBadPass = "pw-WRONG-39"
)

// TestMain runs treasurybot's main in a child process when a test asks for it (runBot): the refusals under test
// are exits.
func TestMain(m *testing.M) {
	if args, ok := os.LookupEnv("TREASURYBOT_TEST_MAIN"); ok {
		os.Args = append([]string{"treasurybot"}, strings.Split(args, "\n")...)
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// botNode is a fake daemon (login == "") or a fake wallet started with --rpc-login login. The daemon says it is
// mainnet; the wallet answers GetAddress. Any other method is answered with an RPC error, so the bot stops at its
// first read of the pass contract, before anything could be sent.
type botNode struct {
	login string
	mu    sync.Mutex
	calls []string // method, then "+auth" when the request carried an Authorization header
}

func (n *botNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		io.WriteString(w, `{"jsonrpc":"2.0","id":"1","result":{"address":"dero1qytreasurywallet"}}`)
	default:
		io.WriteString(w, `{"jsonrpc":"2.0","id":"1","error":{"code":-32000,"message":"fake node: stopped before `+req.Method+`"}}`)
	}
}

func (n *botNode) seen() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.calls...)
}

func startBotNode(t *testing.T, login string) (*botNode, string) {
	t.Helper()
	n := &botNode{login: login}
	srv := httptest.NewServer(n)
	t.Cleanup(srv.Close)
	return n, strings.TrimPrefix(srv.URL, "http://")
}

// mainnetBot is a command line for a mainnet config.js, with the config and the journal in a scratch directory. It
// has no -yes, so it is the runbook's dry run: the one that still reads the wallet.
func mainnetBot(t *testing.T, daemon, wallet string) []string {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.js")
	src := "export default {\n  network: \"mainnet\",\n  factory: \"" + strings.Repeat("a", 64) + "\",\n  founders: \"" + strings.Repeat("b", 64) + "\",\n}\n"
	if err := os.WriteFile(cfg, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return []string{"-daemon", daemon, "-wallet", wallet, "-config", cfg, "-journal", filepath.Join(dir, "journal.json"), "-once", "-timeout", "5s"}
}

// runBot runs main in a child with args, and with VEIL_RPC_LOGIN set to login, or absent when login is "".
func runBot(t *testing.T, login string, args []string) (int, string) {
	t.Helper()
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), rpc.LoginEnv+"=") {
			env = append(env, kv)
		}
	}
	env = append(env, "TREASURYBOT_TEST_MAIN="+strings.Join(args, "\n"))
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
	t.Fatalf("run treasurybot: %v", err)
	return 0, ""
}

// botNoLogin fails when the bot's output repeats any part of a login. It never prints the output.
func botNoLogin(t *testing.T, out string) {
	t.Helper()
	for _, s := range []string{botUser, botPass, botBadPass} {
		if strings.Contains(out, s) {
			t.Errorf("treasurybot's output repeats a login (%d bytes, not shown)", len(out))
			return
		}
	}
}

// botShown is the bot's output for a failure message, or a placeholder when it repeats a login (botNoLogin reports
// that).
func botShown(out string) string {
	for _, s := range []string{botUser, botPass, botBadPass} {
		if strings.Contains(out, s) {
			return "(output not shown: it repeats a login)"
		}
	}
	return out
}

// Without VEIL_RPC_LOGIN a mainnet config stops before the first call, dry run included, and says which variable
// to set.
func TestMainnetBotRefusesWithoutTheWalletLogin(t *testing.T) {
	d, daemon := startBotNode(t, "")
	w, wallet := startBotNode(t, botUser+":"+botPass)
	code, out := runBot(t, "", mainnetBot(t, daemon, wallet))
	if code == 0 || !strings.Contains(out, "mainnet:") || !strings.Contains(out, rpc.LoginEnv) {
		t.Fatalf("exit %d, want a refusal naming %s:\n%s", code, rpc.LoginEnv, botShown(out))
	}
	if strings.Contains(out, "-rpc-login") || strings.Contains(out, "-password") {
		t.Errorf("the refusal points at a command-line flag:\n%s", botShown(out))
	}
	if len(d.seen())+len(w.seen()) != 0 {
		t.Errorf("calls were made before the refusal: daemon %v, wallet %v", d.seen(), w.seen())
	}
}

// A login written into -wallet is refused on mainnet, before any call.
func TestMainnetBotRefusesALoginInTheWalletAddress(t *testing.T) {
	d, daemon := startBotNode(t, "")
	w, wallet := startBotNode(t, botUser+":"+botPass)
	code, out := runBot(t, botUser+":"+botPass, mainnetBot(t, daemon, botUser+":"+botPass+"@"+wallet))
	botNoLogin(t, out)
	if code == 0 || !strings.Contains(out, "host:port") {
		t.Fatalf("exit %d, want a refusal pointing at host:port (%d bytes of output)", code, len(out))
	}
	if len(d.seen())+len(w.seen()) != 0 {
		t.Errorf("calls were made before the refusal")
	}
}

// A wallet that is not on this machine is refused on a mainnet config before any call, dry run included: its login
// would travel to it as HTTP basic auth over plain HTTP (the review of 2026-09-12, finding 5).
func TestMainnetBotRefusesAWalletOffThisMachine(t *testing.T) {
	d, daemon := startBotNode(t, "")
	for _, far := range []string{"10.0.0.5:30002", "wallet.example:30002"} {
		code, out := runBot(t, botUser+":"+botPass, mainnetBot(t, daemon, far))
		botNoLogin(t, out)
		if code == 0 || !strings.Contains(out, "not on this machine") || !strings.Contains(out, far) {
			t.Fatalf("-wallet %s: exit %d, want a refusal naming the wallet:\n%s", far, code, botShown(out))
		}
	}
	if c := d.seen(); len(c) != 0 {
		t.Errorf("calls were made before the refusal: %v", c)
	}
}

// With VEIL_RPC_LOGIN set, the wallet is read with it and the daemon never sees it.
func TestMainnetBotSendsTheLoginToTheWalletOnly(t *testing.T) {
	d, daemon := startBotNode(t, "")
	w, wallet := startBotNode(t, botUser+":"+botPass)
	code, out := runBot(t, botUser+":"+botPass, mainnetBot(t, daemon, wallet))
	botNoLogin(t, out)
	if code == 0 || !strings.Contains(out, "stopped before DERO.GetSC") {
		t.Fatalf("exit %d; the bot did not get past the wallet to the pass contract:\n%s", code, botShown(out))
	}
	if c := w.seen(); strings.Join(c, ",") != "GetAddress+auth" {
		t.Errorf("wallet calls %v, want exactly one authenticated GetAddress", c)
	}
	for _, c := range d.seen() {
		if strings.HasSuffix(c, "+auth") {
			t.Errorf("the daemon was sent a login: %v", d.seen())
		}
	}
}

// A wrong login stops the bot at its first wallet call with the 401 message.
func TestMainnetBotStopsAtAWrongLogin(t *testing.T) {
	d, daemon := startBotNode(t, "")
	_, wallet := startBotNode(t, botUser+":"+botPass)
	code, out := runBot(t, botUser+":"+botBadPass, mainnetBot(t, daemon, wallet))
	botNoLogin(t, out)
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

// The gate is mainnet's alone.
func TestBotWalletLoginIsRequiredOnMainnetOnly(t *testing.T) {
	t.Setenv(rpc.LoginEnv, "")
	for _, n := range []string{"simulator", "testnet"} {
		if err := checkWalletLogin(n); err != nil {
			t.Errorf("network %s refused without a login: %v", n, err)
		}
	}
	if err := checkWalletLogin("mainnet"); err == nil {
		t.Error("mainnet accepted without a login")
	}
}
