package simrpc

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The login the fake wallet is started with, and a wrong one. No error may repeat any of them, and no failure
// message here prints an error that might.
const (
	testUser  = "dmz-deployer"
	testPass  = "pw-Kq7vX2"
	wrongUser = "intruder-58"
	wrongPass = "pw-WRONG-39"
)

var secrets = []string{testUser, testPass, wrongUser, wrongPass}

// fakeWallet answers as netwallet started with --rpc-login testUser:testPass does: 401 "Authorization Required" to
// a call without that basic auth (walletapi/rpcserver/rpc_websocket_server.go), a JSON-RPC reply to one with it.
type fakeWallet struct {
	mu   sync.Mutex
	auth []string // each request's Authorization header, "" when there was none
}

func (f *fakeWallet) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	f.mu.Unlock()
	if u, p, ok := r.BasicAuth(); !ok || u != testUser || p != testPass {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, "Authorization Required")
		return
	}
	var req struct {
		Method string `json:"method"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	switch req.Method {
	case "GetAddress":
		io.WriteString(w, `{"jsonrpc":"2.0","id":"1","result":{"address":"deto1qyfakewallet"}}`)
	case "GetHeight":
		io.WriteString(w, `{"jsonrpc":"2.0","id":"1","result":{"height":42}}`)
	default:
		io.WriteString(w, `{"jsonrpc":"2.0","id":"1","error":{"code":-32601,"message":"method not found"}}`)
	}
}

func (f *fakeWallet) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.auth...)
}

func startWallet(t *testing.T) (*fakeWallet, string) {
	t.Helper()
	f := &fakeWallet{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, strings.TrimPrefix(srv.URL, "http://")
}

// noLogin fails when msg repeats any part of a login. It never prints msg, which would print the login.
func noLogin(t *testing.T, what, msg string) {
	t.Helper()
	for _, s := range secrets {
		if strings.Contains(msg, s) {
			t.Errorf("%s repeats a login (%d-byte message, not shown)", what, len(msg))
			return
		}
	}
}

// safe is msg for a failure message, or a placeholder when msg repeats a login (noLogin reports that).
func safe(msg string) string {
	for _, s := range secrets {
		if strings.Contains(msg, s) {
			return "(not shown: it repeats a login)"
		}
	}
	return msg
}

// The login set in VEIL_RPC_LOGIN reaches the wallet on WalletCall and on every wallet helper.
func TestWalletCallSendsTheLoginFromTheEnvironment(t *testing.T) {
	f, host := startWallet(t)
	t.Setenv(LoginEnv, testUser+":"+testPass)
	var r struct {
		Address string `json:"address"`
	}
	if err := WalletCall(host, "GetAddress", nil, &r); err != nil {
		noLogin(t, "the error", err.Error())
		t.Fatalf("WalletCall with the right login failed (%d-byte error)", len(err.Error()))
	}
	if r.Address != "deto1qyfakewallet" {
		t.Fatalf("address %q", r.Address)
	}
	if a := Address(host); a != "deto1qyfakewallet" {
		t.Fatalf("Address() = %q", a)
	}
	if h := WalletHeight(host); h != 42 {
		t.Fatalf("WalletHeight() = %d", h)
	}
	for i, a := range f.requests() {
		if !strings.HasPrefix(a, "Basic ") {
			t.Errorf("request %d carried no basic auth", i)
		}
	}
}

// A wallet that asks for a login stops the call with the 401 message, never with the wallet's page, and the result
// is left untouched.
func TestWalletRefusesAMissingOrWrongLogin(t *testing.T) {
	_, host := startWallet(t)
	for _, tc := range []struct {
		name, login, hint string
	}{
		{"unset", "", "no login was sent"},
		{"wrong password", testUser + ":" + wrongPass, "is not the one the wallet was started with"},
		{"wrong user", wrongUser + ":" + testPass, "is not the one the wallet was started with"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(LoginEnv, tc.login)
			var r struct {
				Address string `json:"address"`
			}
			err := WalletCall(host, "GetAddress", nil, &r)
			if err == nil {
				t.Fatal("the call succeeded")
			}
			msg := err.Error()
			noLogin(t, "the 401 error", msg)
			if !errors.Is(err, ErrLoginRefused) || !strings.Contains(msg, "the wallet refused the login (401)") {
				t.Errorf("not the 401 error: %q", safe(msg))
			}
			if !strings.Contains(msg, tc.hint) || !strings.Contains(msg, LoginEnv) {
				t.Errorf("the error does not say what to do: %q", safe(msg))
			}
			if strings.Contains(msg, "Authorization Required") || strings.Contains(msg, "decode") {
				t.Errorf("the error reads the wallet's page as a reply: %q", safe(msg))
			}
			if r.Address != "" {
				t.Errorf("the result was filled: %q", r.Address)
			}
		})
	}
}

// The daemon asks for no login and is given none: VEIL_RPC_LOGIN goes to wallets only.
func TestTheDaemonNeverSeesTheLogin(t *testing.T) {
	var mu sync.Mutex
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		io.WriteString(w, `{"jsonrpc":"2.0","id":"1","result":{"height":7,"stableheight":5}}`)
	}))
	defer srv.Close()
	t.Setenv(LoginEnv, testUser+":"+testPass)
	c := &Client{Daemon: strings.TrimPrefix(srv.URL, "http://")}
	if h := c.Height(); h != 7 {
		t.Fatalf("height %d", h)
	}
	var r struct{}
	if err := Call(c.Daemon, "DERO.GetInfo", nil, &r); err != nil {
		t.Fatal(safe(err.Error()))
	}
	mu.Lock()
	defer mu.Unlock()
	for i, a := range auth {
		if a != "" {
			t.Errorf("daemon request %d carried an Authorization header", i)
		}
	}
}

// A login written into the address (user:password@host) still works, and it takes precedence over the variable for
// that host. A wrong one fails with the 401 message and without repeating it.
func TestALoginInTheAddressStillWorks(t *testing.T) {
	_, host := startWallet(t)
	t.Setenv(LoginEnv, "")
	var r struct {
		Address string `json:"address"`
	}
	if err := WalletCall(testUser+":"+testPass+"@"+host, "GetAddress", nil, &r); err != nil || r.Address == "" {
		t.Fatalf("a login in the address no longer works (err set: %v)", err != nil)
	}
	t.Setenv(LoginEnv, testUser+":"+wrongPass)
	if err := WalletCall(testUser+":"+testPass+"@"+host, "GetAddress", nil, &r); err != nil {
		t.Fatalf("the address's own login did not take precedence (err set: %v)", err != nil)
	}
	err := WalletCall(wrongUser+":"+wrongPass+"@"+host, "GetAddress", nil, &r)
	if err == nil {
		t.Fatal("a wrong login in the address was accepted")
	}
	noLogin(t, "the 401 error", err.Error())
	if !errors.Is(err, ErrLoginRefused) || !strings.Contains(err.Error(), "the wallet address") {
		t.Errorf("not the 401 error for the address's login: %q", safe(err.Error()))
	}
}

// Every other failure names the host without its login, and never repeats a body.
func TestNoErrorRepeatsTheLogin(t *testing.T) {
	// a wallet that is not there
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gone := l.Addr().String()
	l.Close()
	t.Setenv(LoginEnv, testUser+":"+testPass)
	err = WalletCall(wrongUser+":"+wrongPass+"@"+gone, "GetAddress", nil, nil)
	if err == nil {
		t.Fatal("a call to a closed port succeeded")
	}
	noLogin(t, "the connection error", err.Error())
	if !strings.Contains(err.Error(), gone) {
		t.Errorf("the connection error does not name the host: %q", safe(err.Error()))
	}
	err = WalletCall(gone, "GetAddress", nil, nil)
	if err == nil {
		t.Fatal("a call to a closed port succeeded")
	}
	noLogin(t, "the connection error", err.Error())

	// an address the URL parser refuses: its error would quote the whole URL
	err = WalletCall(wrongUser+":"+wrongPass+" x@"+gone, "GetAddress", nil, nil)
	if err == nil {
		t.Fatal("an unparseable address was accepted")
	}
	noLogin(t, "the address error", err.Error())

	// any status other than 200 and 401 is not a JSON-RPC reply, and its body is not shown
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "a proxy page: "+testPass)
	}))
	defer srv.Close()
	err = WalletCall(strings.TrimPrefix(srv.URL, "http://"), "GetAddress", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") || errors.Is(err, ErrLoginRefused) {
		t.Fatalf("a 500 is not reported as one (err set: %v)", err != nil)
	}
	noLogin(t, "the HTTP 500 error", err.Error())
}

// A value that is not user:password is refused before any request, without being repeated.
func TestAMalformedLoginSendsNothing(t *testing.T) {
	f, host := startWallet(t)
	t.Setenv(LoginEnv, testPass) // no colon
	err := WalletCall(host, "GetAddress", nil, nil)
	if err == nil || !strings.Contains(err.Error(), LoginEnv) {
		t.Fatalf("a malformed login was not refused by name (err set: %v)", err != nil)
	}
	noLogin(t, "the malformed-login error", err.Error())
	if n := len(f.requests()); n != 0 {
		t.Errorf("%d request(s) reached the wallet", n)
	}
}

// RequireWalletLogin, the mainnet gate of simdeploy and treasurybot: it names the variable, repeats no value, and
// never points at the command line as the place for a password.
func TestRequireWalletLogin(t *testing.T) {
	for _, tc := range []struct {
		name, login string
		hosts       []string
		ok          bool
	}{
		{"set", testUser + ":" + testPass, []string{"127.0.0.1:10103", "127.0.0.1:10104"}, true},
		{"unset", "", []string{"127.0.0.1:10103"}, false},
		{"no colon", testPass, nil, false},
		{"empty user", ":" + testPass, nil, false},
		{"empty password", testUser + ":", nil, false},
		{"login in an address", testUser + ":" + testPass, []string{"127.0.0.1:10103", wrongUser + ":" + wrongPass + "@127.0.0.1:10104"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(LoginEnv, tc.login)
			err := RequireWalletLogin(tc.hosts...)
			if tc.ok {
				if err != nil {
					t.Fatalf("refused: %q", safe(err.Error()))
				}
				return
			}
			if err == nil {
				t.Fatal("accepted")
			}
			msg := err.Error()
			noLogin(t, "the refusal", msg)
			if !strings.Contains(msg, LoginEnv) {
				t.Errorf("the refusal does not name %s: %q", LoginEnv, safe(msg))
			}
			if strings.Contains(msg, "-rpc-login") || strings.Contains(msg, "-password") {
				t.Errorf("the refusal points at a command-line flag: %q", safe(msg))
			}
		})
	}
}
