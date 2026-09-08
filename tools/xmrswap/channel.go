package xmrswap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Channel is the rendezvous: the only thing the two companions say to each other. spore is the intended
// production transport; a directory of JSON files is the test one, and enough for two people who can exchange
// files. Nothing on it is trusted — every value that matters is checked against a proof or against the chain
// — and nothing on it is hosted by anyone.
//
// Recv blocks until a message arrives or ctx ends, and returns ctx.Err() in the second case; a machine that
// wants to poll gives it a deadline. Delivery is at-least-once (a sender repeats its last message until the
// protocol moves on) and a receiver drops what it has already consumed.
type Channel interface {
	Send(ctx context.Context, to string, msg Message) error
	Recv(ctx context.Context) (Message, error)
}

// Message is one envelope. The kinds are the protocol's seven steps; the Body is the kind's JSON, and only the
// fields §1 lists ever go in one: a key share is revealed on chain, never here, and k_b travels only as "gate".
type Message struct {
	Kind string          `json:"kind"`
	Body json.RawMessage `json:"body"`
}

// Message kinds, in protocol order.
const (
	KindOffer      = "offer"       // Bob → Alice: amounts, asset, network, the timelock widths he will use
	KindAliceSetup = "alice_setup" // Alice → Bob: S_a, H_a, π_a, V, v, her DERO address
	KindBobSetup   = "bob_setup"   // Bob → Alice: S_b, H_b, π_b, H_k
	KindDeroLocked = "dero_locked" // Bob → Alice: the lock id (she reads the lock itself from the chain)
	KindXmrLocked  = "xmr_locked"  // Alice → Bob: her Monero txid (he watches his own wallet, not this)
	KindGate       = "gate"        // Bob → Alice: k_b, after ≥ 10 confirmations of her lock
	KindDone       = "done"        // either → other: how it ended, for the screen
)

// DirChannel delivers messages as files: Send writes <dir>/<to>/<name>.json, Recv reads the oldest file in
// <dir>/<me>/ and removes it. A file is written under a temporary name and renamed into place, so a reader
// never sees half a message. The names sort by (time, sequence), so order is preserved per direction.
type DirChannel struct {
	Dir  string
	Me   string
	Poll time.Duration
	seq  uint64
}

// NewDirChannel creates <dir>/<me> and returns the transport. me and every `to` are plain names (bob, alice).
func NewDirChannel(dir, me string) (*DirChannel, error) {
	if err := checkPeerName(me); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, me), 0o700); err != nil {
		return nil, err
	}
	return &DirChannel{Dir: dir, Me: me, Poll: 200 * time.Millisecond}, nil
}

func checkPeerName(n string) error {
	if n == "" || strings.ContainsAny(n, `/\`) || n == "." || n == ".." || strings.HasPrefix(n, ".") {
		return fmt.Errorf("peer name %q: must be a plain directory name", n)
	}
	return nil
}

func (d *DirChannel) Send(ctx context.Context, to string, msg Message) error {
	if err := checkPeerName(to); err != nil {
		return err
	}
	if msg.Kind == "" {
		return errors.New("send: a message needs a kind")
	}
	if len(msg.Body) == 0 {
		msg.Body = json.RawMessage("null")
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	box := filepath.Join(d.Dir, to)
	if err := os.MkdirAll(box, 0o700); err != nil {
		return err
	}
	var nonce [4]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	seq := atomic.AddUint64(&d.seq, 1)
	name := fmt.Sprintf("%020d-%06d-%s-%s.json", time.Now().UnixNano(), seq, d.Me, hex.EncodeToString(nonce[:]))
	tmp := filepath.Join(box, ".tmp-"+name)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, filepath.Join(box, name))
}

// Recv returns the oldest message in this peer's box. A file that is not a message (a stray, a truncated copy
// somebody made by hand) is renamed aside with a ".bad" suffix rather than blocking every message behind it.
func (d *DirChannel) Recv(ctx context.Context) (Message, error) {
	poll := d.Poll
	if poll <= 0 {
		poll = 200 * time.Millisecond
	}
	box := filepath.Join(d.Dir, d.Me)
	for {
		entries, err := os.ReadDir(box)
		if err != nil {
			return Message{}, err
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			n := e.Name()
			if e.IsDir() || !strings.HasSuffix(n, ".json") || strings.HasPrefix(n, ".") {
				continue
			}
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			path := filepath.Join(box, n)
			b, err := os.ReadFile(path)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue // taken by another reader of the same box
				}
				return Message{}, err
			}
			var m Message
			if err := json.Unmarshal(b, &m); err != nil || m.Kind == "" {
				_ = os.Rename(path, path+".bad")
				continue
			}
			if err := os.Remove(path); err != nil {
				return Message{}, err
			}
			return m, nil
		}
		if err := sleep(ctx, poll); err != nil {
			return Message{}, err
		}
	}
}
