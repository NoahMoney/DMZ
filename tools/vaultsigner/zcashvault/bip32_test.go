package zcashvault

import (
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
)

func parsePath(t *testing.T, p string) []uint32 {
	t.Helper()
	parts := strings.Split(p, "/")
	if parts[0] != "m" {
		t.Fatalf("path %q", p)
	}
	var out []uint32
	for _, s := range parts[1:] {
		h := strings.HasSuffix(s, "H")
		n, err := strconv.ParseUint(strings.TrimSuffix(s, "H"), 10, 32)
		if err != nil {
			t.Fatalf("path %q: %v", p, err)
		}
		if h {
			n += HardenedOffset
		}
		out = append(out, uint32(n))
	}
	return out
}

// BIP 32 vectors 1 to 4: every private and public extended key at every listed path, from the seed.
func TestBIP32Vectors(t *testing.T) {
	for _, v := range bip32Vectors {
		seed, err := hex.DecodeString(v.seed)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMaster(seed, false)
		if err != nil {
			t.Fatal(err)
		}
		k, err := m.Path(parsePath(t, v.path)...)
		if err != nil {
			t.Fatalf("%s: %v", v.path, err)
		}
		if got := k.serialize(); got != v.prv {
			t.Fatalf("seed %s…, %s: xprv\n got %s\nwant %s", v.seed[:8], v.path, got, v.prv)
		}
		if got := k.XPub(); got != v.pub {
			t.Fatalf("seed %s…, %s: xpub\n got %s\nwant %s", v.seed[:8], v.path, got, v.pub)
		}
		// both strings parse back to themselves
		for _, s := range []string{v.prv, v.pub} {
			p, err := ParseExtKey(s)
			if err != nil || p.serialize() != s {
				t.Fatalf("%s does not round-trip: %v", s, err)
			}
		}
	}
	t.Logf("%d BIP 32 derivations checked (vectors 1-4)", len(bip32Vectors))
}

// CKDpub agrees with CKDpriv: wherever a vector's step is non-hardened, the child's xpub derived from the PARENT'S
// xpub alone equals the vector. This is the property every party's address derivation rests on.
func TestBIP32PublicDerivationAgrees(t *testing.T) {
	checked := 0
	for i := 1; i < len(bip32Vectors); i++ {
		parent, child := bip32Vectors[i-1], bip32Vectors[i]
		if parent.seed != child.seed || !strings.HasPrefix(child.path, parent.path+"/") {
			continue
		}
		last := parsePath(t, child.path)
		idx := last[len(last)-1]
		pp, err := ParseExtKey(parent.pub)
		if err != nil {
			t.Fatal(err)
		}
		c, err := pp.Child(idx)
		if idx >= HardenedOffset {
			if err == nil {
				t.Fatalf("%s: a hardened child was derived from a public key", child.path)
			}
			continue
		}
		if err != nil || c.XPub() != child.pub {
			t.Fatalf("%s: CKDpub gives %v (%v), want %s", child.path, c, err, child.pub)
		}
		checked++
	}
	if checked < 4 {
		t.Fatalf("only %d public derivations checked", checked)
	}
	t.Logf("%d public derivations agree with the private ones", checked)
}

// BIP 32 vector 5: every invalid extended key is refused.
func TestBIP32InvalidKeys(t *testing.T) {
	for _, v := range bip32Invalid {
		if k, err := ParseExtKey(v.key); err == nil {
			t.Fatalf("accepted an invalid extended key (%s): %v", v.why, k.XPub())
		}
	}
	t.Logf("%d invalid extended keys refused (vector 5)", len(bip32Invalid))
}

func TestBIP32SeedBounds(t *testing.T) {
	for _, n := range []int{0, 15, 65} {
		if _, err := NewMaster(make([]byte, n), false); err == nil {
			t.Fatalf("a %d-byte seed was accepted", n)
		}
	}
	m, err := NewMaster(make([]byte, 32), true)
	if err != nil || !strings.HasPrefix(m.XPub(), "tpub") {
		t.Fatalf("testnet master: %v", err)
	}
	m.Zero()
	if m.IsPrivate() {
		t.Fatal("Zero left the private key")
	}
}
