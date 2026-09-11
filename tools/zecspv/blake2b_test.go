package zecspv

import (
	"bytes"
	"encoding/hex"
	"testing"

	xblake2b "golang.org/x/crypto/blake2b"
)

// RFC 7693 Appendix A: BLAKE2b-512("abc").
func TestBlake2bRFC7693(t *testing.T) {
	const want = "ba80a53f981c4d0d6a2797b69f12f6e94c212f14685ac4b74b12bb6fdbffa2d1" +
		"7d87c5392aab792dc252d5de4533cc9518d38aa8dbf1925ab92386edd4009923"
	if got := hex.EncodeToString(blake2bSum(64, nil, []byte("abc"))); got != want {
		t.Fatalf("BLAKE2b-512(abc) = %s", got)
	}
}

// Unpersonalised, the hash must equal golang.org/x/crypto/blake2b at every length across the block boundaries and
// at the digest sizes Equihash uses; the personalised form differs only in the parameter block, and the Equihash
// vectors pin it.
func TestBlake2bAgainstXCrypto(t *testing.T) {
	data := make([]byte, 400)
	for i := range data {
		data[i] = byte(i*7 + 3)
	}
	for _, size := range []int{32, 50, 54, 64} {
		for n := 0; n <= len(data); n++ {
			x, err := xblake2b.New(size, nil)
			if err != nil {
				t.Fatal(err)
			}
			x.Write(data[:n])
			if got, want := blake2bSum(size, nil, data[:n]), x.Sum(nil); !bytes.Equal(got, want) {
				t.Fatalf("size %d, %d bytes: %x, x/crypto %x", size, n, got, want)
			}
		}
	}
}

// Writing in pieces, and summing one state twice, give the one-shot digest.
func TestBlake2bIncremental(t *testing.T) {
	data := make([]byte, 300)
	for i := range data {
		data[i] = byte(i*13 + 1)
	}
	person := equihashPersonal(200, 9)
	for n := 0; n <= len(data); n++ {
		want := blake2bSum(50, person, data[:n])
		for _, cut := range []int{0, 1, n / 2, 127, 128, 129, 140, n} {
			if cut > n {
				continue
			}
			d := newBlake2b(50, person)
			d.write(data[:cut])
			d.write(data[cut:n])
			if got := d.sum(nil); !bytes.Equal(got, want) {
				t.Fatalf("%d bytes cut at %d: %x, want %x", n, cut, got, want)
			}
			if got := d.sum(nil); !bytes.Equal(got, want) {
				t.Fatalf("%d bytes: a second sum differs", n)
			}
		}
	}
}
