package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	rpc "veil/tools/simrpc"
)

// A read that fails in transport is retried and then succeeds: the signer stays up through a node restart instead of
// exiting and waiting for an operator. Before this, simrpc's helper called log.Fatal, which skipped every defer and
// left the state-file lock for whoever came next.
func TestSignerNodeRetriesAVaultRead(t *testing.T) {
	oldTries, oldBase := nodeRetryTries, nodeRetryBase
	nodeRetryTries, nodeRetryBase = 4, time.Millisecond
	defer func() { nodeRetryTries, nodeRetryBase = oldTries, oldBase }()
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"result": map[string]interface{}{"stringkeys": map[string]interface{}{"v": 3, "sup": 10}}})
	}))
	defer s.Close()
	n := &signerNode{c: &rpc.Client{Daemon: strings.TrimPrefix(s.URL, "http://"), Timeout: time.Second}}
	st, err := n.getSC(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	if st.U("v") != 3 || st.U("sup") != 10 {
		t.Fatalf("state = %+v", st)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
}

func TestSignerNodeReturnsAnErrorAfterTheRetries(t *testing.T) {
	oldTries, oldBase := nodeRetryTries, nodeRetryBase
	nodeRetryTries, nodeRetryBase = 2, time.Millisecond
	defer func() { nodeRetryTries, nodeRetryBase = oldTries, oldBase }()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer s.Close()
	n := &signerNode{c: &rpc.Client{Daemon: strings.TrimPrefix(s.URL, "http://"), Timeout: time.Second}}
	if _, err := n.getSC(strings.Repeat("ab", 32)); err == nil {
		t.Fatal("a permanently failing node was reported as a successful read")
	}
}
