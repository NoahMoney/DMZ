package xmrswap

// GAP-019: the rendezvous, sealed. The directory transport (channel.go) writes plain JSON, so anyone who can read the
// directory — a sync provider, a shared drive's other users — reads the view key v, S_a, S_b, the hashes, both DERO
// addresses and the Monero txid, and anyone who can write it can put a message in either box. A SealedChannel wraps
// any Channel so that every message is encrypted and authenticated under a key the two parties agreed out of band
// for this swap alone: a reader learns only that messages pass and how large they are, and a writer without the key
// cannot make one that opens.
//
// The construction is the standard library's and nothing else:
//   - the swap key K is 32 random bytes, made by the maker (NewChannelKey) and handed to the taker with the offer
//     over whatever the two already trust. It is a secret of the same order as the state file for the swap's
//     privacy, and of none for its coins: everything that decides coins is still checked against a proof or the chain.
//   - each direction has its own key, HMAC-SHA256(K, "xmrswap channel v1|<from>><to>" || 0x01): HKDF-Expand (RFC
//     5869) for one block, with K itself as the pseudorandom key, which it may be since it is uniformly random.
//   - each message is AES-256-GCM under its direction's key, with a fresh random 96-bit nonce and the direction as
//     associated data. A message copied back to its own sender, or into another swap, does not open.
//
// AES-GCM and HMAC-SHA256 are the standard library's, so the channel needs nothing from outside it; golang.org/x/crypto
// (already required by this module, for Keccak) would have served as well with chacha20poly1305. A random 96-bit nonce
// is safe for far more messages than a swap sends (a few dozen, repeats included, per direction).
//
// What it does not hide: that two parties are talking, when, and how much (the transport's file names carry a
// timestamp and the sender's name). What it does not stop: a writer replaying a sealed message of this swap back into
// the box it came from. That is a duplicate, which the channel already delivers by design, and each side consumes
// each kind once.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// KindSealed is the only kind a sealed channel puts on the transport; the real kind travels inside the ciphertext.
const KindSealed = "sealed"

// ChannelKeyLen is the swap key's length in bytes.
const ChannelKeyLen = 32

// NewChannelKey is a fresh swap key, as hex: the maker makes one per swap and gives it to the taker with the offer.
func NewChannelKey() (string, error) {
	b := make([]byte, ChannelKeyLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ParseChannelKey reads a swap key: exactly 32 bytes, as hex.
func ParseChannelKey(s string) ([]byte, error) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != ChannelKeyLen {
		return nil, fmt.Errorf("a channel key is %d bytes of hex (%d characters)", ChannelKeyLen, 2*ChannelKeyLen)
	}
	return b, nil
}

// SealedChannel is a Channel between exactly two parties whose messages open only under their swap key.
type SealedChannel struct {
	inner      Channel
	me, peer   string
	send, recv cipher.AEAD
	// OnDrop, when set, is told why each message that did not open was dropped.
	OnDrop func(reason string)
}

// NewSealedChannel wraps inner for me, who speaks only to peer, under the swap key.
func NewSealedChannel(inner Channel, key []byte, me, peer string) (*SealedChannel, error) {
	if len(key) != ChannelKeyLen {
		return nil, fmt.Errorf("the channel key must be %d bytes, not %d", ChannelKeyLen, len(key))
	}
	if err := checkPeerName(me); err != nil {
		return nil, err
	}
	if err := checkPeerName(peer); err != nil {
		return nil, err
	}
	if me == peer {
		return nil, errors.New("a sealed channel needs two parties")
	}
	send, err := directionAEAD(key, me, peer)
	if err != nil {
		return nil, err
	}
	recv, err := directionAEAD(key, peer, me)
	if err != nil {
		return nil, err
	}
	return &SealedChannel{inner: inner, me: me, peer: peer, send: send, recv: recv}, nil
}

func directionLabel(from, to string) []byte { return []byte("xmrswap channel v1|" + from + ">" + to) }

// directionAEAD is AES-256-GCM under one direction's key, HMAC-SHA256(K, label || 0x01).
func directionAEAD(key []byte, from, to string) (cipher.AEAD, error) {
	m := hmac.New(sha256.New, key)
	m.Write(directionLabel(from, to))
	m.Write([]byte{1})
	block, err := aes.NewCipher(m.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

type sealedBody struct {
	Nonce string `json:"n"`
	Box   []byte `json:"c"`
}

func (c *SealedChannel) Send(ctx context.Context, to string, msg Message) error {
	if to != c.peer {
		return fmt.Errorf("sealed channel: %s speaks only to %s, not %s", c.me, c.peer, to)
	}
	if msg.Kind == "" || msg.Kind == KindSealed {
		return fmt.Errorf("sealed channel: cannot send a message of kind %q", msg.Kind)
	}
	pt, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	nonce := make([]byte, c.send.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	box := c.send.Seal(nil, nonce, pt, directionLabel(c.me, to))
	body, err := json.Marshal(sealedBody{Nonce: hex.EncodeToString(nonce), Box: box})
	if err != nil {
		return err
	}
	return c.inner.Send(ctx, to, Message{Kind: KindSealed, Body: body})
}

// Recv returns the next message that opens under the swap key from the peer. Anything else in the box — a plain
// message, one sealed under another key or for another direction, one altered on the way — is dropped, and OnDrop
// is told; it never reaches the state machine.
func (c *SealedChannel) Recv(ctx context.Context) (Message, error) {
	for {
		m, err := c.inner.Recv(ctx)
		if err != nil {
			return Message{}, err
		}
		msg, err := c.open(m)
		if err != nil {
			if c.OnDrop != nil {
				c.OnDrop(err.Error())
			}
			continue
		}
		return msg, nil
	}
}

func (c *SealedChannel) open(m Message) (Message, error) {
	if m.Kind != KindSealed {
		return Message{}, fmt.Errorf("dropped a %q message that is not sealed: only a message sealed under this swap's key is read", m.Kind)
	}
	var b sealedBody
	if err := json.Unmarshal(m.Body, &b); err != nil {
		return Message{}, errors.New("dropped a sealed message whose envelope does not parse")
	}
	nonce, err := hex.DecodeString(b.Nonce)
	if err != nil || len(nonce) != c.recv.NonceSize() {
		return Message{}, errors.New("dropped a sealed message with a malformed nonce")
	}
	pt, err := c.recv.Open(nil, nonce, b.Box, directionLabel(c.peer, c.me))
	if err != nil {
		return Message{}, fmt.Errorf("dropped a sealed message that does not open as %s's under this swap's key: written by someone else, or altered", c.peer)
	}
	var msg Message
	if err := json.Unmarshal(pt, &msg); err != nil || msg.Kind == "" || msg.Kind == KindSealed {
		return Message{}, errors.New("dropped a sealed message whose contents are not a message")
	}
	return msg, nil
}
