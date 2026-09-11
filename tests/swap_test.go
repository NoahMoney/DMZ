package tests

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

func TestSwapEscrow(t *testing.T) {
	c := harness.New()
	alice, bob, carol := c.NewAccount(), c.NewAccount(), c.NewAccount()
	code := readContract(t, "swap.bas")
	scid, res := c.Install(code, nil, swapInit(), alice, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	t.Logf("swap.bas: %d bytes, install storage gas %d", len(code), res.GasStorage)
	// the secret is a 32-byte value (what an ed25519 / secp256k1 scalar would be), passed as 64 hex chars and hashed raw
	var raw [32]byte
	for i := range raw {
		raw[i] = byte(i*7 + 3)
	}
	secret := hex.EncodeToString(raw[:])
	sum := sha256.Sum256(raw[:])
	hash := hex.EncodeToString(sum[:])
	exp := c.Height + 10
	// bad locks: short hash, expiry in the past, no funds, anonymous
	if r := c.Call(scid, "Lock", rpc.Arguments{harness.S("hash", "abcd"), harness.S("taker", bob.String()), harness.U("expiry", exp), harness.S("asset", "")}, map[crypto.Hash]uint64{zero: 100000}, alice); r.OK() {
		t.Fatal("short hash accepted")
	}
	if r := c.Call(scid, "Lock", rpc.Arguments{harness.S("hash", hash), harness.S("taker", bob.String()), harness.U("expiry", c.Height), harness.S("asset", "")}, map[crypto.Hash]uint64{zero: 100000}, alice); r.OK() {
		t.Fatal("past expiry accepted")
	}
	if r := c.Call(scid, "Lock", rpc.Arguments{harness.S("hash", hash), harness.S("taker", bob.String()), harness.U("expiry", exp), harness.S("asset", "")}, nil, alice); r.OK() {
		t.Fatal("empty lock accepted")
	}
	if r := c.Call(scid, "Lock", rpc.Arguments{harness.S("hash", hash), harness.S("taker", bob.String()), harness.U("expiry", exp), harness.S("asset", "")}, map[crypto.Hash]uint64{zero: 100000}, nil); r.OK() {
		t.Fatal("anonymous lock accepted")
	}
	// lock 1 DERO for bob
	r := c.Call(scid, "Lock", rpc.Arguments{harness.S("hash", hash), harness.S("taker", bob.String()), harness.U("expiry", exp), harness.S("asset", "")}, map[crypto.Hash]uint64{zero: 100000}, alice)
	if !r.OK() || c.U(scid, "n") != 1 || c.U(scid, "v_0") != 100000 || c.S(scid, "h_0") != hash || c.SCBalance(scid, zero) != 100000 {
		t.Fatalf("lock: %v", r.Err)
	}
	// wrong secret from a stranger and from bob, refund too early. (A stranger's Claim with the RIGHT secret pays
	// bob in v2 — tests/swap_v2_test.go — so it is no longer a refusal case.)
	if r := c.Call(scid, "Claim", rpc.Arguments{harness.U("id", 0), harness.S("secret", strings.Repeat("11", 32))}, nil, carol); r.OK() {
		t.Fatal("stranger's wrong secret accepted")
	}
	if r := c.Call(scid, "Claim", rpc.Arguments{harness.U("id", 0), harness.S("secret", strings.Repeat("00", 32))}, nil, bob); r.OK() {
		t.Fatal("wrong secret accepted")
	}
	if r := c.Call(scid, "Refund", rpc.Arguments{harness.U("id", 0)}, nil, alice); r.OK() {
		t.Fatal("refund before expiry accepted")
	}
	// bob claims: funds move, the secret is on chain, nothing can be claimed twice
	r = c.Call(scid, "Claim", rpc.Arguments{harness.U("id", 0), harness.S("secret", secret)}, nil, bob)
	if !r.OK() || r.Sent(zero, bob) != 100000 || c.U(scid, "st_0") != 1 || c.S(scid, "x_0") != secret || c.SCBalance(scid, zero) != 0 {
		t.Fatalf("claim: %v got %d", r.Err, r.Sent(zero, bob))
	}
	if r := c.Call(scid, "Claim", rpc.Arguments{harness.U("id", 0), harness.S("secret", secret)}, nil, bob); r.OK() {
		t.Fatal("double claim accepted")
	}
	if r := c.Call(scid, "Refund", rpc.Arguments{harness.U("id", 0)}, nil, alice); r.OK() {
		t.Fatal("refund of a claimed lock accepted")
	}
	// an asset lock for bob, then expiry: claim refused, stranger cannot refund, maker refunds once. (Every lock
	// names its taker — GAP-024, tests/swap_gaps_test.go — so the bad locks above name bob too, and each is refused
	// for its own reason.)
	var asset crypto.Hash
	asset[0] = 9
	assetHex := hex.EncodeToString(asset[:])
	exp2 := c.Height + 3
	r = c.Call(scid, "Lock", rpc.Arguments{harness.S("hash", hash), harness.S("taker", bob.String()), harness.U("expiry", exp2), harness.S("asset", assetHex)}, map[crypto.Hash]uint64{asset: 5000}, alice)
	if !r.OK() || c.U(scid, "v_1") != 5000 || c.S(scid, "a_1") != string(asset[:]) || c.SCBalance(scid, asset) != 5000 {
		t.Fatalf("asset lock: %v", r.Err)
	}
	if r := c.Call(scid, "Lock", rpc.Arguments{harness.S("hash", hash), harness.S("taker", bob.String()), harness.U("expiry", exp2), harness.S("asset", assetHex)}, map[crypto.Hash]uint64{asset: 5000, zero: 1}, alice); r.OK() {
		t.Fatal("asset lock with stray DERO accepted")
	}
	for c.Height < exp2 {
		c.NextBlock()
	}
	if r := c.Call(scid, "Claim", rpc.Arguments{harness.U("id", 1), harness.S("secret", secret)}, nil, carol); r.OK() {
		t.Fatal("claim after expiry accepted")
	}
	if r := c.Call(scid, "Refund", rpc.Arguments{harness.U("id", 1)}, nil, carol); r.OK() {
		t.Fatal("stranger refunded")
	}
	r = c.Call(scid, "Refund", rpc.Arguments{harness.U("id", 1)}, nil, alice)
	if !r.OK() || r.Sent(asset, alice) != 5000 || c.U(scid, "st_1") != 2 || c.SCBalance(scid, asset) != 0 {
		t.Fatalf("refund: %v got %d", r.Err, r.Sent(asset, alice))
	}
	if r := c.Call(scid, "Refund", rpc.Arguments{harness.U("id", 1)}, nil, alice); r.OK() {
		t.Fatal("double refund accepted")
	}
	t.Logf("lock/claim/refund gas: claim %d compute", r.GasCompute)
}
