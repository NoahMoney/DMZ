package xmrswap

// MT-L02: no secret — s_a, s_b, k_b, the view key, the joint spend key, a wallet password — reaches the companion's
// output or log: not through the state machine's own lines, and not through an error a server made of a request.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestNoSecretReachesTheCompanionsOutput: every line either companion writes — its steps, its errors, its outcomes —
// across the three exits and two runs with processes dying at random, holds none of the swap's secrets.
func TestNoSecretReachesTheCompanionsOutput(t *testing.T) {
	for i, p := range []plan{
		{scenario: "happy", seed: 221}, {scenario: "refund", seed: 222}, {scenario: "punish", seed: 223},
		{scenario: "happy", randomCrash: 0.2, seed: 224}, {scenario: "refund", randomCrash: 0.2, seed: 225},
	} {
		r := newRun(t, p)
		r.drive()
		bob, alice := r.loadState("bob"), r.loadState("alice")
		secrets := map[string]string{
			"s_a": alice.Own.Secret, "s_b": bob.Own.Secret, "k_b": bob.Gate, "the view key": alice.View,
			"bob's joint wallet password": bob.JointPassword, "alice's joint wallet password": alice.JointPassword,
		}
		sa, err1 := ParseScalar(alice.Own.Secret)
		sb, err2 := ParseScalar(bob.Own.Secret)
		if err1 != nil || err2 != nil {
			t.Fatalf("run %d: the shares do not parse: %v %v", i, err1, err2)
		}
		secrets["the joint spend key"] = AddScalars(sa, sb).Hex()
		lines := append(append([]string(nil), r.logs...), "outcome: "+bob.Outcome, "outcome: "+alice.Outcome)
		for name, sec := range secrets {
			if sec == "" {
				t.Fatalf("run %d (%s): %s is empty, so the check would prove nothing", i, p.scenario, name)
			}
			for _, line := range lines {
				if strings.Contains(line, sec) {
					t.Fatalf("run %d (%s): %s in the output: %s", i, p.scenario, name, line)
				}
			}
		}
		t.Logf("run %d (%s, random crashes %.1f): %d lines, %d secrets, none printed", i, p.scenario, p.randomCrash, len(lines), len(secrets))
	}
}

// echoServer answers the calls named in answers, and every other call with a JSON-RPC error that quotes the whole
// request: the worst a daemon, a wallet-rpc or a proxy in front of one could do with a request carrying a secret.
// echoed returns everything it quoted.
func echoServer(t *testing.T, answers map[string]string) (url string, echoed func() string) {
	t.Helper()
	var mu sync.Mutex
	var quoted strings.Builder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string          `json:"method"`
			ID     json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(body, &req)
		id := string(req.ID)
		if id == "" {
			id = `"1"`
		}
		w.Header().Set("Content-Type", "application/json")
		if res, ok := answers[req.Method]; ok {
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, id, res)
			return
		}
		mu.Lock()
		quoted.Write(body)
		mu.Unlock()
		msg, _ := json.Marshal("cannot process this request: " + string(body))
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-1,"message":%s}}`, id, msg)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() string { mu.Lock(); defer mu.Unlock(); return quoted.String() }
}

// TestTheClientsWithholdSecretsFromTheirErrors: the DERO client's Claim (s_a, k_b) and Refund (s_b), and the Monero
// client's generate_from_keys (the view key, the spend key, the password), each refused by a server that quotes the
// request back: the server saw every secret, and none is in the error the caller prints or in the client's log. The
// error still unwraps to what the caller decides on.
func TestTheClientsWithholdSecretsFromTheirErrors(t *testing.T) {
	ctx := context.Background()
	sa, kb, sb := strings.Repeat("a1", 32), strings.Repeat("c3", 32), strings.Repeat("b2", 32)
	url, echoed := echoServer(t, map[string]string{
		"DERO.GetHeight": `{"height":100,"stableheight":92,"topoheight":100,"status":"OK"}`,
		"GetAddress":     `{"address":"` + aliceAddr + `"}`,
	})
	d := NewDeroRPC(url, url, testHTLC)
	var logged []string
	d.Log = func(f string, a ...interface{}) { logged = append(logged, fmt.Sprintf(f, a...)) }
	_, claimErr := d.Claim(ctx, 0, sa, kb, 1000)
	_, refundErr := d.Refund(ctx, 0, sb)

	spend, err := NewScalar(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	view, err := NewScalar(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	addr, err := Address(spend.Public(), view.Public(), Stagenet)
	if err != nil {
		t.Fatal(err)
	}
	wurl, wechoed := echoServer(t, nil)
	const password = "the-joint-wallet-password"
	genErr := NewWalletRPC(wurl, "", "").GenerateFromKeys(ctx, "joint", addr, view.Hex(), spend.Hex(), password, 0)

	seen := echoed() + wechoed()
	secrets := map[string]string{"s_a": sa, "k_b": kb, "s_b": sb, "the view key": view.Hex(), "the spend key": spend.Hex(), "the password": password}
	for name, s := range secrets {
		if !strings.Contains(seen, s) {
			t.Fatalf("the server never saw %s, so the test proves nothing", name)
		}
	}
	for what, e := range map[string]error{"Claim": claimErr, "Refund": refundErr, "generate_from_keys": genErr} {
		if e == nil {
			t.Fatalf("%s refused by the server reported success", what)
		}
		for name, s := range secrets {
			if strings.Contains(e.Error(), s) {
				t.Fatalf("%s's error prints %s: %v", what, name, e)
			}
		}
		if !strings.Contains(e.Error(), "withheld") {
			t.Fatalf("%s's error does not say what was withheld: %v", what, e)
		}
		t.Logf("%s: %.160s", what, e.Error())
	}
	for _, line := range logged {
		for name, s := range secrets {
			if strings.Contains(line, s) {
				t.Fatalf("the DERO client logged %s: %s", name, line)
			}
		}
	}
	var rerr *RPCError
	if !errors.As(genErr, &rerr) || rerr.Code != -1 {
		t.Fatalf("the wallet-rpc's error code no longer reaches the caller: %v", genErr)
	}
}
