package zcashvault

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"

	"veil/tools/zcashtx"
)

// BIP 32 hierarchical deterministic keys over secp256k1, as the vault's signers use them.
//
// Each signer holds its own seed and publishes ONE extended public key: its account key, at a hardened path under
// its master key. Every vault address is then derived from the n published account keys by NON-hardened steps only,
// so every party — the signers, the app, any auditor — derives the same pay-to-script-hash address for vault index
// i from public data alone, and no signer can derive another's private key.
//
// The one thing non-hardened derivation gives away, and why it is acceptable here: a child private key together
// with its parent's extended public key reveals the parent private key (BIP 32, "Security"). A signer's child keys
// never leave its process, and a signer whose child key leaked has lost its account key, which is one share of k;
// the per-vault branch below keeps even that to one vault.
//
// The implementation is BIP 32's text, checked against its test vectors 1 to 5 (bip32_vectors_test.go). The curve
// arithmetic is decred's constant-time secp256k1, the module's existing dependency (tools/go.mod); nothing new is
// added.

// HardenedOffset is the first hardened child index (2^31).
const HardenedOffset = 0x80000000

// Extended key version bytes (BIP 32 §Serialization format): mainnet xpub/xprv, testnet tpub/tprv. Zcash has no
// transparent-specific versions of its own; regtest uses testnet's, as its addresses do.
var (
	versionXprvMain = [4]byte{0x04, 0x88, 0xAD, 0xE4}
	versionXpubMain = [4]byte{0x04, 0x88, 0xB2, 0x1E}
	versionXprvTest = [4]byte{0x04, 0x35, 0x83, 0x94}
	versionXpubTest = [4]byte{0x04, 0x35, 0x87, 0xCF}
)

// ExtKey is an extended key: a private one (priv set) or a public one (priv nil).
type ExtKey struct {
	version  [4]byte
	depth    byte
	parentFP [4]byte
	child    uint32
	chain    [32]byte
	priv     *secp256k1.ModNScalar
	pub      *secp256k1.PublicKey
}

// NewMaster is BIP 32's master key generation: I = HMAC-SHA512("Bitcoin seed", seed), the key is I_L and the chain
// code I_R. A seed of 16 to 64 bytes is what BIP 32 allows; an I_L of zero or at least the curve order is refused.
func NewMaster(seed []byte, testnet bool) (*ExtKey, error) {
	if len(seed) < 16 || len(seed) > 64 {
		return nil, fmt.Errorf("zcashvault: a BIP 32 seed is 16 to 64 bytes, not %d", len(seed))
	}
	mac := hmac.New(sha512.New, []byte("Bitcoin seed"))
	mac.Write(seed)
	I := mac.Sum(nil)
	defer wipe(I)
	var k secp256k1.ModNScalar
	if overflow := k.SetByteSlice(I[:32]); overflow || k.IsZero() {
		return nil, errors.New("zcashvault: this seed gives an invalid master key (BIP 32: use another seed)")
	}
	m := &ExtKey{version: versionXprvMain, priv: &k}
	if testnet {
		m.version = versionXprvTest
	}
	copy(m.chain[:], I[32:])
	m.pub = secp256k1.NewPrivateKey(&k).PubKey()
	return m, nil
}

// IsPrivate says whether the key holds its private half.
func (k *ExtKey) IsPrivate() bool { return k.priv != nil }

// PubKey is the compressed 33-byte public key.
func (k *ExtKey) PubKey() []byte { return k.pub.SerializeCompressed() }

// Testnet says whether the key carries testnet version bytes.
func (k *ExtKey) Testnet() bool { return k.version == versionXprvTest || k.version == versionXpubTest }

// Child is CKDpriv for a private key (hardened or not) and CKDpub for a public one (non-hardened only).
func (k *ExtKey) Child(i uint32) (*ExtKey, error) {
	if k.depth == 255 {
		return nil, errors.New("zcashvault: an extended key at depth 255 has no children")
	}
	var data []byte
	if i >= HardenedOffset {
		if k.priv == nil {
			return nil, errors.New("zcashvault: a hardened child cannot be derived from a public key")
		}
		var kb [32]byte
		k.priv.PutBytes(&kb)
		data = append([]byte{0}, kb[:]...)
		wipe(kb[:])
	} else {
		data = k.pub.SerializeCompressed()
	}
	data = binary.BigEndian.AppendUint32(data, i)
	mac := hmac.New(sha512.New, k.chain[:])
	mac.Write(data)
	wipe(data)
	I := mac.Sum(nil)
	defer wipe(I)

	var il secp256k1.ModNScalar
	if overflow := il.SetByteSlice(I[:32]); overflow {
		// probability below 2^-127; BIP 32 says to proceed with the next index, and the caller decides what that means
		return nil, fmt.Errorf("zcashvault: child %d is invalid (I_L is not below the curve order)", i)
	}
	c := &ExtKey{version: k.version, depth: k.depth + 1, child: i}
	copy(c.chain[:], I[32:])
	fp := zcashtx.Hash160(k.pub.SerializeCompressed())
	copy(c.parentFP[:], fp[:4])
	if k.priv != nil {
		var s secp256k1.ModNScalar
		s.Set(&il)
		s.Add(k.priv)
		if s.IsZero() {
			return nil, fmt.Errorf("zcashvault: child %d is invalid (the key is zero)", i)
		}
		c.priv = &s
		c.pub = secp256k1.NewPrivateKey(&s).PubKey()
		il.Zero()
		return c, nil
	}
	var ilG, parent, sum secp256k1.JacobianPoint
	secp256k1.ScalarBaseMultNonConst(&il, &ilG)
	k.pub.AsJacobian(&parent)
	secp256k1.AddNonConst(&ilG, &parent, &sum)
	if (sum.X.IsZero() && sum.Y.IsZero()) || sum.Z.IsZero() {
		return nil, fmt.Errorf("zcashvault: child %d is invalid (the point at infinity)", i)
	}
	sum.ToAffine()
	c.pub = secp256k1.NewPublicKey(&sum.X, &sum.Y)
	return c, nil
}

// Path derives a chain of children, e.g. Path(44|H, 133|H, 0|H).
func (k *ExtKey) Path(indexes ...uint32) (*ExtKey, error) {
	cur := k
	for _, i := range indexes {
		next, err := cur.Child(i)
		if err != nil {
			return nil, err
		}
		cur = next
	}
	return cur, nil
}

// Neuter is the public half, with the public version bytes.
func (k *ExtKey) Neuter() *ExtKey {
	n := &ExtKey{version: k.version, depth: k.depth, parentFP: k.parentFP, child: k.child, chain: k.chain, pub: k.pub}
	switch k.version {
	case versionXprvMain:
		n.version = versionXpubMain
	case versionXprvTest:
		n.version = versionXpubTest
	}
	return n
}

// XPub is the serialized extended public key (xpub… or tpub…), whatever k holds.
func (k *ExtKey) XPub() string { return k.Neuter().serialize() }

// serialize is BIP 32's 78-byte form in Base58Check. For a private key the string is the secret: only the tests
// call it on one, and nothing in this package ever prints or logs it.
func (k *ExtKey) serialize() string {
	b := make([]byte, 0, 78)
	b = append(b, k.version[:]...)
	b = append(b, k.depth)
	b = append(b, k.parentFP[:]...)
	b = binary.BigEndian.AppendUint32(b, k.child)
	b = append(b, k.chain[:]...)
	if k.priv != nil {
		var kb [32]byte
		k.priv.PutBytes(&kb)
		b = append(b, 0)
		b = append(b, kb[:]...)
		wipe(kb[:])
	} else {
		b = append(b, k.pub.SerializeCompressed()...)
	}
	s := zcashtx.Base58Check(b)
	wipe(b)
	return s
}

// ParseExtKey reads a serialized extended key and refuses every invalid form BIP 32's vector 5 names: a version that
// does not match the key's kind, a bad key prefix, a private key outside 1..n−1, a public key not on the curve, depth
// 0 with a parent fingerprint or an index, an unknown version, and a bad checksum.
func ParseExtKey(s string) (*ExtKey, error) {
	if len(s) > 120 {
		return nil, errors.New("zcashvault: too long for an extended key")
	}
	payload, err := zcashtx.DecodeBase58Check(s)
	if err != nil {
		return nil, fmt.Errorf("zcashvault: extended key: %w", err)
	}
	defer wipe(payload)
	if len(payload) != 78 {
		return nil, fmt.Errorf("zcashvault: an extended key is 78 bytes, not %d", len(payload))
	}
	k := &ExtKey{depth: payload[4], child: binary.BigEndian.Uint32(payload[9:13])}
	copy(k.version[:], payload[0:4])
	copy(k.parentFP[:], payload[5:9])
	copy(k.chain[:], payload[13:45])
	key := payload[45:78]
	var private bool
	switch k.version {
	case versionXprvMain, versionXprvTest:
		private = true
	case versionXpubMain, versionXpubTest:
	default:
		return nil, fmt.Errorf("zcashvault: unknown extended key version %x", k.version)
	}
	if k.depth == 0 && (k.parentFP != [4]byte{} || k.child != 0) {
		return nil, errors.New("zcashvault: a depth-0 extended key with a parent fingerprint or a child index")
	}
	if private {
		if key[0] != 0 {
			return nil, errors.New("zcashvault: a private extended key whose key does not start 0x00")
		}
		var sc secp256k1.ModNScalar
		if overflow := sc.SetByteSlice(key[1:]); overflow || sc.IsZero() {
			return nil, errors.New("zcashvault: a private key outside 1..n-1")
		}
		k.priv = &sc
		k.pub = secp256k1.NewPrivateKey(&sc).PubKey()
	} else {
		if key[0] != 2 && key[0] != 3 {
			return nil, errors.New("zcashvault: a public extended key that is not a compressed point")
		}
		pub, err := secp256k1.ParsePubKey(key)
		if err != nil {
			return nil, fmt.Errorf("zcashvault: the public key is not on the curve: %v", err)
		}
		k.pub = pub
	}
	if got := k.serialize(); got != s {
		return nil, errors.New("zcashvault: not the canonical spelling of that extended key")
	}
	return k, nil
}

// signingKey is the child's private key in the form zcashtx signs with. The caller zeroes it.
func (k *ExtKey) signingKey() (*zcashtx.Key, error) {
	if k.priv == nil {
		return nil, errors.New("zcashvault: a public key cannot sign")
	}
	var kb [32]byte
	k.priv.PutBytes(&kb)
	defer wipe(kb[:])
	return zcashtx.ParseKey(kb)
}

// Zero overwrites the private half, if there is one.
func (k *ExtKey) Zero() {
	if k.priv != nil {
		k.priv.Zero()
		k.priv = nil
	}
	wipe(k.chain[:])
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// equalPub says whether two extended keys are the same public key and chain code, whatever their metadata.
func equalPub(a, b *ExtKey) bool {
	return bytes.Equal(a.PubKey(), b.PubKey()) && a.chain == b.chain
}
