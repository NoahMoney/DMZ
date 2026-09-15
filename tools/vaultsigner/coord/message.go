package coord

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/hpke"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// The wire format, version 1. Every integer is big-endian; every length is checked; nothing may follow the signature.
//
//	header   magic "DMZC" | version 1 | suite 1
//	         vault (32)  the vault scid
//	         tag (32)    SHA-256("DMZ coord round v1" 0x00 | vault | round)
//	         sender (32) the sender's Ed25519 public key
//	         idlen (1) | id    the sender's canonical DERO address
//	         created (8) | expires (8)   unix nanoseconds
//	         -- the wrap context ends here --
//	         n (1) | n × { recipient Ed25519 key (32) | wrap (80) }
//	         nonce (12)
//	body     ctlen (4) | AES-256-GCM(K, nonce, payload, AD = SHA-256("DMZ coord payload v1" 0x00 | header))
//	sig      Ed25519ctx(sender key, context "DMZ coord msg v1", every byte above)
//
// wrap = HPKE base mode (RFC 9180) DHKEM(X25519, HKDF-SHA256) / HKDF-SHA256 / AES-256-GCM, sealing the 32-byte content
// key K to the recipient's X25519 key, with info = SHA-256("DMZ coord wrap v1" 0x00 | SHA-256(wrap context) |
// recipient Ed25519 key): 32 bytes of encapsulated key and 48 of sealed K. K is fresh for every message.
const (
	magic       = "DMZC"
	wireVersion = 1
	suiteV1     = 1
	ctxMessage  = "DMZ coord msg v1"

	// MaxRoundLen bounds a round name.
	MaxRoundLen = 200
	// MaxRecipients bounds the signer set a message is encrypted to (vault.bas seats at most 20 active signers).
	MaxRecipients = 32
	// DefaultMaxPayload is the payload limit a node and a relay take when none is configured; HardMaxPayload is the
	// most either accepts at all.
	DefaultMaxPayload = 4 << 20
	HardMaxPayload    = 16 << 20
	// MaxLifetime is the longest a message may live (expires − created), whatever a node's TTL says.
	MaxLifetime = 7 * 24 * time.Hour

	wrapLen          = 32 + 32 + 16
	nonceLen         = 12
	sigLen           = ed25519.SignatureSize
	envelopeOverhead = 8192 // ≥ every header field at its maximum, the GCM tag and the signature
	minMessage       = 4 + 2 + 32*3 + 1 + 1 + 8 + 8 + 1 + (32 + wrapLen) + nonceLen + 4 + 16 + sigLen
)

// maxMessage is the largest message that can carry a payload of maxPayload bytes.
func maxMessage(maxPayload int) int { return maxPayload + envelopeOverhead }

// ValidRound says whether a round name can be carried: 1 to MaxRoundLen bytes of letters, digits and ._:/@+=-
// (for example "zec/release/12/sig/0" or "xmr/info/3"). Anything else is refused rather than escaped.
func ValidRound(round string) error {
	if len(round) == 0 || len(round) > MaxRoundLen {
		return fmt.Errorf("coord: a round name is 1 to %d bytes, not %d", MaxRoundLen, len(round))
	}
	for i := 0; i < len(round); i++ {
		c := round[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.IndexByte("._:/@+=-", c) >= 0 {
			continue
		}
		return fmt.Errorf("coord: round %q: byte %d is not allowed (letters, digits and ._:/@+=- only)", clip(round, 60), i)
	}
	return nil
}

// roundTag is what stores file a round under. A store never sees the round name, and a message filed under another
// round's tag does not open there, because the tag is inside what the sender signed.
func roundTag(vault [32]byte, round string) [32]byte {
	h := sha256.New()
	h.Write([]byte("DMZ coord round v1\x00"))
	h.Write(vault[:])
	h.Write([]byte(round))
	var t [32]byte
	h.Sum(t[:0])
	return t
}

type slot struct {
	key  [32]byte // the recipient's Ed25519 public key
	wrap [wrapLen]byte
}

// message is one parsed (not yet verified) message. raw is the exact bytes received.
type message struct {
	vault, tag, sender [32]byte
	senderID           string
	created, expires   int64
	slots              []slot
	nonce              [nonceLen]byte
	ct                 []byte
	raw                []byte
	ctxEnd, hdrEnd     int
}

func wrapInfo(wrapCtx []byte, recipient [32]byte) []byte {
	c := sha256.Sum256(wrapCtx)
	h := sha256.New()
	h.Write([]byte("DMZ coord wrap v1\x00"))
	h.Write(c[:])
	h.Write(recipient[:])
	return h.Sum(nil)
}

func payloadAD(header []byte) []byte {
	h := sha256.New()
	h.Write([]byte("DMZ coord payload v1\x00"))
	h.Write(header)
	return h.Sum(nil)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// seal builds and signs one message from me for the round, encrypted to every identity in to.
func seal(me *Identity, vault [32]byte, round string, payload []byte, to []PublicIdentity, created, expires int64) ([]byte, error) {
	if len(to) == 0 || len(to) > MaxRecipients {
		return nil, fmt.Errorf("coord: a message is encrypted to 1 to %d signers, not %d", MaxRecipients, len(to))
	}
	if created <= 0 || expires <= created {
		return nil, errors.New("coord: a message must expire after it was created")
	}
	if len(me.pub.ID) == 0 || len(me.pub.ID) > maxIDLen {
		return nil, errors.New("coord: the sender id is not a canonical address")
	}
	tag := roundTag(vault, round)
	b := make([]byte, 0, envelopeOverhead+len(payload)+16)
	b = append(b, magic...)
	b = append(b, wireVersion, suiteV1)
	b = append(b, vault[:]...)
	b = append(b, tag[:]...)
	b = append(b, me.pub.Sign...)
	b = append(b, byte(len(me.pub.ID)))
	b = append(b, me.pub.ID...)
	b = binary.BigEndian.AppendUint64(b, uint64(created))
	b = binary.BigEndian.AppendUint64(b, uint64(expires))
	ctxEnd := len(b)

	key := make([]byte, 32)
	defer clear(key)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	b = append(b, byte(len(to)))
	seen := make(map[[32]byte]bool, len(to))
	for _, p := range to {
		var rk [32]byte
		copy(rk[:], p.Sign)
		if seen[rk] {
			return nil, fmt.Errorf("coord: %s appears twice among the recipients", p.ID)
		}
		seen[rk] = true
		pk, err := kem.NewPublicKey(p.KEM)
		if err != nil {
			return nil, fmt.Errorf("coord: the encryption key pinned for %s: %v", p.ID, err)
		}
		w, err := hpke.Seal(pk, hpke.HKDFSHA256(), hpke.AES256GCM(), wrapInfo(b[:ctxEnd], rk), key)
		if err != nil {
			return nil, fmt.Errorf("coord: encrypting to %s: %v", p.ID, err)
		}
		if len(w) != wrapLen {
			return nil, fmt.Errorf("coord: a key wrap is %d bytes, not %d", len(w), wrapLen)
		}
		b = append(b, rk[:]...)
		b = append(b, w...)
	}
	var nonce [nonceLen]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	b = append(b, nonce[:]...)
	hdrEnd := len(b)

	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	ad := payloadAD(b[:hdrEnd])
	b = binary.BigEndian.AppendUint32(b, uint32(len(payload)+gcm.Overhead()))
	b = gcm.Seal(b, nonce[:], payload, ad)
	sig, err := me.sign.Sign(nil, b, &ed25519.Options{Context: ctxMessage})
	if err != nil {
		return nil, err
	}
	return append(b, sig...), nil
}

// cursor reads fields off a byte slice; a read past the end sets bad and returns zeroes, checked once at the end.
type cursor struct {
	b   []byte
	off int
	bad bool
}

func (c *cursor) take(n int) []byte {
	if c.bad || n < 0 || n > len(c.b)-c.off {
		c.bad = true
		return nil
	}
	s := c.b[c.off : c.off+n]
	c.off += n
	return s
}

func (c *cursor) byte1() byte {
	s := c.take(1)
	if s == nil {
		return 0
	}
	return s[0]
}

func (c *cursor) u32() uint32 {
	s := c.take(4)
	if s == nil {
		return 0
	}
	return binary.BigEndian.Uint32(s)
}

func (c *cursor) u64() uint64 {
	s := c.take(8)
	if s == nil {
		return 0
	}
	return binary.BigEndian.Uint64(s)
}

var (
	errMalformed = errors.New("malformed or truncated")
	errTooLarge  = errors.New("larger than the message limit")
)

// parse reads a message strictly: the right magic, version and suite, every length within bounds, distinct
// recipients, and not one byte more than the fields. It does not verify the signature or decrypt anything.
func parse(raw []byte, maxLen int) (*message, error) {
	if len(raw) > maxLen {
		return nil, errTooLarge
	}
	if len(raw) < minMessage {
		return nil, errMalformed
	}
	c := &cursor{b: raw}
	m := &message{raw: raw}
	if string(c.take(4)) != magic {
		return nil, errMalformed
	}
	if v := c.take(2); v == nil || v[0] != wireVersion || v[1] != suiteV1 {
		return nil, errors.New("an unknown version or suite")
	}
	copy(m.vault[:], c.take(32))
	copy(m.tag[:], c.take(32))
	copy(m.sender[:], c.take(32))
	idLen := int(c.byte1())
	id := c.take(idLen)
	created, expires := c.u64(), c.u64()
	m.ctxEnd = c.off
	n := int(c.byte1())
	if c.bad || idLen == 0 || idLen > maxIDLen || n == 0 || n > MaxRecipients {
		return nil, errMalformed
	}
	for _, ch := range id {
		if ch < 0x21 || ch > 0x7e {
			return nil, errMalformed
		}
	}
	m.senderID = string(id)
	if created == 0 || created > math.MaxInt64 || expires > math.MaxInt64 || expires <= created {
		return nil, errMalformed
	}
	m.created, m.expires = int64(created), int64(expires)
	m.slots = make([]slot, n)
	for i := range m.slots {
		copy(m.slots[i].key[:], c.take(32))
		copy(m.slots[i].wrap[:], c.take(wrapLen))
		for j := 0; j < i; j++ {
			if m.slots[j].key == m.slots[i].key {
				return nil, errMalformed
			}
		}
	}
	copy(m.nonce[:], c.take(nonceLen))
	m.hdrEnd = c.off
	ctLen := int(c.u32())
	m.ct = c.take(ctLen)
	c.take(sigLen)
	if c.bad || c.off != len(raw) || ctLen < 16 {
		return nil, errMalformed
	}
	return m, nil
}

// verify checks the signature under pub over every byte before it.
func (m *message) verify(pub ed25519.PublicKey) bool {
	if len(pub) != ed25519.PublicKeySize || len(m.raw) < sigLen {
		return false
	}
	signed, sig := m.raw[:len(m.raw)-sigLen], m.raw[len(m.raw)-sigLen:]
	return ed25519.VerifyWithOptions(pub, signed, sig, &ed25519.Options{Context: ctxMessage}) == nil
}

var (
	errNotRecipient = errors.New("not encrypted to this signer")
	errNoDecrypt    = errors.New("does not decrypt")
)

// open decrypts the payload with me's key. A message whose recipient list omits me, or whose wrap or ciphertext was
// altered, does not open.
func (m *message) open(me *Identity) ([]byte, error) {
	var mine [32]byte
	copy(mine[:], me.pub.Sign)
	for i := range m.slots {
		if m.slots[i].key != mine {
			continue
		}
		k, err := hpke.Open(me.kem, hpke.HKDFSHA256(), hpke.AES256GCM(), wrapInfo(m.raw[:m.ctxEnd], mine), m.slots[i].wrap[:])
		if err != nil || len(k) != 32 {
			clear(k)
			return nil, errNoDecrypt
		}
		defer clear(k)
		gcm, err := newGCM(k)
		if err != nil {
			return nil, errNoDecrypt
		}
		pt, err := gcm.Open(nil, m.nonce[:], m.ct, payloadAD(m.raw[:m.hdrEnd]))
		if err != nil {
			return nil, errNoDecrypt
		}
		return pt, nil
	}
	return nil, errNotRecipient
}

// recipientsAre says whether the message is encrypted to exactly the given keys.
func (m *message) recipientsAre(keys map[[32]byte]PublicIdentity) bool {
	if len(m.slots) != len(keys) {
		return false
	}
	for _, s := range m.slots {
		if _, ok := keys[s.key]; !ok {
			return false
		}
	}
	return true
}

// newer orders two copies of one sender's message for one round: the later creation time wins, and the larger
// SHA-256 of the bytes breaks a tie, so every node and every store picks the same one.
func newer(created int64, hash [32]byte, thanCreated int64, thanHash [32]byte) bool {
	if created != thanCreated {
		return created > thanCreated
	}
	return bytes.Compare(hash[:], thanHash[:]) > 0
}
