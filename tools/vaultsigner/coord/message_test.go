package coord

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"math/rand"
	"testing"
	"time"
)

func sealed(t testing.TB, f *fleet, from int, round string, payload []byte) []byte {
	t.Helper()
	now := f.clock.Now().UnixNano()
	raw, err := seal(f.ids[from], mustVault(t, f.vault), round, payload, f.pubs, now, now+int64(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// resign replaces the signature, as the sender itself could after editing its own message.
func resign(t testing.TB, id *Identity, raw []byte) []byte {
	t.Helper()
	body := bytes.Clone(raw[:len(raw)-sigLen])
	sig, err := id.sign.Sign(nil, body, &ed25519.Options{Context: ctxMessage})
	if err != nil {
		t.Fatal(err)
	}
	return append(body, sig...)
}

func TestSealOpenRoundTrip(t *testing.T) {
	f := newFleet(t, 3)
	p := []byte("multisig info, which is sensitive")
	raw := sealed(t, f, 0, "xmr/info/1", p)
	if bytes.Contains(raw, p) {
		t.Fatal("the payload travels in the clear")
	}
	m, err := parse(raw, maxMessage(DefaultMaxPayload))
	if err != nil {
		t.Fatal(err)
	}
	if !m.verify(f.pubs[0].Sign) || m.verify(f.pubs[1].Sign) {
		t.Fatal("the signature verifies under the wrong key, or not under the right one")
	}
	for i := range f.ids {
		got, err := m.open(f.ids[i])
		if err != nil || !bytes.Equal(got, p) {
			t.Fatalf("recipient %d: %v", i, err)
		}
	}
	if _, err := m.open(newIdentity(t, testAddr(77))); !errors.Is(err, errNotRecipient) {
		t.Fatalf("an outsider opened it: %v", err)
	}
}

// Every single-bit change anywhere in a message — header, recipients, wraps, nonce, ciphertext, signature — is refused.
func TestEveryBitFlipIsRefused(t *testing.T) {
	f := newFleet(t, 3)
	raw := sealed(t, f, 0, "zec/release/3/sig/1", []byte("a DER signature, 71 bytes or so, standing in for the real thing."))
	for i := 0; i < len(raw); i++ {
		for bit := 0; bit < 8; bit++ {
			x := bytes.Clone(raw)
			x[i] ^= 1 << bit
			if m, err := parse(x, maxMessage(DefaultMaxPayload)); err == nil && m.verify(f.pubs[0].Sign) {
				t.Fatalf("a flip of bit %d of byte %d of %d still verifies", bit, i, len(raw))
			}
		}
	}
}

func TestEveryTruncationAndExtensionIsRefused(t *testing.T) {
	f := newFleet(t, 3)
	raw := sealed(t, f, 1, "xmr/txset/9/ab12", []byte("txset"))
	for n := 0; n < len(raw); n++ {
		if m, err := parse(raw[:n], maxMessage(DefaultMaxPayload)); err == nil && m.verify(f.pubs[1].Sign) {
			t.Fatalf("a message cut to %d of %d bytes verifies", n, len(raw))
		}
	}
	if _, err := parse(append(bytes.Clone(raw), 0), maxMessage(DefaultMaxPayload)); err == nil {
		t.Fatal("a byte after the signature was accepted")
	}
	if _, err := parse(raw, len(raw)-1); !errors.Is(err, errTooLarge) {
		t.Fatalf("a message over the limit was not refused as too large: %v", err)
	}
}

// Even the sender, who can re-sign, cannot hand recipient B the key wrap made for C: HPKE binds the wrap to C's key
// and the info binds it to the recipient slot, so B's slot does not open.
func TestAnInsiderCannotRouteAnotherSignersWrap(t *testing.T) {
	f := newFleet(t, 3)
	raw := sealed(t, f, 0, "xmr/info/2", []byte("p"))
	m, err := parse(raw, maxMessage(DefaultMaxPayload))
	if err != nil {
		t.Fatal(err)
	}
	x := bytes.Clone(raw)
	// swap the wraps of slots 0 and 1, keep the keys
	off := m.ctxEnd + 1
	w0 := bytes.Clone(x[off+32 : off+32+wrapLen])
	w1 := bytes.Clone(x[off+32+wrapLen+32 : off+32+wrapLen+32+wrapLen])
	copy(x[off+32:], w1)
	copy(x[off+32+wrapLen+32:], w0)
	x = resign(t, f.ids[0], x)
	m2, err := parse(x, maxMessage(DefaultMaxPayload))
	if err != nil || !m2.verify(f.pubs[0].Sign) {
		t.Fatalf("the re-signed message should parse and verify: %v", err)
	}
	for _, s := range m2.slots[:2] {
		for i, p := range f.pubs {
			if bytes.Equal(p.Sign, s.key[:]) {
				if _, err := m2.open(f.ids[i]); !errors.Is(err, errNoDecrypt) {
					t.Fatalf("signer %d opened a wrap made for another signer: %v", i, err)
				}
			}
		}
	}
}

// Structural nonsense is refused by the parser itself, even when the sender re-signs it.
func TestParseRefusesStructuralNonsense(t *testing.T) {
	f := newFleet(t, 3)
	raw := sealed(t, f, 0, "r", []byte("p"))
	m, _ := parse(raw, maxMessage(DefaultMaxPayload))
	edits := map[string]func(x []byte){
		"version 2":              func(x []byte) { x[4] = 2 },
		"suite 9":                func(x []byte) { x[5] = 9 },
		"bad magic":              func(x []byte) { x[0] = 'X' },
		"zero recipients":        func(x []byte) { x[m.ctxEnd] = 0 },
		"too many recipients":    func(x []byte) { x[m.ctxEnd] = MaxRecipients + 1 },
		"duplicate recipients":   func(x []byte) { copy(x[m.ctxEnd+1+32+wrapLen:], x[m.ctxEnd+1:m.ctxEnd+1+32]) },
		"expires at creation":    func(x []byte) { copy(x[m.ctxEnd-8:], x[m.ctxEnd-16:m.ctxEnd-8]) },
		"created zero":           func(x []byte) { binary.BigEndian.PutUint64(x[m.ctxEnd-16:], 0) },
		"created above int64":    func(x []byte) { binary.BigEndian.PutUint64(x[m.ctxEnd-16:], 1<<63) },
		"id with a space":        func(x []byte) { x[4+2+96+1+3] = ' ' },
		"id length zero":         func(x []byte) { x[4+2+96] = 0 },
		"ciphertext length long": func(x []byte) { binary.BigEndian.PutUint32(x[m.hdrEnd:], uint32(len(m.ct)+1)) },
		"ciphertext below a tag": func(x []byte) { binary.BigEndian.PutUint32(x[m.hdrEnd:], 15) },
	}
	for name, edit := range edits {
		x := bytes.Clone(raw)
		edit(x)
		x = resign(t, f.ids[0], x)
		if _, err := parse(x, maxMessage(DefaultMaxPayload)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestRandomBytesNeverVerify(t *testing.T) {
	f := newFleet(t, 3)
	rng := rand.New(rand.NewSource(3))
	good := sealed(t, f, 0, "r", []byte("p"))
	for i := 0; i < 3000; i++ {
		x := make([]byte, rng.Intn(2*len(good)))
		rng.Read(x)
		if i%2 == 0 && len(x) > 6 { // a plausible prefix, random after it
			copy(x, good[:6])
		}
		if m, err := parse(x, maxMessage(DefaultMaxPayload)); err == nil && m.verify(f.pubs[0].Sign) {
			t.Fatal("random bytes verified")
		}
	}
}

func FuzzParse(f *testing.F) {
	fl := newFleet(f, 3)
	f.Add(sealed(f, fl, 0, "r", []byte("p")))
	f.Add([]byte("DMZC\x01\x01"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		if m, err := parse(b, maxMessage(DefaultMaxPayload)); err == nil {
			_ = m.verify(fl.pubs[0].Sign)
			_, _ = m.open(fl.ids[1])
		}
	})
}

func TestValidRound(t *testing.T) {
	for _, ok := range []string{"zec/release/12/sig/0", "xmr/info/3", "xmr/ceremony/round-2", "a", "x=y+z@w:v.u_"} {
		if err := ValidRound(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	long := bytes.Repeat([]byte("a"), MaxRoundLen+1)
	for _, bad := range []string{"", string(long), "has space", "new\nline", "tab\t", "quote\"", "back\\slash", "percent%", "unicodé"} {
		if err := ValidRound(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestRoundTagSeparatesVaultsAndRounds(t *testing.T) {
	v1, v2 := mustVault(t, testVaultHex), mustVault(t, otherVaultHex)
	tags := map[[32]byte]string{}
	for _, v := range [][32]byte{v1, v2} {
		for _, r := range []string{"zec/release/1/sig/0", "zec/release/1/sig/1", "zec/release/10/sig/0", "xmr/info/1"} {
			tg := roundTag(v, r)
			if prev, dup := tags[tg]; dup {
				t.Fatalf("%s and %x/%s share a tag", prev, v[:2], r)
			}
			tags[tg] = r
		}
	}
}
