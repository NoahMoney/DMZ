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

// The DERO leg of the transparent-Zcash swap accepts a strictly narrower set of preimages than the Zcash
// script does. contracts/swap.bas Claim (lines 25-27) demands exactly 64 hex characters decoding to 32 raw
// bytes, and compares HEX(SHA256(raw)) — lowercase — against the stored h_<id> as a string. The Zcash redeem
// script (tools/zecswap/script.go RedeemScript) has no OP_SIZE check, so OP_SHA256 accepts a preimage of any
// length up to the 520-byte push limit, and it holds H as bytes, so the case of the hex the maker posted is
// invisible to it.
//
// Either asymmetry lets the maker — who chooses both s and H — spend the taker's Zcash output while leaving
// the taker unable to claim the DERO escrow, so the maker refunds at expiry and ends with both legs.
func TestSwapPreimageAsymmetryLetsTheMakerTakeBothLegs(t *testing.T) {
	c := harness.New()
	maker, taker := c.NewAccount(), c.NewAccount()
	scid, res := c.Install(readContract(t, "swap.bas"), nil, nil, maker, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}

	// (1) a preimage that is not 32 bytes. The taker cannot see the length of s in H, so nothing the taker
	// checks before funding the Zcash script can detect this.
	short := []byte("thirty-one bytes of secret ....")
	if len(short) != 31 {
		t.Fatalf("fixture is %d bytes", len(short))
	}
	sum := sha256.Sum256(short)
	exp := c.Height + 10
	r := c.Call(scid, "Lock", rpc.Arguments{harness.S("hash", hex.EncodeToString(sum[:])), harness.S("taker", taker.String()), harness.U("expiry", exp), harness.S("asset", "")}, map[crypto.Hash]uint64{zero: 100000}, maker)
	if !r.OK() {
		t.Fatalf("lock under a 31-byte preimage refused: %v", r.Err)
	}
	if r := c.Call(scid, "Claim", rpc.Arguments{harness.U("id", 0), harness.S("secret", hex.EncodeToString(short))}, nil, taker); r.OK() {
		t.Fatal("a 31-byte preimage claimed the escrow — the asymmetry is gone, update this test")
	}
	// no padding of it works either: the hash of the padded value is a different hash
	padded := append(append([]byte{}, short...), 0)
	if r := c.Call(scid, "Claim", rpc.Arguments{harness.U("id", 0), harness.S("secret", hex.EncodeToString(padded))}, nil, taker); r.OK() {
		t.Fatal("a padded preimage claimed the escrow")
	}
	for c.Height < exp {
		c.NextBlock()
	}
	if r := c.Call(scid, "Refund", rpc.Arguments{harness.U("id", 0)}, nil, maker); !r.OK() || c.U(scid, "st_0") != 2 {
		t.Fatalf("the maker could not refund the leg the taker could not claim: %v", r.Err)
	}

	// (2) the same H, posted in upper-case hex. The Zcash script holds H as bytes, so the script and its
	// address are identical; swap.bas compares strings against lowercase HEX(), so the claim can never match.
	var raw [32]byte
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	s32 := sha256.Sum256(raw[:])
	lower := hex.EncodeToString(s32[:])
	upper := strings.ToUpper(lower)
	if lower == upper {
		t.Fatal("fixture hash has no hex letters; pick another")
	}
	exp = c.Height + 10
	if r := c.Call(scid, "Lock", rpc.Arguments{harness.S("hash", upper), harness.S("taker", taker.String()), harness.U("expiry", exp), harness.S("asset", "")}, map[crypto.Hash]uint64{zero: 100000}, maker); !r.OK() {
		t.Fatalf("lock under an upper-case hash refused: %v", r.Err)
	}
	if c.S(scid, "h_1") != upper {
		t.Fatalf("h_1 = %q", c.S(scid, "h_1"))
	}
	if r := c.Call(scid, "Claim", rpc.Arguments{harness.U("id", 1), harness.S("secret", hex.EncodeToString(raw[:]))}, nil, taker); r.OK() {
		t.Fatal("an upper-case hash was matched — the asymmetry is gone, update this test")
	}
	for c.Height < exp {
		c.NextBlock()
	}
	if r := c.Call(scid, "Refund", rpc.Arguments{harness.U("id", 1)}, nil, maker); !r.OK() {
		t.Fatalf("refund: %v", r.Err)
	}
	t.Log("both legs refunded to the maker on DERO while the Zcash script would have paid the maker: the taker's ZEC is lost")
}
