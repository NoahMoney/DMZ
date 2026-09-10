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

	// (2) ZEC-2, repaired. The Zcash script holds H as bytes, so an upper-case hex hash names the same script
	// and the same address there, while swap.bas compares strings against lowercase HEX() and could never match
	// the claim. A maker who posted the upper-case form got a Zcash leg that pays and a DERO leg that cannot,
	// then refunded and kept both. Lock now refuses any hash that is not its own canonical hex, at the door,
	// before any money is taken — so the asymmetric lock cannot exist to be exploited.
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
	if r := c.Call(scid, "Lock", rpc.Arguments{harness.S("hash", upper), harness.S("taker", taker.String()), harness.U("expiry", exp), harness.S("asset", "")}, map[crypto.Hash]uint64{zero: 100000}, maker); r.OK() {
		t.Fatal("a lock under an upper-case hash was accepted: the DERO leg cannot be claimed under it and the maker takes both")
	}
	if c.Exists(scid, "h_1") {
		t.Fatal("the refused lock still wrote an offer")
	}
	// the lower-case form of the same hash is the one both legs can settle, and it works
	if r := c.Call(scid, "Lock", rpc.Arguments{harness.S("hash", lower), harness.S("taker", taker.String()), harness.U("expiry", exp), harness.S("asset", "")}, map[crypto.Hash]uint64{zero: 100000}, maker); !r.OK() {
		t.Fatalf("lock under the canonical hash refused: %v", r.Err)
	}
	if r := c.Call(scid, "Claim", rpc.Arguments{harness.U("id", 1), harness.S("secret", hex.EncodeToString(raw[:]))}, nil, taker); !r.OK() {
		t.Fatalf("the taker could not claim the canonical lock: %v", r.Err)
	}
	t.Log("the asymmetric lock is refused at Lock; the canonical one settles on both legs")
}
