package zecswap

import (
	"bytes"
	"encoding/hex"
	"os"
	"regexp"
	"testing"
)

// Regression tests for the Zcash-side findings of the 2026-09-10 adversarial pass. THEY ARE RED UNTIL THE
// FIXES LAND — each one states the property the fix must establish, not the behaviour the code has today.

// ZEC-1. contracts/swap.bas Claim (lines 25-27) accepts only a preimage of exactly 32 raw bytes given as 64
// hex characters, while OP_SHA256 in the redeem script hashes a pushed item of any length up to the 520-byte
// script element limit. zcash_script accepts a claim with a 1-, 31-, 33- or 64-byte preimage (the PoC run of
// 2026-09-10), so a maker who picks a secret of any other length spends the taker's Zcash output and leaves
// the taker unable to claim the DERO escrow: the maker refunds at expiry and ends with both legs. The taker
// cannot detect it before funding, because H says nothing about the length of s. The claim branch must
// therefore bind the size, as BOLT 3's HTLCs do: OP_SIZE <32> OP_EQUALVERIFY before OP_SHA256.
func TestRedeemScriptBindsThePreimageTo32Bytes(t *testing.T) {
	const opSize = 0x82
	s := vector(t).RedeemScript()
	want := []byte{opSize, 0x01, 32, opEqualVerify, opSHA256}
	if !bytes.Contains(s, want) {
		t.Fatalf("the claim branch does not check that the preimage is 32 bytes, so a maker can spend the\n"+
			"Zcash output with a preimage contracts/swap.bas will never accept:\n  %x", s)
	}
}

// ZEC-3. ScriptNum's own comment promises the minimal encoding, and Zcash's interpreter is run with
// MINIMALDATA (the PoC's flags). Numbers 1..16 must be pushed as OP_1..OP_16, or the redeem script that
// carries such a CLTV cannot be spent by the refund branch through a standard node.
func TestCLTVPushIsMinimal(t *testing.T) {
	for _, c := range []struct {
		n    int64
		want string
	}{{1, "51"}, {5, "55"}, {16, "60"}, {17, "0111"}, {128, "028000"}, {3_000_000, "03c0c62d"}} {
		if got := hex.EncodeToString(push(ScriptNum(c.n))); got != c.want {
			t.Errorf("push(ScriptNum(%d)) = %s, want %s", c.n, got, c.want)
		}
	}
}

// ZEC-4. The package knows NU5 and NU6 only. Mainnet has activated NU6.1 (0x4DEC4DF0, height 3146400,
// ZIP 255), NU6.2 (0x5437F330, height 3364600, ZIP 257) and NU6.3 (0x37A5165B, height 3428143, ZIP 258)
// since. A signature made under a branch id the chain is not on is invalid, so a refund built from these
// constants cannot be broadcast; the id must come from the node's getblockchaininfo consensus.NEXTBLOCK (the
// block the transaction will be mined in), not from a constant and not from consensus.chaintip.
func TestBranchIDsAreNotStale(t *testing.T) {
	src, err := os.ReadFile("sighash.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"0x4DEC4DF0", "0x5437F330", "0x37A5165B"} {
		if !regexp.MustCompile("(?i)" + id).Match(src) {
			t.Errorf("sighash.go does not know consensus branch id %s; every signature it makes for mainnet today is invalid", id)
		}
	}
}
