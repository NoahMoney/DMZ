package zcashtx

import (
	"errors"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

// Signing. Zcash's transparent side signs with ECDSA over secp256k1, DER-encoded, low-S, with the SIGHASH type
// appended; the companion signs one thing only — the claim or the refund of a swap script — with a key the
// user hands it for that purpose (docs/ZEC_SWAP.md §7). The implementation is decred's pure-Go secp256k1
// (github.com/decred/dcrd/dcrec/secp256k1/v4), the one new dependency this market adds; the written answer to
// "what does it provide that a smaller primitive cannot" is in docs/ZEC_SWAP.md §7: a constant-time field and
// scalar arithmetic and RFC 6979 nonces, which a hand-written curve would get wrong in ways no test here would
// catch. golang.org/x/crypto has no secp256k1.

// SigHashAll is the only signature hash type the companion uses: every input and every output committed.
const SigHashAll byte = 0x01

// Key is a secp256k1 private key held in memory for the duration of one spend.
type Key struct{ priv *secp256k1.PrivateKey }

// ParseKey reads a 32-byte scalar and refuses one outside the curve order or zero, rather than reducing it
// silently — a reduced key would sign for a public key the user never derived.
func ParseKey(b [32]byte) (*Key, error) {
	var s secp256k1.ModNScalar
	if overflow := s.SetBytes(&b); overflow != 0 {
		return nil, errors.New("zcashtx: private key is not below the curve order")
	}
	if s.IsZero() {
		return nil, errors.New("zcashtx: private key is zero")
	}
	return &Key{priv: secp256k1.NewPrivateKey(&s)}, nil
}

// PubKey is the compressed 33-byte public key, the form the spend pushes and whose HASH160 the script names.
func (k *Key) PubKey() []byte { return k.priv.PubKey().SerializeCompressed() }

// PubKeyHash is HASH160 of the compressed public key: the claim or refund key hash in the script.
func (k *Key) PubKeyHash() [20]byte { return Hash160(k.PubKey()) }

// Sign produces the DER signature with SigHashAll appended, as a scriptSig pushes it. RFC 6979 nonces, low S.
func (k *Key) Sign(digest [32]byte) []byte {
	sig := ecdsa.Sign(k.priv, digest[:])
	return append(sig.Serialize(), SigHashAll)
}

// Zero overwrites the key material.
func (k *Key) Zero() { k.priv.Zero() }

// VerifySig checks a scriptSig-form signature (DER plus the type byte) against a compressed public key and the
// digest it should have signed. Only SigHashAll is accepted.
func VerifySig(sigWithType, pubkey []byte, digest [32]byte) bool {
	if len(sigWithType) < 9 || sigWithType[len(sigWithType)-1] != SigHashAll {
		return false
	}
	sig, err := ecdsa.ParseDERSignature(sigWithType[:len(sigWithType)-1])
	if err != nil {
		return false
	}
	pub, err := secp256k1.ParsePubKey(pubkey)
	if err != nil {
		return false
	}
	return sig.Verify(digest[:], pub)
}
