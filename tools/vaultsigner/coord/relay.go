package coord

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The relay is a dumb store-and-forward box with an HTTP face. It holds no key and is trusted with nothing: it
// verifies each message's signature against the pinned signer keys only to refuse junk from strangers (a flood
// control, not a security boundary — every node re-verifies every byte it reads back). It can delay, drop, withhold
// or replay messages, which costs liveness; it cannot forge one, alter one, move one into another round or vault,
// or read a payload.
//
//	PUT /v1/m                      body: one message          204 stored (or a newer copy kept)
//	GET /v1/m/<vault hex>/<tag hex>                           200 frames: (length uint32 big-endian | message)*
//	GET /v1/health                                            200 {"ok":true,"messages":n,"bytes":b}
//
// Refusals: 400 malformed, 403 unknown vault or sender or a bad signature, 410 outside its lifetime, 413 too large,
// 503 busy, 507 full (the store or the sender's quota).

// RelayGroup is one vault the relay serves, with the identities of the signers allowed to leave messages for it.
type RelayGroup struct {
	Vault   [32]byte
	Signers []PublicIdentity
}

// RelayConfig configures a relay. Zero values take the defaults noted.
type RelayConfig struct {
	Groups             []RelayGroup
	MaxPayload         int           // default DefaultMaxPayload, at most HardMaxPayload
	MaxTotalBytes      int64         // default 1 GiB, shared fairly: each sender gets MaxTotalBytes / senders
	MaxRoundsPerSender int           // default 4096
	MaxSkew            time.Duration // default DefaultMaxSkew
	MaxConcurrent      int           // concurrent PUTs, default 32
	PruneEvery         time.Duration // default 30 s
	Now                func() time.Time
	Logf               func(format string, args ...any)
}

// Relay serves RelayConfig's vaults over HTTP. Close stops its pruning loop.
type Relay struct {
	store  *MemStore
	keys   map[[32]byte]map[[32]byte]string // vault → sender key → canonical id
	sem    chan struct{}
	maxMsg int
	skew   time.Duration
	now    func() time.Time
	logf   func(string, ...any)
	stop   chan struct{}
	once   sync.Once
}

// NewRelay validates the configuration and starts the pruning loop.
func NewRelay(cfg RelayConfig) (*Relay, error) {
	if len(cfg.Groups) == 0 {
		return nil, errors.New("coord: a relay needs at least one vault and its pinned signers: it stores only what they sign")
	}
	if cfg.MaxPayload <= 0 {
		cfg.MaxPayload = DefaultMaxPayload
	}
	if cfg.MaxPayload > HardMaxPayload {
		return nil, fmt.Errorf("coord: a relay's payload limit is at most %d bytes", HardMaxPayload)
	}
	if cfg.MaxTotalBytes <= 0 {
		cfg.MaxTotalBytes = 1 << 30
	}
	if cfg.MaxSkew <= 0 {
		cfg.MaxSkew = DefaultMaxSkew
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 32
	}
	if cfg.PruneEvery <= 0 {
		cfg.PruneEvery = 30 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	keys := map[[32]byte]map[[32]byte]string{}
	senders := 0
	for _, g := range cfg.Groups {
		if keys[g.Vault] != nil {
			return nil, fmt.Errorf("coord: vault %x is configured twice", g.Vault)
		}
		if len(g.Signers) == 0 {
			return nil, fmt.Errorf("coord: vault %x has no pinned signers", g.Vault)
		}
		m := map[[32]byte]string{}
		for _, p := range g.Signers {
			var k [32]byte
			copy(k[:], p.Sign)
			if _, dup := m[k]; dup {
				return nil, fmt.Errorf("coord: vault %x pins one key twice", g.Vault)
			}
			m[k] = p.ID
		}
		keys[g.Vault] = m
		senders += len(m)
	}
	maxMsg := maxMessage(cfg.MaxPayload)
	perSender := cfg.MaxTotalBytes / int64(senders)
	if perSender < 2*int64(maxMsg) {
		return nil, fmt.Errorf("coord: %d bytes shared by %d senders leaves each less than two messages of %d bytes: raise the total or lower the payload limit", cfg.MaxTotalBytes, senders, maxMsg)
	}
	r := &Relay{
		store: NewMemStore("relay", MemLimits{MaxMessageBytes: maxMsg, MaxTotalBytes: cfg.MaxTotalBytes, MaxSenderBytes: perSender,
			MaxRoundsPerSender: cfg.MaxRoundsPerSender, MaxSkew: cfg.MaxSkew}, cfg.Now),
		keys: keys, sem: make(chan struct{}, cfg.MaxConcurrent), maxMsg: maxMsg, skew: cfg.MaxSkew, now: cfg.Now, logf: cfg.Logf,
		stop: make(chan struct{}),
	}
	go func() {
		t := time.NewTicker(cfg.PruneEvery)
		defer t.Stop()
		for {
			select {
			case <-r.stop:
				return
			case <-t.C:
				r.store.Prune()
			}
		}
	}()
	return r, nil
}

// Close stops the pruning loop.
func (r *Relay) Close() { r.once.Do(func() { close(r.stop) }) }

// Stats reports what the relay holds.
func (r *Relay) Stats() (messages int, bytes int64) { return r.store.Stats() }

func (r *Relay) ServeHTTP(w http.ResponseWriter, q *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	switch p := q.URL.Path; {
	case p == "/v1/health":
		if q.Method != http.MethodGet && q.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		n, b := r.store.Stats()
		h.Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": n, "bytes": b})
	case p == "/v1/m":
		if q.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.put(w, q)
	case strings.HasPrefix(p, "/v1/m/"):
		if q.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.get(w, q, strings.TrimPrefix(p, "/v1/m/"))
	default:
		http.NotFound(w, q)
	}
}

func (r *Relay) put(w http.ResponseWriter, q *http.Request) {
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	default:
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, q.Body, int64(r.maxMsg)))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			http.Error(w, "message too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}
	m, err := parse(raw, r.maxMsg)
	if err != nil {
		if errors.Is(err, errTooLarge) {
			http.Error(w, "message too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "malformed message", http.StatusBadRequest)
		return
	}
	if len(m.ct)-16 > r.maxMsg-envelopeOverhead { // the relay cannot read a payload, but its length shows
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}
	senders := r.keys[m.vault]
	if senders == nil {
		http.Error(w, "this relay does not serve that vault", http.StatusForbidden)
		return
	}
	id, ok := senders[m.sender]
	if !ok || id != m.senderID {
		http.Error(w, "the sender is not a pinned signer of that vault", http.StatusForbidden)
		return
	}
	now := r.now().UnixNano()
	if m.expires <= now || m.created > now+int64(r.skew) || m.expires-m.created > int64(MaxLifetime) {
		http.Error(w, "the message is expired, dated in the future, or lives too long (check the clock)", http.StatusGone)
		return
	}
	if !m.verify(ed25519.PublicKey(m.sender[:])) {
		http.Error(w, "bad signature", http.StatusForbidden)
		return
	}
	switch err := r.store.Put(q.Context(), Stored{Vault: m.vault, Tag: m.tag, Sender: m.sender, Created: m.created, Expires: m.expires, Raw: raw}); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrFull):
		r.logf("relay: refused a message from %s: store or sender quota full", id)
		http.Error(w, "store full", http.StatusInsufficientStorage)
	case errors.Is(err, ErrTooLarge):
		http.Error(w, "message too large", http.StatusRequestEntityTooLarge)
	case errors.Is(err, ErrStale):
		http.Error(w, "message outside its lifetime", http.StatusGone)
	default:
		http.Error(w, "not stored", http.StatusInternalServerError)
	}
}

func (r *Relay) get(w http.ResponseWriter, q *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	var vault, tag [32]byte
	if len(parts) != 2 || !hex32(parts[0], &vault) || !hex32(parts[1], &tag) {
		http.Error(w, "want /v1/m/<vault hex>/<tag hex>", http.StatusBadRequest)
		return
	}
	senders := r.keys[vault]
	if senders == nil {
		http.Error(w, "this relay does not serve that vault", http.StatusNotFound)
		return
	}
	raws, _ := r.store.Get(q.Context(), vault, tag, len(senders), r.maxMsg)
	w.Header().Set("Content-Type", "application/octet-stream")
	var hdr [4]byte
	for _, raw := range raws {
		binary.BigEndian.PutUint32(hdr[:], uint32(len(raw)))
		if _, err := w.Write(hdr[:]); err != nil {
			return
		}
		if _, err := w.Write(raw); err != nil {
			return
		}
	}
}

func hex32(s string, out *[32]byte) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.Decode(out[:], []byte(s))
	return err == nil
}

// ServeRelay serves h on ln until ctx ends, with timeouts that bound every connection, over TLS when certFile and
// keyFile are both given.
func ServeRelay(ctx context.Context, ln net.Listener, h http.Handler, certFile, keyFile string) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(sctx)
		case <-done:
		}
	}()
	defer close(done)
	var err error
	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			return errors.New("coord: TLS needs both a certificate and a key file")
		}
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		err = srv.ServeTLS(ln, certFile, keyFile)
	} else {
		err = srv.Serve(ln)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
