package coord

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	rpc "veil/tools/simrpc"
)

const (
	testVaultHex  = "5a1e0c0de0000000000000000000000000000000000000000000000000000001"
	otherVaultHex = "5a1e0c0de0000000000000000000000000000000000000000000000000000002"
)

var ctx = context.Background()

func mustVault(t testing.TB, s string) [32]byte {
	t.Helper()
	var v [32]byte
	if !hex32(s, &v) {
		t.Fatalf("bad vault %s", s)
	}
	return v
}

// testAddr is a well-formed DERO address for a made-up 33-byte key.
func testAddr(b byte) string {
	raw := make([]byte, 33)
	raw[0] = 2
	for i := 1; i < 33; i++ {
		raw[i] = b ^ byte(i*37)
	}
	return rpc.AddressFromRaw(raw)
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Now()} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (l *logSink) logf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *logSink) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func newIdentity(t testing.TB, id string) *Identity {
	t.Helper()
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	idn, err := fromSeed(id, seed)
	if err != nil {
		t.Fatal(err)
	}
	return idn
}

// fleet is n signers of one vault sharing a clock and a log.
type fleet struct {
	vault string
	ids   []*Identity
	pubs  []PublicIdentity
	clock *fakeClock
	logs  *logSink
}

func newFleet(t testing.TB, n int) *fleet {
	t.Helper()
	f := &fleet{vault: testVaultHex, clock: newClock(), logs: &logSink{}}
	for i := 0; i < n; i++ {
		id := newIdentity(t, testAddr(byte(i+1)))
		f.ids = append(f.ids, id)
		f.pubs = append(f.pubs, id.Public())
	}
	return f
}

func (f *fleet) node(t testing.TB, i int, stores ...Store) *Node {
	return f.nodeWith(t, i, nil, stores...)
}

func (f *fleet) nodeWith(t testing.TB, i int, mod func(*Config), stores ...Store) *Node {
	t.Helper()
	cfg := Config{Vault: f.vault, Identity: f.ids[i], Signers: f.pubs, Stores: stores, Now: f.clock.Now, Logf: f.logs.logf, FetchTimeout: 2 * time.Second}
	if mod != nil {
		mod(&cfg)
	}
	n, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *fleet) id(i int) string { return f.ids[i].ID() }

func (f *fleet) key(i int) [32]byte {
	var k [32]byte
	copy(k[:], f.pubs[i].Sign)
	return k
}

func (f *fleet) mem(name string) *MemStore { return NewMemStore(name, MemLimits{}, f.clock.Now) }

func payloadOf(id, round string) []byte { return []byte("SECRET-PAYLOAD-MARKER " + id + " " + round) }

func mustGet(t testing.TB, n *Node, round string) map[string][]byte {
	t.Helper()
	m, err := n.Get(ctx, round)
	if err != nil {
		t.Fatalf("Get(%s) by %s: %v", round, n.Self(), err)
	}
	return m
}

func mustPut(t testing.TB, n *Node, round string, payload []byte) {
	t.Helper()
	if err := n.Put(ctx, round, payload); err != nil {
		t.Fatalf("Put(%s) by %s: %v", round, n.Self(), err)
	}
}

func sameMap(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || !bytes.Equal(v, w) {
			return false
		}
	}
	return true
}

func showMap(m map[string][]byte) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s…=%q ", clip(k, 12), clip(string(m[k]), 60))
	}
	return b.String()
}

// hookStore is a relay that misbehaves on request: down, dropping puts, rewriting what it serves.
type hookStore struct {
	name  string
	down  atomic.Bool
	mu    sync.Mutex
	inner Store
	onPut func(m Stored) bool          // false: accept silently and throw away
	onGet func(raws [][]byte) [][]byte // rewrites what is served
}

func (h *hookStore) Name() string { return h.name }

func (h *hookStore) setInner(s Store) {
	h.mu.Lock()
	h.inner = s
	h.mu.Unlock()
}

func (h *hookStore) Put(ctx context.Context, m Stored) error {
	if h.down.Load() {
		return errors.New(h.name + " is down")
	}
	h.mu.Lock()
	inner, onPut := h.inner, h.onPut
	keep := onPut == nil || onPut(m)
	h.mu.Unlock()
	if !keep || inner == nil {
		return nil
	}
	return inner.Put(ctx, m)
}

func (h *hookStore) Get(ctx context.Context, vault, tag [32]byte, maxCount, maxBytes int) ([][]byte, error) {
	if h.down.Load() {
		return nil, errors.New(h.name + " is down")
	}
	h.mu.Lock()
	inner := h.inner
	h.mu.Unlock()
	var raws [][]byte
	if inner != nil {
		var err error
		if raws, err = inner.Get(ctx, vault, tag, maxCount, maxBytes); err != nil {
			return nil, err
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.onGet != nil {
		raws = h.onGet(raws)
	}
	return raws, nil
}

func (h *hookStore) Health(context.Context) error {
	if h.down.Load() {
		return errors.New(h.name + " is down")
	}
	return nil
}

// only returns the single raw message a store holds for (vault, round).
func only(t testing.TB, s Store, vault [32]byte, round string) []byte {
	t.Helper()
	raws, err := s.Get(ctx, vault, roundTag(vault, round), 64, maxMessage(HardMaxPayload))
	if err != nil || len(raws) != 1 {
		t.Fatalf("want one message for %s, got %d (%v)", round, len(raws), err)
	}
	return raws[0]
}

func storedOf(t testing.TB, raw []byte, tag [32]byte) Stored {
	t.Helper()
	m, err := parse(raw, maxMessage(HardMaxPayload))
	if err != nil {
		t.Fatal(err)
	}
	return Stored{Vault: m.vault, Tag: tag, Sender: m.sender, Created: m.created, Expires: m.expires, Raw: raw}
}
