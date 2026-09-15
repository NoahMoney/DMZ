package coord

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestThreeSignersSeeEachOther(t *testing.T) {
	f := newFleet(t, 3)
	relay := f.mem("relay")
	nodes := []*Node{f.node(t, 0, relay), f.node(t, 1, relay), f.node(t, 2, relay)}
	round := "zec/release/7/sig/0"
	want := map[string][]byte{}
	for i, n := range nodes {
		mustPut(t, n, round, payloadOf(f.id(i), round))
		want[f.id(i)] = payloadOf(f.id(i), round)
	}
	for _, n := range nodes {
		if got := mustGet(t, n, round); !sameMap(got, want) {
			t.Fatalf("%s sees %s", n.Self(), showMap(got))
		}
	}
	// the caller owns what Get returns
	got := mustGet(t, nodes[0], round)
	got[f.id(1)][0] ^= 0xff
	if again := mustGet(t, nodes[0], round); !sameMap(again, want) {
		t.Fatal("changing a returned payload changed the node's copy")
	}
	if _, err := nodes[0].Get(ctx, "other/round"); err != nil {
		t.Fatal(err)
	}
}

func TestForgedMessagesAreRefused(t *testing.T) {
	f := newFleet(t, 3)
	relay := f.mem("relay")
	vault := mustVault(t, f.vault)
	round := "xmr/info/1"
	tag := roundTag(vault, round)
	a := f.node(t, 0, relay)
	mustPut(t, a, round, payloadOf(f.id(0), round))
	now := f.clock.Now().UnixNano()
	exp := now + int64(time.Hour)

	// an outsider with its own key, encrypting to the real signer set
	out := newIdentity(t, testAddr(99))
	raw, err := seal(out, vault, round, []byte("SECRET-PAYLOAD-MARKER forged by an outsider"), f.pubs, now, exp)
	if err != nil {
		t.Fatal(err)
	}
	var outKey [32]byte
	copy(outKey[:], out.pub.Sign)
	if err := relay.Put(ctx, Stored{Vault: vault, Tag: tag, Sender: outKey, Created: now, Expires: exp, Raw: raw}); err != nil {
		t.Fatal(err)
	}
	// an outsider claiming signer C's key and address, signing with its own key
	imp := &Identity{pub: PublicIdentity{ID: f.id(2), Sign: f.pubs[2].Sign, KEM: out.pub.KEM}, sign: out.sign, kem: out.kem}
	raw2, err := seal(imp, vault, round, []byte("SECRET-PAYLOAD-MARKER forged as C"), f.pubs, now, exp)
	if err != nil {
		t.Fatal(err)
	}
	if err := relay.Put(ctx, Stored{Vault: vault, Tag: tag, Sender: f.key(2), Created: now, Expires: exp, Raw: raw2}); err != nil {
		t.Fatal(err)
	}
	// a relay that alters A's genuine message in flight
	alter := &hookStore{name: "altering relay", inner: relay, onGet: func(raws [][]byte) [][]byte {
		var out [][]byte
		for _, r := range raws {
			x := bytes.Clone(r)
			x[len(x)-sigLen-20] ^= 0x01
			out = append(out, x)
		}
		return out
	}}
	b := f.node(t, 1, relay, alter)
	want := map[string][]byte{f.id(0): payloadOf(f.id(0), round)}
	if got := mustGet(t, b, round); !sameMap(got, want) {
		t.Fatalf("B sees %s, want only A's genuine payload", showMap(got))
	}
	c := f.node(t, 2, alter) // reaches only the altering relay: it gets nothing, never something wrong
	if got := mustGet(t, c, round); len(got) != 0 {
		t.Fatalf("C, behind an altering relay, sees %s", showMap(got))
	}
	d := b.Status().Dropped
	if d["signed by a key no signer pinned"] == 0 || d["bad signature"] < 2 {
		t.Fatalf("drop reasons %v", d)
	}
}

func TestReplayIntoAnotherRoundOrVaultIsRefused(t *testing.T) {
	f := newFleet(t, 3)
	relay := f.mem("relay")
	vault := mustVault(t, f.vault)
	r1, r2 := "zec/release/1/sig/0", "zec/release/1/sig/1"
	a := f.node(t, 0, relay)
	mustPut(t, a, r1, payloadOf(f.id(0), r1))
	raw := only(t, relay, vault, r1)
	// the relay files A's round-1 message under round 2
	if err := relay.Put(ctx, storedOf(t, raw, roundTag(vault, r2))); err != nil {
		t.Fatal(err)
	}
	b := f.node(t, 1, relay)
	if got := mustGet(t, b, r2); len(got) != 0 {
		t.Fatalf("a round-1 message was accepted for round 2: %s", showMap(got))
	}
	if b.Status().Dropped["for another round"] == 0 {
		t.Fatalf("drops %v", b.Status().Dropped)
	}
	// the same signers, the same keys, a second vault: the relay files A's vault-1 message under vault 2
	v2 := mustVault(t, otherVaultHex)
	st := storedOf(t, raw, roundTag(v2, r1))
	st.Vault = v2
	if err := relay.Put(ctx, st); err != nil {
		t.Fatal(err)
	}
	b2 := f.nodeWith(t, 1, func(c *Config) { c.Vault = otherVaultHex }, relay)
	if got := mustGet(t, b2, r1); len(got) != 0 {
		t.Fatalf("a vault-1 message was accepted for vault 2: %s", showMap(got))
	}
	if b2.Status().Dropped["for another vault"] == 0 {
		t.Fatalf("drops %v", b2.Status().Dropped)
	}
	// and the genuine one is still there for round 1 of vault 1
	if got := mustGet(t, b, r1); !bytes.Equal(got[f.id(0)], payloadOf(f.id(0), r1)) {
		t.Fatal("the genuine message was lost")
	}
}

func TestReorderedCopiesConvergeOnTheNewest(t *testing.T) {
	f := newFleet(t, 3)
	relay := f.mem("relay")
	vault := mustVault(t, f.vault)
	round := "xmr/txset/5/aa"
	a := f.node(t, 0, relay)
	mustPut(t, a, round, []byte("v1"))
	v1 := only(t, relay, vault, round)
	f.clock.Advance(time.Second)
	mustPut(t, a, round, []byte("v2"))
	v2 := only(t, relay, vault, round)
	seq := func(steps ...[][]byte) *hookStore {
		i := 0
		return &hookStore{name: fmt.Sprintf("replaying relay %p", &i), onGet: func([][]byte) [][]byte {
			s := steps[min(i, len(steps)-1)]
			i++
			return s
		}}
	}
	b := f.node(t, 1, seq([][]byte{v2}, [][]byte{v1}, [][]byte{v1, v2}))
	for i := 0; i < 3; i++ {
		if got := mustGet(t, b, round); string(got[f.id(0)]) != "v2" {
			t.Fatalf("poll %d: B rolled back to %q", i, got[f.id(0)])
		}
	}
	c := f.node(t, 2, seq([][]byte{v1}, [][]byte{v1, v2}))
	if got := mustGet(t, c, round); string(got[f.id(0)]) != "v1" {
		t.Fatalf("C first sees %q", got[f.id(0)])
	}
	if got := mustGet(t, c, round); string(got[f.id(0)]) != "v2" {
		t.Fatalf("C does not move on to the newer copy: %q", got[f.id(0)])
	}
}

func TestTruncatedAndOversizedMessagesAreRefused(t *testing.T) {
	f := newFleet(t, 3)
	relay := f.mem("relay")
	vault := mustVault(t, f.vault)
	round := "zec/release/2/sig/0"
	a := f.node(t, 0, relay)
	mustPut(t, a, round, payloadOf(f.id(0), round))
	raw := only(t, relay, vault, round)
	cut := func(n int) *hookStore {
		return &hookStore{name: fmt.Sprintf("truncating relay %d", n), onGet: func([][]byte) [][]byte { return [][]byte{raw[:n]} }}
	}
	for _, n := range []int{0, 1, 5, 100, len(raw) / 2, len(raw) - sigLen, len(raw) - 1} {
		b := f.node(t, 1, cut(n))
		if got := mustGet(t, b, round); len(got) != 0 {
			t.Fatalf("a message cut to %d bytes was accepted", n)
		}
	}
	b := f.node(t, 1, cut(len(raw)/2), relay) // one honest store is enough
	if got := mustGet(t, b, round); !bytes.Equal(got[f.id(0)], payloadOf(f.id(0), round)) {
		t.Fatal("the honest copy was not taken")
	}

	small := func(c *Config) { c.MaxPayload = 1024 }
	s := f.nodeWith(t, 2, small, relay)
	if err := s.Put(ctx, round, make([]byte, 1025)); err == nil {
		t.Fatal("a payload over the limit was put")
	}
	if err := s.Put(ctx, round, make([]byte, 1024)); err != nil {
		t.Fatal(err)
	}
	big := f.nodeWith(t, 0, func(c *Config) { c.MaxPayload = 1 << 20 }, relay)
	mustPut(t, big, "big", make([]byte, 100<<10))
	bigRaw := only(t, relay, vault, "big")
	inject := &hookStore{name: "oversize relay", onGet: func([][]byte) [][]byte { return [][]byte{bigRaw} }}
	s2 := f.nodeWith(t, 2, small, inject)
	if got := mustGet(t, s2, "big"); len(got) != 0 {
		t.Fatal("a message over the receiver's limit was accepted")
	}
	if s2.Status().Dropped[errTooLarge.Error()] == 0 {
		t.Fatalf("drops %v", s2.Status().Dropped)
	}
}

func TestExpiredAndFutureMessagesAreRefused(t *testing.T) {
	f := newFleet(t, 3)
	relay := f.mem("relay")
	vault := mustVault(t, f.vault)
	round := "xmr/info/9"
	a := f.node(t, 0, relay)
	mustPut(t, a, round, payloadOf(f.id(0), round))
	raw := only(t, relay, vault, round)
	replay := &hookStore{name: "replaying relay", onGet: func([][]byte) [][]byte { return [][]byte{raw} }}
	f.clock.Advance(DefaultTTL + time.Minute)
	b := f.node(t, 1, replay)
	if got := mustGet(t, b, round); len(got) != 0 {
		t.Fatal("an expired message was accepted")
	}
	if b.Status().Dropped["expired"] == 0 {
		t.Fatalf("drops %v", b.Status().Dropped)
	}
	// a sender whose clock runs 10 minutes ahead: the relay refuses its message, and a replay of it is refused too
	ahead := &fakeClock{t: f.clock.Now().Add(10 * time.Minute)}
	fast := f.nodeWith(t, 0, func(c *Config) { c.Now = ahead.Now }, relay)
	if err := fast.Put(ctx, round, []byte("from the future")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("the relay took a message dated 10 minutes ahead: %v", err)
	}
	now := ahead.Now().UnixNano()
	future, _ := seal(f.ids[0], vault, round, []byte("from the future"), f.pubs, now, now+int64(time.Hour))
	replay2 := &hookStore{name: "replaying relay 2", onGet: func([][]byte) [][]byte { return [][]byte{future} }}
	c := f.node(t, 2, replay2)
	if got := mustGet(t, c, round); len(got) != 0 {
		t.Fatal("a message dated in the future was accepted")
	}
}

// One signer offline: the others coordinate; the offline one sees only its own payload and is told the channel is
// unavailable; when it returns it catches up from what the relay kept, and its own payload reaches the others
// without its calling Put again.
func TestOneSignerOffline(t *testing.T) {
	f := newFleet(t, 3)
	relay := f.mem("relay")
	cView := &hookStore{name: "relay as C sees it", inner: relay}
	cView.down.Store(true)
	a, b, c := f.node(t, 0, relay), f.node(t, 1, relay), f.node(t, 2, cView)
	round := "zec/release/4/sig/0"
	mustPut(t, a, round, payloadOf(f.id(0), round))
	mustPut(t, b, round, payloadOf(f.id(1), round))
	if err := c.Put(ctx, round, payloadOf(f.id(2), round)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("C's Put with no store: %v", err)
	}
	ab := map[string][]byte{f.id(0): payloadOf(f.id(0), round), f.id(1): payloadOf(f.id(1), round)}
	for _, n := range []*Node{a, b} {
		if got := mustGet(t, n, round); !sameMap(got, ab) {
			t.Fatalf("%s sees %s", n.Self(), showMap(got))
		}
	}
	got, err := c.Get(ctx, round)
	if !errors.Is(err, ErrUnavailable) || !sameMap(got, map[string][]byte{f.id(2): payloadOf(f.id(2), round)}) {
		t.Fatalf("offline C: %v %s", err, showMap(got))
	}
	if err := c.Health(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("offline C's health: %v", err)
	}
	cView.down.Store(false)
	all := map[string][]byte{f.id(0): payloadOf(f.id(0), round), f.id(1): payloadOf(f.id(1), round), f.id(2): payloadOf(f.id(2), round)}
	if got := mustGet(t, c, round); !sameMap(got, all) {
		t.Fatalf("C back online sees %s", showMap(got))
	}
	if got := mustGet(t, a, round); !sameMap(got, all) {
		t.Fatalf("A does not see C's payload after C came back: %s", showMap(got))
	}
}

// Relays that drop and delay at random: every answer ever returned is a payload its sender actually published for
// that round (safety), and the round completes once the survivors carry enough (liveness).
func TestLossyRelaysNeverYieldAWrongPayload(t *testing.T) {
	f := newFleet(t, 3)
	rng := rand.New(rand.NewSource(7))
	var rmu sync.Mutex
	roll := func(p float64) bool { rmu.Lock(); defer rmu.Unlock(); return rng.Float64() < p }
	lossy := func(name string, p float64) *hookStore {
		return &hookStore{name: name, inner: f.mem(name),
			onPut: func(Stored) bool { return !roll(p) },
			onGet: func(raws [][]byte) [][]byte {
				var out [][]byte
				for _, r := range raws {
					if !roll(p) {
						out = append(out, r)
					}
				}
				return out
			}}
	}
	dead := &hookStore{name: "dead relay", inner: f.mem("dead")}
	dead.down.Store(true)
	stores := []Store{lossy("lossy-1", 0.8), lossy("lossy-2", 0.8), dead}
	nodes := []*Node{f.node(t, 0, stores...), f.node(t, 1, stores...), f.node(t, 2, stores...)}
	total, worst := 0, 0
	for r := 0; r < 25; r++ {
		round := fmt.Sprintf("xmr/txset/%d/abc", r)
		iters := 0
		for done := false; !done; iters++ {
			if iters > 500 {
				t.Fatalf("round %d never completed through two lossy relays", r)
			}
			done = true
			for i, n := range nodes {
				_ = n.Put(ctx, round, payloadOf(f.id(i), round)) // re-publish every poll, as a backend should
				got, _ := n.Get(ctx, round)
				for id, v := range got {
					if !bytes.Equal(v, payloadOf(id, round)) {
						t.Fatalf("SAFETY: %s got %q for %s", n.Self(), v, id)
					}
				}
				if len(got) < 3 {
					done = false
				}
			}
		}
		total += iters
		worst = max(worst, iters)
	}
	if worst < 2 {
		t.Fatal("no round needed a second poll: the loss was not exercised")
	}
	t.Logf("25 rounds through two relays dropping 80%% each way and one dead relay: %d polls in all, %d at worst", total, worst)
}

func TestADelayingRelayOnlyDelays(t *testing.T) {
	f := newFleet(t, 3)
	seen := map[[32]byte]int{}
	slow := &hookStore{name: "slow relay", inner: f.mem("slow"), onGet: func(raws [][]byte) [][]byte {
		var out [][]byte
		for _, r := range raws {
			h := hash(r)
			seen[h]++
			if seen[h] > 5 {
				out = append(out, r)
			}
		}
		return out
	}}
	nodes := []*Node{f.node(t, 0, slow), f.node(t, 1, slow), f.node(t, 2, slow)}
	round := "zec/release/5/sig/2"
	for i, n := range nodes {
		mustPut(t, n, round, payloadOf(f.id(i), round))
	}
	polls := 0
	for {
		polls++
		complete := true
		for _, n := range nodes {
			got := mustGet(t, n, round)
			for id, v := range got {
				if !bytes.Equal(v, payloadOf(id, round)) {
					t.Fatalf("SAFETY: %s got %q for %s", n.Self(), v, id)
				}
			}
			complete = complete && len(got) == 3
		}
		if complete {
			break
		}
		if polls > 20 {
			t.Fatal("never completed")
		}
	}
	if polls < 3 {
		t.Fatalf("completed after %d polls: the delay was not exercised", polls)
	}
}

// Every store gone: Put says so, Get returns only what this node holds (its own payload) and says so. Nothing is
// invented, and the round completes when a store returns.
func TestLosingEveryStoreIsADelayNotAWrongAnswer(t *testing.T) {
	f := newFleet(t, 3)
	relay := f.mem("relay")
	gate := &hookStore{name: "relay", inner: relay}
	nodes := []*Node{f.node(t, 0, gate), f.node(t, 1, gate), f.node(t, 2, gate)}
	gate.down.Store(true)
	round := "xmr/ceremony/1"
	for i, n := range nodes {
		if err := n.Put(ctx, round, payloadOf(f.id(i), round)); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("Put with no store: %v", err)
		}
		got, err := n.Get(ctx, round)
		if !errors.Is(err, ErrUnavailable) || len(got) != 1 || !bytes.Equal(got[f.id(i)], payloadOf(f.id(i), round)) {
			t.Fatalf("Get with no store: %v %s", err, showMap(got))
		}
	}
	gate.down.Store(false)
	for pass := 0; pass < 2; pass++ { // first pass: each node re-offers its payload; second: everyone reads all
		for _, n := range nodes {
			_, _ = n.Get(ctx, round)
		}
	}
	for _, n := range nodes {
		if got := mustGet(t, n, round); len(got) != 3 {
			t.Fatalf("after the store returned, %s sees %s", n.Self(), showMap(got))
		}
	}
}

func TestARelayThatRestartsEmptyIsHealedByTheSender(t *testing.T) {
	f := newFleet(t, 3)
	box := &hookStore{name: "relay", inner: f.mem("relay-1")}
	a, b := f.node(t, 0, box), f.node(t, 1, box)
	round := "zec/release/6/sig/0"
	mustPut(t, a, round, payloadOf(f.id(0), round))
	box.setInner(f.mem("relay-2")) // restarted with nothing
	if got := mustGet(t, b, round); len(got) != 0 {
		t.Fatalf("B sees %s from an empty relay", showMap(got))
	}
	mustGet(t, a, round) // A notices the relay lacks its message and offers it again
	if got := mustGet(t, b, round); !bytes.Equal(got[f.id(0)], payloadOf(f.id(0), round)) {
		t.Fatal("A's payload did not come back after the relay restarted")
	}
}

func TestAMessageNotEncryptedToMeIsNotRead(t *testing.T) {
	f := newFleet(t, 3)
	relay := f.mem("relay")
	vault := mustVault(t, f.vault)
	round := "xmr/info/4"
	now := f.clock.Now().UnixNano()
	raw, err := seal(f.ids[0], vault, round, payloadOf(f.id(0), round), []PublicIdentity{f.pubs[0], f.pubs[2]}, now, now+int64(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := relay.Put(ctx, storedOf(t, raw, roundTag(vault, round))); err != nil {
		t.Fatal(err)
	}
	b, c := f.node(t, 1, relay), f.node(t, 2, relay)
	if got := mustGet(t, b, round); len(got) != 0 {
		t.Fatal("B read a message not encrypted to it")
	}
	if b.Status().Dropped[errNotRecipient.Error()] == 0 {
		t.Fatalf("drops %v", b.Status().Dropped)
	}
	if got := mustGet(t, c, round); !bytes.Equal(got[f.id(0)], payloadOf(f.id(0), round)) {
		t.Fatal("C, a recipient, could not read it")
	}
	if !strings.Contains(f.logs.all(), "diverged") {
		t.Fatal("a message encrypted to a different signer set was not reported")
	}
}

func TestASenderNamingAnotherSignerIsRefused(t *testing.T) {
	f := newFleet(t, 3)
	relay := f.mem("relay")
	vault := mustVault(t, f.vault)
	round := "r"
	imp := &Identity{pub: PublicIdentity{ID: f.id(1), Sign: f.pubs[0].Sign, KEM: f.pubs[0].KEM}, sign: f.ids[0].sign, kem: f.ids[0].kem}
	now := f.clock.Now().UnixNano()
	raw, err := seal(imp, vault, round, []byte("A claiming to be B"), f.pubs, now, now+int64(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := relay.Put(ctx, storedOf(t, raw, roundTag(vault, round))); err != nil {
		t.Fatal(err)
	}
	c := f.node(t, 2, relay)
	if got := mustGet(t, c, round); len(got) != 0 {
		t.Fatalf("C sees %s", showMap(got))
	}
	if c.Status().Dropped["names a sender other than its key's"] == 0 {
		t.Fatalf("drops %v", c.Status().Dropped)
	}
}

// The channel authenticates; it does not make everyone agree. A signer that publishes different payloads to
// different stores is seen by each node through the stores it reaches: the newest where it reaches both.
func TestEquivocationIsVisibleNotPrevented(t *testing.T) {
	f := newFleet(t, 3)
	s1 := &hookStore{name: "relay-1", inner: f.mem("relay-1")}
	s2 := &hookStore{name: "relay-2", inner: f.mem("relay-2")}
	a := f.node(t, 0, s1, s2)
	round := "xmr/txset/8/cd"
	s2.down.Store(true)
	mustPut(t, a, round, []byte("v1"))
	f.clock.Advance(time.Second)
	s2.down.Store(false)
	s1.down.Store(true)
	mustPut(t, a, round, []byte("v2"))
	s1.down.Store(false)
	b := f.node(t, 1, s1, s2)
	if got := mustGet(t, b, round); string(got[f.id(0)]) != "v2" {
		t.Fatalf("B, reaching both stores, sees %q", got[f.id(0)])
	}
	c := f.node(t, 2, s1)
	if got := mustGet(t, c, round); string(got[f.id(0)]) != "v1" {
		t.Fatalf("C, reaching only the first store, sees %q", got[f.id(0)])
	}
}

func TestLogsNeverCarryPayloadsOrKeys(t *testing.T) {
	f := newFleet(t, 3)
	relay := f.mem("relay")
	down := &hookStore{name: "down relay", inner: f.mem("x")}
	down.down.Store(true)
	garbage := &hookStore{name: "garbage relay", onGet: func([][]byte) [][]byte {
		return [][]byte{[]byte("SECRET-PAYLOAD-MARKER in the clear"), bytes.Repeat([]byte{7}, 900)}
	}}
	nodes := []*Node{f.node(t, 0, relay, down, garbage), f.node(t, 1, relay, garbage), f.node(t, 2, relay)}
	for r := 0; r < 3; r++ {
		round := fmt.Sprintf("zec/release/%d/sig/0", r)
		for i, n := range nodes {
			_ = n.Put(ctx, round, payloadOf(f.id(i), round))
			f.clock.Advance(time.Millisecond)
			_ = n.Put(ctx, round, append(payloadOf(f.id(i), round), '2')) // a replacement, which is logged
		}
		for _, n := range nodes {
			_, _ = n.Get(ctx, round)
		}
	}
	logs := f.logs.all()
	if logs == "" {
		t.Fatal("nothing was logged: the test exercised nothing")
	}
	if strings.Contains(logs, "MARKER") {
		t.Fatalf("a payload reached the log:\n%s", logs)
	}
	for _, id := range f.ids {
		if strings.Contains(strings.ToLower(logs), hex.EncodeToString(id.sign.Seed())) {
			t.Fatal("a private key reached the log")
		}
	}
}

func TestConcurrentUse(t *testing.T) {
	f := newFleet(t, 3)
	relay := f.mem("relay")
	nodes := []*Node{f.node(t, 0, relay), f.node(t, 1, relay), f.node(t, 2, relay)}
	var wg sync.WaitGroup
	errs := make(chan error, 300)
	for i, n := range nodes {
		for r := 0; r < 10; r++ {
			wg.Add(1)
			go func(i, r int, n *Node) {
				defer wg.Done()
				round := fmt.Sprintf("zec/release/%d/sig/0", r)
				if err := n.Put(ctx, round, payloadOf(f.id(i), round)); err != nil {
					errs <- err
				}
				if _, err := n.Get(ctx, round); err != nil {
					errs <- err
				}
			}(i, r, n)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	for r := 0; r < 10; r++ {
		round := fmt.Sprintf("zec/release/%d/sig/0", r)
		for _, n := range nodes {
			got := mustGet(t, n, round)
			if len(got) != 3 {
				t.Fatalf("%s round %d: %s", n.Self(), r, showMap(got))
			}
			for id, v := range got {
				if !bytes.Equal(v, payloadOf(id, round)) {
					t.Fatalf("wrong payload for %s", id)
				}
			}
		}
	}
}

func TestNewRefusesBadConfigurations(t *testing.T) {
	f := newFleet(t, 3)
	relay := f.mem("relay")
	stranger := newIdentity(t, testAddr(50))
	cases := map[string]func(c *Config){
		"vault not hex":   func(c *Config) { c.Vault = "xyz" },
		"vault short":     func(c *Config) { c.Vault = testVaultHex[:62] },
		"no identity":     func(c *Config) { c.Identity = nil },
		"self not pinned": func(c *Config) { c.Identity = stranger },
		"self pinned otherwise": func(c *Config) {
			c.Signers = append([]PublicIdentity{c.Signers[0], c.Signers[1]}, PublicIdentity{ID: f.id(2), Sign: stranger.pub.Sign, KEM: stranger.pub.KEM})
		},
		"duplicate id": func(c *Config) { c.Signers = append(c.Signers, c.Signers[1]) },
		"duplicate key": func(c *Config) {
			c.Signers = append(c.Signers, PublicIdentity{ID: testAddr(60), Sign: c.Signers[1].Sign, KEM: c.Signers[1].KEM})
		},
		"one signer":        func(c *Config) { c.Signers = c.Signers[2:] },
		"no store":          func(c *Config) { c.Stores = nil },
		"store twice":       func(c *Config) { c.Stores = []Store{relay, relay} },
		"ttl too short":     func(c *Config) { c.TTL = time.Second },
		"ttl too long":      func(c *Config) { c.TTL = 8 * 24 * time.Hour },
		"payload too large": func(c *Config) { c.MaxPayload = HardMaxPayload + 1 },
		"skew negative":     func(c *Config) { c.MaxSkew = -time.Second },
	}
	for name, mod := range cases {
		cfg := Config{Vault: f.vault, Identity: f.ids[2], Signers: f.pubs, Stores: []Store{relay}}
		mod(&cfg)
		if _, err := New(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	n := f.node(t, 0, relay)
	for _, round := range []string{"", "with space", strings.Repeat("r", MaxRoundLen+1)} {
		if err := n.Put(ctx, round, nil); err == nil {
			t.Errorf("Put(%q) accepted", round)
		}
		if _, err := n.Get(ctx, round); err == nil {
			t.Errorf("Get(%q) accepted", round)
		}
	}
	if err := n.Put(ctx, "empty-payload-is-fine", nil); err != nil {
		t.Fatal(err)
	}
}

func TestCacheStaysWithinItsBudget(t *testing.T) {
	f := newFleet(t, 3)
	relay := f.mem("relay")
	n := f.node(t, 0, relay)
	n.budget = 64 << 10
	for r := 0; r < 200; r++ {
		mustPut(t, n, fmt.Sprintf("r/%d", r), make([]byte, 4<<10))
	}
	if n.cacheBytes > n.budget {
		t.Fatalf("cache holds %d bytes, budget %d", n.cacheBytes, n.budget)
	}
	if got := mustGet(t, n, "r/199"); len(got[f.id(0)]) != 4<<10 {
		t.Fatal("the newest round is gone")
	}
	if got := mustGet(t, n, "r/0"); len(got[f.id(0)]) != 4<<10 { // evicted from memory, read back from the relay
		t.Fatal("an evicted round did not come back from the relay")
	}
}

func hash(b []byte) [32]byte { return sha256.Sum256(b) }
