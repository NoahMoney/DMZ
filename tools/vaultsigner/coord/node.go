package coord

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Coordinator is the method set the foreign backends code against; *Node satisfies it.
type Coordinator interface {
	Put(ctx context.Context, round string, payload []byte) error
	Get(ctx context.Context, round string) (map[string][]byte, error)
}

var _ Coordinator = (*Node)(nil)

const (
	// DefaultTTL is how long a message lives when the configuration does not say.
	DefaultTTL = 24 * time.Hour
	// MinTTL is the shortest lifetime a node accepts in its configuration.
	MinTTL = time.Minute
	// DefaultMaxSkew is the clock difference tolerated between a sender and a receiver.
	DefaultMaxSkew = 5 * time.Minute
	// DefaultFetchTimeout bounds one store operation.
	DefaultFetchTimeout = 10 * time.Second

	maxCachedRounds = 4096
)

// Config is everything a node needs. Signers is the pinned set, this signer included, exactly as every other signer
// pinned it; the node refuses to start when this signer's own entry differs from its identity.
type Config struct {
	Vault        string // the vault scid, 64 hex characters
	Identity     *Identity
	Signers      []PublicIdentity
	Stores       []Store
	TTL          time.Duration // default DefaultTTL, between MinTTL and MaxLifetime
	MaxPayload   int           // default DefaultMaxPayload, at most HardMaxPayload
	MaxSkew      time.Duration // default DefaultMaxSkew, at most an hour
	FetchTimeout time.Duration // default DefaultFetchTimeout, at most five minutes
	Now          func() time.Time
	// Logf receives drop reasons, store failures and replacements — never a payload or a key.
	Logf func(format string, args ...any)
}

type cached struct {
	created, expires int64
	hash             [32]byte
	payload          []byte
}

type roundState struct {
	peers map[[32]byte]*cached // by sender key
	own   *Stored              // this node's newest message for the round, to offer again to a store that lost it
	used  int64
	bytes int
}

type storeState struct {
	ok, failed atomic.Uint64
	mu         sync.Mutex
	lastOK     time.Time
	lastErr    string
}

// Node is one signer's end of the coordination channel. It is safe for concurrent use.
type Node struct {
	vault      [32]byte
	me         *Identity
	myKey      [32]byte
	peers      []PublicIdentity // sorted by key; the recipients of every message
	byKey      map[[32]byte]PublicIdentity
	stores     []Store
	sstate     []*storeState
	ttl        time.Duration
	skew       time.Duration
	fetch      time.Duration
	maxPayload int
	maxMsg     int
	budget     int
	now        func() time.Time
	logf       func(string, ...any)

	mu          sync.Mutex
	lastCreated int64
	rounds      map[[32]byte]*roundState
	cacheBytes  int
	dropped     map[dropKey]bool
	dropCount   map[string]uint64
	divergent   map[[32]byte]bool
}

type dropKey struct{ tag, hash [32]byte }

// New checks the configuration and returns a node. It does no I/O.
func New(cfg Config) (*Node, error) {
	var vault [32]byte
	if !hex32(strings.ToLower(cfg.Vault), &vault) {
		return nil, fmt.Errorf("coord: the vault must be its 64-hex scid, not %q", clip(cfg.Vault, 80))
	}
	if cfg.Identity == nil {
		return nil, errors.New("coord: no identity")
	}
	if len(cfg.Signers) < 2 || len(cfg.Signers) > MaxRecipients {
		return nil, fmt.Errorf("coord: pin 2 to %d signers, this one included (got %d)", MaxRecipients, len(cfg.Signers))
	}
	if len(cfg.Stores) == 0 {
		return nil, errors.New("coord: no store: configure at least one relay or shared directory")
	}
	n := &Node{
		vault: vault, me: cfg.Identity, byKey: map[[32]byte]PublicIdentity{}, stores: cfg.Stores,
		ttl: cfg.TTL, skew: cfg.MaxSkew, fetch: cfg.FetchTimeout, maxPayload: cfg.MaxPayload, now: cfg.Now, logf: cfg.Logf,
		rounds: map[[32]byte]*roundState{}, dropped: map[dropKey]bool{}, dropCount: map[string]uint64{}, divergent: map[[32]byte]bool{},
	}
	copy(n.myKey[:], cfg.Identity.pub.Sign)
	if n.ttl == 0 {
		n.ttl = DefaultTTL
	}
	if n.ttl < MinTTL || n.ttl > MaxLifetime {
		return nil, fmt.Errorf("coord: a message lifetime is between %s and %s", MinTTL, MaxLifetime)
	}
	if n.skew == 0 {
		n.skew = DefaultMaxSkew
	}
	if n.skew < 0 || n.skew > time.Hour {
		return nil, errors.New("coord: the clock skew window is between 0 and an hour")
	}
	if n.fetch == 0 {
		n.fetch = DefaultFetchTimeout
	}
	if n.fetch < 0 || n.fetch > 5*time.Minute {
		return nil, errors.New("coord: the store timeout is between 0 and five minutes")
	}
	if n.maxPayload == 0 {
		n.maxPayload = DefaultMaxPayload
	}
	if n.maxPayload < 0 || n.maxPayload > HardMaxPayload {
		return nil, fmt.Errorf("coord: the payload limit is at most %d bytes", HardMaxPayload)
	}
	n.maxMsg = maxMessage(n.maxPayload)
	n.budget = max(256<<20, 8*n.maxPayload)
	if n.now == nil {
		n.now = time.Now
	}
	if n.logf == nil {
		n.logf = func(string, ...any) {}
	}
	ids := map[string]bool{}
	self := false
	for _, p := range cfg.Signers {
		cid, err := CanonicalID(p.ID)
		if err != nil || cid != p.ID {
			return nil, fmt.Errorf("coord: pinned signer %q is not a canonical id", clip(p.ID, 80))
		}
		if len(p.Sign) != 32 || len(p.KEM) != kemKeyLen {
			return nil, fmt.Errorf("coord: pinned signer %s has no parsed identity", p.ID)
		}
		var k [32]byte
		copy(k[:], p.Sign)
		if ids[p.ID] {
			return nil, fmt.Errorf("coord: signer %s is pinned twice", p.ID)
		}
		if _, dup := n.byKey[k]; dup {
			return nil, fmt.Errorf("coord: two signers are pinned with the same key (%s)", p.ID)
		}
		ids[p.ID] = true
		n.byKey[k] = p
		if p.ID == cfg.Identity.pub.ID {
			if !bytes.Equal(p.Sign, cfg.Identity.pub.Sign) || !bytes.Equal(p.KEM, cfg.Identity.pub.KEM) {
				return nil, fmt.Errorf("coord: the identity pinned for this signer (%s) is not the one in its key file: the other signers would refuse its messages", p.ID)
			}
			self = true
		}
	}
	if !self {
		return nil, fmt.Errorf("coord: this signer (%s) is not among the pinned signers", cfg.Identity.pub.ID)
	}
	n.peers = append([]PublicIdentity(nil), cfg.Signers...)
	sort.Slice(n.peers, func(i, j int) bool { return bytes.Compare(n.peers[i].Sign, n.peers[j].Sign) < 0 })
	names := map[string]bool{}
	for _, s := range cfg.Stores {
		if s == nil || names[s.Name()] {
			return nil, errors.New("coord: a store is missing or configured twice")
		}
		names[s.Name()] = true
		n.sstate = append(n.sstate, &storeState{})
	}
	return n, nil
}

// Self is this signer's id.
func (n *Node) Self() string { return n.me.pub.ID }

// Fingerprint identifies the vault and the pinned signer set; every signer's must be the same.
func (n *Node) Fingerprint() string { return SetFingerprint(n.vault, n.peers) }

// Put publishes this signer's payload for a round, signed, bound to the vault, the round and the sender, and
// encrypted to every pinned signer (this one included, so it can read its own payload back after a restart). A
// later Put for the same round replaces the earlier one everywhere it reaches: put in the round name everything the
// payload is about (a transaction hash, an attempt number), because a store may still serve the older copy to a
// node that has not seen the newer. Put succeeds when at least one store accepted the message; otherwise it returns
// ErrUnavailable, and the payload is still offered again to every store on each later Get. Putting the same
// payload again, every poll while a round is open, is cheap and heals a store that restarted empty.
func (n *Node) Put(ctx context.Context, round string, payload []byte) error {
	if err := ValidRound(round); err != nil {
		return err
	}
	if len(payload) > n.maxPayload {
		return fmt.Errorf("coord: a payload of %d bytes is over the limit of %d", len(payload), n.maxPayload)
	}
	n.mu.Lock()
	created := n.now().UnixNano()
	if created <= n.lastCreated {
		created = n.lastCreated + 1
	}
	n.lastCreated = created
	n.mu.Unlock()
	expires := created + int64(n.ttl)
	raw, err := seal(n.me, n.vault, round, payload, n.peers, created, expires)
	if err != nil {
		return err
	}
	tag := roundTag(n.vault, round)
	sm := &Stored{Vault: n.vault, Tag: tag, Sender: n.myKey, Created: created, Expires: expires, Raw: raw}
	n.mu.Lock()
	rs := n.roundLocked(tag)
	rs.own = sm
	n.setLocked(rs, n.myKey, &cached{created: created, expires: expires, hash: sha256.Sum256(raw), payload: bytes.Clone(payload)})
	n.evictLocked(tag)
	n.mu.Unlock()
	if n.putAll(ctx, *sm, n.stores, n.sstate) == 0 {
		return fmt.Errorf("%w: round %s is kept in memory and offered again on the next Get", ErrUnavailable, round)
	}
	return nil
}

func (n *Node) putAll(ctx context.Context, sm Stored, stores []Store, states []*storeState) int {
	var wg sync.WaitGroup
	var ok atomic.Int32
	for i, s := range stores {
		wg.Add(1)
		go func(s Store, st *storeState) {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, n.fetch)
			defer cancel()
			if err := s.Put(c, sm); err != nil {
				n.storeFailed(s, st, "put", err)
				return
			}
			n.storeOK(st)
			ok.Add(1)
		}(s, states[i])
	}
	wg.Wait()
	return int(ok.Load())
}

// Get returns, keyed by signer id, the newest authentic payload each signer has published for the round that this
// node can read — its own included. It never waits for a payload to arrive: callers poll. It reads every store
// (bounded by the fetch timeout), re-verifies every byte, and drops anything forged, altered, truncated, oversized,
// expired, dated in the future, for another round or another vault, or not encrypted to this signer. When no store
// answers it returns what this node already holds together with an error wrapping ErrUnavailable; the map is still
// authentic, only possibly incomplete. The returned slices are the caller's.
func (n *Node) Get(ctx context.Context, round string) (map[string][]byte, error) {
	if err := ValidRound(round); err != nil {
		return nil, err
	}
	tag := roundTag(n.vault, round)
	type result struct {
		raws [][]byte
		err  error
	}
	results := make([]result, len(n.stores))
	var wg sync.WaitGroup
	for i, s := range n.stores {
		wg.Add(1)
		go func(i int, s Store) {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, n.fetch)
			defer cancel()
			raws, err := s.Get(c, n.vault, tag, len(n.peers)+8, n.maxMsg)
			results[i] = result{raws, err}
		}(i, s)
	}
	wg.Wait()
	now := n.now().UnixNano()
	var own *Stored
	n.mu.Lock()
	if rs := n.rounds[tag]; rs != nil && rs.own != nil && rs.own.Expires > now {
		o := *rs.own
		own = &o
	}
	n.mu.Unlock()
	var ownHash [32]byte
	if own != nil {
		ownHash = sha256.Sum256(own.Raw)
	}
	answered := 0
	var heal []Store
	var healState []*storeState
	for i, r := range results {
		s, st := n.stores[i], n.sstate[i]
		if r.err != nil {
			n.storeFailed(s, st, "get", r.err)
			continue
		}
		n.storeOK(st)
		answered++
		has := false
		for _, raw := range r.raws {
			if n.accept(s.Name(), tag, raw) == ownHash && own != nil {
				has = true
			}
		}
		if own != nil && !has {
			heal = append(heal, s)
			healState = append(healState, st)
		}
	}
	if len(heal) > 0 { // a store that answered without our newest message lost it, or never got it: offer it again
		n.putAll(ctx, *own, heal, healState)
	}
	out := n.snapshot(tag)
	if answered == 0 {
		return out, fmt.Errorf("%w: round %s returns only what this node already held", ErrUnavailable, round)
	}
	return out, nil
}

// accept verifies one message read from a store and caches its payload when it is the newest from its sender. It
// returns the SHA-256 of the bytes.
func (n *Node) accept(store string, tag [32]byte, raw []byte) [32]byte {
	h := sha256.Sum256(raw)
	n.mu.Lock()
	if n.dropped[dropKey{tag, h}] {
		n.mu.Unlock()
		return h
	}
	if rs := n.rounds[tag]; rs != nil {
		for _, c := range rs.peers {
			if c.hash == h {
				n.mu.Unlock()
				return h
			}
		}
	}
	n.mu.Unlock()
	m, err := parse(raw, n.maxMsg)
	if err != nil {
		n.drop(tag, h, store, err.Error(), "")
		return h
	}
	peer, known := n.byKey[m.sender]
	switch {
	case m.vault != n.vault:
		n.drop(tag, h, store, "for another vault", m.senderID)
		return h
	case m.tag != tag:
		n.drop(tag, h, store, "for another round", m.senderID)
		return h
	case !known:
		n.drop(tag, h, store, "signed by a key no signer pinned", m.senderID)
		return h
	case m.senderID != peer.ID:
		n.drop(tag, h, store, "names a sender other than its key's", m.senderID)
		return h
	}
	now := n.now().UnixNano()
	switch {
	case m.expires <= now:
		n.drop(tag, h, store, "expired", peer.ID)
		return h
	case m.created > now+int64(n.skew):
		n.drop(tag, h, store, reasonFuture, peer.ID)
		return h
	case m.expires-m.created > int64(MaxLifetime):
		n.drop(tag, h, store, "living longer than the protocol allows", peer.ID)
		return h
	case len(m.ct)-16 > n.maxPayload: // the ciphertext's length gives the payload's away: refuse before any work
		n.drop(tag, h, store, "a payload over this node's limit", peer.ID)
		return h
	}
	if !m.verify(peer.Sign) {
		n.drop(tag, h, store, "bad signature", peer.ID)
		return h
	}
	n.mu.Lock()
	if rs := n.rounds[tag]; rs != nil {
		if c := rs.peers[m.sender]; c != nil && !newer(m.created, h, c.created, c.hash) {
			n.mu.Unlock()
			return h // an older copy of what we hold, or a replay of it: authentic, and superseded
		}
	}
	n.mu.Unlock()
	payload, err := m.open(n.me)
	if err != nil {
		n.drop(tag, h, store, err.Error(), peer.ID)
		return h
	}
	if len(payload) > n.maxPayload {
		n.drop(tag, h, store, "a payload over this node's limit", peer.ID)
		return h
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if !m.recipientsAre(n.byKey) && !n.divergent[m.sender] {
		n.divergent[m.sender] = true
		n.logf("coord: %s encrypts to a signer set that differs from this node's pinned set: the coordination configs have diverged", peer.ID)
	}
	rs := n.roundLocked(tag)
	if c := rs.peers[m.sender]; c != nil {
		if !newer(m.created, h, c.created, c.hash) {
			return h
		}
		if m.sender != n.myKey {
			n.logf("coord: %s replaced its payload for a round (%s)", peer.ID, hex.EncodeToString(tag[:4]))
		}
	}
	n.setLocked(rs, m.sender, &cached{created: m.created, expires: m.expires, hash: h, payload: payload})
	n.evictLocked(tag)
	return h
}

// reasonFuture is the one refusal that time can undo: a message dated ahead of this clock becomes acceptable once
// the clock catches up, so it is not remembered as refused.
const reasonFuture = "dated in the future (check both clocks)"

// drop records that a message was refused for the round tag. The memo is per round: a message refused as "for
// another round" is exactly the message its own round must accept.
func (n *Node) drop(tag, h [32]byte, store, reason, claimed string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	k := dropKey{tag, h}
	if n.dropped[k] {
		return
	}
	if reason != reasonFuture {
		if len(n.dropped) >= 8192 {
			n.dropped = map[dropKey]bool{}
		}
		n.dropped[k] = true
	}
	n.dropCount[reason]++
	if claimed != "" {
		claimed = " (claimed sender " + clip(claimed, 80) + ")"
	}
	n.logf("coord: dropped a message from %s: %s%s", store, reason, claimed)
}

func (n *Node) roundLocked(tag [32]byte) *roundState {
	rs := n.rounds[tag]
	if rs == nil {
		rs = &roundState{peers: map[[32]byte]*cached{}}
		n.rounds[tag] = rs
	}
	rs.used = n.now().UnixNano()
	return rs
}

func (n *Node) setLocked(rs *roundState, sender [32]byte, c *cached) {
	if old := rs.peers[sender]; old != nil {
		rs.bytes -= len(old.payload)
		n.cacheBytes -= len(old.payload)
	}
	rs.peers[sender] = c
	rs.bytes += len(c.payload)
	n.cacheBytes += len(c.payload)
}

// evictLocked keeps the cache under its byte budget and round count, dropping expired entries first and then the
// least recently used rounds, never the one in use.
func (n *Node) evictLocked(keep [32]byte) {
	if n.cacheBytes <= n.budget && len(n.rounds) <= maxCachedRounds {
		return
	}
	now := n.now().UnixNano()
	for tag, rs := range n.rounds {
		for k, c := range rs.peers {
			if c.expires <= now {
				rs.bytes -= len(c.payload)
				n.cacheBytes -= len(c.payload)
				delete(rs.peers, k)
			}
		}
		if len(rs.peers) == 0 && (rs.own == nil || rs.own.Expires <= now) && tag != keep {
			delete(n.rounds, tag)
		}
	}
	for n.cacheBytes > n.budget || len(n.rounds) > maxCachedRounds {
		var oldest [32]byte
		var at int64 = -1
		for tag, rs := range n.rounds {
			if tag != keep && (at < 0 || rs.used < at) {
				oldest, at = tag, rs.used
			}
		}
		if at < 0 {
			return
		}
		n.cacheBytes -= n.rounds[oldest].bytes
		delete(n.rounds, oldest)
	}
}

func (n *Node) snapshot(tag [32]byte) map[string][]byte {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := map[string][]byte{}
	rs := n.rounds[tag]
	if rs == nil {
		return out
	}
	now := n.now().UnixNano()
	for k, c := range rs.peers {
		if c.expires <= now {
			rs.bytes -= len(c.payload)
			n.cacheBytes -= len(c.payload)
			delete(rs.peers, k)
			continue
		}
		out[n.byKey[k].ID] = bytes.Clone(c.payload)
	}
	rs.used = now
	return out
}

func (n *Node) storeOK(st *storeState) {
	st.ok.Add(1)
	st.mu.Lock()
	st.lastOK = n.now()
	st.lastErr = ""
	st.mu.Unlock()
}

// storeFailed counts a failure and logs it when it differs from the store's previous state, so a relay that stays
// down is reported once rather than on every poll.
func (n *Node) storeFailed(s Store, st *storeState, op string, err error) {
	st.failed.Add(1)
	msg := op + ": " + err.Error()
	st.mu.Lock()
	changed := st.lastErr != msg
	st.lastErr = msg
	st.mu.Unlock()
	if changed {
		n.logf("coord: store %s failed (%s); coordination continues through the other stores, if any", s.Name(), clip(msg, 300))
	}
}

// StoreStatus is one store's record.
type StoreStatus struct {
	Name      string
	OK        uint64
	Failed    uint64
	LastOK    time.Time
	LastError string
}

// Status is a node's counters, for monitoring. It holds no payload.
type Status struct {
	Self        string
	Fingerprint string
	Stores      []StoreStatus
	Dropped     map[string]uint64 // by reason
	Rounds      int
	CachedBytes int
}

// Status reports the node's counters.
func (n *Node) Status() Status {
	s := Status{Self: n.me.pub.ID, Fingerprint: n.Fingerprint(), Dropped: map[string]uint64{}}
	for i, st := range n.sstate {
		st.mu.Lock()
		s.Stores = append(s.Stores, StoreStatus{Name: n.stores[i].Name(), OK: st.ok.Load(), Failed: st.failed.Load(), LastOK: st.lastOK, LastError: st.lastErr})
		st.mu.Unlock()
	}
	n.mu.Lock()
	for k, v := range n.dropCount {
		s.Dropped[k] = v
	}
	s.Rounds, s.CachedBytes = len(n.rounds), n.cacheBytes
	n.mu.Unlock()
	return s
}

// Health asks every store whether it is reachable. It returns nil when all are, an error naming the ones that are
// not when some are down (coordination continues through the rest), and an error wrapping ErrUnavailable when none
// is. It is for monitoring: a backend must not make its own Health fail on it, because signing waits on the
// channel while deposits and mints do not need it at all.
func (n *Node) Health(ctx context.Context) error {
	errs := make([]error, len(n.stores))
	var wg sync.WaitGroup
	for i, s := range n.stores {
		wg.Add(1)
		go func(i int, s Store) {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, n.fetch)
			defer cancel()
			errs[i] = s.Health(c)
		}(i, s)
	}
	wg.Wait()
	var bad []string
	for i, err := range errs {
		if err != nil {
			bad = append(bad, n.stores[i].Name()+": "+clip(err.Error(), 200))
		}
	}
	switch {
	case len(bad) == 0:
		return nil
	case len(bad) == len(n.stores):
		return fmt.Errorf("%w: %s", ErrUnavailable, strings.Join(bad, "; "))
	}
	return fmt.Errorf("coord: %d of %d stores unreachable, coordination continues through the others: %s", len(bad), len(n.stores), strings.Join(bad, "; "))
}

// Close releases idle connections. The node holds no goroutine.
func (n *Node) Close() error {
	for _, s := range n.stores {
		if c, ok := s.(interface{ CloseIdle() }); ok {
			c.CloseIdle()
		}
	}
	return nil
}

// SetFingerprint is the digest every signer compares during setup: the vault and every pinned (id, identity) pair,
// in a canonical order, so two signers with the same pins print the same fingerprint whatever order their files use.
func SetFingerprint(vault [32]byte, peers []PublicIdentity) string {
	lines := make([]string, 0, len(peers))
	for _, p := range peers {
		lines = append(lines, p.ID+" "+p.String())
	}
	sort.Strings(lines)
	return fingerprint("DMZ coord set v1\n" + hex.EncodeToString(vault[:]) + "\n" + strings.Join(lines, "\n"))
}
