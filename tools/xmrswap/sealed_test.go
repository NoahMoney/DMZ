package xmrswap

// GAP-019: the sealed rendezvous. A reader of the directory learns nothing of what the parties say, and a writer
// without the swap key cannot make either party read a message of his.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sealedPair(t *testing.T, dir string, key []byte) (bob, alice *SealedChannel, drops *[]string) {
	t.Helper()
	var dropped []string
	mk := func(me, peer string) *SealedChannel {
		d, err := NewDirChannel(dir, me)
		if err != nil {
			t.Fatal(err)
		}
		d.Poll = time.Millisecond
		c, err := NewSealedChannel(d, key, me, peer)
		if err != nil {
			t.Fatal(err)
		}
		c.OnDrop = func(reason string) { dropped = append(dropped, me+": "+reason) }
		return c
	}
	return mk("bob", "alice"), mk("alice", "bob"), &dropped
}

func mustKey(t *testing.T) []byte {
	t.Helper()
	s, err := NewChannelKey()
	if err != nil {
		t.Fatal(err)
	}
	k, err := ParseChannelKey(s)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func recvWithin(t *testing.T, c Channel) (Message, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return c.Recv(ctx)
}

// TestSealedChannelKeepsTheDirectoryBlind: both directions carry their messages, and nothing a message says — its
// kind, the view key in it — is in the file a reader of the directory sees.
func TestSealedChannelKeepsTheDirectoryBlind(t *testing.T) {
	dir := t.TempDir()
	bob, alice, drops := sealedPair(t, dir, mustKey(t))
	ctx := context.Background()
	secretView := strings.Repeat("5e", 32)
	if err := alice.Send(ctx, "bob", Message{Kind: KindAliceSetup, Body: json.RawMessage(`{"v":"` + secretView + `"}`)}); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "bob", "*.json"))
	if len(files) != 1 {
		t.Fatalf("%d files in bob's box", len(files))
	}
	raw, _ := os.ReadFile(files[0])
	if strings.Contains(string(raw), secretView) || strings.Contains(string(raw), KindAliceSetup) {
		t.Fatalf("the file a reader of the directory sees holds the message in the clear: %s", raw)
	}
	m, err := recvWithin(t, bob)
	if err != nil || m.Kind != KindAliceSetup || !strings.Contains(string(m.Body), secretView) {
		t.Fatalf("bob received %+v, %v", m, err)
	}
	if err := bob.Send(ctx, "alice", Message{Kind: KindBobSetup, Body: json.RawMessage(`{"x":1}`)}); err != nil {
		t.Fatal(err)
	}
	if m, err := recvWithin(t, alice); err != nil || m.Kind != KindBobSetup {
		t.Fatalf("alice received %+v, %v", m, err)
	}
	if err := bob.Send(ctx, "carol", Message{Kind: KindGate}); err == nil {
		t.Fatal("a sealed channel sent to a third party")
	}
	if len(*drops) != 0 {
		t.Fatalf("dropped: %v", *drops)
	}
}

// TestSealedChannelRefusesImpersonationAndTampering: a writer who can put files in bob's box but lacks the swap key —
// with a plain message, one sealed under another swap's key, one of alice's altered in its ciphertext or its nonce,
// bob's own message copied back to him, or garbage — gets nothing read. Each is dropped and said, and alice's real
// message behind it is the one bob receives.
func TestSealedChannelRefusesImpersonationAndTampering(t *testing.T) {
	ctx := context.Background()
	genuine := Message{Kind: KindXmrLocked, Body: json.RawMessage(`{"tx":"the-real-one"}`)}
	for _, tc := range []struct {
		name   string
		forge  func(t *testing.T, dir string, key []byte, bob, alice *SealedChannel)
		reason string
	}{
		{"a plain message written into the box", func(t *testing.T, dir string, _ []byte, _, _ *SealedChannel) {
			w, _ := NewDirChannel(dir, "alice")
			_ = w.Send(ctx, "bob", Message{Kind: KindXmrLocked, Body: json.RawMessage(`{"tx":"forged"}`)})
		}, "not sealed"},
		{"a message sealed under another swap's key", func(t *testing.T, dir string, _ []byte, _, _ *SealedChannel) {
			w, _ := NewDirChannel(dir, "alice")
			other, _ := NewSealedChannel(w, mustKey(t), "alice", "bob")
			_ = other.Send(ctx, "bob", Message{Kind: KindXmrLocked, Body: json.RawMessage(`{"tx":"forged"}`)})
		}, "does not open"},
		{"alice's message with its ciphertext altered", func(t *testing.T, dir string, key []byte, _, alice *SealedChannel) {
			_ = alice.Send(ctx, "bob", Message{Kind: KindXmrLocked, Body: json.RawMessage(`{"tx":"forged"}`)})
			rewrite(t, filepath.Join(dir, "bob"), func(b *sealedBody) { b.Box[len(b.Box)/2] ^= 1 })
		}, "does not open"},
		{"alice's message with its nonce altered", func(t *testing.T, dir string, key []byte, _, alice *SealedChannel) {
			_ = alice.Send(ctx, "bob", Message{Kind: KindXmrLocked, Body: json.RawMessage(`{"tx":"forged"}`)})
			rewrite(t, filepath.Join(dir, "bob"), func(b *sealedBody) {
				n, _ := hex.DecodeString(b.Nonce)
				n[0] ^= 1
				b.Nonce = hex.EncodeToString(n)
			})
		}, "does not open"},
		{"bob's own message copied back into his box", func(t *testing.T, dir string, key []byte, bob, _ *SealedChannel) {
			_ = bob.Send(ctx, "alice", Message{Kind: KindXmrLocked, Body: json.RawMessage(`{"tx":"forged"}`)})
			files, _ := filepath.Glob(filepath.Join(dir, "alice", "*.json"))
			if len(files) != 1 {
				t.Fatalf("%d files in alice's box", len(files))
			}
			if err := os.Rename(files[0], filepath.Join(dir, "bob", "00000000000000000000-000000-alice-reflected.json")); err != nil {
				t.Fatal(err)
			}
		}, "does not open"},
		{"garbage in a sealed envelope", func(t *testing.T, dir string, _ []byte, _, _ *SealedChannel) {
			w, _ := NewDirChannel(dir, "alice")
			_ = w.Send(ctx, "bob", Message{Kind: KindSealed, Body: json.RawMessage(`{"n":"zz","c":"AAAA"}`)})
		}, "malformed nonce"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			key := mustKey(t)
			bob, alice, drops := sealedPair(t, dir, key)
			tc.forge(t, dir, key, bob, alice)
			time.Sleep(2 * time.Millisecond) // the genuine message sorts after the forgery
			if err := alice.Send(ctx, "bob", genuine); err != nil {
				t.Fatal(err)
			}
			m, err := recvWithin(t, bob)
			if err != nil || m.Kind != genuine.Kind || string(m.Body) != string(genuine.Body) {
				t.Fatalf("bob received %+v (%v), not alice's genuine message", m, err)
			}
			if len(*drops) != 1 || !strings.Contains((*drops)[0], tc.reason) {
				t.Fatalf("drops %v, want one saying %q", *drops, tc.reason)
			}
			if _, err := recvWithin(t, bob); err == nil {
				t.Fatal("the forgery was delivered after all")
			}
		})
	}
}

// rewrite alters the one sealed message in box, in place.
func rewrite(t *testing.T, box string, f func(*sealedBody)) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(box, "*.json"))
	if len(files) != 1 {
		t.Fatalf("%d files in %s", len(files), box)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var m Message
	var b sealedBody
	if err := json.Unmarshal(raw, &m); err != nil || json.Unmarshal(m.Body, &b) != nil {
		t.Fatalf("not a sealed message: %s", raw)
	}
	f(&b)
	m.Body, _ = json.Marshal(b)
	out, _ := json.Marshal(m)
	if err := os.WriteFile(files[0], out, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestASwapRunsOverASealedChannel: the whole happy path with both companions on the sealed channel.
func TestASwapRunsOverASealedChannel(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 211})
	key := mustKey(t)
	for _, role := range []string{"bob", "alice"} {
		peer := map[string]string{"bob": "alice", "alice": "bob"}[role]
		d, err := NewDirChannel(filepath.Join(r.dir, "channel"), role)
		if err != nil {
			t.Fatal(err)
		}
		d.Poll = time.Millisecond
		sc, err := NewSealedChannel(d, key, role, peer)
		if err != nil {
			t.Fatal(err)
		}
		role := role
		spy := &spyChannel{Channel: sc, onSend: func(to string, m Message) error { return r.checkSend(role, to, m) }}
		var sw *Swap
		if role == "bob" {
			sw, err = NewBob(r.config(role), r.loadState(role), newMemDero(r.chain, bobAddr), r.bobW, r.prover, spy)
			r.bob = sw
		} else {
			sw, err = NewAlice(r.config(role), r.loadState(role), newMemDero(r.chain, aliceAddr), r.aliceW, r.prover, spy)
			r.alice = sw
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	r.until(400, r.bothDone, nil)
	if b, a := r.loadState("bob"), r.loadState("alice"); b.Phase != "swept" || a.Phase != "claimed" {
		t.Fatalf("bob %s, alice %s", b.Phase, a.Phase)
	}
}
