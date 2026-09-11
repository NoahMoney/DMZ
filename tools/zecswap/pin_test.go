package zecswap

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	ref "veil/reference"
)

// The code hashes the companion accepts are the hashes of contracts/board.bas and contracts/swap.bas as they install:
// reference.Canon of the source, the bytes DERO.GetSC hands back.
func TestPinnedCodeIsTheTreesContracts(t *testing.T) {
	for _, c := range []struct{ file, name, pin string }{
		{"../../contracts/board.bas", "BoardCodeSHA256", BoardCodeSHA256},
		{"../../contracts/swap.bas", "EscrowCodeSHA256", EscrowCodeSHA256},
	} {
		b, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatal(err)
		}
		code := ref.Canon(string(b))
		sum := sha256.Sum256([]byte(code))
		if got := hex.EncodeToString(sum[:]); got != c.pin {
			t.Fatalf("%s canonicalises to %d bytes hashing %s; the companion pins %s: update %s with the contract", c.file, len(code), got, c.pin, c.name)
		}
		t.Logf("%s: %d canonical bytes, sha256 %s", c.file, len(code), c.pin)
	}
}
