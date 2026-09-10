package zecswap

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// Regression tests for the repairs of the Zcash-side findings of the 2026-09-10 audit. hazards_test.go states
// the properties the finder asked for; these pin the behaviour the fixes actually establish.

// ZEC-1. The script the taker funds must accept exactly the secrets contracts/swap.bas accepts. swap.bas Claim
// takes 64 hex characters that decode to 32 bytes; the claim branch now opens with OP_SIZE <32> OP_EQUALVERIFY,
// which research/crosschain/poc/zec-t-htlc proves under Zcash's consensus interpreter (run.log: 1-, 31-, 33-
// and 64-byte preimages rejected; the same four were Ok(true) against the 94-byte script).
func TestRedeemScriptOpensWithTheSizeCheck(t *testing.T) {
	s := vector(t).RedeemScript()
	// OP_IF OP_SIZE <1 byte: 32> OP_EQUALVERIFY OP_SHA256 <32 bytes: H>
	want := []byte{opIf, opSize, 0x01, SecretBytes, opEqualVerify, opSHA256, 0x20}
	if !hasPrefix(s, want) {
		t.Fatalf("the claim branch does not begin by binding the preimage to %d bytes:\n  %x", SecretBytes, s)
	}
	if len(s) != 98 {
		t.Fatalf("redeem script is %d bytes; the size check makes it 98", len(s))
	}
	// the size check is in the claim branch only: the refund branch's push of the height is untouched
	if !hasSub(s, []byte{opElse, 0x03, 0xc0, 0xc6, 0x2d, opCheckLockTimeVerify}) {
		t.Fatalf("the refund branch changed:\n  %x", s)
	}
}

// ZEC-1, the CLI half. A secret of any length but 32 must be refused before it is ever used to build a script.
func TestParseSecretTakesOnly32Bytes(t *testing.T) {
	good := strings.Repeat("ab", SecretBytes)
	if got, err := ParseSecret(good); err != nil || hex.EncodeToString(got[:]) != good {
		t.Fatalf("a 32-byte secret was refused: %v", err)
	}
	for _, bad := range []string{"", "ab", strings.Repeat("ab", 31), strings.Repeat("ab", 33), strings.Repeat("ab", 64)} {
		if _, err := ParseSecret(bad); err == nil {
			t.Fatalf("accepted a %d-byte secret", len(bad)/2)
		}
	}
}

// ZEC-2. A hash on the board that is not lower-case hex can never be matched by contracts/swap.bas, whose
// comparison is against the DVM's lower-case HEX(SHA256(...)) — while the Zcash side, holding bytes, cannot
// tell the difference and builds a working claim. Every reader of a board hash goes through ParseSwapHash, so
// the asymmetry is refused before a script exists.
func TestParseSwapHashTakesOnlyLowerCaseHex(t *testing.T) {
	lower := "3d9891e697fcb6e0e772ed5684f02dc06a73e5aead33ac0a1106ded42ac08df2"
	h, err := ParseSwapHash(lower)
	if err != nil || hex.EncodeToString(h[:]) != lower {
		t.Fatalf("a lower-case hash was refused: %v", err)
	}
	for _, bad := range []struct{ in, why string }{
		{strings.ToUpper(lower), "upper case"},
		{lower[:60] + "ABCD", "mixed case"},
		{lower[:63], "63 characters"},
		{lower + "0", "65 characters"},
		{"", "empty"},
		{strings.Repeat("g", 64), "not hex"},
		{strings.Repeat(" ", 64), "spaces"},
	} {
		if _, err := ParseSwapHash(bad.in); err == nil {
			t.Errorf("accepted a hash that is %s", bad.why)
		}
	}
	// the upper-case refusal says why, because the reason is not visible from the Zcash side
	if _, err := ParseSwapHash(strings.ToUpper(lower)); err == nil || !strings.Contains(err.Error(), "lower-case") {
		t.Errorf("the upper-case refusal does not name the reason: %v", err)
	}
}

// ZEC-3. Nothing on DERO can bound cltv against the Zcash chain, so the companion must. A cltv at or below the
// tip leaves the refund branch spendable at once; a cltv at or above LOCKTIME_THRESHOLD is a Unix time already
// past; a cltv far above the tip strands the taker's own ZEC.
func TestCheckCLTV(t *testing.T) {
	const tip = 3_000_000
	for _, bad := range []uint32{0, 1, tip - 1, tip, LockTimeThreshold, 1_600_000_000, 0x7fffffff} {
		if err := CheckCLTV(bad, tip); err == nil {
			t.Errorf("cltv %d accepted against tip %d", bad, tip)
		}
	}
	if err := CheckCLTV(tip+CLTVWindow, tip); err != nil {
		t.Errorf("the policy value tip+%d was refused: %v", CLTVWindow, err)
	}
	// the tolerance boundary: inclusive on both ends, refused one step outside
	for _, ok := range []uint32{tip + CLTVWindow - CLTVTolerance, tip + CLTVWindow + CLTVTolerance} {
		if err := CheckCLTV(ok, tip); err != nil {
			t.Errorf("cltv %d (%d from the tip) was refused: %v", ok, ok-tip, err)
		}
	}
	for _, bad := range []uint32{tip + CLTVWindow - CLTVTolerance - 1, tip + CLTVWindow + CLTVTolerance + 1} {
		if err := CheckCLTV(bad, tip); err == nil {
			t.Errorf("cltv %d (%d from the tip) was accepted", bad, bad-tip)
		}
	}
	// the form check stands alone, for a caller rebuilding the script of a swap already funded
	if err := CheckCLTVHeight(tip); err != nil {
		t.Errorf("a plain height was refused by the form check: %v", err)
	}
	for _, bad := range []uint32{0, LockTimeThreshold, 1_600_000_000} {
		if err := CheckCLTVHeight(bad); err == nil {
			t.Errorf("the form check accepted %d", bad)
		}
	}
}

// ZEC-4. The branch id is signed over, so it cannot be guessed. This build knows the five epochs that carry v5
// transactions and refuses to sign under anything else; the id itself must come from the node's
// getblockchaininfo consensus.nextblock, and CheckBranchID is where the node's answer is checked.
func TestBranchIDForHeight(t *testing.T) {
	for _, c := range []struct {
		net    Network
		height uint32
		want   uint32
	}{
		{Mainnet, 1_687_104, BranchNU5}, {Mainnet, 2_726_399, BranchNU5},
		{Mainnet, 2_726_400, BranchNU6}, {Mainnet, 3_146_399, BranchNU6},
		{Mainnet, 3_146_400, BranchNU61}, {Mainnet, 3_364_599, BranchNU61},
		{Mainnet, 3_364_600, BranchNU62}, {Mainnet, 3_428_142, BranchNU62},
		{Mainnet, 3_428_143, BranchNU63}, {Mainnet, 9_000_000, BranchNU63},
		{Testnet, 1_842_420, BranchNU5}, {Testnet, 2_976_000, BranchNU6},
		{Testnet, 3_536_500, BranchNU61}, {Testnet, 4_052_000, BranchNU62},
		{Testnet, 4_134_000, BranchNU63},
	} {
		got, err := BranchIDForHeight(c.net, c.height)
		if err != nil || got != c.want {
			t.Errorf("BranchIDForHeight(%s, %d) = %08x %v, want %08x", c.net, c.height, got, err, c.want)
		}
	}
	// below NU5 there is no v5 transaction to sign
	if _, err := BranchIDForHeight(Mainnet, 1_687_103); err == nil {
		t.Error("a pre-NU5 height was given a branch id")
	}
}

func TestCheckBranchIDRequiresTheNodesAnswer(t *testing.T) {
	// the node and the schedule agree
	if err := CheckBranchID(Mainnet, 3_428_144, BranchNU63); err != nil {
		t.Errorf("the current epoch was refused: %v", err)
	}
	// an id this build has never heard of: the binary is older than the chain and must not guess
	if err := CheckBranchID(Mainnet, 9_000_000, 0xDEADBEEF); err == nil {
		t.Error("an unknown branch id was accepted")
	}
	// a known id that disagrees with the schedule for that height
	if err := CheckBranchID(Mainnet, 3_428_144, BranchNU5); err == nil {
		t.Error("a branch id from the wrong epoch was accepted")
	}
}

// The signing path fails closed on a branch id this build does not know, rather than making a signature that
// is invalid on every node and cannot be corrected once the funds are locked.
func TestSigDigestRefusesAnUnknownBranchID(t *testing.T) {
	tx := swapClaimTx(t)
	tx.BranchID = 0xDEADBEEF
	if _, err := tx.SigDigest(0, SigHashAll); err == nil {
		t.Fatal("signed under an unknown consensus branch id")
	}
	tx.BranchID = BranchNU63
	if _, err := tx.SigDigest(0, SigHashAll); err != nil {
		t.Fatalf("the current epoch was refused: %v", err)
	}
}

// ZEC-5, second half: ZIP 203 makes a transaction invalid in any block above nExpiryHeight, and the field is
// capped. A transaction above the cap can never be mined, so it is not signed and not read.
func TestExpiryHeightIsBounded(t *testing.T) {
	tx := swapClaimTx(t)
	tx.ExpiryHeight = MaxExpiryHeight + 1
	if _, err := tx.SigDigest(0, SigHashAll); err == nil {
		t.Fatal("signed a transaction whose expiry height can never be reached")
	}
	tx.ExpiryHeight = MaxExpiryHeight
	if _, err := tx.SigDigest(0, SigHashAll); err != nil {
		t.Fatalf("the maximum expiry height was refused: %v", err)
	}
	tx.ExpiryHeight = MaxExpiryHeight + 1
	if _, err := ParseV5(tx.Serialize()); err == nil {
		t.Fatal("parsed a transaction whose expiry height can never be reached")
	}
}

// ZEC-5, first half, at every length-prefixed field rather than only the one the minimised case reached: a
// CompactSize near 2^63 must be an error, not a panic.
func TestOversizeLengthsAreErrors(t *testing.T) {
	tx := swapClaimTx(t)
	raw := tx.Serialize()
	for _, at := range []struct {
		name string
		find []byte
	}{
		{"the scriptSig length", append(CompactSize(uint64(len(tx.In[0].ScriptSig))), tx.In[0].ScriptSig...)},
		{"the scriptPubKey length", append(CompactSize(uint64(len(tx.Out[0].ScriptPubKey))), tx.Out[0].ScriptPubKey...)},
	} {
		i := indexOf(raw, at.find)
		if i < 0 {
			t.Fatalf("%s is not where this test thinks it is", at.name)
		}
		bad := append(append([]byte{}, raw[:i]...), append(CompactSize(1<<63-1), raw[i+len(at.find):]...)...)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s: ParseV5 panicked instead of erroring: %v", at.name, r)
				}
			}()
			if _, err := ParseV5(bad); err == nil {
				t.Fatalf("%s: an oversize length was accepted", at.name)
			}
		}()
	}
}

// swapClaimTx is a plausible claim spend of the swap script: one input, one output, filled in enough to sign.
func swapClaimTx(t *testing.T) *Tx {
	t.Helper()
	h := vector(t)
	var prev [32]byte
	prev[0] = 0xaa
	return &Tx{
		BranchID:     BranchNU63,
		ExpiryHeight: 3_000_500,
		In:           []TxIn{{PrevOut: OutPoint{Hash: prev, Index: 1}, ScriptSig: []byte{0x51, 0x52, 0x53}, Sequence: 0xffffffff, Value: 25_000_000, ScriptPubKey: h.ScriptPubKey()}},
		Out:          []TxOut{{Value: 24_990_000, ScriptPubKey: append(append([]byte{opDup, opHash160}, push(h.ClaimPkh[:])...), opEqualVerify, opCheckSig)}},
	}
}

// The board's hash and the script's bytes are the same thing said twice; a taker must be able to prove it.
func TestTheBoardHashIsTheScriptHash(t *testing.T) {
	var secret [32]byte
	for i := range secret {
		secret[i] = byte(i)
	}
	sum := sha256.Sum256(secret[:])
	board := hex.EncodeToString(sum[:])
	h, err := ParseSwapHash(board)
	if err != nil {
		t.Fatal(err)
	}
	htlc := HTLC{Hash: h, CLTV: 3_000_000}
	if !hasSub(htlc.RedeemScript(), sum[:]) {
		t.Fatal("the hash the board carries is not the hash the script commits to")
	}
	if hex.EncodeToString(h[:]) != board {
		t.Fatal("the round trip through ParseSwapHash is not the identity")
	}
}

func hasPrefix(b, p []byte) bool { return len(b) >= len(p) && string(b[:len(p)]) == string(p) }
func hasSub(b, s []byte) bool    { return indexOf(b, s) >= 0 }
func indexOf(b, s []byte) int    { return strings.Index(string(b), string(s)) }
