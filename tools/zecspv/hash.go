package zecspv

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
)

// Hash is a 32-byte digest in internal byte order: the order it has inside a header and inside the Merkle tree.
// Node RPC and block explorers print block hashes and txids reversed; String and ParseHash use that display order.
type Hash [32]byte

// String is the display (RPC) form: the bytes reversed, in hex.
func (h Hash) String() string {
	r := h
	reverse(r[:])
	return hex.EncodeToString(r[:])
}

// ParseHash reads a hash in display (RPC) order, as getblockhash, getblock and getrawtransaction print it.
func ParseHash(s string) (Hash, error) {
	var h Hash
	if len(s) != 2*len(h) {
		return Hash{}, errors.New("zecspv: a hash is 64 hex characters")
	}
	if _, err := hex.Decode(h[:], []byte(s)); err != nil {
		return Hash{}, fmt.Errorf("zecspv: hash: %w", err)
	}
	reverse(h[:])
	return h, nil
}

// value reads the hash as a 256-bit little-endian integer, the form the difficulty filter compares (§7.7.2).
func (h Hash) value() *big.Int {
	r := h
	reverse(r[:])
	return new(big.Int).SetBytes(r[:])
}

func reverse(b []byte) {
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
}

// sha256d is SHA-256 applied twice to the concatenation of parts.
func sha256d(parts ...[]byte) Hash {
	s := sha256.New()
	for _, p := range parts {
		s.Write(p)
	}
	var first [32]byte
	return sha256.Sum256(s.Sum(first[:0]))
}
