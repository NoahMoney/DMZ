package swapstatus

import (
	"bytes"
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestAFeedIsOffUntilAsked: no -status serves nothing and costs nothing; with it, the code is shown once, on the
// terminal given, and every pass's answer is what the app reads; one the contract refuses is not served, and is said.
func TestAFeedIsOffUntilAsked(t *testing.T) {
	var none *Feed
	none.Publish()
	none.Linger(context.Background(), time.Hour)
	none.Close()
	if f, err := Start("", nil, nil, nil); f != nil || err != nil {
		t.Fatalf("-status off: %v %v", f, err)
	}
	if _, err := Start("0.0.0.0:18095", sample, &bytes.Buffer{}, nil); err == nil {
		t.Fatal("a non-loopback -status was started")
	}

	port := freePort(t)
	var term bytes.Buffer
	var logs []string
	p := sample()
	f, err := Start("127.0.0.1:"+strconv.Itoa(port), func() Payload { return p }, &term, func(s string, a ...interface{}) { logs = append(logs, s) })
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if strings.Count(term.String(), f.srv.Code()) != 1 {
		t.Fatalf("the pairing code is not shown exactly once: %q", term.String())
	}
	get := func() int {
		req, _ := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+Path, nil)
		req.Header.Set("Authorization", "Bearer "+f.srv.Code())
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	f.Publish()
	if c := get(); c != 200 {
		t.Fatalf("after a pass: %d", c)
	}
	p.Outcome = "cancelled"
	f.Publish()
	f.Publish()
	if c := get(); c != 503 || len(logs) != 1 {
		t.Fatalf("a refused answer: %d, said %d times", c, len(logs))
	}
	p.Outcome = "refunded"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.Linger(ctx, time.Hour) // returns at once when the program is stopped
	if c := get(); c != 200 {
		t.Fatalf("the final answer: %d", c)
	}
}
