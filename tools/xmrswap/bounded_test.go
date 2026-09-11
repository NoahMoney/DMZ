package xmrswap

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// replyCases are the malformed and oversized answers both RPC clients must refuse, the same way every time: a reply
// past the cap is refused, not truncated; an envelope with neither result nor error, or a null result, is not read
// as zeros; a second JSON value after the first is not ignored; a result of the wrong shape is an error.
func replyCases(cap int) []struct {
	name, want string
	write      func(w http.ResponseWriter)
} {
	fixed := func(s string) func(w http.ResponseWriter) { return func(w http.ResponseWriter) { w.Write([]byte(s)) } }
	return []struct {
		name, want string
		write      func(w http.ResponseWriter)
	}{
		{"a reply past the cap", "longer than", func(w http.ResponseWriter) {
			w.Write([]byte(`{"jsonrpc":"2.0","id":"1","result":{"height":5},"pad":"`))
			chunk := bytes.Repeat([]byte("x"), 1<<20)
			for n := 0; n <= cap>>20; n++ {
				w.Write(chunk)
			}
			w.Write([]byte(`"}`))
		}},
		{"neither a result nor an error", "neither", fixed(`{"jsonrpc":"2.0","id":"1"}`)},
		{"a null result", "neither", fixed(`{"jsonrpc":"2.0","id":"1","result":null}`)},
		{"a second value after the reply", "decode", fixed(`{"jsonrpc":"2.0","id":"1","result":{"height":5}}{"jsonrpc":"2.0","id":"1","result":{"height":6}}`)},
		{"a result of the wrong shape", "result", fixed(`{"jsonrpc":"2.0","id":"1","result":{"height":"five"}}`)},
	}
}

// TestDeroClientRefusesOversizedAndMalformedReplies (bounded input): every daemon and wallet answer is read through
// a size cap, with a timeout, and a malformed one is refused deterministically.
func TestDeroClientRefusesOversizedAndMalformedReplies(t *testing.T) {
	ctx := context.Background()
	for _, c := range replyCases(maxDeroReply) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { c.write(w) }))
		cl := NewDeroRPC(srv.URL, srv.URL, strings.Repeat("ab", 32))
		h, err := cl.Height(ctx)
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: height %d, err %v", c.name, h, err)
		}
	}
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(time.Second) }))
	defer hang.Close()
	cl := NewDeroRPC(hang.URL, hang.URL, strings.Repeat("ab", 32))
	cl.HTTP.Timeout = 100 * time.Millisecond
	start := time.Now()
	if _, err := cl.Height(ctx); err == nil || time.Since(start) > 900*time.Millisecond {
		t.Fatalf("a daemon that never answers: %v after %v", err, time.Since(start))
	}
	// a client built without an HTTP client gets the bounded fallback, never http.DefaultClient
	if fallbackRPCClient == http.DefaultClient || fallbackRPCClient.Timeout == 0 {
		t.Fatal("the fallback client has no timeout")
	}
}

// TestWalletClientRefusesOversizedAndMalformedReplies (bounded input): the same for monero-wallet-rpc.
func TestWalletClientRefusesOversizedAndMalformedReplies(t *testing.T) {
	ctx := context.Background()
	for _, c := range replyCases(maxWalletReply) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { c.write(w) }))
		h, err := NewWalletRPC(srv.URL, "", "").Height(ctx)
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: height %d, err %v", c.name, h, err)
		}
	}
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(time.Second) }))
	defer hang.Close()
	cctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := NewWalletRPC(hang.URL, "", "").Height(cctx); err == nil || time.Since(start) > 900*time.Millisecond {
		t.Fatalf("a wallet that never answers: %v after %v", err, time.Since(start))
	}
}

// TestProverOutputIsBounded: a prover that floods its output is cut off at the cap, and the call fails.
func TestProverOutputIsBounded(t *testing.T) {
	if os.Getenv("XMRSWAP_PROVER_FLOOD") == "1" {
		os.Stdout.Write(bytes.Repeat([]byte("x"), 2*maxProverOutput))
		os.Exit(0)
	}
	t.Setenv("XMRSWAP_PROVER_FLOOD", "1")
	p := NewProverCLI([]string{os.Args[0], "-test.run=^TestProverOutputIsBounded$", "--"})
	if _, _, _, err := p.Keygen(context.Background()); err == nil || !strings.Contains(err.Error(), "longer than") {
		t.Fatalf("a flooding prover: %v", err)
	}
}
