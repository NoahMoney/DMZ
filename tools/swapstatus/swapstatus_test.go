package swapstatus

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func sample() Payload {
	return Payload{V: 1, Program: "xmrswap", Role: "taker", Direction: "deposit", Network: "simulator", Contract: strings.Repeat("c", 64),
		Lock: U(3), Asset: "", DeroAmount: "2000000", ForeignAmount: "1000000000000", Fee: "5000000000", Phase: "xmr_locked",
		Updated: time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
		Steps:   []Step{{ID: "dero_lock", Label: "The maker locks 20 DERO for you", Chain: DERO, State: Done, Conf: U(12), Need: U(8)}}}
}

// freePort is a loopback port nothing listens on, found by binding port 0 and letting it go.
func freePort(t *testing.T) int {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return p
}

// TestTheStatusIsServedOnLoopbackOnly: every -status that is not exactly 127.0.0.1:PORT is refused before anything is
// bound — another interface, every name (localhost included, which a hosts file can point elsewhere), IPv6, a port the
// app cannot pair with, no port.
func TestTheStatusIsServedOnLoopbackOnly(t *testing.T) {
	for _, a := range []string{"0.0.0.0:18095", "localhost:18095", "[::1]:18095", "::1:18095", "192.168.1.5:18095", "10.0.0.1:18095", "example.com:18095",
		"127.0.0.2:18095", "127.0.0.1", "127.0.0.1:0", "127.0.0.1:80", "127.0.0.1:65536", "127.0.0.1:+18095", "127.0.0.1:018095", ":18095", ""} {
		if _, err := ParseAddr(a); err == nil {
			t.Errorf("-status %q was accepted", a)
		}
		if s, err := Listen(a); err == nil {
			s.Close()
			t.Errorf("-status %q was bound", a)
		}
	}
	if p, err := ParseAddr("127.0.0.1:18095"); err != nil || p != 18095 {
		t.Fatalf("127.0.0.1:18095: %d, %v", p, err)
	}
}

func TestThePairingCodeIsFreshAndHex(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		c, err := NewCode()
		if err != nil || len(c) != 32 || strings.Trim(c, "0123456789abcdef") != "" || seen[c] {
			t.Fatalf("code %q, %v", c, err)
		}
		seen[c] = true
	}
}

func do(t *testing.T, method, url, host string, hdr map[string]string) (*http.Response, string) {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

// TestTheServerRefusesEverythingButThePairedApp is §3's order against a real listener: a foreign Host (DNS rebinding)
// gets 403, the preflight 204 with CORS and no data, a missing or wrong code 401, another method or path 404 — every
// one with no body — and only the code gets the answer.
func TestTheServerRefusesEverythingButThePairedApp(t *testing.T) {
	port := freePort(t)
	s, err := Listen("127.0.0.1:" + strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	url := "http://127.0.0.1:" + strconv.Itoa(port) + Path
	bearer := map[string]string{"Authorization": "Bearer " + s.Code()}
	if !strings.Contains(s.Announcement(), s.Code()) || !strings.Contains(s.Announcement(), strconv.Itoa(port)) {
		t.Fatalf("the announcement does not give the port and the code: %s", s.Announcement())
	}

	// before the companion has published anything: the code is right and there is nothing to serve
	if res, body := do(t, "GET", url, "", bearer); res.StatusCode != 503 || body != "" {
		t.Fatalf("before any answer: %d %q", res.StatusCode, body)
	}
	if err := s.Publish(sample()); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name, method, url, host string
		hdr                     map[string]string
		want                    int
	}{
		{"a rebinding page's own name as the Host", "GET", url, "evil.example:" + strconv.Itoa(port), bearer, 403},
		{"the right name on another port", "GET", url, "127.0.0.1:1", bearer, 403},
		{"a Host with no port", "GET", url, "127.0.0.1", bearer, 403},
		{"no code", "GET", url, "", nil, 401},
		{"a wrong code", "GET", url, "", map[string]string{"Authorization": "Bearer " + strings.Repeat("0", 32)}, 401},
		{"the code without Bearer", "GET", url, "", map[string]string{"Authorization": s.Code()}, 401},
		{"the code and one more character", "GET", url, "", map[string]string{"Authorization": "Bearer " + s.Code() + "0"}, 401},
		{"the code in lower-case bearer", "GET", url, "", map[string]string{"Authorization": "bearer " + s.Code()}, 401},
		{"a POST", "POST", url, "", bearer, 404},
		{"another path", "GET", strings.TrimSuffix(url, Path) + "/v1/secret", "", bearer, 404},
		{"a query", "GET", url + "?x=1", "", bearer, 404},
	} {
		res, body := do(t, c.method, c.url, c.host, c.hdr)
		if res.StatusCode != c.want || body != "" {
			t.Errorf("%s: %d %q, want %d and no body", c.name, res.StatusCode, body, c.want)
		}
		if c.want == 403 && res.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("%s: a foreign Host was given CORS headers", c.name)
		}
	}

	// the preflight: CORS for the Authorization header, Private Network Access when asked, and no data without a code
	res, body := do(t, "OPTIONS", url, "", map[string]string{"Origin": "https://evil.example", "Access-Control-Request-Method": "GET",
		"Access-Control-Request-Headers": "authorization", "Access-Control-Request-Private-Network": "true"})
	if res.StatusCode != 204 || body != "" || res.Header.Get("Access-Control-Allow-Origin") != "*" || res.Header.Get("Access-Control-Allow-Methods") != "GET" ||
		res.Header.Get("Access-Control-Allow-Headers") != "Authorization" || res.Header.Get("Access-Control-Max-Age") != "600" ||
		res.Header.Get("Access-Control-Allow-Private-Network") != "true" {
		t.Fatalf("preflight: %d %q %v", res.StatusCode, body, res.Header)
	}
	if res, _ := do(t, "OPTIONS", url, "evil.example", nil); res.StatusCode != 403 {
		t.Fatalf("a preflight from a rebinding page: %d", res.StatusCode)
	}

	// localhost:PORT is the other Host the app may send; the answer is the payload, uncached, JSON
	for _, host := range []string{"", "localhost:" + strconv.Itoa(port)} {
		res, body := do(t, "GET", url, host, bearer)
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/json" || res.Header.Get("Cache-Control") != "no-store" ||
			res.Header.Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("the paired answer (Host %q): %d %v", host, res.StatusCode, res.Header)
		}
		var got Payload
		if err := json.Unmarshal([]byte(body), &got); err != nil || got.Program != "xmrswap" || got.Steps[0].ID != "dero_lock" {
			t.Fatalf("the answer: %v %s", err, body)
		}
		if strings.Contains(body, s.Code()) {
			t.Fatal("the answer carries the pairing code")
		}
	}

	// an answer that breaks the contract is refused, and the last good one is no longer served as current
	bad := sample()
	bad.Outcome = "cancelled"
	if err := s.Publish(bad); err == nil {
		t.Fatal("an outcome outside the contract was published")
	}
	if res, _ := do(t, "GET", url, "", bearer); res.StatusCode != 503 {
		t.Fatalf("after a refused answer: %d", res.StatusCode)
	}
}

// TestValidateIsTheAppsRules: each value the app refuses, the companion refuses to serve.
func TestValidateIsTheAppsRules(t *testing.T) {
	if err := sample().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, f := range map[string]func(*Payload){
		"version 2":                   func(p *Payload) { p.V = 2 },
		"another program":             func(p *Payload) { p.Program = "btcswap" },
		"a maker making a deposit":    func(p *Payload) { p.Role, p.Direction = "maker", "deposit" },
		"a taker making a withdrawal": func(p *Payload) { p.Direction = "withdrawal" },
		"an unknown network":          func(p *Payload) { p.Network = "regtest" },
		"a short contract":            func(p *Payload) { p.Contract = "cc" },
		"an upper-case asset":         func(p *Payload) { p.Asset = strings.Repeat("A", 64) },
		"an amount in a float":        func(p *Payload) { p.DeroAmount = "1.5" },
		"a negative fee":              func(p *Payload) { p.Fee = "-1" },
		"an unknown outcome":          func(p *Payload) { p.Outcome = "cancelled" },
		"no time":                     func(p *Payload) { p.Updated = "" },
		"33 steps":                    func(p *Payload) { p.Steps = make([]Step, 33) },
		"an unknown chain":            func(p *Payload) { p.Steps[0].Chain = "Bitcoin" },
		"an unknown state":            func(p *Payload) { p.Steps[0].State = "pending" },
		"a txid that is not hex":      func(p *Payload) { p.Steps[0].TxID = "(claimed; txid not recorded)" },
		"a deadline on no chain":      func(p *Payload) { p.Steps[0].Deadline = &Deadline{Chain: "?", Height: 1} },
	} {
		p := sample()
		p.Steps = append([]Step(nil), p.Steps...)
		f(&p)
		if err := p.Validate(); err == nil {
			t.Errorf("%s was valid", name)
		}
	}
	for _, k := range []string{"secret", "preimage", "privateKey", "seed", "mnemonic", "spendKey", "view_key", "channelKey", "pairingCode"} {
		if got := SecretField([]byte(`{"steps":[{"x":{"` + k + `":1}}]}`)); got != k {
			t.Errorf("the field %q was not found: %q", k, got)
		}
	}
	b, _ := json.Marshal(sample())
	if k := SecretField(b); k != "" {
		t.Fatalf("the payload's own fields read as a secret's: %q", k)
	}
}

func TestUnitsAndCounts(t *testing.T) {
	for _, c := range []struct {
		v, d uint64
		want string
	}{{2000000, 5, "20"}, {150000, 5, "1.5"}, {1, 12, "0.000000000001"}, {1000000000000, 12, "1"}, {50000000, 8, "0.5"}, {7, 0, "7"}, {0, 5, "0"}} {
		if got := Units(c.v, c.d); got != c.want {
			t.Errorf("Units(%d, %d) = %q, want %q", c.v, c.d, got, c.want)
		}
	}
	if Depth(0, 5) != nil || Depth(5, 0) != nil || *Depth(10, 4) != 6 || *Depth(3, 4) != 0 {
		t.Fatal("Depth")
	}
	if *Confirmations(10, 10) != 1 || Confirmations(0, 1) != nil {
		t.Fatal("Confirmations")
	}
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	if d := At(DERO, 110, 100, true, now); d.ETA == nil || *d.ETA != "2026-09-13T12:03:00Z" {
		t.Fatalf("At: %+v", d)
	}
	if d := At(Zcash, 110, 100, false, now); d.ETA != nil {
		t.Fatal("an estimate on a chain mined on demand")
	}
	if d := At(DERO, 110, 0, true, now); d.ETA != nil {
		t.Fatal("an estimate with no tip")
	}
}
