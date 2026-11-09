package zcashtx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadsRetryTransientFailures(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"result": "00" + strings.Repeat("ab", 31)})
	}))
	defer s.Close()
	c := NewZcashRPC(s.URL, "", "")
	c.Retries = 2
	c.RetryDelay = time.Millisecond
	var got string
	if err := c.call(context.Background(), "getblockhash", []interface{}{1}, &got); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
}

func TestSendRawTransactionIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "temporary", http.StatusServiceUnavailable)
	}))
	defer s.Close()
	c := NewZcashRPC(s.URL, "", "")
	c.Retries = 3
	c.RetryDelay = time.Millisecond
	if _, err := c.SendRawTx(context.Background(), []byte{1}); err == nil {
		t.Fatal("the broadcast failure was hidden")
	}
	if calls.Load() != 1 {
		t.Fatalf("broadcast attempts = %d, want 1", calls.Load())
	}
}

func TestRPCErrorsAreNotRetried(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"code": -8, "message": "invalid parameter"}})
	}))
	defer s.Close()
	c := NewZcashRPC(s.URL, "", "")
	c.Retries = 3
	c.RetryDelay = time.Millisecond
	var got string
	if err := c.call(context.Background(), "getblockhash", []interface{}{1}, &got); err == nil {
		t.Fatal("the node's error was swallowed")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}
