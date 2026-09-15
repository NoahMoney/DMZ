package coord

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"sort"
	"sync"
	"time"
)

// Stored is one message as a store files it: the fields a store needs to keep one copy per (vault, round, sender)
// and to expire it, and the exact bytes. A store is never trusted with any of it — a node re-verifies every byte it
// reads back — so a store that files a message under the wrong round or sender only loses that message.
type Stored struct {
	Vault, Tag, Sender [32]byte
	Created, Expires   int64
	Raw                []byte
}

// Store is a place signers leave messages for each other: a relay over HTTP, a shared directory, or memory in a
// test. A store may lose, delay, duplicate, reorder, truncate or replay anything; it cannot forge, alter or read a
// payload, and a node treats everything it returns as untrusted input.
type Store interface {
	// Name identifies the store in logs and status (never with credentials).
	Name() string
	// Put files a message. Keeping a newer copy from the same sender instead is success.
	Put(ctx context.Context, m Stored) error
	// Get returns at most maxCount messages of at most maxBytes each for (vault, tag). Raw slices are read-only.
	Get(ctx context.Context, vault, tag [32]byte, maxCount, maxBytes int) ([][]byte, error)
	// Health says whether the store is reachable now.
	Health(ctx context.Context) error
}

var (
	// ErrUnavailable is returned (wrapped) when no configured store answered.
	ErrUnavailable = errors.New("coord: no store reachable")
	// ErrFull is a store refusing a message for lack of space or of the sender's quota.
	ErrFull = errors.New("coord: store full")
	// ErrTooLarge is a message over the store's size limit.
	ErrTooLarge = errors.New("coord: message too large")
	// ErrStale is a message already expired, dated too far ahead, or living longer than MaxLifetime.
	ErrStale = errors.New("coord: message expired, dated in the future, or living too long")
)

// MemLimits bound a MemStore. Zero values take the defaults noted.
type MemLimits struct {
	MaxMessageBytes    int           // default maxMessage(DefaultMaxPayload)
	MaxTotalBytes      int64         // default 1 GiB
	MaxSenderBytes     int64         // default MaxTotalBytes: set it to share the space fairly among senders
	MaxRoundsPerSender int           // default 4096
	MaxSkew            time.Duration // default DefaultMaxSkew
}

type memSlot struct {
	created, expires int64
	hash             [32]byte
	raw              []byte
}

type roundKey struct{ vault, tag [32]byte }

type senderUse struct {
	bytes  int64
	rounds int
}

// MemStore is the relay's storage, usable directly in a process: one copy per (vault, round tag, sender), the newest
// by (created, hash); bounded in total bytes, per-sender bytes and per-sender rounds; expired copies are pruned.
type MemStore struct {
	name   string
	lim    MemLimits
	now    func() time.Time
	mu     sync.Mutex
	rounds map[roundKey]map[[32]byte]*memSlot
	use    map[[32]byte]*senderUse
	total  int64
	count  int
}

// NewMemStore returns an empty store. now may be nil (time.Now).
func NewMemStore(name string, lim MemLimits, now func() time.Time) *MemStore {
	if lim.MaxMessageBytes <= 0 {
		lim.MaxMessageBytes = maxMessage(DefaultMaxPayload)
	}
	if lim.MaxTotalBytes <= 0 {
		lim.MaxTotalBytes = 1 << 30
	}
	if lim.MaxSenderBytes <= 0 || lim.MaxSenderBytes > lim.MaxTotalBytes {
		lim.MaxSenderBytes = lim.MaxTotalBytes
	}
	if lim.MaxRoundsPerSender <= 0 {
		lim.MaxRoundsPerSender = 4096
	}
	if lim.MaxSkew <= 0 {
		lim.MaxSkew = DefaultMaxSkew
	}
	if now == nil {
		now = time.Now
	}
	return &MemStore{name: name, lim: lim, now: now, rounds: map[roundKey]map[[32]byte]*memSlot{}, use: map[[32]byte]*senderUse{}}
}

func (s *MemStore) Name() string                     { return s.name }
func (s *MemStore) Health(ctx context.Context) error { return ctx.Err() }

// Put keeps the newer of the stored copy and m. It refuses a message over the size limit, one outside its
// lifetime, and one that would take the store or its sender over quota even after expired copies are pruned.
func (s *MemStore) Put(_ context.Context, m Stored) error {
	if len(m.Raw) > s.lim.MaxMessageBytes {
		return ErrTooLarge
	}
	now := s.now().UnixNano()
	if m.Expires <= now || m.Created > now+int64(s.lim.MaxSkew) || m.Expires-m.Created > int64(MaxLifetime) || m.Created <= 0 {
		return ErrStale
	}
	h := sha256.Sum256(m.Raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	rk := roundKey{m.Vault, m.Tag}
	var old *memSlot
	if r := s.rounds[rk]; r != nil {
		old = r[m.Sender]
	}
	if old != nil && old.expires > now && !newer(m.Created, h, old.created, old.hash) {
		return nil // the copy already held is this one, or newer: keep it
	}
	fits := func() bool {
		u := s.use[m.Sender]
		var ub int64
		var ur int
		if u != nil {
			ub, ur = u.bytes, u.rounds
		}
		delta := int64(len(m.Raw))
		if old != nil {
			delta -= int64(len(old.raw))
		}
		return s.total+delta <= s.lim.MaxTotalBytes && ub+delta <= s.lim.MaxSenderBytes && (old != nil || ur < s.lim.MaxRoundsPerSender)
	}
	if !fits() {
		s.pruneLocked(now)
		if r := s.rounds[rk]; r != nil {
			old = r[m.Sender]
		} else {
			old = nil
		}
		if !fits() {
			return ErrFull
		}
	}
	if old != nil {
		s.deleteLocked(rk, m.Sender)
	}
	r := s.rounds[rk]
	if r == nil {
		r = map[[32]byte]*memSlot{}
		s.rounds[rk] = r
	}
	r[m.Sender] = &memSlot{created: m.Created, expires: m.Expires, hash: h, raw: bytes.Clone(m.Raw)}
	u := s.use[m.Sender]
	if u == nil {
		u = &senderUse{}
		s.use[m.Sender] = u
	}
	u.bytes += int64(len(m.Raw))
	u.rounds++
	s.total += int64(len(m.Raw))
	s.count++
	return nil
}

func (s *MemStore) deleteLocked(rk roundKey, sender [32]byte) {
	r := s.rounds[rk]
	sl := r[sender]
	if sl == nil {
		return
	}
	delete(r, sender)
	if len(r) == 0 {
		delete(s.rounds, rk)
	}
	if u := s.use[sender]; u != nil {
		u.bytes -= int64(len(sl.raw))
		u.rounds--
		if u.rounds <= 0 {
			delete(s.use, sender)
		}
	}
	s.total -= int64(len(sl.raw))
	s.count--
}

func (s *MemStore) pruneLocked(now int64) {
	for rk, r := range s.rounds {
		for sender, sl := range r {
			if sl.expires <= now {
				s.deleteLocked(rk, sender)
			}
		}
	}
}

// Prune drops every expired copy.
func (s *MemStore) Prune() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now().UnixNano())
}

// Get returns the live copies for (vault, tag), ordered by sender key.
func (s *MemStore) Get(_ context.Context, vault, tag [32]byte, maxCount, maxBytes int) ([][]byte, error) {
	now := s.now().UnixNano()
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rounds[roundKey{vault, tag}]
	keys := make([][32]byte, 0, len(r))
	for k, sl := range r {
		if sl.expires > now && len(sl.raw) <= maxBytes {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return bytes.Compare(keys[i][:], keys[j][:]) < 0 })
	if len(keys) > maxCount {
		keys = keys[:maxCount]
	}
	out := make([][]byte, 0, len(keys))
	for _, k := range keys {
		out = append(out, r[k].raw)
	}
	return out, nil
}

// Stats reports how many messages and bytes the store holds.
func (s *MemStore) Stats() (messages int, bytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count, s.total
}
