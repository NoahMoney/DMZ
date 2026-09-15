package simrpc

import (
	"strings"
	"testing"
)

func TestAddressFromRaw(t *testing.T) {
	raw := make([]byte, 33)
	raw[0] = 2
	for i := 1; i < 33; i++ {
		raw[i] = byte(i*7 + 200)
	}
	a := AddressFromRaw(raw)
	if !strings.HasPrefix(a, "dero1") || DVMAddress(a) != a {
		t.Fatalf("%q is not the DVM spelling", a)
	}
	hrp, data := bech32Decode(a)
	b := convertBits(data, 5, 8, false)
	if hrp != "dero" || len(b) != 34 || b[0] != 1 {
		t.Fatalf("decoded %s %d bytes", hrp, len(b))
	}
	for i := range raw {
		if b[i+1] != int(raw[i]) {
			t.Fatalf("byte %d differs", i)
		}
	}
	for _, n := range []int{0, 32, 34} {
		if AddressFromRaw(make([]byte, n)) != "" {
			t.Errorf("a %d-byte key was spelt as an address", n)
		}
	}
}
