package xmrswap

// MT-B25 (L1 of the proof audit): a secret revealed unreduced — x = s + kℓ — is the same key share, and the companion
// recovers it instead of stalling on it; a value whose reduction is not the committed share is still refused.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"testing"
)

// groupOrder is ℓ = 2^252 + 27742317777372353535851937790883648493.
var groupOrder = func() *big.Int {
	c, _ := new(big.Int).SetString("27742317777372353535851937790883648493", 10)
	return new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 252), c)
}()

// unreduced is the 32-byte little-endian encoding of s + k·ℓ, or false when it does not fit in 32 bytes.
func unreduced(s Scalar, k int64) ([]byte, bool) {
	be := make([]byte, 32)
	for i := range s {
		be[31-i] = s[i]
	}
	x := new(big.Int).Add(new(big.Int).SetBytes(be), new(big.Int).Mul(big.NewInt(k), groupOrder))
	if x.BitLen() > 256 {
		return nil, false
	}
	out := make([]byte, 32)
	xb := x.Bytes()
	for i := range xb {
		out[i] = xb[len(xb)-1-i]
	}
	return out, true
}

func TestARevealedShareNotReducedModLStillOpensTheKey(t *testing.T) {
	for _, k := range []int64{1, 15} {
		var s Scalar
		var x []byte
		for {
			var err error
			if s, err = NewScalar(rand.Reader); err != nil {
				t.Fatal(err)
			}
			var ok bool
			if x, ok = unreduced(s, k); ok {
				break // s + 15ℓ fits in 32 bytes only when s is small enough; draw again until it does
			}
		}
		xh, h, pub := hex.EncodeToString(x), SHA256Hex(x), s.Public().Hex()
		if _, err := ParseScalar(xh); !errors.Is(err, ErrNotReduced) {
			t.Fatalf("s + %dℓ parsed as a scalar (%v): the case under test is not unreduced", k, err)
		}
		for _, form := range []string{xh, strings.ToUpper(xh)} {
			got, err := RecoverShare(form, h, pub)
			if err != nil || got != s {
				t.Fatalf("s + %dℓ: recovered %s (%v), want %s", k, got.Hex(), err, s.Hex())
			}
		}
		// the swap's own path: bob reads it off a Claim, keeps s, and s_a + s_b opens the joint key
		sw := zzRefSwap(t, &zzRefW{})
		sw.st.Peer.H, sw.st.Peer.S = h, pub
		if err := sw.checkRevealed(xh); err != nil || sw.st.Revealed != s.Hex() {
			t.Fatalf("s + %dℓ on the chain: %v, kept %q", k, err, sw.st.Revealed)
		}
		own, _ := ParseScalar(sw.st.Own.Secret)
		if key, err := sw.spendKey(); err != nil || key != AddScalars(own, s).Hex() {
			t.Fatalf("the joint key from s + %dℓ: %v", k, err)
		}
		t.Logf("s + %dℓ recovered", k)
	}
	// a value whose reduction is another share is refused, though it hashes to its own H
	s, _ := NewScalar(rand.Reader)
	other, _ := NewScalar(rand.Reader)
	x, _ := unreduced(other, 1)
	if _, err := RecoverShare(hex.EncodeToString(x), SHA256Hex(x), s.Public().Hex()); err == nil {
		t.Fatal("a value reducing to another share was taken")
	}
	// and one that does not hash to the committed H is refused, reduced or not
	y, _ := unreduced(s, 1)
	if _, err := RecoverShare(hex.EncodeToString(y), SHA256Hex(s[:]), s.Public().Hex()); err == nil {
		t.Fatal("s + ℓ was taken against the hash of s")
	}
	if _, err := RecoverShare("abcd", SHA256Hex([]byte{0xab, 0xcd}), s.Public().Hex()); err == nil {
		t.Fatal("two bytes were taken as a share")
	}
}
