package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestGate puts the gate in front of a stand-in zebrad that records every body the gate forwards, requires the
// upstream cookie, and answers a fixed result.
func newTestGate(t *testing.T) (*gate, *[]string) {
	t.Helper()
	var got []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, string(b))
		if u, p, ok := r.BasicAuth(); !ok || u != "__cookie__" || p != "zebra" {
			http.Error(w, "upstream auth", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"jsonrpc":"1.0","id":1,"result":"ok","error":null}`)
	}))
	t.Cleanup(up.Close)
	cookie := filepath.Join(t.TempDir(), ".cookie")
	if err := os.WriteFile(cookie, []byte("__cookie__:zebra\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &gate{allowed: parseAllow(zecswapMethods), allow: zecswapMethods, want: "__cookie__:client",
		upstream: up.URL, upCookie: cookie, client: up.Client()}, &got
}

func post(g *gate, body string, auth bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	if auth {
		req.SetBasicAuth("__cookie__", "client")
	}
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	return rec
}

// An allowed call reaches zebrad with its own cookie, rebuilt from the checked fields: the method, id and params the
// client sent, and nothing else.
func TestAnAllowedCallIsForwardedRebuilt(t *testing.T) {
	g, got := newTestGate(t)
	rec := post(g, `{"jsonrpc":"1.0","id":7,"method":"getblockhash","params":[0],"extra":"dropped"}`, true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Fatalf("the allowed call did not come back: %d %s", rec.Code, rec.Body)
	}
	if len(*got) != 1 {
		t.Fatalf("%d forwards, want 1", len(*got))
	}
	var fwd map[string]json.RawMessage
	if err := json.Unmarshal([]byte((*got)[0]), &fwd); err != nil {
		t.Fatal(err)
	}
	if string(fwd["method"]) != `"getblockhash"` || string(fwd["id"]) != "7" || string(fwd["params"]) != "[0]" || fwd["extra"] != nil {
		t.Fatalf("forwarded %s", (*got)[0])
	}
}

// A body naming "method" twice passes the check on one value; what zebrad receives must name that value and no other.
func TestADuplicatedMethodCannotSmuggleAnother(t *testing.T) {
	g, got := newTestGate(t)
	post(g, `{"jsonrpc":"1.0","id":1,"method":"stop","method":"getblockhash","params":[0]}`, true)
	if len(*got) != 1 {
		t.Fatalf("%d forwards, want 1", len(*got))
	}
	if f := (*got)[0]; strings.Contains(f, "stop") || strings.Count(f, `"method"`) != 1 {
		t.Fatalf("the gate forwarded a body carrying a method it did not check: %s", f)
	}
}

// Every refusal is answered by the gate itself, and nothing refused reaches zebrad.
func TestRefusalsNeverReachZebrad(t *testing.T) {
	g, got := newTestGate(t)
	for _, c := range []struct {
		name, body string
		auth       bool
		code       int
		errCode    string
	}{
		{"no cookie", `{"id":1,"method":"getblockhash","params":[0]}`, false, http.StatusUnauthorized, ""},
		{"a method not served", `{"id":1,"method":"stop"}`, true, http.StatusOK, "-32601"},
		{"a batch", `[{"id":1,"method":"getblockhash","params":[0]}]`, true, http.StatusOK, "-32600"},
		{"not JSON", `not json`, true, http.StatusOK, "-32700"},
		{"too large", `{"method":"sendrawtransaction","params":["` + strings.Repeat("a", maxRequest) + `"]}`, true, http.StatusRequestEntityTooLarge, ""},
	} {
		rec := post(g, c.body, c.auth)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.errCode) {
			t.Errorf("%s: HTTP %d %q, want HTTP %d with %q", c.name, rec.Code, rec.Body.String(), c.code, c.errCode)
		}
	}
	get := httptest.NewRecorder()
	g.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/", nil))
	if get.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: HTTP %d, want 405", get.Code)
	}
	if len(*got) != 0 {
		t.Fatalf("refused requests reached zebrad: %v", *got)
	}
}
