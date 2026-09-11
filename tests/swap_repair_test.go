package tests

// Repair of ZEC-2 (docs/audit/FINDINGS_2026-09-10.md), contract half: swap.bas Claim compares the stored hash
// with HEX(SHA256(raw)) and the DVM writes hex in lower case, while the counter-chain script holds the same 32
// bytes and cannot see how they were written. A maker who posted H in upper case therefore had a DERO leg no
// taker could ever claim and a Zcash leg that paid normally: the taker funds, the maker claims the ZEC with s,
// the DERO Claim is refused for ever, and at expiry the maker refunds the DERO too.
//
// swap.bas Lock 29 and board.bas Post 27 now refuse any hash that is not exactly the lower-case hex of the
// bytes it names, so the lock that cannot be claimed is never made and the offer that baits it is never posted.
// tests/zzswap_preimage_test.go, which pins the old behaviour, must be inverted by whoever owns it.

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// hashCases returns the lower-case hex of SHA256(raw) and the two renderings of it that name the same 32 bytes
// everywhere except in a DVM string comparison.
func hashCases(t *testing.T) (raw [32]byte, lower, upper, mixed string) {
	t.Helper()
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	sum := sha256.Sum256(raw[:])
	lower = hex.EncodeToString(sum[:])
	upper = strings.ToUpper(lower)
	mixed = lower[:32] + strings.ToUpper(lower[32:])
	if lower == upper {
		t.Fatal("fixture hash has no hex letters; pick another")
	}
	return
}

func TestSwapLockRefusesAHashItCouldNeverMatch(t *testing.T) {
	c := harness.New()
	maker, taker := c.NewAccount(), c.NewAccount()
	code := readContract(t, "swap.bas")
	scid, res := c.Install(code, nil, swapInit(), maker, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	t.Logf("swap.bas: %d bytes installed, install storage gas %d of %d", len(code), res.GasStorage, harness.MaxStorageGas)
	if res.GasStorage >= harness.MaxStorageGas {
		t.Fatalf("install storage gas %d does not fit the %d cap", res.GasStorage, harness.MaxStorageGas)
	}

	raw, lower, upper, mixed := hashCases(t)
	lock := func(hash, taker string) harness.Result {
		return c.Call(scid, "Lock", rpc.Arguments{harness.S("hash", hash), harness.S("taker", taker),
			harness.U("expiry", c.Height+10), harness.S("asset", "")}, map[crypto.Hash]uint64{zero: 100000}, maker)
	}
	for _, hash := range []string{upper, mixed} {
		if r := lock(hash, taker.String()); r.OK() {
			t.Fatalf("Lock accepted %s…, a hash Claim can never match", hash[:16])
		}
	}
	// A lock that names no taker is refused before the hash is looked at (GAP-024), so there is no second path for
	// the check to guard; the refusal still holds.
	if r := lock(upper, ""); r.OK() {
		t.Fatal("Lock with no taker named accepted an upper-case hash")
	}
	if n := c.U(scid, "n"); n != 0 {
		t.Fatalf("n %d: a refused Lock still wrote a row", n)
	}
	if b := c.SCBalance(scid, zero); b != 0 {
		t.Fatalf("the contract kept %d of a refused lock", b)
	}

	// The canonical form is untouched: it locks, and the taker claims it with the 32-byte secret.
	exp := c.Height + 10
	r := c.Call(scid, "Lock", rpc.Arguments{harness.S("hash", lower), harness.S("taker", taker.String()),
		harness.U("expiry", exp), harness.S("asset", "")}, map[crypto.Hash]uint64{zero: 100000}, maker)
	if !r.OK() || c.U(scid, "n") != 1 || c.S(scid, "h_0") != lower {
		t.Fatalf("lower-case lock: %v h_0 %q", r.Err, c.S(scid, "h_0"))
	}
	rc := c.Call(scid, "Claim", rpc.Arguments{harness.U("id", 0), harness.S("secret", hex.EncodeToString(raw[:]))}, nil, taker)
	if !rc.OK() || c.U(scid, "st_0") != 1 || rc.Sent(zero, taker) != 100000 {
		t.Fatalf("claim: %v st_0 %d sent %d", rc.Err, c.U(scid, "st_0"), rc.Sent(zero, taker))
	}
}

func TestBoardPostRefusesAHashTheDeroLegCouldNeverMatch(t *testing.T) {
	c := harness.New()
	maker := c.NewAccount()
	code := readContract(t, "board.bas")
	scid, res := c.Install(code, nil, nil, maker, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	t.Logf("board.bas: %d bytes installed, install storage gas %d of %d", len(code), res.GasStorage, harness.MaxStorageGas)
	if res.GasStorage >= harness.MaxStorageGas {
		t.Fatalf("install storage gas %d does not fit the %d cap", res.GasStorage, harness.MaxStorageGas)
	}

	_, lower, upper, mixed := hashCases(t)
	post := func(hash string) harness.Result {
		return c.Call(scid, "Post", rpc.Arguments{harness.S("chain", "zec"), harness.S("giveAsset", ""),
			harness.U("giveAmount", 100000), harness.U("wantAmount", 50000), harness.S("wantAddr", "t1MakerPaysMeHere"),
			harness.S("hash", hash), harness.U("expiry", c.Height+100)}, nil, maker)
	}
	for _, hash := range []string{upper, mixed} {
		if r := post(hash); r.OK() {
			t.Fatalf("Post accepted %s…: the taker would fund a script whose DERO leg cannot be claimed", hash[:16])
		}
	}
	if n := c.U(scid, "n"); n != 0 {
		t.Fatalf("n %d: a refused Post still wrote a row", n)
	}
	if r := post(lower); !r.OK() || c.U(scid, "n") != 1 || c.S(scid, "h_0") != lower {
		t.Fatalf("lower-case post: %v h_0 %q", r.Err, c.S(scid, "h_0"))
	}
}
