package xmrswap

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	ref "veil/reference"
)

// TestPinnedCodeIsTheTreesHtlc: the code hash the companion accepts (U4) is the hash of contracts/htlc.bas as it
// installs — reference.Canon of the source, the same bytes tools/simhtlc installs and DERO.GetSC hands back.
func TestPinnedCodeIsTheTreesHtlc(t *testing.T) {
	b, err := os.ReadFile("../../contracts/htlc.bas")
	if err != nil {
		t.Fatal(err)
	}
	code := ref.Canon(string(b))
	sum := sha256.Sum256([]byte(code))
	if got := hex.EncodeToString(sum[:]); got != HTLCCodeSHA256 {
		t.Fatalf("contracts/htlc.bas canonicalises to %d bytes hashing %s; the companion pins %s: update HTLCCodeSHA256 with the contract", len(code), got, HTLCCodeSHA256)
	}
	t.Logf("htlc.bas: %d canonical bytes, sha256 %s", len(code), HTLCCodeSHA256)
}
