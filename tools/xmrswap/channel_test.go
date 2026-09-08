package xmrswap

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDirChannelOrderAndConsumption: messages arrive in the order they were sent, each exactly once, and an
// empty box makes Recv wait for its context rather than invent anything.
func TestDirChannelOrderAndConsumption(t *testing.T) {
	dir := t.TempDir()
	bob, err := NewDirChannel(dir, "bob")
	if err != nil {
		t.Fatal(err)
	}
	alice, err := NewDirChannel(dir, "alice")
	if err != nil {
		t.Fatal(err)
	}
	alice.Poll, bob.Poll = 5*time.Millisecond, 5*time.Millisecond
	ctx := context.Background()
	for i, k := range []string{KindOffer, KindBobSetup, KindDeroLocked, KindGate} {
		body, _ := json.Marshal(map[string]int{"i": i})
		if err := bob.Send(ctx, "alice", Message{Kind: k, Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	for i, k := range []string{KindOffer, KindBobSetup, KindDeroLocked, KindGate} {
		m, err := alice.Recv(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var b struct{ I int }
		if err := json.Unmarshal(m.Body, &b); err != nil || m.Kind != k || b.I != i {
			t.Fatalf("message %d: got %s %s, want %s {i:%d}", i, m.Kind, m.Body, k, i)
		}
	}
	short, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if _, err := alice.Recv(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("empty box: got %v, want the context's deadline", err)
	}
	// nothing was left behind, and nothing crossed into the other box
	for _, who := range []string{"alice", "bob"} {
		entries, _ := os.ReadDir(filepath.Join(dir, who))
		if len(entries) != 0 {
			t.Fatalf("%s's box still holds %d entries", who, len(entries))
		}
	}
}

// TestDirChannelNoPartialReads: a message is visible only under its final name; a temporary file, a stray
// non-message and a truncated copy never come back as a message or block the ones behind them.
func TestDirChannelNoPartialReads(t *testing.T) {
	dir := t.TempDir()
	alice, err := NewDirChannel(dir, "alice")
	if err != nil {
		t.Fatal(err)
	}
	alice.Poll = 5 * time.Millisecond
	box := filepath.Join(dir, "alice")
	if err := os.WriteFile(filepath.Join(box, ".tmp-00000000000000000001-000001-bob-deadbeef.json"), []byte(`{"kind":"gate","body":{"kb":"x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(box, "00000000000000000000-000000-bob-00000000.json"), []byte(`{"kind":"gate","bo`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(box, "notes.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	bob, _ := NewDirChannel(dir, "bob")
	if err := bob.Send(context.Background(), "alice", Message{Kind: KindDone, Body: json.RawMessage(`{"outcome":"x"}`)}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m, err := alice.Recv(ctx)
	if err != nil || m.Kind != KindDone {
		t.Fatalf("got %+v %v, want the done message past the junk", m, err)
	}
	entries, _ := os.ReadDir(box)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	joined := strings.Join(names, " ")
	if !strings.Contains(joined, ".bad") || !strings.Contains(joined, ".tmp-") || !strings.Contains(joined, "notes.txt") {
		t.Fatalf("junk handling: %v", names)
	}
}

// TestDirChannelRefusesPathPeers: a peer name is a directory name, never a path.
func TestDirChannelRefusesPathPeers(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"", "..", "../x", "a/b", `a\b`, ".hidden"} {
		if _, err := NewDirChannel(dir, bad); err == nil {
			t.Fatalf("peer %q accepted", bad)
		}
	}
	c, err := NewDirChannel(dir, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Send(context.Background(), "../alice", Message{Kind: KindOffer}); err == nil {
		t.Fatal("a path was accepted as a peer")
	}
	if err := c.Send(context.Background(), "alice", Message{}); err == nil {
		t.Fatal("a message without a kind was accepted")
	}
}
