package coord

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestRelay(t *testing.T, f *fleet, mod func(*RelayConfig)) (*Relay, *httptest.Server) {
	t.Helper()
	cfg := RelayConfig{Groups: []RelayGroup{{Vault: mustVault(t, f.vault), Signers: f.pubs}}, Now: f.clock.Now}
	if mod != nil {
		mod(&cfg)
	}
	r, err := NewRelay(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(func() { srv.Close(); r.Close() })
	return r, srv
}

func httpStore(t *testing.T, url string) *HTTPStore {
	t.Helper()
	s, err := NewHTTPStore(url, 5*time.Second, 0)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func putRaw(t *testing.T, url string, body []byte) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, url+"/v1/m", bytes.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestThreeSignersThroughAnHTTPRelay(t *testing.T) {
	f := newFleet(t, 3)
	_, srv := newTestRelay(t, f, nil)
	nodes := []*Node{f.node(t, 0, httpStore(t, srv.URL)), f.node(t, 1, httpStore(t, srv.URL)), f.node(t, 2, httpStore(t, srv.URL+"/"))}
	round := "zec/release/11/sig/3"
	want := map[string][]byte{}
	for i, n := range nodes {
		mustPut(t, n, round, payloadOf(f.id(i), round))
		want[f.id(i)] = payloadOf(f.id(i), round)
	}
	for _, n := range nodes {
		if got := mustGet(t, n, round); !sameMap(got, want) {
			t.Fatalf("%s sees %s", n.Self(), showMap(got))
		}
		if err := n.Health(ctx); err != nil {
			t.Fatal(err)
		}
		n.Close()
	}
}

func TestTheRelayRefusesWhatItShouldNotStore(t *testing.T) {
	f := newFleet(t, 3)
	relay, srv := newTestRelay(t, f, func(c *RelayConfig) { c.MaxPayload = 4096 })
	vault := mustVault(t, f.vault)
	now := f.clock.Now().UnixNano()
	hour := int64(time.Hour)
	mk := func(id *Identity, v [32]byte, created, expires int64, payload []byte) []byte {
		raw, err := seal(id, v, "r", payload, f.pubs, created, expires)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	good := mk(f.ids[0], vault, now, now+hour, []byte("p"))
	altered := bytes.Clone(good)
	altered[len(altered)-sigLen-5] ^= 1
	outsider := newIdentity(t, testAddr(90))
	imp := &Identity{pub: PublicIdentity{ID: f.id(1), Sign: f.pubs[0].Sign, KEM: f.pubs[0].KEM}, sign: f.ids[0].sign, kem: f.ids[0].kem}
	cases := []struct {
		name string
		body []byte
		want int
	}{
		{"malformed", []byte("hello"), http.StatusBadRequest},
		{"too large", make([]byte, maxMessage(4096)+1), http.StatusRequestEntityTooLarge},
		{"payload over the relay's limit", mk(f.ids[0], vault, now, now+hour, make([]byte, 5000)), http.StatusRequestEntityTooLarge},
		{"another vault", mk(f.ids[0], mustVault(t, otherVaultHex), now, now+hour, []byte("p")), http.StatusForbidden},
		{"unpinned sender", mk(outsider, vault, now, now+hour, []byte("p")), http.StatusForbidden},
		{"sender naming another signer", mk(imp, vault, now, now+hour, []byte("p")), http.StatusForbidden},
		{"bad signature", altered, http.StatusForbidden},
		{"expired", mk(f.ids[0], vault, now-2*hour, now-hour, []byte("p")), http.StatusGone},
		{"dated in the future", mk(f.ids[0], vault, now+hour, now+2*hour, []byte("p")), http.StatusGone},
		{"living too long", mk(f.ids[0], vault, now, now+int64(8*24*time.Hour), []byte("p")), http.StatusGone},
		{"good", good, http.StatusNoContent},
	}
	for _, c := range cases {
		if got := putRaw(t, srv.URL, c.body); got != c.want {
			t.Errorf("%s: status %d, want %d", c.name, got, c.want)
		}
	}
	if n, _ := relay.Stats(); n != 1 {
		t.Fatalf("the relay holds %d messages, want only the good one", n)
	}
	for path, want := range map[string]int{
		"/v1/m/" + otherVaultHex + "/" + strings.Repeat("00", 32): http.StatusNotFound,
		"/v1/m/zz/yy": http.StatusBadRequest,
		"/v1/m/" + testVaultHex + "/" + strings.Repeat("00", 32) + "/x": http.StatusBadRequest,
		"/elsewhere": http.StatusNotFound,
		"/v1/health": http.StatusOK,
	} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s: %d, want %d", path, resp.StatusCode, want)
		}
	}
	resp, _ := http.Post(srv.URL+"/v1/m", "application/octet-stream", bytes.NewReader(good))
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /v1/m: %d", resp.StatusCode)
	}
}

func TestTheRelayKeepsTheNewestAndBoundsEachSender(t *testing.T) {
	f := newFleet(t, 3)
	_, srv := newTestRelay(t, f, func(c *RelayConfig) { c.MaxRoundsPerSender = 2 })
	a := f.node(t, 0, httpStore(t, srv.URL))
	mustPut(t, a, "r", []byte("v1"))
	vault := mustVault(t, f.vault)
	v1 := only(t, httpStore(t, srv.URL), vault, "r")
	f.clock.Advance(time.Second)
	mustPut(t, a, "r", []byte("v2"))
	if got := putRaw(t, srv.URL, v1); got != http.StatusNoContent { // a replay of the older copy is accepted and ignored
		t.Fatalf("replaying the older copy: %d", got)
	}
	b := f.node(t, 1, httpStore(t, srv.URL))
	if got := mustGet(t, b, "r"); string(got[f.id(0)]) != "v2" {
		t.Fatalf("the relay serves %q after a replay of the older copy", got[f.id(0)])
	}
	mustPut(t, a, "r2", []byte("x"))
	if err := a.Put(ctx, "r3", []byte("x")); err == nil || !strings.Contains(err.Error(), "507") && !strings.Contains(err.Error(), "no store") {
		t.Fatalf("a third round from one sender was stored past its quota: %v", err)
	}
	mustPut(t, b, "r3", []byte("others are unaffected"))
}

func TestAClientRefusesAHostileRelay(t *testing.T) {
	var frames []byte
	var hdr [4]byte
	for i := 0; i < 100; i++ {
		binary.BigEndian.PutUint32(hdr[:], 1)
		frames = append(frames, hdr[:]...)
		frames = append(frames, 'x')
	}
	bodies := map[string][]byte{
		"a huge frame":           {0xff, 0xff, 0xff, 0xff, 1, 2, 3},
		"a zero frame":           {0, 0, 0, 0},
		"too many frames":        frames,
		"a truncated frame":      {0, 0, 0, 9, 1, 2},
		"a truncated header":     {0, 0},
		"a frame over the limit": append([]byte{0, 0, 0x10, 0}, make([]byte, 0x1000)...),
	}
	var vault, tag [32]byte
	for name, body := range bodies {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(body) }))
		if _, err := httpStore(t, srv.URL).Get(ctx, vault, tag, 10, 2048); err == nil {
			t.Errorf("%s: accepted", name)
		}
		srv.Close()
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(nil) }))
	defer target.Close()
	redirect := httptest.NewServer(http.RedirectHandler(target.URL+"/v1/m/x", http.StatusFound))
	defer redirect.Close()
	if _, err := httpStore(t, redirect.URL).Get(ctx, vault, tag, 10, 2048); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("a redirect was followed: %v", err)
	}
	for _, bad := range []string{"ftp://h", "http://", "http://user:pw@h", "http://h?q=1", "http://h#f", "h:80"} {
		if _, err := NewHTTPStore(bad, time.Second, 0); err == nil {
			t.Errorf("relay URL %q accepted", bad)
		}
	}
}

func TestHealthReportsAClockThatDisagreesWithTheRelay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Date", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat))
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	defer srv.Close()
	s, err := NewHTTPStore(srv.URL, time.Second, DefaultMaxSkew)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Health(ctx); err == nil || !strings.Contains(err.Error(), "clock") {
		t.Fatalf("a relay an hour off: %v", err)
	}
}

func TestRelayConfigIsChecked(t *testing.T) {
	f := newFleet(t, 3)
	v := mustVault(t, f.vault)
	bad := map[string]RelayConfig{
		"no groups":     {},
		"no signers":    {Groups: []RelayGroup{{Vault: v}}},
		"vault twice":   {Groups: []RelayGroup{{Vault: v, Signers: f.pubs}, {Vault: v, Signers: f.pubs}}},
		"key twice":     {Groups: []RelayGroup{{Vault: v, Signers: []PublicIdentity{f.pubs[0], f.pubs[0]}}}},
		"payload huge":  {Groups: []RelayGroup{{Vault: v, Signers: f.pubs}}, MaxPayload: HardMaxPayload + 1},
		"space too low": {Groups: []RelayGroup{{Vault: v, Signers: f.pubs}}, MaxTotalBytes: 1 << 20},
	}
	for name, c := range bad {
		if r, err := NewRelay(c); err == nil {
			r.Close()
			t.Errorf("%s: accepted", name)
		}
	}
	_ = hex.EncodeToString
}
